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
	"time"
)

func tempFile(t *testing.T) string {
	fp, err := ioutil.TempFile("/tmp", "jfstest-*.db")
	if err != nil {
		t.Fatalf("create temp file: %s", err)
	}
	if err = fp.Close(); err != nil {
		log.Fatalf("close temp file: %s", err)
	}
	return fp.Name()
}

func TestSQLiteClient(t *testing.T) {
	tmp := tempFile(t)
	defer func() {
		if !t.Failed() {
			_ = os.Remove(tmp)
		}
	}()
	m, err := newSQLMeta("sqlite3", tmp, &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}

	testMetaClient(t, m)
	testTruncateAndDelete(t, m)
	testRemove(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	m.(*dbMeta).conf.CaseInsensi = true
	testCaseIncensi(t, m)
	m.(*dbMeta).conf.OpenCache = time.Second
	m.(*dbMeta).of.expire = time.Second
	testOpenCache(t, m)
	m.(*dbMeta).conf.ReadOnly = true
	testReadOnly(t, m)
}

func resetDB(m *dbMeta) {
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
}

func TestMySQLClient(t *testing.T) {
	m, err := newSQLMeta("mysql", "root:@/dev", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "mysql" {
		t.Fatalf("create meta: %s", err)
	}
	resetDB(m.(*dbMeta))

	testMetaClient(t, m)
	testTruncateAndDelete(t, m)
	testRemove(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	m.(*dbMeta).conf.CaseInsensi = true
	testCaseIncensi(t, m)
	m.(*dbMeta).conf.OpenCache = time.Second
	m.(*dbMeta).of.expire = time.Second
	testOpenCache(t, m)
	m.(*dbMeta).conf.ReadOnly = true
	testReadOnly(t, m)
}

func TestPostgreSQLClient(t *testing.T) {
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "postgres" {
		t.Fatalf("create meta: %s", err)
	}
	resetDB(m.(*dbMeta))

	testMetaClient(t, m)
	testTruncateAndDelete(t, m)
	testRemove(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	m.(*dbMeta).conf.CaseInsensi = true
	testCaseIncensi(t, m)
	m.(*dbMeta).conf.OpenCache = time.Second
	m.(*dbMeta).of.expire = time.Second
	testOpenCache(t, m)
	m.(*dbMeta).conf.ReadOnly = true
	testReadOnly(t, m)
}
