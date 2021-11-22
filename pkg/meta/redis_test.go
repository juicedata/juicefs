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

//nolint:errcheck
package meta

import (
	"bytes"
	"context"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

func TestRedisClient(t *testing.T) {
	var conf Config
	_, err := newRedisMeta("http", "127.0.0.1:6379/7", &conf)
	if err == nil {
		t.Fatal("meta created with invalid url")
	}
	m, err := newRedisMeta("redis", "127.0.0.1:6379/7", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.(*redisMeta).rdb.FlushDB(context.TODO())
	testMetaClient(t, m)
}

func testMetaClient(t *testing.T, m Meta) {
	m.OnMsg(DeleteChunk, func(args ...interface{}) error { return nil })
	_ = m.Init(Format{Name: "test"}, true)
	err := m.Init(Format{Name: "test"}, false) // changes nothing
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
	m.NewSession()
	switch r := m.(type) {
	case *redisMeta:
		go r.cleanStaleSessions()
	}
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
		t.Fatalf("unlink d.: %s", st)
	}
	if st := m.Rmdir(ctx, parent, ".."); st != syscall.ENOTEMPTY {
		t.Fatalf("unlink d..: %s", st)
	}
	if st := m.Lookup(ctx, 1, "d", &parent, attr); st != 0 {
		t.Fatalf("lookup d: %s", st)
	}
	if st := m.Lookup(ctx, parent, ".", &inode, attr); st != 0 || inode != parent {
		t.Fatalf("lookup .: %s", st)
	}
	if st := m.Lookup(ctx, parent, "..", &inode, attr); st != 0 || inode != 1 {
		t.Fatalf("lookup ..: %s", st)
	}
	if st := m.Access(ctx, parent, 4, attr); st != 0 {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	var tino Ino
	if st := m.Lookup(ctx, inode, ".", &tino, attr); st != syscall.ENOTDIR {
		t.Fatalf("lookup /d/f/.: %s", st)
	}
	if st := m.Lookup(ctx, inode, "..", &tino, attr); st != syscall.ENOTDIR {
		t.Fatalf("lookup /d/f/..: %s", st)
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
	if st := m.Resolve(ctx, 1, "d/f", &inode, attr); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve d/f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f", &inode, attr); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	var ctx2 = NewContext(0, 1, []uint32{1})
	if st := m.Resolve(ctx2, parent, "/f", &inode, attr); st != syscall.EACCES && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f/c", &inode, attr); st != syscall.ENOTDIR && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f2", &inode, attr); st != syscall.ENOENT && st != syscall.ENOTSUP {
		t.Fatalf("resolve f2: %s", st)
	}
	// check owner permission
	var p1, c1 Ino
	if st := m.Mkdir(ctx2, 1, "d1", 02766, 022, 0, &p1, attr); st != 0 {
		t.Fatalf("mkdir d1: %s", st)
	}
	attr.Gid = 1
	m.SetAttr(ctx, p1, SetAttrGID, 0, attr)
	if attr.Mode&02000 == 0 {
		t.Fatalf("SGID is lost")
	}
	var ctx3 = NewContext(2, 2, []uint32{2})
	if st := m.Mkdir(ctx3, p1, "d2", 0777, 022, 0, &c1, attr); st != 0 {
		t.Fatalf("mkdir d2: %s", st)
	}
	if attr.Gid != ctx2.Gid() {
		t.Fatalf("inherit gid: %d != %d", attr.Gid, ctx2.Gid())
	}
	if runtime.GOOS == "linux" && attr.Mode&02000 == 0 {
		t.Fatalf("not inherit sgid")
	}
	if st := m.Resolve(ctx2, 1, "/d1/d2", nil, nil); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve /d1/d2: %s", st)
	}
	m.Rmdir(ctx2, p1, "d2")
	m.Rmdir(ctx2, 1, "d1")

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
	if st := m.Rename(ctx, parent, "f", 1, "f2", RenameWhiteout, &inode, attr); st != syscall.ENOTSUP {
		t.Fatalf("rename d/f -> f2: %s", st)
	}
	if st := m.Rename(ctx, parent, "f", 1, "f2", 0, &inode, attr); st != 0 {
		t.Fatalf("rename d/f -> f2: %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f2")
	}()
	if st := m.Rename(ctx, 1, "f2", 1, "f2", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f2: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "f", RenameExchange, &inode, attr); st != syscall.ENOENT {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Rename(ctx, 1, "f2", 1, "f", RenameNoReplace, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "f", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f", 1, "d", RenameExchange, &inode, attr); st != 0 {
		t.Fatalf("rename f <-> d: %s", st)
	}
	// Test rename with parent change
	var parent2 Ino
	if st := m.Mkdir(ctx, 1, "d4", 0777, 0, 0, &parent2, attr); st != 0 {
		t.Fatalf("create dir d4: %s", st)
	}
	if st := m.Mkdir(ctx, parent2, "d5", 0777, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create dir d4/d5: %s", st)
	}
	if st := m.Rename(ctx, parent2, "d5", 1, "d5", RenameNoReplace, &inode, attr); st != 0 {
		t.Fatalf("rename d4/d5 <-> d5: %s", st)
	} else if attr.Parent != 1 {
		t.Fatalf("after rename d4/d5 <-> d5 parent %d expect 1", attr.Parent)
	}
	if st := m.Mknod(ctx, parent2, "f6", TypeFile, 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create dir d4/f6: %s", st)
	}
	if st := m.Rename(ctx, 1, "d5", parent2, "f6", RenameExchange, &inode, attr); st != 0 {
		t.Fatalf("rename d5 <-> d4/d6: %s", st)
	} else if attr.Parent != parent2 {
		t.Fatalf("after exchange d5 <-> d4/f6 parent %d expect %d", attr.Parent, parent2)
	} else if attr.Typ != TypeDirectory {
		t.Fatalf("after exchange d5 <-> d4/f6 type %d expect %d", attr.Typ, TypeDirectory)
	}
	if st := m.Lookup(ctx, 1, "d5", &inode, attr); st != 0 || attr.Parent != 1 {
		t.Fatalf("lookup d5 after exchange: %s; parent %d expect 1", st, attr.Parent)
	} else if attr.Typ != TypeFile {
		t.Fatalf("after exchange d5 <-> d4/f6 type %d expect %d", attr.Typ, TypeFile)
	}
	if st := m.Rmdir(ctx, parent2, "f6"); st != 0 {
		t.Fatalf("rmdir d4/f6 : %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d4"); st != 0 {
		t.Fatalf("rmdir d4 first : %s", st)
	}
	if st := m.Unlink(ctx, 1, "d5"); st != 0 {
		t.Fatalf("rmdir d6 : %s", st)
	}
	if st := m.Lookup(ctx, 1, "f", &inode, attr); st != 0 || inode != parent {
		t.Fatalf("lookup f: %s; inode %d expect %d", st, inode, parent)
	}
	if st := m.Rename(ctx, 1, "d", 1, "f", RenameExchange, &inode, attr); st != 0 {
		t.Fatalf("rename d <-> f: %s", st)
	}
	if st := m.Link(ctx, inode, 1, "f3", attr); st != 0 {
		t.Fatalf("link f3 -> f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f3")
	if st := m.Link(ctx, inode, 1, "F3", attr); st != 0 { // CaseInsensi = false
		t.Fatalf("link F3 -> f: %s", st)
	}
	_ = m.Unlink(ctx, 1, "F3")
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
	// try to open a file that does not exist
	if st := m.Open(ctx, 99999, 2, &Attr{}); st != syscall.ENOENT {
		t.Fatalf("open not exist inode got %d, expected %d", st, syscall.ENOENT)
	}
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
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocKeepSize, 100, 50); st != 0 {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocCollapesRange, 100, 50); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocInsertRange, 100, 50); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocCollapesRange, 100, 50); st != syscall.ENOTSUP {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole, 100, 50); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocKeepSize, 0, 0); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, parent, fallocPunchHole|fallocKeepSize, 100, 50); st != syscall.EPERM {
		t.Fatalf("fallocate dir: %s", st)
	}
	if st := m.Read(ctx, inode, 0, &chunks); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(chunks) != 3 || chunks[1].Chunkid != 0 || chunks[1].Len != 50 || chunks[2].Chunkid != chunkid || chunks[2].Len != 50 {
		t.Fatalf("chunks: %v", chunks)
	}

	// xattr
	if st := m.SetXattr(ctx, inode, "a", []byte("v"), XattrCreateOrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v2"), XattrCreateOrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	var value []byte
	if st := m.GetXattr(ctx, inode, "a", &value); st != 0 || string(value) != "v2" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.ListXattr(ctx, inode, &value); st != 0 || string(value) != "a\000" {
		t.Fatalf("listxattr: %s %v", st, value)
	}
	if st := m.RemoveXattr(ctx, inode, "a"); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v"), XattrReplace); st != ENOATTR {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrCreate); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrCreate); st != syscall.EEXIST {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v4"), XattrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v5"), 5); st != 0 { // unknown flag is ignored
		t.Fatalf("setxattr: %s", st)
	}

	var totalspace, availspace, iused, iavail uint64
	if st := m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	var summary Summary
	if st := GetSummary(m, ctx, 1, &summary); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected := Summary{Length: 402, Size: 20480, Files: 3, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := GetSummary(m, ctx, inode, &summary); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 602, Size: 24576, Files: 4, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir d: %s", st)
	}
}

func TestStickyBitRedis(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1:6379/5", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.(*redisMeta).rdb.FlushDB(context.Background())
	testStickyBit(t, m)
}

func testStickyBit(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var sticky, normal, inode Ino
	var attr = &Attr{}
	m.Mkdir(ctx, 1, "tmp", 01777, 0, 0, &sticky, attr)
	m.Mkdir(ctx, 1, "tmp2", 0777, 0, 0, &normal, attr)
	ctxA := NewContext(1, 1, []uint32{1})
	// file
	m.Create(ctxA, sticky, "f", 0777, 0, 0, &inode, attr)
	m.Create(ctxA, normal, "f", 0777, 0, 0, &inode, attr)
	ctxB := NewContext(1, 2, []uint32{1})
	if e := m.Unlink(ctxB, sticky, "f"); e != syscall.EACCES {
		t.Fatalf("unlink f: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "f", normal, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	m.Create(ctxB, sticky, "f2", 0777, 0, 0, &inode, attr)
	if e := m.Rename(ctxB, sticky, "f2", sticky, "f", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("overwrite f: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxA, normal, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "f", sticky, "f3", 0, &inode, attr); e != 0 {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Unlink(ctxA, sticky, "f3"); e != 0 {
		t.Fatalf("unlink f3: %s", e)
	}
	// dir
	m.Mkdir(ctxA, sticky, "d", 0777, 0, 0, &inode, attr)
	m.Mkdir(ctxA, normal, "d", 0777, 0, 0, &inode, attr)
	if e := m.Rmdir(ctxB, sticky, "d"); e != syscall.EACCES {
		t.Fatalf("rmdir d: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "d", normal, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	m.Mkdir(ctxB, sticky, "d2", 0777, 0, 0, &inode, attr)
	if e := m.Rename(ctxB, sticky, "d2", sticky, "d", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("overwrite d: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxA, normal, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "d", sticky, "d3", 0, &inode, attr); e != 0 {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rmdir(ctxA, sticky, "d3"); e != 0 {
		t.Fatalf("rmdir d3: %s", e)
	}
}

func TestLocksRedis(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1:6379/5", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	testLocks(t, m)
}

func testLocks(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	// flock
	o1 := uint64(0xF000000000000001)
	if st := m.Flock(ctx, inode, o1, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock again: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}

	// POSIX locks
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_RDLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_RDLCK, 0, 0x2FFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != syscall.EAGAIN {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0x10000, 0x20000, 1); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_UNLCK, 0, 0x20000, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0, 0xFFFF, 10); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0x2000, 0xFFFF, 20); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != syscall.EAGAIN {
		t.Fatalf("plock rlock: %s", st)
	}
	var ltype, pid uint32 = syscall.F_WRLCK, 1
	var start, end uint64 = 0x2000, 0xFFFF
	if st := m.Getlk(ctx, inode, o1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_WRLCK || pid != 20 || start != 0x2000 || end != 0xFFFF {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_UNLCK, 0, 0x2FFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	ltype = syscall.F_WRLCK
	start, end = 0, 0xFFFFFF
	if st := m.Getlk(ctx, inode, o1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_UNLCK || pid != 0 || start != 0 || end != 0 {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}

	// concurrent locks
	var g sync.WaitGroup
	var count int
	var err syscall.Errno
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			if st := m.Setlk(ctx, inode, uint64(i), true, syscall.F_WRLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				err = st
			}
			count++
			time.Sleep(time.Millisecond)
			count--
			if count > 0 {
				logger.Fatalf("count should be be zero but got %d", count)
			}
			if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_UNLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				logger.Fatalf("plock unlock: %s", st)
				err = st
			}
		}(i)
	}
	g.Wait()
	if err != 0 {
		t.Fatalf("lock fail: %s", err)
	}
}

func TestRemove(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1:6379/5", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	testRemove(t, m)
}

func testRemove(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := Remove(m, ctx, 1, "f"); st != 0 {
		t.Fatalf("rmr f: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0755, 0, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d2", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/d2: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/f: %s", st)
	}
	if st := Remove(m, ctx, 1, "d"); st != 0 {
		t.Fatalf("rmr d: %s", st)
	}
}

func TestCaseIncensi(t *testing.T) {
	var conf = Config{CaseInsensi: true}
	m, err := newRedisMeta("redis", "127.0.0.1:6379/6", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	testCaseIncensi(t, m)
}

func testCaseIncensi(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Create(ctx, 1, "foo", 0755, 0, 0, &inode, attr)
	if st := m.Create(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create Foo should be ok")
	}
	if st := m.Create(ctx, 1, "Foo", 0755, 0, syscall.O_EXCL, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("create should fail with EEXIST")
	}
	if st := m.Lookup(ctx, 1, "Foo", &inode, attr); st != 0 {
		t.Fatalf("lookup Foo should be OK")
	}
	if st := m.Rename(ctx, 1, "Foo", 1, "bar", 0, &inode, attr); st != 0 {
		t.Fatalf("rename Foo to bar should be OK, but got %s", st)
	}
	if st := m.Create(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create Foo should be OK")
	}
	if st := m.Lookup(ctx, 1, "Bar", &inode, attr); st != 0 {
		t.Fatalf("lookup Bar should be OK")
	}
	if st := m.Link(ctx, inode, 1, "foo", attr); st != syscall.EEXIST {
		t.Fatalf("link should fail with EEXIST")
	}
	if st := m.Unlink(ctx, 1, "Bar"); st != 0 {
		t.Fatalf("unlink Bar should be OK")
	}
	if st := m.Unlink(ctx, 1, "foo"); st != 0 {
		t.Fatalf("unlink foo should be OK")
	}
	if st := m.Mkdir(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("mkdir Foo should be OK, but got %s", st)
	}
	if st := m.Rmdir(ctx, 1, "foo"); st != 0 {
		t.Fatalf("rmdir foo should be OK")
	}
}

func TestCompaction(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1:6379/8", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	testCompaction(t, m)
}

type compactor interface {
	compactChunk(inode Ino, indx uint32, force bool)
}

func testCompaction(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	var l sync.Mutex
	deleted := make(map[uint64]int)
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		l.Lock()
		chunkid := args[0].(uint64)
		deleted[chunkid] = 1
		l.Unlock()
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f")
	}()

	// random write
	var chunkid uint64
	m.NewChunk(ctx, inode, 0, 0, &chunkid)
	_ = m.Write(ctx, inode, 1, uint32(0), Slice{Chunkid: chunkid, Size: 64 << 20, Len: 64 << 20})
	m.NewChunk(ctx, inode, 0, 0, &chunkid)
	_ = m.Write(ctx, inode, 1, uint32(30<<20), Slice{Chunkid: chunkid, Size: 8, Len: 8})
	m.NewChunk(ctx, inode, 0, 0, &chunkid)
	_ = m.Write(ctx, inode, 1, uint32(40<<20), Slice{Chunkid: chunkid, Size: 8, Len: 8})
	var cs1 []Slice
	_ = m.Read(ctx, inode, 1, &cs1)
	if len(cs1) != 5 {
		t.Fatalf("expect 5 slices, but got %+v", cs1)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 1, true)
	}
	var cs []Slice
	_ = m.Read(ctx, inode, 1, &cs)
	if len(cs) != 1 {
		t.Fatalf("expect 1 slice, but got %+v", cs)
	}

	// append
	var size uint32 = 100000
	for i := 0; i < 200; i++ {
		var chunkid uint64
		m.NewChunk(ctx, inode, 0, 0, &chunkid)
		if st := m.Write(ctx, inode, 0, uint32(i)*size, Slice{Chunkid: chunkid, Size: size, Len: size}); st != 0 {
			t.Fatalf("write %d: %s", i, st)
		}
		time.Sleep(time.Millisecond)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, true)
	}
	var chunks []Slice
	if st := m.Read(ctx, inode, 0, &chunks); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(chunks) >= 10 {
		t.Fatalf("inode %d should be compacted, but have %d slices", inode, len(chunks))
	}
	var total uint32
	for _, s := range chunks {
		total += s.Len
	}
	if total != size*200 {
		t.Fatalf("size of slice should be %d, but got %d", size*200, total)
	}

	// TODO: check result if that's predictable
	if st := m.CompactAll(ctx); st != 0 {
		logger.Fatalf("compactall: %s", st)
	}
	var slices []Slice
	if st := m.ListSlices(ctx, &slices, false, nil); st != 0 {
		logger.Fatalf("list all slices: %s", st)
	}

	l.Lock()
	deletes := len(deleted)
	l.Unlock()
	if deletes < 30 {
		t.Fatalf("deleted chunks %d is less then 30", deletes)
	}
}

func TestConcurrentWrite(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1/9", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	testConcurrentWrite(t, m)
}

func testConcurrentWrite(t *testing.T, m Meta) {
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)

	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "f")

	var errno syscall.Errno
	var g sync.WaitGroup
	for i := 0; i <= 10; i++ {
		g.Add(1)
		go func(indx uint32) {
			defer g.Done()
			for j := 0; j < 100; j++ {
				var chunkid uint64
				m.NewChunk(ctx, inode, indx, 0, &chunkid)
				var slice = Slice{Chunkid: chunkid, Size: 100, Len: 100}
				st := m.Write(ctx, inode, indx, 0, slice)
				if st != 0 {
					errno = st
					break
				}
			}
		}(uint32(i))
	}
	g.Wait()
	if errno != 0 {
		t.Fatal()
	}
}

func TestTruncateAndDelete(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1/10", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.(*redisMeta).rdb.FlushDB(context.Background())
	testTruncateAndDelete(t, m)
}

func testTruncateAndDelete(t *testing.T, m Meta) {
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)

	ctx := Background
	var inode Ino
	var attr = &Attr{}
	m.Unlink(ctx, 1, "f")
	if st := m.Truncate(ctx, 1, 0, 4<<10, attr); st != syscall.EPERM {
		t.Fatalf("truncate dir %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	var cid uint64
	if st := m.NewChunk(ctx, inode, 0, 100, &cid); st != 0 {
		t.Fatalf("new chunk: %s", st)
	}
	if st := m.Write(ctx, inode, 0, 100, Slice{cid, 100, 0, 100}); st != 0 {
		t.Fatalf("write file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, 200<<20, attr); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, (10<<40)+10, attr); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, (300<<20)+10, attr); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	var total int64
	process := mpb.New(mpb.WithWidth(32), mpb.WithOutput(logger.WriterLevel(logrus.InfoLevel)))
	bar := process.AddSpinner(total,
		mpb.PrependDecorators(
			// display our name with one space on the right
			decor.Name("listed slices counter:", decor.WC{W: len("listed slices counter:") + 1, C: decor.DidentRight}),
			decor.CurrentNoUnit("%d"),
		),
		mpb.BarFillerClearOnComplete(),
	)
	var ss []Slice
	m.ListSlices(ctx, &ss, false, func() {
		bar.SetTotal(total+2048, false)
		bar.Increment()
	})
	if len(ss) != 1 {
		t.Fatalf("number of chunks: %d != 1, %+v", len(ss), ss)
	}
	m.Close(ctx, inode)
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink file %s", st)
	}

	time.Sleep(time.Millisecond * 100)
	m.ListSlices(ctx, &ss, false, nil)
	// the last chunk could be found and deleted
	if len(ss) > 1 {
		t.Fatalf("number of chunks: %d > 1, %+v", len(ss), ss)
	}
}

func TestCopyFileRange(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1/10", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.(*redisMeta).rdb.FlushDB(context.Background())
	testCopyFileRange(t, m)
}

func testCopyFileRange(t *testing.T, m Meta) {
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)

	ctx := Background
	var iin, iout Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "fin")
	_ = m.Unlink(ctx, 1, "fout")
	if st := m.Create(ctx, 1, "fin", 0650, 022, 0, &iin, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fin")
	if st := m.Create(ctx, 1, "fout", 0650, 022, 0, &iout, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fout")
	m.Write(ctx, iin, 0, 100, Slice{10, 200, 0, 100})
	m.Write(ctx, iin, 1, 100<<10, Slice{11, 40 << 20, 0, 40 << 20})
	m.Write(ctx, iin, 3, 0, Slice{12, 63 << 20, 10 << 20, 30 << 20})
	m.Write(ctx, iout, 2, 10<<20, Slice{13, 50 << 20, 10 << 20, 30 << 20})
	var copied uint64
	if st := m.CopyFileRange(ctx, iin, 150, iout, 30<<20, 200<<20, 0, &copied); st != 0 {
		t.Fatalf("copy file range: %s", st)
	}
	var expected uint64 = 200 << 20
	if copied != expected {
		t.Fatalf("expect copy %d bytes, but got %d", expected, copied)
	}
	var expectedChunks = [][]Slice{
		{{0, 30 << 20, 0, 30 << 20}, {10, 200, 50, 50}, {0, 0, 200, ChunkSize - 30<<20 - 50}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {0, 0, 0, 100 << 10}, {11, 40 << 20, 0, (34 << 20) + 150 - (100 << 10)}},
		{{11, 40 << 20, (34 << 20) + 150 - (100 << 10), 6<<20 - 150 + 100<<10}, {0, 0, 40<<20 + 100<<10, ChunkSize - 40<<20 - 100<<10}, {0, 0, 0, 150 + (ChunkSize - 30<<20)}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {12, 63 << 20, 10 << 20, (8 << 20) + 150}},
	}
	for i := uint32(0); i < 4; i++ {
		var chunks []Slice
		if st := m.Read(ctx, iout, i, &chunks); st != 0 {
			t.Fatalf("read chunk %d: %s", i, st)
		}
		if len(chunks) != len(expectedChunks[i]) {
			t.Fatalf("expect chunk %d: %+v, but got %+v", i, expectedChunks[i], chunks)
		}
		for j, s := range chunks {
			if s != expectedChunks[i][j] {
				t.Fatalf("expect slice %d,%d: %+v, but got %+v", i, j, expectedChunks[i][j], s)
			}
		}
	}
}

func TestCloseSessionRedis(t *testing.T) {
	var conf Config
	m, err := newRedisMeta("redis", "127.0.0.1/10", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.(*redisMeta).rdb.FlushDB(context.Background())
	testCloseSession(t, m)
}

func testCloseSession(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, true)
	if err := m.NewSession(); err != nil {
		t.Fatalf("new session: %s", err)
	}

	ctx := Background
	var inode Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Flock(ctx, inode, 1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 1, false, syscall.F_WRLCK, 0x10000, 0x20000, 1); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Open(ctx, inode, 2, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	var sid uint64
	switch m := m.(type) {
	case *redisMeta:
		sid = uint64(m.sid)
	case *dbMeta:
		sid = m.sid
	case *kvMeta:
		sid = m.sid
	}
	s, err := m.GetSession(sid)
	if err != nil {
		t.Fatalf("get session: %s", err)
	} else {
		if len(s.Flocks) != 1 || len(s.Plocks) != 1 || len(s.Sustained) != 1 {
			t.Fatalf("incorrect session: flock %d plock %d sustained %d", len(s.Flocks), len(s.Plocks), len(s.Sustained))
		}
	}
	if err = m.CloseSession(); err != nil {
		t.Fatalf("close session: %s", err)
	}
	if _, err = m.GetSession(sid); err == nil {
		t.Fatalf("get a deleted session: %s", err)
	}
	switch m := m.(type) {
	case *redisMeta:
		s, err = m.getSession(strconv.FormatUint(sid, 10), true)
	case *dbMeta:
		s, err = m.getSession(&session{Sid: sid}, true)
	case *kvMeta:
		s, err = m.getSession(sid, true)
	}
	if err != nil {
		t.Fatalf("get session: %s", err)
	}
	var empty SessionInfo
	if s.SessionInfo != empty {
		t.Fatalf("incorrect session info %+v", s.SessionInfo)
	}
	if len(s.Flocks) != 0 || len(s.Plocks) != 0 || len(s.Sustained) != 0 {
		t.Fatalf("incorrect session: flock %d plock %d sustained %d", len(s.Flocks), len(s.Plocks), len(s.Sustained))
	}
}
