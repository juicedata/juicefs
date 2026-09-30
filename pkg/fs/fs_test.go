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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
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
	if s, e := f.Summary(ctx, true, true); e != 0 || s.Dirs != 0 || s.Files != 1 || s.Length != 5 || s.Size != 4<<10 {
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

	if s, e := d.Summary(ctx, true, true); e != 0 || s.Dirs != 2 || s.Files != 2 || s.Length != 7 || s.Size != 16<<10 {
		t.Fatalf("summary: %s %+v", e, s)
	}
	if q, e := d.GetQuota(ctx); e != nil || q.MaxInodes != 0 || q.MaxSpace != (1<<30) {
		t.Fatalf("quota: %s %+v", e, q)
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
	if e := fs.Rmr(ctx, "/d", false, meta.RmrDefaultThreads); e != 0 {
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
	if err := fs.Rmr(ctx, "/ttt/", false, meta.RmrDefaultThreads); err != 0 {
		t.Fatalf("rmr /ttt/: %s", err)
	}
	if _, err := fs.Stat(ctx, "/ttt/"); err != syscall.ENOENT {
		t.Fatalf("stat /ttt/: %s", err)
	}
}

func TestBatchDeleteEntries(t *testing.T) {
	jfs := createTestFS(t)
	ctx := meta.NewContext(1, 1, []uint32{2})

	if err := jfs.MkdirAll(ctx, "/batch/dir", 0777, 022); err != 0 {
		t.Fatalf("mkdir /batch/dir: %s", err)
	}
	for _, p := range []string{"/batch/dir/file1", "/batch/dir/file2"} {
		f, err := jfs.Create(ctx, p, 0666, 022)
		if err != 0 {
			t.Fatalf("create %s: %s", p, err)
		}
		if err := f.Close(ctx); err != 0 {
			t.Fatalf("close %s: %s", p, err)
		}
	}
	if err := jfs.Mkdir(ctx, "/batch/dir/subdir", 0777, 022); err != 0 {
		t.Fatalf("mkdir /batch/dir/subdir: %s", err)
	}
	if err := jfs.Symlink(ctx, "missing-target", "/batch/dir/link"); err != 0 {
		t.Fatalf("symlink: %s", err)
	}

	err := jfs.BatchDeleteEntries(ctx, "/batch/dir", []string{
		"/batch/dir/file1",
		"/batch/dir/missing",
		"/batch/dir/subdir",
		"/batch/dir/link",
	})
	if err != 0 {
		t.Fatalf("batch delete: %s", err)
	}
	if _, err := jfs.Stat(ctx, "/batch/dir/file1"); err != syscall.ENOENT {
		t.Fatalf("file1 should be deleted: %s", err)
	}
	if _, err := jfs.Lstat(ctx, "/batch/dir/link"); err != syscall.ENOENT {
		t.Fatalf("symlink should be deleted: %s", err)
	}
	if _, err := jfs.Stat(ctx, "/batch/dir/file2"); err != 0 {
		t.Fatalf("file2 should remain: %s", err)
	}
	if fi, err := jfs.Stat(ctx, "/batch/dir/subdir"); err != 0 || !fi.IsDir() {
		t.Fatalf("subdir should remain: %s %+v", err, fi)
	}
}

func TestBatchDeleteEntriesChecksParentWriteAndSearchPermission(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode uint16
	}{
		{name: "no-write", mode: 0555},
		{name: "no-search", mode: 0222},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jfs := createTestFS(t)
			ctx := meta.NewContext(1, 1, []uint32{2})
			parent := "/protected-" + tc.name
			if err := jfs.Mkdir(ctx, parent, tc.mode, 000); err != 0 {
				t.Fatalf("mkdir %s: %s", parent, err)
			}

			userCtx := meta.NewContext(2, 2, []uint32{3})
			err := jfs.BatchDeleteEntries(userCtx, parent, []string{parent + "/missing"})
			if err != syscall.EACCES {
				t.Fatalf("batch delete without parent write and search permission: %s", err)
			}
		})
	}
}

func TestResolveRelativeSymlinkAfterRedirection(t *testing.T) {
	fs := createTestFS(t)
	ctx := meta.NewContext(1, 1, []uint32{2})

	for _, dir := range []string{"/a", "/x/y", "/x/z"} {
		if err := fs.MkdirAll(ctx, dir, 0777, 022); err != 0 {
			t.Fatalf("mkdir %s: %s", dir, err)
		}
	}

	f, err := fs.Create(ctx, "/x/z/file", 0666, 022)
	if err != 0 {
		t.Fatalf("create target file: %s", err)
	}
	if err := f.Close(ctx); err != 0 {
		t.Fatalf("close target file: %s", err)
	}

	if err := fs.Symlink(ctx, "../x/y", "/a/link1"); err != 0 {
		t.Fatalf("symlink /a/link1: %s", err)
	}
	if err := fs.Symlink(ctx, "../z", "/x/y/link2"); err != 0 {
		t.Fatalf("symlink /x/y/link2: %s", err)
	}

	if fi, err := fs.Stat(ctx, "/a/link1/link2/file"); err != 0 {
		t.Fatalf("stat nested relative symlink path: %s", err)
	} else if fi.name != "file" {
		t.Fatalf("unexpected final name: %+v", fi)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("close: %s", err)
	}
}

func createTestFS(t testing.TB) *FileSystem {
	objStore, _ := object.CreateStorage("mem", "", "", "", "")
	return createTestFSWithStorage(t, objStore, 4096)
}

// createTestFSWithStorage builds a FileSystem on the given object storage with
// no block cache, so every read goes to the storage. blockSize is in KiB.
func createTestFSWithStorage(t testing.TB, objStore object.ObjectStorage, blockSize int) *FileSystem {
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: blockSize,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: meta.DefaultConf(),
		Chunk: &chunk.Config{
			BlockSize:   format.BlockSize << 10,
			MaxUpload:   1,
			MaxDownload: 200,
			BufferSize:  100 << 20,
		},
		DirEntryTimeout: time.Millisecond * 100,
		EntryTimeout:    time.Millisecond * 100,
		AttrTimeout:     time.Millisecond * 100,
		AccessLog:       filepath.Join(t.TempDir(), "juicefs.access.log"),
	}
	store := chunk.NewCachedStore(objStore, *conf.Chunk, nil)
	jfs, err := NewFileSystem(&conf, m, store, nil)
	if err != nil {
		t.Fatalf("initialize  failed: %s", err)
	}
	jfs.checkAccessFile = time.Millisecond
	jfs.rotateAccessLog = 500
	t.Cleanup(func() { _ = jfs.Close() })
	return jfs
}

// gatedStorage counts how many Get calls are in flight at the same time. Each Get
// waits until `want` calls have arrived or `timeout` passes, so callers that are
// serialized upstream show up as maxInflight == 1 instead of a flaky timing gap.
type gatedStorage struct {
	object.ObjectStorage
	want        int32
	timeout     time.Duration
	arrived     int32
	inflight    int32
	maxInflight int32
	release     chan struct{}
	once        sync.Once
}

func newGatedStorage(base object.ObjectStorage, want int, timeout time.Duration) *gatedStorage {
	return &gatedStorage{ObjectStorage: base, want: int32(want), timeout: timeout, release: make(chan struct{})}
}

func (g *gatedStorage) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	cur := atomic.AddInt32(&g.inflight, 1)
	defer atomic.AddInt32(&g.inflight, -1)
	for {
		old := atomic.LoadInt32(&g.maxInflight)
		if cur <= old || atomic.CompareAndSwapInt32(&g.maxInflight, old, cur) {
			break
		}
	}
	if atomic.AddInt32(&g.arrived, 1) >= g.want {
		g.once.Do(func() { close(g.release) })
	}
	select {
	case <-g.release:
	case <-time.After(g.timeout):
	}
	return g.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

// Positioned reads on one File must not wait for each other: HBase issues many
// preads on a single open HFile from different handler threads, and each read of
// an uncached block costs one round trip to the object storage.
func TestFileConcurrentPread(t *testing.T) {
	const (
		blockSize = 64 << 10
		readers   = 4
		readLen   = 4096
	)
	base, _ := object.CreateStorage("mem", "", "", "", "")
	gated := newGatedStorage(base, readers, 2*time.Second)
	fs := createTestFSWithStorage(t, gated, blockSize>>10)
	ctx := meta.NewContext(1, 1, []uint32{2})

	// 6 blocks; the reads below stay away from block 0 and from the last 32 KiB,
	// which are the two places the reader starts readahead on its own.
	data := make([]byte, 6*blockSize)
	for i := range data {
		data[i] = byte(i * 7)
	}
	f, err := fs.Create(ctx, "/pread", 0644, 022)
	if err != 0 {
		t.Fatalf("create: %s", err)
	}
	if _, err := f.Write(ctx, data); err != 0 {
		t.Fatalf("write: %s", err)
	}
	if err := f.Close(ctx); err != 0 {
		t.Fatalf("close: %s", err)
	}

	f, err = fs.Open(ctx, "/pread", vfs.MODE_MASK_R)
	if err != 0 {
		t.Fatalf("open: %s", err)
	}
	defer f.Close(ctx)

	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := 0; i < readers; i++ {
		off := int64((i + 1) * blockSize)
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, readLen)
			n, err := f.Pread(ctx, buf, off)
			if err != nil || n != readLen {
				errs <- fmt.Errorf("pread at %d: (%d,%v)", off, n, err)
				return
			}
			if !bytes.Equal(buf, data[off:off+readLen]) {
				errs <- fmt.Errorf("pread at %d: data mismatch", off)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := atomic.LoadInt32(&gated.arrived); got != readers {
		t.Fatalf("expected %d storage reads (one per uncached block), got %d", readers, got)
	}
	if got := atomic.LoadInt32(&gated.maxInflight); got != readers {
		t.Fatalf("expected %d concurrent storage reads from one file, got %d", readers, got)
	}
}

// Close must wait for positioned reads that are already in flight, the way the
// FUSE path drains readers with handle.Wlock: otherwise a slow read observes the
// closed reader and returns EOF instead of data.
func TestFileCloseWaitsForPread(t *testing.T) {
	const (
		blockSize = 64 << 10
		readLen   = 4096
	)
	base, _ := object.CreateStorage("mem", "", "", "", "")
	// want=2 never arrives, so the single read below sits in Get for 2s.
	gated := newGatedStorage(base, 2, 2*time.Second)
	fs := createTestFSWithStorage(t, gated, blockSize>>10)
	ctx := meta.NewContext(1, 1, []uint32{2})

	data := make([]byte, 4*blockSize)
	for i := range data {
		data[i] = byte(i * 13)
	}
	f, err := fs.Create(ctx, "/pread-close", 0644, 022)
	if err != 0 {
		t.Fatalf("create: %s", err)
	}
	if _, err := f.Write(ctx, data); err != 0 {
		t.Fatalf("write: %s", err)
	}
	if err := f.Close(ctx); err != 0 {
		t.Fatalf("close: %s", err)
	}

	f, err = fs.Open(ctx, "/pread-close", vfs.MODE_MASK_R)
	if err != 0 {
		t.Fatalf("open: %s", err)
	}
	type result struct {
		n   int
		err error
	}
	got := make(chan result, 1)
	off := int64(blockSize)
	buf := make([]byte, readLen)
	go func() {
		n, err := f.Pread(ctx, buf, off)
		got <- result{n, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&gated.arrived) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("pread never reached the storage")
		}
		time.Sleep(time.Millisecond)
	}

	if err := f.Close(ctx); err != 0 {
		t.Fatalf("close: %s", err)
	}
	// A drained read has already returned; one still stuck in Get needs ~2s.
	var r result
	select {
	case r = <-got:
	case <-time.After(time.Second):
		t.Fatal("Close returned while a positioned read was still in flight")
	}
	if r.err != nil || r.n != readLen {
		t.Fatalf("pread during close: (%d,%v), want (%d,<nil>)", r.n, r.err, readLen)
	}
	if !bytes.Equal(buf, data[off:off+readLen]) {
		t.Fatal("pread during close returned wrong data")
	}
	if _, err := f.Pread(ctx, buf, off); err != syscall.EBADF {
		t.Fatalf("pread after close: %v, want EBADF", err)
	}
}
