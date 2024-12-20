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

package vfs

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// nolint:errcheck

func createTestVFS(applyMetaConfOption func(metaConfig *meta.Config), metaUri string) (*VFS, object.ObjectStorage) {
	mp := "/jfs"
	metaConf := meta.DefaultConf()
	metaConf.MountPoint = mp
	if applyMetaConfOption != nil {
		applyMetaConfOption(metaConf)
	}
	if metaUri == "" {
		metaUri = "memkv://"
	}
	m := meta.NewClient(metaUri, metaConf)
	format := &meta.Format{
		Name:        "test",
		UUID:        uuid.New().String(),
		Storage:     "mem",
		BlockSize:   4096,
		Compression: "lz4",
		DirStats:    true,
	}
	err := m.Init(format, true)
	if err != nil {
		log.Fatalf("setting: %s", err)
	}
	conf := &Config{
		Meta:    metaConf,
		Format:  *format,
		Version: "Juicefs",
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			Compress:   format.Compression,
			MaxUpload:  2,
			BufferSize: 30 << 20,
			CacheSize:  10 << 20,
			CacheDir:   "memory",
		},
		FuseOpts: &FuseOptions{},
	}
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_",
		prometheus.WrapRegistererWith(prometheus.Labels{"mp": mp, "vol_name": format.Name}, registry))
	store := chunk.NewCachedStore(blob, *conf.Chunk, registry)
	return NewVFS(conf, m, store, registerer, registry), blob
}

func TestVFSBasic(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))

	if st, e := v.StatFS(ctx, 1); e != 0 {
		t.Fatalf("statfs 1: %s", e)
	} else if st.Total-st.Avail != 0 {
		t.Fatalf("used: %d", st.Total-st.Avail)
	}

	// dirs
	de, e := v.Mkdir(ctx, 1, "d1", 0755, 0)
	if e != 0 {
		t.Fatalf("mkdir d1: %s", e)
	}
	if _, e := v.Mkdir(ctx, de.Inode, "d2", 0755, 0); e != 0 {
		t.Fatalf("mkdir d1/d2: %s", e)
	}
	if e := v.Rmdir(ctx, 1, "d1"); e != syscall.ENOTEMPTY {
		t.Fatalf("rmdir not empty: %s", e)
	}
	if e := v.Rmdir(ctx, de.Inode, "d2"); e != 0 {
		t.Fatalf("rmdir d1/d2: %s", e)
	}

	// files
	fe, e := v.Mknod(ctx, de.Inode, "f1", 0644|syscall.S_IFREG, 0, 0)
	if e != 0 {
		t.Fatalf("mknod d1/f1: %s", e)
	}
	if e := v.Access(ctx, fe.Inode, unix.X_OK); e != syscall.EACCES {
		t.Fatalf("access d1/f1: %s", e)
	}
	if _, e := v.SetAttr(ctx, fe.Inode, meta.SetAttrMtimeNow|meta.SetAttrAtimeNow, 0, 0, 0, 0, 0, 0, 0, 0, 0); e != 0 {
		t.Fatalf("setattr d1/f2 mtimeNow: %s", e)
	}
	if fe2, e := v.SetAttr(ctx, fe.Inode, meta.SetAttrMode|meta.SetAttrUID|meta.SetAttrGID|meta.SetAttrAtime|meta.SetAttrMtime|meta.SetAttrSize, 0, 0755, 1, 3, 1234, 1234, 5678, 5678, 1024); e != 0 {
		t.Fatalf("setattr d1/f1: %s %d %d", e, fe2.Attr.Gid, fe2.Attr.Length)
	} else if fe2.Attr.Mode != 0755 || fe2.Attr.Uid != 1 || fe2.Attr.Gid != 3 || fe2.Attr.Atime != 1234 || fe2.Attr.Atimensec != 5678 || fe2.Attr.Mtime != 1234 || fe2.Attr.Mtimensec != 5678 || fe2.Attr.Length != 1024 {
		t.Fatalf("setattr d1/f1: %+v", fe2.Attr)
	}
	if e := v.Access(ctx, fe.Inode, unix.X_OK); e != 0 {
		t.Fatalf("access d1/f1: %s", e)
	}
	if _, e := v.Link(ctx, fe.Inode, 1, "f2"); e != 0 {
		t.Fatalf("link f2->f1: %s", e)
	}
	if fe, e := v.GetAttr(ctx, fe.Inode, 0); e != 0 || fe.Attr.Nlink != 2 {
		t.Fatalf("getattr d1/f2: %s %d", e, fe.Attr.Nlink)
	}
	if e := v.Unlink(ctx, de.Inode, "f1"); e != 0 {
		t.Fatalf("unlink d1/f1: %s", e)
	}
	if fe, e := v.Lookup(ctx, 1, "f2"); e != 0 || fe.Attr.Nlink != 1 {
		t.Fatalf("lookup f2: %s", e)
	}
	if e := v.Rename(ctx, 1, "f2", 1, "f3", 0); e != 0 {
		t.Fatalf("rename f2 -> f3: %s", e)
	}
	if fe, fh, e := v.Open(ctx, fe.Inode, syscall.O_RDONLY); e != 0 {
		t.Fatalf("open f3: %s", e)
	} else if e := v.Flush(ctx, fe.Inode, fh, 0); e != 0 {
		t.Fatalf("close f3: %s", e)
	} else {
		v.Release(ctx, fe.Inode, fh)
	}

	// symlink
	if fe, e := v.Symlink(ctx, "f2", 1, "sym"); e != 0 {
		t.Fatalf("symlink sym -> f2: %s", e)
	} else if target, e := v.Readlink(ctx, fe.Inode); e != 0 || string(target) != "f2" {
		t.Fatalf("readlink sym: %s %s", e, string(target))
	}

	// edge cases
	longName := strings.Repeat("a", 256)
	if _, e = v.Lookup(ctx, 1, longName); e != syscall.ENAMETOOLONG {
		t.Fatalf("lookup long name")
	}
	if _, _, e = v.Create(ctx, 1, longName, 0, 0, 0); e != syscall.ENAMETOOLONG {
		t.Fatalf("create long name")
	}
	if _, e = v.Mknod(ctx, 1, longName, 0, 0, 0); e != syscall.ENAMETOOLONG {
		t.Fatalf("mknod long name")
	}
	if _, e = v.Mkdir(ctx, 1, longName, 0, 0); e != syscall.ENAMETOOLONG {
		t.Fatalf("mkdir long name")
	}
	if _, e = v.Link(ctx, 2, 1, longName); e != syscall.ENAMETOOLONG {
		t.Fatalf("link long name")
	}
	if e = v.Unlink(ctx, 1, longName); e != syscall.ENAMETOOLONG {
		t.Fatalf("unlink long name")
	}
	if e = v.Rmdir(ctx, 1, longName); e != syscall.ENAMETOOLONG {
		t.Fatalf("rmdir long name")
	}
	if _, e = v.Symlink(ctx, "", 1, longName); e != syscall.ENAMETOOLONG {
		t.Fatalf("symlink long name")
	}
	if e = v.Rename(ctx, 1, "a", 1, longName, 0); e != syscall.ENAMETOOLONG {
		t.Fatalf("rename long name")
	}
	if e = v.Rename(ctx, 1, longName, 1, "a", 0); e != syscall.ENAMETOOLONG {
		t.Fatalf("rename long name")
	}

}

func TestVFSIO(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	fe, fh, e := v.Create(ctx, 1, "file", 0755, 0, syscall.O_RDWR)
	if e != 0 {
		t.Fatalf("create file: %s", e)
	}
	if e = v.Fallocate(ctx, fe.Inode, 0, 0, 64<<10, fh); e != 0 {
		t.Fatalf("fallocate : %s", e)
	}
	if e = v.Write(ctx, fe.Inode, []byte("hello"), 0, fh); e != 0 {
		t.Fatalf("write file: %s", e)
	}
	if e = v.Fsync(ctx, fe.Inode, 1, fh); e != 0 {
		t.Fatalf("fsync file: %s", e)
	}
	if e = v.Write(ctx, fe.Inode, []byte("hello"), 100<<20, fh); e != 0 {
		t.Fatalf("write file: %s", e)
	}
	var attr meta.Attr
	if e = v.Truncate(ctx, fe.Inode, (100<<20)+2, fh, &attr); e != 0 {
		t.Fatalf("truncate file: %s", e)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, fh, 10<<20, 10, 0); e != 0 || n != 10 {
		t.Fatalf("copyfilerange: %s %d", e, n)
	}
	var buf = make([]byte, 128<<10)
	if n, e := v.Read(ctx, fe.Inode, buf, 0, fh); e != 0 {
		t.Fatalf("read file: %s", e)
	} else if n != len(buf) {
		t.Fatalf("short read file: %d != %d", n, len(buf))
	} else if string(buf[:5]) != "hello" {
		t.Fatalf("unexpected data: %q", string(buf[:5]))
	}
	if n, e := v.Read(ctx, fe.Inode, buf[:6], 10<<20, fh); e != 0 || n != 6 || string(buf[:n]) != "hello\x00" {
		t.Fatalf("read file end: %s %d %s", e, n, string(buf[:n]))
	}
	if n, e := v.Read(ctx, fe.Inode, buf, 100<<20, fh); e != 0 || n != 2 || string(buf[:n]) != "he" {
		t.Fatalf("read file end: %s %d %s", e, n, string(buf[:n]))
	}
	if e = v.Flush(ctx, fe.Inode, fh, 0); e != 0 {
		t.Fatalf("flush file: %s", e)
	}

	// edge cases
	_, fh2, _ := v.Open(ctx, fe.Inode, syscall.O_RDONLY)
	_, fh3, _ := v.Open(ctx, fe.Inode, syscall.O_WRONLY)
	wHandle := v.findHandle(fe.Inode, fh3)
	if wHandle == nil {
		t.Fatalf("failed to find O_WRONLY handle")
	}
	wHandle.reader = nil
	// read
	if _, e = v.Read(ctx, fe.Inode, nil, 0, 0); e != syscall.EBADF {
		t.Fatalf("read bad fd: %s", e)
	}
	if _, e = v.Read(ctx, fe.Inode, make([]byte, 1024), 0, fh3); e != syscall.EBADF {
		t.Fatalf("read write-only fd: %s", e)
	}
	if _, e = v.Read(ctx, fe.Inode, nil, 1<<60, fh2); e != syscall.EFBIG {
		t.Fatalf("read off too big: %s", e)
	}
	// write
	if e = v.Write(ctx, fe.Inode, nil, 0, 0); e != syscall.EBADF {
		t.Fatalf("write bad fd: %s", e)
	}
	if e = v.Write(ctx, fe.Inode, nil, 1<<60, fh2); e != syscall.EFBIG {
		t.Fatalf("write off too big: %s", e)
	}
	if e = v.Write(ctx, fe.Inode, make([]byte, 1024), 0, fh2); e != syscall.EBADF {
		t.Fatalf("write read-only fd: %s", e)
	}
	// truncate
	if e = v.Truncate(ctx, fe.Inode, -1, 0, &meta.Attr{}); e != syscall.EINVAL {
		t.Fatalf("truncate invalid off,length: %s", e)
	}
	if e = v.Truncate(ctx, fe.Inode, 1<<60, 0, &meta.Attr{}); e != syscall.EFBIG {
		t.Fatalf("truncate too large: %s", e)
	}
	// fallocate
	if e = v.Fallocate(ctx, fe.Inode, 0, -1, -1, fh); e != syscall.EINVAL {
		t.Fatalf("fallocate invalid off,length: %s", e)
	}
	if e = v.Fallocate(ctx, statsInode, 0, 0, 1, fh); e != syscall.EPERM {
		t.Fatalf("fallocate invalid off,length: %s", e)
	}
	if e = v.Fallocate(ctx, fe.Inode, 0, 0, 100, 0); e != syscall.EBADF {
		t.Fatalf("fallocate invalid off,length: %s", e)
	}
	if e = v.Fallocate(ctx, fe.Inode, 0, 1<<60, 1<<60, fh); e != syscall.EFBIG {
		t.Fatalf("fallocate invalid off,length: %s", e)
	}
	if e = v.Fallocate(ctx, fe.Inode, 0, 1<<10, 1<<20, fh2); e != syscall.EBADF {
		t.Fatalf("fallocate read-only fd: %s", e)
	}

	// copy file range
	if n, e := v.CopyFileRange(ctx, statsInode, fh, 0, fe.Inode, fh, 10<<20, 10, 0); e != syscall.ENOTSUP {
		t.Fatalf("copyfilerange internal file: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, statsInode, fh, 10<<20, 10, 0); e != syscall.EPERM {
		t.Fatalf("copyfilerange internal file: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, 0, 0, fe.Inode, fh, 10<<20, 10, 0); e != syscall.EBADF {
		t.Fatalf("copyfilerange invalid fh: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, 0, 10<<20, 10, 0); e != syscall.EBADF {
		t.Fatalf("copyfilerange invalid fh: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, fh, 10<<20, 10, 1); e != syscall.EINVAL {
		t.Fatalf("copyfilerange invalid flag: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, fh, 10<<20, 1<<50, 0); e != syscall.EINVAL {
		t.Fatalf("copyfilerange overlap: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, fh, 1<<63, 1<<63, 0); e != syscall.EFBIG {
		t.Fatalf("copyfilerange too big file: %s %d", e, n)
	}
	if n, e := v.CopyFileRange(ctx, fe.Inode, fh, 0, fe.Inode, fh2, 1<<20, 1<<10, 0); e != syscall.EACCES {
		t.Fatalf("copyfilerange too big file: %s %d", e, n)
	}

	// sequntial write/read
	for i := uint64(0); i < 1001; i++ {
		if e := v.Write(ctx, fe.Inode, make([]byte, 128<<10), i*(128<<10), fh); e != 0 {
			t.Fatalf("write big file: %s", e)
		}
	}
	buf = make([]byte, 128<<10)
	for i := uint64(0); i < 1000; i++ {
		if n, e := v.Read(ctx, fe.Inode, buf, i*(128<<10), fh); e != 0 || n != (128<<10) {
			t.Fatalf("read big file: %s", e)
		} else {
			for j := 0; j < 128<<10; j++ {
				if buf[j] != 0 {
					t.Fatalf("read big file: %d %d", j, buf[j])
				}
			}
		}
	}
	// many small write
	buf = make([]byte, 5<<10)
	for j := range buf {
		buf[j] = 1
	}
	for i := int64(32 - 1); i >= 0; i-- {
		if e := v.Write(ctx, fe.Inode, buf, uint64(i)*(4<<10), fh); e != 0 {
			t.Fatalf("write big file: %s", e)
		}
	}
	time.Sleep(time.Millisecond * 1500) // wait for it to be flushed
	buf = make([]byte, 128<<10)
	if n, e := v.Read(ctx, fe.Inode, buf, 0, fh); e != 0 || n != (128<<10) {
		t.Fatalf("read big file: %s", e)
	} else {
		for j := range buf {
			if buf[j] != 1 {
				t.Fatalf("read big file: %d %d", j, buf[j])
			}
		}
	}

	v.Release(ctx, fe.Inode, fh)
}

func TestVFSXattrs(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	fe, e := v.Mkdir(ctx, 1, "xattrs", 0755, 0)
	if e != 0 {
		t.Fatalf("mkdir xattrs: %s", e)
	}
	// normal cases
	if _, e := v.GetXattr(ctx, fe.Inode, "test", 0); e != meta.ENOATTR {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if e := v.SetXattr(ctx, fe.Inode, "test", []byte("value"), 0); e != 0 {
		t.Fatalf("setxattr test: %s", e)
	}
	if e = v.SetXattr(ctx, fe.Inode, "test", []byte("v1"), meta.XattrCreate); e == 0 {
		t.Fatalf("setxattr test (create): %s", e)
	}
	if v, e := v.ListXattr(ctx, fe.Inode, 100); e != 0 || string(v) != "test\x00" {
		t.Fatalf("listxattr: %s %q", e, string(v))
	}
	if v, e := v.GetXattr(ctx, fe.Inode, "test", 5); e != 0 || string(v) != "value" {
		t.Fatalf("getxattr test: %s %v", e, v)
	}
	if e = v.SetXattr(ctx, fe.Inode, "test", []byte("v2"), meta.XattrReplace); e != 0 {
		t.Fatalf("setxattr test (replace): %s", e)
	}
	if v, e := v.GetXattr(ctx, fe.Inode, "test", 5); e != 0 || string(v) != "v2" {
		t.Fatalf("getxattr test: %s %v", e, v)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "test", 1); e != syscall.ERANGE {
		t.Fatalf("getxattr large value: %s", e)
	}
	if v, e := v.ListXattr(ctx, fe.Inode, 1); e != syscall.ERANGE {
		t.Fatalf("listxattr: %s %q", e, string(v))
	}
	if e := v.RemoveXattr(ctx, fe.Inode, "test"); e != 0 {
		t.Fatalf("removexattr test: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "test", 0); e != meta.ENOATTR {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if v, e := v.ListXattr(ctx, fe.Inode, 100); e != 0 || string(v) != "" {
		t.Fatalf("listxattr: %s %q", e, string(v))
	}
	// edge case
	if e = v.SetXattr(ctx, fe.Inode, "", []byte("v2"), 0); e != syscall.EINVAL {
		t.Fatalf("setxattr long key: %s", e)
	}
	if e = v.SetXattr(ctx, fe.Inode, strings.Repeat("test", 100), []byte("v2"), 0); e != syscall.EPERM && e != syscall.ERANGE {
		t.Fatalf("setxattr long key: %s", e)
	}
	if e = v.SetXattr(ctx, fe.Inode, "test", make([]byte, 1<<20), 0); e != syscall.E2BIG && e != syscall.ERANGE {
		t.Fatalf("setxattr long key: %s", e)
	}
	if e = v.SetXattr(ctx, fe.Inode, "system.posix_acl_access", []byte("v2"), 0); e != syscall.ENOTSUP {
		t.Fatalf("setxattr long key: %s", e)
	}
	if e = v.SetXattr(ctx, configInode, "test", []byte("v2"), 0); e != syscall.EPERM {
		t.Fatalf("setxattr long key: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "", 0); e != syscall.EINVAL {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, strings.Repeat("test", 100), 0); e == 0 {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if _, e := v.GetXattr(ctx, configInode, "test", 0); e != meta.ENOATTR {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "system.posix_acl_access", 0); e != syscall.ENODATA {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if v, e := v.ListXattr(ctx, configInode, 0); e != meta.ENOATTR {
		t.Fatalf("listxattr: %s %q", e, string(v))
	}
	if e := v.RemoveXattr(ctx, fe.Inode, strings.Repeat("test", 100)); e != syscall.EPERM && e != syscall.ERANGE {
		t.Fatalf("removexattr test: %s", e)
	}
	if e := v.RemoveXattr(ctx, fe.Inode, ""); e != syscall.EINVAL {
		t.Fatalf("removexattr test: %s", e)
	}
	if e := v.RemoveXattr(ctx, fe.Inode, "system.posix_acl_access"); e != syscall.ENOTSUP {
		t.Fatalf("removexattr test: %s", e)
	}
	if e := v.RemoveXattr(ctx, configInode, "test"); e != syscall.EPERM {
		t.Fatalf("removexattr test: %s", e)
	}
}

type accessCase struct {
	uid  uint32
	gid  uint32
	mode uint16
	r    syscall.Errno
}

func TestAccessMode(t *testing.T) {
	var attr = meta.Attr{
		Uid:  1,
		Gid:  2,
		Mode: 0751,
	}

	cases := []accessCase{
		{0, 0, MODE_MASK_R | MODE_MASK_W | MODE_MASK_X, 0},
		{1, 3, MODE_MASK_R | MODE_MASK_W | MODE_MASK_X, 0},
		{2, 2, MODE_MASK_R | MODE_MASK_X, 0},
		{2, 2, MODE_MASK_W, syscall.EACCES},
		{3, 4, MODE_MASK_X, 0},
		{3, 4, MODE_MASK_R, syscall.EACCES},
		{3, 4, MODE_MASK_W, syscall.EACCES},
	}
	for _, c := range cases {
		if e := accessTest(&attr, c.mode, c.uid, c.gid); e != c.r {
			t.Fatalf("expect %s on case %+v, but got %s", c.r, c, e)
		}
	}
}

func assertEqual(t *testing.T, a interface{}, b interface{}) {
	if reflect.DeepEqual(a, b) {
		return
	}
	message := fmt.Sprintf("%v != %v", a, b)
	t.Fatal(message)
}

func TestSetattrStr(t *testing.T) {
	assertEqual(t, setattrStr(0, 0, 0, 0, 0, 0, 0), "")
	assertEqual(t, setattrStr(meta.SetAttrMode, 01755, 0, 0, 0, 0, 0), "mode=?rwxr-xr-t:01755")
	assertEqual(t, setattrStr(meta.SetAttrUID, 0, 1, 0, 0, 0, 0), "uid=1")
	assertEqual(t, setattrStr(meta.SetAttrGID, 0, 1, 2, 0, 0, 0), "gid=2")
	assertEqual(t, setattrStr(meta.SetAttrAtime, 0, 0, 0, -2, -1, 0), "atime=NOW")
	assertEqual(t, setattrStr(meta.SetAttrAtime, 0, 0, 0, 123, 123, 0), "atime=123")
	assertEqual(t, setattrStr(meta.SetAttrAtimeNow, 0, 0, 0, 0, 0, 0), "atime=NOW")
	assertEqual(t, setattrStr(meta.SetAttrMtime, 0, 0, 0, 0, -1, 0), "mtime=NOW")
	assertEqual(t, setattrStr(meta.SetAttrMtime, 0, 0, 0, 0, 123, 0), "mtime=123")
	assertEqual(t, setattrStr(meta.SetAttrMtimeNow, 0, 0, 0, 0, 0, 0), "mtime=NOW")
	assertEqual(t, setattrStr(meta.SetAttrSize, 0, 0, 0, 0, 0, 123), "size=123")
	assertEqual(t, setattrStr(meta.SetAttrUID|meta.SetAttrGID, 0, 1, 2, 0, 0, 0), "uid=1,gid=2")
}

func TestVFSLocks(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	fe, fh, e := v.Create(ctx, 1, "flock", 0644, 0, syscall.O_RDWR)
	if e != 0 {
		t.Fatalf("create flock: %s", e)
	}
	// flock
	if e = v.Flock(ctx, fe.Inode, fh, 123, 100, true); e != syscall.EINVAL {
		t.Fatalf("flock wr: %s", e)
	}
	if e = v.Flock(ctx, fe.Inode, fh, 123, syscall.F_WRLCK, true); e != 0 {
		t.Fatalf("flock wr: %s", e)
	}
	if e := v.Flock(ctx, fe.Inode, fh, 456, syscall.F_RDLCK, false); e != syscall.EAGAIN {
		t.Fatalf("flock rd: should block")
	}

	done := make(chan bool)
	go func() {
		_ = v.Flock(ctx, fe.Inode, fh, 456, syscall.F_RDLCK, true)
		done <- true
	}()
	if e := v.Flock(ctx, fe.Inode, fh, 123, syscall.F_UNLCK, true); e != 0 {
		t.Fatalf("flock unlock: %s", e)
	}
	select {
	case <-done:
	case <-time.NewTimer(time.Millisecond * 100).C:
		t.Fatalf("flock timeout on rdlock")
	}
	if e := v.Flock(ctx, fe.Inode, fh, 456, syscall.F_UNLCK, true); e != 0 {
		t.Fatalf("flock unlock rd: %s", e)
	}

	// posix lock
	if e = v.Setlk(ctx, fe.Inode, fh, 1, 0, 100, 100, 1, true); e != syscall.EINVAL {
		t.Fatalf("setlk: %s", e)
	}
	if e = v.Setlk(ctx, fe.Inode, fh, 1, 0, 100, syscall.F_WRLCK, 1, true); e != 0 {
		t.Fatalf("setlk: %s", e)
	}
	var start, len uint64 = 10, 1000
	var typ, pid uint32 = syscall.LOCK_UN, 10
	if e = v.Getlk(ctx, fe.Inode, fh, 2, &start, &len, &typ, &pid); e != syscall.EINVAL {
		t.Fatalf("getlk: %s", e)
	}
	typ = syscall.F_RDLCK
	if e = v.Getlk(ctx, fe.Inode, fh, 2, &start, &len, &typ, &pid); e != 0 {
		t.Fatalf("getlk: %s", e)
	} else if start != 0 || len != 100 || typ != syscall.F_WRLCK || pid != 1 {
		t.Fatalf("getlk result: %d %d %d %d", start, len, typ, pid)
	}
	if e = v.Setlk(ctx, fe.Inode, fh, 2, 10, 100, syscall.F_RDLCK, 10, false); e != syscall.EAGAIN {
		t.Fatalf("setlk rd: %s", e)
	}
	go func() {
		_ = v.Setlk(ctx, fe.Inode, fh, 2, 10, 100, syscall.F_RDLCK, 10, false)
		done <- true
	}()
	if e = v.Setlk(ctx, fe.Inode, fh, 1, 10, 100, syscall.F_UNLCK, 1, true); e != 0 {
		t.Fatalf("setlk unlock: %s", e)
	}
	select {
	case <-done:
	case <-time.NewTimer(time.Millisecond * 100).C:
		t.Fatalf("setlk timeout on rdlock")
	}
	if e = v.Setlk(ctx, fe.Inode, fh, 2, 0, 20, syscall.F_RDLCK, 10, false); e != syscall.EAGAIN {
		t.Fatalf("setlk rd: %s", e)
	}
	if e = v.Setlk(ctx, fe.Inode, fh, 1, 0, 1000, syscall.F_UNLCK, 1, true); e != 0 {
		t.Fatalf("setlk unlock: %s", e)
	}
	if e = v.Flush(ctx, fe.Inode, fh, 0); e != 0 {
		t.Fatalf("flush: %s", e)
	}
	v.Release(ctx, fe.Inode, fh)
	// invalid fd
	if e = v.Flock(ctx, fe.Inode, 10, 123, syscall.F_WRLCK, true); e != syscall.EBADF {
		t.Fatalf("flock wr: %s", e)
	}
	if e = v.Setlk(ctx, fe.Inode, 10, 1, 0, 1000, syscall.F_UNLCK, 1, true); e != syscall.EBADF {
		t.Fatalf("setlk unlock: %s", e)
	}
	if e = v.Getlk(ctx, fe.Inode, 10, 2, &start, &len, &typ, &pid); e != syscall.EBADF {
		t.Fatalf("getlk: %s", e)
	}
	// internal file
	fe, _ = v.Lookup(ctx, 1, ".stats")
	if e = v.Flock(ctx, fe.Inode, 10, 123, syscall.F_WRLCK, true); e != syscall.EPERM {
		t.Fatalf("flock wr: %s", e)
	}
	if e = v.Setlk(ctx, fe.Inode, 10, 1, 0, 1000, syscall.F_UNLCK, 1, true); e != syscall.EPERM {
		t.Fatalf("setlk unlock: %s", e)
	}
	if e = v.Getlk(ctx, fe.Inode, 10, 2, &start, &len, &typ, &pid); e != syscall.EPERM {
		t.Fatalf("getlk: %s", e)
	}
}

func TestInternalFile(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	// list internal files
	fh, _ := v.Opendir(ctx, 1, 0)
	entries, _, e := v.Readdir(ctx, 1, 1024, 0, fh, true)
	if e != 0 {
		t.Fatalf("readdir 1: %s", e)
	}
	internalFiles := make(map[string]bool)
	for _, e := range entries {
		if IsSpecialName(string(e.Name)) && e.Attr.Typ == meta.TypeFile {
			internalFiles[string(e.Name)] = true
		}
	}
	if len(internalFiles) != 3 {
		t.Fatalf("there should be 3 internal files but got %d", len(internalFiles))
	}
	v.Releasedir(ctx, 1, fh)

	// .config
	ctx2 := NewLogContext(meta.NewContext(10, 111, []uint32{222}))
	fe, e := v.Lookup(ctx2, 1, ".config")
	if e != 0 {
		t.Fatalf("lookup .config: %s", e)
	}
	if e := v.Access(ctx2, fe.Inode, unix.R_OK); e != syscall.EACCES { // other user can't access .config
		t.Fatalf("access .config: %s", e)
	}
	if _, e := v.GetAttr(ctx, fe.Inode, 0); e != 0 {
		t.Fatalf("getattr .config: %s", e)
	}
	// ignore setattr on internal files
	if fe2, e := v.SetAttr(ctx, fe.Inode, meta.SetAttrUID, 0, 0, ctx2.Uid(), 0, 0, 0, 0, 0, 0); e != 0 || fe2.Attr.Uid != fe.Attr.Uid {
		t.Fatalf("can't setattr on internal files")
	}
	if e = v.Unlink(ctx, 1, ".config"); e != syscall.EPERM {
		t.Fatalf("should not unlink internal file")
	}
	if _, _, e = v.Open(ctx, fe.Inode, syscall.O_WRONLY); e != syscall.EACCES {
		t.Fatalf("write .config: %s", e)
	}
	_, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDONLY)
	if e != 0 {
		t.Fatalf("open .config: %s", e)
	}
	buf := make([]byte, 10240)
	if _, e := v.Read(ctx, fe.Inode, buf, 0, 0); e != syscall.EBADF {
		t.Fatalf("read .config: %s", e)
	}
	if n, e := v.Read(ctx, fe.Inode, buf, 0, fh); e != 0 {
		t.Fatalf("read .config: %s", e)
	} else if !strings.Contains(string(buf[:n]), v.Conf.Format.UUID) {
		t.Fatalf("invalid config: %q", string(buf[:n]))
	}

	// .stats
	fe, e = v.Lookup(ctx, 1, ".stats")
	if e != 0 {
		t.Fatalf("lookup .stats: %s", e)
	}
	if e := v.Access(ctx, fe.Inode, unix.W_OK); e != 0 { // root can do everything
		t.Fatalf("access .stats: %s", e)
	}
	fe, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDONLY)
	if e != 0 {
		t.Fatalf("open .stats: %s", e)
	}
	defer v.Release(ctx, fe.Inode, fh)
	defer v.Flush(ctx, fe.Inode, fh, 0)
	buf = make([]byte, 128<<10)
	n, e := v.Read(ctx, fe.Inode, buf[:4<<10], 0, fh)
	if e != 0 {
		t.Fatalf("read .stats: %s", e)
	}
	if n == 4<<10 {
		if n2, e := v.Read(ctx, fe.Inode, buf[n:], uint64(n), fh); e != 0 {
			t.Fatalf("read .stats 2: %s", e)
		} else {
			n += n2
		}
	}
	if !strings.Contains(string(buf[:n]), "fuse_open_handlers") {
		t.Fatalf(".stats should contains `memory`, but got %s", string(buf[:n]))
	}
	if e = v.Truncate(ctx, fe.Inode, 0, 1, &meta.Attr{}); e != syscall.EPERM {
		t.Fatalf("truncate .config: %s", e)
	}

	// accesslog
	fe, e = v.Lookup(ctx, 1, ".accesslog")
	if e != 0 {
		t.Fatalf("lookup .accesslog: %s", e)
	}
	fe, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDONLY)
	if e != 0 {
		t.Fatalf("open .accesslog: %s", e)
	}
	if n, e = v.Read(ctx, fe.Inode, buf, 0, fh); e != 0 {
		t.Fatalf("read .accesslog: %s", e)
	} else if !strings.Contains(string(buf[:n]), "open (9223372032559808513") {
		t.Fatalf("invalid access log: %q", string(buf[:n]))
	}
	_ = v.Flush(ctx, fe.Inode, fh, 0)
	v.Release(ctx, fe.Inode, fh)

	// control messages
	fe, e = v.Lookup(ctx, 1, ".control")
	if e != 0 {
		t.Fatalf("lookup .control: %s", e)
	}
	fe, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDWR)
	if e != 0 {
		t.Fatalf("open .stats: %s", e)
	}
	readControl := func(resp []byte, off *uint64) (int, syscall.Errno) {
		for {
			if n, errno := v.Read(ctx, fe.Inode, resp, *off, fh); n == 0 {
				time.Sleep(time.Millisecond * 200)
			} else if n%17 == 0 {
				*off += uint64(n)
				continue
			} else if n%17 == 1 {
				*off += uint64(n / 17 * 17)
				resp[0] = resp[n-1]
				return 1, errno
			} else {
				return n, errno
			}
		}
	}

	readData := func(resp []byte, fileOff *uint64) ([]byte, syscall.Errno) {
		var off uint64
		for {
			n, errno := v.Read(ctx, fe.Inode, resp, *fileOff, fh)
			if errno != 0 {
				return nil, errno
			}
			if n == 0 {
				time.Sleep(time.Millisecond * 200)
				continue
			}
			*fileOff += uint64(n)
			for {
				if n == 1 {
					return nil, syscall.Errno(resp[off])
				} else if off+17 <= uint64(n) && resp[off] == meta.CPROGRESS {
					off += 17
				} else if off+5 < uint64(n) && resp[off] == meta.CDATA {
					size := binary.BigEndian.Uint32(resp[off+1 : off+5])
					if off+5+uint64(size) > uint64(n) {
						logger.Errorf("Bad response off %d n %d: %v", off, n, resp)
						return nil, syscall.EIO
					}
					return resp[off+5 : off+5+uint64(size)], 0
				} else {
					logger.Errorf("Bad response off %d n %d: %v", off, n, resp)
					return nil, syscall.EIO
				}
			}
		}
	}

	// rmr
	buf = make([]byte, 4+4+8+1+4)
	w := utils.FromBuffer(buf)
	w.Put32(meta.Rmr)
	w.Put32(13)
	w.Put64(1)
	w.Put8(4)
	w.Put([]byte("file"))
	if e := v.Write(ctx, fe.Inode, w.Bytes(), 0, fh); e != 0 {
		t.Fatalf("write info: %s", e)
	}
	var off uint64 = uint64(len(buf))
	resp := make([]byte, 1024*10)
	if n, e := readControl(resp, &off); e != 0 || n != 1 {
		t.Fatalf("read result: %s %d", e, n)
	} else if resp[0] != byte(syscall.ENOENT) {
		t.Fatalf("rmr result: %s", string(buf[:n]))
	} else {
		off += uint64(n)
	}
	// legacy info
	buf = make([]byte, 4+4+8)
	w = utils.FromBuffer(buf)
	w.Put32(meta.LegacyInfo)
	w.Put32(8)
	w.Put64(1)
	if e := v.Write(ctx, fe.Inode, w.Bytes(), off, fh); e != 0 {
		t.Fatalf("write legacy info: %s", e)
	}
	off += uint64(len(buf))
	buf = make([]byte, 1024*10)
	if n, e = readControl(buf, &off); e != 0 {
		t.Fatalf("read result: %s %d", e, n)
	} else if !strings.Contains(string(buf[:n]), "dirs:") {
		t.Fatalf("legacy info result: %s", string(buf[:n]))
	} else {
		off += uint64(n)
	}
	// info v2
	buf = make([]byte, 4+4+8)
	w = utils.FromBuffer(buf)
	w.Put32(meta.InfoV2)
	w.Put32(8)
	w.Put64(1)
	if e := v.Write(ctx, fe.Inode, w.Bytes(), off, fh); e != 0 {
		t.Fatalf("write info v2: %s", e)
	}
	off += uint64(len(buf))
	buf = make([]byte, 1024*10)
	data, e := readData(buf, &off)
	if e != 0 {
		t.Fatalf("read progress bar: %s %d", e, n)
	}

	var infoResp InfoResponse
	if e := json.Unmarshal(data, &infoResp); e != nil {
		t.Fatalf("unmarshal info v2: %s", e)
	}
	if infoResp.Failed && infoResp.Reason != "" {
		t.Fatalf("info v2 result: %s", infoResp.Reason)
	}

	// fill
	buf = make([]byte, 4+4+8+1+1+2+1)
	w = utils.FromBuffer(buf)
	w.Put32(meta.FillCache)
	w.Put32(13)
	w.Put64(1)
	w.Put8(1)
	w.Put([]byte("/"))
	w.Put16(2)
	w.Put8(0)
	if e := v.Write(ctx, fe.Inode, w.Bytes()[:10], 0, fh); e != 0 {
		t.Fatalf("write fill 1: %s", e)
	}
	if e := v.Write(ctx, fe.Inode, w.Bytes()[10:], 0, fh); e != 0 {
		t.Fatalf("write fill 2: %s", e)
	}
	off += uint64(len(buf))
	resp = make([]byte, 1024*10)

	data, _ = json.Marshal(CacheResponse{})
	expectSize := 1 + 4 + len(data)
	if n, e = readControl(resp, &off); e != 0 || n != expectSize {
		t.Fatalf("read result: %s %d %d", e, n, expectSize)
	}

	off += uint64(n)

	// invalid msg
	buf = make([]byte, 4+4+2)
	w = utils.FromBuffer(buf)
	w.Put32(meta.Rmr)
	w.Put32(0)
	if e := v.Write(ctx, fe.Inode, buf, off, fh); e != 0 {
		t.Fatalf("write info: %s", e)
	}
	off += uint64(len(buf))
	resp = make([]byte, 1024)
	if n, e := v.Read(ctx, fe.Inode, resp, off, fh); e != 0 || n != 1 {
		t.Fatalf("read result: %s %d", e, n)
	} else if resp[0] != uint8(syscall.EIO) {
		t.Fatalf("result: %s", string(resp[:n]))
	}
}

func TestReaddirCache(t *testing.T) {
	engines := map[string]string{
		"kv":    "",
		"db":    "sqlite3://",
		"redis": "redis://127.0.0.1:6379/2",
	}
	for typ, metaUri := range engines {
		testReaddirCache(t, metaUri, typ, 20)
		testReaddirCache(t, metaUri, typ, 4096)
	}
}

func testReaddirCache(t *testing.T, metaUri string, typ string, batchNum int) {
	v, _ := createTestVFS(nil, metaUri)
	ctx := NewLogContext(meta.Background())

	old := meta.DirBatchNum
	meta.DirBatchNum[typ] = batchNum
	defer func() {
		meta.DirBatchNum = old
	}()

	entry, st := v.Mkdir(ctx, 1, "testdir", 0777, 022)
	if st != 0 {
		t.Fatalf("mkdir testdir: %s", st)
	}
	parent := entry.Inode
	for i := 0; i <= 100; i++ {
		_, _ = v.Mkdir(ctx, parent, fmt.Sprintf("d%03d", i), 0777, 022)
	}

	defer func() {
		for i := 0; i <= 120; i++ {
			_ = v.Rmdir(ctx, parent, fmt.Sprintf("d%03d", i))
		}
		_ = v.Rmdir(ctx, 1, "testdir")
	}()

	fh, _ := v.Opendir(ctx, parent, 0)
	defer v.Releasedir(ctx, parent, fh)
	initNum, num := 2, 20
	var files = make(map[string]bool)
	// read first 20
	entries, _, _ := v.Readdir(ctx, parent, 20, initNum, fh, true)
	for _, e := range entries[:num] {
		files[string(e.Name)] = true
	}

	off := num + initNum
	{
		entries, _, _ = v.Readdir(ctx, parent, 20, off, fh, true) // read next 20
		v.UpdateReaddirOffset(ctx, parent, fh, off+1)             // but readdir buffer is too full to return all entries
		name := fmt.Sprintf("d%03d", off+2)
		_ = v.Rmdir(ctx, parent, name)
		entries, _, _ = v.Readdir(ctx, parent, 20, off, fh, true) // should only get 19 entries
		for _, e := range entries {
			if string(e.Name) == name {
				t.Fatalf("dir %s should be deleted", name)
			}
		}
	}
	v.UpdateReaddirOffset(ctx, parent, fh, off)
	for i := 0; i < 100; i += 10 {
		name := fmt.Sprintf("d%03d", i)
		_ = v.Rmdir(ctx, parent, name)
		delete(files, name)
	}
	for i := 100; i < 110; i++ {
		_, _ = v.Mkdir(ctx, parent, fmt.Sprintf("d%03d", i), 0777, 022)
		_ = v.Rename(ctx, parent, fmt.Sprintf("d%03d", i), parent, fmt.Sprintf("d%03d", i+10), 0)
		delete(files, fmt.Sprintf("d%03d", i))
	}
	for {
		entries, _, _ := v.Readdir(ctx, parent, 20, off, fh, true)
		if len(entries) == 0 {
			break
		}
		if len(entries) > 20 {
			entries = entries[:20]
		}
		for _, e := range entries {
			if e.Inode > 0 {
				files[string(e.Name)] = true
			} else {
				t.Logf("invalid entry %s", e.Name)
			}
		}
		off += len(entries)
		v.UpdateReaddirOffset(ctx, parent, fh, off)
	}
	for i := 0; i < 100; i += 10 {
		name := fmt.Sprintf("d%03d", i)
		if _, ok := files[name]; ok {
			t.Fatalf("dir %s should be deleted", name)
		}
	}
	for i := 100; i < 110; i++ {
		name := fmt.Sprintf("d%03d", i)
		if _, ok := files[name]; ok {
			t.Fatalf("dir %s should be deleted", name)
		}
	}
	for i := 110; i < 120; i++ {
		name := fmt.Sprintf("d%03d", i)
		if _, ok := files[name]; !ok {
			t.Fatalf("dir %s should be added", name)
		}
	}
}

func TestVFSReadDirSort(t *testing.T) {
	for _, metaUri := range []string{"", "sqlite3://", "redis://127.0.0.1:6379/2"} {
		testVFSReadDirSort(t, metaUri)
	}
}

func testVFSReadDirSort(t *testing.T, metaUri string) {
	v, _ := createTestVFS(func(metaConfig *meta.Config) {
		metaConfig.SortDir = true
	}, metaUri)
	ctx := NewLogContext(meta.Background())
	entry, st := v.Mkdir(ctx, 1, "testdir", 0777, 022)
	if st != 0 {
		t.Fatalf("mkdir testdir: %s", st)
	}
	parent := entry.Inode
	for i := 0; i < 100; i++ {
		_, _ = v.Mkdir(ctx, parent, fmt.Sprintf("d%d", i), 0777, 022)
	}
	defer func() {
		for i := 0; i < 100; i++ {
			_ = v.Rmdir(ctx, parent, fmt.Sprintf("d%d", i))
		}
		_ = v.Rmdir(ctx, 1, "testdir")
	}()
	fh, _ := v.Opendir(ctx, parent, 0)
	entries1, _, _ := v.Readdir(ctx, parent, 60, 10, fh, true)
	sorted := slices.IsSortedFunc(entries1, func(i, j *meta.Entry) int {
		return strings.Compare(string(i.Name), string(j.Name))
	})
	if !sorted {
		t.Fatalf("read dir result should sorted")
	}
	v.Releasedir(ctx, parent, fh)

	fh2, _ := v.Opendir(ctx, parent, 0)
	entries2, _, _ := v.Readdir(ctx, parent, 60, 10, fh, true)
	for i := 0; i < len(entries1); i++ {
		if string(entries1[i].Name) != string(entries2[i].Name) {
			t.Fatalf("read dir result should be same")
		}
	}
	v.Releasedir(ctx, parent, fh2)
}

func testReaddirBatch(t *testing.T, metaUri string, typ string, batchNum int) {
	n, extra := 5, 40

	v, _ := createTestVFS(nil, metaUri)
	ctx := NewLogContext(meta.Background())

	old := meta.DirBatchNum
	meta.DirBatchNum[typ] = batchNum
	defer func() {
		meta.DirBatchNum = old
	}()

	entry, st := v.Mkdir(ctx, 1, "testdir", 0777, 022)
	if st != 0 {
		t.Fatalf("mkdir testdir: %s", st)
	}

	parent := entry.Inode
	for i := 0; i < n*batchNum+extra; i++ {
		_, _ = v.Mkdir(ctx, parent, fmt.Sprintf("d%d", i), 0777, 022)
	}
	defer func() {
		for i := 0; i < n*batchNum+extra; i++ {
			_ = v.Rmdir(ctx, parent, fmt.Sprintf("d%d", i))
		}
		v.Rmdir(ctx, 1, "testdir")
	}()

	fh, _ := v.Opendir(ctx, parent, 0)
	defer v.Releasedir(ctx, parent, fh)
	entries1, _, _ := v.Readdir(ctx, parent, 0, 0, fh, true)
	require.NotNil(t, entries1)
	require.Equal(t, 2+batchNum, len(entries1)) // init entries: "." and ".."

	entries2, _, _ := v.Readdir(ctx, parent, 0, 2, fh, true)
	require.NotNil(t, entries2)
	require.Equal(t, batchNum, len(entries2))

	entries3, _, _ := v.Readdir(ctx, parent, 0, 2+batchNum, fh, true)
	require.NotNil(t, entries3)
	require.Equal(t, batchNum, len(entries3))

	// reach the end
	entries4, _, _ := v.Readdir(ctx, parent, 0, n*batchNum+extra+2, fh, true)
	require.NotNil(t, entries4)
	require.Equal(t, 0, len(entries4))

	// skip-style readdir
	entries5, _, _ := v.Readdir(ctx, parent, 0, n*batchNum+2, fh, true)
	require.NotNil(t, entries5)
	require.Equal(t, extra, len(entries5))

	entries6, _, _ := v.Readdir(ctx, parent, 0, 2, fh, true)
	require.Equal(t, len(entries2), len(entries6))
	for i := 0; i < len(entries2); i++ {
		require.Equal(t, entries2[i].Inode, entries6[i].Inode)
	}

	// dir seak
	entries7, _, _ := v.Readdir(ctx, parent, 0, n*batchNum+2-20, fh, true)
	require.True(t, reflect.DeepEqual(entries5, entries7[20:]))
}

func TestReadDirBatch(t *testing.T) {
	engines := map[string]string{
		"kv":    "",
		"db":    "sqlite3://",
		"redis": "redis://127.0.0.1:6379/2",
	}
	for typ, metaUri := range engines {
		testReaddirBatch(t, metaUri, typ, 100)
		testReaddirBatch(t, metaUri, typ, 4096)
	}
}

func TestReaddir(t *testing.T) {
	engines := map[string]string{
		"kv":    "",
		"db":    "sqlite3://",
		"redis": "redis://127.0.0.1:6379/2",
	}
	for typ, metaUri := range engines {
		batchNum := meta.DirBatchNum[typ]
		extra := rand.Intn(batchNum)
		testReaddir(t, metaUri, 20, 0)
		testReaddir(t, metaUri, 20, 5)
		testReaddir(t, metaUri, 2*batchNum, 0)
		testReaddir(t, metaUri, 2*batchNum, extra)
	}
}

func testReaddir(t *testing.T, metaUri string, dirNum int, offset int) {
	v, _ := createTestVFS(nil, metaUri)
	ctx := NewLogContext(meta.Background())

	entry, st := v.Mkdir(ctx, 1, "testdir", 0777, 022)
	if st != 0 {
		t.Fatalf("mkdir testdir: %s", st)
	}

	parent := entry.Inode
	for i := 0; i < dirNum; i++ {
		_, _ = v.Mkdir(ctx, parent, fmt.Sprintf("d%d", i), 0777, 022)
	}
	defer func() {
		for i := 0; i < dirNum; i++ {
			_ = v.Rmdir(ctx, parent, fmt.Sprintf("d%d", i))
		}
		v.Rmdir(ctx, 1, "testdir")
	}()

	fh, _ := v.Opendir(ctx, parent, 0)
	defer v.Releasedir(ctx, parent, fh)

	readAll := func(ctx Context, parent Ino, fh uint64, off int) []*meta.Entry {
		var entries []*meta.Entry
		for {
			ents, _, st := v.Readdir(ctx, parent, 0, off, fh, true)
			require.Equal(t, st, syscall.Errno(0))
			if len(ents) == 0 {
				break
			}
			off += len(ents)
			entries = append(entries, ents...)
		}
		return entries
	}

	entriesOne := readAll(ctx, parent, fh, offset)
	entriesTwo := readAll(ctx, parent, fh, offset)
	require.True(t, reflect.DeepEqual(entriesOne, entriesTwo))
}
