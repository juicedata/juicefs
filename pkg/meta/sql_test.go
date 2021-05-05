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

package meta

import (
	"os"
	"testing"
)

func TestSQLClient(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db", &DBConfig{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMetaClient(t, m)
}

func TestConcurrentWriteSQL(t *testing.T) {
	os.Remove("test2.db")
	m, err := NewSQLMeta("sqlite3", "test2.db", &DBConfig{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testConcurrentWrite(t, m)
}

func TestCompactionSQL(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db", &DBConfig{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCompaction(t, m)
}

func TestTruncateAndDeleteSQL(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db", &DBConfig{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testTruncateAndDelete(t, m)
}

func TestCopyFileRangeSQL(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db", &DBConfig{})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCopyFileRange(t, m)
}

func TestCaseIncensiSQL(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db", &DBConfig{CaseInsensi: true})
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testCaseIncensi(t, m)
}
