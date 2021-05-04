/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package meta

import (
	"bytes"
	"os"
	"syscall"
	"testing"
)

func TestSQLClient(t *testing.T) {
	os.Remove("test.db")
	m, err := NewSQLMeta("sqlite3", "test.db")
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error { return nil })
	_ = m.Init(Format{Name: "test"}, true)
	err = m.Init(Format{Name: "test"}, false) // changes nothing
	if err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	format, err := m.Load()
	if err != nil {
		t.Fatalf("load failed after initialization: %s", err)
	}
	if format.Name != "test" {
		t.Fatalf("load got volume name %s, expected %s", format.Name, "test")
	}
	_ = m.NewSession()
	// go m.(*sqlM).cleanStaleSessions()
	ctx := Background
	var parent, inode, dummyInode Ino
	var attr = &Attr{}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	defer m.Rmdir(ctx, 1, "d")
	if st := m.Unlink(ctx, 1, "d"); st != syscall.EPERM {
		t.Fatalf("unlink d: %s", st)
	}
	if st := m.Rmdir(ctx, parent, "."); st != syscall.EINVAL {
		t.Fatalf("Rmdir d.: %s", st)
	}
	if st := m.Rmdir(ctx, parent, ".."); st != syscall.ENOTEMPTY {
		t.Fatalf("Rmdir d..: %s", st)
	}
	if st := m.Lookup(ctx, 1, "d", &parent, attr); st != 0 {
		t.Fatalf("lookup d: %s", st)
	}
	if st := m.Access(ctx, parent, 4, attr); st != 0 {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	defer m.Unlink(ctx, parent, "f")
	if st := m.Rmdir(ctx, parent, "f"); st != syscall.ENOTDIR {
		t.Fatalf("rmdir f: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != syscall.ENOTEMPTY {
		t.Fatalf("rmdir d: %s", st)
	}
	if st := m.Mknod(ctx, inode, "df", TypeFile, 0650, 022, 0, &dummyInode, nil); st != syscall.ENOTDIR {
		t.Fatalf("create fd: %s", st)
	}
	if st := m.Mknod(ctx, parent, "f", TypeFile, 0650, 022, 0, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Lookup(ctx, parent, "f", &inode, attr); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}
	attr.Atime = 2
	attr.Mtime = 2
	attr.Uid = 1
	attr.Gid = 1
	attr.Mode = 0644
	if st := m.SetAttr(ctx, inode, SetAttrAtime|SetAttrMtime|SetAttrUID|SetAttrGID|SetAttrMode, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.SetAttr(ctx, inode, 0, 0, attr); st != 0 { // changes nothing
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Atime != 2 || attr.Mtime != 2 || attr.Uid != 1 || attr.Gid != 1 || attr.Mode != 0644 {
		t.Fatalf("atime:%d mtime:%d uid:%d gid:%d mode:%o", attr.Atime, attr.Mtime, attr.Uid, attr.Gid, attr.Mode)
	}
	if st := m.SetAttr(ctx, inode, SetAttrAtimeNow|SetAttrMtimeNow, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	fakeCtx := NewContext(100, 1, []uint32{1})
	if st := m.Access(fakeCtx, parent, 4, nil); st != syscall.EACCES {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Access(fakeCtx, inode, 4, nil); st != 0 {
		t.Fatalf("access f: %s", st)
	}
	var entries []*Entry
	if st := m.Readdir(ctx, parent, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 3 {
		t.Fatalf("entries: %d", len(entries))
	} else if string(entries[0].Name) != "." || string(entries[1].Name) != ".." || string(entries[2].Name) != "f" {
		t.Fatalf("entries: %+v", entries)
	}
	if st := m.Rename(ctx, parent, "f", 1, "f2", &inode, attr); st != 0 {
		t.Fatalf("rename d/f -> f2: %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f2")
	}()
	if st := m.Rename(ctx, 1, "f2", 1, "f2", &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f2: %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0644, 022, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Rename(ctx, 1, "f2", 1, "f", &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Link(ctx, inode, 1, "f3", attr); st != 0 {
		t.Fatalf("link f3 -> f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f3")
	if st := m.Link(ctx, parent, 1, "d2", attr); st != syscall.EPERM {
		t.Fatalf("link d2 -> d: %s", st)
	}
	if st := m.Symlink(ctx, 1, "s", "/f", &inode, attr); st != 0 {
		t.Fatalf("symlink s -> /f: %s", st)
	}
	defer m.Unlink(ctx, 1, "s")
	var target1, target2 []byte
	if st := m.ReadLink(ctx, inode, &target1); st != 0 {
		t.Fatalf("readlink s: %s", st)
	}
	if st := m.ReadLink(ctx, inode, &target2); st != 0 { // cached
		t.Fatalf("readlink s: %s", st)
	}
	if !bytes.Equal(target1, target2) || !bytes.Equal(target1, []byte("/f")) {
		t.Fatalf("readlink got %s %s, expected %s", target1, target2, "/f")
	}
	if st := m.ReadLink(ctx, parent, &target1); st != syscall.ENOENT {
		t.Fatalf("readlink d: %s", st)
	}
	if st := m.Lookup(ctx, 1, "f", &inode, attr); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}

	// data
	var chunkid uint64
	if st := m.Open(ctx, inode, 2, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	if st := m.NewChunk(ctx, inode, 0, 0, &chunkid); st != 0 {
		t.Fatalf("write chunk: %s", st)
	}
	var s = Slice{Chunkid: chunkid, Size: 100, Len: 100}
	if st := m.Write(ctx, inode, 0, 100, s); st != 0 {
		t.Fatalf("write end: %s", st)
	}
	var chunks []Slice
	if st := m.Read(ctx, inode, 0, &chunks); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(chunks) != 2 || chunks[0].Chunkid != 0 || chunks[0].Size != 100 || chunks[1].Chunkid != chunkid || chunks[1].Size != 100 {
		t.Fatalf("chunks: %v", chunks)
	}
}
