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
	"runtime"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

type Ino = meta.Ino
type Attr = meta.Attr
type Context = LogContext

const (
	rootID      = 1
	maxName     = 255
	maxSymlink  = 4096
	maxFileSize = meta.ChunkSize << 31
)

type StorageConfig struct {
	Name       string
	Endpoint   string
	AccessKey  string
	SecretKey  string
	Key        string
	KeyPath    string
	Passphrase string
}

type Config struct {
	Meta       *meta.Config
	Format     *meta.Format
	Primary    *StorageConfig
	Chunk      *chunk.Config
	Version    string
	Mountpoint string
	Prefix     string
	AccessLog  string
}

var (
	m      meta.Meta
	reader DataReader
	writer DataWriter
)

func Lookup(ctx Context, parent Ino, name string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "lookup (%d,%s): %s%s", parent, name, strerr(err), (*Entry)(entry))
	}()
	nleng := len(name)
	if nleng > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	var inode Ino
	var attr = &Attr{}
	if parent == rootID {
		if nleng == 2 && name[0] == '.' && name[1] == '.' {
			name = name[:1]
		}
		n := getInternalNodeByName(name)
		if n != nil {
			entry = &meta.Entry{Inode: n.inode, Attr: n.attr}
			return
		}

	}
	err = m.Lookup(ctx, parent, name, &inode, attr)
	if err != 0 {
		return
	}
	UpdateLength(inode, attr)
	entry = &meta.Entry{Inode: inode, Attr: attr}
	return
}

func GetAttr(ctx Context, ino Ino, opened uint8) (entry *meta.Entry, err syscall.Errno) {
	defer func() { logit(ctx, "getattr (%d): %s%s", ino, strerr(err), (*Entry)(entry)) }()
	if IsSpecialNode(ino) && getInternalNode(ino) != nil {
		n := getInternalNode(ino)
		entry = &meta.Entry{Inode: n.inode, Attr: n.attr}
		return
	}
	var attr = &Attr{}
	err = m.GetAttr(ctx, ino, attr)
	if err != 0 {
		return
	}
	UpdateLength(ino, attr)
	entry = &meta.Entry{Inode: ino, Attr: attr}
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

func Mknod(ctx Context, parent Ino, name string, mode uint16, cumask uint16, rdev uint32) (entry *meta.Entry, err syscall.Errno) {
	nleng := uint8(len(name))
	defer func() {
		logit(ctx, "mknod (%d,%s,%s:0%04o,0x%08X): %s%s", parent, name, smode(mode), mode, rdev, strerr(err), (*Entry)(entry))
	}()
	if parent == rootID && isSpecialName(name) {
		err = syscall.EACCES
		return
	}
	if nleng > maxName {
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
	err = m.Mknod(ctx, parent, name, _type, mode&07777, cumask, uint32(rdev), &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func Unlink(ctx Context, parent Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "unlink (%d,%s): %s", parent, name, strerr(err)) }()
	nleng := uint8(len(name))
	if parent == rootID && isSpecialName(name) {
		err = syscall.EACCES
		return
	}
	if nleng > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = m.Unlink(ctx, parent, name)
	return
}

func Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "mkdir (%d,%s,%s:0%04o): %s%s", parent, name, smode(mode), mode, strerr(err), (*Entry)(entry))
	}()
	nleng := uint8(len(name))
	if parent == rootID && isSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if nleng > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = m.Mkdir(ctx, parent, name, mode, cumask, 0, &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func Rmdir(ctx Context, parent Ino, name string) (err syscall.Errno) {
	nleng := uint8(len(name))
	defer func() { logit(ctx, "rmdir (%d,%s): %s", parent, name, strerr(err)) }()
	if parent == rootID && isSpecialName(name) {
		err = syscall.EACCES
		return
	}
	if nleng > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = m.Rmdir(ctx, parent, name)
	return
}

func Symlink(ctx Context, path string, parent Ino, name string) (entry *meta.Entry, err syscall.Errno) {
	nleng := uint8(len(name))
	defer func() {
		logit(ctx, "symlink (%d,%s,%s): %s%s", parent, name, path, strerr(err), (*Entry)(entry))
	}()
	if parent == rootID && isSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if nleng > maxName || (len(path)+1) > maxSymlink {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = m.Symlink(ctx, parent, name, path, &inode, attr)
	if err == 0 {
		entry = &meta.Entry{Inode: inode, Attr: attr}
	}
	return
}

func Readlink(ctx Context, ino Ino) (path []byte, err syscall.Errno) {
	defer func() { logit(ctx, "readlink (%d): %s (%s)", ino, strerr(err), string(path)) }()
	err = m.ReadLink(ctx, ino, &path)
	return
}

func Rename(ctx Context, parent Ino, name string, newparent Ino, newname string) (err syscall.Errno) {
	defer func() { logit(ctx, "rename (%d,%s,%d,%s): %s", parent, name, newparent, newname, strerr(err)) }()
	if parent == rootID && isSpecialName(name) {
		err = syscall.EACCES
		return
	}
	if newparent == rootID && isSpecialName(newname) {
		err = syscall.EACCES
		return
	}
	if len(name) > maxName || len(newname) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	err = m.Rename(ctx, parent, name, newparent, newname, nil, nil)
	return
}

func Link(ctx Context, ino Ino, newparent Ino, newname string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "link (%d,%d,%s): %s%s", ino, newparent, newname, strerr(err), (*Entry)(entry))
	}()
	if IsSpecialNode(ino) {
		err = syscall.EACCES
		return
	}
	if newparent == rootID && isSpecialName(newname) {
		err = syscall.EACCES
		return
	}
	if len(newname) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var attr = &Attr{}
	err = m.Link(ctx, ino, newparent, newname, attr)
	if err == 0 {
		UpdateLength(ino, attr)
		entry = &meta.Entry{Inode: ino, Attr: attr}
	}
	return
}

func Opendir(ctx Context, ino Ino) (fh uint64, err syscall.Errno) {
	defer func() { logit(ctx, "opendir (%d): %s [fh:%d]", ino, strerr(err), fh) }()
	if IsSpecialNode(ino) {
		err = syscall.ENOTDIR
		return
	}
	fh = newHandle(ino).fh
	return
}

func UpdateLength(inode Ino, attr *meta.Attr) {
	if attr.Full && attr.Typ == meta.TypeFile {
		length := writer.GetLength(inode)
		if length > attr.Length {
			attr.Length = length
		}
		reader.Truncate(inode, attr.Length)
	}
}

func Readdir(ctx Context, ino Ino, size uint32, off int, fh uint64, plus bool) (entries []*meta.Entry, err syscall.Errno) {
	defer func() { logit(ctx, "readdir (%d,%d,%d): %s (%d)", ino, size, off, strerr(err), len(entries)) }()
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.Lock()
	defer h.Unlock()

	if h.children == nil || off == 0 {
		var inodes []*meta.Entry
		err = m.Readdir(ctx, ino, 1, &inodes)
		if err == syscall.EACCES {
			err = m.Readdir(ctx, ino, 0, &inodes)
		}
		if err != 0 {
			return
		}
		h.children = inodes
		if ino == rootID {
			// add internal nodes
			for _, node := range internalNodes {
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
	return
}

func Releasedir(ctx Context, ino Ino, fh uint64) int {
	h := findHandle(ino, fh)
	if h == nil {
		return 0
	}
	ReleaseHandler(ino, fh)
	logit(ctx, "releasedir (%d): OK", ino)
	return 0
}

func Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	defer func() {
		logit(ctx, "create (%d,%s,%s:0%04o): %s%s [fh:%d]", parent, name, smode(mode), mode, strerr(err), (*Entry)(entry), fh)
	}()
	if parent == rootID && isSpecialName(name) {
		err = syscall.EEXIST
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}

	var inode Ino
	var attr = &Attr{}
	err = m.Create(ctx, parent, name, mode&07777, cumask, &inode, attr)
	if runtime.GOOS == "darwin" && err == syscall.ENOENT {
		err = syscall.EACCES
	}
	if err != 0 {
		return
	}

	fh = newFileHandle(inode, 0, flags)
	entry = &meta.Entry{Inode: inode, Attr: attr}
	return
}

func Open(ctx Context, ino Ino, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	var attr = &Attr{}
	defer func() {
		if entry != nil {
			logit(ctx, "open (%d): %s [fh:%d]", ino, strerr(err), fh)
		} else {
			logit(ctx, "open (%d): %s", ino, strerr(err))
		}
	}()
	if IsSpecialNode(ino) {
		if (flags & O_ACCMODE) != syscall.O_RDONLY {
			err = syscall.EACCES
			return
		}
		h := newHandle(ino)
		fh = h.fh
		switch ino {
		case logInode:
			openAccessLog(fh)
		}
		n := getInternalNode(ino)
		entry = &meta.Entry{Inode: ino, Attr: n.attr}
		return
	}

	err = m.Open(ctx, ino, uint8(flags), attr)
	if err != 0 {
		return
	}

	UpdateLength(ino, attr)
	fh = newFileHandle(ino, attr.Length, flags)
	entry = &meta.Entry{Inode: ino, Attr: attr}
	return
}

func Truncate(ctx Context, ino Ino, size int64, opened uint8, attr *Attr) (err syscall.Errno) {
	defer func() { logit(ctx, "truncate (%d,%d): %s", ino, size, strerr(err)) }()
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
	writer.Flush(ctx, ino)
	err = m.Truncate(ctx, ino, 0, uint64(size), attr)
	if err != 0 {
		return
	}
	writer.Truncate(ino, uint64(size))
	reader.Truncate(ino, uint64(size))
	return 0
}

func ReleaseHandler(ino Ino, fh uint64) {
	releaseFileHandle(ino, fh)
}

func Release(ctx Context, ino Ino, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "release (%d): %s", ino, strerr(err)) }()
	if IsSpecialNode(ino) {
		if ino == logInode {
			closeAccessLog(fh)
		}
		releaseHandle(ino, fh)
		return
	}
	if fh > 0 {
		f := findHandle(ino, fh)
		if f != nil {
			f.Lock()
			// rwlock_wait_for_unlock:
			for (f.writing | f.writers | f.readers) != 0 {
				if f.cond.WaitWithTimeout(time.Millisecond*100) && ctx.Canceled() {
					f.Unlock()
					err = syscall.EINTR
					return
				}
			}
			locks := f.locks
			owner := f.flockOwner
			f.Unlock()
			if f.writer != nil {
				f.writer.Close(ctx)
			}
			if locks&1 != 0 {
				_ = m.Flock(ctx, ino, owner, syscall.F_UNLCK, false)
			}
		}
		_ = m.Close(ctx, ino)
		go releaseFileHandle(ino, fh) // after writes it waits for data sync, so do it after everything
	}
	return
}

func Read(ctx Context, ino Ino, buf []byte, off uint64, fh uint64) (n int, err syscall.Errno) {
	size := uint32(len(buf))
	if IsSpecialNode(ino) {
		if ino == logInode {
			n = readAccessLog(fh, buf)
		} else {
			h := findHandle(ino, fh)
			if h == nil {
				err = syscall.EBADF
				return
			}
			data := h.data
			if int(off) < len(data) {
				data = data[off:]
				if int(size) < len(data) {
					data = data[:size]
				}
				n = copy(buf, data)
			}
			logit(ctx, "read (%d,%d,%d): OK (%d)", ino, size, off, n)
		}
		return
	}

	defer func() {
		logit(ctx, "read (%d,%d,%d): %s (%d)", ino, size, off, strerr(err), n)
	}()
	h := findHandle(ino, fh)
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

	writer.Flush(ctx, ino)
	n, err = h.reader.Read(ctx, off, buf)
	if err == syscall.ENOENT {
		err = syscall.EBADF
	}
	h.removeOp(ctx)
	return
}

func Write(ctx Context, ino Ino, buf []byte, off, fh uint64) (err syscall.Errno) {
	size := uint64(len(buf))
	defer func() { logit(ctx, "write (%d,%d,%d): %s", ino, size, off, strerr(err)) }()
	if IsSpecialNode(ino) {
		err = syscall.EACCES
		return
	}
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if off >= maxFileSize || off+size >= maxFileSize {
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

	err = h.writer.Write(ctx, off, buf)
	if err == syscall.ENOENT || err == syscall.EPERM || err == syscall.EINVAL {
		err = syscall.EBADF
	}
	h.removeOp(ctx)

	if err != 0 {
		return
	}
	reader.Truncate(ino, writer.GetLength(ino))
	return
}

func Fallocate(ctx Context, ino Ino, mode uint8, off, length int64, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fallocate (%d,%d,%d,%d): %s", ino, mode, off, length, strerr(err)) }()
	if off < 0 || length <= 0 {
		err = syscall.EINVAL
		return
	}
	if IsSpecialNode(ino) {
		err = syscall.EACCES
		return
	}
	h := findHandle(ino, fh)
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

	err = m.Fallocate(ctx, ino, mode, uint64(off), uint64(length))
	return
}

func doFsync(ctx Context, h *handle) (err syscall.Errno) {
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
	return err
}

func Flush(ctx Context, ino Ino, fh uint64, lockOwner uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "flush (%d): %s", ino, strerr(err)) }()
	if IsSpecialNode(ino) {
		return
	}
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}

	if h.writer != nil {
		if !h.Wlock(ctx) {
			h.cancelOp(ctx.Pid())
			err = syscall.EINTR
			return
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
		_ = m.Setlk(ctx, ino, lockOwner, false, syscall.F_UNLCK, 0, 0x7FFFFFFFFFFFFFFF, 0)
	}
	return
}

func Fsync(ctx Context, ino Ino, datasync int, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fsync (%d,%d): %s", ino, datasync, strerr(err)) }()
	if IsSpecialNode(ino) {
		return
	}
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	err = doFsync(ctx, h)
	return
}

const (
	xattrMaxName = 255
	xattrMaxSize = 65536
)

func SetXattr(ctx Context, ino Ino, name string, value []byte, flags int) (err syscall.Errno) {
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
	err = m.SetXattr(ctx, ino, name, value)
	return
}

func GetXattr(ctx Context, ino Ino, name string, size uint32) (value []byte, err syscall.Errno) {
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
	err = m.GetXattr(ctx, ino, name, &value)
	if size > 0 && len(value) > int(size) {
		err = syscall.ERANGE
	}
	return
}

func ListXattr(ctx Context, ino Ino, size int) (data []byte, err syscall.Errno) {
	defer func() { logit(ctx, "listxattr (%d,%d): %s (%d)", ino, size, strerr(err), len(data)) }()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	err = m.ListXattr(ctx, ino, &data)
	if size > 0 && len(data) > size {
		err = syscall.ERANGE
	}
	return
}

func RemoveXattr(ctx Context, ino Ino, name string) (err syscall.Errno) {
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
	err = m.RemoveXattr(ctx, ino, name)
	return
}

var logger = utils.GetLogger("juicefs")

func Init(conf *Config, m_ meta.Meta, store chunk.ChunkStore) {
	m = m_
	reader = NewDataReader(conf, m, store)
	writer = NewDataWriter(conf, m, store)
	handles = make(map[Ino][]*handle)
}
