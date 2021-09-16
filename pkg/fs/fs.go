/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package fs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/trace"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

var logger = utils.GetLogger("juicefs")

const rotateAccessLog = 300 << 20 // 300 MiB

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
	switch attr.Typ {
	case meta.TypeDirectory:
		mode |= os.ModeDir
	case meta.TypeSymlink:
		mode |= os.ModeSymlink
	case meta.TypeFile:
	default:
		mode = 0
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

func (fs *FileStat) Atime() int64 { return int64(fs.attr.Atime*1000) + int64(fs.attr.Atimensec/1e6) }
func (fs *FileStat) Mtime() int64 { return int64(fs.attr.Mtime*1000) + int64(fs.attr.Mtimensec/1e6) }

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

	cacheM  sync.Mutex
	entries map[Ino]map[string]*entryCache
	attrs   map[Ino]*attrCache

	logBuffer chan string
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
		m:       m,
		conf:    conf,
		reader:  reader,
		writer:  vfs.NewDataWriter(conf, m, d, reader),
		entries: make(map[meta.Ino]map[string]*entryCache),
		attrs:   make(map[meta.Ino]*attrCache),
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
	opsDurationsHistogram.Observe(used.Seconds())
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
		if lastcheck.Add(time.Minute).After(time.Now()) {
			continue
		}
		lastcheck = time.Now()
		var fi os.FileInfo
		fi, err = f.Stat()
		if err == nil && fi.Size() > rotateAccessLog {
			f.Close()
			fi, err = os.Stat(path)
			if err == nil && fi.Size() > rotateAccessLog {
				tmp := fmt.Sprintf("%s.%p", path, fs)
				if os.Rename(path, tmp) == nil {
					for i := 6; i > 0; i-- {
						_ = os.Rename(path+"."+strconv.Itoa(i), path+"."+strconv.Itoa(i+1))
					}
					_ = os.Rename(tmp, path+".1")
				} else {
					fi, err = os.Stat(path)
					if err == nil && fi.Size() > rotateAccessLog*7 {
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
	_ = fs.m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail)
	return
}

func (fs *FileSystem) Open(ctx meta.Context, path string, flags uint32) (f *File, err syscall.Errno) {
	_, task := trace.NewTask(context.TODO(), "Open")
	defer task.End()
	l := vfs.NewLogContext(ctx)
	if flags != 0 {
		defer func() { fs.log(l, "Open (%s,%d): %s", path, flags, errstr(err)) }()
	} else {
		defer func() { fs.log(l, "Lookup (%s): %s", path, errstr(err)) }()
	}
	var fi *FileStat
	fi, err = fs.resolve(ctx, path, true)
	if err != 0 {
		return
	}

	if flags != 0 && !fi.IsDir() {
		err = fs.m.Access(ctx, fi.inode, uint8(flags), fi.attr)
		if err != 0 {
			return nil, err
		}
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
	return fs.resolve(ctx, path, false)
}

func (fs *FileSystem) Mkdir(ctx meta.Context, p string, mode uint16) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Mkdir").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Mkdir (%s, %o): %s", p, mode, errstr(err)) }()
	if p == "/" {
		return syscall.EEXIST
	}
	fi, err := fs.resolve(ctx, path.Dir(p), true)
	if err != 0 {
		return err
	}
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
	if err != 0 {
		return err
	}
	var inode Ino
	err = fs.m.Mkdir(ctx, fi.inode, path.Base(p), mode, 0, 0, &inode, nil)
	fs.invalidateEntry(fi.inode, path.Base(p))
	return
}

func (fs *FileSystem) Delete(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Delete").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Delete (%s): %s", p, errstr(err)) }()
	parent, err := fs.resolve(ctx, path.Dir(p), true)
	if err != 0 {
		return
	}
	fi, err := fs.resolve(ctx, p, false)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, parent.inode, mMaskW, parent.attr)
	if err != 0 {
		return err
	}
	if fi.IsDir() {
		err = fs.m.Rmdir(ctx, parent.inode, path.Base(p))
	} else {
		err = fs.m.Unlink(ctx, parent.inode, path.Base(p))
	}
	fs.invalidateEntry(parent.inode, path.Base(p))
	return
}

func (fs *FileSystem) Rmr(ctx meta.Context, p string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rmr").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rmr (%s): %s", p, errstr(err)) }()
	parent, err := fs.resolve(ctx, path.Dir(p), true)
	if err != 0 {
		return
	}
	err = meta.Remove(fs.m, ctx, parent.inode, path.Base(p))
	fs.invalidateEntry(parent.inode, path.Base(p))
	return
}

func (fs *FileSystem) Rename(ctx meta.Context, oldpath string, newpath string, flags uint32) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Rename").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Rename (%s,%s,%d): %s", oldpath, newpath, flags, errstr(err)) }()
	oldfi, err := fs.resolve(ctx, path.Dir(oldpath), true)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, oldfi.inode, mMaskW, oldfi.attr)
	if err != 0 {
		return
	}
	newfi, err := fs.resolve(ctx, path.Dir(newpath), true)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, newfi.inode, mMaskW, newfi.attr)
	if err != 0 {
		return
	}
	err = fs.m.Rename(ctx, oldfi.inode, path.Base(oldpath), newfi.inode, path.Base(newpath), flags, nil, nil)
	fs.invalidateEntry(oldfi.inode, path.Base(oldpath))
	fs.invalidateEntry(newfi.inode, path.Base(newpath))
	return
}

func (fs *FileSystem) Symlink(ctx meta.Context, target string, link string) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Symlink").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Symlink (%s,%s): %s", target, link, errstr(err)) }()
	fi, err := fs.resolve(ctx, path.Dir(link), true)
	if err != 0 {
		return
	}
	rel, e := filepath.Rel(path.Dir(link), target)
	if e != nil {
		// external link
		rel = target
	}
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
	if err != 0 {
		return
	}
	err = fs.m.Symlink(ctx, fi.inode, path.Base(link), rel, nil, nil)
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

const (
	mMaskR = 4
	mMaskW = 2
	mMaskX = 1
)

func (fs *FileSystem) Truncate(ctx meta.Context, path string, length uint64) (err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Truncate").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Truncate (%s,%d): %s", path, length, errstr(err)) }()
	fi, err := fs.resolve(ctx, path, true)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
	if err != 0 {
		return
	}
	err = fs.m.Truncate(ctx, fi.inode, 0, length, nil)
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
	err = fs.m.Access(ctx, dfi.inode, mMaskW, dfi.attr)
	if err != 0 {
		return
	}
	sfi, err = fs.resolve(ctx, src, true)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, sfi.inode, mMaskR, sfi.attr)
	if err != 0 {
		return
	}
	err = fs.m.CopyFileRange(ctx, sfi.inode, soff, dfi.inode, doff, size, 0, &written)
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
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
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
	err = fs.m.Access(ctx, fi.inode, mMaskR, fi.attr)
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
	err = fs.m.Access(ctx, fi.inode, mMaskR, fi.attr)
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
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
	if err != 0 {
		return
	}
	err = fs.m.RemoveXattr(ctx, fi.inode, name)
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

	err = fs.m.Lookup(ctx, parent, name, inode, attr)
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
	var inode Ino
	var attr = &Attr{}

	if fs.conf.FastResolve {
		err = fs.m.Resolve(ctx, 1, p, &inode, attr)
		if err == 0 {
			fi = AttrToFileInfo(inode, attr)
			p = strings.TrimRight(p, "/")
			ss := strings.Split(p, "/")
			fi.name = ss[len(ss)-1]
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
		if parent == 1 && i == len(ss)-1 && vfs.IsSpecialName(name) {
			inode, attr := vfs.GetInternalNodeByName(name)
			fi = AttrToFileInfo(inode, attr)
			parent = inode
			break
		}
		if i > 0 {
			if err := fs.m.Access(ctx, parent, mMaskX, attr); err != 0 {
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
		fi.name = name
		if (!resolved || followLastSymlink) && fi.IsSymlink() {
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
			fi, err = fs.resolve(ctx, target, followLastSymlink)
			if err != 0 {
				return
			}
			inode = fi.Inode()
		}
		parent = inode
	}
	if parent == 1 {
		err = fs.m.GetAttr(ctx, parent, attr)
		if err != 0 {
			return
		}
		fi = AttrToFileInfo(1, attr)
	}
	return fi, 0
}

func (fs *FileSystem) Create(ctx meta.Context, p string, mode uint16) (f *File, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Create").End()
	l := vfs.NewLogContext(ctx)
	defer func() { fs.log(l, "Create (%s,%o): %s", p, mode, errstr(err)) }()
	var inode Ino
	var attr = &Attr{}
	var fi *FileStat
	fi, err = fs.resolve(ctx, path.Dir(p), true)
	if err != 0 {
		return
	}
	err = fs.m.Access(ctx, fi.inode, mMaskW, fi.attr)
	if err != 0 {
		return
	}
	err = fs.m.Create(ctx, fi.inode, path.Base(p), mode&07777, 0, syscall.O_EXCL, &inode, attr)
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
	return nil
}

func (fs *FileSystem) Close() error {
	fs.Flush()
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
	if ctx.Uid() != 0 && ctx.Uid() != f.info.attr.Uid {
		return syscall.EACCES
	}
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
		if ctx.Uid() != 0 {
			return syscall.EACCES
		}
		flag |= meta.SetAttrUID
	}
	if gid != uint32(f.info.Gid()) {
		if ctx.Uid() != 0 {
			if ctx.Uid() != uint32(f.info.Uid()) {
				return syscall.EACCES
			}
			var found = false
			for _, g := range ctx.Gids() {
				if gid == g {
					found = true
					break
				}
			}
			if !found {
				return syscall.EACCES
			}
		}
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
	err = f.fs.m.Access(ctx, f.inode, mMaskW, f.info.attr)
	if err != 0 {
		return err
	}
	var attr Attr
	attr.Atime = atime / 1000
	attr.Atimensec = uint32(atime%1000) * 1e6
	attr.Mtime = mtime / 1000
	attr.Mtimensec = uint32(mtime%1000) * 1e6
	err = f.fs.m.SetAttr(ctx, f.inode, flag, 0, &attr)
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
	readSizeHistogram.Observe(float64(got))
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
		f.wdata.Close(meta.Background)
		f.wdata = nil
		return
	}
	if offset+int64(len(b)) > int64(f.info.attr.Length) {
		f.info.attr.Length = uint64(offset + int64(len(b)))
	}
	writtenSizeHistogram.Observe(float64(len(b)))
	return len(b), 0
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
				rdata.Close(meta.Background)
			})
		}
		if f.wdata != nil {
			err = f.wdata.Close(meta.Background)
			f.wdata = nil
		}
		f.fs.m.Close(ctx, f.inode)
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
		err = f.fs.m.Access(ctx, f.inode, mMaskR, f.info.attr)
		if err != 0 {
			return nil, err
		}
		var inodes []*meta.Entry
		err = f.fs.m.Readdir(ctx, f.inode, 1, &inodes)
		if err != 0 {
			return
		}
		for _, n := range inodes {
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
		err = f.fs.m.Access(ctx, f.inode, mMaskR|mMaskX, f.info.attr)
		if err != 0 {
			return nil, err
		}
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

func (f *File) Summary(ctx meta.Context, depth uint8, maxentries uint32) (s *meta.Summary, err syscall.Errno) {
	defer trace.StartRegion(context.TODO(), "fs.Summary").End()
	l := vfs.NewLogContext(ctx)
	defer func() {
		f.fs.log(l, "Summary (%s): %s (%d,%d,%d,%d)", f.path, errstr(err), s.Length, s.Size, s.Files, s.Dirs)
	}()
	s = &meta.Summary{}
	err = meta.GetSummary(f.fs.m, ctx, f.inode, s)
	return
}
