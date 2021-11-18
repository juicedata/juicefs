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

package vfs

import (
	"log"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

func createTestVFS() *VFS {
	mp := "/jfs"
	metaConf := &meta.Config{
		Retries:    10,
		Strict:     true,
		MountPoint: mp,
	}
	m := meta.NewClient("memkv://", metaConf)
	format := meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "mem",
		BlockSize: 4096,
	}
	err := m.Init(format, true)
	if err != nil {
		log.Fatalf("setting: %s", err)
	}
	conf := &Config{
		Meta:       metaConf,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			Compress:   format.Compression,
			MaxUpload:  2,
			BufferSize: 30 << 20,
			CacheSize:  10,
			CacheDir:   "memory",
		},
	}

	blob, _ := object.CreateStorage("mem", "", "", "")
	store := chunk.NewCachedStore(blob, *conf.Chunk)
	return NewVFS(conf, m, store)
}

func TestVFSBasic(t *testing.T) {
	v := createTestVFS()
	ctx := NewLogContext(meta.Background)

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
	} else if e := v.Release(ctx, fe.Inode, fh); e != 0 {
		t.Fatalf("release f3: %s", e)
	}

	// symlink
	if fe, e := v.Symlink(ctx, "f2", 1, "sym"); e != 0 {
		t.Fatalf("symlink sym -> f2: %s", e)
	} else if target, e := v.Readlink(ctx, fe.Inode); e != 0 || string(target) != "f2" {
		t.Fatalf("readlink sym: %s %s", e, string(target))
	}
}

func TestVFSIO(t *testing.T) {
	v := createTestVFS()
	ctx := NewLogContext(meta.Background)
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
	if e = v.Truncate(ctx, fe.Inode, (100<<20)+2, 1, &attr); e != 0 {
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
	_ = v.Release(ctx, fe.Inode, fh)
	// TODO: copy file range
}

func TestVFSXattrs(t *testing.T) {
	v := createTestVFS()
	ctx := NewLogContext(meta.Background)
	fe, e := v.Mkdir(ctx, 1, "xattrs", 0755, 0)
	if e != 0 {
		t.Fatalf("mkdir xattrs: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "test", 0); e != syscall.ENOATTR {
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
	if e := v.RemoveXattr(ctx, fe.Inode, "test"); e != 0 {
		t.Fatalf("removexattr test: %s", e)
	}
	if _, e := v.GetXattr(ctx, fe.Inode, "test", 0); e != syscall.ENOATTR {
		t.Fatalf("getxattr not existed: %s", e)
	}
	if v, e := v.ListXattr(ctx, fe.Inode, 100); e != 0 || string(v) != "" {
		t.Fatalf("listxattr: %s %q", e, string(v))
	}
}

func TestVFSLocks(t *testing.T) {

}

func TestInternalFile(t *testing.T) {
	v := createTestVFS()
	ctx := NewLogContext(meta.Background)
	// list internal files
	fh, _ := v.Opendir(ctx, 1)
	entries, e := v.Readdir(ctx, 1, 1024, 0, fh, true)
	if e != 0 {
		t.Fatalf("readdir 1: %s", e)
	}
	internalFiles := make(map[string]bool)
	for _, e := range entries {
		if IsSpecialName(string(e.Name)) && e.Attr.Typ == meta.TypeFile {
			internalFiles[string(e.Name)] = true
		}
	}
	if len(internalFiles) != 4 {
		t.Fatalf("there should be 4 internal files but got %d", len(internalFiles))
	}
	v.Releasedir(ctx, 1, fh)

	// .stats
	fe, e := v.Lookup(ctx, 1, ".stats")
	if e != 0 {
		t.Fatalf("lookup .stats: %s", e)
	}
	fe, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDONLY)
	if e != 0 {
		t.Fatalf("open .stats: %s", e)
	}
	defer v.Release(ctx, fe.Inode, fh)
	defer v.Flush(ctx, fe.Inode, fh, 0)
	buf := make([]byte, 128<<10)
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

	// control messages
	fe, e = v.Lookup(ctx, 1, ".control")
	if e != 0 {
		t.Fatalf("lookup .control: %s", e)
	}
	fe, fh, e = v.Open(ctx, fe.Inode, syscall.O_RDWR)
	if e != 0 {
		t.Fatalf("open .stats: %s", e)
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
	if n, e := v.Read(ctx, fe.Inode, resp, off, fh); e != 0 || n != 1 {
		t.Fatalf("read result: %s %d", e, n)
	} else if resp[0] != byte(syscall.ENOENT) {
		t.Fatalf("rmr result: %s", string(buf[:n]))
	} else {
		off += uint64(n)
	}
	// info
	buf = make([]byte, 4+4+8)
	w = utils.FromBuffer(buf)
	w.Put32(meta.Info)
	w.Put32(8)
	w.Put64(1)
	if e := v.Write(ctx, fe.Inode, w.Bytes(), off, fh); e != 0 {
		t.Fatalf("write info: %s", e)
	}
	off += uint64(len(buf))
	buf = make([]byte, 1024*10)
	if n, e := v.Read(ctx, fe.Inode, buf, off, fh); e != 0 || n == 0 {
		t.Fatalf("read result: %s", e)
	} else if !strings.Contains(string(buf[:n]), "dirs:") {
		t.Fatalf("info result: %s", string(buf[:n]))
	} else {
		off += uint64(n)
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
	if e := v.Write(ctx, fe.Inode, w.Bytes(), 0, fh); e != 0 {
		t.Fatalf("write info: %s", e)
	}
	off += uint64(len(buf))
	resp = make([]byte, 1024*10)
	if n, e := v.Read(ctx, fe.Inode, resp, off, fh); e != 0 || n != 1 {
		t.Fatalf("read result: %s", e)
	} else if resp[0] != 0 {
		t.Fatalf("fill result: %s", string(buf[:n]))
	}
}
