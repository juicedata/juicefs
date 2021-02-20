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
	fs       *fs.FileSystem
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
		return vfs.NewLogContext(meta.Background)
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
	j.fs.Meta().StatFS(ctx, &totalspace, &availspace, &iused, &iavail)
	var bsize uint64 = 0x10000
	if runtime.GOOS == "darwin" {
		if totalspace > 0x0001000000000000 {
			bsize = 0x20000
		}
	}
	blocks := totalspace / bsize
	bavail := availspace / bsize
	if runtime.GOOS == "darwin" {
		if totalspace/bsize > 0xFFFFFFFF {
			blocks = 0xFFFFFFFF
		}
		if availspace/bsize > 0xFFFFFFFF {
			used := (totalspace - availspace) / bsize
			if used >= blocks {
				bavail = 0
			} else {
				bavail = blocks - used
			}
		}
	}
	stat.Namemax = 255
	stat.Frsize = bsize
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
	_, errno := vfs.Mknod(ctx, parent.Inode(), path.Base(p), uint16(mode), 0, uint32(dev))
	e = -int(errno)
	return
}

// Mkdir creates a directory.
func (j *juice) Mkdir(path string, mode uint32) (e int) {
	ctx := j.newContext()
	defer trace(path, mode)(&e)
	e = errorconv(j.fs.Mkdir(ctx, path, uint16(mode)))
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
	_, errno := vfs.Symlink(ctx, target, parent.Inode(), path.Base(newpath))
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
	t, errno := vfs.Readlink(ctx, fi.Inode())
	e = -int(errno)
	target = string(t)
	return
}

// Rename renames a file.
func (j *juice) Rename(oldpath string, newpath string) (e int) {
	ctx := j.newContext()
	defer trace(oldpath, newpath)(&e)
	e = errorconv(j.fs.Rename(ctx, oldpath, newpath))
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
	entry, fh, errno := vfs.Create(ctx, parent.Inode(), path.Base(p), uint16(mode), 0, uint32(flags))
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

func cache_mode(mattr uint8) (bool, bool) {
	var direct_io, keep_cache bool
	return direct_io, keep_cache
}

// Open opens a file.
// The flags are a combination of the fuse.O_* constants.
func (j *juice) OpenEx(path string, fi *fuse.FileInfo_t) (e int) {
	ctx := j.newContext()
	defer trace(path, fi.Flags)(&e)
	f, err := j.fs.Open(ctx, path, 0)
	if err != 0 {
		e = -fuse.ENOENT
		return
	}
	entry, fh, errno := vfs.Open(ctx, f.Inode(), uint32(fi.Flags))
	if errno == 0 {
		fi.Fh = fh
		fi.DirectIo, fi.KeepCache = cache_mode(entry.Attr.Flags)
		if vfs.IsSpecialNode(f.Inode()) {
			fi.DirectIo = true
		}
		fi.NonSeekable = false
		j.Lock()
		j.handlers[fh] = f.Inode()
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
func (j *juice) Getattr(p string, stat *fuse.Stat_t, fh uint64) (e int) {
	ctx := j.newContext()
	defer trace(p, fh)(stat, &e)
	ino := j.h2i(&fh)
	if ino == 0 {
		fi, err := j.fs.Stat(ctx, p)
		if err != 0 {
			e = -fuse.ENOENT
			return
		}
		ino = fi.Inode()
	}
	entry, errrno := vfs.GetAttr(ctx, ino, 0)
	if errrno != 0 {
		e = -int(errrno)
		return
	}
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
	e = -int(vfs.Truncate(ctx, ino, size, 1, nil))
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
	n, err := vfs.Read(ctx, ino, buf, uint64(off), fh)
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
	errno := vfs.Write(ctx, ino, buff, uint64(off), fh)
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
	e = -int(vfs.Flush(ctx, ino, fh, 0))
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
		vfs.Release(j.newContext(), ino, fh)
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
		e = -int(vfs.Fsync(ctx, ino, 1, fh))
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
	fh, errno := vfs.Opendir(ctx, f.Inode())
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
	// TODO: read all
	ctx := j.newContext()
	entries, err := vfs.Readdir(ctx, ino, 10000, int(ofst), fh, true)
	if err != 0 {
		e = -int(err)
		return
	}
	var st fuse.Stat_t
	var ok bool
	for _, e := range entries {
		if e.Attr.Full {
			// vfs.UpdateEntry(e)
			attrToStat(e.Inode, e.Attr, &st)
			ok = fill(string(e.Name), &st, 0)
		} else {
			ok = fill(string(e.Name), nil, 0)
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
	e = -int(vfs.Releasedir(j.newContext(), ino, fh))
	return
}

func Serve(conf *vfs.Config, fs_ *fs.FileSystem, unc, fuseOpt string, fileCacheTo float64, asRoot, disk bool, delayClose int) error {
	if unc != "" {
		unc = strings.ReplaceAll(strings.ReplaceAll(unc, "\\", "/"), "//", "/")
	}

	var jfs juice
	jfs.conf = conf
	jfs.fs = fs_
	jfs.asRoot = asRoot
	jfs.delayClose = delayClose
	host := fuse.NewFileSystemHost(&jfs)
	var options = "volname=" + conf.Format.Name
	if runtime.GOOS == "windows" {
		options += ",ExactFileSystemName=JuiceFS,create_umask=022"
		options += ",DirInfoTimeout=1000,VolumeInfoTimeout=1000,KeepFileCache"
		options += ",ThreadCount=16"
		options += fmt.Sprintf(",FileInfoTimeout=%d", int(fileCacheTo*1000))
		if unc != "" {
			options += ",VolumePrefix=" + unc
		} else if !disk {
			options += ",VolumePrefix=/juicefs/" + conf.Format.Name
		}
	} else if runtime.GOOS == "darwin" {
		options += ",fstypename=JuiceFS,noappledouble,noapplexattr"
		options += ",allow_other,allow_recursion,daemon_timeout=5,default_permissions"
	}
	if asRoot {
		options += ",uid=-1,gid=-1"
	}
	if fuseOpt != "" {
		options += "," + fuseOpt
	}
	host.SetCapCaseInsensitive(runtime.GOOS == "windows" && strings.Contains(conf.Mountpoint, ":"))
	host.SetCapReaddirPlus(true)
	if host.Mount(conf.Mountpoint, []string{"-o", options}) {
		return fmt.Errorf("bad")
	}
	return nil
}
