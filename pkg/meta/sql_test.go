/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"io/ioutil"
	"log"
	"os"
	"testing"
)

func tempFile(t *testing.T) string {
	fp, err := ioutil.TempFile("", "test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %s", err)
	}
	if err = fp.Close(); err != nil {
		log.Fatalf("close temp file: %s", err)
	}
	return fp.Name()
}

func TestSQLClient(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMetaClient(t, m)
}

func TestMySQLClient(t *testing.T) {
	m, err := newSQLMeta("mysql", "root:@/dev", &Config{})
	if err != nil {
		t.Skipf("create meta: %s", err)
	}
	m.engine.DropTables(&setting{})
	m.engine.DropTables(&counter{})
	m.engine.DropTables(&node{})
	m.engine.DropTables(&edge{})
	m.engine.DropTables(&symlink{})
	m.engine.DropTables(&chunk{})
	m.engine.DropTables(&chunkRef{})
	m.engine.DropTables(&session{})
	m.engine.DropTables(&sustained{})
	m.engine.DropTables(&xattr{})
	m.engine.DropTables(&delfile{})
	m.engine.DropTables(&flock{})
	m.engine.DropTables(&plock{})

	testTruncateAndDelete(t, m)
	testMetaClient(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m)
	testCopyFileRange(t, m)
	m.conf.CaseInsensi = true
	testCaseIncensi(t, m)
}

func TestStickyBitSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testStickyBit(t, m)
}

func TestLocksSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testLocks(t, m)
}

func TestConcurrentWriteSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testConcurrentWrite(t, m)
}

func TestCompactionSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCompaction(t, m)
}

func TestTruncateAndDeleteSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testTruncateAndDelete(t, m)
}

func TestCopyFileRangeSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCopyFileRange(t, m)
}

func TestCaseIncensiSQL(t *testing.T) {
	tmp := tempFile(t)
	defer os.Remove(tmp)
	m, err := newSQLMeta("sqlite3", tmp, &Config{CaseInsensi: true})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCaseIncensi(t, m)
}
