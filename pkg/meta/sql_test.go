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
	"strings"
	"testing"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-unit-test.db"), &Config{})
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestMySQLClient(t *testing.T) {
	m, err := newSQLMeta("mysql", "root:@/dev", &Config{})
	if err != nil || m.Name() != "mysql" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClient(t *testing.T) {
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", &Config{})
	if err != nil || m.Name() != "postgres" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClientWithSearchPath(t *testing.T) {
	_, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable&search_path=juicefs,public", &Config{})
	if !strings.Contains(err.Error(), "currently, only one schema is supported in search_path") {
		t.Fatalf("TestPostgreSQLClientWithSearchPath error: %s", err)
	}
}
