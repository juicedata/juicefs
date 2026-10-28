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
	"path/filepath"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/acl"
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

func TestHandleQuota(t *testing.T) {
	fs := createTestFS(t)
	ctx := meta.NewContext(1, 0, []uint32{0})
	if e := fs.Mkdir(ctx, "/d", 0777, 0); e != 0 {
		t.Fatalf("mkdir /d: %s", e)
	}

	cases := []struct {
		key, getKey, listKey string
		capacity, inodes     uint64
	}{
		{"/d", "/d", "/d", 1 << 30, 50},
		{"uid:1000", "1000", "uid:1000", 1 << 30, 100},
		{"gid:2000", "2000", "gid:2000", 2 << 30, 200},
	}
	for _, c := range cases {
		if _, e := fs.HandleQuota(ctx, c.key, meta.QuotaSet, c.capacity, c.inodes, false, false, false); e != 0 {
			t.Fatalf("set quota %s: %s", c.key, e)
		}
	}
	expect := func(qs map[string]*meta.Quota, key string, capacity, inodes uint64) {
		t.Helper()
		q := qs[key]
		if q == nil {
			t.Fatalf("quota %s not found in %v", key, qs)
		}
		if q.MaxSpace != int64(capacity) || q.MaxInodes != int64(inodes) {
			t.Fatalf("quota %s: %+v", key, q)
		}
	}
	for _, c := range cases {
		qs, e := fs.HandleQuota(ctx, c.key, meta.QuotaGet, 0, 0, false, false, false)
		if e != 0 {
			t.Fatalf("get quota %s: %s", c.key, e)
		}
		expect(qs, c.getKey, c.capacity, c.inodes)
	}
	qs, e := fs.HandleQuota(ctx, "", meta.QuotaList, 0, 0, false, false, false)
	if e != 0 || len(qs) != len(cases) {
		t.Fatalf("list quota: %s %v", e, qs)
	}
	for _, c := range cases {
		expect(qs, c.listKey, c.capacity, c.inodes)
	}

	for _, c := range cases {
		if _, e := fs.HandleQuota(ctx, c.key, meta.QuotaDel, 0, 0, false, false, false); e != 0 {
			t.Fatalf("delete quota %s: %s", c.key, e)
		}
		qs, e := fs.HandleQuota(ctx, c.key, meta.QuotaGet, 0, 0, false, false, false)
		if e != 0 {
			t.Fatalf("get quota %s after delete: %s", c.key, e)
		}
		// user/group quotas keep their usage and only drop the limits
		if q := qs[c.getKey]; q != nil && (q.MaxSpace != -1 || q.MaxInodes != -1) {
			t.Fatalf("quota %s after delete: %+v", c.key, q)
		}
	}
	if qs, e := fs.HandleQuota(ctx, "", meta.QuotaList, 0, 0, false, false, false); e != 0 || len(qs) != 0 {
		t.Fatalf("list quota after delete: %s %v", e, qs)
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

func TestResolveDotDot(t *testing.T) {
	fs := createTestFS(t)
	ctx := meta.Background()
	if err := fs.MkdirAll(ctx, "/x/y", 0777, 0); err != 0 {
		t.Fatal(err)
	}
	for _, p := range []string{"/f", "/x/f", "/victim"} {
		f, err := fs.Create(ctx, p, 0666, 0)
		if err != 0 {
			t.Fatal(err)
		}
		_ = f.Close(ctx)
	}
	for link, target := range map[string]string{
		"/ln": "x/y", "/chain": "ln", "/target": "ln/../f", "/loop": "loop",
	} {
		if err := fs.Symlink(ctx, target, link); err != 0 {
			t.Fatal(err)
		}
	}
	want, err := fs.Stat(ctx, "/x/f")
	if err != 0 {
		t.Fatal(err)
	}
	for _, fast := range []bool{false, true} {
		fs.conf.FastResolve = fast
		for _, tc := range []struct {
			path string
			err  syscall.Errno
		}{
			{"/f/..", syscall.ENOTDIR},
			{"/missing/..", syscall.ENOENT},
			{"/ln/../f", 0},
			{"ln/../f", 0},
			{"../x/f", 0},
			{"/chain/../f", 0},
			{"/target", 0},
			{"/ln/../../ln/../f", 0},
			{"/f/", syscall.ENOTDIR},
			{"/loop", syscall.ELOOP},
		} {
			fi, err := fs.Stat(ctx, tc.path)
			if err != tc.err {
				t.Errorf("Stat(%q), fast=%v: got %s, want %s", tc.path, fast, err, tc.err)
			} else if err == 0 && fi.Inode() != want.Inode() {
				t.Errorf("Stat(%q), fast=%v: got inode %d, want %d", tc.path, fast, fi.Inode(), want.Inode())
			}
		}
		if fi, err := fs.Lstat(ctx, "/chain/../f"); err != 0 || fi.Inode() != want.Inode() {
			t.Errorf("Lstat intermediate symlink chain: %v, %s", fi, err)
		}
		if fi, err := fs.Lstat(ctx, "/chain"); err != 0 || !fi.IsSymlink() {
			t.Errorf("Lstat final symlink: %v, %s", fi, err)
		}
	}
	if err := fs.Delete(ctx, "/f/../victim"); err != syscall.ENOTDIR {
		t.Errorf("Delete through file: %s", err)
	}
	if _, err := fs.Stat(ctx, "/victim"); err != 0 {
		t.Errorf("victim should remain: %s", err)
	}
	if err := fs.MkdirAll(ctx, "/f/../z", 0777, 0); err != syscall.ENOTDIR {
		t.Errorf("MkdirAll through file: %s", err)
	}
	if _, err := fs.Stat(ctx, "/z"); err != syscall.ENOENT {
		t.Errorf("z should not exist: %s", err)
	}
	if err := fs.MkdirAll(ctx, "/g/..", 0777, 0); err != 0 {
		t.Fatal(err)
	}
	if _, err := fs.Stat(ctx, "/g"); err != 0 {
		t.Errorf("MkdirAll should create g: %s", err)
	}
	if f, err := fs.Create(ctx, "/ln/../new", 0666, 0); err != 0 {
		t.Fatal(err)
	} else {
		_ = f.Close(ctx)
	}
	if _, err := fs.Stat(ctx, "/x/new"); err != 0 {
		t.Errorf("Create should follow symlink before ..: %s", err)
	}
	if err := fs.Rename(ctx, "/x/y", "/g/y", 0); err != 0 {
		t.Fatal(err)
	}
	if _, err := fs.Stat(ctx, "/g/y/../new"); err != syscall.ENOENT {
		t.Errorf(".. should not hit stale dentry cache after rename: %s", err)
	}
	if err := fs.Symlink(ctx, ".", "/g/self"); err != 0 {
		t.Fatal(err)
	}
	gInfo, err := fs.Stat(ctx, "/g")
	if err != 0 {
		t.Fatal(err)
	}
	if fi, err := fs.Stat(ctx, "/g/self"); err != 0 || fi.Inode() != gInfo.Inode() || !fi.IsDir() {
		t.Errorf("Stat symlink to .: %+v, %s", fi, err)
	}
	if err := fs.Mkdir(ctx, "/g/self/new", 0777, 0); err != 0 {
		t.Errorf("Mkdir through symlink to .: %s", err)
	}
	if err := fs.Symlink(ctx, "..", "/g/root-link"); err != 0 {
		t.Fatal(err)
	}
	if fi, err := fs.Stat(ctx, "/g/root-link"); err != 0 || fi.Inode() != meta.RootInode || fi.Name() != "root-link" {
		t.Errorf("Stat symlink to root: %+v, %s", fi, err)
	}
	if err := fs.Rmr(ctx, "/f/x", false, 1); err != syscall.ENOTDIR {
		t.Errorf("Rmr under file: %s", err)
	}
	if err := fs.Rmr(ctx, "/f/..", false, 1); err != syscall.ENOTDIR {
		t.Errorf("Rmr file/..: %s", err)
	}
	if err := fs.Rmr(ctx, "/x/.", false, 1); err != syscall.EINVAL {
		t.Errorf("Rmr dir/.: %s", err)
	}
	if err := fs.Rmr(ctx, "/x/..", false, 1); err != syscall.ENOTEMPTY {
		t.Errorf("Rmr dir/..: %s", err)
	}
	if _, err := fs.Stat(ctx, "/x/new"); err != 0 {
		t.Errorf("Rmr dir/. or dir/.. must not remove entries: %s", err)
	}
	if err := fs.Rmr(ctx, "/", false, 1); err != syscall.EBUSY {
		t.Errorf("Rmr /: %s", err)
	}
	if err := fs.Rmdir(ctx, "/"); err != syscall.EBUSY {
		t.Errorf("Rmdir /: %s", err)
	}
	if err := fs.Delete(ctx, "/"); err != syscall.EBUSY {
		t.Errorf("Delete /: %s", err)
	}
	if err := fs.Rename(ctx, "/f", "/f/x", 0); err != syscall.ENOTDIR {
		t.Errorf("Rename(f, f/x): %s", err)
	}
	if err := fs.Rename(ctx, "/g", "/g/self/sub", 0); err != syscall.EINVAL {
		t.Errorf("Rename into symlinked self: %s", err)
	}
	if err := fs.Rename(ctx, "/g/.", "/z", 0); err != syscall.EBUSY {
		t.Errorf("Rename dir/.: %s", err)
	}
	if err := fs.Rename(ctx, "/f/", "/z", 0); err != syscall.ENOTDIR {
		t.Errorf("Rename f/: %s", err)
	}
	if err := fs.Rename(ctx, "/f", "/z/", 0); err != syscall.ENOTDIR {
		t.Errorf("Rename to z/: %s", err)
	}
	if err := fs.Mkdir(ctx, "/exdir", 0777, 0); err != 0 {
		t.Fatal(err)
	}
	if err := fs.Rename(ctx, "/f", "/exdir/", 0); err != syscall.ENOTDIR {
		t.Errorf("Rename(file, dir/, 0): %s", err)
	}
	if err := fs.Rename(ctx, "/exdir", "/f/", meta.RenameNoReplace); err != syscall.EEXIST {
		t.Errorf("Rename(dir, file/, NoReplace): %s", err)
	}
	if err := fs.Rename(ctx, "/f/", "/missing", meta.RenameExchange); err != syscall.ENOENT {
		t.Errorf("Rename(file/, missing, Exchange): %s", err)
	}
	if err := fs.Rename(ctx, "/f", "/exdir/", meta.RenameExchange); err != 0 {
		t.Errorf("RenameExchange(file, dir/): %s", err)
	}
	if err := fs.Rename(ctx, "/exdir", "/f/", meta.RenameExchange); err != 0 {
		t.Errorf("RenameExchange back: %s", err)
	}
	if err := fs.Mkdir(ctx, "/locked", 0700, 0); err != 0 {
		t.Fatal(err)
	}
	if f, err := fs.Create(ctx, "/locked/present", 0666, 0); err != 0 {
		t.Fatal(err)
	} else {
		_ = f.Close(ctx)
	}
	userCtx := meta.NewContext(2, 2, []uint32{3})
	for _, dst := range []string{"/locked/present/", "/locked/missing/"} {
		if err := fs.Link(userCtx, "/f", dst); err != syscall.EACCES {
			t.Errorf("Link(%s) without search perm: got %s, want EACCES", dst, err)
		}
		if err := fs.Symlink(userCtx, "x", dst); err != syscall.EACCES {
			t.Errorf("Symlink(%s) without search perm: got %s, want EACCES", dst, err)
		}
		if err := fs.Rename(userCtx, dst, "/z", 0); err != syscall.EACCES {
			t.Errorf("Rename(%s) without search perm: got %s, want EACCES", dst, err)
		}
	}
	rootFile, err := fs.Open(ctx, "/", 0)
	if err != 0 {
		t.Fatal(err)
	}
	if err := rootFile.Chmod(ctx, 0700); err != 0 {
		t.Fatal(err)
	}
	for _, dst := range []string{"/f/", "/missing/"} {
		if err := fs.Link(userCtx, "/f", dst); err != syscall.EACCES {
			t.Errorf("Link(%s) on 0700 root: got %s, want EACCES", dst, err)
		}
		if err := fs.Symlink(userCtx, "x", dst); err != syscall.EACCES {
			t.Errorf("Symlink(%s) on 0700 root: got %s, want EACCES", dst, err)
		}
	}
	if err := rootFile.Chmod(ctx, 0755); err != 0 {
		t.Fatal(err)
	}
	_ = rootFile.Close(ctx)
	if err := fs.Link(ctx, "/f", "/z/"); err != syscall.ENOENT {
		t.Errorf("Link to z/: %s", err)
	}
	if err := fs.Symlink(ctx, "x", "/z/"); err != syscall.ENOENT {
		t.Errorf("Symlink to z/: %s", err)
	}
	if err := fs.Symlink(ctx, "x", "/f/"); err != syscall.EEXIST {
		t.Errorf("Symlink to f/: %s", err)
	}
	if _, err := fs.Stat(ctx, ""); err != syscall.ENOENT {
		t.Errorf("Stat empty: %s", err)
	}
	if err := fs.Rename(ctx, "/f", "", 0); err != syscall.ENOENT {
		t.Errorf("Rename to empty: %s", err)
	}
	if rf, err := fs.Open(ctx, "/f", 0); err != 0 {
		t.Fatal(err)
	} else {
		if _, err := rf.Readdir(ctx, 0); err != syscall.ENOTDIR {
			t.Errorf("Readdir file: %s", err)
		}
		if _, err := rf.ReaddirPlus(ctx, 0); err != syscall.ENOTDIR {
			t.Errorf("ReaddirPlus file: %s", err)
		}
		_ = rf.Close(ctx)
	}

	// ReadOnly client Mkdir("/") -> EEXIST and MkdirAll("/") -> 0.
	fs.conf.Meta.ReadOnly = true
	if err := fs.Mkdir(ctx, "/", 0755, 0); err != syscall.EEXIST {
		t.Errorf("ReadOnly Mkdir(/): got %s, want EEXIST", err)
	}
	if err := fs.MkdirAll(ctx, "/", 0755, 0); err != 0 {
		t.Errorf("ReadOnly MkdirAll(/): got %s, want 0", err)
	}
	fs.conf.Meta.ReadOnly = false

	// SetFacl after caching 0700 directory must take effect immediately for Mkdir.
	format := fs.Meta().GetFormat()
	format.EnableACL = true
	if err := fs.Meta().Init(&format, false); err != nil {
		t.Fatal(err)
	}
	fs.conf.FastResolve = false
	fs.conf.DirEntryTimeout = time.Minute
	fs.conf.AttrTimeout = time.Minute
	if err := fs.Mkdir(ctx, "/acldir", 0700, 0); err != 0 {
		t.Fatal(err)
	}
	aclInfo, err := fs.Stat(ctx, "/acldir")
	if err != 0 {
		t.Fatal(err)
	}
	rule := &acl.Rule{
		Owner:      7,
		Group:      0,
		Mask:       7,
		Other:      0,
		NamedUsers: acl.Entries{{Id: 2, Perm: 7}},
	}
	if err := fs.SetFacl(ctx, "/acldir", acl.TypeAccess, rule); err != 0 {
		t.Fatalf("SetFacl(/acldir): %s", err)
	}
	if err := fs.Mkdir(userCtx, "/acldir/sub", 0755, 0); err != 0 {
		t.Errorf("Mkdir after SetFacl on cached 0700 dir: got %s, want 0", err)
	}
	// Also verify cross-client ACL grant when local fs.attrs cache still holds stale 0700 attr.
	if err := fs.Mkdir(ctx, "/acldir2", 0700, 0); err != 0 {
		t.Fatal(err)
	}
	aclInfo2, err := fs.Stat(ctx, "/acldir2")
	if err != 0 {
		t.Fatal(err)
	}
	if err := fs.m.SetFacl(ctx, aclInfo2.Inode(), acl.TypeAccess, rule); err != 0 {
		t.Fatalf("remote SetFacl(/acldir2): %s", err)
	}
	if err := fs.Mkdir(userCtx, "/acldir2/sub", 0755, 0); err != 0 {
		t.Errorf("Mkdir after remote SetFacl on cached 0700 dir: got %s, want 0", err)
	}
	_ = aclInfo

	// Cached 0700 parent directory removed and recreated with 0777 by another client
	// must be re-resolved on Mkdir / Create / deep Mkdir (both without and with trash).
	fs.rotateAccessLog = 300 << 20
	for _, trashDays := range []int{0, 1} {
		format := fs.Meta().GetFormat()
		format.TrashDays = trashDays
		if err := fs.Meta().Init(&format, false); err != nil {
			t.Fatal(err)
		}
		if err := fs.Mkdir(ctx, "/recreated", 0700, 0); err != 0 {
			t.Fatal(err)
		}
		if _, err := fs.Stat(ctx, "/recreated"); err != 0 {
			t.Fatal(err)
		}
		if err := fs.m.Rmdir(ctx, meta.RootInode, "recreated"); err != 0 {
			t.Fatal(err)
		}
		var newParentIno, newSubIno Ino
		if err := fs.m.Mkdir(ctx, meta.RootInode, "recreated", 0777, 0, 0, &newParentIno, nil); err != 0 {
			t.Fatal(err)
		}
		if err := fs.m.Mkdir(ctx, newParentIno, "sub", 0777, 0, 0, &newSubIno, nil); err != 0 {
			t.Fatal(err)
		}
		if err := fs.Mkdir(userCtx, "/recreated/newdir", 0755, 0); err != 0 {
			t.Errorf("Mkdir(/recreated/newdir) with trashDays=%d: got %s, want 0", trashDays, err)
		}
		if cf, err := fs.Create(userCtx, "/recreated/newfile", 0644, 0); err != 0 {
			t.Errorf("Create(/recreated/newfile) with trashDays=%d: got %s, want 0", trashDays, err)
		} else {
			_ = cf.Close(userCtx)
		}
		// Reset cache to stale 0700 parent to also test intermediate traversal (/recreated/sub/deep).
		if err := fs.Rmr(ctx, "/recreated", true, 1); err != 0 {
			t.Fatal(err)
		}
		if err := fs.Mkdir(ctx, "/recreated", 0700, 0); err != 0 {
			t.Fatal(err)
		}
		if _, err := fs.Stat(ctx, "/recreated"); err != 0 {
			t.Fatal(err)
		}
		if err := fs.m.Rmdir(ctx, meta.RootInode, "recreated"); err != 0 {
			t.Fatal(err)
		}
		if err := fs.m.Mkdir(ctx, meta.RootInode, "recreated", 0777, 0, 0, &newParentIno, nil); err != 0 {
			t.Fatal(err)
		}
		if err := fs.m.Mkdir(ctx, newParentIno, "sub", 0777, 0, 0, &newSubIno, nil); err != 0 {
			t.Fatal(err)
		}
		if err := fs.Mkdir(userCtx, "/recreated/sub/deep", 0755, 0); err != 0 {
			t.Errorf("Mkdir(/recreated/sub/deep) with trashDays=%d: got %s, want 0", trashDays, err)
		}
		if err := fs.Rmr(ctx, "/recreated", true, 1); err != 0 {
			t.Fatal(err)
		}
	}
}

func TestResolveSubdir(t *testing.T) {
	fs := createTestFS(t)
	ctx := meta.Background()
	for _, p := range []string{"/data/a/b", "/data_wrong", "/other", "/outside"} {
		if err := fs.MkdirAll(ctx, p, 0777, 0); err != 0 {
			t.Fatal(err)
		}
	}
	fs.conf.Mountpoint = "/mnt/jfs"
	for link, target := range map[string]string{
		"/data/ln": "a/b", "/data/abs": "/mnt/jfs/data/a/b",
		"/data/out": "../outside", "/data/absout": "/mnt/jfs/outside",
	} {
		if err := fs.Symlink(ctx, target, link); err != 0 {
			t.Fatal(err)
		}
	}
	want, err := fs.Stat(ctx, "/data/a")
	if err != 0 {
		t.Fatal(err)
	}
	fs.subdirPrefixes = []string{"/data", "/other"}
	for _, fast := range []bool{false, true} {
		fs.conf.FastResolve = fast
		for _, p := range []string{"/data/a/b/..", "/data/ln/..", "/data/abs/.."} {
			if fi, err := fs.Stat(ctx, p); err != 0 || fi.Inode() != want.Inode() {
				t.Errorf("Stat(%q), fast=%v: %v, %s", p, fast, fi, err)
			}
		}
		for _, p := range []string{"/", "/data_wrong", "/data/..", "/data/../outside", "/data/out", "/data/absout", "/data/out/../data"} {
			if _, err := fs.Stat(ctx, p); err != syscall.EACCES {
				t.Errorf("Stat(%q), fast=%v: got %s, want EACCES", p, fast, err)
			}
		}
		if _, err := fs.Stat(ctx, "/data/missing/.."); err != syscall.ENOENT {
			t.Errorf("missing component must be looked up: %s", err)
		}
		if _, err := fs.Stat(ctx, "/data/../other"); err != 0 {
			t.Errorf("second allowed subdir: %s", err)
		}
		if fi, err := fs.Lstat(ctx, "/data/out"); err != 0 || !fi.IsSymlink() {
			t.Errorf("Lstat should not follow the final symlink: %v, %s", fi, err)
		}
		if err := fs.Mkdir(ctx, "/data/out/new", 0777, 0); err != syscall.EACCES {
			t.Errorf("Mkdir outside subdir: %s", err)
		}
		if err := fs.Mkdir(ctx, "/data/..", 0777, 0); err != syscall.EACCES {
			t.Errorf("Mkdir /data/.. outside subdir: %s", err)
		}
		if err := fs.Rmr(ctx, "/data/..", false, 1); err != syscall.EACCES {
			t.Errorf("Rmr /data/.. outside subdir: %s", err)
		}
	}
	fs.subdirPrefixes = []string{"/data/a"}
	if _, err := fs.Stat(ctx, "/data/a/b"); err != 0 {
		t.Errorf("should traverse the subdir's ancestors: %s", err)
	}
	if _, err := fs.Stat(ctx, "/data"); err != syscall.EACCES {
		t.Errorf("should not return the subdir's ancestor: %s", err)
	}

	// Stale cached child moved outside subdir by another client must not escape via "..".
	fs.subdirPrefixes = nil
	if err := fs.MkdirAll(ctx, "/data/child", 0777, 0); err != 0 {
		t.Fatal(err)
	}
	f, err := fs.Create(ctx, "/outside/secret", 0666, 0)
	if err != 0 {
		t.Fatal(err)
	}
	_ = f.Close(ctx)
	dataInfo, err := fs.Stat(ctx, "/data")
	if err != 0 {
		t.Fatal(err)
	}
	outsideInfo, err := fs.Stat(ctx, "/outside")
	if err != 0 {
		t.Fatal(err)
	}
	fs.subdirPrefixes = []string{"/data"}
	if _, err := fs.Stat(ctx, "/data/child"); err != 0 {
		t.Fatal(err)
	}
	if err := fs.m.Rename(ctx, dataInfo.Inode(), "child", outsideInfo.Inode(), "child", 0, nil, nil); err != 0 {
		t.Fatal(err)
	}
	if _, err := fs.Stat(ctx, "/data/child/../secret"); err != syscall.ENOENT {
		t.Errorf("stale cached child with .. must not escape subdir: got %s, want ENOENT", err)
	}
}

type fastResolveMeta struct {
	meta.Meta
	conf *meta.Config
}

func (m fastResolveMeta) Resolve(ctx meta.Context, parent Ino, p string, inode *Ino, attr *Attr, _ bool) syscall.Errno {
	if m.conf != nil && m.conf.CaseInsensi {
		return syscall.ENOTSUP
	}
	if p == "/" {
		*inode = meta.RootInode
		return m.Meta.GetAttr(ctx, meta.RootInode, attr)
	}
	return m.Meta.Resolve(ctx, parent, p, inode, attr, true)
}

type lookupErrMeta struct {
	meta.Meta
	name string
	err  syscall.Errno
}

func (m lookupErrMeta) Lookup(ctx meta.Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno {
	if name == m.name {
		return m.err
	}
	return m.Meta.Lookup(ctx, parent, name, inode, attr, checkPerm)
}

func TestFastResolveRenameIntoSubdir(t *testing.T) {
	fs := createTestFS(t)
	fs.m = fastResolveMeta{Meta: fs.m, conf: fs.conf.Meta}
	fs.conf.FastResolve = true
	ctx := meta.Background()
	if err := fs.MkdirAll(ctx, "/a/b/c/d", 0777, 0); err != 0 {
		t.Fatal(err)
	}
	if err := fs.Rename(ctx, "/a", "/a/b/c/d/moved", 0); err != syscall.EINVAL {
		t.Fatalf("Rename(/a, /a/b/c/d/moved): got %s, want EINVAL", err)
	}
	if err := fs.Rename(ctx, "/a/b/c/d", "/a", meta.RenameExchange); err != syscall.EINVAL {
		t.Fatalf("RenameExchange(/a/b/c/d, /a): got %s, want EINVAL", err)
	}
	if _, err := fs.Stat(ctx, "/a/b/c/d"); err != 0 {
		t.Fatalf("/a/b/c/d should remain intact: %s", err)
	}

	if err := fs.Symlink(ctx, "../A/b/c/d", "/a/link"); err != 0 {
		t.Fatal(err)
	}
	fs.conf.Meta.CaseInsensi = true
	if err := fs.Rename(ctx, "/a", "/a/link/moved", 0); err != syscall.EINVAL {
		t.Fatalf("case-insensitive Rename(/a, /a/link/moved): got %s, want EINVAL", err)
	}
	if err := fs.Rename(ctx, "/a/link", "/a", meta.RenameExchange); err != syscall.EINVAL {
		t.Fatalf("case-insensitive RenameExchange(/a/link, /a): got %s, want EINVAL", err)
	}
	if _, err := fs.Stat(ctx, "/a/b/c/d"); err != 0 {
		t.Fatalf("/a/b/c/d should remain intact after case-insensitive rename: %s", err)
	}
	fs.InvalidateEntry(meta.RootInode, "a")
	fs.InvalidateEntry(meta.RootInode, "A")

	// Stale cached source inode replaced by another client must not bypass loop check.
	fs.conf.FastResolve = false
	fs.conf.DirEntryTimeout = time.Minute
	fs.conf.EntryTimeout = time.Minute
	if _, err := fs.Lstat(ctx, "/a/link"); err != 0 {
		t.Fatalf("cache /a and /a/link: %s", err)
	}
	if err := fs.m.Rename(ctx, meta.RootInode, "a", meta.RootInode, "old", 0, nil, nil); err != 0 {
		t.Fatalf("client2 rename /a -> /old: %s", err)
	}
	var newA, newB, newC, newD Ino
	if err := fs.m.Mkdir(ctx, meta.RootInode, "a", 0777, 0, 0, &newA, nil); err != 0 {
		t.Fatal(err)
	}
	if err := fs.m.Mkdir(ctx, newA, "b", 0777, 0, 0, &newB, nil); err != 0 {
		t.Fatal(err)
	}
	if err := fs.m.Mkdir(ctx, newB, "c", 0777, 0, 0, &newC, nil); err != 0 {
		t.Fatal(err)
	}
	if err := fs.m.Mkdir(ctx, newC, "d", 0777, 0, 0, &newD, nil); err != 0 {
		t.Fatal(err)
	}
	if err := fs.Rename(ctx, "/a", "/a/link/moved", 0); err != syscall.EINVAL {
		t.Fatalf("client1 Rename(/a, /a/link/moved) with stale cached /a: got %s, want EINVAL", err)
	}
	fs.InvalidateEntry(meta.RootInode, "a")
	fs.InvalidateEntry(meta.RootInode, "A")
	if _, err := fs.Stat(ctx, "/a/b/c/d"); err != 0 {
		t.Fatalf("new /a/b/c/d should remain intact: %s", err)
	}
	fs.conf.DirEntryTimeout = time.Millisecond * 100
	fs.conf.EntryTimeout = time.Millisecond * 100
	fs.conf.FastResolve = true

	// Distinct Unicode names under meta.CaseInsensi ("/i" vs "/İ" U+0130) must not collide in entry cache.
	fi1, err := fs.Create(ctx, "/i", 0666, 0)
	if err != 0 {
		t.Fatalf("Create(/i): %s", err)
	}
	_ = fi1.Close(ctx)
	fi2, err := fs.Create(ctx, "/\u0130", 0666, 0)
	if err != 0 {
		t.Fatalf("Create(/İ): %s", err)
	}
	_ = fi2.Close(ctx)
	if _, err := fs.Stat(ctx, "/i"); err != 0 {
		t.Fatalf("Stat(/i): %s", err)
	}
	wf, err := fs.Open(ctx, "/\u0130", vfs.MODE_MASK_W)
	if err != 0 {
		t.Fatalf("Open(/İ): %s", err)
	}
	if _, err := wf.Write(ctx, []byte("x")); err != 0 {
		t.Fatalf("Write(/İ): %s", err)
	}
	if err := wf.Close(ctx); err != 0 {
		t.Fatalf("Close(/İ): %s", err)
	}
	if st1, err := fs.Stat(ctx, "/i"); err != 0 || st1.Size() != 0 {
		t.Fatalf("/i size should remain 0, got %v, err=%s", st1, err)
	}
	if st2, err := fs.Stat(ctx, "/\u0130"); err != 0 || st2.Size() != 1 {
		t.Fatalf("/İ size should be 1, got %v, err=%s", st2, err)
	}
	fs.conf.Meta.CaseInsensi = false

	// Deep directory hierarchy (> 1024 levels) must still reject moving an ancestor into its descendant.
	deep := "/deep"
	if err := fs.Mkdir(ctx, deep, 0777, 0); err != 0 {
		t.Fatal(err)
	}
	curInfo, err := fs.Stat(ctx, deep)
	if err != 0 {
		t.Fatal(err)
	}
	curIno := curInfo.Inode()
	for i := 0; i < 1025; i++ {
		var childIno Ino
		if err := fs.m.Mkdir(ctx, curIno, "d", 0777, 0, 0, &childIno, nil); err != 0 {
			t.Fatalf("mkdir level %d: %s", i, err)
		}
		curIno = childIno
		deep += "/d"
	}
	if err := fs.Rename(ctx, "/deep", deep+"/moved", 0); err != syscall.EINVAL {
		t.Fatalf("Rename(/deep, <1025 levels>/moved): got %s, want EINVAL", err)
	}
	if _, err := fs.Stat(ctx, "/deep/d"); err != 0 {
		t.Fatalf("/deep/d should remain intact: %s", err)
	}

	f, err := fs.Create(ctx, "/f", 0666, 0)
	if err != 0 {
		t.Fatal(err)
	}
	_ = f.Close(ctx)
	fs.m = lookupErrMeta{Meta: fs.m, name: "ioerr", err: syscall.EIO}
	if err := fs.Link(ctx, "/f", "/ioerr/"); err != syscall.EIO {
		t.Fatalf("Link(/f, /ioerr/): got %s, want EIO", err)
	}
	if err := fs.Symlink(ctx, "x", "/ioerr/"); err != syscall.EIO {
		t.Fatalf("Symlink(x, /ioerr/): got %s, want EIO", err)
	}
}

func createTestFS(t testing.TB) *FileSystem {
	metaConf := meta.DefaultConf()
	m := meta.NewClient("memkv://", metaConf)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: metaConf,
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
	objStore, _ := object.CreateStorage("mem", "", "", "", "")
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
