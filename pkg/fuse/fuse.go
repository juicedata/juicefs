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

package fuse

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

var logger = utils.GetLogger("juicefs")

type fileSystem struct {
	fuse.RawFileSystem
	conf *vfs.Config
	v    *vfs.VFS
}

func newFileSystem(conf *vfs.Config, v *vfs.VFS) *fileSystem {
	return &fileSystem{
		RawFileSystem: fuse.NewDefaultRawFileSystem(),
		conf:          conf,
		v:             v,
	}
}

func (fs *fileSystem) replyEntry(out *fuse.EntryOut, e *meta.Entry) fuse.Status {
	out.NodeId = uint64(e.Inode)
	out.Generation = 1
	out.SetAttrTimeout(fs.conf.AttrTimeout)
	if e.Attr.Typ == meta.TypeDirectory {
		out.SetEntryTimeout(fs.conf.DirEntryTimeout)
	} else {
		out.SetEntryTimeout(fs.conf.EntryTimeout)
	}
	if vfs.IsSpecialNode(e.Inode) {
		out.SetAttrTimeout(time.Hour)
	}
	attrToStat(e.Inode, e.Attr, &out.Attr)
	return 0
}

func (fs *fileSystem) Lookup(cancel <-chan struct{}, header *fuse.InHeader, name string, out *fuse.EntryOut) (status fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	entry, err := fs.v.Lookup(ctx, Ino(header.NodeId), name)
	if err != 0 {
		return fuse.Status(err)
	}
	return fs.replyEntry(out, entry)
}

func (fs *fileSystem) GetAttr(cancel <-chan struct{}, in *fuse.GetAttrIn, out *fuse.AttrOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	var opened uint8
	if in.Fh() != 0 {
		opened = 1
	}
	entry, err := fs.v.GetAttr(ctx, Ino(in.NodeId), opened)
	if err != 0 {
		return fuse.Status(err)
	}
	attrToStat(entry.Inode, entry.Attr, &out.Attr)
	out.AttrValid = uint64(fs.conf.AttrTimeout.Seconds())
	if vfs.IsSpecialNode(Ino(in.NodeId)) {
		out.AttrValid = 3600
	}
	return 0
}

func (fs *fileSystem) SetAttr(cancel <-chan struct{}, in *fuse.SetAttrIn, out *fuse.AttrOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	var opened uint8
	if in.Fh != 0 {
		opened = 1
	}
	entry, err := fs.v.SetAttr(ctx, Ino(in.NodeId), int(in.Valid), opened, in.Mode, in.Uid, in.Gid, int64(in.Atime), int64(in.Mtime), in.Atimensec, in.Mtimensec, in.Size)
	if err != 0 {
		return fuse.Status(err)
	}
	out.AttrValid = uint64(fs.conf.AttrTimeout.Seconds())
	if vfs.IsSpecialNode(entry.Inode) {
		out.AttrValid = 3600
	}
	attrToStat(entry.Inode, entry.Attr, &out.Attr)
	return 0
}

func (fs *fileSystem) Mknod(cancel <-chan struct{}, in *fuse.MknodIn, name string, out *fuse.EntryOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entry, err := fs.v.Mknod(ctx, Ino(in.NodeId), name, uint16(in.Mode), getUmask(in), in.Rdev)
	if err != 0 {
		return fuse.Status(err)
	}
	return fs.replyEntry(out, entry)
}

func (fs *fileSystem) Mkdir(cancel <-chan struct{}, in *fuse.MkdirIn, name string, out *fuse.EntryOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entry, err := fs.v.Mkdir(ctx, Ino(in.NodeId), name, uint16(in.Mode), uint16(in.Umask))
	if err != 0 {
		return fuse.Status(err)
	}
	return fs.replyEntry(out, entry)
}

func (fs *fileSystem) Unlink(cancel <-chan struct{}, header *fuse.InHeader, name string) (code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	err := fs.v.Unlink(ctx, Ino(header.NodeId), name)
	return fuse.Status(err)
}

func (fs *fileSystem) Rmdir(cancel <-chan struct{}, header *fuse.InHeader, name string) (code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	err := fs.v.Rmdir(ctx, Ino(header.NodeId), name)
	return fuse.Status(err)
}

func (fs *fileSystem) Rename(cancel <-chan struct{}, in *fuse.RenameIn, oldName string, newName string) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Rename(ctx, Ino(in.NodeId), oldName, Ino(in.Newdir), newName, in.Flags)
	return fuse.Status(err)
}

func (fs *fileSystem) Link(cancel <-chan struct{}, in *fuse.LinkIn, name string, out *fuse.EntryOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entry, err := fs.v.Link(ctx, Ino(in.Oldnodeid), Ino(in.NodeId), name)
	if err != 0 {
		return fuse.Status(err)
	}
	return fs.replyEntry(out, entry)
}

func (fs *fileSystem) Symlink(cancel <-chan struct{}, header *fuse.InHeader, target string, name string, out *fuse.EntryOut) (code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	entry, err := fs.v.Symlink(ctx, target, Ino(header.NodeId), name)
	if err != 0 {
		return fuse.Status(err)
	}
	return fs.replyEntry(out, entry)
}

func (fs *fileSystem) Readlink(cancel <-chan struct{}, header *fuse.InHeader) (out []byte, code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	path, err := fs.v.Readlink(ctx, Ino(header.NodeId))
	return path, fuse.Status(err)
}

func (fs *fileSystem) GetXAttr(cancel <-chan struct{}, header *fuse.InHeader, attr string, dest []byte) (sz uint32, code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	value, err := fs.v.GetXattr(ctx, Ino(header.NodeId), attr, uint32(len(dest)))
	if err != 0 {
		return 0, fuse.Status(err)
	}
	copy(dest, value)
	return uint32(len(value)), 0
}

func (fs *fileSystem) ListXAttr(cancel <-chan struct{}, header *fuse.InHeader, dest []byte) (uint32, fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	data, err := fs.v.ListXattr(ctx, Ino(header.NodeId), len(dest))
	if err != 0 {
		return 0, fuse.Status(err)
	}
	copy(dest, data)
	return uint32(len(data)), 0
}

func (fs *fileSystem) SetXAttr(cancel <-chan struct{}, in *fuse.SetXAttrIn, attr string, data []byte) fuse.Status {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.SetXattr(ctx, Ino(in.NodeId), attr, data, in.Flags)
	return fuse.Status(err)
}

func (fs *fileSystem) RemoveXAttr(cancel <-chan struct{}, header *fuse.InHeader, attr string) (code fuse.Status) {
	ctx := newContext(cancel, header)
	defer releaseContext(ctx)
	err := fs.v.RemoveXattr(ctx, Ino(header.NodeId), attr)
	return fuse.Status(err)
}

func (fs *fileSystem) Create(cancel <-chan struct{}, in *fuse.CreateIn, name string, out *fuse.CreateOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entry, fh, err := fs.v.Create(ctx, Ino(in.NodeId), name, uint16(in.Mode), 0, in.Flags)
	if err != 0 {
		return fuse.Status(err)
	}
	out.Fh = fh
	return fs.replyEntry(&out.EntryOut, entry)
}

func (fs *fileSystem) Open(cancel <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) (status fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entry, fh, err := fs.v.Open(ctx, Ino(in.NodeId), in.Flags)
	if err != 0 {
		return fuse.Status(err)
	}
	out.Fh = fh
	if vfs.IsSpecialNode(Ino(in.NodeId)) {
		out.OpenFlags |= fuse.FOPEN_DIRECT_IO
	} else if entry.Attr.KeepCache {
		out.OpenFlags |= fuse.FOPEN_KEEP_CACHE
	}
	return 0
}

func (fs *fileSystem) Read(cancel <-chan struct{}, in *fuse.ReadIn, buf []byte) (fuse.ReadResult, fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	n, err := fs.v.Read(ctx, Ino(in.NodeId), buf, in.Offset, in.Fh)
	if err != 0 {
		return nil, fuse.Status(err)
	}
	return fuse.ReadResultData(buf[:n]), 0
}

func (fs *fileSystem) Release(cancel <-chan struct{}, in *fuse.ReleaseIn) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	fs.v.Release(ctx, Ino(in.NodeId), in.Fh)
}

func (fs *fileSystem) Write(cancel <-chan struct{}, in *fuse.WriteIn, data []byte) (written uint32, code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Write(ctx, Ino(in.NodeId), data, in.Offset, in.Fh)
	if err != 0 {
		return 0, fuse.Status(err)
	}
	return uint32(len(data)), 0
}

func (fs *fileSystem) Flush(cancel <-chan struct{}, in *fuse.FlushIn) fuse.Status {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Flush(ctx, Ino(in.NodeId), in.Fh, in.LockOwner)
	return fuse.Status(err)
}

func (fs *fileSystem) Fsync(cancel <-chan struct{}, in *fuse.FsyncIn) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Fsync(ctx, Ino(in.NodeId), int(in.FsyncFlags), in.Fh)
	return fuse.Status(err)
}

func (fs *fileSystem) Fallocate(cancel <-chan struct{}, in *fuse.FallocateIn) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Fallocate(ctx, Ino(in.NodeId), uint8(in.Mode), int64(in.Offset), int64(in.Length), in.Fh)
	return fuse.Status(err)
}

func (fs *fileSystem) CopyFileRange(cancel <-chan struct{}, in *fuse.CopyFileRangeIn) (written uint32, code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	copied, err := fs.v.CopyFileRange(ctx, Ino(in.NodeId), in.FhIn, in.OffIn, Ino(in.NodeIdOut), in.FhOut, in.OffOut, in.Len, uint32(in.Flags))
	if err != 0 {
		return 0, fuse.Status(err)
	}
	return uint32(copied), 0
}

func (fs *fileSystem) GetLk(cancel <-chan struct{}, in *fuse.LkIn, out *fuse.LkOut) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	l := in.Lk
	err := fs.v.Getlk(ctx, Ino(in.NodeId), in.Fh, in.Owner, &l.Start, &l.End, &l.Typ, &l.Pid)
	if err == 0 {
		out.Lk = l
	}
	return fuse.Status(err)
}

func (fs *fileSystem) SetLk(cancel <-chan struct{}, in *fuse.LkIn) (code fuse.Status) {
	return fs.setLk(cancel, in, false)
}

func (fs *fileSystem) SetLkw(cancel <-chan struct{}, in *fuse.LkIn) (code fuse.Status) {
	return fs.setLk(cancel, in, true)
}

func (fs *fileSystem) setLk(cancel <-chan struct{}, in *fuse.LkIn, block bool) (code fuse.Status) {
	if in.LkFlags&fuse.FUSE_LK_FLOCK != 0 {
		return fs.Flock(cancel, in, block)
	}
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	l := in.Lk
	err := fs.v.Setlk(ctx, Ino(in.NodeId), in.Fh, in.Owner, l.Start, l.End, l.Typ, l.Pid, block)
	return fuse.Status(err)
}

func (fs *fileSystem) Flock(cancel <-chan struct{}, in *fuse.LkIn, block bool) (code fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	err := fs.v.Flock(ctx, Ino(in.NodeId), in.Fh, in.Owner, in.Lk.Typ, block)
	return fuse.Status(err)
}

func (fs *fileSystem) OpenDir(cancel <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) (status fuse.Status) {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	fh, err := fs.v.Opendir(ctx, Ino(in.NodeId))
	out.Fh = fh
	return fuse.Status(err)
}

func (fs *fileSystem) ReadDir(cancel <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entries, err := fs.v.Readdir(ctx, Ino(in.NodeId), in.Size, int(in.Offset), in.Fh, false)
	var de fuse.DirEntry
	for _, e := range entries {
		de.Ino = uint64(e.Inode)
		de.Name = string(e.Name)
		de.Mode = e.Attr.SMode()
		if !out.AddDirEntry(de) {
			break
		}
	}
	return fuse.Status(err)
}

func (fs *fileSystem) ReadDirPlus(cancel <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	ctx := newContext(cancel, &in.InHeader)
	defer releaseContext(ctx)
	entries, err := fs.v.Readdir(ctx, Ino(in.NodeId), in.Size, int(in.Offset), in.Fh, true)
	var de fuse.DirEntry
	for _, e := range entries {
		de.Ino = uint64(e.Inode)
		de.Name = string(e.Name)
		de.Mode = e.Attr.SMode()
		eo := out.AddDirLookupEntry(de)
		if eo == nil {
			break
		}
		if e.Attr.Full {
			fs.v.UpdateLength(e.Inode, e.Attr)
			fs.replyEntry(eo, e)
		} else {
			eo.Ino = uint64(e.Inode)
			eo.Generation = 1
		}
	}
	return fuse.Status(err)
}

var cancelReleaseDir = make(chan struct{})

func (fs *fileSystem) ReleaseDir(in *fuse.ReleaseIn) {
	ctx := newContext(cancelReleaseDir, &in.InHeader)
	defer releaseContext(ctx)
	fs.v.Releasedir(ctx, Ino(in.NodeId), in.Fh)
}

func (fs *fileSystem) StatFs(cancel <-chan struct{}, in *fuse.InHeader, out *fuse.StatfsOut) (code fuse.Status) {
	ctx := newContext(cancel, in)
	defer releaseContext(ctx)
	st, err := fs.v.StatFS(ctx, Ino(in.NodeId))
	if err != 0 {
		return fuse.Status(err)
	}
	out.NameLen = 255
	out.Frsize = 4096
	out.Bsize = 4096
	out.Blocks = st.Total / uint64(out.Bsize)
	if out.Blocks < 1 {
		out.Blocks = 1
	}
	out.Bavail = st.Avail / uint64(out.Bsize)
	out.Bfree = out.Bavail
	out.Files = st.Files
	out.Ffree = st.Favail
	return 0
}

// Serve starts a server to serve requests from FUSE.
func Serve(v *vfs.VFS, options string, xattrs bool) error {
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, os.Getpid(), -19); err != nil {
		logger.Warnf("setpriority: %s", err)
	}

	conf := v.Conf
	imp := newFileSystem(conf, v)

	var opt fuse.MountOptions
	opt.FsName = "JuiceFS:" + conf.Format.Name
	opt.Name = "juicefs"
	opt.SingleThreaded = false
	opt.MaxBackground = 50
	opt.EnableLocks = true
	opt.DisableXAttrs = !xattrs
	opt.IgnoreSecurityLabels = true
	opt.MaxWrite = 1 << 20
	opt.MaxReadAhead = 1 << 20
	opt.DirectMount = true
	opt.AllowOther = os.Getuid() == 0
	for _, n := range strings.Split(options, ",") {
		if n == "allow_other" || n == "allow_root" {
			opt.AllowOther = true
		} else if n == "nonempty" {
		} else if n == "debug" {
			opt.Debug = true
		} else if n == "writeback_cache" || n == "writeback" {
			opt.EnableWriteback = true
		} else if strings.TrimSpace(n) != "" {
			opt.Options = append(opt.Options, n)
		}
	}
	opt.Options = append(opt.Options, "default_permissions")
	if runtime.GOOS == "darwin" {
		opt.Options = append(opt.Options, "fssubtype=juicefs")
		opt.Options = append(opt.Options, "volname="+conf.Format.Name)
		opt.Options = append(opt.Options, "daemon_timeout=60", "iosize=65536", "novncache")
	}
	fssrv, err := fuse.NewServer(imp, conf.Mountpoint, &opt)
	if err != nil {
		return fmt.Errorf("fuse: %s", err)
	}

	fssrv.Serve()
	return nil
}
