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

func TestSQLiteBatchUpdateChunkRefs(t *testing.T) {
	metaClient, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-batch-chunk-refs.db"), testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	m := metaClient.(*dbMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %s", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_refs", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_refs: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_refs", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_refs: %s", st)
	}

	var srcFile Ino
	if st := m.Mknod(ctx, srcDir, "file", TypeFile, 0644, 022, 0, "", &srcFile, nil); st != 0 {
		t.Fatalf("mknod file: %s", st)
	}
	var slice1, slice2 uint64
	if st := m.NewSlice(ctx, &slice1); st != 0 {
		t.Fatalf("new slice1: %s", st)
	}
	if st := m.NewSlice(ctx, &slice2); st != 0 {
		t.Fatalf("new slice2: %s", st)
	}
	const sliceSize = uint32(4096)
	if st := m.Write(ctx, srcFile, 0, 0, Slice{Id: slice1, Size: sliceSize, Len: sliceSize}, time.Now()); st != 0 {
		t.Fatalf("write slice1: %s", st)
	}
	if st := m.Write(ctx, srcFile, 0, sliceSize, Slice{Id: slice2, Size: sliceSize, Len: sliceSize}, time.Now()); st != 0 {
		t.Fatalf("write slice2: %s", st)
	}
	if got := sqlSliceRefCount(t, m, slice1, sliceSize); got != 1 {
		t.Fatalf("slice1 refs before clone: got %d, want 1", got)
	}
	if got := sqlSliceRefCount(t, m, slice2, sliceSize); got != 1 {
		t.Fatalf("slice2 refs before clone: got %d, want 1", got)
	}

	var entries []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &entries); st != 0 {
		t.Fatalf("readdir src_refs: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range entries {
		name := string(e.Name)
		if name != "." && name != ".." {
			batchEntries = append(batchEntries, e)
		}
	}
	var cloned uint64
	if st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned); st != 0 {
		t.Fatalf("batch clone: %s", st)
	}
	if cloned != 1 {
		t.Fatalf("batch clone count: got %d, want 1", cloned)
	}
	if got := sqlSliceRefCount(t, m, slice1, sliceSize); got != 2 {
		t.Fatalf("slice1 refs after clone: got %d, want 2", got)
	}
	if got := sqlSliceRefCount(t, m, slice2, sliceSize); got != 2 {
		t.Fatalf("slice2 refs after clone: got %d, want 2", got)
	}

	var dstFile Ino
	var dstAttr Attr
	if st := m.Lookup(ctx, dstDir, "file", &dstFile, &dstAttr, false); st != 0 {
		t.Fatalf("lookup cloned file: %s", st)
	}
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		t.Fatalf("slice %d should still have a reference", args[0].(uint64))
		return nil
	})
	if err := m.deleteChunk(dstFile, 0); err != nil {
		t.Fatalf("delete cloned chunk: %s", err)
	}
	if got := sqlSliceRefCount(t, m, slice1, sliceSize); got != 1 {
		t.Fatalf("slice1 refs after deleting clone chunk: got %d, want 1", got)
	}
	if got := sqlSliceRefCount(t, m, slice2, sliceSize); got != 1 {
		t.Fatalf("slice2 refs after deleting clone chunk: got %d, want 1", got)
	}
}

func sqlSliceRefCount(t *testing.T, m *dbMeta, id uint64, size uint32) int {
	t.Helper()
	ref := sliceRef{Id: id}
	ok, err := m.db.Get(&ref)
	if err != nil {
		t.Fatalf("get slice ref %d: %s", id, err)
	}
	if !ok {
		return 0
	}
	if ref.Size != size {
		t.Fatalf("slice ref %d size: got %d, want %d", id, ref.Size, size)
	}
	return ref.Refs
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
