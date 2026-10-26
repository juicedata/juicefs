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
	"io/fs"
	"path"
	"syscall"
	"time"
)

// Stat is returned by the Sys method of the fs.FileInfo of this package.
type Stat struct {
	Inode uint64
	Mode  uint32 // POSIX st_mode
	Nlink uint32
	Uid   uint32
	Gid   uint32
	Size  uint64
	Rdev  uint32
	Atime time.Time
	Mtime time.Time
	Ctime time.Time
}

type fileInfo struct {
	name string
	st   *Stat
}

// newFileInfo names the file by the last element of name, as os does.
func newFileInfo(name string, st *Stat) *fileInfo {
	return &fileInfo{name: path.Base(name), st: st}
}

func (fi *fileInfo) Name() string       { return fi.name }
func (fi *fileInfo) Size() int64        { return int64(fi.st.Size) }
func (fi *fileInfo) Mode() fs.FileMode  { return fileMode(fi.st.Mode) }
func (fi *fileInfo) ModTime() time.Time { return fi.st.Mtime }
func (fi *fileInfo) IsDir() bool        { return fi.Mode().IsDir() }
func (fi *fileInfo) Sys() any           { return fi.st }

// fileMode is fillFileStatFromSys of os on Unix.
func fileMode(mode uint32) fs.FileMode {
	m := fs.FileMode(mode & 0777)
	switch mode & syscall.S_IFMT {
	case syscall.S_IFBLK:
		m |= fs.ModeDevice
	case syscall.S_IFCHR:
		m |= fs.ModeDevice | fs.ModeCharDevice
	case syscall.S_IFDIR:
		m |= fs.ModeDir
	case syscall.S_IFIFO:
		m |= fs.ModeNamedPipe
	case syscall.S_IFLNK:
		m |= fs.ModeSymlink
	case syscall.S_IFSOCK:
		m |= fs.ModeSocket
	}
	if mode&syscall.S_ISGID != 0 {
		m |= fs.ModeSetgid
	}
	if mode&syscall.S_ISUID != 0 {
		m |= fs.ModeSetuid
	}
	if mode&syscall.S_ISVTX != 0 {
		m |= fs.ModeSticky
	}
	return m
}

// syscallMode is syscallMode of os: the numeric 02000 is dropped, use
// fs.ModeSetgid.
func syscallMode(mode fs.FileMode) uint16 {
	m := uint16(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		m |= syscall.S_ISUID
	}
	if mode&fs.ModeSetgid != 0 {
		m |= syscall.S_ISGID
	}
	if mode&fs.ModeSticky != 0 {
		m |= syscall.S_ISVTX
	}
	return m
}
