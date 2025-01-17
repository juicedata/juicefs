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

package fs

import (
	"io"
	"os"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
)

// mutate_test_job_number: 5
func TestFileStat(t *testing.T) {
	attr := meta.Attr{
		Typ:   meta.TypeDirectory,
		Mode:  07740,
		Atime: 1,
		Mtime: 2,
	}
	st := AttrToFileInfo(2, &attr)
	if st.Inode() != 2 {
		t.Fatalf("inode should be 2")
	}
	if !st.IsDir() {
		t.Fatalf("should be a dir")
	}
	mode := st.Mode()
	if mode&os.ModeSticky == 0 {
		t.Fatalf("sticky bit should be set")
	}
	if mode&os.ModeSetuid == 0 {
		t.Fatalf("suid should be set")
	}
	if mode&os.ModeSetgid == 0 {
		t.Fatalf("sgid should be set")
	}
	if st.ModTime().Unix() != 2 {
		t.Fatalf("unixtimestamp : %d", st.ModTime().Unix())
	}
	if st.Sys() != &attr {
		t.Fatalf("sys should be meta attr")
	}
	attr.Typ = meta.TypeSymlink
	if !st.IsSymlink() {
		t.Fatalf("should be a symlink")
	}
}

// nolint:errcheck
func TestFileSystem(t *testing.T) {
	fs := createTestFS(t)
	ctx := meta.NewContext(1, 1, []uint32{2})
	if total, avail := fs.StatFS(ctx); total != 1<<30 || avail != (1<<30) {
		t.Fatalf("statfs: %d %d", total, avail)
	}
	if e := fs.Access(ctx, "/", 7); e != 0 {
		t.Fatalf("access /: %s", e)
	}
	f, err := fs.Create(ctx, "/hello", 0666, 022)
	if err != 0 {
		t.Fatalf("create /hello: %s", err)
	}
	if f.Name() != "/hello" {
		t.Fatalf("name: %s", f.Name())
	}
	_ = f.Close(ctx)
	f, err = fs.Open(ctx, "/hello", meta.MODE_MASK_R|meta.MODE_MASK_W)
	if err != 0 {
		t.Fatalf("open %s", err)
	}
	if fi, err := f.Stat(); err != nil || fi.Mode() != 0644 {
		t.Fatalf("stat: %s %+v", err, fi)
	}
	if n, err := f.Write(ctx, []byte("world")); err != 0 || n != 5 {
		t.Fatalf("write 5 bytes: %d %s", n, err)
	}
	if err := f.Fsync(ctx); err != 0 {
		t.Fatalf("fsync: %s", err)
	}
	var buf = make([]byte, 10)
	if n, err := f.Pread(ctx, buf, 2); err != nil || n != 3 || string(buf[:n]) != "rld" {
		t.Fatalf("pread(2): %d %s %s", n, err, string(buf[:n]))
	}
	if n, err := f.Seek(ctx, -3, io.SeekEnd); err != nil || n != 2 {
		t.Fatalf("seek 3 bytes before end: %d %s", n, err)
	}
	if n, err := f.Write(ctx, []byte("t")); err != 0 || n != 1 {
		t.Fatalf("write 1 bytes: %d %s", n, err)
	}
	if n, err := f.Seek(ctx, -2, io.SeekCurrent); err != nil || n != 1 {
		t.Fatalf("seek 2 bytes before current: %d %s", n, err)
	}
	if n, err := f.Read(ctx, buf); err != nil || n != 4 || string(buf[:n]) != "otld" {
		t.Fatalf("read(): %d %s %s", n, err, string(buf[:n]))
	}
	if n, err := f.Read(ctx, buf); err != io.EOF || n != 0 {
		t.Fatalf("read(): %d %s %s", n, err, string(buf[:n]))
	}
	if n, err := f.Pwrite(ctx, []byte("t"), 1); err != 0 || n != 1 {
		t.Fatalf("write 1 bytes: %d %s", n, err)
	}
	if e := f.Flush(ctx); e != 0 {
		t.Fatalf("flush /hello: %s", e)
	}

	if e := f.Chmod(ctx, 0640); e != 0 {
		t.Fatalf("chown: %s", e)
	}
	if e := f.Chown(ctx, 1, 2); e != 0 {
		t.Fatalf("chown: %s", e)
	}
	if e := f.Utime(ctx, 1, 2); e != 0 {
		t.Fatalf("utime: %s", e)
	}
	if s, e := f.Summary(ctx); e != 0 || s.Dirs != 0 || s.Files != 1 || s.Length != 5 || s.Size != 4<<10 {
		t.Fatalf("summary: %s %+v", e, s)
	}
	if e := f.Close(ctx); e != 0 {
		t.Fatalf("close /hello: %s", e)
	}
	if fi, err := fs.Stat(ctx, "/hello"); err != 0 {
		t.Fatalf("stat /hello: %s", err)
	} else if fi.Mode() != 0640 || fi.Uid() != 1 || fi.Gid() != 2 || fi.Atime() != 1 || fi.Mtime() != 2 {
		t.Fatalf("stat /hello: %+v", fi)
	}
	if e := fs.Truncate(ctx, "/hello", 2); e != 0 {
		t.Fatalf("truncate : %s", e)
	}
	if n, e := fs.CopyFileRange(ctx, "/hello", 0, "/hello", 5, 5); e != 0 || n != 2 {
		t.Fatalf("copyfilerange: %s %d", e, n)
	}

	if e := fs.SetXattr(ctx, "/hello", "k", []byte("value"), 0); e != 0 {
		t.Fatalf("setxattr /hello: %s", e)
	}
	if v, e := fs.GetXattr(ctx, "/hello", "k"); e != 0 || string(v) != "value" {
		t.Fatalf("getxattr /hello: %s %s", e, string(v))
	}
	if names, e := fs.ListXattr(ctx, "/hello"); e != 0 || string(names) != "k\x00" {
		t.Fatalf("listxattr /hello: %s %+v", e, names)
	}
	if e := fs.RemoveXattr(ctx, "/hello", "k"); e != 0 {
		t.Fatalf("removexattr /hello: %s", e)
	}

	if e := fs.Symlink(ctx, "hello", "/sym"); e != 0 {
		t.Fatalf("symlink: %s", e)
	}
	if target, e := fs.Readlink(ctx, "/sym"); e != 0 || string(target) != "hello" {
		t.Fatalf("readlink: %s", string(target))
	}
	if fi, err := fs.Stat(ctx, "/sym"); err != 0 || fi.name != "sym" || fi.IsSymlink() {
		t.Fatalf("stat symlink: %s %+v", err, fi)
	}
	if fi, err := fs.Lstat(ctx, "/sym"); err != 0 || fi.name != "sym" || !fi.IsSymlink() {
		t.Fatalf("lstat symlink: %s %+v", err, fi)
	}
	if err := fs.Delete(ctx, "/sym"); err != 0 {
		t.Fatalf("delete /sym: %s", err)
	}

	if _, e := fs.Open(meta.NewContext(2, 2, []uint32{3}), "/hello", meta.MODE_MASK_W); e == 0 || e != syscall.EACCES {
		t.Fatalf("open without permission: %s", e)
	}

	if err := fs.Mkdir(ctx, "/d", 0777, 022); err != 0 {
		t.Fatalf("mkdir /d: %s", err)
	}
	d, e := fs.Open(ctx, "/", 0)
	if e != 0 {
		t.Fatalf("open /: %s", e)
	}
	defer d.Close(ctx)
	if fis, e := d.Readdir(ctx, 0); e != 0 || len(fis) != 2 {
		t.Fatalf("readdir /: %s, %d entries", e, len(fis))
	} else {
		sort.Slice(fis, func(i, j int) bool { return fis[i].Name() < fis[j].Name() })
		if fis[0].Name() != "d" || fis[1].Name() != "hello" {
			t.Fatalf("readdir names: %+v", fis)
		}
	}
	if es, e := d.ReaddirPlus(ctx, 0); e != 0 || len(es) != 2 {
		t.Fatalf("readdirplus: %s, %d entries", e, len(es))
	} else {
		sort.Slice(es, func(i, j int) bool { return es[i].Inode < es[j].Inode })
		if string(es[0].Name) != "hello" || string(es[1].Name) != "d" {
			t.Fatalf("readdirplus names: %+v", es)
		}
	}
	if e := fs.Rename(ctx, "/hello", "/d/f", 0); e != 0 {
		t.Fatalf("rename: %s", e)
	}
	if e := fs.Symlink(ctx, "d", "/sd"); e != 0 {
		t.Fatalf("symlink: %s", e)
	}
	if fi, e := fs.Stat(ctx, "/sd/f"); e != 0 || fi.name != "f" {
		t.Fatalf("follow symlink: %s %+v", e, fi)
	}

	if s, e := d.Summary(ctx); e != 0 || s.Dirs != 2 || s.Files != 2 || s.Length != 7 || s.Size != 16<<10 {
		t.Fatalf("summary: %s %+v", e, s)
	}
	if e := fs.Delete(ctx, "/d"); e == 0 || !IsNotEmpty(e) {
		t.Fatalf("rmdir: %s", e)
	}
	if err := fs.Delete(ctx, "/d/f"); err != 0 {
		t.Fatalf("delete /d/f: %s", err)
	}
	if err := fs.Delete(ctx, "/d/f"); err == 0 || !IsNotExist(err) {
		t.Fatalf("delete /d/f: %s", err)
	}
	if e := fs.Rmr(ctx, "/d", meta.RmrDefaultThreads); e != 0 {
		t.Fatalf("delete /d -r: %s", e)
	}

	time.Sleep(time.Second * 2)
	if e := fs.Flush(); e != nil {
		t.Fatalf("flush : %s", e)
	}
	if e := fs.Close(); e != nil {
		t.Fatalf("close: %s", e)
	}
	if e := fs.Close(); e != nil {
		t.Fatalf("close: %s", e)
	}

	// path with trailing /
	if err := fs.Mkdir(ctx, "/ddd/", 0777, 000); err != 0 {
		t.Fatalf("mkdir /ddd/: %s", err)
	}
	if _, err := fs.Create(ctx, "/ddd/ddd", 0777, 000); err != 0 {
		t.Fatalf("create /ddd/ddd: %s", err)
	}
	if _, err := fs.Create(ctx, "/ddd/fff/", 0777, 000); err != syscall.EINVAL {
		t.Fatalf("create /ddd/fff/: %s", err)
	}
	if err := fs.Delete(ctx, "/ddd/"); err != syscall.ENOTEMPTY {
		t.Fatalf("delete /ddd/: %s", err)
	}
	if err := fs.Rename(ctx, "/ddd/", "/ttt/", 0); err != 0 {
		t.Fatalf("delete /ddd/: %s", err)
	}
	if err := fs.Rmr(ctx, "/ttt/", meta.RmrDefaultThreads); err != 0 {
		t.Fatalf("rmr /ttt/: %s", err)
	}
	if _, err := fs.Stat(ctx, "/ttt/"); err != syscall.ENOENT {
		t.Fatalf("stat /ttt/: %s", err)
	}
}

func createTestFS(t *testing.T) *FileSystem {
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: meta.DefaultConf(),
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize << 10,
			MaxUpload:  1,
			BufferSize: 100 << 20,
		},
		DirEntryTimeout: time.Millisecond * 100,
		EntryTimeout:    time.Millisecond * 100,
		AttrTimeout:     time.Millisecond * 100,
		AccessLog:       "/tmp/juicefs.access.log",
	}
	objStore, _ := object.CreateStorage("mem", "", "", "", "")
	store := chunk.NewCachedStore(objStore, *conf.Chunk, nil)
	jfs, err := NewFileSystem(&conf, m, store)
	jfs.checkAccessFile = time.Millisecond
	jfs.rotateAccessLog = 500
	if err != nil {
		t.Fatalf("initialize  failed: %s", err)
	}
	return jfs
}
