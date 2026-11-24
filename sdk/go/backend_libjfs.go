//go:build libjfs

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

/*
#cgo LDFLAGS: -ljfs
#include <stdint.h>
#include <stdlib.h>
#include <sys/types.h>

// The exported functions of libjfs (sdk/java/libjfs), declared here so that
// building only needs the library. A Go bool is a uint8_t.
int64_t jfs_init(uintptr_t credential, int32_t count, char *name, char *conf, char *user, char *group, char *superuser, char *supergroup);
int32_t jfs_term(int64_t pid, int64_t h, int terminate);
int32_t jfs_set_ids(int64_t h, uint32_t uid, uint32_t *gids, int32_t ngids);
int32_t jfs_statx(int64_t pid, int64_t h, char *path, int32_t follow, void *buf);
int32_t jfs_listdirx(int64_t pid, int64_t h, char *path, void **buf, int64_t *size);
int32_t jfs_mkdir(int64_t pid, int64_t h, char *path, uint16_t mode, uint16_t umask);
int32_t jfs_mkdirAll(int64_t pid, int64_t h, char *path, uint16_t mode, uint16_t umask, uint8_t existOK);
int32_t jfs_delete(int64_t pid, int64_t h, char *path);
int32_t jfs_rmdir(int64_t pid, int64_t h, char *path);
int32_t jfs_rmr(int64_t pid, int64_t h, char *path);
int32_t jfs_rename0(int64_t pid, int64_t h, char *oldpath, char *newpath, uint32_t flags);
int32_t jfs_link(int64_t pid, int64_t h, char *src, char *dst);
int32_t jfs_symlink(int64_t pid, int64_t h, char *target, char *link);
int32_t jfs_readlink(int64_t pid, int64_t h, char *link, void *buf, int32_t bufsize);
int32_t jfs_truncate(int64_t pid, int64_t h, char *path, uint64_t length);
int32_t jfs_chmod(int64_t pid, int64_t h, char *path, mode_t mode);
int32_t jfs_chown(int64_t pid, int64_t h, char *path, uint32_t uid, uint32_t gid);
int32_t jfs_lchown(int64_t pid, int64_t h, char *path, uint32_t uid, uint32_t gid);
int32_t jfs_utimens(int64_t pid, int64_t h, char *path, int64_t asec, int64_t ansec, int64_t msec, int64_t mnsec);
int32_t jfs_openx(int64_t pid, int64_t h, char *path, uint32_t mask, void *buf);
int32_t jfs_create(int64_t pid, int64_t h, char *path, uint16_t mode, uint16_t umask);
int32_t jfs_getXattr(int64_t pid, int64_t h, char *path, char *name, void *buf, int32_t bufsize);
int32_t jfs_listXattr(int64_t pid, int64_t h, char *path, void *buf, int32_t bufsize);
int32_t jfs_setXattr(int64_t pid, int64_t h, char *path, char *name, void *value, int32_t vlen, int32_t mode);
int32_t jfs_removeXattr(int64_t pid, int64_t h, char *path, char *name);
int32_t jfs_clone(int64_t pid, int64_t h, char *src, char *dst, uint8_t preserve);
int32_t jfs_info(int64_t pid, int64_t h, char *path, uint8_t **buf, uint8_t recursive, uint8_t strict);
int32_t jfs_gettreesummary(int64_t pid, int64_t h, char *path, uint8_t depth, uint8_t entries, uint8_t **buf);
int32_t jfs_quota(int64_t pid, int64_t h, char *path, uint8_t cmd, uint64_t cap, uint64_t inodes, uint8_t strict, uint8_t repair, uint8_t create, uint8_t **buf);
int32_t jfs_warmup(int64_t pid, int64_t h, char *paths, int32_t threads, uint8_t background, uint8_t evict, uint8_t check, uint8_t **buf);
int32_t jfs_status(int64_t pid, int64_t h, uint8_t trash, uint64_t session, uint8_t **buf);
int32_t jfs_metrics(int64_t h, void **buf, int64_t *size);
int32_t jfs_pread(int64_t pid, int32_t fd, void *buf, int32_t count, int64_t offset);
int32_t jfs_pwrite(int64_t pid, int32_t fd, void *buf, int32_t count, int64_t offset);
int32_t jfs_ftruncate(int64_t pid, int32_t fd, uint64_t size);
int32_t jfs_freaddirx(int64_t pid, int32_t fd, void **buf, int64_t *size);
int32_t jfs_fsync(int64_t pid, int32_t fd);
int32_t jfs_fstatx(int64_t pid, int32_t fd, void *buf);
int32_t jfs_fchmod(int64_t pid, int32_t fd, uint16_t mode);
int32_t jfs_fchown(int64_t pid, int32_t fd, uint32_t uid, uint32_t gid);
int32_t jfs_close(int64_t pid, int32_t fd);
*/
import "C"

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// libjfs takes a pid to tell the threads of Java apart; the SDK does not.
const pid = 0

// maxIO is the most jfs_pread reads in one call.
const maxIO = 1 << 30

// libjfsErrnos are the Linux values libjfs returns on all platforms (Linux has
// EDQUOT as 122, P56); others keep the value of the platform.
var libjfsErrnos = map[int32]syscall.Errno{
	39: syscall.ENOTEMPTY,
	40: syscall.ELOOP,
	61: syscall.ENODATA,
	69: syscall.EDQUOT,
	95: syscall.ENOTSUP,
}

// cerr converts a negative errno from libjfs; others mean success.
func cerr(r C.int32_t) error {
	return libjfsErrno(int32(r))
}

func libjfsErrno(r int32) error {
	if r >= 0 {
		return nil
	}
	if e, ok := libjfsErrnos[-r]; ok {
		return e
	}
	return syscall.Errno(-r)
}

type libjfsBackend struct {
	h C.int64_t
}

// javaConf is the configuration of jfs_init.
type javaConf struct {
	MetaURL           string `json:"meta"`
	Bucket            string `json:"bucket"`
	StorageClass      string `json:"storageClass"`
	ReadOnly          bool   `json:"readOnly"`
	NoSession         bool   `json:"noSession"`
	NoBGJob           bool   `json:"noBGJob"`
	OpenCache         string `json:"openCache"`
	BackupMeta        string `json:"backupMeta"`
	BackupSkipTrash   bool   `json:"backupSkipTrash"`
	Heartbeat         string `json:"heartbeat"`
	CacheDir          string `json:"cacheDir"`
	CacheSize         string `json:"cacheSize"`
	FreeSpace         string `json:"freeSpace"`
	AutoCreate        bool   `json:"autoCreate"`
	CacheFullBlock    bool   `json:"cacheFullBlock"`
	CacheChecksum     string `json:"cacheChecksum"`
	CacheEviction     string `json:"cacheEviction"`
	CacheScanInterval string `json:"cacheScanInterval"`
	CacheExpire       string `json:"cacheExpire"`
	Writeback         bool   `json:"writeback"`
	MemorySize        string `json:"memorySize"`
	Prefetch          int    `json:"prefetch"`
	Readahead         string `json:"readahead"`
	UploadLimit       string `json:"uploadLimit"`
	DownloadLimit     string `json:"downloadLimit"`
	MaxUploads        int    `json:"maxUploads"`
	MaxDownloads      int    `json:"maxDownloads"`
	MaxDeletes        int    `json:"maxDeletes"`
	SkipDirNlink      int    `json:"skipDirNlink"`
	SkipDirMtime      string `json:"skipDirMtime"`
	IORetries         int    `json:"ioRetries"`
	GetTimeout        string `json:"getTimeout"`
	PutTimeout        string `json:"putTimeout"`
	FastResolve       bool   `json:"fastResolve"`
	AttrTimeout       string `json:"attrTimeout"`
	EntryTimeout      string `json:"entryTimeout"`
	DirEntryTimeout   string `json:"dirEntryTimeout"`
	Debug             bool   `json:"debug"`
	NoUsageReport     bool   `json:"noUsageReport"`
	AccessLog         string `json:"accessLog"`
	PushGateway       string `json:"pushGateway"`
	PushInterval      string `json:"pushInterval"`
	PushAuth          string `json:"pushAuth"`
	PushLabels        string `json:"pushLabels"`
	Caller            int    `json:"caller"`
	UsageTag          string `json:"usageTag"`
}

// javaConf converts c into the strings jfs_init parses: durations by
// utils.Duration, bytes by utils.ParseBytesStr and Mbps by
// utils.ParseMbpsStr, which panics on "".
func (c *Config) javaConf(metaURL string) *javaConf {
	dur := func(d time.Duration) string { return d.String() }
	bytes := func(n uint64) string { return strconv.FormatUint(n, 10) + "B" }
	return &javaConf{
		MetaURL:           metaURL,
		Bucket:            c.Bucket,
		StorageClass:      c.StorageClass,
		ReadOnly:          c.ReadOnly,
		NoSession:         c.NoSession,
		NoBGJob:           c.NoBGJob,
		OpenCache:         dur(c.OpenCache),
		BackupMeta:        dur(c.BackupMeta),
		BackupSkipTrash:   c.BackupSkipTrash,
		Heartbeat:         dur(c.Heartbeat),
		CacheDir:          c.CacheDir,
		CacheSize:         bytes(c.CacheSize),
		FreeSpace:         strconv.FormatFloat(c.FreeSpaceRatio, 'g', -1, 64),
		AutoCreate:        true,
		CacheFullBlock:    !c.CachePartialOnly,
		CacheChecksum:     c.VerifyCacheChecksum,
		CacheEviction:     c.CacheEviction,
		CacheScanInterval: dur(c.CacheScanInterval),
		CacheExpire:       dur(c.CacheExpire),
		Writeback:         c.Writeback,
		MemorySize:        bytes(c.BufferSize),
		Prefetch:          c.Prefetch,
		Readahead:         bytes(c.MaxReadahead),
		UploadLimit:       strconv.FormatInt(c.UploadLimit, 10),
		DownloadLimit:     strconv.FormatInt(c.DownloadLimit, 10),
		MaxUploads:        c.MaxUploads,
		MaxDownloads:      200, // the value SelfCheck sets for 0, without its warning
		MaxDeletes:        c.MaxDeletes,
		SkipDirNlink:      c.SkipDirNlink,
		SkipDirMtime:      dur(c.SkipDirMtime),
		IORetries:         c.IORetries,
		GetTimeout:        dur(c.GetTimeout),
		PutTimeout:        dur(c.PutTimeout),
		FastResolve:       c.FastResolve,
		AttrTimeout:       dur(c.AttrCache),
		EntryTimeout:      dur(c.EntryCache),
		DirEntryTimeout:   dur(c.DirEntryCache),
		Debug:             c.Debug,
		NoUsageReport:     c.NoUsageReport,
		AccessLog:         c.AccessLog,
		PushGateway:       c.PushGateway,
		PushInterval:      dur(c.PushInterval),
		PushAuth:          c.PushAuth,
		PushLabels:        c.PushLabels,
		Caller:            1, // POSIX semantics, as Python
		UsageTag:          "go-sdk",
	}
}

func newBackend(name, metaURL string, conf *Config) (backend, error) {
	uid, gids, err := identity()
	if err != nil {
		return nil, err
	}
	js, err := json.Marshal(conf.javaConf(metaURL))
	if err != nil {
		return nil, err
	}
	cname, cconf := C.CString(name), C.CString(string(js))
	defer C.free(unsafe.Pointer(cname))
	defer C.free(unsafe.Pointer(cconf))
	// The same user and superuser make jfs_create and jfs_mkdir skip changing
	// the owner; jfs_set_ids replaces the identity from the names.
	user, empty := C.CString(strconv.FormatUint(uint64(uid), 10)), C.CString("")
	defer C.free(unsafe.Pointer(user))
	defer C.free(unsafe.Pointer(empty))
	h := C.jfs_init(0, 0, cname, cconf, user, empty, user, empty)
	if h <= 0 {
		return nil, errors.New("initialize libjfs: see its log for the reason")
	}
	if err = cerr(C.jfs_set_ids(h, C.uint32_t(uid), (*C.uint32_t)(unsafe.Pointer(&gids[0])), C.int32_t(len(gids)))); err != nil {
		C.jfs_term(pid, h, 1)
		return nil, fmt.Errorf("set identity: %w", err)
	}
	return &libjfsBackend{h: h}, nil
}

// withCString returns -EINVAL for s containing NUL, which a C string would cut.
func withCString(s string, fn func(*C.char) C.int32_t) C.int32_t {
	if strings.IndexByte(s, 0) >= 0 {
		return -C.int32_t(syscall.EINVAL)
	}
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return fn(cs)
}

// withCString2 is withCString for functions returning a value and an error.
func withCString2[T any](s string, fn func(*C.char) (T, error)) (T, error) {
	if strings.IndexByte(s, 0) >= 0 {
		var zero T
		return zero, syscall.EINVAL
	}
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return fn(cs)
}

func (b *libjfsBackend) call(p string, fn func(*C.char) C.int32_t) error {
	return cerr(withCString(p, fn))
}

func (b *libjfsBackend) call2(p1, p2 string, fn func(*C.char, *C.char) C.int32_t) error {
	return cerr(withCString(p1, func(c1 *C.char) C.int32_t {
		return withCString(p2, func(c2 *C.char) C.int32_t { return fn(c1, c2) })
	}))
}

// output returns the result in memory from malloc of a function returning its
// length.
func output(fn func(**C.uint8_t) C.int32_t) ([]byte, error) {
	var p *C.uint8_t
	r := fn(&p)
	if p != nil {
		defer C.free(unsafe.Pointer(p))
	}
	if r < 0 {
		return nil, cerr(r)
	}
	return C.GoBytes(unsafe.Pointer(p), C.int(r)), nil
}

// output2 is output for functions returning the length in size.
func output2(fn func(*unsafe.Pointer, *C.int64_t) C.int32_t) ([]byte, error) {
	var p unsafe.Pointer
	var size C.int64_t
	r := fn(&p, &size)
	if p != nil {
		defer C.free(p)
	}
	if r < 0 {
		return nil, cerr(r)
	}
	return C.GoBytes(p, C.int(size)), nil
}

const attrSize = 72

// decodeStat reads putAttr of libjfs.
func decodeStat(b []byte) *Stat {
	be := binary.BigEndian
	ts := func(off int) time.Time {
		return time.Unix(int64(be.Uint64(b[off:])), int64(be.Uint32(b[off+8:])))
	}
	return &Stat{
		Inode: be.Uint64(b[0:]),
		Mode:  be.Uint32(b[8:]),
		Nlink: be.Uint32(b[12:]),
		Uid:   be.Uint32(b[16:]),
		Gid:   be.Uint32(b[20:]),
		Rdev:  be.Uint32(b[24:]),
		Size:  be.Uint64(b[28:]),
		Atime: ts(36),
		Mtime: ts(48),
		Ctime: ts(60),
	}
}

// decodeEntries reads putEntries of libjfs.
func decodeEntries(b []byte) []*fileInfo {
	var infos []*fileInfo
	for len(b) > 0 {
		n := int(binary.BigEndian.Uint16(b))
		name := string(b[2 : 2+n])
		b = b[2+n:]
		infos = append(infos, newFileInfo(name, decodeStat(b[:attrSize])))
		b = b[attrSize:]
	}
	return infos
}

func (b *libjfsBackend) stat(p string, follow bool) (*Stat, error) {
	var buf [attrSize]byte
	var f C.int32_t
	if follow {
		f = 1
	}
	err := b.call(p, func(cp *C.char) C.int32_t {
		return C.jfs_statx(pid, b.h, cp, f, unsafe.Pointer(&buf[0]))
	})
	if err != nil {
		return nil, err
	}
	return decodeStat(buf[:]), nil
}

func (b *libjfsBackend) readdir(p string) ([]*fileInfo, error) {
	data, err := withCString2(p, func(cp *C.char) ([]byte, error) {
		return output2(func(buf *unsafe.Pointer, size *C.int64_t) C.int32_t {
			return C.jfs_listdirx(pid, b.h, cp, buf, size)
		})
	})
	if err != nil {
		return nil, err
	}
	return decodeEntries(data), nil
}

func (b *libjfsBackend) mkdir(p string, mode, umask uint16) error {
	return b.call(p, func(cp *C.char) C.int32_t {
		return C.jfs_mkdir(pid, b.h, cp, C.uint16_t(mode), C.uint16_t(umask))
	})
}

func (b *libjfsBackend) mkdirAll(p string, mode, umask uint16) error {
	return b.call(p, func(cp *C.char) C.int32_t {
		return C.jfs_mkdirAll(pid, b.h, cp, C.uint16_t(mode), C.uint16_t(umask), 0)
	})
}

func (b *libjfsBackend) remove(p string) error {
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_delete(pid, b.h, cp) })
}

func (b *libjfsBackend) rmdir(p string) error {
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_rmdir(pid, b.h, cp) })
}

func (b *libjfsBackend) removeAll(p string) error {
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_rmr(pid, b.h, cp) })
}

func (b *libjfsBackend) rename(oldpath, newpath string) error {
	return b.call2(oldpath, newpath, func(o, n *C.char) C.int32_t { return C.jfs_rename0(pid, b.h, o, n, 0) })
}

func (b *libjfsBackend) link(oldpath, newpath string) error {
	return b.call2(oldpath, newpath, func(o, n *C.char) C.int32_t { return C.jfs_link(pid, b.h, o, n) })
}

// symlink passes "/"+p, which resolves as p, so that the filepath.Rel of
// jfs_symlink fails on the relative target and keeps it (P28); "" is kept, as
// "/" would be the root.
func (b *libjfsBackend) symlink(target, p string) error {
	link := p
	if p != "" {
		link = "/" + p
	}
	return b.call2(target, link, func(t, l *C.char) C.int32_t { return C.jfs_symlink(pid, b.h, t, l) })
}

// readlink retries with a larger buffer, as jfs_readlink truncates the result.
func (b *libjfsBackend) readlink(p string) (string, error) {
	return withCString2(p, func(cp *C.char) (string, error) {
		for size := 4096; ; size *= 2 {
			buf := make([]byte, size)
			r := C.jfs_readlink(pid, b.h, cp, unsafe.Pointer(&buf[0]), C.int32_t(size))
			if r < 0 {
				return "", cerr(r)
			}
			if int(r) < size-1 {
				return string(buf[:r]), nil
			}
		}
	})
}

func (b *libjfsBackend) truncate(p string, size uint64) error {
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_truncate(pid, b.h, cp, C.uint64_t(size)) })
}

func (b *libjfsBackend) chmod(p string, mode uint16) error {
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_chmod(pid, b.h, cp, C.mode_t(mode)) })
}

func (b *libjfsBackend) chown(p string, uid, gid uint32, follow bool) error {
	return b.call(p, func(cp *C.char) C.int32_t {
		if follow {
			return C.jfs_chown(pid, b.h, cp, C.uint32_t(uid), C.uint32_t(gid))
		}
		return C.jfs_lchown(pid, b.h, cp, C.uint32_t(uid), C.uint32_t(gid))
	})
}

func (b *libjfsBackend) utime(p string, atime, mtime time.Time) error {
	// jfs_utimens leaves a time unchanged if both its parts are negative.
	split := func(t time.Time) (C.int64_t, C.int64_t) {
		if t.IsZero() {
			return -1, -1
		}
		return C.int64_t(t.Unix()), C.int64_t(t.Nanosecond())
	}
	asec, ansec := split(atime)
	msec, mnsec := split(mtime)
	return b.call(p, func(cp *C.char) C.int32_t { return C.jfs_utimens(pid, b.h, cp, asec, ansec, msec, mnsec) })
}

// The mode masks of vfs.
const (
	modeMaskR = 4
	modeMaskW = 2
)

func (b *libjfsBackend) open(p string, flag int) (fileBackend, *Stat, error) {
	var mask C.uint32_t
	switch flag & syscall.O_ACCMODE {
	case syscall.O_RDONLY:
		mask = modeMaskR
	case syscall.O_WRONLY:
		mask = modeMaskW
	default:
		mask = modeMaskR | modeMaskW
	}
	var buf [attrSize]byte
	fd := withCString(p, func(cp *C.char) C.int32_t {
		return C.jfs_openx(pid, b.h, cp, mask, unsafe.Pointer(&buf[0]))
	})
	if fd < 0 {
		return nil, nil, cerr(fd)
	}
	return &libjfsFile{b: b, fd: fd}, decodeStat(buf[:]), nil
}

func (b *libjfsBackend) create(p string, mode, umask uint16) (fileBackend, error) {
	fd := withCString(p, func(cp *C.char) C.int32_t {
		return C.jfs_create(pid, b.h, cp, C.uint16_t(mode), C.uint16_t(umask))
	})
	if fd < 0 {
		return nil, cerr(fd)
	}
	return &libjfsFile{b: b, fd: fd}, nil
}

// xattr retries with a larger buffer, as fn returns the size of the buffer
// without copying when the result does not fit (P16).
func xattr(fn func(buf unsafe.Pointer, size C.int32_t) C.int32_t) ([]byte, error) {
	for size := 4096; ; size *= 2 {
		buf := make([]byte, size)
		r := fn(unsafe.Pointer(&buf[0]), C.int32_t(size))
		if r < 0 {
			return nil, cerr(r)
		}
		if int(r) < size {
			return buf[:r], nil
		}
	}
}

func (b *libjfsBackend) getxattr(p, attr string) ([]byte, error) {
	return withCString2(p, func(cp *C.char) ([]byte, error) {
		return withCString2(attr, func(ca *C.char) ([]byte, error) {
			return xattr(func(buf unsafe.Pointer, size C.int32_t) C.int32_t {
				return C.jfs_getXattr(pid, b.h, cp, ca, buf, size)
			})
		})
	})
}

func (b *libjfsBackend) listxattr(p string) ([]byte, error) {
	return withCString2(p, func(cp *C.char) ([]byte, error) {
		return xattr(func(buf unsafe.Pointer, size C.int32_t) C.int32_t {
			return C.jfs_listXattr(pid, b.h, cp, buf, size)
		})
	})
}

func (b *libjfsBackend) setxattr(p, attr string, data []byte, flags XattrFlag) error {
	// libjfs cannot take a nil pointer, even for no data.
	value := []byte{0}
	if len(data) > 0 {
		value = data
	}
	return b.call2(p, attr, func(cp, ca *C.char) C.int32_t {
		return C.jfs_setXattr(pid, b.h, cp, ca, unsafe.Pointer(&value[0]), C.int32_t(len(data)), C.int32_t(flags))
	})
}

func (b *libjfsBackend) removexattr(p, attr string) error {
	return b.call2(p, attr, func(cp, ca *C.char) C.int32_t { return C.jfs_removeXattr(pid, b.h, cp, ca) })
}

func cbool(v bool) C.uint8_t {
	if v {
		return 1
	}
	return 0
}

func (b *libjfsBackend) clone(src, dst string, preserve bool) error {
	return b.call2(src, dst, func(s, d *C.char) C.int32_t { return C.jfs_clone(pid, b.h, s, d, cbool(preserve)) })
}

// outputJSON decodes the JSON in output into v.
func outputJSON(p string, v any, fn func(*C.char, **C.uint8_t) C.int32_t) error {
	data, err := withCString2(p, func(cp *C.char) ([]byte, error) {
		return output(func(buf **C.uint8_t) C.int32_t { return fn(cp, buf) })
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (b *libjfsBackend) info(p string, recursive, strict bool) (*InfoResult, error) {
	var r InfoResult // the same fields as meta.Summary
	err := outputJSON(p, &r, func(cp *C.char, buf **C.uint8_t) C.int32_t {
		return C.jfs_info(pid, b.h, cp, buf, cbool(recursive), cbool(strict))
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// metaTreeSummary is meta.TreeSummary.
type metaTreeSummary struct {
	Path              string
	Type              uint8
	Size, Files, Dirs uint64
	Children          []*metaTreeSummary
}

// metaTypes are the type bits of the file types of meta.
var metaTypes = []fs.FileMode{0, 0, fs.ModeDir, fs.ModeSymlink, fs.ModeNamedPipe, fs.ModeDevice, fs.ModeDevice | fs.ModeCharDevice, fs.ModeSocket}

func (s *metaTreeSummary) convert() *TreeSummary {
	t := &TreeSummary{Path: s.Path, Size: s.Size, Files: s.Files, Dirs: s.Dirs}
	if int(s.Type) < len(metaTypes) {
		t.Type = metaTypes[s.Type]
	}
	for _, c := range s.Children {
		t.Children = append(t.Children, c.convert())
	}
	return t
}

func (b *libjfsBackend) summary(p string, depth, entries uint8) (*TreeSummary, error) {
	var s metaTreeSummary
	err := outputJSON(p, &s, func(cp *C.char, buf **C.uint8_t) C.int32_t {
		return C.jfs_gettreesummary(pid, b.h, cp, C.uint8_t(depth), C.uint8_t(entries), buf)
	})
	if err != nil {
		return nil, err
	}
	return s.convert(), nil
}

func (b *libjfsBackend) quota(cmd uint8, name string, capacity, inodes uint64, strict, repair, create bool) (map[string]*Quota, error) {
	var qs map[string]*Quota // the same fields as meta.Quota
	err := outputJSON(name, &qs, func(cp *C.char, buf **C.uint8_t) C.int32_t {
		return C.jfs_quota(pid, b.h, cp, C.uint8_t(cmd), C.uint64_t(capacity), C.uint64_t(inodes), cbool(strict), cbool(repair), cbool(create), buf)
	})
	if err != nil {
		return nil, err
	}
	if qs == nil {
		qs = make(map[string]*Quota)
	}
	return qs, nil
}

// warmup runs in the foreground in a goroutine for background, as the result
// of jfs_warmup races with its goroutine (P17).
func (b *libjfsBackend) warmup(paths []string, threads int, evict, check, background bool) (*WarmupResult, error) {
	js, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	run := func() (*WarmupResult, error) {
		r := &WarmupResult{} // the same fields as vfs.CacheResponse
		err := outputJSON(string(js), r, func(cp *C.char, buf **C.uint8_t) C.int32_t {
			return C.jfs_warmup(pid, b.h, cp, C.int32_t(threads), 0, cbool(evict), cbool(check), buf)
		})
		if err != nil {
			return nil, err
		}
		if r.Locations == nil {
			r.Locations = make(map[string]uint64)
		}
		return r, nil
	}
	if background {
		go func() { _, _ = run() }()
		return nil, nil
	}
	return run()
}

// metaFormat is the part of meta.Format in StatusResult.
type metaFormat struct {
	Name, UUID, Storage, Bucket string
	Tiers                       map[string]struct{ StorageClass string }
	BlockSize                   int
	Compression                 string
	Capacity, Inodes            uint64
	EncryptKey, EncryptAlgo     string
	UploadLimit, DownloadLimit  int64
	TrashDays                   int
	EnableACL                   bool
}

// metaSession is meta.Session.
type metaSession struct {
	Sid        uint64
	Expire     time.Time
	Version    string
	HostName   string
	IPAddrs    []string
	MountPoint string
	MountTime  time.Time
	ProcessID  int
	Sustained  []uint64
	Flocks     []struct {
		Inode, Owner uint64
		Ltype        string
	}
	Plocks []struct {
		Inode, Owner uint64
		Records      []struct {
			Type       uint32
			Pid        uint32
			Start, End uint64
		}
	}
}

func (s *metaSession) convert() *Session {
	r := &Session{
		Sid:        s.Sid,
		Expire:     s.Expire,
		Version:    s.Version,
		HostName:   s.HostName,
		IPAddrs:    s.IPAddrs,
		MountPoint: s.MountPoint,
		MountTime:  s.MountTime,
		ProcessID:  s.ProcessID,
		Sustained:  s.Sustained,
	}
	for _, l := range s.Flocks {
		r.Flocks = append(r.Flocks, Flock{Inode: l.Inode, Owner: l.Owner, Type: l.Ltype})
	}
	for _, l := range s.Plocks {
		p := Plock{Inode: l.Inode, Owner: l.Owner}
		for _, rec := range l.Records {
			p.Records = append(p.Records, PlockRecord{Type: plockType(rec.Type), Pid: rec.Pid, Start: rec.Start, End: rec.End})
		}
		r.Plocks = append(r.Plocks, p)
	}
	return r
}

// plockType converts F_RDLCK and F_WRLCK, whose values differ by platform.
func plockType(t uint32) string {
	switch t {
	case syscall.F_RDLCK:
		return "R"
	case syscall.F_WRLCK:
		return "W"
	}
	return fmt.Sprint(t)
}

// removePassword is utils.RemovePassword.
func removePassword(uri string) string {
	p := strings.LastIndex(uri, "@")
	if p < 0 {
		return uri
	}
	sp := strings.Index(uri, "://") + 3
	if sp == 2 {
		sp = 0
	}
	cp := strings.Index(uri[sp:], ":")
	if cp < 0 || sp+cp > p {
		return uri
	}
	return uri[:sp+cp] + ":****" + uri[p:]
}

func (b *libjfsBackend) status(trash bool) (*StatusResult, error) {
	data, err := output(func(buf **C.uint8_t) C.int32_t {
		return C.jfs_status(pid, b.h, cbool(trash), 0, buf)
	})
	if err != nil {
		return nil, err
	}
	return decodeStatus(data, trash)
}

// decodeStatus converts the JSON of meta.Sections, whose Statistic has the
// fields of VolumeStat and TrashStat.
func decodeStatus(data []byte, trash bool) (*StatusResult, error) {
	var s struct {
		Setting  metaFormat
		Sessions []*metaSession
		Stat     struct {
			VolumeStat
			TrashStat
		}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	f := &s.Setting
	r := &StatusResult{
		Setting: VolumeSetting{
			Name:          f.Name,
			UUID:          f.UUID,
			Storage:       f.Storage,
			Bucket:        removePassword(f.Bucket),
			StorageClass:  f.Tiers["0"].StorageClass,
			BlockSize:     f.BlockSize << 10,
			Compression:   f.Compression,
			Capacity:      f.Capacity,
			Inodes:        f.Inodes,
			TrashDays:     f.TrashDays,
			UploadLimit:   f.UploadLimit,
			DownloadLimit: f.DownloadLimit,
			EnableACL:     f.EnableACL,
		},
		Stat: s.Stat.VolumeStat,
	}
	if f.EncryptKey != "" {
		r.Setting.EncryptAlgo = f.EncryptAlgo
	}
	for _, ss := range s.Sessions {
		r.Sessions = append(r.Sessions, ss.convert())
	}
	if trash {
		t := s.Stat.TrashStat
		r.Stat.Trash = &t
	}
	return r, nil
}

func (b *libjfsBackend) sessionInfo(sid uint64) (*Session, error) {
	// jfs_status takes 0 as no session; meta has no session 0.
	if sid == 0 {
		return nil, syscall.EIO
	}
	data, err := output(func(buf **C.uint8_t) C.int32_t {
		return C.jfs_status(pid, b.h, 0, C.uint64_t(sid), buf)
	})
	if err != nil {
		return nil, err
	}
	var s metaSession
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return s.convert(), nil
}

// gatherer parses the metrics of libjfs, which keeps them in its own registry.
func (b *libjfsBackend) gatherer() prometheus.Gatherer {
	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		data, err := output2(func(buf *unsafe.Pointer, size *C.int64_t) C.int32_t {
			return C.jfs_metrics(b.h, buf, size)
		})
		if err != nil {
			return nil, err
		}
		var parser expfmt.TextParser
		families, err := parser.TextToMetricFamilies(strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		mfs := make([]*dto.MetricFamily, 0, len(families))
		for _, mf := range families {
			mfs = append(mfs, mf)
		}
		sort.Slice(mfs, func(i, j int) bool { return mfs[i].GetName() < mfs[j].GetName() })
		return mfs, nil
	})
}

func (b *libjfsBackend) close() error {
	return cerr(C.jfs_term(pid, b.h, 1))
}

type libjfsFile struct {
	b  *libjfsBackend
	fd C.int32_t
}

func (f *libjfsFile) pread(b []byte, off int64) (int, error) {
	var n int
	for n < len(b) {
		c := min(len(b)-n, maxIO)
		r := C.jfs_pread(pid, f.fd, unsafe.Pointer(&b[n]), C.int32_t(c), C.int64_t(off+int64(n)))
		if r < 0 {
			return n, cerr(r)
		}
		n += int(r)
		if int(r) < c {
			break
		}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (f *libjfsFile) pwrite(b []byte, off int64) (int, error) {
	var n int
	for n < len(b) {
		c := min(len(b)-n, maxIO)
		r := C.jfs_pwrite(pid, f.fd, unsafe.Pointer(&b[n]), C.int32_t(c), C.int64_t(off+int64(n)))
		if r < 0 {
			return n, cerr(r)
		}
		n += int(r)
	}
	return n, nil
}

func (f *libjfsFile) truncate(size int64) error {
	return cerr(C.jfs_ftruncate(pid, f.fd, C.uint64_t(size)))
}

func (f *libjfsFile) readdir() ([]*fileInfo, error) {
	data, err := output2(func(buf *unsafe.Pointer, size *C.int64_t) C.int32_t {
		return C.jfs_freaddirx(pid, f.fd, buf, size)
	})
	if err != nil {
		return nil, err
	}
	return decodeEntries(data), nil
}

func (f *libjfsFile) fsync() error {
	return cerr(C.jfs_fsync(pid, f.fd))
}

func (f *libjfsFile) stat() (*Stat, error) {
	var buf [attrSize]byte
	if err := cerr(C.jfs_fstatx(pid, f.fd, unsafe.Pointer(&buf[0]))); err != nil {
		return nil, err
	}
	return decodeStat(buf[:]), nil
}

func (f *libjfsFile) chmod(mode uint16) error {
	return cerr(C.jfs_fchmod(pid, f.fd, C.uint16_t(mode)))
}

func (f *libjfsFile) chown(uid, gid uint32) error {
	return cerr(C.jfs_fchown(pid, f.fd, C.uint32_t(uid), C.uint32_t(gid)))
}

func (f *libjfsFile) close() error {
	return cerr(C.jfs_close(pid, f.fd))
}
