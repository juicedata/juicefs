//go:build darwin

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

package fusemac

import (
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/winfsp/cgofuse/fuse"
)

const fhUnset = ^uint64(0)

// ufImmutable and ufAppend are the native BSD flags (UF_IMMUTABLE, UF_APPEND)
// the kernel sends to Chflags and expects back in stat. UF_READONLY is also
// accepted as input for backwards compatibility.
const (
	ufImmutable = 0x2
	ufAppend    = 0x4
)

var logger = utils.GetLogger("juicefs")

var (
	_ fuse.FileSystemInterface = (*FS)(nil)
	_ fuse.FileSystemOpenEx    = (*FS)(nil)
)

// FS is the POSIX filesystem on top of vfs.VFS.
type FS struct {
	fuse.FileSystemBase
	sync.RWMutex
	vfs              *vfs.VFS
	conf             *vfs.Config
	host             *fuse.FileSystemHost
	handles          map[uint64]meta.Ino
	asRoot           bool
	xattrs           bool
	disableSymlink   bool
	readdirBatchSize int
	destroyed        atomic.Int32
}

func NewFS(conf *vfs.Config, v *vfs.VFS) *FS {
	jfs := &FS{
		vfs:              v,
		conf:             conf,
		handles:          make(map[uint64]meta.Ino),
		readdirBatchSize: 1024,
	}
	return jfs
}

func (fsys *FS) newContext() vfs.LogContext {
	if fsys.asRoot {
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
	if uid == 0 && fsys.conf != nil && fsys.conf.RootSquash != nil {
		uid = fsys.conf.RootSquash.Uid
		gid = fsys.conf.RootSquash.Gid
	}
	if fsys.conf != nil && fsys.conf.AllSquash != nil {
		uid = fsys.conf.AllSquash.Uid
		gid = fsys.conf.AllSquash.Gid
	}
	ctx := meta.NewContext(uint32(pid), uid, []uint32{gid})
	return vfs.NewLogContext(ctx)
}

func (fsys *FS) Init() {
	fsys.Lock()
	defer fsys.Unlock()
	if fsys.handles == nil {
		fsys.handles = make(map[uint64]meta.Ino)
	}
}

func (fsys *FS) Destroy() {
	// cgofuse calls Destroy only after the session ends, so once this flag is
	// set the mount is already gone and unmounting again would just race teardown.
	fsys.destroyed.Store(1)
	fsys.Lock()
	defer fsys.Unlock()
	fsys.handles = nil
}

func errorconv(err syscall.Errno) int {
	if err == 0 {
		return 0
	}
	switch err {
	case syscall.EPERM:
		return -fuse.EPERM
	case syscall.ENOENT:
		return -fuse.ENOENT
	case syscall.ESRCH:
		return -fuse.ESRCH
	case syscall.EINTR:
		return -fuse.EINTR
	case syscall.EIO:
		return -fuse.EIO
	case syscall.ENXIO:
		return -fuse.ENXIO
	case syscall.E2BIG:
		return -fuse.E2BIG
	case syscall.ENOEXEC:
		return -fuse.ENOEXEC
	case syscall.EBADF:
		return -fuse.EBADF
	case syscall.ECHILD:
		return -fuse.ECHILD
	case syscall.EAGAIN:
		return -fuse.EAGAIN
	case syscall.ENOMEM:
		return -fuse.ENOMEM
	case syscall.EACCES:
		return -fuse.EACCES
	case syscall.EFAULT:
		return -fuse.EFAULT
	case syscall.EBUSY:
		return -fuse.EBUSY
	case syscall.EEXIST:
		return -fuse.EEXIST
	case syscall.EXDEV:
		return -fuse.EXDEV
	case syscall.ENODEV:
		return -fuse.ENODEV
	case syscall.ENOTDIR:
		return -fuse.ENOTDIR
	case syscall.EISDIR:
		return -fuse.EISDIR
	case syscall.EINVAL:
		return -fuse.EINVAL
	case syscall.ENFILE:
		return -fuse.ENFILE
	case syscall.EMFILE:
		return -fuse.EMFILE
	case syscall.ENOTTY:
		return -fuse.ENOTTY
	case syscall.EFBIG:
		return -fuse.EFBIG
	case syscall.ENOSPC:
		return -fuse.ENOSPC
	case syscall.ESPIPE:
		return -fuse.ESPIPE
	case syscall.EROFS:
		return -fuse.EROFS
	case syscall.EMLINK:
		return -fuse.EMLINK
	case syscall.EPIPE:
		return -fuse.EPIPE
	case syscall.ENAMETOOLONG:
		return -fuse.ENAMETOOLONG
	case syscall.ENOSYS:
		return -fuse.ENOSYS
	case syscall.ENOTEMPTY:
		return -fuse.ENOTEMPTY
	case syscall.ELOOP:
		return -fuse.ELOOP
	case syscall.ENODATA:
		return -fuse.ENODATA
	case syscall.ENOATTR: // Darwin alias of ENODATA
		return -fuse.ENODATA
	default:
		return -int(err)
	}
}

func fuseFlagToSyscall(flag int) int {
	var ret int
	if flag&fuse.O_WRONLY != 0 {
		ret |= syscall.O_WRONLY
	} else if flag&fuse.O_RDWR != 0 {
		ret |= syscall.O_RDWR
	} else {
		ret |= syscall.O_RDONLY
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

func (fsys *FS) attrToStat(inode meta.Ino, attr *meta.Attr, stat *fuse.Stat_t) {
	*stat = fuse.Stat_t{}
	stat.Ino = uint64(inode)
	stat.Mode = attr.SMode()
	stat.Uid = attr.Uid
	stat.Gid = attr.Gid
	stat.Nlink = attr.Nlink
	stat.Atim = fuse.Timespec{Sec: attr.Atime, Nsec: int64(attr.Atimensec)}
	stat.Mtim = fuse.Timespec{Sec: attr.Mtime, Nsec: int64(attr.Mtimensec)}
	stat.Ctim = fuse.Timespec{Sec: attr.Ctime, Nsec: int64(attr.Ctimensec)}
	stat.Birthtim = stat.Ctim

	var rdev uint32
	var size, blocks uint64
	switch attr.Typ {
	case meta.TypeDirectory, meta.TypeSymlink, meta.TypeFile:
		size = attr.Length
		blocks = (size + 511) / 512
		stat.Blksize = 4096
	case meta.TypeBlockDev, meta.TypeCharDev:
		rdev = attr.Rdev
	}
	stat.Size = int64(size)
	stat.Blocks = int64(blocks)
	stat.Rdev = uint64(rdev)
	if attr.Flags&meta.FlagImmutable != 0 {
		stat.Flags |= ufImmutable
	}
	if attr.Flags&meta.FlagAppend != 0 {
		stat.Flags |= ufAppend
	}
}

// resolvePath walks p component by component from the root.
func (fsys *FS) resolvePath(ctx vfs.LogContext, p string) (meta.Ino, *meta.Entry, syscall.Errno) {
	p = path.Clean(p)
	if p == "/" || p == "." || p == "" {
		entry, err := fsys.vfs.GetAttr(ctx, meta.RootInode, 0)
		return meta.RootInode, entry, err
	}

	base := path.Base(p)
	if path.Dir(p) == "/" && vfs.IsSpecialName(base) {
		ino, attr := vfs.GetInternalNodeByName(base)
		if ino == 0 {
			return 0, nil, syscall.ENOENT
		}
		return ino, &meta.Entry{Inode: ino, Attr: attr}, 0
	}

	currentIno := meta.RootInode
	var currentEntry *meta.Entry
	var err syscall.Errno

	parts := strings.SplitSeq(strings.Trim(p, "/"), "/")
	for seg := range parts {
		if seg == "" || seg == "." {
			continue
		}
		currentEntry, err = fsys.vfs.Lookup(ctx, currentIno, seg)
		if err != 0 {
			return 0, nil, err
		}
		currentIno = currentEntry.Inode
	}

	return currentIno, currentEntry, 0
}

// resolveParent returns the parent inode and the last path component.
func (fsys *FS) resolveParent(ctx vfs.LogContext, p string) (meta.Ino, string, syscall.Errno) {
	p = path.Clean(p)
	if p == "/" || p == "." || p == "" {
		return 0, "", syscall.EINVAL
	}
	dir, base := path.Split(p)
	parentIno, _, err := fsys.resolvePath(ctx, dir)
	if err != 0 {
		return 0, "", err
	}
	return parentIno, base, 0
}

// resolveIno returns the inode for a path, preferring the open handle fh.
// opened is 1 when the inode came from a handle; useFh is the handle to pass
// on to the vfs layer (0 when the path was resolved).
func (fsys *FS) resolveIno(ctx vfs.LogContext, p string, fh uint64) (ino meta.Ino, opened uint8, useFh uint64, err syscall.Errno) {
	if fh != 0 && fh != fhUnset {
		fsys.RLock()
		ino = fsys.handles[fh]
		fsys.RUnlock()
		if ino != 0 {
			return ino, 1, fh, 0
		}
	}
	ino, _, err = fsys.resolvePath(ctx, p)
	return ino, 0, 0, err
}

func (fsys *FS) Statfs(p string, stat *fuse.Statfs_t) int {
	ctx := fsys.newContext()
	var totalspace, availspace, iused, iavail uint64
	if errno := fsys.vfs.Meta.StatFS(ctx, meta.RootInode, &totalspace, &availspace, &iused, &iavail); errno != 0 {
		return errorconv(errno)
	}
	var bsize uint64 = 4096
	blocks := totalspace / bsize
	bavail := availspace / bsize
	// FUSE-T/FSKit only keep 32 bits of the block counts, so cap them before
	// large capacities wrap to zero or negative (df would complain otherwise).
	const maxBlocks = 1<<32 - 1
	if blocks > maxBlocks {
		blocks = maxBlocks
	}
	if bavail > maxBlocks {
		bavail = maxBlocks
	}
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

func (fsys *FS) Mknod(p string, mode uint32, dev uint64) int {
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	_, errno := fsys.vfs.Mknod(ctx, parentIno, base, uint16(mode), 0, uint32(dev))
	return errorconv(errno)
}

func (fsys *FS) Mkdir(p string, mode uint32) int {
	if p == "/.UMOUNTIT" {
		if fsys.conf != nil {
			logger.Infof("Umount %s ...", fsys.conf.Meta.MountPoint)
		}
		if fsys.host != nil {
			go fsys.host.Unmount()
		}
		return -fuse.ENOENT
	}
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	_, errno := fsys.vfs.Mkdir(ctx, parentIno, base, uint16(mode), 0)
	return errorconv(errno)
}

func (fsys *FS) Unlink(p string) int {
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.Unlink(ctx, parentIno, base))
}

func (fsys *FS) Rmdir(p string) int {
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.Rmdir(ctx, parentIno, base))
}

func (fsys *FS) Link(oldpath string, newpath string) int {
	ctx := fsys.newContext()
	oldIno, _, err := fsys.resolvePath(ctx, oldpath)
	if err != 0 {
		return errorconv(err)
	}
	newParentIno, newBase, err := fsys.resolveParent(ctx, newpath)
	if err != 0 {
		return errorconv(err)
	}
	_, errno := fsys.vfs.Link(ctx, oldIno, newParentIno, newBase)
	return errorconv(errno)
}

func (fsys *FS) Symlink(target string, newpath string) int {
	if fsys.disableSymlink {
		return -fuse.ENOSYS
	}
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, newpath)
	if err != 0 {
		return errorconv(err)
	}
	_, errno := fsys.vfs.Symlink(ctx, target, parentIno, base)
	return errorconv(errno)
}

func (fsys *FS) Readlink(p string) (int, string) {
	if p == "/" && fsys.disableSymlink {
		return -fuse.ENOSYS, ""
	}
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err), ""
	}
	t, errno := fsys.vfs.Readlink(ctx, ino)
	return errorconv(errno), string(t)
}

func (fsys *FS) Rename(oldpath string, newpath string) int {
	ctx := fsys.newContext()
	oldParentIno, oldBase, err := fsys.resolveParent(ctx, oldpath)
	if err != 0 {
		return errorconv(err)
	}
	newParentIno, newBase, err := fsys.resolveParent(ctx, newpath)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.Rename(ctx, oldParentIno, oldBase, newParentIno, newBase, 0))
}

func (fsys *FS) Chmod(p string, mode uint32) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	_, errno := fsys.vfs.SetAttr(ctx, ino, meta.SetAttrMode, 0, mode, 0, 0, 0, 0, 0, 0, 0)
	return errorconv(errno)
}

func (fsys *FS) Chown(p string, uid uint32, gid uint32) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	set := 0
	if uid != 0xffffffff {
		set |= meta.SetAttrUID
	}
	if gid != 0xffffffff {
		set |= meta.SetAttrGID
	}
	_, errno := fsys.vfs.SetAttr(ctx, ino, set, 0, 0, uid, gid, 0, 0, 0, 0, 0)
	return errorconv(errno)
}

func (fsys *FS) Utimens(p string, tmsp []fuse.Timespec) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	if len(tmsp) < 2 {
		return 0
	}
	// cgofuse leaves UTIME_NOW/UTIME_OMIT in the nsec field; translate them
	// into the matching SetAttr flags instead of passing the sentinel down.
	var set int
	var atime, mtime int64
	var atimensec, mtimensec uint32
	switch tmsp[0].Nsec {
	case fuse.UTIME_NOW:
		set |= meta.SetAttrAtimeNow
	case fuse.UTIME_OMIT:
	default:
		set |= meta.SetAttrAtime
		atime = tmsp[0].Sec
		atimensec = uint32(tmsp[0].Nsec)
	}
	switch tmsp[1].Nsec {
	case fuse.UTIME_NOW:
		set |= meta.SetAttrMtimeNow
	case fuse.UTIME_OMIT:
	default:
		set |= meta.SetAttrMtime
		mtime = tmsp[1].Sec
		mtimensec = uint32(tmsp[1].Nsec)
	}
	if set == 0 {
		return 0
	}
	_, errno := fsys.vfs.SetAttr(ctx, ino, set, 0, 0, 0, 0, atime, mtime, atimensec, mtimensec, 0)
	return errorconv(errno)
}

// Chflags mirrors chflags(2): only immutable and append are supported, and
// only the owner (or root) may change them.
func (fsys *FS) Chflags(p string, flags uint32) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	if vfs.IsSpecialNode(ino) {
		return -fuse.EPERM
	}

	if flags&^(fuse.UF_READONLY|ufImmutable|ufAppend) != 0 {
		return -fuse.ENOTSUP
	}

	var cur meta.Attr
	if errno := fsys.vfs.Meta.GetAttr(ctx, ino, &cur); errno != 0 {
		return errorconv(errno)
	}
	if ctx.CheckPermission() && ctx.Uid() != 0 && ctx.Uid() != cur.Uid {
		return -fuse.EPERM
	}

	newFlags := cur.Flags &^ (uint8(meta.FlagImmutable) | uint8(meta.FlagAppend))
	if flags&(fuse.UF_READONLY|ufImmutable) != 0 {
		newFlags |= meta.FlagImmutable
	}
	if flags&ufAppend != 0 {
		newFlags |= meta.FlagAppend
	}
	return errorconv(fsys.vfs.Meta.SetAttr(ctx, ino, meta.SetAttrFlag, 0, &meta.Attr{Flags: newFlags}))
}

func (fsys *FS) Access(p string, mask uint32) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.Access(ctx, ino, int(mask)))
}

func (fsys *FS) Create(p string, flags int, mode uint32) (int, uint64) {
	var fi fuse.FileInfo_t
	fi.Flags = flags
	errc := fsys.CreateEx(p, mode, &fi)
	return errc, fi.Fh
}

func (fsys *FS) CreateEx(p string, mode uint32, fi *fuse.FileInfo_t) int {
	ctx := fsys.newContext()
	parentIno, base, err := fsys.resolveParent(ctx, p)
	if err != 0 {
		return errorconv(err)
	}

	entry, fh, errno := fsys.vfs.Create(ctx, parentIno, base, uint16(mode), 0, uint32(fuseFlagToSyscall(fi.Flags)))
	if errno == 0 {
		fi.Fh = fh
		if vfs.IsSpecialNode(entry.Inode) {
			fi.DirectIo = true
		} else {
			fi.KeepCache = entry.Attr.KeepCache
		}
		fsys.Lock()
		fsys.handles[fh] = entry.Inode
		fsys.Unlock()
	}
	return errorconv(errno)
}

func (fsys *FS) Open(p string, flags int) (int, uint64) {
	var fi fuse.FileInfo_t
	fi.Flags = flags
	e := fsys.OpenEx(p, &fi)
	return e, fi.Fh
}

func (fsys *FS) OpenEx(p string, fi *fuse.FileInfo_t) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}

	entry, fh, errno := fsys.vfs.Open(ctx, ino, uint32(fuseFlagToSyscall(fi.Flags)))
	if errno == 0 {
		fi.Fh = fh
		if vfs.IsSpecialNode(ino) {
			fi.DirectIo = true
		} else {
			fi.KeepCache = entry.Attr.KeepCache
		}
		fsys.Lock()
		fsys.handles[fh] = ino
		fsys.Unlock()
	}
	return errorconv(errno)
}

func (fsys *FS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	ctx := fsys.newContext()
	ino, opened, _, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	entry, errno := fsys.vfs.GetAttr(ctx, ino, opened)
	if errno != 0 {
		return errorconv(errno)
	}

	fsys.vfs.UpdateLength(entry.Inode, entry.Attr)
	fsys.attrToStat(entry.Inode, entry.Attr, stat)
	return 0
}

func (fsys *FS) Truncate(p string, size int64, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, useFh, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	return errorconv(fsys.vfs.Truncate(ctx, ino, size, useFh, nil))
}

func (fsys *FS) Read(p string, buff []byte, ofst int64, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, useFh, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	n, err := fsys.vfs.Read(ctx, ino, buff, uint64(ofst), useFh)
	if err != 0 {
		return errorconv(err)
	}
	return n
}

func (fsys *FS) Write(p string, buff []byte, ofst int64, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, useFh, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	errno := fsys.vfs.Write(ctx, ino, buff, uint64(ofst), useFh)
	if errno != 0 {
		return errorconv(errno)
	}
	return len(buff)
}

func (fsys *FS) Flush(p string, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, useFh, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	return errorconv(fsys.vfs.Flush(ctx, ino, useFh, 0))
}

func (fsys *FS) Release(p string, fh uint64) int {
	fsys.Lock()
	ino, ok := fsys.handles[fh]
	if ok {
		delete(fsys.handles, fh)
	}
	fsys.Unlock()

	if !ok {
		ino, _, _ = fsys.resolvePath(fsys.newContext(), p)
	}

	if ino != 0 {
		fsys.vfs.Release(fsys.newContext(), ino, fh)
	}
	return 0
}

func (fsys *FS) Fsync(p string, datasync bool, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, useFh, err := fsys.resolveIno(ctx, p, fh)
	if err != 0 {
		return errorconv(err)
	}

	syncFlag := 0
	if datasync {
		syncFlag = 1
	}
	return errorconv(fsys.vfs.Fsync(ctx, ino, syncFlag, useFh))
}

func (fsys *FS) Opendir(p string) (int, uint64) {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err), 0
	}
	fh, errno := fsys.vfs.Opendir(ctx, ino, 0)
	if errno == 0 {
		fsys.Lock()
		fsys.handles[fh] = ino
		fsys.Unlock()
	}
	return errorconv(errno), fh
}

func (fsys *FS) Readdir(p string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}

	// Directories can't be seeked; reject a non-zero offset.
	if ofst > 0 {
		return -fuse.ESPIPE
	}

	// FSKit calls Readdir without Opendir first, so the host fh isn't a valid
	// vfs handle; open one for the whole listing instead.
	dirfh, errno := fsys.vfs.Opendir(ctx, ino, 0)
	if errno != 0 {
		return errorconv(errno)
	}
	defer fsys.vfs.Releasedir(ctx, ino, dirfh)

	// FUSE-T/NFS expects "." and ".." to be present.
	if !fill(".", nil, 0) || !fill("..", nil, 0) {
		return 0
	}

	// Read everything in one pass, always with offset 0, so the host can't
	// resume in the middle.
	batchSize := fsys.readdirBatchSize
	if batchSize <= 0 {
		batchSize = 1024
	}

	currentOffset := 0
	for {
		entries, readAt, err := fsys.vfs.Readdir(ctx, ino, uint32(batchSize), currentOffset, dirfh, true)
		if err != 0 {
			return errorconv(err)
		}
		if len(entries) == 0 {
			break
		}

		ok := true
		for _, entry := range entries {
			name := string(entry.Name)
			if name == "." || name == ".." {
				continue
			}
			var st fuse.Stat_t
			if fsys.vfs.ModifiedSince(entry.Inode, readAt) {
				if e2, err := fsys.vfs.GetAttr(ctx, entry.Inode, 0); err == 0 {
					entry.Attr = e2.Attr
				}
			}
			fsys.vfs.UpdateLength(entry.Inode, entry.Attr)
			fsys.attrToStat(entry.Inode, entry.Attr, &st)
			ok = fill(name, &st, 0)
			if !ok {
				break
			}
		}
		if !ok {
			break
		}
		currentOffset += len(entries)
	}
	return 0
}

func (fsys *FS) Releasedir(p string, fh uint64) int {
	fsys.Lock()
	ino, ok := fsys.handles[fh]
	if ok {
		delete(fsys.handles, fh)
	}
	fsys.Unlock()

	if !ok {
		ino, _, _ = fsys.resolvePath(fsys.newContext(), p)
	}

	if ino != 0 {
		return fsys.vfs.Releasedir(fsys.newContext(), ino, fh)
	}
	return 0
}

func (fsys *FS) Fsyncdir(p string, datasync bool, fh uint64) int {
	return 0
}

func (fsys *FS) Setxattr(p string, name string, value []byte, flags int) int {
	if !fsys.xattrs {
		return -fuse.ENOSYS
	}
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.SetXattr(ctx, ino, name, value, uint32(flags)))
}

func (fsys *FS) Getxattr(p string, name string) (int, []byte) {
	if !fsys.xattrs {
		return -fuse.ENOSYS, nil
	}
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err), nil
	}
	val, errno := fsys.vfs.GetXattr(ctx, ino, name, 0)
	if errno != 0 {
		return errorconv(errno), nil
	}
	return 0, val
}

func (fsys *FS) Removexattr(p string, name string) int {
	if !fsys.xattrs {
		return -fuse.ENOSYS
	}
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	return errorconv(fsys.vfs.RemoveXattr(ctx, ino, name))
}

func (fsys *FS) Listxattr(p string, fill func(name string) bool) int {
	if !fsys.xattrs {
		return -fuse.ENOSYS
	}
	ctx := fsys.newContext()
	ino, _, err := fsys.resolvePath(ctx, p)
	if err != 0 {
		return errorconv(err)
	}
	data, errno := fsys.vfs.ListXattr(ctx, ino, 0)
	if errno != 0 {
		return errorconv(errno)
	}
	start := 0
	for i := range data {
		if data[i] == 0 {
			if i > start {
				if !fill(string(data[start:i])) {
					break
				}
			}
			start = i + 1
		}
	}
	return 0
}
