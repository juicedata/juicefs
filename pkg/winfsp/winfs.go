//go:build windows
// +build windows

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

package winfsp

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/sys/windows/registry"
)

var logger = utils.GetLogger("juicefs")

const invalidFileHandle = uint64(0xffffffffffffffff)

type Ino = meta.Ino

type handleInfo struct {
	ino           meta.Ino
	cacheAttr     *meta.Attr
	attrExpiredAt time.Time
}

func trace(vals ...interface{}) func(vals ...interface{}) {
	uid, gid, pid := fuse.Getcontext()
	return Trace(1, fmt.Sprintf("[uid=%v,gid=%v,pid=%d]", uid, gid, pid), vals...)
}

type juice struct {
	fuse.FileSystemBase
	sync.RWMutex
	conf         *vfs.Config
	vfs          *vfs.VFS
	fs           *fs.FileSystem
	host         *fuse.FileSystemHost
	handlers     map[uint64]handleInfo
	badfd        map[uint64]uint64
	inoHandleMap map[meta.Ino][]uint64

	asRoot         bool
	delayClose     int
	enabledGetPath bool
	disableSymlink bool

	attrCacheTimeout time.Duration
}

// Init is called when the file system is created.
func (j *juice) Init() {
	j.handlers = make(map[uint64]handleInfo)
	j.badfd = make(map[uint64]uint64)
	j.inoHandleMap = make(map[meta.Ino][]uint64)
}

func (j *juice) newContext() vfs.LogContext {
	if j.asRoot {
		return vfs.NewLogContext(meta.Background())
	}
	uid, gid, pid := fuse.Getcontext()
	if uid == 0xffffffff || uid == 18 {
		uid = 0
	}
	if gid == 0xffffffff || gid == 18 {
		gid = 0
	}
	if pid == -1 {
		pid = 0
	}
	ctx := meta.NewContext(uint32(pid), uid, []uint32{gid})
	return vfs.NewLogContext(ctx)
}

// Statfs gets file system statistics.
func (j *juice) Statfs(path string, stat *fuse.Statfs_t) int {
	ctx := j.newContext()
	// defer trace(path)(stat)
	var totalspace, availspace, iused, iavail uint64
	j.fs.Meta().StatFS(ctx, meta.RootInode, &totalspace, &availspace, &iused, &iavail)
	var bsize uint64 = 4096
	blocks := totalspace / bsize
	bavail := availspace / bsize
	stat.Namemax = 255
	stat.Frsize = 4096
	stat.Bsize = bsize
	stat.Blocks = blocks
	stat.Bfree = bavail
	stat.Bavail = bavail
	stat.Files = iused + iavail
	stat.Ffree = iavail
	stat.Favail = iavail
	return 0
}

func errorconv(err syscall.Errno) int {
	// convert based on the error.i file in winfsp project
	switch err {
	case syscall.EACCES:
		return -fuse.EACCES
	case syscall.EEXIST:
		return -fuse.EEXIST
	case syscall.ENOENT, syscall.ENOTDIR:
		return -fuse.ENOENT
	case syscall.ECANCELED:
		return -fuse.EINTR
	case syscall.EIO:
		return -fuse.EIO
	case syscall.EINVAL:
		return -fuse.EINVAL
	case syscall.EBADFD:
		return -fuse.EBADF
	case syscall.EDQUOT:
		return -fuse.ENOSPC
	case syscall.EBUSY:
		return -fuse.EBUSY
	case syscall.ENOTEMPTY:
		return -fuse.ENOTEMPTY
	case syscall.ENAMETOOLONG:
		return -fuse.ENAMETOOLONG
	case syscall.ERROR_HANDLE_EOF:
		return -fuse.ENODATA
	}

	return -int(err)
}

func fuseFlagToSyscall(flag int) int {
	var ret int

	if flag&fuse.O_RDONLY != 0 {
		ret |= syscall.O_RDONLY
	}
	if flag&fuse.O_WRONLY != 0 {
		ret |= syscall.O_WRONLY
	}
	if flag&fuse.O_RDWR != 0 {
		ret |= syscall.O_RDWR
	}
	if flag&fuse.O_APPEND != 0 {
		ret |= syscall.O_APPEND
	}
	if flag&fuse.O_CREAT != 0 {
		ret |= syscall.O_CREAT
	}
	if flag&fuse.O_EXCL != 0 {
		ret |= syscall.O_EXCL
	}
	if flag&fuse.O_TRUNC != 0 {
		ret |= syscall.O_TRUNC
	}
	return ret

}

// Mknod creates a file node.
func (j *juice) Mknod(p string, mode uint32, dev uint64) (e int) {
	ctx := j.newContext()
	defer trace(p, mode, dev)(&e)
	parent, err := j.fs.Open(ctx, path.Dir(p), 0)
	if err != 0 {
		e = errorconv(err)
		return
	}
	_, errno := j.vfs.Mknod(ctx, parent.Inode(), path.Base(p), uint16(mode), 0, uint32(dev))
	e = errorconv(errno)
	if e == 0 {
		j.fs.InvalidateEntry(parent.Inode(), path.Base(p))
	}
	return
}

// Mkdir creates a directory.
func (j *juice) Mkdir(path string, mode uint32) (e int) {
	if path == "/.UMOUNTIT" {
		logger.Infof("Umount %s ...", j.conf.Meta.MountPoint)
		go j.host.Unmount()
		return -fuse.ENOENT
	}
	ctx := j.newContext()
	defer trace(path, mode)(&e)
	e = errorconv(j.fs.Mkdir(ctx, path, uint16(mode), 0))
	return
}

// Unlink removes a file.
func (j *juice) Unlink(path string) (e int) {
	ctx := j.newContext()
	defer trace(path)(&e)
	e = errorconv(j.fs.Delete(ctx, path))
	return
}

// Rmdir removes a directory.
func (j *juice) Rmdir(path string) (e int) {
	ctx := j.newContext()
	defer trace(path)(&e)
	e = errorconv(j.fs.Delete(ctx, path))
	return
}

func (j *juice) Symlink(target string, newpath string) (e int) {
	return -fuse.ENOSYS
	ctx := j.newContext()
	defer trace(target, newpath)(&e)
	parent, err := j.fs.Open(ctx, path.Dir(newpath), 0)
	if err != 0 {
		e = errorconv(err)
		return
	}
	_, errno := j.vfs.Symlink(ctx, target, parent.Inode(), path.Base(newpath))
	e = errorconv(errno)
	return
}

func (j *juice) Readlink(path string) (e int, target string) {
	ctx := j.newContext()
	defer trace(path)(&e, &target)
	if path == "/" && j.disableSymlink {
		e = -fuse.ENOSYS
		return
	}
	fi, err := j.fs.Lstat(ctx, path)
	if err != 0 {
		e = errorconv(err)
		return
	}
	t, errno := j.vfs.Readlink(ctx, fi.Inode())
	e = errorconv(errno)
	target = string(t)
	return
}

// Rename renames a file.
func (j *juice) Rename(oldpath string, newpath string) (e int) {
	ctx := j.newContext()
	defer trace(oldpath, newpath)(&e)
	e = errorconv(j.fs.Rename(ctx, oldpath, newpath, 0))
	return
}

// Chmod changes the permission bits of a file.
func (j *juice) Chmod(path string, mode uint32) (e int) {
	ctx := j.newContext()
	defer trace(path, mode)(&e)
	f, err := j.fs.Open(ctx, path, 0)
	if err != 0 {
		e = errorconv(err)
		return
	}
	e = errorconv(f.Chmod(ctx, uint16(mode)))
	if e == 0 {
		j.invalidateAttrCache(f.Inode())
	}
	return
}

// Chown changes the owner and group of a file.
func (j *juice) Chown(path string, uid uint32, gid uint32) (e int) {
	ctx := j.newContext()
	defer trace(path, uid, gid)(&e)
	f, err := j.fs.Open(ctx, path, 0)
	if err != 0 {
		e = errorconv(err)
		return
	}
	if runtime.GOOS == "windows" {
		// FIXME: don't change ownership in windows
		return 0
	}
	info, _ := f.Stat()
	if uid == 0xffffffff {
		uid = uint32(info.(*fs.FileStat).Uid())
	}
	if gid == 0xffffffff {
		gid = uint32(info.(*fs.FileStat).Gid())
	}
	e = errorconv(f.Chown(ctx, uid, gid))
	return
}

// Utimens changes the access and modification times of a file.
func (j *juice) Utimens(path string, tmsp []fuse.Timespec) (e int) {
	ctx := j.newContext()
	defer trace(path, tmsp)(&e)
	f, err := j.fs.Open(ctx, path, 0)
	if err != 0 {
		e = errorconv(err)
	} else {
		e = errorconv(f.Utime2(ctx, tmsp[0].Sec, tmsp[0].Nsec, tmsp[1].Sec, tmsp[1].Nsec))
		if e == 0 {
			j.invalidateAttrCache(f.Inode())
		}
	}
	return
}

// Create creates and opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) Create(p string, flags int, mode uint32) (e int, fh uint64) {
	ctx := j.newContext()
	defer trace(p, flags, mode)(&e, &fh)
	parent, err := j.fs.Open(ctx, path.Dir(p), 0)
	if err != 0 {
		e = errorconv(err)
		return
	}

	entry, fh, errno := j.vfs.Create(ctx, parent.Inode(), path.Base(p), uint16(mode), 0, uint32(fuseFlagToSyscall(flags)))
	if errno == 0 {
		j.Lock()
		j.handlers[fh] = handleInfo{
			ino:           entry.Inode,
			cacheAttr:     entry.Attr,
			attrExpiredAt: time.Now().Add(j.conf.AttrTimeout),
		}
		j.inoHandleMap[entry.Inode] = append(j.inoHandleMap[entry.Inode], fh)
		j.Unlock()
	}
	e = errorconv(errno)
	if e == 0 {
		j.fs.InvalidateEntry(parent.Inode(), path.Base(p))
	}
	return
}

// Open opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) Open(path string, flags int) (e int, fh uint64) {
	var fi fuse.FileInfo_t
	fi.Flags = fuseFlagToSyscall(flags)
	e = j.OpenEx(path, &fi)
	fh = fi.Fh
	return
}

// Open opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) OpenEx(p string, fi *fuse.FileInfo_t) (e int) {
	ctx := j.newContext()
	defer trace(p, fi.Flags)(&e, &fi.Fh)
	ino := meta.Ino(0)
	if strings.HasSuffix(p, "/.control") {
		ino, _ = vfs.GetInternalNodeByName(".control")
		if ino == 0 {
			e = -fuse.ENOENT
			return
		}
	} else if filename := path.Base(p); vfs.IsSpecialName(filename) && path.Dir(p) == "/" {
		ino, _ = vfs.GetInternalNodeByName(filename)
		if ino == 0 {
			e = -fuse.ENOENT
			return
		}
	} else {
		f, err := j.fs.Open(ctx, p, 0)
		if err != 0 {
			e = -fuse.ENOENT
			return
		}
		ino = f.Inode()
	}

	entry, fh, errno := j.vfs.Open(ctx, ino, uint32(fuseFlagToSyscall(fi.Flags)))
	if errno == 0 {
		fi.Fh = fh
		if vfs.IsSpecialNode(ino) {
			fi.DirectIo = true
		} else {
			fi.KeepCache = entry.Attr.KeepCache
		}
		j.Lock()
		j.handlers[fh] = handleInfo{
			ino:           ino,
			cacheAttr:     entry.Attr,
			attrExpiredAt: time.Now().Add(j.conf.AttrTimeout),
		}
		j.inoHandleMap[ino] = append(j.inoHandleMap[ino], fh)
		j.Unlock()
	}
	e = errorconv(errno)
	return
}

func attrToStat(inode Ino, attr *meta.Attr, stat *fuse.Stat_t) {
	stat.Ino = uint64(inode)
	stat.Mode = attr.SMode()
	stat.Uid = attr.Uid
	if stat.Uid == 0 {
		stat.Uid = 18 // System
	}
	stat.Gid = attr.Gid
	if stat.Gid == 0 {
		stat.Gid = 18 // System
	}
	stat.Birthtim.Sec = attr.Atime
	stat.Birthtim.Nsec = int64(attr.Atimensec)
	stat.Atim.Sec = attr.Atime
	stat.Atim.Nsec = int64(attr.Atimensec)
	stat.Mtim.Sec = attr.Mtime
	stat.Mtim.Nsec = int64(attr.Mtimensec)
	stat.Ctim.Sec = attr.Ctime
	stat.Ctim.Nsec = int64(attr.Ctimensec)
	stat.Nlink = attr.Nlink
	var rdev uint32
	var size, blocks uint64
	switch attr.Typ {
	case meta.TypeDirectory:
		fallthrough
	case meta.TypeSymlink:
		fallthrough
	case meta.TypeFile:
		size = attr.Length
		blocks = (size + 0xffff) / 0x10000
		stat.Blksize = 0x10000
	case meta.TypeBlockDev:
		fallthrough
	case meta.TypeCharDev:
		rdev = attr.Rdev
	}
	stat.Size = int64(size)
	stat.Blocks = int64(blocks)
	stat.Rdev = uint64(rdev)
	if attr.Flags&meta.FlagImmutable != 0 {
		stat.Flags |= fuse.UF_READONLY
	}
	if attr.Flags&meta.FlagWindowsHidden != 0 {
		stat.Flags |= fuse.UF_HIDDEN
	}
	if attr.Flags&meta.FlagWindowsSystem != 0 {
		stat.Flags |= fuse.UF_SYSTEM
	}
	if attr.Flags&meta.FlagWindowsArchive != 0 {
		stat.Flags |= fuse.UF_ARCHIVE
	}
}

func (j *juice) h2i(fh *uint64) meta.Ino {
	defer j.RUnlock()
	j.RLock()

	entry := j.handlers[*fh]
	if entry.ino == 0 {
		newfh := j.badfd[*fh]
		if newfh != 0 {
			entry = j.handlers[newfh]
			if entry.ino > 0 {
				*fh = newfh
			}
		}
	}
	return entry.ino
}

func (j *juice) reopen(p string, fh *uint64) meta.Ino {
	e, newfh := j.Open(p, os.O_RDWR)
	if e != 0 {
		return 0
	}
	j.Lock()
	defer j.Unlock()
	j.badfd[*fh] = newfh
	*fh = newfh
	return j.handlers[newfh].ino
}

// Getattr gets file attributes.
func (j *juice) getAttrForSpFile(ctx vfs.LogContext, p string, stat *fuse.Stat_t, fh uint64) (e int) {
	parentDir := path.Dir(p)
	_, err := j.fs.Stat(ctx, parentDir)
	if err != 0 {
		e = -fuse.ENOENT
		return
	}

	filename := path.Base(p)
	inode, attr := vfs.GetInternalNodeByName(filename)
	if inode == 0 {
		e = -fuse.ENOENT
		return
	}

	j.vfs.UpdateLength(inode, attr)

	attr.Gid = ctx.Gid()
	attr.Uid = ctx.Uid()

	attrToStat(inode, attr, stat)
	return
}

func (j *juice) invalidateAttrCache(ino meta.Ino) {
	if j.attrCacheTimeout == 0 || ino == 0 {
		return
	}
	j.fs.InvalidateAttr(ino) // invalidate the attrcache in fs layer
	j.Lock()
	defer j.Unlock()

	handlers := j.inoHandleMap[ino]
	for _, fh := range handlers {
		if cache, ok := j.handlers[fh]; ok {
			cache.cacheAttr = nil
			cache.attrExpiredAt = time.Time{}
			j.handlers[fh] = cache
		}
	}
}

func (j *juice) getAttrFromCache(fh uint64) (entry *meta.Entry) {
	if j.attrCacheTimeout == 0 || fh == invalidFileHandle {
		return nil
	}
	j.RLock()
	defer j.RUnlock()
	if cache, ok := j.handlers[fh]; ok && cache.cacheAttr != nil {
		if time.Now().Before(cache.attrExpiredAt) {
			entry = &meta.Entry{
				Inode: cache.ino,
				Attr:  cache.cacheAttr,
			}
			return entry
		}
	}
	return nil
}

func (j *juice) setAttrCache(fh uint64, attr *meta.Attr) {
	if j.attrCacheTimeout == 0 || fh == invalidFileHandle {
		return
	}

	j.Lock()
	defer j.Unlock()

	if cache, ok := j.handlers[fh]; ok {
		cache.cacheAttr = attr
		cache.attrExpiredAt = time.Now().Add(j.attrCacheTimeout)
		j.handlers[fh] = cache
	}
}

func (j *juice) getAttr(ctx vfs.Context, fh uint64, ino Ino, opened uint8) (entry *meta.Entry, err syscall.Errno) {
	if entry := j.getAttrFromCache(fh); entry != nil {
		return entry, 0
	}

	if entry, err = j.vfs.GetAttr(ctx, ino, opened); err != 0 {
		return nil, err
	}

	j.setAttrCache(fh, entry.Attr)

	return entry, 0
}

// Getattr gets file attributes.
func (j *juice) Getattr(p string, stat *fuse.Stat_t, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(p, fh)(stat, &e)
	ino := j.h2i(&fh)

	if ino == 0 {
		// special case for .control file
		if strings.HasSuffix(p, "/.control") {
			e = j.getAttrForSpFile(ctx, p, stat, fh)
			return
		} else if vfs.IsSpecialName(path.Base(p)) && path.Dir(p) == "/" {
			e = j.getAttrForSpFile(ctx, p, stat, fh)
			return
		}

		fi, err := j.fs.Lstat(ctx, p)
		if err != 0 {
			// Known issue: If the parent directory is not exists, the Windows api such as
			// GetFileAttributeX expects the ERROR_PATH_NOT_FOUND returned.
			// However, the fuse api has no such error code defined.
			e = -fuse.ENOENT
			return
		}
		ino = fi.Inode()
		entry := fi.Attr()
		if entry != nil {
			j.vfs.UpdateLength(ino, entry)
			attrToStat(ino, entry, stat)
			return
		}
	}

	entry, errrno := j.getAttr(ctx, fh, ino, 0)
	if errrno != 0 {
		e = errorconv(errrno)
		return
	}
	j.vfs.UpdateLength(entry.Inode, entry.Attr)
	attrToStat(entry.Inode, entry.Attr, stat)
	return
}

// Truncate changes the size of a file.
func (j *juice) Truncate(path string, size int64, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(path, size, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	e = errorconv(j.vfs.Truncate(ctx, ino, size, 0, nil))
	if e == 0 {
		j.invalidateAttrCache(ino)
	}
	return
}

// Read reads data from a file.
func (j *juice) Read(path string, buf []byte, off int64, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(path, len(buf), off, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		logger.Warnf("read from released fd %d for %s, re-open it", fh, path)
		ino = j.reopen(path, &fh)
	}
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	n, err := j.vfs.Read(ctx, ino, buf, uint64(off), fh)
	if err != 0 {
		e = errorconv(err)
		return
	}
	return n
}

// Write writes data to a file.
func (j *juice) Write(path string, buff []byte, off int64, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(path, len(buff), off, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		logger.Warnf("write to released fd %d for %s, re-open it", fh, path)
		ino = j.reopen(path, &fh)
	}
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	errno := j.vfs.Write(ctx, ino, buff, uint64(off), fh)
	if errno != 0 {
		e = errorconv(errno)
	} else {
		e = len(buff)
	}

	return
}

// Flush flushes cached file data.
func (j *juice) Flush(path string, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(path, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	e = errorconv(j.vfs.Flush(ctx, ino, fh, 0))
	return
}

func (j *juice) cleanInoHandlerMap(ino meta.Ino, fh uint64) {
	handles := j.inoHandleMap[ino]
	for i, handle := range handles {
		if handle == fh {
			j.inoHandleMap[ino] = append(handles[:i], handles[i+1:]...)
			break
		}
	}
	if len(j.inoHandleMap[ino]) == 0 {
		delete(j.inoHandleMap, ino)
	}
}

// Release closes an open file.
func (j *juice) Release(path string, fh uint64) int {
	defer trace(path, fh)()
	orig := fh
	ino := j.h2i(&fh)
	if ino == 0 {
		logger.Warnf("release invalid fd %d for %s", fh, path)
		return -fuse.EBADF
	}
	go func() {
		time.Sleep(time.Second * time.Duration(j.delayClose))
		j.Lock()
		delete(j.handlers, fh)
		j.cleanInoHandlerMap(ino, fh)
		if orig != fh {
			delete(j.badfd, orig)
			j.cleanInoHandlerMap(ino, orig)
		}
		j.Unlock()
		j.vfs.Release(j.newContext(), ino, fh)
	}()
	return 0
}

// Fsync synchronizes file contents.
func (j *juice) Fsync(path string, datasync bool, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(path, datasync, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		e = -fuse.EBADF
	} else {
		e = errorconv(j.vfs.Fsync(ctx, ino, 1, fh))
	}
	return
}

// Opendir opens a directory.
func (j *juice) Opendir(path string) (e int, fh uint64) {
	ctx := j.newContext()
	defer trace(path)(&e, &fh)
	f, err := j.fs.Open(ctx, path, 0)
	if err != 0 {
		e = -fuse.ENOENT
		return
	}
	fh, errno := j.vfs.Opendir(ctx, f.Inode(), 0)
	if errno == 0 {
		j.Lock()
		j.handlers[fh] = handleInfo{
			ino: f.Inode(),
		}
		j.inoHandleMap[f.Inode()] = append(j.inoHandleMap[f.Inode()], fh)

		j.Unlock()
	}
	e = errorconv(errno)
	return
}

// Readdir reads a directory.
func (j *juice) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) (e int) {
	defer trace(path, ofst, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	ctx := j.newContext()
	entries, readAt, err := j.vfs.Readdir(ctx, ino, 100000, int(ofst), fh, true)
	if err != 0 {
		e = errorconv(err)
		return
	}
	var st fuse.Stat_t
	var ok bool
	var full = true
	// all the entries should have same format
	for _, e := range entries {
		if !e.Attr.Full {
			full = false
			break
		}
	}
	for _, e := range entries {
		name := string(e.Name)
		if full {
			if j.vfs.ModifiedSince(e.Inode, readAt) {
				if e2, err := j.vfs.GetAttr(ctx, e.Inode, 0); err == 0 {
					e.Attr = e2.Attr
				}
			}
			j.vfs.UpdateLength(e.Inode, e.Attr)
			attrToStat(e.Inode, e.Attr, &st)
			ok = fill(name, &st, 0)
		} else {
			ok = fill(name, nil, 0)
		}
		if !ok {
			break
		}
	}
	return
}

// Releasedir closes an open directory.
func (j *juice) Releasedir(path string, fh uint64) (e int) {
	defer trace(path, fh)(&e)
	ino := j.h2i(&fh)
	if ino == 0 {
		e = -fuse.EBADF
		return
	}
	j.Lock()
	delete(j.handlers, fh)
	j.cleanInoHandlerMap(ino, fh)
	j.Unlock()
	e = -int(j.vfs.Releasedir(j.newContext(), ino, fh))
	return
}

func (j *juice) Chflags(path string, flags uint32) (e int) {
	defer trace(path, flags)(&e)

	ctx := j.newContext()
	fi, err := j.fs.Stat(ctx, path)
	if err != 0 {
		e = -fuse.ENOENT
		return
	}

	var flagSet uint8
	if flags&fuse.UF_READONLY != 0 {
		flagSet |= meta.FlagImmutable
	}
	if flags&fuse.UF_HIDDEN != 0 {
		flagSet |= meta.FlagWindowsHidden
	}
	if flags&fuse.UF_SYSTEM != 0 {
		flagSet |= meta.FlagWindowsSystem
	}
	if flags&fuse.UF_ARCHIVE != 0 {
		flagSet |= meta.FlagWindowsArchive
	}

	ino := fi.Inode()
	err = j.vfs.ChFlags(ctx, ino, flagSet)
	if err != 0 {
		e = errorconv(err)
	} else {
		j.invalidateAttrCache(ino)
	}

	return
}

func (j *juice) Getpath(p string, fh uint64) (e int, ret string) {
	if !j.enabledGetPath {
		ret = p
		return
	}

	if strings.HasSuffix(p, "/.control") {
		ret = p
		return
	} else if vfs.IsSpecialName(path.Base(p)) && path.Dir(p) == "/" {
		ret = p
		return
	}

	defer trace(p, fh)(&e, &ret)
	ino := j.h2i(&fh)
	ctx := j.newContext()
	if ino == 0 {
		fi, err := j.fs.Stat(ctx, p)
		if err != 0 {
			e = errorconv(err)
			return
		}
		ino = fi.Inode()
	}

	paths := j.vfs.Meta.GetPaths(ctx, ino)
	if len(paths) == 0 {
		ret = p
		return
	}

	if len(paths) == 1 {
		ret = paths[0]
		return
	}

	retCandidicate := paths[0]

	for _, path := range paths {
		if p == path {
			ret = path
			return
		} else if strings.EqualFold(path, p) {
			retCandidicate = path
		}
	}

	ret = retCandidicate
	return
}

func Serve(v *vfs.VFS, fuseOpt string, asRoot bool, delayCloseSec int, showDotFiles bool, threadsCount int, caseSensitive bool, enabledGetPath bool) {
	var jfs juice
	conf := v.Conf
	jfs.attrCacheTimeout = v.Conf.AttrTimeout
	jfs.conf = conf
	jfs.vfs = v
	jfs.enabledGetPath = enabledGetPath
	var err error
	jfs.fs, err = fs.NewFileSystem(conf, v.Meta, v.Store, nil)
	if err != nil {
		logger.Fatalf("Initialize FileSystem failed: %s", err)
	}
	jfs.disableSymlink = os.Getenv("JUICEFS_ENABLE_SYMLINK") != "1"
	jfs.asRoot = asRoot
	jfs.delayClose = delayCloseSec
	host := fuse.NewFileSystemHost(&jfs)
	jfs.host = host
	var options = "volname=" + conf.Format.Name
	options += fmt.Sprintf(",ExactFileSystemName=JuiceFS,ThreadCount=%d", threadsCount)
	options += fmt.Sprintf(",DirInfoTimeout=%d,VolumeInfoTimeout=1000,KeepFileCache", int(conf.DirEntryTimeout.Seconds()*1000))
	options += fmt.Sprintf(",FileInfoTimeout=%d", int(conf.EntryTimeout.Seconds()*1000))
	options += ",VolumePrefix=/juicefs/" + conf.Format.Name
	if asRoot {
		options += ",uid=-1,gid=-1"
	}
	if fuseOpt != "" {
		options += "," + fuseOpt
	}
	if !showDotFiles {
		options += ",dothidden"
	}
	host.SetCapCaseInsensitive(!caseSensitive)
	host.SetCapReaddirPlus(true)
	logger.Debugf("mount point: %s, options: %s", conf.Meta.MountPoint, options)
	_ = host.Mount(conf.Meta.MountPoint, []string{"-o", options})
}

func RunAsSystemSerivce(name string, mountpoint string, logPath string) error {
	// https://winfsp.dev/doc/WinFsp-Service-Architecture/
	logger.Info("Running as Windows system service.")

	var cmds []string
	for _, v := range os.Args[1:] {
		if v == "-d" || v == "--background" {
			continue
		}
		cmds = append(cmds, v)
	}

	cmdLine := strings.Join(cmds, " ")

	regKeyPath := "SOFTWARE\\WOW6432Node\\WinFsp\\Services\\juicefs"
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regKeyPath, registry.ALL_ACCESS)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND || err == syscall.ERROR_PATH_NOT_FOUND {
			logger.Info("Registry key not found, create it")
			k, _, err = registry.CreateKey(registry.LOCAL_MACHINE, regKeyPath, registry.ALL_ACCESS)
			if err != nil {
				return fmt.Errorf("Failed to create registry key: %s", err)
			}
		} else {
			return fmt.Errorf("Failed to open registry key: %s", err)
		}
	}
	defer k.Close()

	err = k.SetStringValue("CommandLine", cmdLine)
	if err != nil {
		return fmt.Errorf("Failed to set registry key: %s", err)
	}

	securityDescriptor := "D:P(A;;RPWPLC;;;WD)"
	err = k.SetStringValue("Security", securityDescriptor)
	if err != nil {
		return fmt.Errorf("Failed to set registry key: %s", err)
	}

	filePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Failed to get current file path: %s", err)
	}

	err = k.SetStringValue("Executable", filePath)
	if err != nil {
		return fmt.Errorf("Failed to set registry key: %s", err)
	}

	err = k.SetDWordValue("JobControl", 1)
	if err != nil {
		return fmt.Errorf("Failed to set registry key: %s", err)
	}

	if logPath != "" {
		err = k.SetStringValue("Stderr", logPath)
		if err != nil {
			return fmt.Errorf("Failed to set registry key: %s", err)
		}
	} else {
		err = k.DeleteValue("Stderr")
		if err != nil {
			return fmt.Errorf("Failed to delete registry key: %s", err)
		}

	}

	logger.Debug("Starting juicefs service.")
	cmd := exec.Command("net", "use", mountpoint, "\\\\juicefs\\"+name)
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("Failed to call system command %s, %s", cmd.String(), err)
	}

	logger.Info("Juicefs system service started successfully.")
	return nil
}
