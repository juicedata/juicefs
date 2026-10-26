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

package juicefs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// File is an open file or directory, like os.File. Its methods may be called
// concurrently, but are not serialized: concurrent calls of Read, Write and
// Seek share the offset without ordering, and Close does not wait for the
// calls in progress. Callers that need ordering must synchronize them.
type File struct {
	c    *Client
	f    fileBackend
	name string

	off    atomic.Int64 // offset of a file, or entries returned of a directory
	size   atomic.Int64 // length at open, updated by the writes of this handle
	closed atomic.Bool

	// dirMu guards dirents and the offset of a directory; it is not held
	// while reading the directory.
	dirMu   sync.Mutex
	dirents []*fileInfo // listing of a directory, sorted; dropped by Seek

	readable, writable, append, isDir bool
}

var (
	_ io.ReadWriteSeeker = (*File)(nil)
	_ io.ReaderAt        = (*File)(nil)
	_ io.WriterAt        = (*File)(nil)
	_ io.Closer          = (*File)(nil)
	_ io.StringWriter    = (*File)(nil)
	_ fs.ReadDirFile     = (*File)(nil)
)

const openFlags = os.O_RDONLY | os.O_WRONLY | os.O_RDWR | os.O_CREATE | os.O_EXCL | os.O_TRUNC | os.O_APPEND

// The same text as that of os, whose error is not exported.
var errWriteAtInAppendMode = errors.New("os: invalid use of WriteAt on file opened with O_APPEND")

// Open opens name for reading.
func (c *Client) Open(name string) (*File, error) {
	return c.OpenFile(name, os.O_RDONLY, 0)
}

// Create is os.Create.
func (c *Client) Create(name string) (*File, error) {
	return c.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

// OpenFile is os.OpenFile, which supports only the flags O_RDONLY, O_WRONLY,
// O_RDWR, O_CREATE, O_EXCL, O_TRUNC and O_APPEND; others give EINVAL. perm
// applies with the umask read by New. Writes with O_APPEND go to the end of
// the file as known by this handle, not atomically. A file open when the
// Client is closed is closed with it.
func (c *Client) OpenFile(name string, flag int, perm fs.FileMode) (*File, error) {
	f, err := c.openFile(name, flag, perm)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	return f, nil
}

func (c *Client) openFile(name string, flag int, perm fs.FileMode) (*File, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	if flag&^openFlags != 0 {
		return nil, syscall.EINVAL
	}
	f := &File{c: c, name: name, append: flag&os.O_APPEND != 0}
	switch flag & syscall.O_ACCMODE {
	case os.O_RDONLY:
		f.readable = true
	case os.O_WRONLY:
		f.writable = true
	case os.O_RDWR:
		f.readable, f.writable = true, true
	default:
		return nil, syscall.EINVAL
	}
	create, trunc := flag&os.O_CREATE != 0, flag&os.O_TRUNC != 0
	var st *Stat
	var err error
	if create && flag&os.O_EXCL != 0 {
		f.f, err = c.backend.create(name, syscallMode(perm), c.umask)
	} else {
		f.f, st, err = c.backend.open(name, flag)
		if err == syscall.ENOENT && create {
			f.f, err = c.backend.create(name, syscallMode(perm), c.umask)
			if err == syscall.EEXIST {
				// Created by others since open.
				f.f, st, err = c.backend.open(name, flag)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if st != nil {
		f.isDir = isDir(st)
		f.size.Store(int64(st.Size))
		if f.isDir && (f.writable || create || trunc) {
			err = syscall.EISDIR
		} else if trunc && !f.isDir {
			// Not atomic with open, as in the Python SDK.
			if err = f.f.truncate(0); err == nil {
				f.size.Store(0)
			}
		}
	}
	if err == nil && !c.track(f) {
		err = fs.ErrClosed
	}
	if err != nil {
		_ = f.f.close()
		return nil, err
	}
	return f, nil
}

func (c *Client) ReadFile(name string) ([]byte, error) {
	f, err := c.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// As readFileContents of os, sized by the length at open instead of Stat.
	const minBuf = 512
	statSize := f.size.Load()
	size := minBuf
	if n := statSize + 1; int64(int(n)) == n && int(n) > size {
		size = int(n)
	}
	data := make([]byte, 0, size)
	for {
		n, err := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return data, err
		}
		if remain := cap(data) - len(data); remain == 0 || (statSize == 0 && remain < minBuf) {
			data = slices.Grow(data, minBuf)
		}
	}
}

func (c *Client) WriteFile(name string, data []byte, perm fs.FileMode) error {
	f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err1 != nil && err == nil {
		err = err1
	}
	return err
}

// Name returns the name as passed to OpenFile.
func (f *File) Name() string {
	return f.name
}

func (f *File) wrap(op string, err error) error {
	return pathError(op, f.name, err)
}

// Read reads at most len(b) bytes. The end of the file is its length at open
// plus the writes of this handle.
func (f *File) Read(b []byte) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	n, err = f.pread(b, f.off.Load())
	f.off.Add(int64(n))
	return n, f.wrap("read", err)
}

// ReadAt does not change the offset.
func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if off < 0 {
		return 0, &fs.PathError{Op: "readat", Path: f.name, Err: errors.New("negative offset")}
	}
	for len(b) > 0 {
		m, e := f.pread(b, off)
		n += m
		b = b[m:]
		off += int64(m)
		if e == nil && m == 0 {
			e = io.EOF
		}
		if e != nil {
			err = f.wrap("read", e)
			break
		}
	}
	return n, err
}

// pread checks the length first, as os does.
func (f *File) pread(b []byte, off int64) (int, error) {
	switch {
	case f.closed.Load():
		return 0, fs.ErrClosed
	case len(b) == 0:
		return 0, nil
	case f.isDir:
		return 0, syscall.EISDIR
	case !f.readable:
		return 0, syscall.EBADF
	}
	return f.f.pread(b, off)
}

// Write writes b at the offset, or at the end known by this handle with
// O_APPEND.
func (f *File) Write(b []byte) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if err = f.checkWrite(); err == nil && len(b) > 0 {
		if f.append {
			off := f.size.Load()
			n, err = f.pwrite(b, off)
			f.off.Store(off + int64(n))
		} else {
			n, err = f.pwrite(b, f.off.Load())
			f.off.Add(int64(n))
		}
	}
	return n, f.wrap("write", err)
}

// WriteAt is io.WriterAt; it does not change the offset and fails with
// O_APPEND, as os does.
func (f *File) WriteAt(b []byte, off int64) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if f.append {
		return 0, errWriteAtInAppendMode
	}
	if off < 0 {
		return 0, &fs.PathError{Op: "writeat", Path: f.name, Err: errors.New("negative offset")}
	}
	if len(b) == 0 {
		return 0, nil
	}
	if err = f.checkWrite(); err == nil {
		n, err = f.pwrite(b, off)
	}
	return n, f.wrap("write", err)
}

func (f *File) WriteString(s string) (n int, err error) {
	return f.Write([]byte(s))
}

func (f *File) checkWrite() error {
	if f.closed.Load() {
		return fs.ErrClosed
	}
	if !f.writable {
		return syscall.EBADF
	}
	return nil
}

func (f *File) pwrite(b []byte, off int64) (int, error) {
	n, err := f.f.pwrite(b, off)
	end := off + int64(n)
	for {
		size := f.size.Load()
		if end <= size || f.size.CompareAndSwap(size, end) {
			break
		}
	}
	return n, err
}

// Seek sets the offset; io.SeekEnd is relative to the length known by this
// handle. On a directory it restarts reading from the backend, and the
// offset counts entries in the order of names.
func (f *File) Seek(offset int64, whence int) (ret int64, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if f.closed.Load() {
		err = fs.ErrClosed
	} else if f.isDir {
		ret, err = f.seekDir(offset, whence)
	} else {
		ret, err = seekOffset(&f.off, offset, whence, f.size.Load())
	}
	if err != nil {
		return 0, f.wrap("seek", err)
	}
	return ret, nil
}

// seekOffset sets off to offset relative to whence, where end is the end.
func seekOffset(off *atomic.Int64, offset int64, whence int, end int64) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = off.Load()
	case io.SeekEnd:
		base = end
	default:
		return 0, syscall.EINVAL
	}
	ret := base + offset
	if ret < 0 {
		return 0, syscall.EINVAL
	}
	off.Store(ret)
	return ret, nil
}

// seekDir drops the listing, as os drops its directory state on any Seek.
func (f *File) seekDir(offset int64, whence int) (int64, error) {
	var infos []*fileInfo
	var err error
	if whence == io.SeekEnd {
		infos, err = f.listDir()
	}
	f.dirMu.Lock()
	defer f.dirMu.Unlock()
	f.dirents = infos
	if err != nil {
		return 0, err
	}
	return seekOffset(&f.off, offset, whence, int64(len(infos)))
}

// Stat returns the current attributes of the open file; Size includes the
// writes of this handle that are not flushed.
func (f *File) Stat() (fs.FileInfo, error) {
	if f == nil {
		return nil, fs.ErrInvalid
	}
	if f.closed.Load() {
		return nil, f.wrap("stat", fs.ErrClosed)
	}
	st, err := f.f.stat()
	if err != nil {
		return nil, f.wrap("stat", err)
	}
	if size := f.size.Load(); !f.isDir && size > int64(st.Size) {
		st.Size = uint64(size)
	}
	return newFileInfo(f.name, st), nil
}

func (f *File) Truncate(size int64) error {
	if f == nil {
		return fs.ErrInvalid
	}
	var err error
	switch {
	case f.closed.Load():
		err = fs.ErrClosed
	case !f.writable || size < 0:
		err = syscall.EINVAL
	default:
		if err = f.f.truncate(size); err == nil {
			f.size.Store(size)
		}
	}
	return f.wrap("truncate", err)
}

// Sync uploads the written data and commits it to the metadata.
func (f *File) Sync() error {
	if f == nil {
		return fs.ErrInvalid
	}
	err := error(fs.ErrClosed)
	if !f.closed.Load() {
		err = f.f.fsync()
	}
	return f.wrap("sync", err)
}

// Close returns the error of uploading the written data, which must be
// checked. It does not wait for the calls in progress; their results, and
// whether their writes are uploaded, are undefined.
func (f *File) Close() error {
	if f == nil {
		return fs.ErrInvalid
	}
	return f.wrap("close", f.close())
}

// close closes f once; later calls return fs.ErrClosed.
func (f *File) close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return fs.ErrClosed
	}
	f.c.untrack(f)
	return f.f.close()
}

// Chmod changes the mode of the open file, as Client.Chmod.
func (f *File) Chmod(mode fs.FileMode) error {
	if f == nil {
		return fs.ErrInvalid
	}
	err := error(fs.ErrClosed)
	if !f.closed.Load() {
		err = f.f.chmod(syscallMode(mode))
	}
	return f.wrap("chmod", err)
}

// Chown changes the owner of the open file; -1 leaves an ID unchanged.
func (f *File) Chown(uid, gid int) error {
	if f == nil {
		return fs.ErrInvalid
	}
	err := error(fs.ErrClosed)
	if !f.closed.Load() {
		var u, g uint32
		if u, g, err = owner(uid, gid, f.f.stat); err == nil {
			err = f.f.chown(u, g)
		}
	}
	return f.wrap("chown", err)
}

// ReadDir is os.File.ReadDir, with the entries in the order of names. Info of
// an entry returns the attributes read with the directory.
func (f *File) ReadDir(n int) ([]fs.DirEntry, error) {
	if f == nil {
		return nil, fs.ErrInvalid
	}
	infos, err := f.readdir(n)
	entries := make([]fs.DirEntry, len(infos))
	for i, fi := range infos {
		entries[i] = fs.FileInfoToDirEntry(fi)
	}
	return entries, f.wrap("readdir", err)
}

// Readdir is os.File.Readdir, with the entries in the order of names.
func (f *File) Readdir(n int) ([]fs.FileInfo, error) {
	if f == nil {
		return nil, fs.ErrInvalid
	}
	infos, err := f.readdir(n)
	fis := make([]fs.FileInfo, len(infos))
	for i, fi := range infos {
		fis[i] = fi
	}
	return fis, f.wrap("readdir", err)
}

// Readdirnames is os.File.Readdirnames, in the order of names.
func (f *File) Readdirnames(n int) (names []string, err error) {
	if f == nil {
		return nil, fs.ErrInvalid
	}
	infos, err := f.readdir(n)
	names = make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.name
	}
	return names, f.wrap("readdir", err)
}

// readdir returns up to n entries after those returned, or all of them if
// n <= 0, reading the directory first if needed.
func (f *File) readdir(n int) ([]*fileInfo, error) {
	if f.closed.Load() {
		return nil, fs.ErrClosed
	}
	if !f.isDir {
		return nil, syscall.ENOTDIR
	}
	f.dirMu.Lock()
	dirents := f.dirents
	f.dirMu.Unlock()
	if dirents == nil {
		var err error
		if dirents, err = f.listDir(); err != nil {
			return nil, err
		}
	}
	f.dirMu.Lock()
	defer f.dirMu.Unlock()
	if f.dirents == nil {
		f.dirents = dirents
	}
	off := f.off.Load()
	rest := f.dirents[min(off, int64(len(f.dirents))):]
	if n > 0 {
		if len(rest) == 0 {
			return nil, io.EOF
		}
		rest = rest[:min(n, len(rest))]
	}
	f.off.Store(off + int64(len(rest)))
	return rest, nil
}

// listDir reads the directory sorted by name, never nil.
func (f *File) listDir() ([]*fileInfo, error) {
	infos, err := f.f.readdir()
	if err != nil {
		return nil, err
	}
	sortByName(infos)
	if infos == nil {
		infos = []*fileInfo{}
	}
	return infos, nil
}

func sortByName(infos []*fileInfo) {
	slices.SortFunc(infos, func(a, b *fileInfo) int { return strings.Compare(a.name, b.name) })
}
