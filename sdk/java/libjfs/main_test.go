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

package main

import (
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func TestSetOwner(t *testing.T) {
	m := meta.NewClient("memkv://", nil)
	if err := m.Init(&meta.Format{Name: "test", BlockSize: 4096}, true); err != nil {
		t.Fatal(err)
	}
	conf := vfs.Config{Meta: meta.DefaultConf(), Chunk: &chunk.Config{
		BlockSize: 4096 << 10, MaxUpload: 1, MaxDownload: 1, BufferSize: 100 << 20,
	}}
	obj, err := object.CreateStorage("mem", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	jfs, err := fs.NewFileSystem(&conf, m, chunk.NewCachedStore(obj, *conf.Chunk, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jfs.Close() })
	w := &wrapper{FileSystem: jfs, m: &mapping{
		usernames: map[string]uint32{"owner": 1000, "other": 2000},
		groups:    map[string]uint32{"group": 1000, "other": 2000},
	}}
	root := meta.Background()
	owner := meta.NewContext(1, 1000, []uint32{1000})
	nonOwner := meta.NewContext(1, 2000, []uint32{2000})
	groupMember := meta.NewContext(1, 2000, []uint32{2000, 1000})

	for _, tc := range []struct {
		name  string
		dir   bool
		mode  uint16
		flags uint8
		ctx   meta.Context
	}{
		{"file", false, 0755, 0, nonOwner},
		{"directory", true, 0755, 0, nonOwner},
		{"sgid_in_group", false, 02644, 0, groupMember},
		{"sgid_outside_group", false, 02644, 0, nonOwner},
		{"suid", false, 04755, 0, nonOwner},
		{"immutable", false, 06755, meta.FlagImmutable, nonOwner},
		{"append_only", false, 06755, meta.FlagAppend, nonOwner},
		{"owner", false, 06755, 0, owner},
		{"root", false, 06755, 0, root},
	} {
		t.Run("empty/"+tc.name, func(t *testing.T) {
			path := "/" + tc.name
			var ino meta.Ino
			var st syscall.Errno
			if tc.dir {
				st = m.Mkdir(root, meta.RootInode, tc.name, 0755, 0, 0, &ino, nil)
			} else {
				st = m.Create(root, meta.RootInode, tc.name, 0755, 0, 0, &ino, nil)
			}
			if st != 0 {
				t.Fatal(st)
			}
			before := meta.Attr{Uid: 1000, Gid: 1000, Mode: tc.mode, Flags: tc.flags}
			if st = m.SetAttr(root, ino, meta.SetAttrUID|meta.SetAttrGID|meta.SetAttrMode|meta.SetAttrFlag, 0, &before); st != 0 {
				t.Fatal(st)
			}
			// Empty Java requests must preserve the existing no-change behavior,
			// including set-ID bits and ctime, regardless of the caller's ownership.
			if st := setOwner(w, tc.ctx, path, "", ""); st != 0 {
				t.Fatalf("setOwner with no owner or group: %d", st)
			}
			var after meta.Attr
			if st = m.GetAttr(root, ino, &after); st != 0 {
				t.Fatal(st)
			}
			if after != before {
				t.Fatalf("empty setOwner changed attributes: before=%+v, after=%+v", before, after)
			}
		})
	}

	for _, tc := range []struct {
		name, owner, group string
		uid, gid           uint32
	}{
		{"owner", "other", "", 2000, 1000},
		{"group", "", "other", 1000, 2000},
		{"both", "other", "other", 2000, 2000},
	} {
		t.Run("specified/"+tc.name, func(t *testing.T) {
			f, st := w.Create(owner, "/specified-"+tc.name, 0644, 0)
			if st != 0 {
				t.Fatal(st)
			}
			defer f.Close(root)
			if st := m.SetAttr(root, f.Inode(), meta.SetAttrUID|meta.SetAttrGID, 0, &meta.Attr{Uid: 1000, Gid: 1000}); st != 0 {
				t.Fatal(st)
			}
			if st := setOwner(w, root, f.Name(), tc.owner, tc.group); st != 0 {
				t.Fatal(st)
			}
			var attr meta.Attr
			if st := m.GetAttr(root, f.Inode(), &attr); st != 0 {
				t.Fatal(st)
			}
			if attr.Uid != tc.uid || attr.Gid != tc.gid {
				t.Fatalf("got %d:%d, want %d:%d", attr.Uid, attr.Gid, tc.uid, tc.gid)
			}
		})
	}

	t.Run("empty_path_errors", func(t *testing.T) {
		if st := w.Mkdir(root, "/private", 0700, 0); st != 0 {
			t.Fatal(st)
		}
		for _, tc := range []struct {
			path string
			want syscall.Errno
		}{
			{"/missing", syscall.ENOENT},
			{"/private/file", syscall.EACCES},
		} {
			if st := setOwner(w, nonOwner, tc.path, "", ""); st != errno(tc.want) {
				t.Fatalf("setOwner(%s): %d, want %d", tc.path, st, errno(tc.want))
			}
		}
	})
}
