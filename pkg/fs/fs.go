/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/acl"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/prometheus/client_golang/prometheus"
)

var logger = utils.GetLogger("juicefs")

type Ino = meta.Ino
type Attr = meta.Attr
type LogContext = vfs.LogContext

func IsExist(err error) bool {
	return err == syscall.EEXIST || err == syscall.EACCES || err == syscall.EPERM
}

func IsNotExist(err error) bool {
	return err == syscall.ENOENT
}

func IsNotEmpty(err error) bool {
	return err == syscall.ENOTEMPTY
}

func errstr(e error) string {
	if e == nil {
		return "OK"
	}
	if eno, ok := e.(syscall.Errno); ok && eno == 0 {
		return "OK"
	}
	return e.Error()
}

type FileStat struct {
	name  string
	inode Ino
	attr  *Attr
}

func (fs *FileStat) Inode() Ino   { return fs.inode }
func (fs *FileStat) Name() string { return fs.name }
func (fs *FileStat) Size() int64  { return int64(fs.attr.Length) }
func (fs *FileStat) Mode() os.FileMode {
	attr := fs.attr
	mode := os.FileMode(attr.Mode & 0777)
	if attr.Mode&04000 != 0 {
		mode |= os.ModeSetuid
	}
	if attr.Mode&02000 != 0 {
		mode |= os.ModeSetgid
	}
	if attr.Mode&01000 != 0 {
		mode |= os.ModeSticky
	}
	if attr.AccessACL+attr.DefaultACL > 0 {
		mode |= 1 << 18
	}
	switch attr.Typ {
	case meta.TypeDirectory:
		mode |= os.ModeDir
	case meta.TypeSymlink:
		mode |= os.ModeSymlink
	case meta.TypeFile:
	default:
	}
	return mode
}
func (fs *FileStat) ModTime() time.Time {
	return time.Unix(fs.attr.Mtime, int64(fs.attr.Mtimensec))
}
func (fs *FileStat) IsDir() bool      { return fs.attr.Typ == meta.TypeDirectory }
func (fs *FileStat) IsSymlink() bool  { return fs.attr.Typ == meta.TypeSymlink }
func (fs *FileStat) Sys() interface{} { return fs.attr }
func (fs *FileStat) Uid() int         { return int(fs.attr.Uid) }
func (fs *FileStat) Gid() int         { return int(fs.attr.Gid) }

func (fs *FileStat) Atime() int64 { return fs.attr.Atime*1000 + int64(fs.attr.Atimensec/1e6) }
func (fs *FileStat) Mtime() int64 { return fs.attr.Mtime*1000 + int64(fs.attr.Mtimensec/1e6) }

func (fs *FileStat) Attr() *Attr { return fs.attr }

func AttrToFileInfo(inode Ino, attr *Attr) *FileStat {
	return &FileStat{inode: inode, attr: attr}
}

type entryCache struct {
	inode  Ino
	typ    uint8
	expire time.Time
}

type attrCache struct {
	attr   Attr
	expire time.Time
}

type FileSystem struct {
	conf        *vfs.Config
	reader      vfs.DataReader
	writer      vfs.DataWriter
	m           meta.Meta
	store       chunk.ChunkStore
	cacheFiller *vfs.CacheFiller

	Superuser  string
	Supergroup string

	cacheM          sync.Mutex
	entries         map[Ino]map[string]*entryCache
	attrs           map[Ino]*attrCache
	checkAccessFile time.Duration
	rotateAccessLog int64
	logBuffer       chan string

	readSizeHistogram     prometheus.Histogram
	writtenSizeHistogram  prometheus.Histogram
	opsDurationsHistogram prometheus.Histogram

	registry *prometheus.Registry

	// Pre-parsed subdir prefixes for fast path checking
	subdirPrefixes []string
}

type File struct {
	path  string
	inode Ino
	info  *FileStat
	fs    *FileSystem

	sync.Mutex
	flags    uint32
	offset   int64
	rdata    vfs.FileReader
	wdata    vfs.FileWriter
	dircache []os.FileInfo
	entries  []*meta.Entry
	data     []byte
}

func NewFileSystem(conf *vfs.Config, m meta.Meta, d chunk.ChunkStore, registry *prometheus.Registry) (*FileSystem, error) {
	reader := vfs.NewDataReader(conf, m, d)
	fs := &FileSystem{
		m:               m,
		store:           d,
		conf:            conf,
		cacheFiller:     vfs.NewCacheFiller(conf, m, d),
		reader:          reader,
		writer:          vfs.NewDataWriter(conf, m, d, reader),
		entries:         make(map[meta.Ino]map[string]*entryCache),
		attrs:           make(map[meta.Ino]*attrCache),
		checkAccessFile: time.Minute,
		rotateAccessLog: 300 << 20, // 300 MiB

		readSizeHistogram: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sdk_read_size_bytes",
			Help:    "size of read distributions.",
			Buckets: prometheus.LinearBuckets(4096, 4096, 32),
		}),
		writtenSizeHistogram: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sdk_written_size_bytes",
			Help:    "size of write distributions.",
			Buckets: prometheus.LinearBuckets(4096, 4096, 32),
		}),
		opsDurationsHistogram: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sdk_ops_durations_histogram_seconds",
			Help:    "Operations latency distributions.",
			Buckets: prometheus.ExponentialBuckets(0.00001, 1.8, 29),
		}),
		registry: registry,
	}

	// Pre-parse subdir prefixes for fast path checking
	if conf.Subdir != "" {
		subdirs := strings.Split(conf.Subdir, ",")
		fs.subdirPrefixes = make([]string, 0, len(subdirs))
		for _, prefix := range subdirs {
			prefix = strings.TrimSpace(prefix)
			if prefix != "" {
				fs.subdirPrefixes = append(fs.subdirPrefixes, prefix)
			}
		}
	}

	go fs.cleanupCache()
	if conf.AccessLog != "" {
		f, err := os.OpenFile(conf.AccessLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			logger.Errorf("Open access log %s: %s", conf.AccessLog, err)
		} else {
			_ = os.Chmod(conf.AccessLog, 0666)
			fs.logBuffer = make(chan string, 1024)
			go fs.flushLog(f, fs.logBuffer, conf.AccessLog)
		}
	}
	return fs, nil
}

func (fs *FileSystem) InitMetrics(reg prometheus.Registerer) {
	if reg != nil {
		reg.MustRegister(fs.readSizeHistogram)
		reg.MustRegister(fs.writtenSizeHistogram)
		reg.MustRegister(fs.opsDurationsHistogram)
		vfs.InitMemoryBufferMetrics(fs.writer, fs.reader, reg)
	}
}

func (fs *FileSystem) cleanupCache() {
	for {
		fs.cacheM.Lock()
		now := time.Now()
		var cnt int
		for inode, it := range fs.attrs {
			if now.After(it.expire) {
				delete(fs.attrs, inode)
			}
			cnt++
			if cnt > 1000 {
				break
			}
		}
		cnt = 0
	OUTER:
		for inode, es := range fs.entries {
			for n, e := range es {
				if now.After(e.expire) {
					delete(es, n)
					if len(es) == 0 {
						delete(fs.entries, inode)
					}
				}
				cnt++
				if cnt > 1000 {
					break OUTER
				}
			}
		}
		fs.cacheM.Unlock()
		time.Sleep(time.Second)
	}
}

func (fs *FileSystem) InvalidateEntry(parent Ino, name string) {
	fs.cacheM.Lock()
	defer fs.cacheM.Unlock()
	es, ok := fs.entries[parent]
	if ok {
		delete(es, name)
		if len(es) == 0 {
			delete(fs.entries, parent)
		}
	}
}

func (fs *FileSystem) InvalidateAttr(ino Ino) {
	fs.cacheM.Lock()
	defer fs.cacheM.Unlock()
	delete(fs.attrs, ino)
}

func (fs *FileSystem) log(ctx LogContext, format string, args ...interface{}) {
	used := ctx.Duration()
	fs.opsDurationsHistogram.Observe(used.Seconds())
	if fs.logBuffer == nil {
		return
	}
	now := utils.Now()
	cmd := fmt.Sprintf(format, args...)
	ts := now.Format("2006.01.02 15:04:05.000000")
	cmd += fmt.Sprintf(" <%.6f>", used.Seconds())
	line := fmt.Sprintf("%s [uid:%d,gid:%d,pid:%d] %s\n", ts, ctx.Uid(), ctx.Gid(), ctx.Pid(), cmd)
	select {
	case fs.logBuffer <- line:
	default:
		logger.Debugf("log dropped: %s", line[:len(line)-1])
	}
}

func (fs *FileSystem) flushLog(f *os.File, logBuffer chan string, path string) {
	buf := make([]byte, 0, 128<<10)
	var lastcheck = time.Now()
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	for {
		line, ok := <-logBuffer
		if !ok {
			return
		}
		buf = append(buf[:0], []byte(line)...)
	LOOP:
		for len(buf) < (128 << 10) {
			select {
			case line, ok = <-logBuffer:
				if !ok {
					break LOOP
				}
				buf = append(buf, []byte(line)...)
			default:
				break LOOP
			}
		}
		_, err := f.Write(buf)
		if err != nil {
			logger.Errorf("write access log: %s", err)
			break
		}
		if lastcheck.Add(fs.checkAccessFile).After(time.Now()) {
			continue
		}
		lastcheck = time.Now()
		var fi os.FileInfo
		fi, err = f.Stat()
		if err == nil && fi.Size() > fs.rotateAccessLog {
			_ = f.Close()
			fi, err = os.Stat(path)
			if err == nil && fi.Size() > fs.rotateAccessLog {
				tmp := fmt.Sprintf("%s.%p", path, fs)
				if os.Rename(path, tmp) == nil {
					for i := 6; i > 0; i-- {
						_ = os.Rename(path+"."+strconv.Itoa(i), path+"."+strconv.Itoa(i+1))
					}
					_ = os.Rename(tmp, path+".1")
				} else {
					fi, err = os.Stat(path)
					if err == nil && fi.Size() > fs.rotateAccessLog*7 {
						logger.Infof("can't rename %s, truncate it", path)
						_ = os.Truncate(path, 0)
					}
				}
			}
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				logger.Errorf("open %s: %s", path, err)
				break
			}
			_ = os.Chmod(path, 0666)
		}
	}
}

func (fs *FileSystem) Meta() meta.Meta {
	return fs.m
}

func (fs *FileSystem) StatFS(ctx meta.Context) (totalspace uint64, availspace uint64) {
	defer trace.StartRegion(context.TODO(), "fs.StatFS").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "StatFS (): (%d,%d)", totalspace, availspace) }()
	var iused, iavail uint64
	_ = fs.m.StatFS(ctx, meta.RootInode, &totalspace, &availspace, &iused, &iavail)
	return
}

// open file without following symlink
func (fs *FileSystem) Lopen(ctx meta.Context, path string, flags uint32) (f *File, err syscall.Errno) {
	return fs.open(ctx, path, flags, false)
}

func (fs *FileSystem) Open(ctx meta.Context, path string, flags uint32) (*File, syscall.Errno) {
	return fs.open(ctx, path, flags, true)
}

func (fs *FileSystem) open(ctx meta.Context, path string, flags uint32, followLink bool) (f *File, err syscall.Errno) {
	_, task := trace.NewTask(context.TODO(), "Open")
	defer task.End()
	l := vfs.NewLogContext(ctx)
	if flags != 0 {
		defer func() { fs.log(l, "Open (%s,%d): %s", path, flags, errstr(err)) }()
	} else {
		defer func() { fs.log(l, "Lookup (%s): %s", path, errstr(err)) }()
	}
	var fi *FileStat
	fi, err = fs.resolve(ctx, path, followLink)
	if err != 0 {
		return
	}

	if flags != 0 && !fi.IsDir() {
		var oflags uint32 = syscall.O_RDONLY
		if flags == vfs.MODE_MASK_W {
			oflags = syscall.O_WRONLY
		} else if flags&vfs.MODE_MASK_W != 0 {
			oflags = syscall.O_RDWR
		}
		err = fs.m.Open(ctx, fi.inode, oflags, fi.attr)
		if err != 0 {
			return
		}
	}

	f = &File{}
	f.path = path
	f.inode = fi.inode
	f.info = fi
	f.fs = fs
	f.flags = flags
	switch fi.inode {
	case vfs.ConfigInode:
		fs.conf.Format = fs.Meta().GetFormat()
		fs.conf.Format.RemoveSecret()
		f.data, _ = json.MarshalIndent(fs.conf, "", " ")
		f.info.attr.Length = uint64(len(f.data))
	case vfs.StatsInode:
		f.data = vfs.CollectMetrics(fs.registry)
		f.info.attr.Length = uint64(len(f.data))
	}
	return
}

func (fs *FileSystem) Access(ctx meta.Context, path string, flags int) (err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Access (%s): %s", path, errstr(err)) }()
	var fi *FileStat
	fi, err = fs.resolve(ctx, path, true)
	if err != 0 {
		return
	}

	if ctx.Uid() != 0 && flags != 0 {
		err = fs.m.Access(ctx, fi.inode, uint8(flags), fi.attr)
	}
	return
}

func (fs *FileSystem) Stat(ctx meta.Context, path string) (fi *FileStat, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Stat").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Stat (%s): %s", path, errstr(err)) }()
	return fs.resolve(ctx, path, true)
}

func (fs *FileSystem) Lstat(ctx meta.Context, path string) (fi *FileStat, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Lstat").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Lstat (%s): %s", path, errstr(err)) }()
	return fs.resolve(ctx, path, false)
}

// parentDir preserves dot components so the parent can be resolved in order.
func parentDir(p string) string {
	dir, _ := path.Split(strings.TrimRight(p, "/"))
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return "/"
	}
	return dir
}

func (fs *FileSystem) Mkdir(ctx meta.Context, p string, mode uint16, umask uint16) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Mkdir").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Mkdir (%s, %o): %s", p, mode, errstr(err)) }()
	nd, err := fs.resolveParent(ctx, p)
	if err != 0 {
		return err
	}
	if err = fs.checkCreate(ctx, nd, true); err != 0 {
		return err
	}
	var inode Ino
	err = fs.m.Mkdir(ctx, nd.dir.inode, nd.name, mode, umask, 0, &inode, nil)
	if err == syscall.ENOENT && nd.dir.inode != 1 {
		// parent be moved into trash, try again
		if fs.conf.DirEntryTimeout > 0 {
			parent := parentDir(p)
			if fi, err := fs.resolve(ctx, parentDir(parent), true); err == 0 {
				fs.InvalidateEntry(fi.inode, path.Base(parent))
			}
		}
		if fi2, e := fs.resolve(ctx, parentDir(p), true); e != 0 {
			return e
		} else if fi2.inode != nd.dir.inode {
			err = fs.m.Mkdir(ctx, fi2.inode, nd.name, mode, umask, 0, &inode, nil)
		}
	}
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) MkdirAll(ctx meta.Context, p string, mode uint16, umask uint16) (err syscall.Errno) {
	return fs.MkdirAll0(ctx, p, mode, umask, true)
}

func (fs *FileSystem) MkdirAll0(ctx meta.Context, p string, mode uint16, umask uint16, existOK bool) (err syscall.Errno) {
	err = fs.Mkdir(ctx, p, mode, umask)
	if err == syscall.ENOENT {
		err = fs.MkdirAll(ctx, parentDir(p), mode, umask)
		if err == 0 {
			err = fs.Mkdir(ctx, p, mode, umask)
		}
	}
	if existOK && err == syscall.EEXIST {
		err = 0
	}
	return err
}

func (fs *FileSystem) Unlink(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Unlink").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Unlink (%s): %s", p, errstr(err)) }()
	return fs.Delete0(ctx, p, true)
}

func (fs *FileSystem) Delete(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Delete").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Delete (%s): %s", p, errstr(err)) }()
	return fs.Delete0(ctx, p, false)
}

func (fs *FileSystem) BatchDeleteEntries(ctx meta.Context, parent string, ps []string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.BatchDeleteEntries").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "BatchDeleteEntries : %s", errstr(err)) }()
	parentInfo, errno := fs.Stat(ctx, parent)
	if errno != 0 {
		return errno
	}
	if errno = fs.m.Access(ctx, parentInfo.inode, meta.MODE_MASK_W|meta.MODE_MASK_X, parentInfo.attr); errno != 0 {
		return errno
	}
	var entries []*meta.Entry
	for _, p := range ps {
		var inode Ino
		var attr meta.Attr
		name := path.Base(p)
		if err = fs.lookup(ctx, parentInfo.inode, name, &inode, &attr); err != 0 {
			if err == syscall.ENOENT {
				continue
			}
			return
		}
		entries = append(entries, &meta.Entry{Inode: inode, Name: []byte(name), Attr: &attr})
	}
	if len(entries) == 0 {
		return 0
	}
	eno := fs.m.BatchUnlink(ctx, parentInfo.inode, entries, nil, false)
	for _, p := range ps {
		fs.InvalidateEntry(parentInfo.inode, path.Base(p))
	}
	return eno
}

func (fs *FileSystem) Delete0(ctx meta.Context, p string, callByUnlink bool) (err syscall.Errno) {
	nd, err := fs.resolveParent(ctx, p)
	if err != 0 {
		return
	}
	if nd.typ != lastNorm && callByUnlink {
		return syscall.EISDIR
	}
	if err = nd.rmdirErr(); err != 0 {
		return
	}
	fi, err := fs.resolve(ctx, p, false)
	if err != 0 {
		return
	}
	if fi.IsDir() {
		if callByUnlink {
			err = syscall.EISDIR
			return
		}
		err = fs.m.Rmdir(ctx, nd.dir.inode, nd.name)
	} else {
		err = fs.m.Unlink(ctx, nd.dir.inode, nd.name)
	}
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) Rmdir(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rmdir").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rmdir (%s): %s", p, errstr(err)) }()
	nd, err := fs.resolveParent(ctx, p)
	if err != 0 {
		return
	}
	if err = nd.rmdirErr(); err != 0 {
		return
	}
	err = fs.m.Rmdir(ctx, nd.dir.inode, nd.name)
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

// Rmr follows rm(1): ".", ".." and "/" are refused before anything is removed.
func (fs *FileSystem) Rmr(ctx meta.Context, p string, skipTrash bool, numthreads int) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rmr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rmr (%s): %s", p, errstr(err)) }()
	nd, err := fs.resolveParent(ctx, p)
	if err != 0 {
		return
	}
	if err = nd.rmdirErr(); err != 0 {
		return
	}
	if nd.slash {
		var inode Ino
		var attr Attr
		if err = fs.m.Lookup(ctx, nd.dir.inode, nd.name, &inode, &attr, false); err != 0 {
			return
		}
		if attr.Typ != meta.TypeDirectory {
			return syscall.ENOTDIR
		}
	}
	err = fs.m.Remove(ctx, nd.dir.inode, nd.name, skipTrash, numthreads, nil)
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) Rename(ctx meta.Context, oldpath string, newpath string, flags uint32) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rename").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rename (%s,%s,%d): %s", oldpath, newpath, flags, errstr(err)) }()
	// generic checks of renameat2(2), whether the flags are supported is up to the metadata engine
	if flags&^(meta.RenameNoReplace|meta.RenameExchange|meta.RenameWhiteout) != 0 ||
		flags&meta.RenameExchange != 0 && flags&(meta.RenameNoReplace|meta.RenameWhiteout) != 0 {
		return syscall.EINVAL
	}
	src, err := fs.resolveParent(ctx, oldpath)
	if err != 0 {
		return
	}
	dst, err := fs.resolveParent(ctx, newpath)
	if err != 0 {
		return
	}
	if src.typ != lastNorm {
		return syscall.EBUSY
	}
	if dst.typ != lastNorm {
		if flags&meta.RenameNoReplace != 0 {
			return syscall.EEXIST
		}
		return syscall.EBUSY
	}
	if err = fs.checkRename(ctx, src, dst, flags); err != 0 {
		return
	}
	err = fs.m.Rename(ctx, src.dir.inode, src.name, dst.dir.inode, dst.name, flags, nil, nil)
	fs.InvalidateEntry(src.dir.inode, src.name)
	fs.InvalidateEntry(dst.dir.inode, dst.name)
	return
}

// checkRename makes the checks of do_renameat2() in Linux that need the entries:
// trailing slashes and moving a directory under itself. The metadata engines
// are trusted for everything else. Entries are looked up from the engine
// directly since a stale cache must not let a loop through.
func (fs *FileSystem) checkRename(ctx meta.Context, src, dst nameData, flags uint32) syscall.Errno {
	cross := src.dir.inode != dst.dir.inode
	if !cross && !src.slash && !dst.slash {
		return 0
	}
	var sinode, dinode Ino
	var sattr, dattr Attr
	if err := fs.m.Lookup(ctx, src.dir.inode, src.name, &sinode, &sattr, false); err != 0 {
		return err
	}
	exchange := flags&meta.RenameExchange != 0
	if flags&(meta.RenameNoReplace|meta.RenameExchange) != 0 {
		err := fs.m.Lookup(ctx, dst.dir.inode, dst.name, &dinode, &dattr, false)
		if err == 0 && flags&meta.RenameNoReplace != 0 {
			return syscall.EEXIST
		}
		if err != 0 && (exchange || err != syscall.ENOENT) {
			return err
		}
		if exchange && dst.slash && dattr.Typ != meta.TypeDirectory {
			return syscall.ENOTDIR
		}
	}
	if sattr.Typ != meta.TypeDirectory && (src.slash || dst.slash && !exchange) {
		return syscall.ENOTDIR
	}
	if !cross {
		return 0
	}
	if sattr.Typ == meta.TypeDirectory {
		if err := fs.checkAncestor(ctx, sinode, dst.dir.inode, src.dir.inode); err != 0 {
			return err
		}
	}
	if exchange && dattr.Typ == meta.TypeDirectory {
		return fs.checkAncestor(ctx, dinode, src.dir.inode, dst.dir.inode)
	}
	return 0
}

// checkAncestor returns EINVAL if dir is inode or one of its ancestors. It walks
// up until stop (the other parent, above which dir can't be) or the root.
func (fs *FileSystem) checkAncestor(ctx meta.Context, dir, inode, stop Ino) syscall.Errno {
	visited := make(map[Ino]bool)
	for inode != stop && inode > meta.RootInode && !inode.IsTrash() {
		if inode == dir {
			return syscall.EINVAL
		}
		if visited[inode] {
			return syscall.ELOOP
		}
		visited[inode] = true
		var attr Attr
		if err := fs.m.GetAttr(ctx, inode, &attr); err != 0 {
			return err
		}
		inode = attr.Parent
	}
	return 0
}

// checkCreate follows filename_create() in Linux: ".", ".." and "/" always
// exist, and a trailing slash can only name a new directory.
func (fs *FileSystem) checkCreate(ctx meta.Context, nd nameData, dir bool) syscall.Errno {
	if nd.typ != lastNorm {
		return syscall.EEXIST
	}
	if nd.slash && !dir {
		var inode Ino
		if err := fs.m.Lookup(ctx, nd.dir.inode, nd.name, &inode, &Attr{}, false); err != 0 {
			return err
		}
		return syscall.EEXIST
	}
	return 0
}

func (fs *FileSystem) Link(ctx meta.Context, src string, dst string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Link").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Link (%s,%s): %s", src, dst, errstr(err)) }()

	fi, err := fs.resolve(ctx, src, false)
	if err != 0 {
		return
	}
	nd, err := fs.resolveParent(ctx, dst)
	if err != 0 {
		return
	}
	if err = fs.checkCreate(ctx, nd, false); err != 0 {
		return
	}
	err = fs.m.Link(ctx, fi.inode, nd.dir.inode, nd.name, nil)
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) Symlink(ctx meta.Context, target string, link string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Symlink").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Symlink (%s,%s): %s", target, link, errstr(err)) }()
	if target == "" {
		return syscall.ENOENT // as getname() in Linux, before the link is resolved
	}
	nd, err := fs.resolveParent(ctx, link)
	if err != 0 {
		return
	}
	if err = fs.checkCreate(ctx, nd, false); err != 0 {
		return
	}
	err = fs.m.Symlink(ctx, nd.dir.inode, nd.name, target, nil, nil)
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) Readlink(ctx meta.Context, link string) (path []byte, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Readlink").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Readlink (%s): %s (%d)", link, errstr(err), len(path)) }()
	fi, err := fs.resolve(ctx, link, false)
	if err != 0 {
		return
	}
	err = fs.m.ReadLink(ctx, fi.inode, &path)
	return
}

func (fs *FileSystem) Truncate(ctx meta.Context, path string, length uint64) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Truncate").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Truncate (%s,%d): %s", path, length, errstr(err)) }()
	fi, err := fs.resolve(ctx, path, true)
	if err != 0 {
		return
	}
	if fi.IsDir() {
		return syscall.EISDIR
	}
	err = fs.m.Truncate(ctx, fi.inode, 0, length, nil, false)
	return
}

func (fs *FileSystem) CopyFileRange(ctx meta.Context, src string, soff uint64, dst string, doff uint64, size uint64) (written uint64, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.CopyFileRange").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		fs.log(l, "CopyFileRange (%s,%d,%s,%d,%d): (%d,%s)", dst, doff, src, soff, size, written, errstr(err))
	}()
	var dfi, sfi *FileStat
	dfi, err = fs.resolve(ctx, dst, true)
	if err != 0 {
		return
	}
	sfi, err = fs.resolve(ctx, src, true)
	if err != 0 {
		return
	}
	err = fs.m.CopyFileRange(ctx, sfi.inode, soff, dfi.inode, doff, size, 0, &written, nil)
	return
}

func (fs *FileSystem) SetXattr(ctx meta.Context, p string, name string, value []byte, flags uint32) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.SetXattr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "SetXAttr (%s,%s,%d,%d): %s", p, name, len(value), flags, errstr(err)) }()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	err = fs.m.SetXattr(ctx, fi.inode, name, value, flags)
	return
}

func (fs *FileSystem) GetXattr(ctx meta.Context, p string, name string) (result []byte, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.GetXattr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "GetXattr (%s,%s): (%d,%s)", p, name, len(result), errstr(err)) }()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	err = fs.m.GetXattr(ctx, fi.inode, name, &result)
	return
}

func (fs *FileSystem) ListXattr(ctx meta.Context, p string) (names []byte, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.ListXattr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "ListXattr (%s): (%d,%s)", p, len(names), errstr(err)) }()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	err = fs.m.ListXattr(ctx, fi.inode, &names)
	return
}

func (fs *FileSystem) RemoveXattr(ctx meta.Context, p string, name string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.RemoveXattr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "RemoveXattr (%s,%s): %s", p, name, errstr(err)) }()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	err = fs.m.RemoveXattr(ctx, fi.inode, name)
	return
}

func (fs *FileSystem) GetFacl(ctx meta.Context, p string, acltype uint8, rule *acl.Rule) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.GetFacl").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "GetFacl (%s,%d): %s", p, acltype, errstr(err)) }()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	err = fs.m.GetFacl(ctx, fi.inode, acltype, rule)
	return
}

func (fs *FileSystem) SetFacl(ctx meta.Context, p string, acltype uint8, rule *acl.Rule) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.SetFacl").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		fs.log(l, "SetFacl (%s,%d,%v): %s", p, acltype, rule, errstr(err))
	}()
	fi, err := fs.resolve(ctx, p, true)
	if err != 0 {
		return
	}
	if acltype == acl.TypeDefault && fi.Mode().IsRegular() {
		if rule.IsEmpty() {
			return
		} else {
			return syscall.ENOTSUP
		}
	}
	if rule.IsEmpty() {
		oldRule := acl.EmptyRule()
		if err = fs.m.GetFacl(ctx, fi.inode, acltype, oldRule); err != 0 {
			return err
		}
		rule.Owner = oldRule.Owner
		rule.Other = oldRule.Other
		rule.Group = oldRule.Group & oldRule.Mask
	}
	err = fs.m.SetFacl(ctx, fi.inode, acltype, rule)
	fs.InvalidateAttr(fi.inode)
	return
}

func (fs *FileSystem) lookup(ctx meta.Context, parent Ino, name string, inode *Ino, attr *Attr) (err syscall.Errno) {
	now := time.Now()
	if fs.conf.DirEntryTimeout > 0 || fs.conf.EntryTimeout > 0 {
		fs.cacheM.Lock()
		es, ok := fs.entries[parent]
		if ok {
			e, ok := es[name]
			if ok {
				if now.Before(e.expire) {
					ac := fs.attrs[e.inode]
					fs.cacheM.Unlock()
					*inode = e.inode
					if ac == nil || now.After(ac.expire) {
						err = fs.m.GetAttr(ctx, e.inode, attr)
						if err == 0 && fs.conf.AttrTimeout > 0 {
							fs.cacheM.Lock()
							fs.attrs[e.inode] = &attrCache{*attr, now.Add(fs.conf.AttrTimeout)}
							fs.cacheM.Unlock()
						}
					} else {
						*attr = ac.attr
					}
					return err
				}
				delete(es, name)
				if len(es) == 0 {
					delete(fs.entries, parent)
				}
			}
		}
		fs.cacheM.Unlock()
	}

	err = fs.m.Lookup(ctx, parent, name, inode, attr, false)
	if err == 0 && (fs.conf.DirEntryTimeout > 0 && attr.Typ == meta.TypeDirectory || fs.conf.EntryTimeout > 0 && attr.Typ != meta.TypeDirectory) {
		fs.cacheM.Lock()
		if fs.conf.AttrTimeout > 0 {
			fs.attrs[*inode] = &attrCache{*attr, now.Add(fs.conf.AttrTimeout)}
		}
		es, ok := fs.entries[parent]
		if !ok {
			es = make(map[string]*entryCache)
			fs.entries[parent] = es
		}
		var expire time.Time
		if attr.Typ == meta.TypeDirectory {
			expire = now.Add(fs.conf.DirEntryTimeout)
		} else {
			expire = now.Add(fs.conf.EntryTimeout)
		}
		es[name] = &entryCache{*inode, attr.Typ, expire}
		fs.cacheM.Unlock()
	}
	// TODO: support for `negative_dentry_cache`?
	return err
}

func (fs *FileSystem) resolve(ctx meta.Context, p string, followLastSymlink bool) (fi *FileStat, err syscall.Errno) {
	fi, err = fs.doResolve(ctx, p, followLastSymlink, false)
	if fs.mayBeStale(err) {
		fi, err = fs.doResolve(ctx, p, followLastSymlink, true)
	}
	return
}

// mayBeStale reports whether err may come from stale caches, since cached
// entries can only allow but not deny. Such a path is resolved again with the
// caches revalidated, like LOOKUP_REVAL in Linux.
func (fs *FileSystem) mayBeStale(err syscall.Errno) bool {
	return (err == syscall.EACCES || err == syscall.ENOTDIR) && (fs.conf.DirEntryTimeout > 0 || fs.conf.EntryTimeout > 0)
}

// lastType classifies the last component of a path, as LAST_* in Linux namei.
type lastType uint8

const (
	lastNorm lastType = iota
	lastDot
	lastDotDot
	lastRoot
)

// nameData is a path resolved up to its last component.
type nameData struct {
	dir   *FileStat // the parent directory, nil for lastRoot
	name  string
	typ   lastType
	slash bool // the last component has trailing slashes
}

// rmdirErr returns the error of rmdir(2) on ".", ".." or "/".
func (nd nameData) rmdirErr() syscall.Errno {
	switch nd.typ {
	case lastDot:
		return syscall.EINVAL
	case lastDotDot:
		return syscall.ENOTEMPTY
	case lastRoot:
		return syscall.EBUSY
	}
	return 0
}

// resolveParent resolves all but the last component of p, like filename_parentat()
// in Linux. The parent must be a searchable directory. The last component is only
// classified, so callers must reject ".", ".." and "/" with their own errors
// before passing the name to the metadata engine.
func (fs *FileSystem) resolveParent(ctx meta.Context, p string) (nd nameData, err syscall.Errno) {
	if p == "" {
		return nd, syscall.ENOENT
	}
	trimmed := strings.TrimRight(p, "/")
	nd.slash = len(trimmed) < len(p)
	if trimmed == "" {
		nd.typ = lastRoot
		return nd, 0
	}
	dir := parentDir(trimmed)
	for _, reval := range []bool{false, true} {
		if nd.dir, err = fs.doResolve(ctx, dir, true, reval); err == 0 {
			if !nd.dir.IsDir() {
				err = syscall.ENOTDIR
			} else {
				err = fs.m.Access(ctx, nd.dir.inode, meta.MODE_MASK_X, nd.dir.attr)
			}
		}
		if !fs.mayBeStale(err) {
			break
		}
	}
	if err != 0 {
		return
	}
	switch nd.name = path.Base(trimmed); nd.name {
	case ".":
		nd.typ = lastDot
	case "..":
		nd.typ = lastDotDot
	}
	return nd, 0
}

func (fs *FileSystem) allowedSubdir(p string, allowAncestor bool) bool {
	if len(fs.subdirPrefixes) == 0 {
		return true
	}
	for _, prefix := range fs.subdirPrefixes {
		prefix = strings.TrimRight(prefix, "/")
		if p == prefix || strings.HasPrefix(p, prefix+"/") ||
			allowAncestor && (p == "/" || strings.HasPrefix(prefix, p+"/")) {
			return true
		}
	}
	return false
}

// doResolve resolves p. With reval, cached entries are dropped before lookup.
func (fs *FileSystem) doResolve(ctx meta.Context, p string, followLastSymlink, reval bool) (fi *FileStat, err syscall.Errno) {
	if p == "" {
		return nil, syscall.ENOENT
	}
	var inode Ino
	var attr = &Attr{}

	// The root is not resolved here, so it's always RootInode even if the metadata engine is chrooted.
	if fs.conf.FastResolve && len(fs.subdirPrefixes) == 0 && p != "/" && strings.HasPrefix(p, "/") && path.Clean(p) == p {
		err = fs.m.Resolve(ctx, 1, p, &inode, attr, false)
		if err == 0 {
			fi = AttrToFileInfo(inode, attr)
			p = strings.TrimRight(p, "/")
			ss := strings.Split(p, "/")
			fi.name = ss[len(ss)-1]
			if fi.IsSymlink() && followLastSymlink {
				// fast resolve can't follow symlink
				err = syscall.ENOTSUP
			}
		}
		if err != syscall.ENOTSUP {
			return
		}
	}

	// Fallback to the default implementation that calls `fs.m.Lookup` for each directory along the path.
	// It might be slower for deep directories, but it works for every meta that implements `Lookup`.
	type dirStep struct {
		ino  Ino
		attr Attr
		path string
	}
	var stack []dirStep
	parent := Ino(1)
	resolvedPath := "/"
	symlinks := 0
	ss := strings.Split(p, "/")
	for i := 0; i < len(ss); i++ {
		name := ss[i]
		isLastComponent := i == len(ss)-1
		if parent > 1 && attr.Typ != meta.TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		if len(name) == 0 {
			continue
		}
		if parent == meta.RootInode && isLastComponent && vfs.IsSpecialName(name) {
			inode, pattr := vfs.GetInternalNodeByName(name)
			parent, *attr = inode, *pattr
			resolvedPath = "/" + name
			break
		}
		if parent > 1 {
			if err := fs.m.Access(ctx, parent, meta.MODE_MASK_X, attr); err != 0 {
				return nil, err
			}
		}
		if name == "." {
			continue
		}
		if name == ".." {
			if n := len(stack); n > 0 {
				top := stack[n-1]
				stack = stack[:n-1]
				parent, *attr, resolvedPath = top.ino, top.attr, top.path
			}
			continue
		}

		nextPath := path.Join(resolvedPath, name)
		if !fs.allowedSubdir(nextPath, true) {
			return nil, syscall.EACCES
		}
		parentAttr := *attr
		if reval {
			fs.InvalidateEntry(parent, name)
		}
		err = fs.lookup(ctx, parent, name, &inode, attr)
		if err != 0 {
			return
		}
		if (!isLastComponent || followLastSymlink) && attr.Typ == meta.TypeSymlink {
			if symlinks++; symlinks > 40 {
				return nil, syscall.ELOOP
			}
			var buf []byte
			err = fs.m.ReadLink(ctx, inode, &buf)
			if err != 0 {
				return
			}
			target := string(buf)
			if strings.Contains(target, "://") {
				return &FileStat{name: target}, syscall.ENOTSUP
			}
			if strings.HasPrefix(target, "/") {
				mp := fs.conf.Mountpoint
				if !strings.HasSuffix(mp, "/") {
					mp += "/"
				}
				if strings.HasPrefix(target, mp) {
					target = target[len(mp):]
					parent, *attr, resolvedPath, stack = meta.RootInode, Attr{}, "/", stack[:0]
				} else {
					fi = AttrToFileInfo(inode, attr)
					fi.name = "file:" + target
					logger.Errorf("external link: %s -> %s", p, target)
					return fi, utils.ErrExtlink
				}
			} else {
				*attr = parentAttr
			}
			ss = append(strings.Split(target, "/"), ss[i+1:]...)
			i = -1
			continue
		}
		stack = append(stack, dirStep{parent, parentAttr, resolvedPath})
		parent = inode
		resolvedPath = nextPath
	}
	if !fs.allowedSubdir(resolvedPath, false) {
		return nil, syscall.EACCES
	}
	if parent == meta.RootInode {
		err = fs.m.GetAttr(ctx, parent, attr)
		if err != 0 {
			return
		}
	}
	fi = AttrToFileInfo(parent, attr)
	if base := path.Base(p); base != "/" {
		fi.name = base
	}
	return fi, 0
}

func (fs *FileSystem) Create(ctx meta.Context, p string, mode uint16, umask uint16) (f *File, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Create").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Create (%s,%o): %s", p, mode, errstr(err)) }()
	nd, err := fs.resolveParent(ctx, p)
	if err != 0 {
		return
	}
	if nd.typ != lastNorm {
		return nil, syscall.EEXIST
	}
	if nd.slash {
		return nil, syscall.EISDIR
	}
	var inode Ino
	var attr = &Attr{}
	err = fs.m.Create(ctx, nd.dir.inode, nd.name, mode&07777, umask, syscall.O_EXCL, &inode, attr)
	if err == syscall.ENOENT && nd.dir.inode != 1 {
		// dir be moved into trash, try again
		if fs.conf.DirEntryTimeout > 0 {
			parent := parentDir(p)
			if fi, err := fs.resolve(ctx, parentDir(parent), true); err == 0 {
				fs.InvalidateEntry(fi.inode, path.Base(parent))
			}
		}
		if fi2, e := fs.resolve(ctx, parentDir(p), true); e != 0 {
			return nil, e
		} else if fi2.inode != nd.dir.inode {
			err = fs.m.Create(ctx, fi2.inode, nd.name, mode&07777, umask, syscall.O_EXCL, &inode, attr)
		}
	}
	if err == 0 {
		fi := AttrToFileInfo(inode, attr)
		fi.name = nd.name
		f = &File{}
		f.flags = vfs.MODE_MASK_W
		f.path = p
		f.inode = fi.inode
		f.info = fi
		f.fs = fs
	}
	fs.InvalidateEntry(nd.dir.inode, nd.name)
	return
}

func (fs *FileSystem) Flush() error {
	buffer := fs.logBuffer
	if buffer != nil {
		buffer <- "" // flush
	}
	fs.Meta().FlushSession()
	return nil
}

func (fs *FileSystem) Close() error {
	_ = fs.Flush()
	buffer := fs.logBuffer
	if buffer != nil {
		fs.logBuffer = nil
		close(buffer)
	}
	return fs.Meta().CloseSession()
}

func (fs *FileSystem) Clone(ctx meta.Context, src, dst string, preserve bool) (err syscall.Errno) {
	snd, err := fs.resolveParent(ctx, src)
	if err != 0 {
		return
	}
	// The source is looked up as lookup_last() in Linux: a plain name is an entry
	// of the parent, anything else names a directory.
	var srcParent, srcIno Ino
	var srcAttr Attr
	if snd.typ == lastNorm && !snd.slash {
		srcParent = snd.dir.inode
		err = fs.lookup(ctx, srcParent, snd.name, &srcIno, &srcAttr)
	} else if fi, e := fs.resolve(ctx, src, false); e != 0 {
		err = e
	} else if fi.inode == meta.RootInode {
		err = syscall.EINVAL // the destination is always under the root
	} else {
		srcParent, srcIno, srcAttr = fi.attr.Parent, fi.inode, *fi.attr
	}
	if err != 0 {
		return
	}
	dnd, err := fs.resolveParent(ctx, dst)
	if err != 0 {
		return
	}
	if err = fs.checkCreate(ctx, dnd, srcAttr.Typ == meta.TypeDirectory); err != 0 {
		return
	}

	var count, total uint64
	umask := uint16(utils.GetUmask())

	var cmode uint8
	if preserve {
		cmode |= meta.CLONE_MODE_PRESERVE_ATTR
	}

	if err = fs.m.Clone(meta.NewContext(ctx.Pid(), ctx.Uid(), ctx.Gids()), srcParent, srcIno, dnd.dir.inode, dnd.name, cmode, umask, meta.CLONE_DEFAULT_CONCURRENCY, &count, &total); err != 0 {
		logger.Errorf("clone failed srcIno:%d,dstParentIno:%d,dstName:%s,cmode:%d,umask:%d,eno:%v", srcIno, dnd.dir.inode, dnd.name, cmode, umask, err)
	}
	return
}

func (fs *FileSystem) Warmup(ctx meta.Context, paths []string, numthreads int, background bool, isEvict bool, isCheck bool, resp *vfs.CacheResponse) {
	action := vfs.WarmupCache
	if isEvict {
		action = vfs.EvictCache
	}
	if isCheck {
		action = vfs.CheckCache
	}

	if background {
		go fs.cacheFiller.Cache(meta.NewContext(ctx.Pid(), ctx.Uid(), ctx.Gids()), action, paths, int(numthreads), resp)
	} else {
		fs.cacheFiller.Cache(meta.NewContext(ctx.Pid(), ctx.Uid(), ctx.Gids()), action, paths, int(numthreads), resp)
	}
}

func (fs *FileSystem) HandleQuota(ctx meta.Context, key string, cmd uint8, capacity, inodes uint64, strict, repair, create bool) (qs map[string]*meta.Quota, err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() {
		fs.log(l, "QuotaCtl (%s,%d,%d,%d,%t,%t,%t): %s", key, cmd, capacity, inodes, create, repair, strict, errstr(err))
	}()
	if cmd == meta.QuotaSet && capacity == 0 && inodes == 0 {
		return nil, syscall.EINVAL
	}
	var qtype uint32
	var qkey string
	if strings.HasPrefix(key, "uid:") {
		qtype = meta.UserQuotaType
		qkey = key[4:]
	} else if strings.HasPrefix(key, "gid:") {
		qtype = meta.GroupQuotaType
		qkey = key[4:]
	} else if key != "" {
		qtype = meta.DirQuotaType
		qkey = key
	} else {
		qkey = ""
		if cmd == meta.QuotaList {
			qtype = meta.AllQuotaType
		} else if cmd == meta.QuotaCheck {
			qtype = meta.UserQuotaType
		} else {
			return nil, syscall.EINVAL
		}
	}

	qs = make(map[string]*meta.Quota)
	if cmd == meta.QuotaSet {
		q := &meta.Quota{MaxSpace: -1, MaxInodes: -1} // negative means no change
		if capacity > 0 {
			q.MaxSpace = int64(capacity)
		}
		if inodes > 0 {
			q.MaxInodes = int64(inodes)
		}
		qs[qkey] = q
	}
	if _err := fs.m.HandleQuota(meta.Background(), cmd, qkey, qtype, qs, strict, repair, create); _err != nil {
		if strings.HasPrefix(_err.Error(), "no quota for inode") {
			return qs, 0
		}
		var eno syscall.Errno
		if errors.As(_err, &eno) {
			err = eno
		} else {
			err = syscall.EINVAL
		}
	}
	return
}

// File

func (f *File) FS() *FileSystem {
	return f.fs
}

func (f *File) Inode() Ino {
	return f.inode
}

func (f *File) Name() string {
	return f.path
}

func (f *File) Stat() (fi os.FileInfo, err error) {
	return f.info, nil
}

func (f *File) Chmod(ctx meta.Context, mode uint16) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Chmod").End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Chmod (%s,%o): %s", f.path, mode, errstr(err)) }()
	var attr = Attr{Mode: mode}
	err = f.fs.m.SetAttr(ctx, f.inode, meta.SetAttrMode, 0, &attr)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Chown(ctx meta.Context, uid uint32, gid uint32) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Chown").End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Chown (%s,%d,%d): %s", f.path, uid, gid, errstr(err)) }()
	var flag uint16
	if uid != uint32(f.info.Uid()) {
		flag |= meta.SetAttrUID
	}
	if gid != uint32(f.info.Gid()) {
		flag |= meta.SetAttrGID
	}
	var attr = Attr{Uid: uid, Gid: gid}
	err = f.fs.m.SetAttr(ctx, f.inode, flag, 0, &attr)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Utime(ctx meta.Context, atime, mtime int64) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Utime").End()
	var flag uint16
	if atime >= 0 {
		flag |= meta.SetAttrAtime
	}
	if mtime >= 0 {
		flag |= meta.SetAttrMtime
	}
	if flag == 0 {
		return 0
	}
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Utime (%s,%d,%d): %s", f.path, atime, mtime, errstr(err)) }()
	var attr Attr
	attr.Atime = atime / 1000
	attr.Atimensec = uint32(atime%1000) * 1e6
	attr.Mtime = mtime / 1000
	attr.Mtimensec = uint32(mtime%1000) * 1e6
	err = f.fs.m.SetAttr(ctx, f.inode, flag, 0, &attr)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Utime2(ctx meta.Context, atimeSec, atimeNSec, mtimeSec, mtimeNsec int64) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Utime2").End()
	var flag uint16
	if atimeSec >= 0 || atimeNSec >= 0 {
		flag |= meta.SetAttrAtime
	}
	if mtimeSec >= 0 || mtimeNsec >= 0 {
		flag |= meta.SetAttrMtime
	}
	if flag == 0 {
		return 0
	}
	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "Utime2 (%s,%d,%d,%d,%d): %s", f.path, atimeSec, atimeNSec, mtimeSec, mtimeNsec, errstr(err))
	}()
	var attr Attr
	attr.Atime = atimeSec
	attr.Atimensec = uint32(atimeNSec)
	attr.Mtime = mtimeSec
	attr.Mtimensec = uint32(mtimeNsec)
	err = f.fs.m.SetAttr(ctx, f.inode, flag, 0, &attr)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Seek(ctx meta.Context, offset int64, whence int) (int64, error) {
	defer trace.StartRegion(context.TODO(), "fs.Seek").End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Seek (%s,%d,%d): %d", f.path, offset, whence, f.offset) }()
	f.Lock()
	defer f.Unlock()
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = f.info.Size() + offset
	}
	return f.offset, nil
}

func (f *File) Read(ctx meta.Context, b []byte) (n int, err error) {
	_, task := trace.NewTask(context.TODO(), "Read")
	defer task.End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Read (%s,%d): (%d,%s)", f.path, len(b), n, errstr(err)) }()
	f.Lock()
	defer f.Unlock()
	n, err = f.pread(ctx, b, f.offset)
	f.offset += int64(n)
	return
}

func (f *File) Pread(ctx meta.Context, b []byte, offset int64) (n int, err error) {
	_, task := trace.NewTask(context.TODO(), "Pread")
	defer task.End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Pread (%s,%d,%d): (%d,%s)", f.path, len(b), offset, n, errstr(err)) }()
	f.Lock()
	defer f.Unlock()
	n, err = f.pread(ctx, b, offset)
	return
}

func (f *File) pread(ctx meta.Context, b []byte, offset int64) (n int, err error) {
	if offset >= f.info.Size() {
		return 0, io.EOF
	}
	if int64(len(b))+offset > f.info.Size() {
		b = b[:f.info.Size()-offset]
	}
	if f.data != nil {
		n := copy(b, f.data[offset:])
		return n, nil
	}
	if f.wdata != nil {
		eno := f.wdata.Flush(ctx)
		if eno != 0 {
			err = eno
			return
		}
	}
	if f.rdata == nil {
		f.rdata = f.fs.reader.Open(f.inode, uint64(f.info.Size()))
	}

	got, eno := f.rdata.Read(ctx, uint64(offset), b)
	for eno == syscall.EAGAIN {
		got, eno = f.rdata.Read(ctx, uint64(offset), b)
	}
	if eno != 0 {
		err = eno
		return
	}
	if got == 0 {
		return 0, io.EOF
	}
	f.fs.readSizeHistogram.Observe(float64(got))
	return got, nil
}

func (f *File) Write(ctx meta.Context, b []byte) (n int, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Write").End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Write (%s,%d): (%d,%s)", f.path, len(b), n, errstr(err)) }()
	f.Lock()
	defer f.Unlock()
	n, err = f.pwrite(ctx, b, f.offset)
	f.offset += int64(n)
	return
}

func (f *File) Pwrite(ctx meta.Context, b []byte, offset int64) (n int, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Pwrite").End()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Pwrite (%s,%d,%d): (%d,%s)", f.path, len(b), offset, n, errstr(err)) }()
	f.Lock()
	defer f.Unlock()
	n, err = f.pwrite(ctx, b, offset)
	return
}

func (f *File) pwrite(ctx meta.Context, b []byte, offset int64) (n int, err syscall.Errno) {
	if f.wdata == nil {
		f.wdata = f.fs.writer.Open(f.inode, uint64(f.info.Size()), f.info.attr.Tier)
	}
	err = f.wdata.Write(ctx, uint64(offset), b)
	if err != 0 {
		_ = f.wdata.Close(meta.Background())
		f.wdata = nil
		return
	}
	if offset+int64(len(b)) > int64(f.info.attr.Length) {
		f.info.attr.Length = uint64(offset + int64(len(b)))
	}
	f.fs.writtenSizeHistogram.Observe(float64(len(b)))
	return len(b), 0
}

func (f *File) Truncate(ctx meta.Context, length uint64) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Truncate").End()
	f.Lock()
	defer f.Unlock()
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Truncate (%s,%d): %s", f.path, length, errstr(err)) }()
	if f.wdata != nil {
		err = f.wdata.Flush(ctx)
		if err != 0 {
			return
		}
	}
	err = f.fs.m.Truncate(ctx, f.inode, 0, length, nil, false)
	if err == 0 {
		_ = f.fs.m.InvalidateChunkCache(ctx, f.inode, uint32(((length - 1) >> meta.ChunkBits)))
		f.fs.writer.Truncate(f.inode, length)
		f.fs.reader.Truncate(f.inode, length)
		f.info.attr.Length = length
		f.fs.InvalidateAttr(f.inode)
	}
	return
}

func (f *File) Flush(ctx meta.Context) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Flush").End()
	f.Lock()
	defer f.Unlock()
	if f.wdata == nil {
		return
	}
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Flush (%s): %s", f.path, errstr(err)) }()
	err = f.wdata.Flush(ctx)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Fsync(ctx meta.Context) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Fsync").End()
	f.Lock()
	defer f.Unlock()
	if f.wdata == nil {
		return 0
	}
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Fsync (%s): %s", f.path, errstr(err)) }()
	err = f.wdata.Flush(ctx)
	f.fs.InvalidateAttr(f.inode)
	return
}

func (f *File) Close(ctx meta.Context) (err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Close (%s): %s", f.path, errstr(err)) }()
	f.Lock()
	defer f.Unlock()
	if f.flags != 0 && !f.info.IsDir() {
		f.offset = 0
		if f.rdata != nil {
			rdata := f.rdata
			f.rdata = nil
			time.AfterFunc(time.Second, func() {
				rdata.Close(meta.Background())
			})
		}
		if f.wdata != nil {
			err = f.wdata.Close(meta.Background())
			f.fs.InvalidateAttr(f.inode)
			f.wdata = nil
		}
		_ = f.fs.m.Close(ctx, f.inode)
	}
	return
}

func (f *File) Readdir(ctx meta.Context, count int) (fi []os.FileInfo, err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Readdir (%s,%d): (%s,%d)", f.path, count, errstr(err), len(fi)) }()
	if !f.info.IsDir() {
		return nil, syscall.ENOTDIR
	}
	f.Lock()
	defer f.Unlock()
	fi = f.dircache
	if fi == nil {
		var inodes []*meta.Entry
		err = f.fs.m.Readdir(ctx, f.inode, 1, &inodes)
		if err != 0 {
			return
		}
		if f.fs.conf.Meta.SortDir {
			sort.Slice(inodes[2:], func(i, j int) bool {
				return string(inodes[i].Name) < string(inodes[j].Name)
			})
		}
		// skip . and ..
		for _, n := range inodes[2:] {
			i := AttrToFileInfo(n.Inode, n.Attr)
			i.name = string(n.Name)
			fi = append(fi, i)
		}
		f.dircache = fi
	}

	if len(fi) < int(f.offset) {
		return nil, 0
	}
	fi = fi[f.offset:]
	if count > 0 && len(fi) > count {
		fi = fi[:count]
	}
	f.offset += int64(len(fi))
	return
}

func (f *File) ReaddirPlus(ctx meta.Context, offset int) (entries []*meta.Entry, err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "ReaddirPlus (%s,%d): (%s,%d)", f.path, offset, errstr(err), len(entries)) }()
	if !f.info.IsDir() {
		return nil, syscall.ENOTDIR
	}
	f.Lock()
	defer f.Unlock()
	if f.entries == nil {
		var es []*meta.Entry
		err = f.fs.m.Readdir(ctx, f.inode, 1, &es)
		if err != 0 {
			return
		}
		// filter out . and ..
		f.entries = make([]*meta.Entry, 0, len(es))
		for _, e := range es {
			if !bytes.Equal(e.Name, []byte{'.'}) && !bytes.Equal(e.Name, []byte("..")) {
				f.entries = append(f.entries, e)
			}
		}
		if f.fs.conf.Meta.SortDir {
			sort.Slice(f.entries, func(i, j int) bool {
				return string(f.entries[i].Name) < string(f.entries[j].Name)
			})
		}
	}
	if offset >= len(f.entries) {
		offset = len(f.entries)
	}
	entries = f.entries[offset:]
	return
}

func (f *File) Summary(ctx meta.Context, recursive, strict bool) (s *meta.Summary, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Summary").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "Summary (%s): %s (%d,%d,%d,%d)", f.path, errstr(err), s.Length, s.Size, s.Files, s.Dirs)
	}()
	s = &meta.Summary{}
	err = f.fs.m.GetSummary(ctx, f.inode, s, recursive, strict)
	return
}

func (f *File) GetTreeSummary(ctx meta.Context, depth, entries uint8, strict bool) (s *meta.TreeSummary, err syscall.Errno) {
	s = &meta.TreeSummary{
		Inode: f.inode,
		Path:  "",
		Type:  meta.TypeDirectory,
	}

	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "GetTreeSummary (%s,%d,%d,%t): %s (%d,%d,%d)", f.path, depth, entries, strict, errstr(err), s.Size, s.Files, s.Dirs)
	}()
	err = f.fs.m.GetTreeSummary(ctx, s, depth, entries, strict, meta.SortBySize, nil)
	s.Path = path.Base(f.path)
	return
}

func (f *File) GetQuota(ctx meta.Context) (quota *meta.Quota, err error) {
	defer trace.StartRegion(context.TODO(), "fs.getQuota").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "getQuota (%s): %s", f.path, errstr(err))
	}()
	err = nil
	qs := make(map[string]*meta.Quota)
	// get filesystem quota if root
	if f.inode == meta.RootInode {
		format := f.fs.m.GetFormat()
		quota = &meta.Quota{
			MaxSpace:  int64(format.Capacity),
			MaxInodes: int64(format.Inodes),
		}
		return quota, err
	}
	// get directory quota
	err = f.fs.m.HandleQuota(ctx, meta.QuotaGet, f.path, meta.DirQuotaType, qs, false, false, false)
	if err != nil {
		return nil, err
	}
	quota = qs[f.path]
	return quota, err
}
