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
	// ChunkSize is size of a chunk
	ChunkSize = 1 << 26 // 64M
	// DeleteChunk is a message to delete a chunk from object store.
	DeleteChunk = 1000
	// CompactChunk is a message to compact a chunk in object store.
	CompactChunk = 1001
	// Rmr is a message to remove a directory recursively.
	Rmr = 1002
)

const (
	TypeFile      = 1 // type for regular file
	TypeDirectory = 2 // type for directory
	TypeSymlink   = 3 // type for symlink
	TypeFIFO      = 4 // type for FIFO node
	TypeBlockDev  = 5 // type for block device
	TypeCharDev   = 6 // type for character device
	TypeSocket    = 7 // type for socket
)

const (
	// SetAttrMode is a mask to update a attribute of node
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

// MsgCallback is a callback for messages from meta service.
type MsgCallback func(...interface{}) error

// Attr represents attributes of a node.
type Attr struct {
	Flags     uint8  // reserved flags
	Typ       uint8  // type of a node
	Mode      uint16 // permission mode
	Uid       uint32 // owner id
	Gid       uint32 // group id of owner
	Atime     int64  // last access time
	Mtime     int64  // last modified time
	Ctime     int64  // last change time for meta
	Atimensec uint32 // nanosecond part of atime
	Mtimensec uint32 // nanosecond part of mtime
	Ctimensec uint32 // nanosecond part of ctime
	Nlink     uint32 // number of links (sub-directories or hardlinks)
	Length    uint64 // length of regular file
	Rdev      uint32 // device number
	Parent    Ino    // inode of parent, only for Directory
	Full      bool   // the attributes are completed or not
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

// SMode is the file mode including type and unix permission.
func (a Attr) SMode() uint32 {
	return typeToStatType(a.Typ) | uint32(a.Mode)
}

// Entry is an entry inside a directory.
type Entry struct {
	Inode Ino
	Name  []byte
	Attr  *Attr
}

// Slice is a slice of a chunk.
// Multiple slices could be combined together as a chunk.
type Slice struct {
	Chunkid uint64
	Size    uint32
	Off     uint32
	Len     uint32
}

// Summary represents the total number of files/directories and
// total length of all files inside a directory.
type Summary struct {
	Length uint64
	Size   uint64
	Files  uint64
	Dirs   uint64
}

// Meta is a interface for a meta service for file system.
type Meta interface {
	// Init is used to initialize a meta service.
	Init(format Format, force bool) error
	// Load loads the existing setting of a formatted volume from meta service.
	Load() (*Format, error)

	// StatFS returns summary statistics of a volume.
	StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno
	// Access checks the access permission on given inode.
	Access(ctx Context, inode Ino, modemask uint8, attr *Attr) syscall.Errno
	// Lookup returns the inode and attributes for the given entry in a directory.
	Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	// GetAttr returns the attributes for given node.
	GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	// SetAttr updates the attributes for given node.
	SetAttr(ctx Context, inode Ino, set uint16, sggidclearmode uint8, attr *Attr) syscall.Errno
	// Truncate changes the length for given file.
	Truncate(ctx Context, inode Ino, flags uint8, attrlength uint64, attr *Attr) syscall.Errno
	// Fallocate preallocate given space for given file.
	Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno
	// ReadLink returns the target of a symlink.
	ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno
	// Symlink creates a symlink in a directory with given name.
	Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno
	// Mknod creates a node in a directory with given name, type and permissions.
	Mknod(ctx Context, parent Ino, name string, _type uint8, mode uint16, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno
	// Mkdir creates a sub-directory with given name and mode.
	Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno
	// Unlink removes a file entry from a directory.
	// The file will be deleted if it's not linked by any entries and not open by any sessions.
	Unlink(ctx Context, parent Ino, name string) syscall.Errno
	// Rmdir removes an empty sub-directory.
	Rmdir(ctx Context, parent Ino, name string) syscall.Errno
	// Rename move an entry from a source directory to another with given name.
	// The targeted entry will be overwrited if it's a file or empty directory.
	// For Hadoop, the target should not be overwritten.
	Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno
	// Link creates an entry for node.
	Link(ctx Context, inodeSrc, parent Ino, name string, attr *Attr) syscall.Errno
	// Readdir returns all entries for given directory, which include attributes if plus is true.
	Readdir(ctx Context, inode Ino, wantattr uint8, entries *[]*Entry) syscall.Errno
	// Create creates a file in a directory with given name.
	Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno
	// Open checks permission on a node and track it as open.
	Open(ctx Context, inode Ino, flags uint8, attr *Attr) syscall.Errno
	// Close a file.
	Close(ctx Context, inode Ino) syscall.Errno
	// Read returns the list of slices on the given chunk.
	Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno
	// NewChunk returns a new id for new data.
	NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno
	// Write put a slice of data on top of the given chunk.
	Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno
	// CopyFileRange copies part of a file to another one.
	CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno

	// GetXattr returns the value of extended attribute for given name.
	GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno
	// ListXattr returns all extended attributes of a node.
	ListXattr(ctx Context, inode Ino, dbuff *[]byte) syscall.Errno
	// SetXattr update the extended attribute of a node.
	SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno
	// RemoveXattr removes the extended attribute of a node.
	RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	// Flock tries to put a lock on given file.
	Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno
	// Getlk returns the current lock owner for a range on a file.
	Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno
	// Setlk sets a file range lock on given file.
	Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno

	// Summary returns the summary for given file or directory.
	Summary(ctx Context, inode Ino, summary *Summary) syscall.Errno
	// Rmr remove all the files and directories recursively.
	Rmr(ctx Context, inode Ino, name string) syscall.Errno

	// OnMsg add a callback for the given message type.
	OnMsg(mtype uint32, cb MsgCallback)
}
