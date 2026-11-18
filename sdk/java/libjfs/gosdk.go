/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package main

// Functions used by the Go SDK (sdk/go, -tags libjfs). They only take
// integers and pointers, use the errno of errno(), and encode attributes with
// putAttr: all integers are big-endian.

// #include <stdint.h>
// #include <stdlib.h>
import "C"
import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// registries keeps the metrics registry of each file system, under fslock.
var registries = make(map[*fs.FileSystem]*prometheus.Registry)

const attrSize = 72

// putAttr writes inode:8 mode:4 nlink:4 uid:4 gid:4 rdev:4 length:8
// atime:8 atimensec:4 mtime:8 mtimensec:4 ctime:8 ctimensec:4.
func putAttr(out *utils.Buffer, inode meta.Ino, attr *meta.Attr) {
	out.Put64(uint64(inode))
	out.Put32(attr.SMode())
	out.Put32(attr.Nlink)
	out.Put32(attr.Uid)
	out.Put32(attr.Gid)
	out.Put32(attr.Rdev)
	out.Put64(attr.Length)
	out.Put64(uint64(attr.Atime))
	out.Put32(attr.Atimensec)
	out.Put64(uint64(attr.Mtime))
	out.Put32(attr.Mtimensec)
	out.Put64(uint64(attr.Ctime))
	out.Put32(attr.Ctimensec)
}

func fillAttr(buf unsafe.Pointer, inode meta.Ino, attr *meta.Attr) {
	putAttr(utils.FromBuffer(unsafe.Slice((*byte)(buf), attrSize)), inode, attr)
}

// putEntries writes namelen:2 name attr for each entry except "." and "..",
// into memory from malloc.
func putEntries(entries []*meta.Entry, buf *unsafe.Pointer, size *int64) {
	var n int
	for _, e := range entries {
		n += 2 + len(e.Name) + attrSize
	}
	b := make([]byte, n)
	out := utils.FromBuffer(b)
	for _, e := range entries {
		if name := string(e.Name); name == "." || name == ".." {
			continue
		}
		out.Put16(uint16(len(e.Name)))
		out.Put(e.Name)
		putAttr(out, e.Inode, e.Attr)
	}
	*size = int64(out.Offset())
	*buf = C.CBytes(b[:out.Offset()])
}

func openFile(fd int32) *fwrapper {
	filesLock.Lock()
	defer filesLock.Unlock()
	return openFiles[fd]
}

// jfs_set_ids sets the numeric uid and gids of h. The user and group names
// cannot express them, so h keeps them when libjfs rebuilds the identities from
// the names on jfs_init of the same volume, reload of the format and
// jfs_update_uid_grouping.
//
//export jfs_set_ids
func jfs_set_ids(h int64, uid uint32, gids *C.uint32_t, ngids int32) int32 {
	w := F(h)
	if w == nil || ngids < 0 {
		return EINVAL
	}
	ids := make([]uint32, ngids)
	for i, g := range unsafe.Slice(gids, ngids) {
		ids[i] = uint32(g)
	}
	fslock.Lock()
	defer fslock.Unlock()
	w.fixedIds = true
	w.ctx = meta.NewContext(uint32(os.Getpid()), uid, ids)
	return 0
}

//export jfs_statx
func jfs_statx(pid int64, h int64, cpath *C.char, follow int32, buf unsafe.Pointer) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	var fi *fs.FileStat
	var err syscall.Errno
	if follow != 0 {
		fi, err = w.Stat(w.withPid(pid), C.GoString(cpath))
	} else {
		fi, err = w.Lstat(w.withPid(pid), C.GoString(cpath))
	}
	if err != 0 {
		return errno(err)
	}
	fillAttr(buf, fi.Inode(), fi.Sys().(*meta.Attr))
	return 0
}

//export jfs_listdirx
func jfs_listdirx(pid int64, h int64, cpath *C.char, buf *unsafe.Pointer, size *int64) int32 {
	*buf = nil
	*size = 0
	w := F(h)
	if w == nil {
		return EINVAL
	}
	ctx := w.withPid(pid)
	f, err := w.Open(ctx, C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(ctx)
	entries, err := f.ReaddirPlus(ctx, 0)
	if err != 0 {
		return errno(err)
	}
	putEntries(entries, buf, size)
	return 0
}

//export jfs_lchown
func jfs_lchown(pid int64, h int64, cpath *C.char, uid uint32, gid uint32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	ctx := w.withPid(pid)
	f, err := w.Lopen(ctx, C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(ctx)
	return errno(f.Chown(ctx, uid, gid))
}

// jfs_utimens leaves a time unchanged if both its parts are negative.
//
//export jfs_utimens
func jfs_utimens(pid int64, h int64, cpath *C.char, asec, ansec, msec, mnsec int64) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	ctx := w.withPid(pid)
	f, err := w.Open(ctx, C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(ctx)
	return errno(f.Utime2(ctx, asec, ansec, msec, mnsec))
}

// jfs_openx opens a file or a directory with the mode mask of vfs, and fills
// buf with the attributes at open.
//
//export jfs_openx
func jfs_openx(pid int64, h int64, cpath *C.char, mask uint32, buf unsafe.Pointer) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), mask)
	if err != 0 {
		return errno(err)
	}
	fi, _ := f.Stat()
	fillAttr(buf, f.Inode(), fi.Sys().(*meta.Attr))
	return nextFileHandle(f, w)
}

// jfs_fstatx reads the attributes from meta, as fs.File.Stat returns those at
// open.
//
//export jfs_fstatx
func jfs_fstatx(pid int64, fd int32, buf unsafe.Pointer) int32 {
	f := openFile(fd)
	if f == nil {
		return EINVAL
	}
	inode := f.Inode()
	if vfs.IsSpecialNode(inode) {
		fi, _ := f.Stat()
		fillAttr(buf, inode, fi.Sys().(*meta.Attr))
		return 0
	}
	var attr meta.Attr
	if err := f.w.Meta().GetAttr(f.w.withPid(pid), inode, &attr); err != 0 {
		return errno(err)
	}
	fillAttr(buf, inode, &attr)
	return 0
}

// jfs_freaddirx reads the opened directory by inode on each call.
//
//export jfs_freaddirx
func jfs_freaddirx(pid int64, fd int32, buf *unsafe.Pointer, size *int64) int32 {
	*buf = nil
	*size = 0
	f := openFile(fd)
	if f == nil {
		return EINVAL
	}
	var entries []*meta.Entry
	if err := f.w.Meta().Readdir(f.w.withPid(pid), f.Inode(), 1, &entries); err != 0 {
		return errno(err)
	}
	putEntries(entries, buf, size)
	return 0
}

//export jfs_fchmod
func jfs_fchmod(pid int64, fd int32, mode uint16) int32 {
	f := openFile(fd)
	if f == nil {
		return EINVAL
	}
	return errno(f.Chmod(f.w.withPid(pid), mode))
}

//export jfs_fchown
func jfs_fchown(pid int64, fd int32, uid uint32, gid uint32) int32 {
	f := openFile(fd)
	if f == nil {
		return EINVAL
	}
	return errno(f.Chown(f.w.withPid(pid), uid, gid))
}

// jfs_metrics returns the metrics of h in the Prometheus text format.
//
//export jfs_metrics
func jfs_metrics(h int64, buf *unsafe.Pointer, size *int64) int32 {
	*buf = nil
	*size = 0
	w := F(h)
	if w == nil {
		return EINVAL
	}
	fslock.Lock()
	registry := registries[w.FileSystem]
	fslock.Unlock()
	if registry == nil {
		return 0
	}
	mfs, err := registry.Gather()
	if err != nil {
		logger.Warnf("gather metrics: %s", err)
	}
	var b bytes.Buffer
	for _, mf := range mfs {
		if _, err := expfmt.MetricFamilyToText(&b, mf); err != nil {
			logger.Errorf("encode metrics: %s", err)
			return EIO
		}
	}
	*size = int64(b.Len())
	*buf = C.CBytes(b.Bytes())
	return 0
}
