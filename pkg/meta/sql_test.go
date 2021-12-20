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
	"testing"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", "/tmp/jfs-unit-test.db", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestMySQLClient(t *testing.T) {
	m, err := newSQLMeta("mysql", "root:@/dev", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "mysql" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClient(t *testing.T) {
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", &Config{MaxDeletes: 1})
	if err != nil || m.Name() != "postgres" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}
