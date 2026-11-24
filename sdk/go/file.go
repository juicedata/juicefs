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
	"sync/atomic"
	"syscall"
)

// File is an open file or directory, like os.File. Its methods may be called
// concurrently, but are not serialized: concurrent calls of Read, Write and
// Seek share the offset without ordering, so do the directory reads, which
// may return the same entries or skip some; Close does not wait for the
// calls in progress. Callers that need ordering must synchronize them.
type File struct {
	c    *Client
	f    fileBackend
	name string

	off    atomic.Int64 // offset of a file, or entries returned of a directory
	closed atomic.Bool

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

func (c *Client) Open(name string) (*File, error) {
	return c.OpenFile(name, os.O_RDONLY, 0)
}

func (c *Client) Create(name string) (*File, error) {
	return c.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

// OpenFile is os.OpenFile, which supports only the flags O_RDONLY, O_WRONLY,
// O_RDWR, O_CREATE, O_EXCL, O_TRUNC and O_APPEND; others give EINVAL. perm
// applies with the umask read by New. Writes with O_APPEND go to the end of
// the file read before each write, not atomically. A file open when the
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
		if f.isDir && (f.writable || create || trunc) {
			err = syscall.EISDIR
		} else if trunc && !f.isDir {
			// Truncation is not atomic with open.
			err = f.f.truncate(0)
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

	// As readFileContents of os, sized by the current length.
	const minBuf = 512
	var statSize int64
	if st, e := f.f.stat(); e == nil {
		statSize = int64(st.Size)
	}
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

// Read uses the length at open, updated by writes and truncation through
// this handle. Appends through other handles are not visible.
func (f *File) Read(b []byte) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	n, err = f.pread(b, f.off.Load())
	f.off.Add(int64(n))
	return n, f.wrap("read", err)
}

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

// Write writes b at the offset, or at the current end of the file with
// O_APPEND.
func (f *File) Write(b []byte) (n int, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if err = f.checkWrite(); err == nil && len(b) > 0 {
		if f.append {
			var st *Stat
			if st, err = f.f.stat(); err == nil {
				off := int64(st.Size)
				n, err = f.f.pwrite(b, off)
				f.off.Store(off + int64(n))
			}
		} else {
			n, err = f.f.pwrite(b, f.off.Load())
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
		n, err = f.f.pwrite(b, off)
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

// Seek sets the offset; io.SeekEnd is relative to the current length of the
// file, including the writes of this client that are not flushed. On a
// directory it restarts reading from the backend, and the offset counts
// entries in the order of the backend.
func (f *File) Seek(offset int64, whence int) (ret int64, err error) {
	if f == nil {
		return 0, fs.ErrInvalid
	}
	if f.closed.Load() {
		err = fs.ErrClosed
	} else if f.isDir {
		ret, err = f.seekDir(offset, whence)
	} else {
		var end int64
		if whence == io.SeekEnd {
			var st *Stat
			if st, err = f.f.stat(); err == nil {
				end = int64(st.Size)
			}
		}
		if err == nil {
			ret, err = seekOffset(&f.off, offset, whence, end)
		}
	}
	if err != nil {
		return 0, f.wrap("seek", err)
	}
	return ret, nil
}

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
	if err := f.f.rewinddir(); err != nil {
		return 0, err
	}
	var end int64
	if whence == io.SeekEnd {
		infos, err := f.f.readdir(0, 0)
		if err != nil {
			return 0, err
		}
		end = int64(len(infos))
	}
	return seekOffset(&f.off, offset, whence, end)
}

// Stat returns the current attributes of the open file; Size includes the
// writes of this client that are not flushed.
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
		err = f.f.truncate(size)
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

func (f *File) close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return fs.ErrClosed
	}
	f.c.untrack(f)
	return f.f.close()
}

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

// ReadDir is os.File.ReadDir, with the entries in the order of the backend.
// Info of an entry returns the attributes read with the directory.
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

// Readdir is os.File.Readdir, with the entries in the order of the backend.
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

// Readdirnames is os.File.Readdirnames, in the order of the backend.
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

func (f *File) readdir(n int) ([]*fileInfo, error) {
	if f.closed.Load() {
		return nil, fs.ErrClosed
	}
	if !f.isDir {
		return nil, syscall.ENOTDIR
	}
	infos, err := f.f.readdir(f.off.Load(), n)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(infos) == 0 {
		return nil, io.EOF
	}
	f.off.Add(int64(len(infos)))
	return infos, nil
}
