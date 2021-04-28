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
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

func encodeSlices(size int) []string {
	w := utils.NewBuffer(24)
	w.Put32(0)
	w.Put64(1014)
	w.Put32(122)
	w.Put32(0)
	w.Put32(122)
	v := string(w.Bytes())
	vals := make([]string, size)
	for i := range vals {
		vals[i] = v
	}
	return vals
}

func BenchmarkReadSlices(b *testing.B) {
	cases := []struct {
		desc string
		size int
	}{
		{"small", 4},
		{"mid", 64},
		{"large", 1024},
	}
	for _, c := range cases {
		b.Run(c.desc, func(b *testing.B) {
			vals := encodeSlices(c.size)
			b.ResetTimer()
			var slices []*slice
			for i := 0; i < b.N; i++ {
				slices = readSlices(vals)
			}
			if len(slices) != len(vals) {
				b.Fail()
			}
		})
	}
}

// nolint:errcheck
func TestRedisClient(t *testing.T) {
	var conf RedisConfig
	_, err := NewRedisMeta("http://127.0.0.1:6379/7", &conf)
	if err == nil {
		t.Fatal("meta created with invalid url")
	}
	m, err := NewRedisMeta("redis://127.0.0.1:6379/7", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
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
	var target []byte
	if st := m.ReadLink(ctx, inode, &target); st != 0 {
		t.Fatalf("readlink s: %s", st)
	}
	if !bytes.Equal(target, []byte("/f")) {
		t.Fatalf("readlink got %s, expected %s", target, "/f")
	}
	if st := m.ReadLink(ctx, parent, &target); st != syscall.ENOENT {
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
	if st := m.SetXattr(ctx, inode, "a", []byte("v")); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	var value []byte
	if st := m.GetXattr(ctx, inode, "a", &value); st != 0 || string(value) != "v" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.ListXattr(ctx, inode, &value); st != 0 || string(value) != "a\000" {
		t.Fatalf("listxattr: %s %v", st, value)
	}
	if st := m.RemoveXattr(ctx, inode, "a"); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}

	// flock
	if st := m.Flock(ctx, inode, 1, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 1, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock again: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 1, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}

	// POSIX locks
	if st := m.Setlk(ctx, inode, 1, false, syscall.F_RDLCK, 0, 0xFFFF, 1); st != 0 {
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
	if st := m.Setlk(ctx, inode, 1, false, syscall.F_UNLCK, 0, 0x20000, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0, 0xFFFF, 10); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 1, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != syscall.EAGAIN {
		t.Fatalf("plock rlock: %s", st)
	}
	var ltype, pid uint32 = syscall.F_WRLCK, 1
	var start, end uint64 = 0, 0xFFFF
	if st := m.Getlk(ctx, inode, 1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_WRLCK || pid != 10 || start != 0 || end != 0xFFFF {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_UNLCK, 0, 0x2FFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	ltype = syscall.F_WRLCK
	start, end = 0, 0xFFFFFF
	if st := m.Getlk(ctx, inode, 1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_UNLCK || pid != 0 || start != 0 || end != 0 {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}

	// concurrent locks
	var g sync.WaitGroup
	var count int
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
				logger.Errorf("count should be be zero but got %d", count)
			}
			if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_UNLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				logger.Errorf("plock unlock: %s", st)
				err = st
			}
		}(i)
	}
	g.Wait()

	var totalspace, availspace, iused, iavail uint64
	if st := m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	var summary Summary
	if st := m.Summary(ctx, 1, &summary); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected := Summary{Length: 202, Size: 16384, Files: 3, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := m.Summary(ctx, inode, &summary); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 402, Size: 20480, Files: 4, Dirs: 2}
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

func TestRmr(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1:6379/5", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Rmr(ctx, 1, "f"); st != 0 {
		t.Fatalf("rmr f: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0755, 0, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d2", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/d2: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0644, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/f: %s", st)
	}
	if st := m.Rmr(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmr d: %s", st)
	}
}

func TestCaseIncensi(t *testing.T) {
	var conf = RedisConfig{CaseInsensi: true}
	m, err := NewRedisMeta("redis://127.0.0.1:6379/6", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Create(ctx, 1, "foo", 0755, 0, &inode, attr)
	if st := m.Create(ctx, 1, "Foo", 0755, 0, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("create should fail with EEXIST")
	}
	if st := m.Lookup(ctx, 1, "Foo", &inode, attr); st != 0 {
		t.Fatalf("lookup Foo should be OK")
	}
	if st := m.Rename(ctx, 1, "Foo", 1, "bar", &inode, attr); st != 0 {
		t.Fatalf("rename Foo to bar should be OK")
	}
	if st := m.Unlink(ctx, 1, "Bar"); st != 0 {
		t.Fatalf("unlink Bar should be OK")
	}
	if st := m.Mkdir(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("mkdir Foo should be OK")
	}
	if st := m.Rmdir(ctx, 1, "foo"); st != 0 {
		t.Fatalf("rmdir foo should be OK")
	}
}

func TestCompaction(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1:6379/8", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	_ = m.Init(Format{Name: "test"}, true)
	done := make(chan bool, 1)
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
		select {
		case done <- true:
		default:
		}
		return nil
	})
	_ = m.NewSession()
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f")
	}()

	// random write
	_ = m.Write(ctx, inode, 1, uint32(0), Slice{Chunkid: uint64(1000), Size: 64 << 20, Len: 64 << 20})
	_ = m.Write(ctx, inode, 1, uint32(30<<20), Slice{Chunkid: uint64(1001), Size: 8, Len: 8})
	_ = m.Write(ctx, inode, 1, uint32(40<<20), Slice{Chunkid: uint64(1002), Size: 8, Len: 8})
	var cs1 []Slice
	_ = m.Read(ctx, inode, 1, &cs1)
	if len(cs1) != 5 {
		t.Fatalf("expect 5 slices, but got %+v", cs1)
	}
	m.(*redisMeta).compactChunk(inode, 1)
	var cs []Slice
	_ = m.Read(ctx, inode, 1, &cs)
	if len(cs) != 1 {
		t.Fatalf("expect 1 slice, but got %+v", cs)
	}

	// append
	var size uint32 = 1000000
	for i := 0; i < 50; i++ {
		if st := m.Write(ctx, inode, 0, uint32(i)*size, Slice{Chunkid: uint64(i) + 1, Size: size, Len: size}); st != 0 {
			t.Fatalf("write %d: %s", i, st)
		}
		time.Sleep(time.Millisecond)
	}
	<-done
	var chunks []Slice
	if st := m.Read(ctx, inode, 0, &chunks); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(chunks) > 20 {
		t.Fatalf("inode %d should be compacted, but have %d slices", inode, len(chunks))
	}
	<-done
	// wait for it to update chunks
	time.Sleep(time.Millisecond * 5)
	if st := m.Read(ctx, inode, 0, &chunks); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(chunks) > 3 {
		t.Fatalf("inode %d should be compacted after read, but have %d slices", inode, len(chunks))
	}
	var total uint32
	for _, s := range chunks {
		total += s.Len
	}
	if total != size*50 {
		t.Fatalf("size of slice should be %d, but got %d", size*50, total)
	}

	// TODO: check result if that's predictable
	if st := m.CompactAll(ctx); st != 0 {
		logger.Fatalf("compactall: %s", st)
	}
	var slices []Slice
	if st := m.ListSlices(ctx, &slices); st != 0 {
		logger.Fatalf("list all slices: %s", st)
	}

	l.Lock()
	deletes := len(deleted)
	l.Unlock()
	if deletes < 40 {
		t.Fatalf("deleted chunks %d is less then 40", deletes)
	}
}

func TestConcurrentWrite(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/9", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)
	_ = m.NewSession()

	ctx := Background
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	// nolint:errcheck
	defer m.Unlink(ctx, 1, "f")

	var errno syscall.Errno
	var g sync.WaitGroup
	for i := 0; i <= 20; i++ {
		g.Add(1)
		go func(indx uint32) {
			defer g.Done()
			for j := 0; j < 100; j++ {
				var slice = Slice{Chunkid: 1, Size: 100, Len: 100}
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

// nolint:errcheck
func TestTruncateAndDelete(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)
	_ = m.NewSession()

	ctx := Background
	var inode Ino
	var attr = &Attr{}
	m.Unlink(ctx, 1, "f")
	if st := m.Truncate(ctx, 1, 0, 4<<10, attr); st != syscall.EPERM {
		t.Fatalf("truncate dir %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Write(ctx, inode, 0, 100, Slice{1, 100, 0, 100}); st != 0 {
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
	r := m.(*redisMeta)

	listAll := func(pattern string) []string {
		var keys, ks []string
		var cursor uint64
		for {
			ks, cursor, err = r.rdb.Scan(ctx, cursor, pattern, 1000).Result()
			keys = append(keys, ks...)
			if err != nil || cursor == 0 {
				break
			}
		}
		return keys
	}

	keys := listAll(fmt.Sprintf("c%d_*", inode))
	if len(keys) != 3 {
		t.Fatalf("number of chunks: %d != 3, %+v", len(keys), keys)
	}
	m.Close(ctx, inode)
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink file %s", st)
	}

	time.Sleep(time.Millisecond * 100)
	keys = listAll(fmt.Sprintf("c%d_*", inode))
	// the last chunk could be found and deleted
	if len(keys) > 1 {
		t.Fatalf("number of chunks: %d > 1, %+v", len(keys), keys)
	}
}

// nolint:errcheck
func TestCopyFileRange(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		t.Skipf("redis is not available: %s", err)
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)
	_ = m.NewSession()

	ctx := Background
	var iin, iout Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "fin")
	_ = m.Unlink(ctx, 1, "fout")
	if st := m.Create(ctx, 1, "fin", 0650, 022, &iin, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fin")
	if st := m.Create(ctx, 1, "fout", 0650, 022, &iout, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fout")
	m.Write(ctx, iin, 0, 100, Slice{10, 200, 0, 100})
	m.Write(ctx, iin, 1, 100<<10, Slice{11, 100 << 10, 0, 10 << 10})
	m.Write(ctx, iin, 3, 0, Slice{12, 63 << 20, 10 << 20, 30 << 20})
	m.Write(ctx, iout, 2, 10<<20, Slice{13, 50 << 20, 10 << 20, 30 << 20})
	var copied uint64
	if st := m.CopyFileRange(ctx, iin, 150, iout, 30<<20, 500<<20, 0, &copied); st != 0 {
		t.Fatalf("copy file range: %s", st)
	}
	var expected uint64 = 3*ChunkSize + 30<<20 - 150
	if copied != expected {
		t.Fatalf("expect copy %d bytes, but got %d", expected, copied)
	}
	var expectedChunks = [][]Slice{
		{{0, 30 << 20, 0, 30 << 20}, {10, 200, 50, 50}, {0, 0, 200, ChunkSize - 30<<20 - 50}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {0, 0, 0, 100 << 10}, {11, 100 << 10, 0, 10 << 10}, {0, 0, 110 << 10, ChunkSize - (30<<20 - 150) - 110<<10}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {0, 0, 0, 150 + (ChunkSize - 30<<20)}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {12, 63 << 20, 10 << 20, 30 << 20}},
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

func benchmarkReaddir(b *testing.B, n int) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		b.Skipf("redis is not available: %s", err)
	}
	_ = m.NewSession()
	ctx := Background
	var inode Ino
	dname := fmt.Sprintf("largedir%d", n)
	var es []*Entry
	if m.Lookup(ctx, 1, dname, &inode, nil) == 0 && m.Readdir(ctx, inode, 0, &es) == 0 && len(es) == n+2 {
	} else {
		_ = m.Rmr(ctx, 1, dname)
		_ = m.Mkdir(ctx, 1, dname, 0755, 0, 0, &inode, nil)
		for j := 0; j < n; j++ {
			_ = m.Create(ctx, inode, fmt.Sprintf("d%d", j), 0755, 0, nil, nil)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var entries []*Entry
		if e := m.Readdir(ctx, inode, 1, &entries); e != 0 {
			b.Fatalf("readdir: %s", e)
		}
		if len(entries) != n+2 {
			b.Fatalf("files: %d != %d", len(entries), n+2)
		}
	}
}

func BenchmarkReaddir10(b *testing.B) {
	benchmarkReaddir(b, 10)
}

func BenchmarkReaddir1k(b *testing.B) {
	benchmarkReaddir(b, 1000)
}

func BenchmarkReaddir100k(b *testing.B) {
	benchmarkReaddir(b, 100000)
}

func BenchmarkReaddir10m(b *testing.B) {
	benchmarkReaddir(b, 10000000)
}
