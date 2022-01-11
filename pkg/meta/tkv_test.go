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

func TestMemKV(t *testing.T) {
	c, _ := newTkvClient("memkv", "")
	c = withPrefix(c, []byte("jfs"))
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
		count = tx.incrBy([]byte("counter"), 1)
		return nil
	})
	if count != 0 {
		t.Fatalf("counter should be 0, but got %d", count)
	}
}
