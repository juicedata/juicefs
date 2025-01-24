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
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/acl"
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
	maxSymlink  = meta.MaxSymlink
	maxFileSize = meta.ChunkSize << 31
)

type Port struct {
	PrometheusAgent string `json:",omitempty"`
	DebugAgent      string `json:",omitempty"`
	ConsulAddr      string `json:",omitempty"`
	PyroscopeAddr   string `json:",omitempty"`
}

// FuseOptions contains options for fuse mount, keep the same structure with `fuse.MountOptions`
type FuseOptions struct {
	AllowOther               bool
	Options                  []string
	MaxBackground            int
	MaxWrite                 int
	MaxReadAhead             int
	IgnoreSecurityLabels     bool // ignoring labels should be provided as a fusermount mount option.
	RememberInodes           bool
	FsName                   string
	Name                     string
	SingleThreaded           bool
	DisableXAttrs            bool
	Debug                    bool
	Logger                   *log.Logger `json:"-"`
	EnableLocks              bool
	EnableSymlinkCaching     bool `json:",omitempty"`
	ExplicitDataCacheControl bool
	SyncRead                 bool `json:",omitempty"`
	DirectMount              bool
	DirectMountStrict        bool `json:",omitempty"`
	DirectMountFlags         uintptr
	EnableAcl                bool
	DisableReadDirPlus       bool `json:",omitempty"`
	EnableWriteback          bool
	EnableIoctl              bool `json:",omitempty"`
	DontUmask                bool
	OtherCaps                uint32
	NoAllocForRead           bool
}

func (o FuseOptions) StripOptions() FuseOptions {
	options := o.Options
	o.Options = make([]string, 0, len(o.Options))
	for _, opt := range options {
		if opt == "nonempty" {
			continue
		}
		o.Options = append(o.Options, opt)
	}

	sort.Strings(o.Options)

	// ignore these options because they won't be send to kernel
	o.IgnoreSecurityLabels,
		o.RememberInodes,
		o.SingleThreaded,
		o.DisableXAttrs,
		o.Debug,
		o.NoAllocForRead = false, false, false, false, false, false

	// ignore there options because they cannot be configured by users
	o.Name = ""
	o.MaxBackground = 0
	o.MaxReadAhead = 0
	o.DirectMount = false
	o.DontUmask = false
	return o
}

type SecurityConfig struct {
	EnableCap     bool
	EnableSELinux bool
}

type Config struct {
	Meta                 *meta.Config
	Format               meta.Format
	Chunk                *chunk.Config
	Security             *SecurityConfig
	Port                 *Port
	Version              string
	AttrTimeout          time.Duration
	DirEntryTimeout      time.Duration
	EntryTimeout         time.Duration
	ReaddirCache         bool
	BackupMeta           time.Duration
	BackupSkipTrash      bool
	FastResolve          bool   `json:",omitempty"`
	AccessLog            string `json:",omitempty"`
	PrefixInternal       bool
	HideInternal         bool
	RootSquash           *AnonymousAccount `json:",omitempty"`
	AllSquash            *AnonymousAccount `json:",omitempty"`
	NonDefaultPermission bool              `json:",omitempty"`

	Pid       int
	PPid      int
	CommPath  string       `json:",omitempty"`
	StatePath string       `json:",omitempty"`
	FuseOpts  *FuseOptions `json:",omitempty"`
}

type AnonymousAccount struct {
	Uid uint32
	Gid uint32
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
	if parent == rootID || name == internalNodes[0].name { // 0 is the control file
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
		logit(ctx, "lookup", err, "(%d,%s):%s", parent, name, (*Entry)(entry))
	}()
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Lookup(ctx, parent, name, &inode, attr, true)
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
	defer func() { logit(ctx, "getattr", err, "(%d):%s", ino, (*Entry)(entry)) }()
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
		logit(ctx, "mknod", err, "(%d,%s,%s:0%04o,0x%08X):%s", parent, name, smode(mode), mode, rdev, (*Entry)(entry))
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
		v.invalidateDirHandle(parent, name, inode, attr)
	}
	return
}

func (v *VFS) Unlink(ctx Context, parent Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "unlink", err, "(%d,%s)", parent, name) }()
	if parent == rootID && IsSpecialName(name) {
		err = syscall.EPERM
		return
	}
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Unlink(ctx, parent, name)
	if err == 0 {
		v.invalidateDirHandle(parent, name, 0, nil)
	}
	return
}

func (v *VFS) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "mkdir", err, "(%d,%s,%s:0%04o):%s", parent, name, smode(mode), mode, (*Entry)(entry))
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
		v.invalidateDirHandle(parent, name, inode, attr)
	}
	return
}

func (v *VFS) Rmdir(ctx Context, parent Ino, name string) (err syscall.Errno) {
	defer func() { logit(ctx, "rmdir", err, "(%d,%s)", parent, name) }()
	if len(name) > maxName {
		err = syscall.ENAMETOOLONG
		return
	}
	err = v.Meta.Rmdir(ctx, parent, name)
	if err == 0 {
		v.invalidateDirHandle(parent, name, 0, nil)
	}
	return
}

func (v *VFS) Symlink(ctx Context, path string, parent Ino, name string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "symlink", err, "(%d,%s,%s):%s", parent, name, path, (*Entry)(entry))
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
		v.invalidateDirHandle(parent, name, inode, attr)
	}
	return
}

func (v *VFS) Readlink(ctx Context, ino Ino) (path []byte, err syscall.Errno) {
	defer func() { logit(ctx, "readlink", err, "(%d): (%s)", ino, string(path)) }()
	err = v.Meta.ReadLink(ctx, ino, &path)
	return
}

func (v *VFS) Rename(ctx Context, parent Ino, name string, newparent Ino, newname string, flags uint32) (err syscall.Errno) {
	defer func() {
		logit(ctx, "rename", err, "(%d,%s,%d,%s,%d)", parent, name, newparent, newname, flags)
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

	var inode Ino
	var attr = &Attr{}
	err = v.Meta.Rename(ctx, parent, name, newparent, newname, flags, &inode, attr)
	if err == 0 {
		v.invalidateDirHandle(parent, name, 0, nil)
		v.invalidateDirHandle(newparent, newname, 0, nil)
		v.invalidateDirHandle(newparent, newname, inode, attr)
	}
	return
}

func (v *VFS) Link(ctx Context, ino Ino, newparent Ino, newname string) (entry *meta.Entry, err syscall.Errno) {
	defer func() {
		logit(ctx, "link", err, "(%d,%d,%s):%s", ino, newparent, newname, (*Entry)(entry))
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
		v.invalidateDirHandle(newparent, newname, ino, attr)
	}
	return
}

func (v *VFS) Opendir(ctx Context, ino Ino, flags uint32) (fh uint64, err syscall.Errno) {
	defer func() { logit(ctx, "opendir", err, "(%d) [fh:%d]", ino, fh) }()
	if ctx.CheckPermission() {
		var mmask uint8 = 0
		switch flags & (syscall.O_RDONLY | syscall.O_WRONLY | syscall.O_RDWR) {
		case syscall.O_RDONLY:
			mmask = MODE_MASK_R
		case syscall.O_WRONLY:
			mmask = MODE_MASK_W
		case syscall.O_RDWR:
			mmask = MODE_MASK_R | MODE_MASK_W
		}
		if err = v.Meta.Access(ctx, ino, mmask, nil); err != 0 {
			return
		}
	}
	fh = v.newHandle(ino, true).fh
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
	defer func() { logit(ctx, "readdir", err, "(%d,%d,%d,%t): (%d)", ino, size, off, plus, len(entries)) }()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.Lock()
	defer h.Unlock()

	if h.dirHandler == nil || off == 0 {
		if h.dirHandler != nil {
			h.dirHandler.Close()
			h.dirHandler = nil
		}
		var initEntries []*meta.Entry
		if ino == rootID && !v.Conf.HideInternal {
			for _, node := range internalNodes[1:] {
				initEntries = append(initEntries, &meta.Entry{
					Inode: node.inode,
					Name:  []byte(node.name),
					Attr:  node.attr,
				})
			}
		}
		h.readAt = time.Now()
		if h.dirHandler, err = v.Meta.NewDirHandler(ctx, ino, plus, initEntries); err != 0 {
			if plus && err == syscall.EACCES {
				h.dirHandler, err = v.Meta.NewDirHandler(ctx, ino, false, initEntries)
			}
			if err != 0 {
				return
			}
		}
	}
	if entries, err = h.dirHandler.List(ctx, off); err != 0 {
		return
	}
	readAt = h.readAt
	logger.Debugf("readdir: [%d:%d] %d entries, offset=%d", ino, fh, len(entries), off)
	return
}

func (v *VFS) UpdateReaddirOffset(ctx Context, ino Ino, fh uint64, off int) {
	h := v.findHandle(ino, fh)
	if h == nil {
		return
	}
	h.Lock()
	defer h.Unlock()
	if h.dirHandler != nil {
		h.dirHandler.Read(off)
	}
}

func (v *VFS) Releasedir(ctx Context, ino Ino, fh uint64) int {
	defer logit(ctx, "releasedir", 0, "(%d)", ino)
	h := v.findHandle(ino, fh)
	if h == nil {
		return 0
	}
	v.ReleaseHandler(ino, fh)
	return 0
}

const O_TMPFILE = 020000000

func (v *VFS) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	defer func() {
		logit(ctx, "create", err, "(%d,%s,%s:0%04o):%s [fh:%d]", parent, name, smode(mode), mode, (*Entry)(entry), fh)
	}()
	// O_TMPFILE support
	doUnlink := runtime.GOOS == "linux" && flags&O_TMPFILE != 0
	if doUnlink {
		name = fmt.Sprintf("tmpfile_%s", uuid.New().String())
	}
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
		v.invalidateDirHandle(parent, name, inode, attr)

		if doUnlink {
			if flags&syscall.O_EXCL != 0 {
				logger.Warnf("The O_EXCL is currently not supported for use with O_TMPFILE")
			}
			err = v.Unlink(ctx, parent, name)
		}
	}
	return
}

func (v *VFS) Open(ctx Context, ino Ino, flags uint32) (entry *meta.Entry, fh uint64, err syscall.Errno) {
	defer func() {
		if entry != nil {
			logit(ctx, "open", err, "(%d,%#x) [fh:%d]", ino, flags, fh)
		} else {
			logit(ctx, "open", err, "(%d,%#x)", ino, flags)
		}
	}()
	var attr = &Attr{}
	if IsSpecialNode(ino) {
		if ino != controlInode && (flags&O_ACCMODE) != syscall.O_RDONLY {
			err = syscall.EACCES
			return
		}
		h := v.newHandle(ino, true)
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
			v.Conf.Format = v.Meta.GetFormat()
			if v.UpdateFormat != nil {
				v.UpdateFormat(&v.Conf.Format)
			}
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

func (v *VFS) Truncate(ctx Context, ino Ino, size int64, fh uint64, attr *Attr) (err syscall.Errno) {
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
	sort.Slice(hs, func(i, j int) bool { return hs[i].fh < hs[j].fh })
	for _, h := range hs {
		if !h.Wlock(ctx) {
			err = syscall.EINTR
			return
		}
		defer func(h *handle) { h.Wunlock() }(h)
	}
	_ = v.writer.Flush(ctx, ino)
	if fh == 0 {
		err = v.Meta.Truncate(ctx, ino, 0, uint64(size), attr, false)
	} else {
		h := v.findHandle(ino, fh)
		if h == nil {
			err = syscall.EBADF
			return
		}
		if h.writer == nil {
			err = syscall.EACCES
			return
		}
		err = v.Meta.Truncate(ctx, ino, 0, uint64(size), attr, true)
	}
	if err == 0 {
		v.writer.Truncate(ino, uint64(size))
		v.reader.Truncate(ino, uint64(size))
		v.invalidateAttr(ino)
	}
	return err
}

func (v *VFS) ReleaseHandler(ino Ino, fh uint64) {
	v.releaseFileHandle(ino, fh)
}

func (v *VFS) Release(ctx Context, ino Ino, fh uint64) {
	var err syscall.Errno
	defer func() { logit(ctx, "release", err, "(%d,%d)", ino, fh) }()
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
			fowner := f.flockOwner
			powner := f.ofdOwner
			f.Unlock()
			if f.writer != nil {
				_ = f.writer.Flush(ctx)
				v.invalidateAttr(ino)
			}
			if locks&1 != 0 {
				_ = v.Meta.Flock(ctx, ino, fowner^fh, F_UNLCK, false)
			}
			if locks&2 != 0 && powner != 0 {
				_ = v.Meta.Setlk(ctx, ino, powner, false, F_UNLCK, 0, 0x7FFFFFFFFFFFFFFF, 0)
			}
		}
		_ = v.Meta.Close(ctx, ino)
		go v.releaseFileHandle(ino, fh) // after writes it waits for data sync, so do it after everything
	}
}

func hasReadPerm(flag uint32) bool {
	return (flag & O_ACCMODE) != syscall.O_WRONLY
}

func (v *VFS) Read(ctx Context, ino Ino, buf []byte, off uint64, fh uint64) (n int, err syscall.Errno) {
	size := uint32(len(buf))
	if IsSpecialNode(ino) {
		if ino == controlInode && runtime.GOOS == "darwin" {
			fh = v.getControlHandle(ctx.Pid())
		}
		h := v.findHandle(ino, fh)
		if h == nil {
			err = syscall.EBADF
			return
		}
		if len(h.data) == 0 {
			switch ino {
			case statsInode:
				h.data = collectMetrics(v.registry)
			case configInode:
				v.Conf.Format = v.Meta.GetFormat()
				if v.UpdateFormat != nil {
					v.UpdateFormat(&v.Conf.Format)
				}
				v.Conf.Format.RemoveSecret()
				h.data, _ = json.MarshalIndent(v.Conf, "", " ")
			}
		}

		if ino == logInode {
			if h.flags&O_RECOVERED != 0 {
				openAccessLog(fh)
			}
			n = readAccessLog(fh, buf)
		} else {
			defer func() { logit(ctx, "read", err, "(%d,%d,%d,%d): %d", ino, size, off, fh, n) }()
			h.Lock()
			defer h.Unlock()
			if off < h.off {
				logger.Errorf("read dropped data from %s: %d < %d", ino, off, h.off)
				err = syscall.EIO
				return
			}
			if int(off-h.off) < len(h.data) {
				n = copy(buf, h.data[off-h.off:])
			}
			if len(h.data) > 2<<20 && off-h.off > 1<<20 {
				// drop first part to avoid OOM
				h.off += 1 << 20
				h.data = h.data[1<<20:]
			}
		}
		return
	}

	defer func() {
		readSizeHistogram.Observe(float64(n))
		logit(ctx, "read", err, "(%d,%d,%d,%d): (%d)", ino, size, off, fh, n)
	}()
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	if h.flags&O_RECOVERED != 0 {
		// recovered
		var attr Attr
		err = v.Meta.Open(ctx, ino, syscall.O_RDONLY, &attr)
		if err != 0 {
			v.releaseHandle(ino, fh)
			err = syscall.EBADF
			return
		}
		h.Lock()
		v.UpdateLength(ino, &attr)
		h.flags = syscall.O_RDONLY
		h.reader = v.reader.Open(h.inode, attr.Length)
		h.Unlock()
	}

	if off >= maxFileSize || off+uint64(size) >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	if h.reader == nil {
		err = syscall.EBADF
		return
	}

	// there could be read operation for write-only if kernel writeback is enabled
	if v.Conf.FuseOpts != nil && !v.Conf.FuseOpts.EnableWriteback && !hasReadPerm(h.flags) {
		err = syscall.EBADF
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
	defer func() { logit(ctx, "write", err, "(%d,%d,%d,%d)", ino, size, off, fh) }()
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
		h.Lock()
		defer h.Unlock()
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
			go v.handleInternalMsg(h.bctx, cmd, rb, h)
		} else {
			logger.Warnf("broken message: %d %d < %d", cmd, size, rb.Left())
			h.data = append(h.data, uint8(syscall.EIO&0xff))
		}
		return
	}

	if h.writer == nil {
		err = syscall.EBADF
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
		v.reader.Invalidate(ino, off, size)
		v.invalidateAttr(ino)
	}
	return
}

func (v *VFS) Fallocate(ctx Context, ino Ino, mode uint8, off, size int64, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fallocate", err, "(%d,%d,%d,%d)", ino, mode, off, size) }()
	if off < 0 || size <= 0 {
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
	if off >= maxFileSize || off+size >= maxFileSize {
		err = syscall.EFBIG
		return
	}
	if h.writer == nil {
		err = syscall.EBADF
		return
	}
	if !h.Wlock(ctx) {
		err = syscall.EINTR
		return
	}
	defer h.Wunlock()
	defer h.removeOp(ctx)

	err = v.writer.Flush(ctx, ino)
	if err != 0 {
		return
	}
	var length uint64
	err = v.Meta.Fallocate(ctx, ino, mode, uint64(off), uint64(size), &length)
	if err == 0 {
		v.writer.Truncate(ino, length)
		s := size
		if off+size > int64(length) {
			s = int64(length) - off
		}
		if s > 0 {
			v.reader.Invalidate(ino, uint64(off), uint64(s))
		}
		v.invalidateAttr(ino)
	}
	return
}

func (v *VFS) CopyFileRange(ctx Context, nodeIn Ino, fhIn, offIn uint64, nodeOut Ino, fhOut, offOut, size uint64, flags uint32) (copied uint64, err syscall.Errno) {
	defer func() {
		logit(ctx, "copy_file_range", err, "(%d,%d,%d,%d,%d,%d)", nodeIn, offIn, nodeOut, offOut, size, flags)
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

	err = v.writer.Flush(ctx, nodeIn)
	if err != 0 {
		return
	}
	err = v.writer.Flush(ctx, nodeOut)
	if err != 0 {
		return
	}
	var length uint64
	err = v.Meta.CopyFileRange(ctx, nodeIn, offIn, nodeOut, offOut, size, flags, &copied, &length)
	if err == 0 {
		v.writer.Truncate(nodeOut, length)
		v.reader.Invalidate(nodeOut, offOut, size)
		v.invalidateAttr(nodeOut)
	}
	return
}

func (v *VFS) Flush(ctx Context, ino Ino, fh uint64, lockOwner uint64) (err syscall.Errno) {
	if ino == controlInode && runtime.GOOS == "darwin" {
		fh = v.getControlHandle(ctx.Pid())
		defer v.releaseControlHandle(ctx.Pid())
	}
	defer func() { logit(ctx, "flush", err, "(%d,%d,%016X)", ino, fh, lockOwner) }()
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
	if lockOwner == h.ofdOwner {
		h.ofdOwner = 0
	}
	h.Unlock()
	if locks&2 != 0 {
		_ = v.Meta.Setlk(ctx, ino, lockOwner, false, F_UNLCK, 0, 0x7FFFFFFFFFFFFFFF, 0)
	}
	return
}

func (v *VFS) Fsync(ctx Context, ino Ino, datasync int, fh uint64) (err syscall.Errno) {
	defer func() { logit(ctx, "fsync", err, "(%d,%d)", ino, datasync) }()
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

var macSupportFlags = meta.XattrCreateOrReplace | meta.XattrCreate | meta.XattrReplace

const (
	_SECURITY_CAPABILITY  = "security.capability"
	_SECURITY_SELINUX     = "security.selinux"
	_SECURITY_ACL         = "system.posix_acl_access"
	_SECURITY_ACL_DEFAULT = "system.posix_acl_default"
)

func isXattrEnabled(conf *Config, name string) bool {
	switch name {
	case _SECURITY_CAPABILITY:
		return conf.Security != nil && conf.Security.EnableCap
	case _SECURITY_SELINUX:
		return conf.Security != nil && conf.Security.EnableSELinux
	case _SECURITY_ACL, _SECURITY_ACL_DEFAULT:
		return conf.Format.EnableACL
	}
	return true
}

func (v *VFS) SetXattr(ctx Context, ino Ino, name string, value []byte, flags uint32) (err syscall.Errno) {
	defer func() { logit(ctx, "setxattr", err, "(%d,%s,%d,%d)", ino, name, len(value), flags) }()
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

	if !isXattrEnabled(v.Conf, name) {
		err = syscall.ENOTSUP
		return
	}

	if typ, ok := aclTypes[name]; ok {
		var rule *acl.Rule
		rule, err = decodeACL(value)
		if err != 0 {
			return
		}
		err = v.Meta.SetFacl(ctx, ino, typ, rule)
		v.invalidateAttr(ino)
	} else {
		// only retain supported flags
		if runtime.GOOS == "darwin" {
			flags &= uint32(macSupportFlags)
		}
		err = v.Meta.SetXattr(ctx, ino, name, value, flags)
	}
	return
}

func (v *VFS) GetXattr(ctx Context, ino Ino, name string, size uint32) (value []byte, err syscall.Errno) {
	defer func() { logit(ctx, "getxattr", err, "(%d,%s,%d): (%d)", ino, name, size, len(value)) }()
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

	if !isXattrEnabled(v.Conf, name) {
		err = syscall.ENODATA
		return
	}

	if typ, ok := aclTypes[name]; ok {
		rule := &acl.Rule{}
		if err = v.Meta.GetFacl(ctx, ino, typ, rule); err != 0 {
			return nil, err
		}
		value = encodeACL(rule)
	} else {
		err = v.Meta.GetXattr(ctx, ino, name, &value)
	}
	if size > 0 && len(value) > int(size) {
		err = syscall.ERANGE
	}
	return
}

func (v *VFS) ListXattr(ctx Context, ino Ino, size int) (data []byte, err syscall.Errno) {
	defer func() { logit(ctx, "listxattr", err, "(%d,%d): (%d)", ino, size, len(data)) }()
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
	defer func() { logit(ctx, "removexattr", err, "(%d,%s)", ino, name) }()
	if IsSpecialNode(ino) {
		err = syscall.EPERM
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

	if !isXattrEnabled(v.Conf, name) {
		err = syscall.ENOTSUP
		return
	}

	if typ, ok := aclTypes[name]; ok {
		err = v.Meta.SetFacl(ctx, ino, typ, acl.EmptyRule())
	} else {
		err = v.Meta.RemoveXattr(ctx, ino, name)
	}

	return
}

var logger = utils.GetLogger("juicefs")

type VFS struct {
	Conf            *Config
	Meta            meta.Meta
	Store           chunk.ChunkStore
	InvalidateEntry func(parent meta.Ino, name string) syscall.Errno
	UpdateFormat    func(*meta.Format)
	reader          DataReader
	writer          DataWriter

	handles   map[Ino][]*handle
	handleIno map[uint64]Ino
	hanleM    sync.Mutex
	nextfh    uint64

	modM       sync.Mutex
	modifiedAt map[Ino]time.Time

	registry *prometheus.Registry
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
		handleIno:  make(map[uint64]Ino),
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
	if conf.PrefixInternal {
		for _, n := range internalNodes {
			n.name = ".jfs" + n.name
		}
		meta.TrashName = ".jfs" + meta.TrashName
	}

	statePath := os.Getenv("_FUSE_STATE_PATH")
	if statePath == "" {
		statePath = fmt.Sprintf("/tmp/state%d.json", os.Getppid())
	}
	if err := v.loadAllHandles(statePath); err != nil && !os.IsNotExist(err) {
		logger.Errorf("load state from %s: %s", statePath, err)
	}
	_ = os.Rename(statePath, statePath+".bak")

	go v.cleanupModified()
	initVFSMetrics(v, writer, reader, registerer)
	return v
}

func (v *VFS) invalidateAttr(ino Ino) {
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

func (v *VFS) FlushAll(path string) (err error) {
	now := time.Now()
	defer func() {
		logger.Infof("flush buffered data in %s: %v", time.Since(now), err)
	}()
	err = v.writer.FlushAll()
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	return v.dumpAllHandles(path)
}

func initVFSMetrics(v *VFS, writer DataWriter, reader DataReader, registerer prometheus.Registerer) {
	if registerer == nil {
		return
	}
	handlersGause := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "fuse_open_handlers",
		Help: "number of open files and directories.",
	}, func() float64 {
		v.hanleM.Lock()
		defer v.hanleM.Unlock()
		return float64(len(v.handles))
	})
	_ = registerer.Register(handlersGause)
	InitMemoryBufferMetrics(writer, reader, registerer)
}

func InitMemoryBufferMetrics(writer DataWriter, reader DataReader, registerer prometheus.Registerer) {
	usedBufferSize := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "used_buffer_size_bytes",
		Help: "size of currently used buffer.",
	}, func() float64 {
		if dw, ok := writer.(*dataWriter); ok {
			return float64(dw.usedBufferSize())
		}
		return 0.0
	})
	storeCacheSize := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "store_cache_size_bytes",
		Help: "size of store cache.",
	}, func() float64 {
		if dw, ok := writer.(*dataWriter); ok {
			return float64(dw.store.UsedMemory())
		}
		return 0.0
	})
	readBufferMetric := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "used_read_buffer_size_bytes",
		Help: "size of currently used buffer for read",
	}, func() float64 {
		if dr, ok := reader.(*dataReader); ok {
			return float64(dr.readBufferUsed())
		}
		return 0.0
	})
	_ = registerer.Register(usedBufferSize)
	_ = registerer.Register(storeCacheSize)
	_ = registerer.Register(readBufferMetric)
}

func InitMetrics(registerer prometheus.Registerer) {
	if registerer == nil {
		return
	}
	registerer.MustRegister(readSizeHistogram)
	registerer.MustRegister(writtenSizeHistogram)
	registerer.MustRegister(opsDurationsHistogram)
	registerer.MustRegister(opsTotal)
	registerer.MustRegister(opsDurations)
	registerer.MustRegister(opsIOErrors)
	registerer.MustRegister(compactSizeHistogram)
}

// Linux ACL format:
//
//	version:8 (2)
//	flags:8 (0)
//	filler:16
//	N * [ tag:16 perm:16 id:32 ]
//	tag:
//	  01 - user
//	  02 - named user
//	  04 - group
//	  08 - named group
//	  10 - mask
//	  20 - other

func encodeACL(n *acl.Rule) []byte {
	length := 4 + 24 + uint32(len(n.NamedUsers)+len(n.NamedGroups))*8
	if n.Mask != 0xFFFF {
		length += 8
	}
	buff := make([]byte, length)
	w := utils.NewNativeBuffer(buff)
	w.Put8(acl.Version) // version
	w.Put8(0)           // flag
	w.Put16(0)          // filler
	wRule := func(tag, perm uint16, id uint32) {
		w.Put16(tag)
		w.Put16(perm)
		w.Put32(id)
	}
	wRule(1, n.Owner, 0xFFFFFFFF)
	for _, rule := range n.NamedUsers {
		wRule(2, rule.Perm, rule.Id)
	}
	wRule(4, n.Group, 0xFFFFFFFF)
	for _, rule := range n.NamedGroups {
		wRule(8, rule.Perm, rule.Id)
	}
	if n.Mask != 0xFFFF {
		wRule(0x10, n.Mask, 0xFFFFFFFF)
	}
	wRule(0x20, n.Other, 0xFFFFFFFF)
	return buff
}

func decodeACL(buff []byte) (*acl.Rule, syscall.Errno) {
	length := len(buff)
	if length < 4 || ((length % 8) != 4) || buff[0] != acl.Version {
		return nil, syscall.EINVAL
	}

	n := acl.EmptyRule()
	r := utils.NewNativeBuffer(buff[4:])
	for r.HasMore() {
		tag := r.Get16()
		perm := r.Get16()
		id := r.Get32()
		switch tag {
		case 1:
			if n.Owner != 0xFFFF {
				return nil, syscall.EINVAL
			}
			n.Owner = perm
		case 2:
			n.NamedUsers = append(n.NamedUsers, acl.Entry{Id: id, Perm: perm})
		case 4:
			if n.Group != 0xFFFF {
				return nil, syscall.EINVAL
			}
			n.Group = perm
		case 8:
			n.NamedGroups = append(n.NamedGroups, acl.Entry{Id: id, Perm: perm})
		case 0x10:
			if n.Mask != 0xFFFF {
				return nil, syscall.EINVAL
			}
			n.Mask = perm
		case 0x20:
			if n.Other != 0xFFFF {
				return nil, syscall.EINVAL
			}
			n.Other = perm
		}
	}
	if n.Mask == 0xFFFF && len(n.NamedUsers)+len(n.NamedGroups) > 0 {
		return nil, syscall.EINVAL
	}
	return n, 0
}

var aclTypes = map[string]uint8{
	_SECURITY_ACL:         acl.TypeAccess,
	_SECURITY_ACL_DEFAULT: acl.TypeDefault,
}
