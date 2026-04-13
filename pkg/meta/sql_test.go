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
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-unit-test.db"), testConfig())
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestSQLiteOpenCacheStaleAfterRenameWithTrash(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-open-cache-stale.db"), testConfig())
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}

	format := testFormat()
	format.TrashDays = 1
	if err := m.Init(format, false); err != nil {
		t.Fatalf("init with trash: %v", err)
	}
	defer func() {
		if err := m.Init(testFormat(), false); err != nil {
			t.Fatalf("init default format: %v", err)
		}
	}()

	base := m.getBase()
	oldOpenCache := base.conf.OpenCache
	oldExpire := base.of.expire
	base.conf.OpenCache = 30 * time.Second
	base.of.expire = 30 * time.Second
	defer func() {
		base.conf.OpenCache = oldOpenCache
		base.of.expire = oldExpire
	}()

	ctx := Background()
	if err := m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	var dir, file1, file2 Ino
	var attr Attr
	if st := m.Mkdir(ctx, RootInode, "cache_repro_dir", 0755, 022, 0, &dir, &attr); st != 0 {
		t.Fatalf("mkdir cache_repro_dir: %s", st)
	}
	defer m.Rmdir(ctx, RootInode, "cache_repro_dir")

	if st := m.Create(ctx, dir, "cache_file1", 0644, 022, 0, &file1, &attr); st != 0 {
		t.Fatalf("create cache_file1: %s", st)
	}
	if st := m.Create(ctx, dir, "cache_file2", 0644, 022, 0, &file2, &attr); st != 0 {
		t.Fatalf("create cache_file2: %s", st)
	}

	var before Attr
	if st := m.GetAttr(ctx, file2, &before); st != 0 {
		t.Fatalf("getattr before rename: %s", st)
	}
	if before.Parent != dir {
		t.Fatalf("unexpected parent before rename, got %d, want %d", before.Parent, dir)
	}

	if st := m.Rename(ctx, dir, "cache_file1", dir, "cache_file2", 0, &file1, &attr); st != 0 {
		t.Fatalf("rename overwrite with trash: %s", st)
	}

	// GetAttr should still return the stale parent when open cache is not invalidated.
	var stale Attr
	if st := m.GetAttr(ctx, file2, &stale); st != 0 {
		t.Fatalf("getattr stale check: %s", st)
	}
	fmt.Printf("stale.Parent = %d\n", stale.Parent)
	if stale.Parent != dir {
		t.Fatalf("expected stale parent from open cache, got %d, want %d", stale.Parent, dir)
	}

	base.of.InvalidateChunk(file2, invalidateAttrOnly)

	var after Attr
	if st := m.GetAttr(ctx, file2, &after); st != 0 {
		t.Fatalf("getattr after cache invalidation: %s", st)
	}
	fmt.Printf("after.Parent = %d\n", after.Parent)
	if after.Parent <= TrashInode {
		t.Fatalf("expect parent moved to trash after invalidation, got %d", after.Parent)
	}
	if stale.Parent != after.Parent {
		t.Fatalf("expected stale parent to match before invalidation, got %d, want %d", stale.Parent, after.Parent)
	}
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

func TestGetCustomConfig(t *testing.T) {
	u := "mysql://root:password@tcp(localhost:3306)/db1?max_open_conns=100&notDefine=str"
	_, after, _ := strings.Cut(u, "?")
	query, err := url.ParseQuery(after)
	if err != nil {
		t.Fatalf("url parse query error: %s", err)
	}
	maxOpenConns, err := extractCustomConfig(&query, "max_open_conns", 1)
	if err != nil {
		t.Fatalf("getCustomConfig error: %s", err)
	}
	if maxOpenConns != 100 {
		t.Fatalf("getCustomConfig error: expect 100 but got %d", maxOpenConns)
	}
	if query.Has("max_open_conns") {
		t.Fatalf("getCustomConfig error: expect not found but found")
	}

	not, err := extractCustomConfig(&query, "notSetKey", "default")
	if err != nil {
		t.Fatalf("getCustomConfig error: %s", err)
	}
	if not != "default" {
		t.Fatalf("getCustomConfig error: expect default but got %s", not)
	}
	if !query.Has("notDefine") {
		t.Fatalf("getCustomConfig error: expect found but not")
	}

}
