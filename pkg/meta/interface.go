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

package meta

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// MaxVersion is the max of supported versions.
	MaxVersion = 1
	// ChunkBits is the size of a chunk.
	ChunkBits = 26
	// ChunkSize is size of a chunk
	ChunkSize = 1 << ChunkBits // 64M
	// DeleteSlice is a message to delete a slice from object store.
	DeleteSlice = 1000
	// CompactChunk is a message to compact a chunk in object store.
	CompactChunk = 1001
	// Rmr is a message to remove a directory recursively.
	Rmr = 1002
	// LegacyInfo is a message to get the internal info for file or directory.
	LegacyInfo = 1003
	// FillCache is a message to build cache for target directories/files
	FillCache = 1004
	// InfoV2 is a message to get the internal info for file or directory.
	InfoV2 = 1005
	// Clone is a message to clone a file or dir from another.
	Clone = 1006
	// OpSummary is a message to get tree summary of directories.
	OpSummary = 1007
	// CompactPath is a message to trigger compact
	CompactPath = 1008
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
	RenameNoReplace = 1 << iota
	RenameExchange
	RenameWhiteout
	_renameReserved1
	_renameReserved2
	RenameRestore // internal
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
	SetAttrFlag = 1 << 15
)

const (
	FlagImmutable = 1 << iota
	FlagAppend
)

const (
	QuotaSet uint8 = iota
	QuotaGet
	QuotaDel
	QuotaList
	QuotaCheck
)

const MaxName = 255
const MaxSymlink = 4096

type Ino uint64

const RootInode Ino = 1
const TrashInode Ino = 0x7FFFFFFF10000000 // larger than vfs.minInternalNode

const RmrDefaultThreads = 50

func (i Ino) String() string {
	return strconv.FormatUint(uint64(i), 10)
}

func (i Ino) IsValid() bool {
	return i >= RootInode
}

func (i Ino) IsTrash() bool {
	return i >= TrashInode
}

func (i Ino) IsNormal() bool {
	return i >= RootInode && i < TrashInode
}

var TrashName = ".trash"

func isTrash(ino Ino) bool {
	return ino >= TrashInode
}

type internalNode struct {
	inode Ino
	name  string
}

// Type of control messages
const CPROGRESS = 0xFE // 16 bytes: progress increment
const CDATA = 0xFF     // 4 bytes: data length

// MsgCallback is a callback for messages from meta service.
type MsgCallback func(...interface{}) error

// Attr represents attributes of a node.
type Attr struct {
	Flags     uint8  // flags
	Typ       uint8  // type of a node
	Mode      uint16 // permission mode
	Uid       uint32 // owner id
	Gid       uint32 // group id of owner
	Rdev      uint32 // device number
	Atime     int64  // last access time
	Mtime     int64  // last modified time
	Ctime     int64  // last change time for meta
	Atimensec uint32 // nanosecond part of atime
	Mtimensec uint32 // nanosecond part of mtime
	Ctimensec uint32 // nanosecond part of ctime
	Nlink     uint32 // number of links (sub-directories or hardlinks)
	Length    uint64 // length of regular file

	Parent    Ino  // inode of parent; 0 means tracked by parentKey (for hardlinks)
	Full      bool // the attributes are completed or not
	KeepCache bool // whether to keep the cached page or not

	AccessACL  uint32 // access ACL id (identical ACL rules share the same access ACL ID.)
	DefaultACL uint32 // default ACL id (default ACL and the access ACL share the same cache and store)
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

func typeToString(_type uint8) string {
	switch _type {
	case TypeFile:
		return "regular"
	case TypeDirectory:
		return "directory"
	case TypeSymlink:
		return "symlink"
	case TypeFIFO:
		return "fifo"
	case TypeBlockDev:
		return "blockdev"
	case TypeCharDev:
		return "chardev"
	case TypeSocket:
		return "socket"
	default:
		return "unknown"
	}
}

func typeFromString(s string) uint8 {
	switch s {
	case "regular":
		return TypeFile
	case "directory":
		return TypeDirectory
	case "symlink":
		return TypeSymlink
	case "fifo":
		return TypeFIFO
	case "blockdev":
		return TypeBlockDev
	case "chardev":
		return TypeCharDev
	case "socket":
		return TypeSocket
	default:
		panic(s)
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
	Id   uint64
	Size uint32
	Off  uint32
	Len  uint32
}

// Summary represents the total number of files/directories and
// total length of all files inside a directory.
type Summary struct {
	Length uint64
	Size   uint64
	Files  uint64
	Dirs   uint64
}

type TreeSummary struct {
	Inode    Ino
	Path     string
	Type     uint8
	Size     uint64
	Files    uint64
	Dirs     uint64
	Children []*TreeSummary `json:",omitempty"`
}

type SessionInfo struct {
	Version    string
	HostName   string
	IPAddrs    []string `json:",omitempty"`
	MountPoint string
	MountTime  time.Time
	ProcessID  int
}

type Flock struct {
	Inode Ino
	Owner uint64
	Ltype string
}

type Plock struct {
	Inode   Ino
	Owner   uint64
	Records []plockRecord
}

// Session contains detailed information of a client session
type Session struct {
	Sid    uint64
	Expire time.Time
	SessionInfo
	Sustained []Ino   `json:",omitempty"`
	Flocks    []Flock `json:",omitempty"`
	Plocks    []Plock `json:",omitempty"`
}

// Meta is a interface for a meta service for file system.
type Meta interface {
	// Name of database
	Name() string
	// Init is used to initialize a meta service.
	Init(format *Format, force bool) error
	// Shutdown close current database connections.
	Shutdown() error
	// Reset cleans up all metadata, VERY DANGEROUS!
	Reset() error
	// Load loads the existing setting of a formatted volume from meta service.
	Load(checkVersion bool) (*Format, error)
	// NewSession creates or update client session.
	NewSession(record bool) error
	// CloseSession does cleanup and close the session.
	CloseSession() error
	// FlushSession flushes the status to meta service.
	FlushSession()
	// GetSession retrieves information of session with sid
	GetSession(sid uint64, detail bool) (*Session, error)
	// ListSessions returns all client sessions.
	ListSessions() ([]*Session, error)
	// ScanDeletedObject scan deleted objects by customized scanner.
	ScanDeletedObject(Context, trashSliceScan, pendingSliceScan, trashFileScan, pendingFileScan) error
	// ListLocks returns all locks of a inode.
	ListLocks(ctx context.Context, inode Ino) ([]PLockItem, []FLockItem, error)
	// CleanStaleSessions cleans up sessions not active for more than 5 minutes
	CleanStaleSessions(ctx Context)
	// CleanupTrashBefore deletes all files in trash before the given time.
	CleanupTrashBefore(ctx Context, edge time.Time, increProgress func(int))
	// CleanupDetachedNodesBefore deletes all detached nodes before the given time.
	CleanupDetachedNodesBefore(ctx Context, edge time.Time, increProgress func())

	// StatFS returns summary statistics of a volume.
	StatFS(ctx Context, ino Ino, totalspace, availspace, iused, iavail *uint64) syscall.Errno
	// Access checks the access permission on given inode.
	Access(ctx Context, inode Ino, modemask uint8, attr *Attr) syscall.Errno
	// Lookup returns the inode and attributes for the given entry in a directory.
	Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno
	// Resolve fetches the inode and attributes for an entry identified by the given path.
	// ENOTSUP will be returned if there's no natural implementation for this operation or
	// if there are any symlink following involved.
	Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno
	// GetAttr returns the attributes for given node.
	GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	// SetAttr updates the attributes for given node.
	SetAttr(ctx Context, inode Ino, set uint16, sggidclearmode uint8, attr *Attr) syscall.Errno
	// Check setting attr is allowed or not
	CheckSetAttr(ctx Context, inode Ino, set uint16, attr Attr) syscall.Errno
	// Truncate changes the length for given file.
	Truncate(ctx Context, inode Ino, flags uint8, attrlength uint64, attr *Attr, skipPermCheck bool) syscall.Errno
	// Fallocate preallocate given space for given file.
	Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, length *uint64) syscall.Errno
	// ReadLink returns the target of a symlink.
	ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno
	// Symlink creates a symlink in a directory with given name.
	Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno
	// Mknod creates a node in a directory with given name, type and permissions.
	Mknod(ctx Context, parent Ino, name string, _type uint8, mode uint16, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno
	// Mkdir creates a sub-directory with given name and mode.
	Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno
	// Unlink removes a file entry from a directory.
	// The file will be deleted if it's not linked by any entries and not open by any sessions.
	Unlink(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno
	// Rmdir removes an empty sub-directory.
	Rmdir(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno
	// Rename move an entry from a source directory to another with given name.
	// The targeted entry will be overwrited if it's a file or empty directory.
	// For Hadoop, the target should not be overwritten.
	Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno
	// Link creates an entry for node.
	Link(ctx Context, inodeSrc, parent Ino, name string, attr *Attr) syscall.Errno
	// Readdir returns all entries for given directory, which include attributes if plus is true.
	Readdir(ctx Context, inode Ino, wantattr uint8, entries *[]*Entry) syscall.Errno
	// NewDirHandler returns a stream for directory entries.
	NewDirHandler(ctx Context, inode Ino, plus bool, initEntries []*Entry) (DirHandler, syscall.Errno)
	// Create creates a file in a directory with given name.
	Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno
	// Open checks permission on a node and track it as open.
	Open(ctx Context, inode Ino, flags uint32, attr *Attr) syscall.Errno
	// Close a file.
	Close(ctx Context, inode Ino) syscall.Errno
	// Read returns the list of slices on the given chunk.
	Read(ctx Context, inode Ino, indx uint32, slices *[]Slice) syscall.Errno
	// NewSlice returns an id for new slice.
	NewSlice(ctx Context, id *uint64) syscall.Errno
	// Write put a slice of data on top of the given chunk.
	Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time) syscall.Errno
	// InvalidateChunkCache invalidate chunk cache
	InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno
	// CopyFileRange copies part of a file to another one.
	CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied, outLength *uint64) syscall.Errno
	// GetParents returns a map of node parents (> 1 parents if hardlinked)
	GetParents(ctx Context, inode Ino) map[Ino]int
	// GetDirStat returns the space and inodes usage of a directory.
	GetDirStat(ctx Context, inode Ino) (stat *dirStat, st syscall.Errno)

	// GetXattr returns the value of extended attribute for given name.
	GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno
	// ListXattr returns all extended attributes of a node.
	ListXattr(ctx Context, inode Ino, dbuff *[]byte) syscall.Errno
	// SetXattr update the extended attribute of a node.
	SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno
	// RemoveXattr removes the extended attribute of a node.
	RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	// Flock tries to put a lock on given file.
	Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno
	// Getlk returns the current lock owner for a range on a file.
	Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno
	// Setlk sets a file range lock on given file.
	Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno

	// Compact all the chunks by merge small slices together
	CompactAll(ctx Context, threads int, bar *utils.Bar) syscall.Errno
	// Compact chunks for specified path
	Compact(ctx Context, inode Ino, concurrency int, preFunc, postFunc func()) syscall.Errno

	// ListSlices returns all slices used by all files.
	ListSlices(ctx Context, slices map[Ino][]Slice, scanPending, delete bool, showProgress func()) syscall.Errno
	// Remove all files and directories recursively.
	// count represents the number of attempted deletions of entries (even if failed).
	Remove(ctx Context, parent Ino, name string, skipTrash bool, numThreads int, count *uint64) syscall.Errno
	// Get summary of a node; for a directory it will accumulate all its child nodes
	GetSummary(ctx Context, inode Ino, summary *Summary, recursive bool, strict bool) syscall.Errno
	// GetTreeSummary returns a summary in tree structure
	GetTreeSummary(ctx Context, root *TreeSummary, depth, topN uint8, strict bool, updateProgress func(count uint64, bytes uint64)) syscall.Errno
	// Clone a file or directory
	Clone(ctx Context, srcParentIno, srcIno, dstParentIno Ino, dstName string, cmode uint8, cumask uint16, count, total *uint64) syscall.Errno
	// GetPaths returns all paths of an inode
	GetPaths(ctx Context, inode Ino) []string
	// Check integrity of an absolute path and repair it if asked
	Check(ctx Context, fpath string, repair bool, recursive bool, statAll bool) error
	// Change root to a directory specified by subdir
	Chroot(ctx Context, subdir string) syscall.Errno
	// chroot set the root directory by inode
	chroot(inode Ino)
	// Get a copy of the current format
	GetFormat() Format

	// OnMsg add a callback for the given message type.
	OnMsg(mtype uint32, cb MsgCallback)
	// OnReload register a callback for any change founded after reloaded.
	OnReload(func(new *Format))

	HandleQuota(ctx Context, cmd uint8, dpath string, quotas map[string]*Quota, strict, repair bool, create bool) error

	// Dump the tree under root, which may be modified by checkRoot
	DumpMeta(w io.Writer, root Ino, threads int, keepSecret, fast, skipTrash bool) error
	LoadMeta(r io.Reader) error

	DumpMetaV2(ctx Context, w io.Writer, opt *DumpOption) error
	LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error

	// getBase return the base engine.
	getBase() *baseMeta
	InitMetrics(registerer prometheus.Registerer)

	SetFacl(ctx Context, ino Ino, aclType uint8, n *aclAPI.Rule) syscall.Errno
	GetFacl(ctx Context, ino Ino, aclType uint8, n *aclAPI.Rule) syscall.Errno
}

type Creator func(driver, addr string, conf *Config) (Meta, error)

var metaDrivers = make(map[string]Creator)

func Register(name string, register Creator) {
	metaDrivers[name] = register
}

func setPasswordFromEnv(uri string) (string, error) {
	atIndex := strings.Index(uri, "@")
	if atIndex == -1 {
		return "", fmt.Errorf("invalid uri: %s", uri)
	}
	dIndex := strings.Index(uri, "://") + 3
	s := strings.Split(uri[dIndex:atIndex], ":")

	if len(s) > 2 {
		return "", fmt.Errorf("invalid uri: %s", uri)
	}

	if len(s) == 2 && s[1] != "" {
		return uri, nil
	}
	pwd := url.UserPassword("", os.Getenv("META_PASSWORD")) // escape only password
	return uri[:dIndex] + s[0] + pwd.String() + uri[atIndex:], nil
}

// NewClient creates a Meta client for given uri.
func NewClient(uri string, conf *Config) Meta {
	var err error
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}
	p := strings.Index(uri, "://")
	if p < 0 {
		logger.Fatalf("invalid uri: %s", uri)
	}
	driver := uri[:p]
	if os.Getenv("META_PASSWORD") != "" && (driver == "mysql" || driver == "postgres") {
		if uri, err = setPasswordFromEnv(uri); err != nil {
			logger.Fatalf(err.Error())
		}
	}
	logger.Infof("Meta address: %s", utils.RemovePassword(uri))
	f, ok := metaDrivers[driver]
	if !ok {
		logger.Fatalf("Invalid meta driver: %s", driver)
	}
	if conf == nil {
		conf = DefaultConf()
	} else {
		conf.SelfCheck()
	}
	m, err := f(driver, uri[p+3:], conf)
	if err != nil {
		logger.Fatalf("Meta %s is not available: %s", utils.RemovePassword(uri), err)
	}
	return m
}
