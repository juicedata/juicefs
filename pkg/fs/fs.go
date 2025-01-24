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
	"fmt"
	"io"
	"os"
	"path"
	"runtime/trace"
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
	conf   *vfs.Config
	reader vfs.DataReader
	writer vfs.DataWriter
	m      meta.Meta

	cacheM          sync.Mutex
	entries         map[Ino]map[string]*entryCache
	attrs           map[Ino]*attrCache
	checkAccessFile time.Duration
	rotateAccessLog int64
	logBuffer       chan string

	readSizeHistogram     prometheus.Histogram
	writtenSizeHistogram  prometheus.Histogram
	opsDurationsHistogram prometheus.Histogram
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
}

func NewFileSystem(conf *vfs.Config, m meta.Meta, d chunk.ChunkStore) (*FileSystem, error) {
	reader := vfs.NewDataReader(conf, m, d)
	fs := &FileSystem{
		m:               m,
		conf:            conf,
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
			Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
		}),
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

func (fs *FileSystem) invalidateEntry(parent Ino, name string) {
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

func (fs *FileSystem) invalidateAttr(ino Ino) {
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
	for {
		line := <-logBuffer
		buf = append(buf[:0], []byte(line)...)
	LOOP:
		for len(buf) < (128 << 10) {
			select {
			case line = <-logBuffer:
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

// parentDir returns parent of /foo/bar/ as /foo
func parentDir(p string) string {
	return path.Dir(strings.TrimRight(p, "/"))
}

func (fs *FileSystem) Mkdir(ctx meta.Context, p string, mode uint16, umask uint16) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Mkdir").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Mkdir (%s, %o): %s", p, mode, errstr(err)) }()
	if p == "/" {
		return syscall.EEXIST
	}
	fi, err := fs.resolve(ctx, parentDir(p), true)
	if err != 0 {
		return err
	}
	var inode Ino
	err = fs.m.Mkdir(ctx, fi.inode, path.Base(p), mode, umask, 0, &inode, nil)
	if err == syscall.ENOENT && fi.inode != 1 {
		// parent be moved into trash, try again
		if fs.conf.DirEntryTimeout > 0 {
			parent := parentDir(p)
			if fi, err := fs.resolve(ctx, parentDir(parent), true); err == 0 {
				fs.invalidateEntry(fi.inode, path.Base(parent))
			}
		}
		if fi2, e := fs.resolve(ctx, parentDir(p), true); e != 0 {
			return e
		} else if fi2.inode != fi.inode {
			err = fs.m.Mkdir(ctx, fi2.inode, path.Base(p), mode, umask, 0, &inode, nil)
		}
	}
	fs.invalidateEntry(fi.inode, path.Base(p))
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

func (fs *FileSystem) Delete(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Delete").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Delete (%s): %s", p, errstr(err)) }()
	parent, err := fs.resolve(ctx, parentDir(p), true)
	if err != 0 {
		return
	}
	fi, err := fs.resolve(ctx, p, false)
	if err != 0 {
		return
	}
	if fi.IsDir() {
		err = fs.m.Rmdir(ctx, parent.inode, path.Base(p))
	} else {
		err = fs.m.Unlink(ctx, parent.inode, path.Base(p))
	}
	fs.invalidateEntry(parent.inode, path.Base(p))
	return
}

func (fs *FileSystem) Rmr(ctx meta.Context, p string, numthreads int) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rmr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rmr (%s): %s", p, errstr(err)) }()
	parent, err := fs.resolve(ctx, parentDir(p), true)
	if err != 0 {
		return
	}
	err = fs.m.Remove(ctx, parent.inode, path.Base(p), false, numthreads, nil)
	fs.invalidateEntry(parent.inode, path.Base(p))
	return
}

func (fs *FileSystem) Rename(ctx meta.Context, oldpath string, newpath string, flags uint32) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rename").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rename (%s,%s,%d): %s", oldpath, newpath, flags, errstr(err)) }()
	oldfi, err := fs.resolve(ctx, parentDir(oldpath), true)
	if err != 0 {
		return
	}
	newfi, err := fs.resolve(ctx, parentDir(newpath), true)
	if err != 0 {
		return
	}
	err = fs.m.Rename(ctx, oldfi.inode, path.Base(oldpath), newfi.inode, path.Base(newpath), flags, nil, nil)
	fs.invalidateEntry(oldfi.inode, path.Base(oldpath))
	fs.invalidateEntry(newfi.inode, path.Base(newpath))
	return
}

func (fs *FileSystem) Link(ctx meta.Context, src string, dst string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Link").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Link (%s,%s): %s", src, dst, errstr(err)) }()

	fi, err := fs.resolve(ctx, src, false)
	if err != 0 {
		return
	}
	pi, err := fs.resolve(ctx, parentDir(dst), true)
	if err != 0 {
		return
	}
	err = fs.m.Link(ctx, fi.inode, pi.inode, path.Base(dst), nil)
	fs.invalidateEntry(pi.inode, path.Base(dst))
	return
}

func (fs *FileSystem) Symlink(ctx meta.Context, target string, link string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Symlink").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Symlink (%s,%s): %s", target, link, errstr(err)) }()
	if strings.HasSuffix(link, "/") {
		return syscall.EINVAL
	}
	fi, err := fs.resolve(ctx, parentDir(link), true)
	if err != 0 {
		return
	}
	err = fs.m.Symlink(ctx, fi.inode, path.Base(link), target, nil, nil)
	fs.invalidateEntry(fi.inode, path.Base(link))
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
	return err
}

func (fs *FileSystem) resolve(ctx meta.Context, p string, followLastSymlink bool) (fi *FileStat, err syscall.Errno) {
	return fs.doResolve(ctx, p, followLastSymlink, make(map[Ino]struct{}))
}

func (fs *FileSystem) doResolve(ctx meta.Context, p string, followLastSymlink bool, visited map[Ino]struct{}) (fi *FileStat, err syscall.Errno) {
	var inode Ino
	var attr = &Attr{}

	if fs.conf.FastResolve {
		err = fs.m.Resolve(ctx, 1, p, &inode, attr)
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
	parent := Ino(1)
	ss := strings.Split(p, "/")
	for i, name := range ss {
		if len(name) == 0 {
			continue
		}
		if parent == meta.RootInode && i == len(ss)-1 && vfs.IsSpecialName(name) {
			inode, attr := vfs.GetInternalNodeByName(name)
			fi = AttrToFileInfo(inode, attr)
			parent = inode
			break
		}
		if parent > 1 {
			if (name == "." || name == "..") && attr.Typ != meta.TypeDirectory {
				return nil, syscall.ENOTDIR
			}
			if err := fs.m.Access(ctx, parent, meta.MODE_MASK_X, attr); err != 0 {
				return nil, err
			}
		}

		var inode Ino
		var resolved bool

		err = fs.lookup(ctx, parent, name, &inode, attr)
		if i == len(ss)-1 {
			resolved = true
		}
		if err != 0 {
			return
		}
		fi = AttrToFileInfo(inode, attr)
		if (!resolved || followLastSymlink) && fi.IsSymlink() {
			if _, ok := visited[inode]; ok {
				logger.Errorf("find a loop symlink: %d", inode)
				return nil, syscall.ELOOP
			} else {
				visited[inode] = struct{}{}
			}
			var buf []byte
			err = fs.m.ReadLink(ctx, inode, &buf)
			if err != 0 {
				return
			}
			target := string(buf)
			if strings.HasPrefix(target, "/") || strings.Contains(target, "://") {
				return &FileStat{name: target}, syscall.ENOTSUP
			}
			target = path.Join(strings.Join(ss[:i], "/"), target)
			fi, err = fs.doResolve(ctx, target, followLastSymlink, visited)
			if err != 0 {
				return
			}
			inode = fi.Inode()
			attr = fi.attr
		}
		fi.name = name
		parent = inode
	}
	if parent == meta.RootInode {
		err = fs.m.GetAttr(ctx, parent, attr)
		if err != 0 {
			return
		}
		fi = AttrToFileInfo(1, attr)
	}
	return fi, 0
}

func (fs *FileSystem) Create(ctx meta.Context, p string, mode uint16, umask uint16) (f *File, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Create").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Create (%s,%o): %s", p, mode, errstr(err)) }()
	if strings.HasSuffix(p, "/") {
		return nil, syscall.EINVAL
	}
	var inode Ino
	var attr = &Attr{}
	var fi *FileStat
	fi, err = fs.resolve(ctx, parentDir(p), true)
	if err != 0 {
		return
	}
	err = fs.m.Create(ctx, fi.inode, path.Base(p), mode&07777, umask, syscall.O_EXCL, &inode, attr)
	if err == syscall.ENOENT && fi.inode != 1 {
		// dir be moved into trash, try again
		if fs.conf.DirEntryTimeout > 0 {
			parent := parentDir(p)
			if fi, err := fs.resolve(ctx, parentDir(parent), true); err == 0 {
				fs.invalidateEntry(fi.inode, path.Base(parent))
			}
		}
		if fi2, e := fs.resolve(ctx, parentDir(p), true); e != 0 {
			return nil, e
		} else if fi2.inode != fi.inode {
			err = fs.m.Create(ctx, fi2.inode, path.Base(p), mode&07777, umask, syscall.O_EXCL, &inode, attr)
		}
	}
	if err == 0 {
		fi = AttrToFileInfo(inode, attr)
		fi.name = path.Base(p)
		f = &File{}
		f.flags = vfs.MODE_MASK_W
		f.path = p
		f.inode = fi.inode
		f.info = fi
		f.fs = fs
	}
	fs.invalidateEntry(fi.inode, path.Base(p))
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
	return nil
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
	f.fs.invalidateAttr(f.inode)
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
	f.fs.invalidateAttr(f.inode)
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
	f.fs.invalidateAttr(f.inode)
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
		f.wdata = f.fs.writer.Open(f.inode, uint64(f.info.Size()))
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
		f.fs.invalidateAttr(f.inode)
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
	f.fs.invalidateAttr(f.inode)
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
	f.fs.invalidateAttr(f.inode)
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
			f.fs.invalidateAttr(f.inode)
			f.wdata = nil
		}
		_ = f.fs.m.Close(ctx, f.inode)
	}
	return
}

func (f *File) Readdir(ctx meta.Context, count int) (fi []os.FileInfo, err syscall.Errno) {
	l := vfs.NewLogContext(ctx)
	defer func() { f.fs.log(l, "Readdir (%s,%d): (%s,%d)", f.path, count, errstr(err), len(fi)) }()
	f.Lock()
	defer f.Unlock()
	fi = f.dircache
	if fi == nil {
		var inodes []*meta.Entry
		err = f.fs.m.Readdir(ctx, f.inode, 1, &inodes)
		if err != 0 {
			return
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
	}
	if offset >= len(f.entries) {
		offset = len(f.entries)
	}
	entries = f.entries[offset:]
	return
}

func (f *File) Summary(ctx meta.Context) (s *meta.Summary, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Summary").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "Summary (%s): %s (%d,%d,%d,%d)", f.path, errstr(err), s.Length, s.Size, s.Files, s.Dirs)
	}()
	s = &meta.Summary{}
	err = f.fs.m.GetSummary(ctx, f.inode, s, true, true)
	return
}
