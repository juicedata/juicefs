/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

package meta

import (
	"syscall"
)

const (
	ChunkSize    = 1 << 26 // 64M
	DeleteChunk  = 1000
	CompactChunk = 1001
)

const (
	TypeFile      = 1
	TypeDirectory = 2
	TypeSymlink   = 3
	TypeFIFO      = 4
	TypeBlockDev  = 5
	TypeCharDev   = 6
	TypeSocket    = 7
)

const (
	SetAttrMode = 1 << iota
	SetAttrUID
	SetAttrGID
	SetAttrSize
	SetAttrAtime
	SetAttrMtime
	SetAttrCtime
	SetAttrAtimeNow
	SetAttrMtimeNow
)

type MsgCallback func(...interface{}) error

type Attr struct {
	Flags     uint8
	Typ       uint8
	Mode      uint16
	Uid       uint32
	Gid       uint32
	Atime     int64
	Mtime     int64
	Ctime     int64
	Atimensec uint32
	Mtimensec uint32
	Ctimensec uint32
	Nlink     uint32
	Length    uint64
	Rdev      uint32
	Parent    Ino // for Directory
	Full      bool
}

func typeToStatType(_type uint8) uint32 {
	switch _type & 0x7F {
	case TypeDirectory:
		return syscall.S_IFDIR
	case TypeSymlink:
		return syscall.S_IFLNK
	case TypeFile:
		return syscall.S_IFREG
	case TypeFIFO:
		return syscall.S_IFIFO
	case TypeSocket:
		return syscall.S_IFSOCK
	case TypeBlockDev:
		return syscall.S_IFBLK
	case TypeCharDev:
		return syscall.S_IFCHR
	default:
		panic(_type)
	}
}

func (a Attr) SMode() uint32 {
	return typeToStatType(a.Typ) | uint32(a.Mode)
}

type Entry struct {
	Inode Ino
	Name  []byte
	Attr  *Attr
}

type Slice struct {
	Chunkid uint64
	Size    uint32
	Off     uint32
	Len     uint32
}

type Summary struct {
	Length uint64
	Size   uint64
	Files  uint64
	Dirs   uint64
}

type Meta interface {
	Init(format Format, force bool) error
	Load() (*Format, error)

	StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno
	Access(ctx Context, inode Ino, modemask uint8, attr *Attr) syscall.Errno
	Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	SetAttr(ctx Context, inode Ino, set uint16, sggidclearmode uint8, attr *Attr) syscall.Errno
	Truncate(ctx Context, inode Ino, flags uint8, attrlength uint64, attr *Attr) syscall.Errno
	Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno
	ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno
	Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno
	Mknod(ctx Context, parent Ino, name string, _type uint8, mode uint16, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno
	Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno
	Unlink(ctx Context, parent Ino, name string) syscall.Errno
	Rmdir(ctx Context, parent Ino, name string) syscall.Errno
	Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno
	Link(ctx Context, inodeSrc, parent Ino, name string, attr *Attr) syscall.Errno
	Readdir(ctx Context, inode Ino, wantattr uint8, entries *[]*Entry) syscall.Errno
	Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno
	Open(ctx Context, inode Ino, flags uint8, attr *Attr) syscall.Errno
	Close(ctx Context, inode Ino) syscall.Errno
	Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno
	NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno
	Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno

	GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno
	ListXattr(ctx Context, inode Ino, dbuff *[]byte) syscall.Errno
	SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno
	RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno
	Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno
	Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno

	Summary(ctx Context, inode Ino, summary *Summary) syscall.Errno
	Rmr(ctx Context, inode Ino, name string) syscall.Errno

	OnMsg(mtype uint32, cb MsgCallback)
}
