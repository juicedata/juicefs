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

//
//mutate:disable
//nolint:errcheck
package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDirMarker(t *testing.T) {
	cases := map[string]string{
		"memkv":  "memkv://",
		"sqlite": "sqlite3://" + filepath.Join(t.TempDir(), "marker.db"),
		"badger": "badger://" + t.TempDir(),
	}
	for _, engine := range []string{"redis", "valkey", "keydb", "mysql", "mariadb", "tidb", "oceanbase", "postgres", "cockroach", "etcd", "tikv", "fdb"} {
		if uri := os.Getenv("JFS_MARKER_TEST_" + strings.ToUpper(engine)); uri != "" {
			cases[engine] = uri
		}
	}
	for name, uri := range cases {
		t.Run(name, func(t *testing.T) {
			m := NewClient(uri, testConfig())
			require.NoError(t, m.Reset())
			format := testFormat()
			format.ChangeLog = name != "etcd"
			require.NoError(t, m.Init(format, true))
			t.Cleanup(func() { require.NoError(t, m.Shutdown()) })
			testDirMarker(t, m)
			if name != "memkv" && name != "badger" {
				t.Run("multiple clients", func(t *testing.T) {
					other := NewClient(uri, testConfig())
					_, err := other.Load(false)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, other.Shutdown()) })
					testDirMarkerWriters(t, []Meta{m, other})
				})
			}
			if format.ChangeLog {
				t.Run("changelog", func(t *testing.T) { testDirMarkerChangelog(t, m) })
			}
			if r, ok := m.(*redisMeta); ok {
				t.Run("invalid xattr storage", func(t *testing.T) { testDirMarkerRedisFailure(t, r) })
			}
		})
	}
}

func testDirMarker(t *testing.T, m Meta) {
	ctx := Background()
	var seq int
	newDirectory := func(t *testing.T) (string, Ino, Attr) {
		t.Helper()
		seq++
		name := fmt.Sprintf("dir-marker-%d", seq)
		var inode Ino
		var attr Attr
		require.Zero(t, m.Mkdir(ctx, RootInode, name, 0755, 0, 0, &inode, &attr))
		return name, inode, attr
	}
	getXattr := func(t *testing.T, inode Ino, key string) []byte {
		t.Helper()
		var value []byte
		require.Zero(t, m.GetXattr(ctx, inode, key, &value))
		return value
	}
	t.Run("promotion preserves inode children and unrelated attributes", func(t *testing.T) {
		name, inode, before := newDirectory(t)
		var child Ino
		require.Zero(t, m.Mknod(ctx, inode, "child", TypeFile, 0644, 0, 0, "", &child, nil))
		require.Zero(t, m.SetXattr(ctx, inode, "user.old", []byte("old"), 0))
		require.Zero(t, m.SetXattr(ctx, inode, "user.posix", []byte("keep"), 0))
		var result Attr
		updates := map[string][]byte{"user.old": nil, "user.new": []byte("new"), "user.missing": nil}
		require.Zero(t, m.SetDirMarker(ctx, RootInode, name, inode, true, updates, &result))
		require.Zero(t, result.Atime)
		require.Zero(t, result.Atimensec)
		require.Equal(t, before.Uid, result.Uid)
		require.Equal(t, before.Gid, result.Gid)
		require.Equal(t, before.Mode, result.Mode)
		require.Equal(t, before.Parent, result.Parent)
		require.Equal(t, []byte("new"), getXattr(t, inode, "user.new"))
		require.Equal(t, []byte("keep"), getXattr(t, inode, "user.posix"))
		var value []byte
		require.Equal(t, ENOATTR, m.GetXattr(ctx, inode, "user.old", &value))
		var found Ino
		require.Zero(t, m.Lookup(ctx, inode, "child", &found, &result, true))
		require.Equal(t, child, found)
		require.Equal(t, syscall.EEXIST, m.SetDirMarker(ctx, RootInode, name, inode, true, map[string][]byte{"user.new": []byte("loser")}, &result))
		require.Equal(t, []byte("new"), getXattr(t, inode, "user.new"))
		require.Zero(t, m.SetDirMarker(ctx, RootInode, name, inode, false, map[string][]byte{"user.new": []byte("overwrite")}, &result))
		require.Equal(t, []byte("overwrite"), getXattr(t, inode, "user.new"))
	})
	t.Run("rejected updates leave all state unchanged", func(t *testing.T) {
		for _, scenario := range []string{"invalid xattr", "permission", "existing marker permission", "immutable", "append only", "read only", "canceled", "existing marker"} {
			t.Run(scenario, func(t *testing.T) {
				name, inode, _ := newDirectory(t)
				require.Zero(t, m.SetXattr(ctx, inode, "user.keep", []byte("old"), 0))
				updates := map[string][]byte{"user.keep": []byte("new"), "user.new": []byte("new")}
				requestCtx := ctx
				exclusive := true
				expected := syscall.EINVAL
				switch scenario {
				case "invalid xattr":
					updates[""] = []byte("invalid")
				case "permission":
					requestCtx = NewContext(1, 1000, []uint32{1000})
					expected = syscall.EACCES
				case "existing marker permission":
					a := Attr{}
					require.Zero(t, m.SetAttr(ctx, inode, SetAttrAtime, 0, &a))
					requestCtx = NewContext(1, 1000, []uint32{1000})
					exclusive = false
					expected = syscall.EACCES
				case "immutable", "append only":
					flag := uint8(FlagImmutable)
					if scenario == "append only" {
						flag = FlagAppend
					}
					a := Attr{Flags: flag}
					require.Zero(t, m.SetAttr(ctx, inode, SetAttrFlag, 0, &a))
					expected = syscall.EPERM
				case "read only":
					m.getBase().conf.ReadOnly = true
					defer func() { m.getBase().conf.ReadOnly = false }()
					expected = syscall.EROFS
				case "canceled":
					requestCtx = NewContext(1, 0, []uint32{0})
					requestCtx.Cancel()
					expected = syscall.EINTR
				case "existing marker":
					a := Attr{}
					require.Zero(t, m.SetAttr(ctx, inode, SetAttrAtime, 0, &a))
					expected = syscall.EEXIST
				}
				var before, after, result Attr
				require.Zero(t, m.getBase().en.doGetAttr(ctx, inode, &before))
				require.Equal(t, expected, m.SetDirMarker(requestCtx, RootInode, name, inode, exclusive, updates, &result))
				require.Zero(t, m.getBase().en.doGetAttr(ctx, inode, &after))
				require.Equal(t, before, after)
				require.Equal(t, []byte("old"), getXattr(t, inode, "user.keep"))
				var value []byte
				require.Equal(t, ENOATTR, m.GetXattr(ctx, inode, "user.new", &value))
			})
		}
	})
	t.Run("renamed and replaced paths are not modified", func(t *testing.T) {
		name, inode, before := newDirectory(t)
		require.Zero(t, m.Rename(ctx, RootInode, name, RootInode, name+"-moved", 0, nil, nil))
		var result Attr
		updates := map[string][]byte{"user.new": []byte("new")}
		require.Equal(t, syscall.ENOENT, m.SetDirMarker(ctx, RootInode, name, inode, true, updates, &result))
		var replacement Ino
		require.Zero(t, m.Mkdir(ctx, RootInode, name, 0755, 0, 0, &replacement, nil))
		require.Equal(t, syscall.EAGAIN, m.SetDirMarker(ctx, RootInode, name, inode, true, updates, &result))
		require.Zero(t, m.getBase().en.doGetAttr(ctx, inode, &result))
		require.Equal(t, before.Atime, result.Atime)
		require.Equal(t, before.Atimensec, result.Atimensec)
		var value []byte
		require.Equal(t, ENOATTR, m.GetXattr(ctx, inode, "user.new", &value))
		require.Equal(t, ENOATTR, m.GetXattr(ctx, replacement, "user.new", &value))
	})
	t.Run("concurrent conditional writers have one complete winner", func(t *testing.T) { testDirMarkerWriters(t, []Meta{m}) })

	t.Run("rename race never promotes a replacement directory", func(t *testing.T) {
		for i := 0; i < 32; i++ {
			name, inode, _ := newDirectory(t)
			start := make(chan struct{})
			result := make(chan syscall.Errno, 1)
			go func() {
				<-start
				var attr Attr
				result <- m.SetDirMarker(ctx, RootInode, name, inode, true, map[string][]byte{"user.winner": []byte("original")}, &attr)
			}()
			close(start)
			require.Zero(t, m.Rename(ctx, RootInode, name, RootInode, name+"-moved", 0, nil, nil))
			var replacement Ino
			require.Zero(t, m.Mkdir(ctx, RootInode, name, 0755, 0, 0, &replacement, nil))
			st := <-result
			require.Contains(t, []syscall.Errno{0, syscall.ENOENT, syscall.EAGAIN}, st)
			var attr Attr
			require.Zero(t, m.getBase().en.doGetAttr(ctx, replacement, &attr))
			require.NotZero(t, attr.Atime)
			var value []byte
			require.Equal(t, ENOATTR, m.GetXattr(ctx, replacement, "user.winner", &value))
		}
	})
	t.Run("case insensitive names", func(t *testing.T) {
		name, inode, _ := newDirectory(t)
		old := m.getBase().conf.CaseInsensi
		m.getBase().conf.CaseInsensi = true
		defer func() { m.getBase().conf.CaseInsensi = old }()
		var attr Attr
		require.Zero(t, m.SetDirMarker(ctx, RootInode, strings.ToUpper(name), inode, true, nil, &attr))
	})
}

func TestDirMarkerSQLiteRollback(t *testing.T) {
	client, err := newSQLMeta("sqlite3", filepath.Join(t.TempDir(), "rollback.db"), testConfig())
	require.NoError(t, err)
	m := client.(*dbMeta)
	require.NoError(t, m.Init(testFormat(), true))
	t.Cleanup(func() { require.NoError(t, m.Shutdown()) })
	ctx := Background()
	var inode Ino
	var before, after Attr
	require.Zero(t, m.Mkdir(ctx, RootInode, "dir", 0755, 0, 0, &inode, &before))
	require.Zero(t, m.SetXattr(ctx, inode, "user.old", []byte("original"), 0))
	// Abort the final inode write, after the xattr changes have executed.
	_, err = m.db.Exec(`CREATE TRIGGER fail_marker BEFORE UPDATE OF atime ON jfs_node BEGIN SELECT RAISE(ABORT, 'injected marker failure'); END`)
	require.NoError(t, err)
	require.NotZero(t, m.SetDirMarker(ctx, RootInode, "dir", inode, true, map[string][]byte{"user.old": nil, "user.new": []byte("new")}, &after))
	require.Zero(t, m.doGetAttr(ctx, inode, &after))
	require.Equal(t, before, after)
	var value []byte
	require.Zero(t, m.GetXattr(ctx, inode, "user.old", &value))
	require.Equal(t, []byte("original"), value)
	require.Equal(t, ENOATTR, m.GetXattr(ctx, inode, "user.new", &value))
}

func testDirMarkerChangelog(t *testing.T, m Meta) {
	ctx := Background()
	var inode Ino
	var attr Attr
	require.Zero(t, m.Mkdir(ctx, RootInode, "marker-log", 0755, 0, 0, &inode, nil))
	updates := map[string][]byte{"user.binary": {0, 255, ',', '%'}, "user.deleted": nil}
	require.Zero(t, m.SetDirMarker(ctx, RootInode, "marker-log", inode, true, updates, &attr))
	logCtx := NewContext(1, 0, []uint32{0})
	defer logCtx.Cancel()
	timer := time.AfterFunc(5*time.Second, logCtx.Cancel)
	defer timer.Stop()
	done := errors.New("record found")
	err := m.ScanChangelog(logCtx, 1, func(version int64, entry string) error {
		parts := strings.Split(entry, "|")
		if len(parts) < 2 || !strings.HasPrefix(parts[1], fmt.Sprintf("SETDIRMARKER(%d,", inode)) {
			return nil
		}
		fields := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(parts[1], "SETDIRMARKER("), ")"), ",", 4)
		require.Len(t, fields, 4)
		require.Equal(t, strconv.FormatInt(attr.Ctime, 10), fields[1])
		require.Equal(t, strconv.FormatUint(uint64(attr.Ctimensec), 10), fields[2])
		raw, err := url.PathUnescape(fields[3])
		require.NoError(t, err)
		var stored map[string][]byte
		require.NoError(t, json.Unmarshal([]byte(raw), &stored))
		require.Equal(t, updates, stored)
		return done
	})
	require.ErrorIs(t, err, done)
}

func testDirMarkerRedisFailure(t *testing.T, m *redisMeta) {
	ctx := Background()
	var inode Ino
	var before, after Attr
	require.Zero(t, m.Mkdir(ctx, RootInode, "marker-corrupt", 0755, 0, 0, &inode, &before))
	// Redis EXEC does not roll back runtime command errors. A wrong-type xattr
	// key must be rejected before enqueuing either the marker or xattr writes.
	require.NoError(t, m.rdb.Set(ctx, m.xattrKey(inode), "invalid hash", 0).Err())
	require.NotZero(t, m.SetDirMarker(ctx, RootInode, "marker-corrupt", inode, true, map[string][]byte{"user.new": []byte("value")}, &after))
	require.Zero(t, m.doGetAttr(ctx, inode, &after))
	require.Equal(t, before, after)
	stored, err := m.rdb.Get(ctx, m.xattrKey(inode)).Result()
	require.NoError(t, err)
	require.Equal(t, "invalid hash", stored)
	require.NoError(t, m.rdb.Del(ctx, m.xattrKey(inode)).Err())
}

func testDirMarkerWriters(t *testing.T, clients []Meta) {
	m := clients[0]
	ctx := Background()
	name := strings.ReplaceAll(t.Name(), "/", "-")
	var inode Ino
	require.Zero(t, m.Mkdir(ctx, RootInode, name, 0755, 0, 0, &inode, nil))
	const writers = 16
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make([]syscall.Errno, writers)
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			var attr Attr
			value := []byte(fmt.Sprint(i))
			results[i] = clients[i%len(clients)].SetDirMarker(ctx, RootInode, name, inode, true, map[string][]byte{"user.first": value, "user.second": value}, &attr)
		}(i)
	}
	close(start)
	wait.Wait()
	winner := -1
	for i, st := range results {
		if st == 0 {
			require.Equal(t, -1, winner)
			winner = i
		} else {
			require.Equal(t, syscall.EEXIST, st)
		}
	}
	require.NotEqual(t, -1, winner)

	var first, second []byte
	require.Zero(t, m.GetXattr(ctx, inode, "user.first", &first))
	require.Zero(t, m.GetXattr(ctx, inode, "user.second", &second))
	require.Equal(t, []byte(fmt.Sprint(winner)), first)
	require.Equal(t, first, second)
}
