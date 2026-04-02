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
	"fmt"
	"os"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
)

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
	m, err := newKVMeta("badger", "badger", testConfig())
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

func TestMemKVBatchClone(t *testing.T) {
	_ = os.Remove(settingPath)
	m, err := newKVMeta("memkv", "jfs-unit-test", testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	m.OnMsg(DeleteSlice, func(args ...interface{}) error { return nil })
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	ctx := Background()
	kvm := m.(*kvMeta)

	// --- setup: source directory ---
	var srcDir Ino
	if st := m.Mkdir(ctx, RootInode, "bcSrc", 0755, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir bcSrc: %s", st)
	}

	// file with a slice
	var fileA Ino
	if st := m.Mknod(ctx, srcDir, "fileA", TypeFile, 0644, 022, 0, "", &fileA, nil); st != 0 {
		t.Fatalf("mknod fileA: %s", st)
	}
	var sliceId uint64
	if st := m.NewSlice(ctx, &sliceId); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	if st := m.Write(ctx, fileA, 0, 0, Slice{Id: sliceId, Size: 512, Off: 0, Len: 512}, time.Now()); st != 0 {
		t.Fatalf("write fileA: %s", st)
	}

	// file with hardlink (nlink > 1)
	var fileB Ino
	if st := m.Mknod(ctx, srcDir, "fileB", TypeFile, 0644, 022, 0, "", &fileB, nil); st != 0 {
		t.Fatalf("mknod fileB: %s", st)
	}
	var fileBAttr Attr
	if st := m.Link(ctx, fileB, srcDir, "fileBLink", &fileBAttr); st != 0 {
		t.Fatalf("link fileB: %s", st)
	}

	// symlink
	var sym Ino
	if st := m.Symlink(ctx, srcDir, "sym", "/target", &sym, nil); st != 0 {
		t.Fatalf("symlink: %s", st)
	}

	entries := []*Entry{
		{Inode: fileA, Name: []byte("fileA")},
		{Inode: fileB, Name: []byte("fileB")},
		{Inode: sym, Name: []byte("sym")},
	}

	// --- helper: read slice ref count ---
	getSliceRef := func(id uint64, size uint32) int64 {
		v, err := kvm.get(kvm.sliceKey(id, size))
		if err != nil || v == nil {
			return 0
		}
		return parseCounter(v)
	}

	refBefore := getSliceRef(sliceId, 512)

	// --- test 1: basic batch clone ---
	var dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "bcDst", 0755, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir bcDst: %s", st)
	}
	var count uint64
	if st := m.getBase().BatchClone(ctx, srcDir, dstDir, entries, 0, 022, &count); st != 0 {
		t.Fatalf("BatchClone: %s", st)
	}
	if count != uint64(len(entries)) {
		t.Fatalf("count: got %d, want %d", count, len(entries))
	}

	// verify slice ref incremented
	refAfter := getSliceRef(sliceId, 512)
	if refAfter != refBefore+1 {
		t.Fatalf("slice ref: got %d, want %d", refAfter, refBefore+1)
	}

	// verify symlink target copied correctly
	var dstEntries []*Entry
	if st := m.Readdir(ctx, dstDir, 1, &dstEntries); st != 0 {
		t.Fatalf("readdir dst: %s", st)
	}
	dstMap := make(map[string]*Entry)
	for _, e := range dstEntries {
		name := string(e.Name)
		if name != "." && name != ".." {
			dstMap[name] = e
		}
	}
	if symE, ok := dstMap["sym"]; !ok {
		t.Fatal("sym not cloned")
	} else {
		var target []byte
		if st := m.ReadLink(ctx, symE.Inode, &target); st != 0 {
			t.Fatalf("readlink cloned sym: %s", st)
		}
		if string(target) != "/target" {
			t.Fatalf("symlink target: got %q, want /target", target)
		}
	}

	// verify hardlink clone has nlink reset to 1
	if fileBE, ok := dstMap["fileB"]; !ok {
		t.Fatal("fileB not cloned")
	} else {
		if fileBE.Attr.Nlink != 1 {
			t.Fatalf("hardlink nlink: got %d, want 1", fileBE.Attr.Nlink)
		}
	}

	// verify cloned fileA has readable chunks
	if fileAE, ok := dstMap["fileA"]; !ok {
		t.Fatal("fileA not cloned")
	} else {
		var slices []Slice
		if st := m.Read(ctx, fileAE.Inode, 0, &slices); st != 0 {
			t.Fatalf("read cloned fileA: %s", st)
		}
		if len(slices) == 0 {
			t.Fatal("cloned fileA has no slices")
		}
	}

	// --- test 2: two entries sharing the same source inode counts ref once ---
	var dstDir2 Ino
	if st := m.Mkdir(ctx, RootInode, "bcDst2", 0755, 022, 0, &dstDir2, nil); st != 0 {
		t.Fatalf("mkdir bcDst2: %s", st)
	}
	sharedEntries := []*Entry{
		{Inode: fileA, Name: []byte("copy1")},
		{Inode: fileA, Name: []byte("copy2")},
	}
	refBefore2 := getSliceRef(sliceId, 512)
	if st := m.getBase().BatchClone(ctx, srcDir, dstDir2, sharedEntries, 0, 022, &count); st != 0 {
		t.Fatalf("BatchClone shared: %s", st)
	}
	// two copies of fileA mean ref should increase by 2
	refAfter2 := getSliceRef(sliceId, 512)
	if refAfter2 != refBefore2+2 {
		t.Fatalf("slice ref for 2 copies: got %d, want %d", refAfter2, refBefore2+2)
	}

	// --- test 3: directory entry rejected with EINVAL ---
	var subDir Ino
	if st := m.Mkdir(ctx, srcDir, "subDir", 0755, 022, 0, &subDir, nil); st != 0 {
		t.Fatalf("mkdir subDir: %s", st)
	}
	var dstDir3 Ino
	if st := m.Mkdir(ctx, RootInode, "bcDst3", 0755, 022, 0, &dstDir3, nil); st != 0 {
		t.Fatalf("mkdir bcDst3: %s", st)
	}
	dirEntries := []*Entry{{Inode: subDir, Name: []byte("subDir")}}
	if st := m.getBase().BatchClone(ctx, srcDir, dstDir3, dirEntries, 0, 022, &count); st != syscall.EINVAL {
		t.Fatalf("BatchClone dir entry: got %s, want EINVAL", st)
	}
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
