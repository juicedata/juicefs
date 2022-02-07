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
	"testing"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-unit-test.db"), &Config{MaxDeletes: 1})
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

func Test_setPasswordFromEnv(t *testing.T) {
	os.Setenv("META_PASSWORD", "dbPasswd")
	tests := []struct {
		args string
		want string
	}{
		//mysql
		{
			args: "root:password@(127.0.0.1:3306)/juicefs",
			want: "root:password@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "root:@(127.0.0.1:3306)/juicefs",
			want: "root:dbPasswd@(127.0.0.1:3306)/juicefs",
		},
		//postgres
		{
			args: "root:password@192.168.1.6:5432/juicefs",
			want: "root:password@192.168.1.6:5432/juicefs",
		},
		{
			args: "root:@192.168.1.6:5432/juicefs",
			want: "root:dbPasswd@192.168.1.6:5432/juicefs",
		},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := setsSPasswordFromEnv(tt.args); got != tt.want {
				t.Errorf("setsSPasswordFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
