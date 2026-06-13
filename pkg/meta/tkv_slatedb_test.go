//go:build slatedb
// +build slatedb

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// tests use durability=memory: with the default (remote) every commit waits
// for the next WAL flush (~100ms), which is too slow for thousands of txns
func TestSlateDBClient(t *testing.T) { //skip mutate
	m, err := newKVMeta("slatedb", "memory?durability=memory", testConfig())
	if err != nil || m.Name() != "slatedb" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestSlateDB(t *testing.T) { //skip mutate
	c, err := newSlateDBClient("memory?durability=memory")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	testTKV(t, c)
}

// exercises the durable commit path (default durability=remote) and
// persistence across reopen on a file:// store
func TestSlateDBFileStore(t *testing.T) { //skip mutate
	addr := t.TempDir()
	c, err := newSlateDBClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.txn(Background(), func(tx *kvTxn) error {
		tx.set([]byte("persist"), []byte("value"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err = c.close(); err != nil {
		t.Fatal(err)
	}

	c, err = newSlateDBClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	var got []byte
	if err = c.simpleTxn(Background(), func(tx *kvTxn) error {
		got = tx.get([]byte("persist"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("value")) {
		t.Fatalf("expect persisted value, got %q", got)
	}
}

func TestSlateDBSimpleTxnReadOnly(t *testing.T) { //skip mutate
	c, err := newSlateDBClient("memory?durability=memory")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	if err := c.txn(Background(), func(tx *kvTxn) error {
		tx.set([]byte("ro_key"), []byte("ro_value"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}

	var got []byte
	var scanned int
	if err := c.simpleTxn(Background(), func(tx *kvTxn) error {
		got = tx.get([]byte("ro_key"))
		tx.scan([]byte("ro_"), nextKey([]byte("ro_")), false, func(k, v []byte) bool {
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

	err = c.simpleTxn(Background(), func(tx *kvTxn) error {
		tx.set([]byte("ro_key2"), []byte("v"))
		return nil
	}, 0)
	if !errors.Is(err, errSlateDBReadOnly) {
		t.Fatalf("expected errSlateDBReadOnly, got %v", err)
	}
	var leaked []byte
	if err := c.simpleTxn(Background(), func(tx *kvTxn) error {
		leaked = tx.get([]byte("ro_key2"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if leaked != nil {
		t.Fatalf("write in read-only txn must not be committed, got %q", leaked)
	}
}

func TestSlateDBSettings(t *testing.T) { //skip mutate
	c, err := newSlateDBClient(t.TempDir() + `?durability=memory&settings={"flush_interval":"5ms","l0_sst_size_bytes":1048576}`)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err = c.txn(Background(), func(tx *kvTxn) error {
		tx.set([]byte("k"), []byte("v"))
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	// note: SlateDB ignores unknown settings keys, so typos are not detected

	if _, err = newSlateDBClient(t.TempDir() + `?settings={"flush_interval":}`); err == nil {
		t.Fatal("expected error for malformed settings JSON")
	}
}

func TestSlateDBConflict(t *testing.T) { //skip mutate
	c, err := newSlateDBClient("memory?durability=memory")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	var retries int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				for {
					err := c.txn(Background(), func(tx *kvTxn) error {
						tx.incrBy([]byte("conflict_counter"), 1)
						return nil
					}, 0)
					if err == nil {
						break
					}
					if !c.shouldRetry(err) {
						t.Errorf("unexpected error: %v", err)
						return
					}
					atomic.AddInt64(&retries, 1)
				}
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}

	var count int64
	if err := c.txn(Background(), func(tx *kvTxn) error {
		count = tx.incrBy([]byte("conflict_counter"), 0)
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Fatalf("counter should be 200, but got %d (retries: %d)", count, retries)
	}
	t.Logf("incremented counter to %d with %d conflict retries", count, retries)
}
