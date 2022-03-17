/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"path"
	"testing"
)

func TestSQLiteClient(t *testing.T) {
	m := NewClient("sqlite3://"+path.Join(t.TempDir(), "jfs-unit-test.db"), nil)
	if m.Name() != "sqlite3" {
		t.Fatalf("Invalid meta name: %s", m.Name())
	}
	testMeta(t, m)
}

func TestMySQLClient(t *testing.T) {
	m := NewClient("mysql://root:@/dev", nil)
	if m.Name() != "mysql" {
		t.Fatalf("Invalid meta name: %s", m.Name())
	}
	testMeta(t, m)
}

func TestPostgreSQLClient(t *testing.T) {
	m := NewClient("postgres://localhost:5432/test?sslmode=disable", nil)
	if m.Name() != "postgres" {
		t.Fatalf("Invalid meta name: %s", m.Name())
	}
	testMeta(t, m)
}
