/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

//nolint:errcheck
package meta

import (
	"testing"
)

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

func TestTKVClient(t *testing.T) {
	m, err := newKVMeta("memkv", "test/jfs", &Config{})
	// m, err := newKVMeta("tikv", "127.0.0.1:2379/jfs", &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}

	testTruncateAndDelete(t, m)
	testMetaClient(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	m.(*kvMeta).conf.CaseInsensi = true
	testCaseIncensi(t, m)
}
