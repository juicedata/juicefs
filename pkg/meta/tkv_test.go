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

//nolint:errcheck
package meta

import (
	"bytes"
	"os"
	"testing"
)

func TestMemKVClient(t *testing.T) {
	_ = os.Remove(settingPath)
	m, err := newKVMeta("memkv", "jfs-unit-test", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "memkv" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestTiKVClient(t *testing.T) {
	m, err := newKVMeta("tikv", "127.0.0.1:2379/jfs-unit-test", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "tikv" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestBadgerClient(t *testing.T) {
	m, err := newKVMeta("badger", "badger", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "badger" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func testTKV(t *testing.T, c tkvClient) {
	txn := func(f func(kt kvTxn)) {
		if err := c.txn(func(kt kvTxn) error {
			f(kt)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// basic
	err := c.reset(nil)
	if err != nil {
		t.Fatalf("reset: %s", err)
	}
	var hasKey bool
	txn(func(kt kvTxn) { hasKey = kt.exist(nil) })
	if hasKey {
		t.Fatalf("has key after reset")
	}
	k := []byte("k")
	v := []byte("value")

	txn(func(kt kvTxn) {
		kt.set(k, v)
		kt.append(k, v)
	})
	var r []byte
	txn(func(kt kvTxn) { r = kt.get(k) })
	if !bytes.Equal(r, []byte("valuevalue")) {
		t.Fatalf("expect 'valuevalue', but got %v", string(r))
	}
	txn(func(kt kvTxn) {
		kt.set([]byte("k2"), v)
		kt.set([]byte("v"), k)
	})
	var ks [][]byte
	txn(func(kt kvTxn) { ks = kt.gets([]byte("k1"), []byte("k2")) })
	if ks[0] != nil || string(ks[1]) != "value" {
		t.Fatalf("gets k1,k2: %+v", ks)
	}

	var keys [][]byte
	txn(func(kt kvTxn) {
		kt.scan([]byte("k"), func(key, value []byte) {
			keys = append(keys, key)
		})
	})
	if len(keys) != 2 || string(keys[0]) != "k" || string(keys[1]) != "k2" {
		t.Fatalf("keys: %+v", keys)
	}
	txn(func(kt kvTxn) { keys = kt.scanKeys(nil) })
	if len(keys) != 3 || string(keys[0]) != "k" || string(keys[1]) != "k2" || string(keys[2]) != "v" {
		t.Fatalf("keys: %+v", keys)
	}
	var values map[string][]byte
	txn(func(kt kvTxn) { values = kt.scanValues([]byte("k"), -1, func(k, v []byte) bool { return len(v) == 5 }) })
	if len(values) != 1 || string(values["k2"]) != "value" {
		t.Fatalf("scan values: %+v", values)
	}
	txn(func(kt kvTxn) { values = kt.scanRange([]byte("k2"), []byte("v")) })
	if len(values) != 1 || string(values["k2"]) != "value" {
		t.Fatalf("scanRange: %+v", values)
	}

	// exists
	txn(func(kt kvTxn) { hasKey = kt.exist([]byte("k")) })
	if !hasKey {
		t.Fatalf("has key k*")
	}
	txn(func(kt kvTxn) { kt.dels(keys...) })
	txn(func(kt kvTxn) { r = kt.get(k) })
	if r != nil {
		t.Fatalf("expect nil, but got %v", string(r))
	}
	txn(func(kt kvTxn) { keys = kt.scanKeys(nil) })
	if len(keys) != 0 {
		t.Fatalf("no keys: %+v", keys)
	}
	txn(func(kt kvTxn) { hasKey = kt.exist(nil) })
	if hasKey {
		t.Fatalf("has not keys")
	}

	// counters
	var count int64
	c.txn(func(tx kvTxn) error {
		count = tx.incrBy([]byte("counter"), -1)
		return nil
	})
	if count != -1 {
		t.Fatalf("counter should be -1, but got %d", count)
	}
	c.txn(func(tx kvTxn) error {
		count = tx.incrBy([]byte("counter"), 0)
		return nil
	})
	if count != -1 {
		t.Fatalf("counter should be -1, but got %d", count)
	}
	c.txn(func(tx kvTxn) error {
		count = tx.incrBy([]byte("counter"), 2)
		return nil
	})
	if count != 1 {
		t.Fatalf("counter should be 1, but got %d", count)
	}
}

func TestBadgerKV(t *testing.T) {
	c, err := newBadgerClient("test_badger")
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
