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
	"encoding/json"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

type Ino = meta.Ino
type Attr = meta.Attr
type Context = LogContext

const (
	rootID      = 1
	maxName     = meta.MaxName
	maxSymlink  = 4096
	maxFileSize = meta.ChunkSize << 31
)

type Config struct {
	Meta            *meta.Config
	Format          *meta.Format
	Chunk           *chunk.Config
	Version         string
	AttrTimeout     time.Duration
	DirEntryTimeout time.Duration
	EntryTimeout    time.Duration
	BackupMeta      time.Duration
	FastResolve     bool   `json:",omitempty"`
	AccessLog       string `json:",omitempty"`
	HideInternal    bool
}

var (
	readSizeHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fuse_read_size_bytes",
		Help:    "size of read distributions.",
		Buckets: prometheus.LinearBuckets(4096, 4096, 32),
	})
	writtenSizeHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fuse_written_size_bytes",
		Help:    "size of write distributions.",
		Buckets: prometheus.LinearBuckets(4096, 4096, 32),
	})
)

func (v *VFS) Lookup(ctx Context, parent Ino, name string) (entry *meta.Entry, err syscall.Errno) {
	var inode Ino
	var attr = &Attr{}
	if parent == rootID || name == ".control" {
		n := getInternalNodeByName(name)
		if n != nil {
			entry = &meta.Entry{Inode: n.inode, Attr: n.attr}
			return
		}
	}
	if IsSpecialNode(parent) && name == "." {
		if n := getInternalNode(parent); n != nil {
			entry = &meta.Entry{Inode: n.inode, Attr: n.attr}
			return
		}
	}
	defer func() {
		logit(ctx, "lookup (%d,%s): %s%s", parent, name, strerr(err), (*Entry)(entry))
	}()
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Lookup(ctx, parent, name, &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func (v *VFS) GetAttr(ctx Context, ino Ino, opened uint8) (entry *meta.Entry, err syscall.Errno) {
	if IsSpecialNode(ino) && getInternalNode(ino) != nil {
		n := getInternalNode(ino)
		entry = &meta.Entry{Inode: n.inode, Attr: n.attr}
		return
	}
	defer func() { logit(ctx, "getattr (%d): %s%s", ino, strerr(err), (*Entry)(entry)) }()
	var attr = &Attr{}
	err = v.Meta.GetAttr(ctx, ino, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: ino, Attr: attr}
	}
	return
}

func get_filetype(mode uint16) uint8 {
	switch mode & (syscall.S_IFMT & 0xffff) {
	case syscall.S_IFIFO:
		return meta.TypeFIFO
	case syscall.S_IFSOCK:
		return meta.TypeSocket
	case syscall.S_IFLNK:
		return meta.TypeSymlink
	case syscall.S_IFREG:
		return meta.TypeFile
	case syscall.S_IFBLK:
		return meta.TypeBlockDev
	case syscall.S_IFDIR:
		return meta.TypeDirectory
	case syscall.S_IFCHR:
		return meta.TypeCharDev
	}
	return meta.TypeFile
}

func (v *VFS) Mknod(ctx Context, parent Ino, name string, mode uint16, cumask uint16, rdev uint32) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "mknod (%d,%s,%s:0%04o,0x%08X): %s%s", parent, name, smode(mode), mode, rdev, strerr(err), (*Entry)(entry))
	}()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	_type := get_filetype(mode)
	if _type == 0 {
		err = syscall.EPERM
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = v.Meta.Mknod(ctx, parent, name, _type, mode&07777, cumask, rdev, "", &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func (v *VFS) Unlink(ctx Context, parent Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "unlink (%d,%s): %s", parent, name, strerr(err)) }()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EPERM
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Unlink(ctx, parent, name)
	return
}

func (v *VFS) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "mkdir (%d,%s,%s:0%04o): %s%s", parent, name, smode(mode), mode, strerr(err), (*Entry)(entry))
	}()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = v.Meta.Mkdir(ctx, parent, name, mode, cumask, 0, &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func (v *VFS) Rmdir(ctx Context, parent Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "rmdir (%d,%s): %s", parent, name, strerr(err)) }()
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Rmdir(ctx, parent, name)
	return
}

func (v *VFS) Symlink(ctx Context, path string, parent Ino, name string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "symlink (%d,%s,%s): %s%s", parent, name, path, strerr(err), (*Entry)(entry))
	}()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if len(name) > maxName || len(path) >= maxSymlink {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = v.Meta.Symlink(ctx, parent, name, path, &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func (v *VFS) Readlink(ctx Context, ino Ino) (path []byte, err syscall.Errno) {
	defer func() { logit(ctx, "readlink (%d): %s (%s)", ino, strerr(err), string(path)) }()
	err = v.Meta.ReadLink(ctx, ino, &path)
	return
}

func (v *VFS) Rename(ctx Context, parent Ino, name string, newparent Ino, newname string, flags uint32) (err syscall.Errno) {
	defer func() {
		logit(ctx, "rename (%d,%s,%d,%s,%d): %s", parent, name, newparent, newname, flags, strerr(err))
	}()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EPERM
		return
	}
	if newparent == rootID && IsSpecialName(newname) {
		err = syscall.EPERM
		return
	}
	if len(name) > maxName || len(newname) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	err = v.Meta.Rename(ctx, parent, name, newparent, newname, flags, nil, nil)
	return
}

func (v *VFS) Link(ctx Context, ino Ino, newparent Ino, newname string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "link (%d,%d,%s): %s%s", ino, newparent, newname, strerr(err), (*Entry)(entry))
	}()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if newparent == rootID && IsSpecialName(newname) {
		err = syscall.EPERM
		return
	}
	if len(newname) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var attr = &Attr{}
	err = v.Meta.Link(ctx, ino, newparent, newname, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: ino, Attr: attr}
	}
	return
}

func (v *VFS) Opendir(ctx Context, ino Ino) (fh uint64, err syscall.Errno) {
	defer func() { logit(ctx, "opendir (%d): %s [fh:%d]", ino, strerr(err), fh) }()
	fh = v.newHandle(ino).fh
	return
}

func (v *VFS) UpdateLength(inode Ino, attr *meta.Attr) {
	if attr.Full && attr.Typ == meta.TypeFile {
		length := v.writer.GetLength(inode)
		if length > attr.Length {
			attr.Length = length
		}
		v.reader.Truncate(inode, attr.Length)
	}
}

func (v *VFS) Readdir(ctx Context, ino Ino, size uint32, off int, fh uint64, plus bool) (entries []*meta.Entry, readAt time.Time, err syscall.Errno) {
	defer func() { logit(ctx, "readdir (%d,%d,%d): %s (%d)", ino, size, off, strerr(err), len(entries)) }()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.Lock()
	defer h.Unlock()

	if h.children == nil || off == 0 {
		var inodes []*meta.Entry
		h.readAt = time.Now()
		err = v.Meta.Readdir(ctx, ino, 1, &inodes)
		if err == syscall.EACCES {
			err = v.Meta.Readdir(ctx, ino, 0, &inodes)
		}
		if err != 0 {
			return
		}
		h.children = inodes
		if ino == rootID && !v.Conf.HideInternal {
			// add internal nodes
			for _, node := range internalNodes[1:] {
				h.children = append(h.children, &meta.Entry{
					Inode: node.inode,
					Name:  []byte(node.name),
					Attr:  node.attr,
				})
			}
		}
	}
	if off < len(h.children) {
		entries = h.children[off:]
	}
	readAt = h.readAt
	return
}

func (v *VFS) Releasedir(ctx Context, ino Ino, fh uint64) int {
	h := v.findHandle(ino, fh)
	if h == nil {
		return 0
	}
	v.ReleaseHandler(ino, fh)
	logit(ctx, "releasedir (%d): OK", ino)
	return 0
}

func (v *VFS) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	defer func() {
		logit(ctx, "create (%d,%s,%s:0%04o): %s%s [fh:%d]", parent, name, smode(mode), mode, strerr(err), (*Entry)(entry), fh)
	}()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = v.Meta.Create(ctx, parent, name, mode&07777, cumask, flags, &inode, attr)
	if runtime.GOOS == "darwin" && err == syscall.ENOENT {
		err = syscall.EACCES
	}
	if err == 0 {
		v.UpdateLength(inode, attr)
		fh = v.newFileHandle(inode, attr.Length, flags)
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func (v *VFS) Open(ctx Context, ino Ino, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	defer func() {
		if entry != nil {
			logit(ctx, "open (%d): %s [fh:%d]", ino, strerr(err), fh)
		} else {
			logit(ctx, "open (%d): %s", ino, strerr(err))
		}
	}()
	var attr = &Attr{}
	if IsSpecialNode(ino) {
		if ino != controlInode && (flags&O_ACCMODE) != syscall.O_RDONLY {
			err = syscall.EACCES
			return
		}
		h := v.newHandle(ino)
		fh = h.fh
		n := getInternalNode(ino)
		if n == nil {
			return
		}
		entry = &meta.Entry{Inode: ino, Attr: n.attr}
		switch ino {
		case logInode:
			openAccessLog(fh)
		case statsInode:
			h.data = collectMetrics(v.registry)
		case configInode:
			v.Conf.Format.RemoveSecret()
			h.data, _ = json.MarshalIndent(v.Conf, "", " ")
			entry.Attr.Length = uint64(len(h.data))
		}
		return
	}

	err = v.Meta.Open(ctx, ino, flags, attr)
	if err == 0 {
		v.UpdateLength(ino, attr)
		fh = v.newFileHandle(ino, attr.Length, flags)
		entry = &meta.Entry{Inode: ino, Attr: attr}
	}
	return
}

func (v *VFS) Truncate(ctx Context, ino Ino, size int64, opened uint8, attr *Attr) (err syscall.Errno) {
	// defer func() { logit(ctx, "truncate (%d,%d): %s", ino, size, strerr(err)) }()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if size < 0 {
		err = syscall.EINVAL
		return
	}
	if size >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	hs := v.findAllHandles(ino)
	for _, h := range hs {
		if !h.Wlock(ctx) {
			err = syscall.EINTR
			return
		}
		defer func(h *handle) { h.Wunlock() }(h)
	}
	_ = v.writer.Flush(ctx, ino)
	err = v.Meta.Truncate(ctx, ino, 0, uint64(size), attr)
	if err == 0 {
		v.writer.Truncate(ino, uint64(size))
		v.reader.Truncate(ino, uint64(size))
		v.invalidateLength(ino)
	}
	return 0
}

func (v *VFS) ReleaseHandler(ino Ino, fh uint64) {
	v.releaseFileHandle(ino, fh)
}

func (v *VFS) Release(ctx Context, ino Ino, fh uint64) {
	var err syscall.Errno
	defer func() { logit(ctx, "release (%d,%d): %s", ino, fh, strerr(err)) }()
	if IsSpecialNode(ino) {
		if ino == logInode {
			closeAccessLog(fh)
		}
		v.releaseHandle(ino, fh)
		return
	}
	if fh > 0 {
		f := v.findHandle(ino, fh)
		if f != nil {
			f.Lock()
			for (f.writing | f.writers | f.readers) != 0 {
				if f.cond.WaitWithTimeout(time.Second) && ctx.Canceled() {
					f.Unlock()
					logger.Warnf("write lock %d interrupted", f.inode)
					err = syscall.EINTR
					return
				}
			}
			locks := f.locks
			owner := f.flockOwner
			f.Unlock()
			if f.writer != nil {
				_ = f.writer.Flush(ctx)
				v.invalidateLength(ino)
			}
			if locks&1 != 0 {
				_ = v.Meta.Flock(ctx, ino, owner, F_UNLCK, false)
			}
		}
		_ = v.Meta.Close(ctx, ino)
		go v.releaseFileHandle(ino, fh) // after writes it waits for data sync, so do it after everything
	}
}

func (v *VFS) Read(ctx Context, ino Ino, buf []byte, off uint64, fh uint64) (n int, err syscall.Errno) {
	size := uint32(len(buf))
	if IsSpecialNode(ino) {
		if ino == logInode {
			n = readAccessLog(fh, buf)
		} else {
			if ino == controlInode && runtime.GOOS == "darwin" {
				fh = v.getControlHandle(ctx.Pid())
			}
			h := v.findHandle(ino, fh)
			if h == nil {
				err = syscall.EBADF
				return
			}
			data := h.data
			if off < h.off {
				data = nil
			} else {
				off -= h.off
			}
			if int(off) < len(data) {
				data = data[off:]
				if int(size) < len(data) {
					data = data[:size]
				}
				n = copy(buf, data)
			}
			if len(h.data) > 2<<20 {
				// drop first part to avoid OOM
				h.off += 1 << 20
				h.data = h.data[1<<20:]
			}
			logit(ctx, "read (%d,%d,%d,%d): %s (%d)", ino, size, off, fh, strerr(err), n)
		}
		return
	}

	defer func() {
		readSizeHistogram.Observe(float64(n))
		logit(ctx, "read (%d,%d,%d): %s (%d)", ino, size, off, strerr(err), n)
	}()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if off >= maxFileSize || off+uint64(size) >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	if h.reader == nil {
		err = syscall.EACCES
		return
	}
	if !h.Rlock(ctx) {
		err = syscall.EINTR
		return
	}
	defer h.Runlock()

	_ = v.writer.Flush(ctx, ino)
	n, err = h.reader.Read(ctx, off, buf)
	for err == syscall.EAGAIN {
		n, err = h.reader.Read(ctx, off, buf)
	}
	if err == syscall.ENOENT {
		err = syscall.EBADF
	}
	h.removeOp(ctx)
	return
}

func (v *VFS) Write(ctx Context, ino Ino, buf []byte, off, fh uint64) (err syscall.Errno) {
	size := uint64(len(buf))
	if ino == controlInode && runtime.GOOS == "darwin" {
		fh = v.getControlHandle(ctx.Pid())
	}
	defer func() { logit(ctx, "write (%d,%d,%d,%d): %s", ino, size, off, fh, strerr(err)) }()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if off >= maxFileSize || off+size >= maxFileSize {
		err = syscall.EFBIG
		return
	}

	if ino == controlInode {
		h.pending = append(h.pending, buf...)
		rb := utils.ReadBuffer(h.pending)
		cmd := rb.Get32()
		size := int(rb.Get32())
		if rb.Left() < size {
			logger.Debugf("message not complete: %d %d > %d", cmd, size, rb.Left())
			return
		}
		h.data = append(h.data, h.pending...)
		h.pending = h.pending[:0]
		if rb.Left() == size {
			h.bctx = meta.NewContext(ctx.Pid(), ctx.Uid(), ctx.Gids())
			go v.handleInternalMsg(h.bctx, cmd, rb, &h.data)
		} else {
			logger.Warnf("broken message: %d %d < %d", cmd, size, rb.Left())
			h.data = append(h.data, uint8(syscall.EIO&0xff))
		}
		return
	}

	if h.writer == nil {
		err = syscall.EACCES
		return
	}

	if !h.Wlock(ctx) {
		err = syscall.EINTR
		return
	}
	defer h.Wunlock()

	err = h.writer.Write(ctx, off, buf)
	if err == syscall.ENOENT || err == syscall.EPERM || err == syscall.EINVAL {
		err = syscall.EBADF
	}
	h.removeOp(ctx)

	if err == 0 {
		writtenSizeHistogram.Observe(float64(len(buf)))
		v.reader.Truncate(ino, v.writer.GetLength(ino))
	}
	return
}

func (v *VFS) Fallocate(ctx Context, ino Ino, mode uint8, off, length int64, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fallocate (%d,%d,%d,%d): %s", ino, mode, off, length, strerr(err)) }()
	if off < 0 || length <= 0 {
		err = syscall.EINVAL
		return
	}
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if off >= maxFileSize || off+length >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	if h.writer == nil {
		err = syscall.EACCES
		return
	}
	if !h.Wlock(ctx) {
		err = syscall.EINTR
		return
	}
	defer h.Wunlock()
	defer h.removeOp(ctx)

	err = v.Meta.Fallocate(ctx, ino, mode, uint64(off), uint64(length))
	return
}

func (v *VFS) CopyFileRange(ctx Context, nodeIn Ino, fhIn, offIn uint64, nodeOut Ino, fhOut, offOut, size uint64, flags uint32) (copied uint64, err syscall.Errno) {
	defer func() {
		logit(ctx, "copy_file_range (%d,%d,%d,%d,%d,%d): %s", nodeIn, offIn, nodeOut, offOut, size, flags, strerr(err))
	}()
	if IsSpecialNode(nodeIn) {
		err = syscall.ENOTSUP
		return
	}
	if IsSpecialNode(nodeOut) {
		err = syscall.EPERM
		return
	}
	hi := v.findHandle(nodeIn, fhIn)
	if fhIn == 0 || hi == nil || hi.inode != nodeIn {
		err = syscall.EBADF
		return
	}
	ho := v.findHandle(nodeOut, fhOut)
	if fhOut == 0 || ho == nil || ho.inode != nodeOut {
		err = syscall.EBADF
		return
	}
	if hi.reader == nil {
		err = syscall.EBADF
		return
	}
	if ho.writer == nil {
		err = syscall.EACCES
		return
	}
	if offIn >= maxFileSize || offIn+size >= maxFileSize || offOut >= maxFileSize || offOut+size >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	if flags != 0 {
		err = syscall.EINVAL
		return
	}
	if nodeIn == nodeOut && (offIn <= offOut && offOut < offIn+size || offOut <= offIn && offIn < offOut+size) {
		err = syscall.EINVAL // overlap
		return
	}

	if !ho.Wlock(ctx) {
		err = syscall.EINTR
		return
	}
	defer ho.Wunlock()
	defer ho.removeOp(ctx)
	if nodeIn != nodeOut {
		if !hi.Rlock(ctx) {
			err = syscall.EINTR
			return
		}
		defer hi.Runlock()
		defer hi.removeOp(ctx)
	}

	err = v.writer.Flush(ctx, nodeOut)
	if err != 0 {
		return
	}
	err = v.Meta.CopyFileRange(ctx, nodeIn, offIn, nodeOut, offOut, size, flags, &copied)
	if err == 0 {
		v.reader.Invalidate(nodeOut, offOut, size)
		v.invalidateLength(nodeOut)
	}
	return
}

func (v *VFS) Flush(ctx Context, ino Ino, fh uint64, lockOwner uint64) (err syscall.Errno) {
	if ino == controlInode && runtime.GOOS == "darwin" {
		fh = v.getControlHandle(ctx.Pid())
		defer v.releaseControlHandle(ctx.Pid())
	}
	defer func() { logit(ctx, "flush (%d,%d,%016X): %s", ino, fh, lockOwner, strerr(err)) }()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if IsSpecialNode(ino) {
		if ino == controlInode && h.bctx != nil {
			h.bctx.Cancel()
		}
		return
	}

	if h.writer != nil {
		for !h.Wlock(ctx) {
			h.cancelOp(ctx.Pid())
		}

		err = h.writer.Flush(ctx)
		if err == syscall.ENOENT || err == syscall.EPERM || err == syscall.EINVAL {
			err = syscall.EBADF
		}
		h.removeOp(ctx)
		h.Wunlock()
	} else if h.reader != nil {
		h.cancelOp(ctx.Pid())
	}

	h.Lock()
	locks := h.locks
	h.Unlock()
	if locks&2 != 0 {
		_ = v.Meta.Setlk(ctx, ino, lockOwner, false, F_UNLCK, 0, 0x7FFFFFFFFFFFFFFF, 0)
	}
	return
}

func (v *VFS) Fsync(ctx Context, ino Ino, datasync int, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fsync (%d,%d): %s", ino, datasync, strerr(err)) }()
	if IsSpecialNode(ino) {
		return
	}
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if h.writer != nil {
		if !h.Wlock(ctx) {
			return syscall.EINTR
		}
		defer h.Wunlock()
		defer h.removeOp(ctx)

		err = h.writer.Flush(ctx)
		if err == syscall.ENOENT || err == syscall.EPERM || err == syscall.EINVAL {
			err = syscall.EBADF
		}
	}
	return
}

const (
	xattrMaxName = 255
	xattrMaxSize = 65536
)

func (v *VFS) SetXattr(ctx Context, ino Ino, name string, value []byte, flags uint32) (err syscall.Errno) {
	defer func() { logit(ctx, "setxattr (%d,%s,%d,%d): %s", ino, name, len(value), flags, strerr(err)) }()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if len(value) > xattrMaxSize {
		if runtime.GOOS == "darwin" {
			err = syscall.E2BIG
		} else {
			err = syscall.ERANGE
		}
		return
	}
	if len(name) > xattrMaxName {
		if runtime.GOOS == "darwin" {
			err = syscall.EPERM
		} else {
			err = syscall.ERANGE
		}
		return
	}
	if len(name) == 0 {
		err = syscall.EINVAL
		return
	}
	if name == "system.posix_acl_access" || name == "system.posix_acl_default" {
		err = syscall.ENOTSUP
		return
	}
	err = v.Meta.SetXattr(ctx, ino, name, value, flags)
	return
}

func (v *VFS) GetXattr(ctx Context, ino Ino, name string, size uint32) (value []byte, err syscall.Errno) {
	defer func() { logit(ctx, "getxattr (%d,%s,%d): %s (%d)", ino, name, size, strerr(err), len(value)) }()

	if IsSpecialNode(ino) {
		err = meta.ENOATTR
		return
	}
	if len(name) > xattrMaxName {
		if runtime.GOOS == "darwin" {
			err = syscall.EPERM
		} else {
			err = syscall.ERANGE
		}
		return
	}
	if len(name) == 0 {
		err = syscall.EINVAL
		return
	}
	if name == "system.posix_acl_access" || name == "system.posix_acl_default" {
		err = syscall.ENOTSUP
		return
	}
	err = v.Meta.GetXattr(ctx, ino, name, &value)
	if size > 0 && len(value) > int(size) {
		err = syscall.ERANGE
	}
	return
}

func (v *VFS) ListXattr(ctx Context, ino Ino, size int) (data []byte, err syscall.Errno) {
	defer func() { logit(ctx, "listxattr (%d,%d): %s (%d)", ino, size, strerr(err), len(data)) }()
	if IsSpecialNode(ino) {
		err = meta.ENOATTR
		return
	}
	err = v.Meta.ListXattr(ctx, ino, &data)
	if size > 0 && len(data) > size {
		err = syscall.ERANGE
	}
	return
}

func (v *VFS) RemoveXattr(ctx Context, ino Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "removexattr (%d,%s): %s", ino, name, strerr(err)) }()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if name == "system.posix_acl_access" || name == "system.posix_acl_default" {
		return syscall.ENOTSUP
	}
	if len(name) > xattrMaxName {
		if runtime.GOOS == "darwin" {
			err = syscall.EPERM
		} else {
			err = syscall.ERANGE
		}
		return
	}
	if len(name) == 0 {
		err = syscall.EINVAL
		return
	}
	err = v.Meta.RemoveXattr(ctx, ino, name)
	return
}

var logger = utils.GetLogger("juicefs")

type VFS struct {
	Conf            *Config
	Meta            meta.Meta
	Store           chunk.ChunkStore
	InvalidateEntry func(parent meta.Ino, name string) syscall.Errno
	reader          DataReader
	writer          DataWriter

	handles map[Ino][]*handle
	hanleM  sync.Mutex
	nextfh  uint64

	modM       sync.Mutex
	modifiedAt map[Ino]time.Time

	handlersGause  prometheus.GaugeFunc
	usedBufferSize prometheus.GaugeFunc
	storeCacheSize prometheus.GaugeFunc
	registry       *prometheus.Registry
}

func NewVFS(conf *Config, m meta.Meta, store chunk.ChunkStore, registerer prometheus.Registerer, registry *prometheus.Registry) *VFS {
	reader := NewDataReader(conf, m, store)
	writer := NewDataWriter(conf, m, store, reader)

	v := &VFS{
		Conf:       conf,
		Meta:       m,
		Store:      store,
		reader:     reader,
		writer:     writer,
		handles:    make(map[Ino][]*handle),
		modifiedAt: make(map[meta.Ino]time.Time),
		nextfh:     1,
		registry:   registry,
	}

	n := getInternalNode(configInode)
	v.Conf.Format.RemoveSecret()
	data, _ := json.MarshalIndent(v.Conf, "", " ")
	n.attr.Length = uint64(len(data))
	if conf.Meta.Subdir != "" { // don't show trash directory
		internalNodes = internalNodes[:len(internalNodes)-1]
	}

	go v.cleanupModified()
	initVFSMetrics(v, writer, registerer)
	return v
}

func (v *VFS) invalidateLength(ino Ino) {
	v.modM.Lock()
	v.modifiedAt[ino] = time.Now()
	v.modM.Unlock()
}

func (v *VFS) ModifiedSince(ino Ino, start time.Time) bool {
	v.modM.Lock()
	t, ok := v.modifiedAt[ino]
	v.modM.Unlock()
	return ok && t.After(start)
}

func (v *VFS) cleanupModified() {
	for {
		v.modM.Lock()
		expire := time.Now().Add(time.Second * -30)
		var cnt, deleted int
		for i, t := range v.modifiedAt {
			if t.Before(expire) {
				delete(v.modifiedAt, i)
				deleted++
			}
			cnt++
			if cnt > 1000 {
				break
			}
		}
		v.modM.Unlock()
		time.Sleep(time.Millisecond * time.Duration(1000*(cnt+1-deleted*2)/(cnt+1)))
	}
}

func initVFSMetrics(v *VFS, writer DataWriter, registerer prometheus.Registerer) {
	if registerer == nil {
		return
	}
	v.handlersGause = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fuse_open_handlers",
		Help: "number of open files and directories.",
	}, func() float64 {
		v.hanleM.Lock()
		defer v.hanleM.Unlock()
		return float64(len(v.handles))
	})
	v.usedBufferSize = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "used_buffer_size_bytes",
		Help: "size of currently used buffer.",
	}, func() float64 {
		if dw, ok := writer.(*dataWriter); ok {
			return float64(dw.usedBufferSize())
		}
		return 0.0
	})
	v.storeCacheSize = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "store_cache_size_bytes",
		Help: "size of store cache.",
	}, func() float64 {
		if dw, ok := writer.(*dataWriter); ok {
			return float64(dw.store.UsedMemory())
		}
		return 0.0
	})
	_ = registerer.Register(v.handlersGause)
	_ = registerer.Register(v.usedBufferSize)
	_ = registerer.Register(v.storeCacheSize)
}

func InitMetrics(registerer prometheus.Registerer) {
	if registerer == nil {
		return
	}
	registerer.MustRegister(readSizeHistogram)
	registerer.MustRegister(writtenSizeHistogram)
	registerer.MustRegister(opsDurationsHistogram)
	registerer.MustRegister(compactSizeHistogram)
}
