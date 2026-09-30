/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package object

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDirStorage(t *testing.T) {
	base, _ := CreateStorage("mem", "bucket", "", "", "")

	tests := []struct {
		name    string
		storage func() ObjectStorage
		wantStr string
	}{
		{
			name:    "withPrefix_dir",
			storage: func() ObjectStorage { return WithPrefix(base, "subdir/") },
			wantStr: base.String() + "subdir/",
		},
		{
			name:    "withPrefix_file",
			storage: func() ObjectStorage { return WithPrefix(base, "subdir/file") },
			wantStr: base.String() + "subdir/",
		},
		{
			name:    "withPrefix_toplevel_file",
			storage: func() ObjectStorage { return WithPrefix(base, "file") },
			wantStr: base.String(),
		},
		{
			name:    "withPrefix_empty",
			storage: func() ObjectStorage { return WithPrefix(base, "") },
			wantStr: base.String(),
		},
		{
			name:    "withPrefix_nested_file",
			storage: func() ObjectStorage { return WithPrefix(base, "a/b/c") },
			wantStr: base.String() + "a/b/",
		},
		{
			name: "filestore_dir",
			storage: func() ObjectStorage {
				fs, _ := CreateStorage("file", "/tmp/", "", "", "")
				return fs
			},
			wantStr: "file:///tmp/",
		},
		{
			name: "filestore_file",
			storage: func() ObjectStorage {
				return &filestore{root: "/tmp/target"}
			},
			wantStr: "file:///tmp/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.storage()
			got := DirStorage(s)
			if got.String() != tc.wantStr {
				t.Errorf("DirStorage(%q).String() = %q, want %q", s.String(), got.String(), tc.wantStr)
			}
		})
	}
}

func TestWithPrefixRejectsKeyPathTraversal(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	disk, err := newDisk(base+"/", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s := WithPrefix(disk, "backup/")
	for _, key := range []string{"../outside.txt", "a/../../outside.txt", "/../outside.txt"} {
		if err := s.Put(ctx, key, bytes.NewReader([]byte("x"))); err == nil {
			t.Fatalf("Put(%q) should be rejected", key)
		}
		if _, err := s.Head(ctx, key); err == nil {
			t.Fatalf("Head(%q) should be rejected", key)
		}
		if _, err := s.Get(ctx, key, 0, -1); err == nil {
			t.Fatalf("Get(%q) should be rejected", key)
		}
		if err := s.Delete(ctx, key); err == nil {
			t.Fatalf("Delete(%q) should be rejected", key)
		}
		if _, _, _, err := s.List(ctx, key, "", "", "/", 10, false); err == nil {
			t.Fatalf("List(%q) should be rejected", key)
		}
		if err := s.(MtimeChanger).Chtimes(key, time.Now()); err == nil {
			t.Fatalf("Chtimes(%q) should be rejected", key)
		}
		if err := s.(SupportSymlink).Symlink("target", key); err == nil {
			t.Fatalf("Symlink(%q) should be rejected", key)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("object escaped the prefix: %v", err)
	}

	if err := s.Put(ctx, "a/b.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put: %s", err)
	}
	if _, err := os.Stat(filepath.Join(base, "backup", "a", "b.txt")); err != nil {
		t.Fatalf("stat: %s", err)
	}
	// the target of a symlink is not a key, it may point to the parent directory
	if err := s.(SupportSymlink).Symlink("../b.txt", "a/c/link"); err != nil {
		t.Fatalf("Symlink: %s", err)
	}

	// keys of object storages are not paths, ".." has no special meaning
	mem, _ := CreateStorage("mem", "bucket", "", "", "")
	ms := WithPrefix(mem, "backup/")
	if err := ms.Put(ctx, "../x", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put to mem: %s", err)
	}
	if _, err := mem.Head(ctx, "backup/../x"); err != nil {
		t.Fatalf("Head from mem: %s", err)
	}
	// nested withPrefix should not be treated as a file system
	nested := WithPrefix(WithPrefix(mem, "a/"), "b/")
	if err := nested.Put(ctx, "../x", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put to nested mem: %s", err)
	}
	if _, err := mem.Head(ctx, "a/b/../x"); err != nil {
		t.Fatalf("Head from mem: %s", err)
	}
	if IsFileSystem(nested) {
		t.Fatal("nested withPrefix of mem should not be a file system")
	}
	nestedDisk := WithPrefix(WithPrefix(disk, "a/"), "b/")
	if err := nestedDisk.Put(ctx, "../x", bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("Put(../x) to nested disk should be rejected")
	}
	if !IsFileSystem(nestedDisk) {
		t.Fatal("nested withPrefix of disk should be a file system")
	}

	// '\' is a path separator for SMB and Windows
	bs := WithPrefix(&backslashDisk{disk.(*filestore)}, "backup/")
	for _, key := range []string{`..\outside/proof.txt`, `a\..\..\outside.txt`} {
		if err := bs.Put(ctx, key, bytes.NewReader([]byte("x"))); err == nil {
			t.Fatalf("Put(%q) should be rejected", key)
		}
	}
	if runtime.GOOS != "windows" {
		// but a normal character on POSIX file systems
		if err := s.Put(ctx, `..\x`, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatalf("Put: %s", err)
		}
		if _, err := os.Stat(filepath.Join(base, "backup", `..\x`)); err != nil {
			t.Fatalf("stat: %s", err)
		}
	}
}

func TestWithPrefixKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, prefix := range []string{"backup/", "backup//", "./backup/", "a/./b/", "backup-"} {
		disk, _ := newDisk(t.TempDir()+"/", "", "", "")
		s := WithPrefix(disk, prefix)
		if err := s.Put(ctx, "hello.txt", bytes.NewReader([]byte("x"))); err != nil {
			t.Fatalf("prefix %q: Put: %s", prefix, err)
		}
		if o, err := s.Head(ctx, "hello.txt"); err != nil || o.Key() != "hello.txt" {
			t.Fatalf("prefix %q: Head = %v, %v", prefix, o, err)
		}
		if got, err := get(s, "hello.txt", 0, -1); err != nil || got != "x" {
			t.Fatalf("prefix %q: Get = %q, %v", prefix, got, err)
		}
	}
}

type backslashDisk struct {
	*filestore
}

func (d *backslashDisk) backslashSeparated() {}
