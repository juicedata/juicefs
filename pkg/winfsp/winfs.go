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
	"path"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/billziss-gh/cgofuse/fuse"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

var logger = utils.GetLogger("juicefs")

type Ino = meta.Ino

func trace(vals ...interface{}) func(vals ...interface{}) {
	uid, gid, pid := fuse.Getcontext()
	return Trace(1, fmt.Sprintf("[uid=%v,gid=%v,pid=%d]", uid, gid, pid), vals...)
}

type juice struct {
	fuse.FileSystemBase
	sync.Mutex
	conf     *vfs.Config
	vfs      *vfs.VFS
	fs       *fs.FileSystem
	host     *fuse.FileSystemHost
	handlers map[uint64]meta.Ino
	badfd    map[uint64]uint64

	asRoot     bool
	delayClose int
}

// Init is called when the file system is created.
func (j *juice) Init() {
	j.handlers = make(map[uint64]meta.Ino)
	j.badfd = make(map[uint64]uint64)
}

func (j *juice) newContext() vfs.LogContext {
	if j.asRoot {
		return vfs.NewLogContext(meta.Background())
	}
	uid, gid, pid := fuse.Getcontext()
	if uid == 0xffffffff {
		uid = 0
	}
	if gid == 0xffffffff {
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
	return -int(err)
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
	e = -int(errno)
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
	ctx := j.newContext()
	defer trace(target, newpath)(&e)
	parent, err := j.fs.Open(ctx, path.Dir(newpath), 0)
	if err != 0 {
		e = errorconv(err)
		return
	}
	_, errno := j.vfs.Symlink(ctx, target, parent.Inode(), path.Base(newpath))
	e = -int(errno)
	return
}

func (j *juice) Readlink(path string) (e int, target string) {
	ctx := j.newContext()
	defer trace(path)(&e, &target)
	fi, err := j.fs.Stat(ctx, path)
	if err != 0 {
		e = errorconv(err)
		return
	}
	t, errno := j.vfs.Readlink(ctx, fi.Inode())
	e = -int(errno)
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
		e = errorconv(f.Utime(ctx, tmsp[0].Sec*1000+tmsp[0].Nsec/1e6, tmsp[1].Sec*1000+tmsp[1].Nsec/1e6))
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
	entry, fh, errno := j.vfs.Create(ctx, parent.Inode(), path.Base(p), uint16(mode), 0, uint32(flags))
	if errno == 0 {
		j.Lock()
		j.handlers[fh] = entry.Inode
		j.Unlock()
	}
	e = -int(errno)
	return
}

// Open opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) Open(path string, flags int) (e int, fh uint64) {
	var fi fuse.FileInfo_t
	fi.Flags = flags
	e = j.OpenEx(path, &fi)
	fh = fi.Fh
	return
}

// Open opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) OpenEx(path string, fi *fuse.FileInfo_t) (e int) {
	ctx := j.newContext()
	defer trace(path, fi.Flags)(&e)
	ino := meta.Ino(0)
	if strings.HasSuffix(path, "/.control") {
		ino, _ = vfs.GetInternalNodeByName(".control")
		if ino == 0 {
			e = -fuse.ENOENT
			return
		}
	} else {
		f, err := j.fs.Open(ctx, path, 0)
		if err != 0 {
			e = -fuse.ENOENT
			return
		}
		ino = f.Inode()
	}

	entry, fh, errno := j.vfs.Open(ctx, ino, uint32(fi.Flags))
	if errno == 0 {
		fi.Fh = fh
		if vfs.IsSpecialNode(ino) {
			fi.DirectIo = true
		} else {
			fi.KeepCache = entry.Attr.KeepCache
		}
		j.Lock()
		j.handlers[fh] = ino
		j.Unlock()
	}
	e = -int(errno)
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
}

func (j *juice) h2i(fh *uint64) meta.Ino {
	defer j.Unlock()
	j.Lock()
	ino := j.handlers[*fh]
	if ino == 0 {
		newfh := j.badfd[*fh]
		if newfh != 0 {
			ino = j.handlers[newfh]
			if ino > 0 {
				*fh = newfh
			}
		}
	}
	return ino
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
	return j.handlers[newfh]
}

// Getattr gets file attributes.
func (j *juice) getAttrForControlFile(ctx vfs.LogContext, p string, stat *fuse.Stat_t, fh uint64) (e int) {
	parentDir := path.Dir(p)
	_, err := j.fs.Stat(ctx, parentDir)
	if err != 0 {
		e = -fuse.ENOENT
		return
	}

	inode, attr := vfs.GetInternalNodeByName(".control")
	if inode == 0 {
		e = -fuse.ENOENT
		return
	}

	j.vfs.UpdateLength(inode, attr)
	attrToStat(inode, attr, stat)
	return
}

// Getattr gets file attributes.
func (j *juice) Getattr(p string, stat *fuse.Stat_t, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(p, fh)(stat, &e)
	ino := j.h2i(&fh)
	if ino == 0 {
		// special case for .control file
		if strings.HasSuffix(p, "/.control") {
			j.getAttrForControlFile(ctx, p, stat, fh)
			return
		}

		fi, err := j.fs.Stat(ctx, p)
		if err != 0 {
			e = -fuse.ENOENT
			return
		}
		ino = fi.Inode()
	}
	entry, errrno := j.vfs.GetAttr(ctx, ino, 0)
	if errrno != 0 {
		e = -int(errrno)
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
	e = -int(j.vfs.Truncate(ctx, ino, size, 0, nil))
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
		e = -int(err)
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
		e = -int(errno)
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
	e = -int(j.vfs.Flush(ctx, ino, fh, 0))
	return
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
		if orig != fh {
			delete(j.badfd, orig)
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
		e = -int(j.vfs.Fsync(ctx, ino, 1, fh))
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
		j.handlers[fh] = f.Inode()
		j.Unlock()
	}
	e = -int(errno)
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
		e = -int(err)
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
	j.Unlock()
	e = -int(j.vfs.Releasedir(j.newContext(), ino, fh))
	return
}

func Serve(v *vfs.VFS, fuseOpt string, fileCacheTo float64, asRoot bool, delayClose int) {
	var jfs juice
	conf := v.Conf
	jfs.conf = conf
	jfs.vfs = v
	var err error
	jfs.fs, err = fs.NewFileSystem(conf, v.Meta, v.Store)
	if err != nil {
		logger.Fatalf("Initialize FileSystem failed: %s", err)
	}
	jfs.asRoot = asRoot
	jfs.delayClose = delayClose
	host := fuse.NewFileSystemHost(&jfs)
	jfs.host = host
	var options = "volname=" + conf.Format.Name
	// create_umask 022 results in 755 for directories and 644 for files, see https://github.com/winfsp/sshfs-win/issues/14
	options += ",ExactFileSystemName=JuiceFS,create_umask=022,ThreadCount=16"
	options += ",DirInfoTimeout=1000,VolumeInfoTimeout=1000,KeepFileCache"
	options += fmt.Sprintf(",FileInfoTimeout=%d", int(fileCacheTo*1000))
	options += ",VolumePrefix=/juicefs/" + conf.Format.Name
	if asRoot {
		options += ",uid=-1,gid=-1"
	}
	if fuseOpt != "" {
		options += "," + fuseOpt
	}
	host.SetCapCaseInsensitive(strings.HasSuffix(conf.Meta.MountPoint, ":"))
	host.SetCapReaddirPlus(true)
	logger.Debugf("mount point: %s, options: %s", conf.Meta.MountPoint, options)
	_ = host.Mount(conf.Meta.MountPoint, []string{"-o", options})
}
