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
	"os"
	"path"
	"strings"
	"testing"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-unit-test.db"), testConfig())
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestMySQLClient(t *testing.T) { //skip mutate
	m, err := newSQLMeta("mysql", "root:@/dev", testConfig())
	if err != nil || m.Name() != "mysql" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClient(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", testConfig())
	if err != nil || m.Name() != "postgres" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClientWithSearchPath(t *testing.T) { //skip mutate
	_, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable&search_path=juicefs,public", testConfig())
	if !strings.Contains(err.Error(), "currently, only one schema is supported in search_path") {
		t.Fatalf("TestPostgreSQLClientWithSearchPath error: %s", err)
	}
}

func TestRecoveryMysqlPwd(t *testing.T) { //skip mutate
	testCase := []struct {
		addr   string
		expect string
	}{
		// no password
		{"root@(localhost:3306)/db1",
			"root@(localhost:3306)/db1",
		},
		// no password
		{"root:@(localhost:3306)/db1",
			"root:@(localhost:3306)/db1",
		},

		{"root::@@(localhost:3306)/db1",
			"root::@@(localhost:3306)/db1",
		},

		{"root:@:@(localhost:3306)/db1",
			"root:@:@(localhost:3306)/db1",
		},

		// no special char
		{"root:password@(localhost:3306)/db1",
			"root:password@(localhost:3306)/db1",
		},

		// set from env @
		{"root:pass%40word@(localhost:3306)/db1",
			"root:pass@word@(localhost:3306)/db1",
		},

		// direct pass special char @
		{"root:pass@word@(localhost:3306)/db1",
			"root:pass@word@(localhost:3306)/db1",
		},

		// set from env |
		{"root:pass%7Cword@(localhost:3306)/db1",
			"root:pass|word@(localhost:3306)/db1",
		},

		// direct pass special char |
		{"root:pass|word@(localhost:3306)/db1",
			"root:pass|word@(localhost:3306)/db1",
		},

		// set from env :
		{"root:pass%3Aword@(localhost:3306)/db1",
			"root:pass:word@(localhost:3306)/db1",
		},

		// direct pass special char :
		{"root:pass:word@(localhost:3306)/db1",
			"root:pass:word@(localhost:3306)/db1",
		},
	}
	for _, tc := range testCase {
		if got := recoveryMysqlPwd(tc.addr); got != tc.expect {
			t.Fatalf("recoveryMysqlPwd error: expect %s but got %s", tc.expect, got)
		}
	}
}
