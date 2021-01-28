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
	m, err := NewRedisMeta("redis://127.0.0.1:6379/7", &conf)
	if err != nil {
		t.Logf("redis is not available: %s", err)
		t.Skip()
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error { return nil })
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var parent, inode Ino
	var attr = &Attr{}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir %s", st)
	}
	defer m.Rmdir(ctx, 1, "d")
	if st := m.Lookup(ctx, 1, "d", &parent, attr); st != 0 {
		t.Fatalf("lookup dir: %s", st)
	}
	if st := m.Access(ctx, parent, 4, attr); st != 0 {
		t.Fatalf("access dir: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, parent, "f")
	if st := m.Lookup(ctx, parent, "f", &inode, attr); st != 0 {
		t.Fatalf("lookup file: %s", st)
	}
	attr.Mtime = 2
	attr.Uid = 1
	if st := m.SetAttr(ctx, inode, SetAttrMtime|SetAttrUID, 0, attr); st != 0 {
		t.Fatalf("setattr file %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr file %s", st)
	}
	if attr.Mtime != 2 || attr.Uid != 1 {
		t.Fatalf("mtime:%d uid:%d", attr.Mtime, attr.Uid)
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
		t.Fatalf("rename f %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f2")
	}()
	if st := m.Lookup(ctx, 1, "f2", &inode, attr); st != 0 {
		t.Fatalf("lookup f2: %s", st)
	}

	// data
	var chunkid uint64
	if st := m.Open(ctx, inode, 2, attr); st != 0 {
		t.Fatalf("open f2: %s", st)
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

	if st := m.Unlink(ctx, 1, "f2"); st != 0 {
		t.Fatalf("unlink: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir: %s", st)
	}
}

func TestCompaction(t *testing.T) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1:6379/8", &conf)
	if err != nil {
		t.Logf("redis is not available: %s", err)
		t.Skip()
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
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0650, 022, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f")
	}()
	for i := 0; i < 50; i++ {
		if st := m.Write(ctx, inode, 0, uint32(i*100), Slice{Chunkid: uint64(i) + 1, Size: 100, Len: 100}); st != 0 {
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
	if len(chunks) > 1 {
		t.Fatalf("inode %d should be compacted after read, but have %d slices", inode, len(chunks))
	}
	if chunks[0].Size != 5000 {
		t.Fatalf("size of slice should be 5000, but got %d", chunks[0].Size)
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
		t.Logf("redis is not available: %s", err)
		t.Skip()
	}
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
		t.Logf("redis is not available: %s", err)
		t.Skip()
	}
	m.OnMsg(DeleteChunk, func(args ...interface{}) error {
		return nil
	})
	_ = m.Init(Format{Name: "test"}, true)
	ctx := Background
	var inode Ino
	var attr = &Attr{}
	m.Unlink(ctx, 1, "f")
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
	r := m.(*redisMeta)
	keys, _, _ := r.rdb.Scan(ctx, 0, fmt.Sprintf("c%d_*", inode), 1000).Result()
	if len(keys) != 3 {
		for _, k := range keys {
			println("key", k)
		}
		t.Fatalf("number of chunks: %d != 3", len(keys))
	}
	m.Close(ctx, inode)
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink file %s", st)
	}
	time.Sleep(time.Millisecond * 100)
	keys, _, _ = r.rdb.Scan(ctx, 0, fmt.Sprintf("c%d_*", inode), 1000).Result()
	// the last chunk will be found and deleted
	if len(keys) != 1 {
		t.Fatalf("number of chunks: %d != 1", len(keys))
	}
}
