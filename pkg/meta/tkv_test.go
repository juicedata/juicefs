/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

//mutate:disable
//nolint:errcheck
package meta

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"syscall"
	"testing"

	"github.com/dgraph-io/badger/v4"
	aclAPI "github.com/juicedata/juicefs/pkg/acl"
)

type aclRollbackClient struct {
	tkvClient
	childKey        []byte
	err             error
	injected        bool
	onRollback      func(*kvTxn)
	retryInternally bool
}

func (c *aclRollbackClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) error {
	err := c.tkvClient.txn(ctx, func(tx *kvTxn) error {
		if err := f(tx); err != nil {
			return err
		}
		// Only interrupt the outer creation transaction, after it stages the child.
		if !c.injected && tx.get(c.childKey) != nil {
			c.injected = true
			if c.onRollback != nil {
				c.onRollback(tx)
			}
			return c.err
		}
		return nil
	}, retry)
	if c.retryInternally && err == c.err {
		return c.tkvClient.txn(ctx, f, retry)
	}
	return err
}

func TestKVTxnOnCommit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bodyError   error
		commitError error
		internal    bool
		attempts    int
		wantError   error
	}{
		{name: "commit", attempts: 1},
		{name: "body_error", bodyError: syscall.EIO, attempts: 1, wantError: syscall.EIO},
		{name: "commit_error", commitError: syscall.EIO, attempts: 1, wantError: syscall.EIO},
		{name: "retry", commitError: badger.ErrConflict, attempts: 2},
		{name: "driver_retry", commitError: badger.ErrConflict, internal: true, attempts: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := newKVMeta("badger", t.TempDir(), testConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := raw.(*kvMeta)
			m.client = withPrefix(m.client, []byte("callbacks/"))
			t.Cleanup(func() { _ = m.Shutdown() })
			key := []byte("committed")
			if tc.commitError != nil {
				m.client = &aclRollbackClient{tkvClient: m.client, childKey: key, err: tc.commitError, retryInternally: tc.internal}
			}
			attempts := 0
			var called []int
			err = m.txn(Background(), func(tx *kvTxn) error {
				attempts++
				attempt := attempts
				tx.set(key, []byte{byte(attempt)})
				tx.onCommit(func() {
					val, err := m.get(key)
					if err != nil || !bytes.Equal(val, []byte{byte(attempt)}) {
						t.Errorf("callback ran before commit: value %x, error %v", val, err)
					}
					called = append(called, attempt*10+1)
				})
				tx.onCommit(func() { called = append(called, attempt*10+2) })
				if len(called) != 0 {
					t.Fatal("callback ran during transaction or for a failed attempt")
				}
				return tc.bodyError
			})
			if err != tc.wantError || attempts != tc.attempts {
				t.Fatalf("transaction: %v, attempts: %d; want %v, %d", err, attempts, tc.wantError, tc.attempts)
			}
			if tc.wantError != nil {
				if len(called) != 0 {
					t.Fatalf("callbacks ran after rollback: %v", called)
				}
			} else if len(called) != 2 || called[0] != attempts*10+1 || called[1] != attempts*10+2 {
				t.Fatalf("callbacks should run once in registration order for attempt %d: %v", attempts, called)
			}
		})
	}
}

func TestKVACLTransactionRollback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retry    bool
		internal bool
	}{{"retry", true, false}, {"driver_retry", true, true}, {"abort", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := newKVMeta("badger", t.TempDir(), testConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := raw.(*kvMeta)
			t.Cleanup(func() { _ = m.Shutdown() })
			format := testFormat()
			format.EnableACL = true
			if err := m.Init(format, true); err != nil {
				t.Fatal(err)
			}
			ctx := Background()
			var parent, child Ino
			if st := m.Mkdir(ctx, RootInode, "parent", 0770, 0, 0, &parent, nil); st != 0 {
				t.Fatal(st)
			}
			rule := &aclAPI.Rule{Owner: 7, Group: 5, Mask: 5, Other: 0, NamedUsers: []aclAPI.Entry{{Id: 1001, Perm: 4}}}
			if st := m.SetFacl(ctx, parent, aclAPI.TypeDefault, rule); st != 0 {
				t.Fatal(st)
			}
			failure := error(syscall.EIO)
			if tc.retry {
				failure = badger.ErrConflict
			}
			client := &aclRollbackClient{tkvClient: m.client, childKey: m.entryKey(parent, "child"), err: failure, retryInternally: tc.internal}
			var otherParent, otherChild Ino
			if !tc.retry {
				if st := m.Mkdir(ctx, RootInode, "other", 0770, 0, 0, &otherParent, nil); st != 0 {
					t.Fatal(st)
				}
				if st := m.SetFacl(ctx, otherParent, aclAPI.TypeDefault, rule); st != 0 {
					t.Fatal(st)
				}
			}
			var abortedACL uint32
			client.onRollback = func(tx *kvTxn) {
				_, inode := m.parseEntry(tx.get(client.childKey))
				var attr Attr
				m.parseAttr(tx.get(m.inodeKey(inode)), &attr)
				abortedACL = attr.AccessACL
				// Try the same ACL in another transaction before the first one rolls back.
				if !tc.retry {
					if st := m.Create(ctx, otherParent, "child", 0666, 022, 0, &otherChild, nil); st != 0 {
						t.Fatalf("concurrent create: %s", st)
					}
				}
			}
			m.client = client
			st := m.Create(ctx, parent, "child", 0666, 022, 0, &child, nil)
			if !client.injected {
				t.Fatal("creation transaction was not interrupted")
			}
			if !tc.retry {
				if st != syscall.EIO {
					t.Fatalf("first create: %s", st)
				}
				st = m.Create(ctx, parent, "child", 0666, 022, 0, &child, nil)
			}
			if st != 0 {
				t.Fatalf("create: %s", st)
			}
			if val, err := m.get(m.aclKey(abortedACL)); err != nil || val != nil {
				t.Fatalf("aborted ACL persisted: %x, error: %v", val, err)
			}
			if m.aclCache.Get(abortedACL) != nil {
				t.Fatal("aborted ACL was published to the cache")
			}
			children := []Ino{child}
			if !tc.retry {
				children = append(children, otherChild)
			}
			for _, inode := range children {
				m.aclCache.Clear()
				var attr Attr
				if st := m.GetAttr(ctx, inode, &attr); st != 0 || attr.AccessACL == 0 {
					t.Fatalf("getattr: %s, ACL: %d", st, attr.AccessACL)
				}
				var got aclAPI.Rule
				if st := m.GetFacl(ctx, inode, aclAPI.TypeAccess, &got); st != 0 {
					t.Fatalf("getfacl after rollback and cache clear: %s", st)
				}
				if want := rule.ChildAccessACL(0666); !got.IsEqual(want) {
					t.Fatalf("ACL: %s, want %s", &got, want)
				}
			}
		})
	}
}

func TestKVACLTransactionCache(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			raw, err := newKVMeta("badger", t.TempDir(), testConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := raw.(*kvMeta)
			m.client = withPrefix(m.client, []byte("acl-test/"))
			t.Cleanup(func() { _ = m.Shutdown() })
			if err := m.Init(testFormat(), true); err != nil {
				t.Fatal(err)
			}
			ctx := Background()
			rule := &aclAPI.Rule{Owner: 7, Group: 5, Mask: 5, Other: 0, NamedUsers: []aclAPI.Entry{{Id: 1001, Perm: 4}}}
			var id uint32
			err = m.txn(ctx, func(tx *kvTxn) error {
				var err error
				if id, err = m.insertACL(tx, rule); err != nil {
					return err
				}
				if val, err := m.get(m.aclKey(id)); err != nil || val != nil {
					t.Fatalf("uncommitted ACL persisted: %x, error: %v", val, err)
				}
				got, err := m.getACL(tx, id)
				if err != nil || !got.IsEqual(rule) {
					t.Fatalf("read own ACL: %v, error: %v", got, err)
				}
				// A higher ID commits first, making this transaction's ID a cache gap.
				other := rule.Dup()
				other.Owner = 6
				if err := m.txn(ctx, func(tx *kvTxn) error {
					_, err := m.insertACL(tx, other)
					return err
				}); err != nil {
					return err
				}
				// Neither reading our own writes nor another snapshot may publish this ACL.
				if err := m.tryLoadMissACLs(tx); err != nil {
					return err
				}
				if err := m.txn(ctx, m.tryLoadMissACLs); err != nil {
					return err
				}
				if m.aclCache.Get(id) != nil || m.aclCache.GetId(rule) != aclAPI.None {
					t.Fatal("uncommitted ACL is visible to other transactions")
				}
				if !commit {
					return syscall.EIO
				}
				return nil
			})
			if commit && err != nil || !commit && err != syscall.EIO {
				t.Fatalf("transaction: %v", err)
			}
			cached := m.aclCache.Get(id)
			val, err := m.get(m.aclKey(id))
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				if cached == nil || !cached.IsEqual(rule) || !bytes.Equal(val, rule.Encode()) {
					t.Fatalf("committed ACL: cached %v, stored %x", cached, val)
				}
			} else if cached != nil || val != nil {
				t.Fatalf("aborted ACL: cached %v, stored %x", cached, val)
			}
		})
	}
}

func TestMemKVClient(t *testing.T) {
	_ = os.Remove(settingPath)
	m, err := newKVMeta("memkv", "jfs-unit-test", testConfig())
	if err != nil || m.Name() != "memkv" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestTiKVClient(t *testing.T) { //skip mutate
	m, err := newKVMeta("tikv", "127.0.0.1:2379/jfs-unit-test", testConfig())
	if err != nil || m.Name() != "tikv" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestBadgerClient(t *testing.T) {
	m, err := newKVMeta("badger", t.TempDir(), testConfig())
	if err != nil || m.Name() != "badger" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestEtcdClient(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	m, err := newKVMeta("etcd", os.Getenv("ETCD_ADDR"), testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func testTKV(t *testing.T, c tkvClient) {
	txn := func(f func(kt *kvTxn)) {
		if err := c.txn(Background(), func(kt *kvTxn) error {
			f(kt)
			return nil
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	// basic
	err := c.reset(nil)
	if err != nil {
		t.Fatalf("reset: %s", err)
	}
	var hasKey bool
	txn(func(kt *kvTxn) { hasKey = kt.exist(nil) })
	if hasKey {
		t.Fatalf("has key after reset")
	}
	k := []byte("k")
	v := []byte("value")

	txn(func(kt *kvTxn) {
		kt.set(k, v)
		kt.append(k, v)
	})
	var r []byte
	txn(func(kt *kvTxn) { r = kt.get(k) })
	if !bytes.Equal(r, []byte("valuevalue")) {
		t.Fatalf("expect 'valuevalue', but got %v", string(r))
	}
	txn(func(kt *kvTxn) {
		kt.set([]byte("k2"), v)
		kt.set([]byte("v"), k)
	})
	var ks [][]byte
	txn(func(kt *kvTxn) { ks = kt.gets([]byte("k1"), []byte("k2")) })
	if ks[0] != nil || string(ks[1]) != "value" {
		t.Fatalf("gets k1,k2: %+v != %+v", ks, [][]byte{nil, []byte("value")})
	}

	var keys [][]byte
	c.scan([]byte("k"), func(key, value []byte) bool {
		keys = append(keys, key)
		return true
	})
	if len(keys) != 2 || string(keys[0]) != "k" || string(keys[1]) != "k2" {
		t.Fatalf("keys: %+v", keys)
	}
	keys = keys[:0]
	txn(func(kt *kvTxn) {
		kt.scan([]byte("a"), []byte("z"), true, func(k, v []byte) bool {
			if len(k) == 1 {
				keys = append(keys, k)
			}
			return true
		})
	})
	if len(keys) != 2 || string(keys[0]) != "k" || string(keys[1]) != "v" {
		t.Fatalf("keys: %+v", keys)
	}
	keys = keys[:0]
	txn(func(kt *kvTxn) {
		kt.scan([]byte("k"), []byte("l"), true, func(k, v []byte) bool {
			keys = append(keys, k)
			return true
		})
	})
	if len(keys) != 2 || string(keys[0]) != "k" || string(keys[1]) != "k2" {
		t.Fatalf("keys: %+v", keys)
	}
	keys = keys[:0]
	txn(func(kt *kvTxn) {
		kt.scan([]byte("a"), []byte("z"), true, func(k, v []byte) bool {
			keys = append(keys, k)
			return true
		})
	})
	if len(keys) != 3 || string(keys[0]) != "k" || string(keys[1]) != "k2" || string(keys[2]) != "v" {
		t.Fatalf("keys: %+v", keys)
	}
	values := make(map[string][]byte)
	txn(func(kt *kvTxn) {
		kt.scan([]byte("k"), nextKey([]byte("k")), false, func(k, v []byte) bool {
			if len(v) == 5 {
				values[string(k)] = v
			}
			return true
		})
	})
	if len(values) != 1 || string(values["k2"]) != "value" {
		t.Fatalf("scan values: %+v", values)
	}
	values = make(map[string][]byte)
	txn(func(kt *kvTxn) {
		kt.scan([]byte("k2"), []byte("v"),
			false, func(k, v []byte) bool {
				values[string(k)] = v
				return true
			})
	})
	if len(values) != 1 || string(values["k2"]) != "value" {
		t.Fatalf("scanRange: %+v", values)
	}

	// exists
	txn(func(kt *kvTxn) { hasKey = kt.exist([]byte("k")) })
	if !hasKey {
		t.Fatalf("has key k*")
	}
	txn(func(kt *kvTxn) {
		for _, key := range keys {
			kt.delete(key)
		}
	})
	txn(func(kt *kvTxn) { r = kt.get(k) })
	if r != nil {
		t.Fatalf("expect nil, but got %v", string(r))
	}
	keys = keys[:0]
	txn(func(kt *kvTxn) {
		kt.scan([]byte("a"), []byte("z"), true, func(k, v []byte) bool {
			keys = append(keys, k)
			return true
		})
	})
	if len(keys) != 0 {
		t.Fatalf("no keys: %+v", keys)
	}
	txn(func(kt *kvTxn) { hasKey = kt.exist(nil) })
	if hasKey {
		t.Fatalf("has not keys")
	}

	// counters
	var count int64
	c.txn(Background(), func(tx *kvTxn) error {
		count = tx.incrBy([]byte("counter"), -1)
		return nil
	}, 0)
	if count != -1 {
		t.Fatalf("counter should be -1, but got %d", count)
	}
	c.txn(Background(), func(tx *kvTxn) error {
		count = tx.incrBy([]byte("counter"), 0)
		return nil
	}, 0)
	if count != -1 {
		t.Fatalf("counter should be -1, but got %d", count)
	}
	c.txn(Background(), func(tx *kvTxn) error {
		count = tx.incrBy([]byte("counter"), 2)
		return nil
	}, 0)
	if count != 1 {
		t.Fatalf("counter should be 1, but got %d", count)
	}

	// key with zeros
	k = []byte("k\x001")
	txn(func(kt *kvTxn) {
		kt.set(k, v)
	})
	var v2 []byte
	txn(func(kt *kvTxn) {
		v2 = kt.get(k)
	})
	if !bytes.Equal(v2, v) {
		t.Fatalf("expect %v but got %v", v, v2)
	}

	// scan many key-value pairs
	keys = make([][]byte, 0, 100000)
	for i := 0; i < 1000; i++ {
		txn(func(kt *kvTxn) {
			for j := 0; j < 100; j++ {
				k := []byte(fmt.Sprintf("Key_%d_%d", i, j))
				v := []byte(fmt.Sprintf("Value_%d_%d", i, j))
				kt.set(k, v)
				keys = append(keys, k)
			}
		})
	}
	kvs := make([][]byte, 0, 200000)
	txn(func(kt *kvTxn) {
		kt.scan([]byte("A"), []byte("Z"), false, func(k, v []byte) bool {
			kvs = append(kvs, k, v)
			return true
		})
	})
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })
	for i, k := range keys {
		if !bytes.Equal(k, kvs[i*2]) || !bytes.Equal([]byte(fmt.Sprintf("Value%s", k[3:])), kvs[i*2+1]) {
			t.Fatalf("expect %s but got %s, %s", k, keys[i*2], keys[i*2+1])
		}
	}
}

func TestBadgerKV(t *testing.T) {
	c, err := newBadgerClient("test_badger")
	if err != nil {
		t.Fatal(err)
	}
	testTKV(t, c)
}

func TestEtcd(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	c, err := newEtcdClient(fmt.Sprintf("%s/jfs", os.Getenv("ETCD_ADDR")))
	if err != nil {
		t.Fatal(err)
	}
	testTKV(t, c)
}

func TestMemKV(t *testing.T) {
	c, _ := newTkvClient("memkv", "")
	c = withPrefix(c, []byte("jfs"))
	testTKV(t, c)
}

func TestBadgerScanKeysOnlyNilValues(t *testing.T) {
	c, err := newBadgerClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	if err := c.txn(Background(), func(kt *kvTxn) error {
		kt.set([]byte("key1"), []byte("value1"))
		kt.set([]byte("key2"), []byte("value2"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}

	var scanned int
	if err := c.txn(Background(), func(kt *kvTxn) error {
		kt.scan([]byte("key"), nextKey([]byte("key")), true, func(k, v []byte) bool {
			if v != nil {
				t.Errorf("keysOnly=true: expected nil value for key %q, got %q", k, v)
			}
			scanned++
			return true
		})
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if scanned != 2 {
		t.Fatalf("expected 2 keys scanned, got %d", scanned)
	}
}

func TestBadgerSimpleTxnReadOnly(t *testing.T) {
	c, err := newBadgerClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	if err := c.txn(Background(), func(kt *kvTxn) error {
		kt.set([]byte("ro_key"), []byte("ro_value"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}

	var got []byte
	var scanned int
	if err := c.simpleTxn(Background(), func(kt *kvTxn) error {
		got = kt.get([]byte("ro_key"))
		kt.scan([]byte("ro_"), nextKey([]byte("ro_")), false, func(k, v []byte) bool {
			scanned++
			return true
		})
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if string(got) != "ro_value" || scanned != 1 {
		t.Fatalf("simpleTxn read: got %q, scanned %d", got, scanned)
	}

	err = c.simpleTxn(Background(), func(kt *kvTxn) error {
		kt.set([]byte("ro_key2"), []byte("v"))
		return nil
	}, 0)
	if err != badger.ErrReadOnlyTxn {
		t.Fatalf("expected ErrReadOnlyTxn, got %v", err)
	}
	var leaked []byte
	if err := c.simpleTxn(Background(), func(kt *kvTxn) error {
		leaked = kt.get([]byte("ro_key2"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if leaked != nil {
		t.Fatalf("write in read-only txn must not be committed, got %q", leaked)
	}
}

func TestBadgerSyncOption(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{name: "default false", addr: root + "/default", expected: false},
		{name: "sync true", addr: root + "/true?sync=true", expected: true},
		{name: "sync false", addr: root + "/false?sync=false", expected: false},
		{name: "sync invalid 1", addr: root + "/invalid1?sync=1", expected: false},
		{name: "sync invalid 0", addr: root + "/invalid0?sync=0", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := newBadgerClient(tt.addr)
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()

			bc, ok := client.(*badgerClient)
			if !ok {
				t.Fatalf("unexpected client type %T", client)
			}
			if got := bc.client.Opts().SyncWrites; got != tt.expected {
				t.Fatalf("sync option mismatch: got %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestBadgerDeleteTxnTooBig(t *testing.T) {
	dir := t.TempDir()

	opt := badger.DefaultOptions(dir)
	opt.Logger = nil
	opt.MetricsEnabled = false
	opt.MemTableSize = 1 << 20
	opt.ValueThreshold = 1 << 10
	db, err := badger.Open(opt)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const numKeys = 5000
	wb := db.NewWriteBatch()
	for i := 0; i < numKeys; i++ {
		if err := wb.Set([]byte(fmt.Sprintf("txbig_%05d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if err := wb.Flush(); err != nil {
		t.Fatal(err)
	}

	var keys [][]byte
	rtx := db.NewTransaction(false)
	it := rtx.NewIterator(badger.IteratorOptions{Prefix: []byte("txbig_"), PrefetchValues: false})
	for it.Rewind(); it.Valid(); it.Next() {
		keys = append(keys, it.Item().KeyCopy(nil))
	}
	it.Close()
	rtx.Discard()

	client := &badgerClient{client: db, done: make(chan struct{})}

	err = client.txn(Background(), func(kt *kvTxn) error {
		for _, key := range keys {
			kt.delete(key)
		}
		return nil
	}, 0)

	if err != badger.ErrTxnTooBig {
		t.Fatalf("expected ErrTxnTooBig, got %v", err)
	}
}
