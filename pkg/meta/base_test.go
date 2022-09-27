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
	"bytes"
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/juicedata/juicefs/pkg/utils"
	"xorm.io/xorm"
)

func TestRedisClient(t *testing.T) {
	var conf = Config{}
	m, err := newRedisMeta("redis", "127.0.0.1:6379/10", &conf)
	if err != nil || m.Name() != "redis" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestRedisCluster(t *testing.T) {
	var conf = Config{}
	m, err := newRedisMeta("redis", "127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003/2", &conf)
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func testMeta(t *testing.T, m Meta) {
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	testMetaClient(t, m)
	testTruncateAndDelete(t, m)
	testTrash(t, m)
	testParents(t, m)
	testRemove(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m, false)
	time.Sleep(time.Second)
	testCompaction(t, m, true)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	testConcurrentDir(t, m)
	testAttrFlags(t, m)
	base := m.getBase()
	base.conf.OpenCache = time.Second
	base.of.expire = time.Second
	testOpenCache(t, m)
	base.conf.CaseInsensi = true
	testCaseIncensi(t, m)
	testCheckAndRepair(t, m)
	base.conf.ReadOnly = true
	testReadOnly(t, m)
}

func testMetaClient(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error { return nil })
	ctx := Background
	var attr = &Attr{}
	if st := m.GetAttr(ctx, 1, attr); st != 0 || attr.Mode != 0777 { // getattr of root always succeed
		t.Fatalf("getattr root: %s", st)
	}

	if err := m.Init(Format{Name: "test"}, true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	if err := m.Init(Format{Name: "test2"}, false); err == nil { // not allowed
		t.Fatalf("change name without --force is not allowed")
	}
	format, err := m.Load(true)
	if err != nil {
		t.Fatalf("load failed after initialization: %s", err)
	}
	if format.Name != "test" {
		t.Fatalf("load got volume name %s, expected %s", format.Name, "test")
	}
	if err = m.NewSession(); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()
	ses, err := m.ListSessions()
	if err != nil || len(ses) != 1 {
		t.Fatalf("list sessions %+v: %s", ses, err)
	}
	base := m.getBase()
	if base.sid != ses[0].Sid {
		t.Fatalf("my sid %d != registered sid %d", base.sid, ses[0].Sid)
	}
	go m.CleanStaleSessions()

	var parent, inode, dummyInode Ino
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
	if st := m.Lookup(ctx, 1, "d", &parent, nil); st != syscall.EINVAL {
		t.Fatalf("lookup d: %s", st)
	}
	if st := m.Lookup(ctx, 1, "..", &inode, attr); st != 0 || inode != 1 {
		t.Fatalf("lookup ..: %s", st)
	}
	if st := m.Lookup(ctx, parent, ".", &inode, attr); st != 0 || inode != parent {
		t.Fatalf("lookup .: %s", st)
	}
	if st := m.Lookup(ctx, parent, "..", &inode, attr); st != 0 || inode != 1 {
		t.Fatalf("lookup ..: %s", st)
	}
	if attr.Nlink != 3 {
		t.Fatalf("nlink expect 3, but got %d", attr.Nlink)
	}
	if st := m.Access(ctx, parent, 4, attr); st != 0 {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	_ = m.Close(ctx, inode)
	var tino Ino
	if st := m.Lookup(ctx, inode, ".", &tino, attr); st != 0 {
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
	if st := m.Mknod(ctx, inode, "df", TypeFile, 0650, 022, 0, "", &dummyInode, nil); st != syscall.ENOTDIR {
		t.Fatalf("create fd: %s", st)
	}
	if st := m.Mknod(ctx, parent, "f", TypeFile, 0650, 022, 0, "", &inode, attr); st != syscall.EEXIST {
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
	if st := m.Mkdir(ctx2, 1, "d1", 02755, 022, 0, &p1, attr); st != 0 {
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
	attr.Mode = 0640
	if st := m.SetAttr(ctx, inode, SetAttrAtime|SetAttrMtime|SetAttrUID|SetAttrGID|SetAttrMode, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.SetAttr(ctx, inode, 0, 0, attr); st != 0 { // changes nothing
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Atime != 2 || attr.Mtime != 2 || attr.Uid != 1 || attr.Gid != 1 || attr.Mode != 0640 {
		t.Fatalf("atime:%d mtime:%d uid:%d gid:%d mode:%o", attr.Atime, attr.Mtime, attr.Uid, attr.Gid, attr.Mode)
	}
	if st := m.SetAttr(ctx, inode, SetAttrAtimeNow|SetAttrMtimeNow, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	fakeCtx := NewContext(100, 2, []uint32{2, 1})
	if st := m.Access(fakeCtx, parent, 2, nil); st != syscall.EACCES {
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
	_ = m.Close(ctx, inode)
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
	if st := m.Rename(ctx, 1, "d", 1, "f", 0, &inode, attr); st != 0 {
		t.Fatalf("rename d -> f: %s", st)
	}
	if st := m.GetAttr(ctx, 1, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Nlink != 2 {
		t.Fatalf("nlink expect 2, but got %d", attr.Nlink)
	}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
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
	if st := m.Mknod(ctx, parent2, "f6", TypeFile, 0650, 022, 0, "", &inode, attr); st != 0 {
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
	if st := m.Lookup(ctx, 1, "f", &inode, attr); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}
	if st := m.Link(ctx, inode, 1, "f3", attr); st != 0 {
		t.Fatalf("link f3 -> f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f3")
	if st := m.Link(ctx, inode, 1, "F3", attr); st != 0 { // CaseInsensi = false
		t.Fatalf("link F3 -> f: %s", st)
	}
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
	var sliceId uint64
	// try to open a file that does not exist
	if st := m.Open(ctx, 99999, syscall.O_RDWR, &Attr{}); st != syscall.ENOENT {
		t.Fatalf("open not exist inode got %d, expected %d", st, syscall.ENOENT)
	}
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	_ = m.Close(ctx, inode)
	if st := m.NewSlice(ctx, &sliceId); st != 0 {
		t.Fatalf("write chunk: %s", st)
	}
	var s = Slice{Id: sliceId, Size: 100, Len: 100}
	if st := m.Write(ctx, inode, 0, 100, s); st != 0 {
		t.Fatalf("write end: %s", st)
	}
	var slices []Slice
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 2 || slices[0].Id != 0 || slices[0].Size != 100 || slices[1].Id != sliceId || slices[1].Size != 100 {
		t.Fatalf("slices: %v", slices)
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
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 3 || slices[1].Id != 0 || slices[1].Len != 50 || slices[2].Id != sliceId || slices[2].Len != 50 {
		t.Fatalf("slices: %v", slices)
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
	if st := m.Unlink(ctx, 1, "F3"); st != 0 {
		t.Fatalf("unlink F3: %s", st)
	}
	if st := m.GetXattr(ctx, inode, "a", &value); st != 0 || string(value) != "v2" {
		t.Fatalf("getxattr: %s %v", st, value)
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
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrReplace); st != 0 {
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
	if totalspace != 1<<50 || iavail != 10<<20 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}
	format.Capacity = 1 << 20
	format.Inodes = 100
	if err = m.Init(*format, false); err != nil {
		t.Fatalf("set quota failed: %s", err)
	}
	if st := m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20 || iavail != 97 {
		time.Sleep(time.Millisecond * 100)
		_ = m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail)
		if totalspace != 1<<20 || iavail != 97 {
			t.Fatalf("total space %d, iavail %d", totalspace, iavail)
		}
	}
	var summary Summary
	if st := GetSummary(m, ctx, parent, &summary, false); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected := Summary{Length: 0, Size: 4096, Files: 0, Dirs: 1}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	summary = Summary{}
	if st := GetSummary(m, ctx, 1, &summary, true); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 402, Size: 20480, Files: 3, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := GetSummary(m, ctx, inode, &summary, true); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 602, Size: 24576, Files: 4, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	if st := m.Unlink(ctx, 1, "f3"); st != 0 {
		t.Fatalf("unlink f3: %s", st)
	}
	time.Sleep(time.Millisecond * 100) // wait for delete
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 0 {
		t.Fatalf("slices: %v", slices)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir d: %s", st)
	}
}

func testStickyBit(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, false)
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

func testLocks(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, false)
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
	if r, ok := m.(*redisMeta); ok {
		ms, err := r.rdb.SMembers(context.Background(), r.lockedKey(r.sid)).Result()
		if err != nil {
			t.Fatalf("Smember %s: %s", r.lockedKey(r.sid), err)
		}
		if len(ms) != 0 {
			t.Fatalf("locked inodes leaked: %d", len(ms))
		}
	}

	// POSIX locks
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_UNLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
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
				panic(fmt.Errorf("count should be be zero but got %d", count))
			}
			if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_UNLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				panic(fmt.Errorf("plock unlock: %s", st))
			}
		}(i)
	}
	g.Wait()
	if err != 0 {
		t.Fatalf("lock fail: %s", err)
	}

	if r, ok := m.(*redisMeta); ok {
		ms, err := r.rdb.SMembers(context.Background(), r.lockedKey(r.sid)).Result()
		if err != nil {
			t.Fatalf("Smember %s: %s", r.lockedKey(r.sid), err)
		}
		if len(ms) != 0 {
			t.Fatalf("locked inode leaked: %d", len(ms))
		}
	}
}

func testRemove(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, false)
	ctx := Background
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Remove(ctx, 1, "f", nil); st != 0 {
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
	if ps := m.GetPaths(ctx, parent); len(ps) == 0 || ps[0] != "/d" {
		t.Fatalf("get path /d: %v", ps)
	}
	if ps := m.GetPaths(ctx, inode); len(ps) == 0 || ps[0] != "/d/f" {
		t.Fatalf("get path /d/f: %v", ps)
	}
	for i := 0; i < 4096; i++ {
		if st := m.Create(ctx, 1, "f"+strconv.Itoa(i), 0644, 0, 0, &inode, attr); st != 0 {
			t.Fatalf("create f%s: %s", strconv.Itoa(i), st)
		}
	}
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 1, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 4099 {
		t.Fatalf("entries: %d", len(entries))
	}
	if st := m.Remove(ctx, 1, "d", nil); st != 0 {
		t.Fatalf("rmr d: %s", st)
	}
}

func testCaseIncensi(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, false)
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
	if st := m.Resolve(ctx, 1, "/Foo", &inode, attr); st != syscall.ENOTSUP {
		t.Fatalf("resolve with case insensitive should be ENOTSUP")
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

type compactor interface {
	compactChunk(inode Ino, indx uint32, force bool)
}

func testCompaction(t *testing.T, m Meta, trash bool) {
	if trash {
		_ = m.Init(Format{Name: "test", TrashDays: 1}, false)
	} else {
		_ = m.Init(Format{Name: "test"}, false)
	}
	var l sync.Mutex
	deleted := make(map[uint64]int)
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		l.Lock()
		sliceId := args[0].(uint64)
		deleted[sliceId] = 1
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
	var sliceId uint64
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(0), Slice{Id: sliceId, Size: 64 << 20, Len: 64 << 20})
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(30<<20), Slice{Id: sliceId, Size: 8, Len: 8})
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(40<<20), Slice{Id: sliceId, Size: 8, Len: 8})
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
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		if st := m.Write(ctx, inode, 0, uint32(i)*size, Slice{Id: sliceId, Size: size, Len: size}); st != 0 {
			t.Fatalf("write %d: %s", i, st)
		}
		time.Sleep(time.Millisecond)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, true)
	}
	var slices []Slice
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(slices) >= 10 {
		t.Fatalf("inode %d should be compacted, but have %d slices", inode, len(slices))
	}
	var total uint32
	for _, s := range slices {
		total += s.Len
	}
	if total != size*200 {
		t.Fatalf("size of slice should be %d, but got %d", size*200, total)
	}

	// TODO: check result if that's predictable
	p, bar := utils.MockProgress()
	if st := m.CompactAll(ctx, bar); st != 0 {
		t.Fatalf("compactall: %s", st)
	}
	p.Done()
	sliceMap := make(map[Ino][]Slice)
	if st := m.ListSlices(ctx, sliceMap, false, nil); st != 0 {
		t.Fatalf("list all slices: %s", st)
	}

	l.Lock()
	deletes := len(deleted)
	l.Unlock()
	if trash {
		if deletes > 10 {
			t.Fatalf("deleted slices %d is greater than 10", deletes)
		}
		if len(sliceMap[1]) < 200 {
			t.Fatalf("list delayed slices %d is less than 200", len(sliceMap[1]))
		}
		m.(engine).doCleanupDelayedSlices(time.Now().Unix()+1, 1000)
		l.Lock()
		deletes = len(deleted)
		l.Unlock()
	}
	if deletes < 200 {
		t.Fatalf("deleted slices %d is less than 200", deletes)
	}
}

func testConcurrentWrite(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, false)

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
				var sliceId uint64
				m.NewSlice(ctx, &sliceId)
				var slice = Slice{Id: sliceId, Size: 100, Len: 100}
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

func testTruncateAndDelete(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	// remove quota
	format, _ := m.Load(false)
	format.Capacity = 0
	_ = m.Init(*format, false)

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
	var sliceId uint64
	if st := m.NewSlice(ctx, &sliceId); st != 0 {
		t.Fatalf("new chunk: %s", st)
	}
	if st := m.Write(ctx, inode, 0, 100, Slice{sliceId, 100, 0, 100}); st != 0 {
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
	slices := make(map[Ino][]Slice)
	m.ListSlices(ctx, slices, false, func() { total++ })
	var totalSlices int
	for _, ss := range slices {
		totalSlices += len(ss)
	}
	if totalSlices != 1 {
		t.Fatalf("number of slices: %d != 1, %+v", totalSlices, slices)
	}
	_ = m.Close(ctx, inode)
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink file %s", st)
	}

	time.Sleep(time.Millisecond * 100)
	slices = make(map[Ino][]Slice)
	m.ListSlices(ctx, slices, false, nil)
	totalSlices = 0
	for _, ss := range slices {
		totalSlices += len(ss)
	}
	// the last chunk could be found and deleted
	if totalSlices > 1 {
		t.Fatalf("number of slices: %d > 1, %+v", totalSlices, slices)
	}
}

func testCopyFileRange(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, false)

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
	var expectedSlices = [][]Slice{
		{{0, 30 << 20, 0, 30 << 20}, {10, 200, 50, 50}, {0, 0, 200, ChunkSize - 30<<20 - 50}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {0, 0, 0, 100 << 10}, {11, 40 << 20, 0, (34 << 20) + 150 - (100 << 10)}},
		{{11, 40 << 20, (34 << 20) + 150 - (100 << 10), 6<<20 - 150 + 100<<10}, {0, 0, 40<<20 + 100<<10, ChunkSize - 40<<20 - 100<<10}, {0, 0, 0, 150 + (ChunkSize - 30<<20)}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {12, 63 << 20, 10 << 20, (8 << 20) + 150}},
	}
	for i := uint32(0); i < 4; i++ {
		var slices []Slice
		if st := m.Read(ctx, iout, i, &slices); st != 0 {
			t.Fatalf("read chunk %d: %s", i, st)
		}
		if len(slices) != len(expectedSlices[i]) {
			t.Fatalf("expect chunk %d: %+v, but got %+v", i, expectedSlices[i], slices)
		}
		for j, s := range slices {
			if s != expectedSlices[i][j] {
				t.Fatalf("expect slice %d,%d: %+v, but got %+v", i, j, expectedSlices[i][j], s)
			}
		}
	}
}

func testCloseSession(t *testing.T, m Meta) {
	_ = m.Init(Format{Name: "test"}, false)
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
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	sid := m.getBase().sid
	s, err := m.GetSession(sid, true)
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
	if _, err = m.GetSession(sid, true); err == nil {
		t.Fatalf("get a deleted session: %s", err)
	}
	switch m := m.(type) {
	case *redisMeta:
		s, err = m.getSession(strconv.FormatUint(sid, 10), true)
	case *dbMeta:
		s, err = m.getSession(&session2{Sid: sid, Info: []byte("{}")}, true)
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

func testTrash(t *testing.T, m Meta) {
	if err := m.Init(Format{Name: "test", TrashDays: 1}, false); err != nil {
		t.Fatalf("init: %s", err)
	}
	ctx := Background
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f1", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f1: %s", st)
	}
	if st := m.Create(ctx, 1, "f2", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f2: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f1", 1, "d", 0, &inode, attr); st != syscall.ENOTEMPTY {
		t.Fatalf("rename f1 -> d: %s", st)
	}
	if st := m.Unlink(ctx, parent, "f"); st != 0 {
		t.Fatalf("unlink d/f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("getattr f(%d): %s, attr %+v", inode, st, attr)
	}
	if st := m.Mkdir(ctx, 1, "d2", 0755, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("mkdir d2: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d2"); st != 0 {
		t.Fatalf("rmdir d2: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("getattr d2(%d): %s, attr %+v", inode, st, attr)
	}
	if st := m.Rename(ctx, 1, "f1", 1, "d", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f1 -> d: %s", st)
	}
	if st := m.Lookup(ctx, TrashInode+1, fmt.Sprintf("1-%d-d", parent), &inode, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("lookup subTrash/d: %s, attr %+v", st, attr)
	}
	if st := m.Rename(ctx, 1, "f2", TrashInode, "td", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename f2 -> td: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", TrashInode+1, "td", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename f2 -> td: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "d", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> d: %s", st)
	}
	if st := m.Unlink(ctx, 1, "d"); st != 0 {
		t.Fatalf("unlink d: %s", st)
	}
	lname := strings.Repeat("f", MaxName)
	if st := m.Create(ctx, 1, lname, 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create %s: %s", lname, st)
	}
	if st := m.Unlink(ctx, 1, lname); st != 0 {
		t.Fatalf("unlink %s: %s", lname, st)
	}
	tname := fmt.Sprintf("1-%d-%s", inode, lname)[:MaxName]
	if st := m.Lookup(ctx, TrashInode+1, tname, &inode, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("lookup subTrash/%s: %s, attr %+v", tname, st, attr)
	}
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %d", len(entries))
	}
	entries = entries[:0]
	if st := m.Readdir(ctx, TrashInode+1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 8 {
		t.Fatalf("entries: %d", len(entries))
	}
	ctx2 := NewContext(1000, 1, []uint32{1})
	if st := m.Unlink(ctx2, TrashInode+1, "d"); st != syscall.EPERM {
		t.Fatalf("unlink d: %s", st)
	}
	if st := m.Rmdir(ctx2, TrashInode+1, "d"); st != syscall.EPERM {
		t.Fatalf("rmdir d: %s", st)
	}
	if st := m.Rename(ctx2, TrashInode+1, "d", 1, "f", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename d -> f: %s", st)
	}
	m.getBase().doCleanupTrash(true)
	if st := m.GetAttr(ctx2, TrashInode+1, attr); st != syscall.ENOENT {
		t.Fatalf("getattr: %s", st)
	}
}

func testParents(t *testing.T, m Meta) {
	if err := m.Init(Format{Name: "test"}, false); err != nil {
		t.Fatalf("init: %s", err)
	}
	ctx := Background
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if attr.Parent != 1 {
		t.Fatalf("expect parent 1, but got %d", attr.Parent)
	}
	checkParents := func(inode Ino, expect map[Ino]int) {
		if ps := m.GetParents(ctx, inode); ps == nil {
			t.Fatalf("get parents of inode %d returns nil", inode)
		} else if !reflect.DeepEqual(ps, expect) {
			t.Fatalf("expect parents %v, but got %v", expect, ps)
		}
	}
	checkParents(inode, map[Ino]int{1: 1})

	if st := m.Link(ctx, inode, 1, "l1", attr); st != 0 {
		t.Fatalf("link l1 -> f: %s", st)
	}
	if attr.Parent != 0 {
		t.Fatalf("expect parent 0, but got %d", attr.Parent)
	}
	checkParents(inode, map[Ino]int{1: 2})

	if st := m.Mkdir(ctx, 1, "d", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Link(ctx, inode, parent, "l2", attr); st != 0 {
		t.Fatalf("link l2 -> f: %s", st)
	}
	if st := m.Link(ctx, inode, parent, "l3", attr); st != 0 {
		t.Fatalf("link l3 -> f: %s", st)
	}
	checkParents(inode, map[Ino]int{1: 2, parent: 2})

	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	if st := m.Create(ctx, 1, "f2", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f2: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "l1", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> l1: %s", st)
	}
	if st := m.Lookup(ctx, parent, "l2", &inode, attr); st != 0 {
		t.Fatalf("lookup d/l2: %s", st)
	}
	if attr.Parent != 0 {
		t.Fatalf("expect parent 0, but got %d", attr.Parent)
	}
	if st := m.Unlink(ctx, parent, "l2"); st != 0 {
		t.Fatalf("unlink d/l2: %s", st)
	}
	checkParents(inode, map[Ino]int{parent: 1})

	// clean up
	if st := m.Unlink(ctx, 1, "l1"); st != 0 {
		t.Fatalf("unlink l1: %s", st)
	}
	if st := m.Unlink(ctx, parent, "l3"); st != 0 {
		t.Fatalf("unlink d/l3: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir d: %s", st)
	}
}

func testOpenCache(t *testing.T, m Meta) {
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	defer m.Close(ctx, inode)

	var attr2 = &Attr{}
	if st := m.GetAttr(ctx, inode, attr2); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if *attr != *attr2 {
		t.Fatalf("attrs not the same: attr %+v; attr2 %+v", *attr, *attr2)
	}
	attr2.Uid = 1
	if st := m.SetAttr(ctx, inode, SetAttrUID, 0, attr2); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Uid != 1 {
		t.Fatalf("attr uid should be 1: %+v", *attr)
	}
}

func testReadOnly(t *testing.T, m Meta) {
	ctx := Background
	if err := m.NewSession(); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	var inode Ino
	var attr = &Attr{}
	if st := m.GetAttr(ctx, 1, attr); st != 0 {
		t.Fatalf("getattr 1: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &inode, attr); st != syscall.EROFS {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != syscall.EROFS {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != syscall.EROFS {
		t.Fatalf("open f: %s", st)
	}
}

func testConcurrentDir(t *testing.T, m Meta) {
	ctx := Background
	var g sync.WaitGroup
	var err error
	format, err := m.Load(false)
	format.Capacity = 0
	format.Inodes = 0
	if err = m.Init(*format, false); err != nil {
		t.Fatalf("set quota failed: %s", err)
	}
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			var d1, d2 Ino
			var attr = new(Attr)
			if st := m.Mkdir(ctx, 1, "d1", 0640, 022, 0, &d1, attr); st != 0 && st != syscall.EEXIST {
				panic(fmt.Errorf("mkdir d1: %s", st))
			} else if st == syscall.EEXIST {
				st = m.Lookup(ctx, 1, "d1", &d1, attr)
				if st != 0 {
					panic(fmt.Errorf("lookup d1: %s", st))
				}
			}
			if st := m.Mkdir(ctx, 1, "d2", 0640, 022, 0, &d2, attr); st != 0 && st != syscall.EEXIST {
				panic(fmt.Errorf("mkdir d2: %s", st))
			} else if st == syscall.EEXIST {
				st = m.Lookup(ctx, 1, "d2", &d2, attr)
				if st != 0 {
					panic(fmt.Errorf("lookup d2: %s", st))
				}
			}
			name := fmt.Sprintf("file%d", i)
			var f Ino
			if st := m.Create(ctx, d1, name, 0664, 0, 0, &f, attr); st != 0 {
				panic(fmt.Errorf("create d1/%s: %s", name, st))
			}
			if st := m.Rename(ctx, d1, name, d2, name, 0, &f, attr); st != 0 {
				panic(fmt.Errorf("rename d1/%s -> d2/%s: %s", name, name, st))
			}
		}(i)
	}
	g.Wait()
	if err != nil {
		t.Fatalf("concurrent dir: %s", err)
	}
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			var d2 Ino
			var attr = new(Attr)
			st := m.Lookup(ctx, 1, "d2", &d2, attr)
			if st != 0 {
				panic(fmt.Errorf("lookup d2: %s", st))
			}
			name := fmt.Sprintf("file%d", i)
			if st := m.Unlink(ctx, d2, name); st != 0 {
				panic(fmt.Errorf("unlink d2/%s: %s", name, st))
			}
			if st := m.Rmdir(ctx, 1, "d1"); st != 0 && st != syscall.ENOTEMPTY && st != syscall.ENOENT {
				panic(fmt.Errorf("rmdir d1: %s", st))
			}
			if st := m.Rmdir(ctx, 1, "d2"); st != 0 && st != syscall.ENOTEMPTY && st != syscall.ENOENT {
				panic(fmt.Errorf("rmdir d2: %s", st))
			}
		}(i)
	}
	g.Wait()
}

func testAttrFlags(t *testing.T, m Meta) {
	ctx := Background
	var attr = &Attr{}
	var inode Ino
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, inode, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY|syscall.O_APPEND, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	attr.Flags = FlagAppend | FlagImmutable
	if st := m.SetAttr(ctx, inode, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY|syscall.O_APPEND, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}

	var d Ino
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &d, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, d, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, d, "f", 0644, 022, 0, &inode, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Unlink(ctx, d, "f"); st != syscall.EPERM {
		t.Fatalf("unlink f: %s", st)
	}
	attr.Flags = FlagAppend | FlagImmutable
	if st := m.SetAttr(ctx, d, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, d, "f2", 0644, 022, 0, &inode, nil); st != syscall.EPERM {
		t.Fatalf("create f2: %s", st)
	}

	var Immutable Ino
	if st := m.Mkdir(ctx, 1, "ImmutFile", 0640, 022, 0, &Immutable, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, Immutable, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, Immutable, "f2", 0644, 022, 0, &inode, nil); st != syscall.EPERM {
		t.Fatalf("create f2: %s", st)
	}

	var src1, dst1, mfile Ino
	attr.Flags = 0
	if st := m.Mkdir(ctx, 1, "src1", 0640, 022, 0, &src1, attr); st != 0 {
		t.Fatalf("mkdir src1: %s", st)
	}
	if st := m.Create(ctx, src1, "mfile", 0644, 022, 0, &mfile, nil); st != 0 {
		t.Fatalf("create mfile: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "dst1", 0640, 022, 0, &dst1, attr); st != 0 {
		t.Fatalf("mkdir dst1: %s", st)
	}

	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, src1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, src1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	if st := m.SetAttr(ctx, dst1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	var delFile Ino
	if st := m.Create(ctx, 1, "delfile", 0644, 022, 0, &delFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagImmutable | FlagAppend
	if st := m.SetAttr(ctx, delFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Unlink(ctx, 1, "delfile"); st != syscall.EPERM {
		t.Fatalf("unlink f: %s", st)
	}

	var fallocFile Ino
	if st := m.Create(ctx, 1, "fallocfile", 0644, 022, 0, &fallocFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, fallocFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize, 0, 1024); st != 0 {
		t.Fatalf("fallocate f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize|fallocZeroRange, 0, 1024); st != syscall.EPERM {
		t.Fatalf("fallocate f: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, fallocFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize, 0, 1024); st != syscall.EPERM {
		t.Fatalf("fallocate f: %s", st)
	}

	var copysrcFile, copydstFile Ino
	if st := m.Create(ctx, 1, "copysrcfile", 0644, 022, 0, &copysrcFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Create(ctx, 1, "copydstfile", 0644, 022, 0, &copydstFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Fallocate(ctx, copysrcFile, 0, 0, 1024); st != 0 {
		t.Fatalf("fallocate f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, copydstFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.CopyFileRange(ctx, copysrcFile, 0, copydstFile, 0, 1024, 0, nil); st != syscall.EPERM {
		t.Fatalf("copy_file_range f: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, copydstFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.CopyFileRange(ctx, copysrcFile, 0, copydstFile, 0, 1024, 0, nil); st != syscall.EPERM {
		t.Fatalf("copy_file_range f: %s", st)
	}
}

func setAttr(t *testing.T, m Meta, inode Ino, attr *Attr) {
	var err error
	switch m := m.(type) {
	case *redisMeta:
		err = m.txn(Background, func(tx *redis.Tx) error {
			return tx.Set(Background, m.inodeKey(inode), m.marshal(attr), 0).Err()
		}, m.inodeKey(inode))
	case *dbMeta:
		err = m.txn(func(s *xorm.Session) error {
			_, err = s.ID(inode).AllCols().Update(&node{
				Inode:  inode,
				Type:   attr.Typ,
				Flags:  attr.Flags,
				Mode:   attr.Mode,
				Uid:    attr.Uid,
				Gid:    attr.Gid,
				Mtime:  attr.Mtime * 1e6,
				Ctime:  attr.Ctime * 1e6,
				Atime:  attr.Atime * 1e6,
				Nlink:  attr.Nlink,
				Length: attr.Length,
				Rdev:   attr.Rdev,
				Parent: attr.Parent,
			})
			return err
		})
	case *kvMeta:
		err = m.txn(func(tx kvTxn) error {
			tx.set(m.inodeKey(inode), m.marshal(attr))
			return nil
		})
	}
	if err != nil {
		t.Fatalf("setAttr: %v", err)
	}
}

func testCheckAndRepair(t *testing.T, m Meta) {
	var checkInode, d1Inode, d2Inode, d3Inode, d4Inode Ino
	dirAttr := &Attr{Mode: 0644, Full: true, Typ: TypeDirectory, Nlink: 3}
	if st := m.Mkdir(Background, RootInode, "check", 0640, 022, 0, &checkInode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background, checkInode, "d1", 0640, 022, 0, &d1Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background, d1Inode, "d2", 0640, 022, 0, &d2Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background, d2Inode, "d3", 0640, 022, 0, &d3Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background, d3Inode, "d4", 0640, 022, 0, &d4Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}

	if st := m.GetAttr(Background, checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, checkInode, dirAttr)

	if st := m.GetAttr(Background, d1Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d1Inode, dirAttr)

	if st := m.GetAttr(Background, d2Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d2Inode, dirAttr)

	if st := m.GetAttr(Background, d3Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d3Inode, dirAttr)

	if st := m.GetAttr(Background, d4Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Full = false
	dirAttr.Nlink = 0
	setAttr(t, m, d4Inode, dirAttr)

	if st := m.Check(Background, "/check", false, false); st != 0 {
		t.Fatalf("check: %s", st)
	}
	if st := m.GetAttr(Background, checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 0 {
		t.Fatalf("checkInode nlink should is 0 now: %d", dirAttr.Nlink)
	}

	if st := m.Check(Background, "/check", true, false); st != 0 {
		t.Fatalf("check: %s", st)
	}
	if st := m.GetAttr(Background, checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 3 || dirAttr.Parent != RootInode {
		t.Fatalf("checkInode nlink should is 3 now: %d", dirAttr.Nlink)
	}

	if st := m.Check(Background, "/check/d1/d2", true, false); st != 0 {
		t.Fatalf("check: %s", st)
	}
	if st := m.GetAttr(Background, d2Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 3 || dirAttr.Parent != d1Inode {
		t.Fatalf("d2Inode nlink should is 3 now: %d", dirAttr.Nlink)
	}
	if st := m.GetAttr(Background, d1Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 0 || dirAttr.Parent != checkInode {
		t.Fatalf("d1Inode nlink should is 0 now: %d", dirAttr.Nlink)
	}

	if st := m.Check(Background, "/", true, true); st != 0 {
		t.Fatalf("check: %s", st)
	}
	for _, ino := range []Ino{checkInode, d1Inode, d2Inode, d3Inode} {
		if st := m.GetAttr(Background, ino, dirAttr); st != 0 {
			t.Fatalf("getattr: %s", st)
		}
		if !dirAttr.Full || dirAttr.Nlink != 3 {
			t.Fatalf("nlink should is 3 now: %d", dirAttr.Nlink)
		}
	}
	if st := m.GetAttr(Background, d4Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if !dirAttr.Full || dirAttr.Nlink != 2 || dirAttr.Parent != d3Inode {
		t.Fatalf("d4Inode attr: %+v", *dirAttr)
	}
}
