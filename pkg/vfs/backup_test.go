/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package vfs

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

func TestRotate(t *testing.T) {
	format := func(ts time.Time) string {
		return "dump-" + ts.UTC().Format("2006-01-02-150405") + ".json.gz"
	}

	now := time.Now()
	objs := make([]string, 0, 25)
	for cursor, i := now.AddDate(0, 0, -100), 0; i <= 200; i++ { // one backup for every half day
		objs = append(objs, format(cursor))
		toDel := rotate(objs, cursor)
		for _, d := range toDel {
			for j, k := range objs {
				if k == d {
					objs = append(objs[:j], objs[j+1:]...)
					break
				}
			}
		}
		cursor = cursor.Add(time.Duration(12) * time.Hour)
	}

	expect := make([]string, 0, 25)
	expect = append(expect, format(now.AddDate(0, 0, -100)))
	for days := 65; days > 14; days -= 7 {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for days := 13; days > 2; days-- {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for i := 4; i >= 0; i-- {
		expect = append(expect, format(now.Add(time.Duration(-i*12)*time.Hour)))
	}

	if len(objs) != len(expect) {
		t.Fatalf("length of objs %d != length of expect %d", len(objs), len(expect))
	}
	for i, o := range objs {
		if o != expect[i] {
			t.Fatalf("obj %s != expect %s", o, expect[i])
		}
	}
}

func TestBackup(t *testing.T) {
	v, blob := createTestVFS(nil, "")
	go Backup(v.Meta, blob, time.Millisecond*100, false)
	time.Sleep(time.Millisecond * 100)

	blob = object.WithPrefix(blob, "meta/")
	kc, _ := object.ListAll(context.TODO(), blob, "", "", true, false)
	var keys []string
	for obj := range kc {
		keys = append(keys, obj.Key())
	}
	if len(keys) < 1 {
		t.Fatalf("there should be at least 1 backup file")
	}
}

type backupHookStorage struct {
	object.ObjectStorage
	beforeCopy func()
}

type backupDumpErrorMeta struct {
	meta.Meta
	err error
}

func (m *backupDumpErrorMeta) DumpMeta(io.Writer, meta.Ino, int, bool, bool, bool) error {
	return m.err
}

func (s *backupHookStorage) Limits() object.Limits {
	// CopyData asks for the destination limits before opening the dump.
	if s.beforeCopy != nil {
		hook := s.beforeCopy
		s.beforeCopy = nil
		hook()
	}
	return s.ObjectStorage.Limits()
}

func TestBackupTempIsolation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	key := "meta/dump-2026-01-02-030405.json.gz"
	checkDump := func(t *testing.T, blob object.ObjectStorage, uuid string) {
		t.Helper()
		r, err := blob.Get(context.Background(), key, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		zr, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		var dump meta.DumpedMeta
		if err = json.NewDecoder(zr).Decode(&dump); err != nil {
			t.Fatal(err)
		}
		if dump.Setting.UUID != uuid {
			t.Fatalf("backup contains volume %s, want %s", dump.Setting.UUID, uuid)
		}
	}
	for _, overlap := range []bool{false, true} {
		t.Run(fmt.Sprintf("overlap=%v", overlap), func(t *testing.T) {
			first, firstBlob := createTestVFS(nil, "sqlite3://"+filepath.Join(t.TempDir(), "first.db"))
			second, secondBlob := createTestVFS(nil, "sqlite3://"+filepath.Join(t.TempDir(), "second.db"))
			backupSecond := func() {
				if path, err := backup(second.Meta, secondBlob, now, true, false); err != nil {
					t.Fatal(err)
				} else if path != secondBlob.String()+key {
					t.Fatalf("backup path %q, want %q", path, secondBlob.String()+key)
				}
				checkDump(t, secondBlob, second.Conf.Format.UUID)
			}
			var target object.ObjectStorage = firstBlob
			if overlap {
				// Complete another volume's backup after the first dump is written,
				// but before CopyData reopens its source. No timing-dependent sleeps.
				target = &backupHookStorage{firstBlob, backupSecond}
			}
			if path, err := backup(first.Meta, target, now, true, false); err != nil {
				t.Fatal(err)
			} else if path != firstBlob.String()+key {
				t.Fatalf("backup path %q, want %q", path, firstBlob.String()+key)
			}
			checkDump(t, firstBlob, first.Conf.Format.UUID)
			if !overlap {
				backupSecond()
			}
		})
	}
	t.Run("dump-error", func(t *testing.T) {
		v, blob := createTestVFS(nil, "sqlite3://"+filepath.Join(t.TempDir(), "failed.db"))
		wantErr := errors.New("dump failed")
		if _, err := backup(&backupDumpErrorMeta{v.Meta, wantErr}, blob, now, true, false); !errors.Is(err, wantErr) {
			t.Fatalf("backup error %v, want %v", err, wantErr)
		}
	})
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "juicefs-backup-") {
			t.Fatalf("temporary backup left behind: %s", entry.Name())
		}
	}
}

func TestUsedInodesWithSubdir(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	m := v.Meta
	ctx := meta.Background()
	var inode meta.Ino
	var attr meta.Attr
	if st := m.Mkdir(ctx, meta.RootInode, "sub", 0755, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("mkdir sub: %s", st)
	}
	for i := 0; i < 5; i++ {
		if st := m.Create(ctx, meta.RootInode, fmt.Sprintf("f%d", i), 0644, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("create f%d: %s", i, st)
		}
	}
	quotas := map[string]*meta.Quota{"/sub": {MaxSpace: 1 << 30, MaxInodes: -1}}
	if err := m.HandleQuota(ctx, meta.QuotaSet, "/sub", meta.DirQuotaType, quotas, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	// quotas are loaded into the client cache with a new session
	if err := m.NewSession(false); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	var dummy, want uint64
	_ = m.StatFS(ctx, 0, &dummy, &dummy, &want, &dummy)
	if st := m.Chroot(ctx, "sub"); st != 0 {
		t.Fatalf("chroot sub: %s", st)
	}
	var subUsed uint64
	_ = m.StatFS(ctx, meta.RootInode, &dummy, &dummy, &subUsed, &dummy)
	if subUsed >= want {
		t.Fatalf("used inodes of the subdir quota = %d, want less than %d (the whole volume)", subUsed, want)
	}
	var got uint64
	_ = m.StatFS(ctx, 0, &dummy, &dummy, &got, &dummy)
	if got != want {
		t.Fatalf("used inodes with subdir = %d, want %d (the whole volume)", got, want)
	}
}
