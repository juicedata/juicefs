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
	"testing"
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
	c.scan([]byte("k"), func(key, value []byte) {
		keys = append(keys, key)
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
