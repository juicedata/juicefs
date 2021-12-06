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

package main

// #cgo linux LDFLAGS: -ldl
// #cgo linux CFLAGS: -Wno-discarded-qualifiers -D_GNU_SOURCE
// #include <unistd.h>
// #include <inttypes.h>
// #include <sys/types.h>
// #include <sys/stat.h>
// #include <fcntl.h>
// #include <utime.h>
import "C"
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/sirupsen/logrus"
)

var (
	filesLock     sync.Mutex
	openFiles     = make(map[int]*fwrapper)
	minFreeHandle = 1

	fslock   sync.Mutex
	handlers = make(map[uintptr]*wrapper)
	activefs = make(map[string][]*wrapper)
	logger   = utils.GetLogger("juicefs")
	pusher   *push.Pusher
)

const (
	EPERM     = -0x01
	ENOENT    = -0x02
	EINTR     = -0x04
	EIO       = -0x05
	EACCES    = -0x0d
	EEXIST    = -0x11
	ENOTDIR   = -0x14
	EINVAL    = -0x16
	ENOSPC    = -0x1c
	EROFS     = -0x1e
	ENOTEMPTY = -0x27
	ENODATA   = -0x3d
	ENOTSUP   = -0x5f
)

func errno(err error) int {
	if err == nil {
		return 0
	}
	eno, ok := err.(syscall.Errno)
	if !ok {
		return EIO
	}
	if eno == 0 {
		return 0
	}
	// Use the errno in Linux for all the OS
	switch eno {
	case syscall.EPERM:
		return EPERM
	case syscall.ENOENT:
		return ENOENT
	case syscall.EINTR:
		return EINTR
	case syscall.EIO:
		return EIO
	case syscall.EACCES:
		return EACCES
	case syscall.EEXIST:
		return EEXIST
	case syscall.ENOTDIR:
		return ENOTDIR
	case syscall.EINVAL:
		return EINVAL
	case syscall.ENOSPC:
		return ENOSPC
	case syscall.EROFS:
		return EROFS
	case syscall.ENOTEMPTY:
		return ENOTEMPTY
	case syscall.ENODATA:
		return ENODATA
	case syscall.ENOTSUP:
		return ENOTSUP
	default:
		logger.Warnf("unknown errno %d: %s", eno, err)
		return -int(eno)
	}
}

type wrapper struct {
	*fs.FileSystem
	ctx        meta.Context
	m          *mapping
	user       string
	superuser  string
	supergroup string
}

func (w *wrapper) withPid(pid int) meta.Context {
	// mapping Java Thread ID to global one
	ctx := meta.NewContext(w.ctx.Pid()*1000+uint32(pid), w.ctx.Uid(), w.ctx.Gids())
	ctx.WithValue(meta.CtxKey("behavior"), "Hadoop")
	return ctx
}

func (w *wrapper) lookupUid(name string) uint32 {
	if name == w.superuser {
		return 0
	}
	return uint32(w.m.lookupUser(name))
}

func (w *wrapper) lookupGid(group string) uint32 {
	if group == w.supergroup {
		return 0
	}
	return uint32(w.m.lookupGroup(group))
}

func (w *wrapper) lookupGids(groups string) []uint32 {
	var gids []uint32
	for _, g := range strings.Split(groups, ",") {
		gids = append(gids, w.lookupGid(g))
	}
	return gids
}

func (w *wrapper) uid2name(uid uint32) string {
	name := w.superuser
	if uid > 0 {
		name = w.m.lookupUserID(int(uid))
	}
	return name
}

func (w *wrapper) gid2name(gid uint32) string {
	group := w.supergroup
	if gid > 0 {
		group = w.m.lookupGroupID(int(gid))
	}
	return group
}

type fwrapper struct {
	*fs.File
	w *wrapper
}

func nextFileHandle(f *fs.File, w *wrapper) int {
	filesLock.Lock()
	defer filesLock.Unlock()
	for i := minFreeHandle; ; i++ {
		if _, ok := openFiles[i]; !ok {
			openFiles[i] = &fwrapper{f, w}
			minFreeHandle = i + 1
			return i
		}
	}
}

func freeHandle(fd int) {
	filesLock.Lock()
	defer filesLock.Unlock()
	f := openFiles[fd]
	if f != nil {
		delete(openFiles, fd)
		if fd < minFreeHandle {
			minFreeHandle = fd
		}
	}
}

type javaConf struct {
	MetaURL         string  `json:"meta"`
	Bucket          string  `json:"bucket"`
	ReadOnly        bool    `json:"readOnly"`
	OpenCache       float64 `json:"openCache"`
	CacheDir        string  `json:"cacheDir"`
	CacheSize       int64   `json:"cacheSize"`
	FreeSpace       string  `json:"freeSpace"`
	AutoCreate      bool    `json:"autoCreate"`
	CacheFullBlock  bool    `json:"cacheFullBlock"`
	Writeback       bool    `json:"writeback"`
	MemorySize      int     `json:"memorySize"`
	Prefetch        int     `json:"prefetch"`
	Readahead       int     `json:"readahead"`
	UploadLimit     int     `json:"uploadLimit"`
	DownloadLimit   int     `json:"downloadLimit"`
	MaxUploads      int     `json:"maxUploads"`
	GetTimeout      int     `json:"getTimeout"`
	PutTimeout      int     `json:"putTimeout"`
	FastResolve     bool    `json:"fastResolve"`
	AttrTimeout     float64 `json:"attrTimeout"`
	EntryTimeout    float64 `json:"entryTimeout"`
	DirEntryTimeout float64 `json:"dirEntryTimeout"`
	Debug           bool    `json:"debug"`
	NoUsageReport   bool    `json:"noUsageReport"`
	AccessLog       string  `json:"accessLog"`
	PushGateway     string  `json:"pushGateway"`
	PushInterval    int     `json:"pushInterval"`
	PushAuth        string  `json:"pushAuth"`
}

func getOrCreate(name, user, group, superuser, supergroup string, f func() *fs.FileSystem) uintptr {
	fslock.Lock()
	defer fslock.Unlock()
	ws := activefs[name]
	var jfs *fs.FileSystem
	var m *mapping
	if len(ws) > 0 {
		jfs = ws[0].FileSystem
		m = ws[0].m
	} else {
		m = newMapping(name)
		jfs = f()
		if jfs == nil {
			return 0
		}
		logger.Infof("JuiceFileSystem created for user:%s group:%s", user, group)
	}
	w := &wrapper{jfs, nil, m, user, superuser, supergroup}
	w.ctx = meta.NewContext(uint32(os.Getpid()), w.lookupUid(user), w.lookupGids(group))
	// root is a normal user in Hadoop, but super user in POSIX (ignored in GUID mapping)
	// woraround: lookup it here to create a bidirectional mapping
	w.lookupUid("root")
	w.lookupGid("root")
	activefs[name] = append(ws, w)
	h := uintptr(unsafe.Pointer(w)) & 0x7fffffff // low 32bits
	handlers[h] = w
	return h
}

func createStorage(format *meta.Format) (object.ObjectStorage, error) {
	var blob object.ObjectStorage
	var err error
	if format.Shards > 1 {
		blob, err = object.NewSharded(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey, format.Shards)
	} else {
		blob, err = object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey)
	}
	if err != nil {
		return nil, err
	}
	return object.WithPrefix(blob, format.Name+"/"), nil
}

//export jfs_init
func jfs_init(cname, jsonConf, user, group, superuser, supergroup *C.char) uintptr {
	name := C.GoString(cname)
	debug.SetGCPercent(50)
	object.UserAgent = "JuiceFS-SDK " + version.Version()
	return getOrCreate(name, C.GoString(user), C.GoString(group), C.GoString(superuser), C.GoString(supergroup), func() *fs.FileSystem {
		var jConf javaConf
		err := json.Unmarshal([]byte(C.GoString(jsonConf)), &jConf)
		if err != nil {
			logger.Fatalf("invalid json: %s", C.GoString(jsonConf))
		}
		if jConf.Debug || os.Getenv("JUICEFS_DEBUG") != "" {
			utils.SetLogLevel(logrus.DebugLevel)
			go func() {
				for port := 6060; port < 6100; port++ {
					logger.Debugf("listen at 127.0.0.1:%d", port)
					_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil)
				}
			}()
		} else if os.Getenv("JUICEFS_LOGLEVEL") != "" {
			level, err := logrus.ParseLevel(os.Getenv("JUICEFS_LOGLEVEL"))
			if err == nil {
				utils.SetLogLevel(level)
			} else {
				utils.SetLogLevel(logrus.WarnLevel)
				logger.Errorf("JUICEFS_LOGLEVEL: %s", err)
			}
		} else {
			utils.SetLogLevel(logrus.WarnLevel)
		}

		addr := jConf.MetaURL
		m := meta.NewClient(addr, &meta.Config{
			Retries:   10,
			Strict:    true,
			ReadOnly:  jConf.ReadOnly,
			OpenCache: time.Duration(jConf.OpenCache * 1e9),
		})
		format, err := m.Load()
		if err != nil {
			logger.Fatalf("load setting: %s", err)
		}

		if jConf.PushGateway != "" && pusher == nil {
			prometheus.DefaultRegisterer = prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.DefaultRegisterer)
			// TODO: support multiple volumes
			pusher = push.New(jConf.PushGateway, "juicefs").Gatherer(prometheus.DefaultGatherer)
			pusher = pusher.Grouping("vol_name", format.Name).Grouping("mp", "sdk-"+strconv.Itoa(os.Getpid()))
			if h, err := os.Hostname(); err == nil {
				pusher = pusher.Grouping("instance", h)
			} else {
				logger.Warnf("cannot get hostname: %s", err)
			}
			if strings.Contains(jConf.PushAuth, ":") {
				parts := strings.Split(jConf.PushAuth, ":")
				pusher = pusher.BasicAuth(parts[0], parts[1])
			}
			interval := time.Second * 10
			if jConf.PushInterval > 0 {
				interval = time.Second * time.Duration(jConf.PushInterval)
			}
			go func() {
				for {
					time.Sleep(interval)
					if err := pusher.Push(); err != nil {
						logger.Warnf("push metrics to %s: %s", jConf.PushGateway, err)
					}
				}
			}()
			meta.InitMetrics()
			vfs.InitMetrics()
			go metric.UpdateMetrics(m)
		}

		if jConf.Bucket != "" {
			format.Bucket = jConf.Bucket
		}
		blob, err := createStorage(format)
		if err != nil {
			logger.Fatalf("object storage: %s", err)
		}
		logger.Infof("Data use %s", blob)

		var freeSpaceRatio = 0.1
		if jConf.FreeSpace != "" {
			freeSpaceRatio, _ = strconv.ParseFloat(jConf.FreeSpace, 64)
		}
		chunkConf := chunk.Config{
			BlockSize:      format.BlockSize * 1024,
			Compress:       format.Compression,
			CacheDir:       jConf.CacheDir,
			CacheMode:      0644, // all user can read cache
			CacheSize:      jConf.CacheSize,
			FreeSpace:      float32(freeSpaceRatio),
			AutoCreate:     jConf.AutoCreate,
			CacheFullBlock: jConf.CacheFullBlock,
			MaxUpload:      jConf.MaxUploads,
			UploadLimit:    int64(jConf.UploadLimit) * 1e6 / 8,
			DownloadLimit:  int64(jConf.DownloadLimit) * 1e6 / 8,
			Prefetch:       jConf.Prefetch,
			Writeback:      jConf.Writeback,
			Partitions:     format.Partitions,
			GetTimeout:     time.Second * time.Duration(jConf.GetTimeout),
			PutTimeout:     time.Second * time.Duration(jConf.PutTimeout),
			BufferSize:     jConf.MemorySize << 20,
			Readahead:      jConf.Readahead << 20,
		}
		if chunkConf.CacheDir != "memory" {
			ds := utils.SplitDir(chunkConf.CacheDir)
			for i := range ds {
				ds[i] = filepath.Join(ds[i], format.UUID)
			}
			chunkConf.CacheDir = strings.Join(ds, string(os.PathListSeparator))
		}
		store := chunk.NewCachedStore(blob, chunkConf)
		m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
			chunkid := args[0].(uint64)
			length := args[1].(uint32)
			return store.Remove(chunkid, int(length))
		}))
		m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			chunkid := args[1].(uint64)
			return vfs.Compact(chunkConf, store, slices, chunkid)
		}))
		err = m.NewSession()
		if err != nil {
			logger.Fatalf("new session: %s", err)
		}

		conf := &vfs.Config{
			Meta: &meta.Config{
				Retries: 10,
			},
			Format:          format,
			Chunk:           &chunkConf,
			AttrTimeout:     time.Millisecond * time.Duration(jConf.AttrTimeout*1000),
			EntryTimeout:    time.Millisecond * time.Duration(jConf.EntryTimeout*1000),
			DirEntryTimeout: time.Millisecond * time.Duration(jConf.DirEntryTimeout*1000),
			AccessLog:       jConf.AccessLog,
			FastResolve:     jConf.FastResolve,
		}
		if !jConf.NoUsageReport {
			go usage.ReportUsage(m, "java-sdk "+version.Version())
		}
		jfs, err := fs.NewFileSystem(conf, m, store)
		if err != nil {
			logger.Errorf("Initialize failed: %s", err)
			return nil
		}
		return jfs
	})
}

func F(p uintptr) *wrapper {
	fslock.Lock()
	defer fslock.Unlock()
	return handlers[p]
}

//export jfs_update_uid_grouping
func jfs_update_uid_grouping(h uintptr, uidstr *C.char, grouping *C.char) {
	w := F(h)
	if w == nil {
		return
	}
	var uids []pwent
	if uidstr != nil {
		for _, line := range strings.Split(C.GoString(uidstr), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 2 {
				continue
			}
			username := strings.TrimSpace(fields[0])
			uid, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
			uids = append(uids, pwent{uid, username})
		}

		var buffer bytes.Buffer
		for _, u := range uids {
			buffer.WriteString(fmt.Sprintf("\t%v:%v\n", u.name, u.id))
		}
		logger.Debugf("Update uids mapping\n %s", buffer.String())
	}

	var gids []pwent
	var groups []string
	if grouping != nil {
		for _, line := range strings.Split(C.GoString(grouping), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 2 {
				continue
			}
			gname := strings.TrimSpace(fields[0])
			gid, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
			gids = append(gids, pwent{gid, gname})
			if len(fields) > 2 {
				for _, user := range strings.Split(fields[len(fields)-1], ",") {
					if strings.TrimSpace(user) == w.user {
						groups = append(groups, gname)
					}
				}
			}
		}
		logger.Debugf("Update groups of %s to %s", w.user, strings.Join(groups, ","))
	}
	w.m.update(uids, gids)

	curGids := w.ctx.Gids()
	if len(groups) > 0 {
		curGids = w.lookupGids(strings.Join(groups, ","))
	}
	w.ctx = meta.NewContext(uint32(os.Getpid()), w.lookupUid(w.user), curGids)
}

//export jfs_term
func jfs_term(pid int, h uintptr) int {
	w := F(h)
	if w == nil {
		return 0
	}
	ctx := w.withPid(pid)
	// sync all open files
	filesLock.Lock()
	var m sync.WaitGroup
	var toClose []int
	for fd, f := range openFiles {
		if f.w == w {
			m.Add(1)
			go func(f *fs.File) {
				defer m.Done()
				f.Close(ctx)
			}(f.File)
			toClose = append(toClose, fd)
		}
	}
	for _, fd := range toClose {
		delete(openFiles, fd)
	}
	filesLock.Unlock()
	m.Wait()

	fslock.Lock()
	defer fslock.Unlock()
	delete(handlers, h)
	for name, ws := range activefs {
		for i := range ws {
			if ws[i] == w {
				if len(ws) > 1 {
					ws[i] = ws[len(ws)-1]
					activefs[name] = ws[:len(ws)-1]
				} else {
					w.Flush()
					// don't close the filesystem, so it can be re-used later
					// w.Close()
					// delete(activefs, name)
				}
			}
		}
	}
	if pusher != nil {
		if err := pusher.Push(); err != nil {
			logger.Warnf("push metrics: %s", err)
		}
	}
	return 0
}

//export jfs_open
func jfs_open(pid int, h uintptr, cpath *C.char, flags int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	path := C.GoString(cpath)
	f, err := w.Open(w.withPid(pid), path, uint32(flags))
	if err != 0 {
		return errno(err)
	}
	st, _ := f.Stat()
	if st.IsDir() {
		return ENOENT
	}
	return nextFileHandle(f, w)
}

//export jfs_access
func jfs_access(pid int, h uintptr, cpath *C.char, flags int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Access(w.withPid(pid), C.GoString(cpath), flags))
}

//export jfs_create
func jfs_create(pid int, h uintptr, cpath *C.char, mode uint16) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	path := C.GoString(cpath)
	f, err := w.Create(w.withPid(pid), path, mode)
	if err != 0 {
		return errno(err)
	}
	return nextFileHandle(f, w)
}

//export jfs_mkdir
func jfs_mkdir(pid int, h uintptr, cpath *C.char, mode C.mode_t) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Mkdir(w.withPid(pid), C.GoString(cpath), uint16(mode)))
}

//export jfs_delete
func jfs_delete(pid int, h uintptr, cpath *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Delete(w.withPid(pid), C.GoString(cpath)))
}

//export jfs_rmr
func jfs_rmr(pid int, h uintptr, cpath *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Rmr(w.withPid(pid), C.GoString(cpath)))
}

//export jfs_rename
func jfs_rename(pid int, h uintptr, oldpath *C.char, newpath *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Rename(w.withPid(pid), C.GoString(oldpath), C.GoString(newpath), meta.RenameNoReplace))
}

//export jfs_truncate
func jfs_truncate(pid int, h uintptr, path *C.char, length uint64) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Truncate(w.withPid(pid), C.GoString(path), length))
}

//export jfs_setXattr
func jfs_setXattr(pid int, h uintptr, path *C.char, name *C.char, value uintptr, vlen int, mode int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	var flags uint32
	switch mode {
	case 1:
		flags = meta.XattrCreate
	case 2:
		flags = meta.XattrReplace
	}
	return errno(w.SetXattr(w.withPid(pid), C.GoString(path), C.GoString(name), toBuf(value, vlen), flags))
}

//export jfs_getXattr
func jfs_getXattr(pid int, h uintptr, path *C.char, name *C.char, buf uintptr, bufsize int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	buff, err := w.GetXattr(w.withPid(pid), C.GoString(path), C.GoString(name))
	if err != 0 {
		return errno(err)
	}
	if len(buff) >= bufsize {
		return bufsize
	}
	copy(toBuf(uintptr(buf), bufsize), buff)
	return len(buff)
}

//export jfs_listXattr
func jfs_listXattr(pid int, h uintptr, path *C.char, buf uintptr, bufsize int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	buff, err := w.ListXattr(w.withPid(pid), C.GoString(path))
	if err != 0 {
		return errno(err)
	}
	if len(buff) >= bufsize {
		return bufsize
	}
	copy(toBuf(uintptr(buf), bufsize), buff)
	return len(buff)
}

//export jfs_removeXattr
func jfs_removeXattr(pid int, h uintptr, path *C.char, name *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.RemoveXattr(w.withPid(pid), C.GoString(path), C.GoString(name)))
}

//export jfs_symlink
func jfs_symlink(pid int, h uintptr, target *C.char, link *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Symlink(w.withPid(pid), C.GoString(target), C.GoString(link)))
}

//export jfs_readlink
func jfs_readlink(pid int, h uintptr, link *C.char, buf uintptr, bufsize int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	target, err := w.Readlink(w.withPid(pid), C.GoString(link))
	if err != 0 {
		return errno(err)
	}
	if len(target)+1 >= bufsize {
		target = target[:bufsize-1]
	}
	wb := utils.NewNativeBuffer(toBuf(buf, bufsize))
	wb.Put(target)
	wb.Put8(0)
	return len(target)
}

// mode:4 length:8 mtime:8 atime:8 user:50 group:50
func fill_stat(w *wrapper, wb *utils.Buffer, st *fs.FileStat) int {
	wb.Put32(uint32(st.Mode()))
	wb.Put64(uint64(st.Size()))
	wb.Put64(uint64(st.Mtime()))
	wb.Put64(uint64(st.Atime()))
	user := w.uid2name(uint32(st.Uid()))
	wb.Put([]byte(user))
	wb.Put8(0)
	group := w.gid2name(uint32(st.Gid()))
	wb.Put([]byte(group))
	wb.Put8(0)
	return 30 + len(user) + len(group)
}

//export jfs_stat1
func jfs_stat1(pid int, h uintptr, cpath *C.char, buf uintptr) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	info, err := w.Stat(w.withPid(pid), C.GoString(cpath))
	if err != 0 {
		return errno(err)
	}
	return fill_stat(w, utils.NewNativeBuffer(toBuf(buf, 130)), info)
}

//export jfs_lstat1
func jfs_lstat1(pid int, h uintptr, cpath *C.char, buf uintptr) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	fi, err := w.Stat(w.withPid(pid), C.GoString(cpath))
	if err != 0 {
		return errno(err)
	}
	return fill_stat(w, utils.NewNativeBuffer(toBuf(buf, 130)), fi)
}

//export jfs_summary
func jfs_summary(pid int, h uintptr, cpath *C.char, buf uintptr) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	ctx := w.withPid(pid)
	f, err := w.Open(ctx, C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(ctx)
	summary, err := f.Summary(ctx, 0, 1)
	if err != 0 {
		return errno(err)
	}
	wb := utils.NewNativeBuffer(toBuf(buf, 24))
	wb.Put64(summary.Length)
	wb.Put64(summary.Files)
	wb.Put64(summary.Dirs)
	return 24
}

//export jfs_statvfs
func jfs_statvfs(pid int, h uintptr, buf uintptr) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	total, avail := w.StatFS(w.withPid(pid))
	wb := utils.NewNativeBuffer(toBuf(buf, 16))
	wb.Put64(total)
	wb.Put64(avail)
	return 0
}

//export jfs_chmod
func jfs_chmod(pid int, h uintptr, cpath *C.char, mode C.mode_t) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(w.withPid(pid))
	return errno(f.Chmod(w.withPid(pid), uint16(mode)))
}

//export jfs_chown
func jfs_chown(pid int, h uintptr, cpath *C.char, uid uint32, gid uint32) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(w.withPid(pid))
	return errno(f.Chown(w.withPid(pid), uid, gid))
}

//export jfs_utime
func jfs_utime(pid int, h uintptr, cpath *C.char, mtime, atime int64) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(w.withPid(pid))
	return errno(f.Utime(w.withPid(pid), atime, mtime))
}

//export jfs_setOwner
func jfs_setOwner(pid int, h uintptr, cpath *C.char, owner *C.char, group *C.char) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(w.withPid(pid))
	st, _ := f.Stat()
	uid := uint32(st.(*fs.FileStat).Uid())
	gid := uint32(st.(*fs.FileStat).Gid())
	if owner != nil {
		uid = w.lookupUid(C.GoString(owner))
	}
	if group != nil {
		gid = w.lookupGid(C.GoString(group))
	}
	return errno(f.Chown(w.withPid(pid), uid, gid))
}

//export jfs_listdir
func jfs_listdir(pid int, h uintptr, cpath *C.char, offset int, buf uintptr, bufsize int) int {
	var ctx meta.Context
	var f *fs.File
	var w *wrapper
	if offset > 0 {
		filesLock.Lock()
		fw := openFiles[int(h)]
		filesLock.Unlock()
		if fw == nil {
			return EINVAL
		}
		freeHandle(int(h))
		w = fw.w
		f = fw.File
		ctx = w.withPid(pid)
	} else {
		w = F(h)
		if w == nil {
			return EINVAL
		}
		var err syscall.Errno
		ctx = w.withPid(pid)
		f, err = w.Open(ctx, C.GoString(cpath), 0)
		if err != 0 {
			return errno(err)
		}
		st, _ := f.Stat()
		if !st.IsDir() {
			return ENOTDIR
		}
	}

	es, err := f.ReaddirPlus(ctx, offset)
	if err != 0 {
		return errno(err)
	}

	wb := utils.NewNativeBuffer(toBuf(buf, bufsize))
	for i, d := range es {
		if wb.Left() < 1+len(d.Name)+1+130+8 {
			wb.Put32(uint32(len(es) - i))
			wb.Put32(uint32(nextFileHandle(f, w)))
			return bufsize - wb.Left() - 8
		}
		wb.Put8(byte(len(d.Name)))
		wb.Put(d.Name)
		header := wb.Get(1)
		header[0] = uint8(fill_stat(w, wb, fs.AttrToFileInfo(d.Inode, d.Attr)))
	}
	wb.Put32(0)
	return bufsize - wb.Left() - 4
}

func toBuf(s uintptr, sz int) []byte {
	return (*[1 << 30]byte)(unsafe.Pointer(s))[:sz:sz]
}

//export jfs_concat
func jfs_concat(pid int, h uintptr, _dst *C.char, buf uintptr, bufsize int) int {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	dst := C.GoString(_dst)
	ctx := w.withPid(pid)
	df, err := w.Open(ctx, dst, vfs.MODE_MASK_W)
	if err != 0 {
		return errno(err)
	}
	defer df.Close(ctx)
	srcs := strings.Split(string(toBuf(buf, bufsize-1)), "\000")
	var tmp string
	if len(srcs) > 1 {
		tmp = filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".merging")
		fi, err := w.Create(ctx, tmp, 0644)
		if err != 0 {
			return errno(err)
		}
		defer func() { _ = w.Delete(ctx, tmp) }()
		defer fi.Close(ctx)
		var off uint64
		for _, src := range srcs {
			copied, err := w.CopyFileRange(ctx, src, 0, tmp, off, 1<<63)
			if err != 0 {
				return errno(err)
			}
			off += copied
		}
	} else {
		tmp = srcs[0]
	}

	dfi, _ := df.Stat()
	_, err = w.CopyFileRange(ctx, tmp, 0, dst, uint64(dfi.Size()), 1<<63)
	return errno(err)
}

//export jfs_lseek
func jfs_lseek(pid, fd int, offset int64, whence int) int64 {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if ok {
		filesLock.Unlock()
		off, _ := f.Seek(f.w.withPid(pid), offset, whence)
		return int64(off)
	}
	filesLock.Unlock()
	return int64(EINVAL)
}

//export jfs_read
func jfs_read(pid, fd int, cbuf uintptr, count int) int {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	n, err := f.Read(f.w.withPid(pid), toBuf(cbuf, count))
	if err != nil && err != io.EOF {
		logger.Errorf("read %s: %s", f.Name(), err)
		return errno(err)
	}
	return int(n)
}

//export jfs_pread
func jfs_pread(pid, fd int, cbuf uintptr, count C.size_t, offset C.off_t) int {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	if count > (1 << 30) {
		count = 1 << 30
	}
	n, err := f.Pread(f.w.withPid(pid), toBuf(cbuf, int(count)), int64(offset))
	if err != nil && err != io.EOF {
		logger.Errorf("read %s: %s", f.Name(), err)
		return errno(err)
	}
	return int(n)
}

//export jfs_write
func jfs_write(pid, fd int, cbuf uintptr, count C.size_t) int {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	buf := toBuf(uintptr(cbuf), int(count))
	n, err := f.Write(f.w.withPid(pid), buf)
	if err != 0 {
		logger.Errorf("write %s: %s", f.Name(), err)
		return errno(err)
	}
	return int(n)
}

//export jfs_flush
func jfs_flush(pid, fd int) int {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	return errno(f.Flush(f.w.withPid(pid)))
}

//export jfs_fsync
func jfs_fsync(pid, fd int) int {
	filesLock.Lock()
	f, ok := openFiles[int(fd)]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	return errno(f.Fsync(f.w.withPid(pid)))
}

//export jfs_close
func jfs_close(pid, fd int) int {
	filesLock.Lock()
	f, ok := openFiles[fd]
	filesLock.Unlock()
	if !ok {
		return 0
	}
	freeHandle(fd)
	return errno(f.Close(f.w.withPid(pid)))
}

func main() {
}
