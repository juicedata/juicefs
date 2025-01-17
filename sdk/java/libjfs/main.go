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

package main

// #cgo linux LDFLAGS: -ldl
// #cgo linux CFLAGS: -Wno-discarded-qualifiers -D_GNU_SOURCE
// #include <unistd.h>
// #include <inttypes.h>
// #include <sys/types.h>
// #include <sys/stat.h>
// #include <fcntl.h>
// #include <utime.h>
// #include <stdlib.h>
// void jfs_callback(const char *msg);
/*
#include <inttypes.h>

typedef struct {
	uint64_t inode;
	uint32_t mode;
	uint32_t uid;
	uint32_t gid;
	uint32_t atime;
	uint32_t mtime;
	uint32_t ctime;
	uint32_t nlink;
	uint64_t length;
} fileInfo;
*/
import "C"
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/juicedata/juicefs/cmd"
	"github.com/juicedata/juicefs/pkg/acl"
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
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
)

var (
	filesLock  sync.Mutex
	openFiles  = make(map[int32]*fwrapper)
	nextHandle = int32(1)

	fslock       sync.Mutex
	handlers           = make(map[int64]*wrapper)
	nextFsHandle int64 = 0
	activefs           = make(map[string][]*wrapper)
	logger             = utils.GetLogger("juicefs")
	bOnce        sync.Once
	bridges      []*Bridge
	pOnce        sync.Once
	pushers      []*push.Pusher

	userGroupCache = make(map[string]map[string][]string) // name -> (user -> groups)

	MaxDeletes = meta.RmrDefaultThreads
	caller     = CALLER_JAVA
)

const (
	CALLER_JAVA = iota
	CALLER_PYTHON
)

const (
	BEHAVIOR_HADOOP = "Hadoop"
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
	EDQUOT    = -0x45
	EROFS     = -0x1e
	ENOTEMPTY = -0x27
	ENODATA   = -0x3d
	ENOTSUP   = -0x5f
)

func errno(err error) int32 {
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
	case syscall.EDQUOT:
		return EDQUOT
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
		return -int32(eno)
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

type logWriter struct {
	buf chan string
}

func (w *logWriter) Write(p []byte) (int, error) {
	select {
	case w.buf <- string(p):
		_, _ = os.Stderr.Write(p)
		return len(p), nil
	default:
		return os.Stderr.Write(p)
	}
}

func newLogWriter() *logWriter {
	w := &logWriter{
		buf: make(chan string, 10),
	}
	go func() {
		for l := range w.buf {
			cmsg := C.CString(l)
			C.jfs_callback(cmsg)
			C.free(unsafe.Pointer(cmsg))
		}
	}()
	return w
}

//export jfs_set_logger
func jfs_set_logger(cb unsafe.Pointer) {
	utils.DisableLogColor()
	if cb != nil {
		utils.SetOutput(newLogWriter())
	} else {
		utils.SetOutput(os.Stderr)
	}
}

func (w *wrapper) withPid(pid int64) meta.Context {
	// mapping Java Thread ID to global one
	ctx := meta.NewContext(w.ctx.Pid()*1000+uint32(pid), w.ctx.Uid(), w.ctx.Gids())
	if caller == CALLER_JAVA {
		ctx.WithValue(meta.CtxKey("behavior"), BEHAVIOR_HADOOP)
	}
	return ctx
}

func (w *wrapper) isSuperuser(name string, groups []string) bool {
	if name == w.superuser {
		return true
	}
	for _, g := range groups {
		if g == w.supergroup {
			return true
		}
	}
	return false
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
		name = w.m.lookupUserID(uid)
	}
	return name
}

func (w *wrapper) gid2name(gid uint32) string {
	group := w.supergroup
	if gid > 0 {
		group = w.m.lookupGroupID(gid)
	}
	return group
}

type fwrapper struct {
	*fs.File
	w *wrapper
}

func nextFileHandle(f *fs.File, w *wrapper) int32 {
	filesLock.Lock()
	defer filesLock.Unlock()
	for i := nextHandle; ; i++ {
		if _, ok := openFiles[i]; !ok {
			openFiles[i] = &fwrapper{f, w}
			nextHandle = i + 1
			return i
		}
	}
}

func freeHandle(fd int32) {
	filesLock.Lock()
	defer filesLock.Unlock()
	f := openFiles[fd]
	if f != nil {
		delete(openFiles, fd)
	}
}

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
	CacheItems        int64  `json:"cacheItems"`
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
	PushGraphite      string `json:"pushGraphite"`
	Caller            int    `json:"caller"`
}

func getOrCreate(name, user, group, superuser, supergroup string, f func() *fs.FileSystem) int64 {
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
		switch jfs.Meta().Name() {
		case "mysql", "postgres", "sqlite3":
			m.mask = 0x7FFFFFFF // limit generated uid to int32
		}
		logger.Infof("JuiceFileSystem created for user:%s group:%s", user, group)
	}
	w := &wrapper{jfs, nil, m, user, superuser, supergroup}
	var gs []string
	if userGroupCache[name] != nil {
		gs = userGroupCache[name][user]
	}
	if gs == nil {
		gs = strings.Split(group, ",")
	}
	group = strings.Join(gs, ",")
	logger.Debugf("update groups of %s to %s", user, group)
	if w.isSuperuser(user, gs) {
		w.ctx = meta.NewContext(uint32(os.Getpid()), 0, []uint32{0})
	} else {
		w.ctx = meta.NewContext(uint32(os.Getpid()), w.lookupUid(user), w.lookupGids(group))
	}
	activefs[name] = append(ws, w)
	nextFsHandle = nextFsHandle + 1
	handlers[nextFsHandle] = w
	return nextFsHandle
}

func push2Gateway(pushGatewayAddr, pushAuth string, pushInterVal time.Duration, registry *prometheus.Registry, commonLabels map[string]string) {
	pusher := push.New(pushGatewayAddr, "juicefs").Gatherer(registry)
	for k, v := range commonLabels {
		pusher.Grouping(k, v)
	}
	if pushAuth != "" {
		if strings.Contains(pushAuth, ":") {
			parts := strings.Split(pushAuth, ":")
			pusher.BasicAuth(parts[0], parts[1])
		}
	}
	pusher.Client(&http.Client{Timeout: 2 * time.Second})
	pushers = append(pushers, pusher)

	pOnce.Do(func() {
		go func() {
			for range time.NewTicker(pushInterVal).C {
				for _, pusher := range pushers {
					if err := pusher.Push(); err != nil {
						logger.Warnf("error pushing to PushGateway: %s", err)
					}
				}
			}
		}()
	})
}

func push2Graphite(graphite string, pushInterVal time.Duration, registry *prometheus.Registry, commonLabels map[string]string) {
	if bridge, err := NewBridge(&Config{
		URL:           graphite,
		Gatherer:      registry,
		UseTags:       true,
		Timeout:       2 * time.Second,
		ErrorHandling: ContinueOnError,
		Logger:        logger,
		CommonLabels:  commonLabels,
	}); err != nil {
		logger.Warnf("NewBridge error:%s", err)
	} else {
		bridges = append(bridges, bridge)
	}

	bOnce.Do(func() {
		go func() {
			for range time.NewTicker(pushInterVal).C {
				for _, brg := range bridges {
					if err := brg.Push(); err != nil {
						logger.Warnf("error pushing to Graphite: %s", err)
					}
				}
			}
		}()
	})
}

//export jfs_init
func jfs_init(cname, jsonConf, user, group, superuser, supergroup *C.char) int64 {
	name := C.GoString(cname)
	debug.SetGCPercent(50)
	object.UserAgent = "JuiceFS-SDK " + version.Version()
	return getOrCreate(name, C.GoString(user), C.GoString(group), C.GoString(superuser), C.GoString(supergroup), func() *fs.FileSystem {
		var jConf javaConf
		err := json.Unmarshal([]byte(C.GoString(jsonConf)), &jConf)
		if err != nil {
			logger.Errorf("invalid json: %s", C.GoString(jsonConf))
			return nil
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

		caller = jConf.Caller
		if jConf.MaxDeletes > 0 {
			MaxDeletes = jConf.MaxDeletes
		}

		metaConf := meta.DefaultConf()
		metaConf.Retries = jConf.IORetries
		metaConf.MaxDeletes = jConf.MaxDeletes
		metaConf.SkipDirNlink = jConf.SkipDirNlink
		metaConf.SkipDirMtime = utils.Duration(jConf.SkipDirMtime)
		metaConf.ReadOnly = jConf.ReadOnly
		metaConf.NoBGJob = jConf.NoBGJob || jConf.NoSession
		metaConf.OpenCache = utils.Duration(jConf.OpenCache)
		metaConf.Heartbeat = utils.Duration(jConf.Heartbeat)
		m := meta.NewClient(jConf.MetaURL, metaConf)
		format, err := m.Load(true)
		if err != nil {
			logger.Errorf("load setting: %s", err)
			return nil
		}
		var registerer prometheus.Registerer
		if jConf.PushGateway != "" || jConf.PushGraphite != "" {
			commonLabels := prometheus.Labels{"vol_name": name, "mp": "sdk-" + strconv.Itoa(os.Getpid())}
			if h, err := os.Hostname(); err == nil {
				commonLabels["instance"] = h
			} else {
				logger.Warnf("cannot get hostname: %s", err)
			}
			if jConf.PushLabels != "" {
				for _, kv := range strings.Split(jConf.PushLabels, ";") {
					var splited = strings.Split(kv, ":")
					if len(splited) != 2 {
						logger.Errorf("invalid label format: %s", kv)
						return nil
					}
					if utils.StringContains([]string{"mp", "vol_name", "instance"}, splited[0]) {
						logger.Warnf("overriding reserved label: %s", splited[0])
					}
					commonLabels[splited[0]] = splited[1]
				}
			}
			registry := prometheus.NewRegistry()
			registerer = prometheus.WrapRegistererWithPrefix("juicefs_", registry)
			registerer.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
			registerer.MustRegister(collectors.NewGoCollector())

			var interval = utils.Duration(jConf.PushInterval)
			if jConf.PushGraphite != "" {
				push2Graphite(jConf.PushGraphite, interval, registry, commonLabels)
			}
			if jConf.PushGateway != "" {
				push2Gateway(jConf.PushGateway, jConf.PushAuth, interval, registry, commonLabels)
			}
			m.InitMetrics(registerer)
			vfs.InitMetrics(registerer)
			go metric.UpdateMetrics(registerer)
		}

		blob, err := cmd.NewReloadableStorage(format, m, func(f *meta.Format) {
			if jConf.Bucket != "" {
				format.Bucket = jConf.Bucket
			}
			if jConf.StorageClass != "" {
				format.StorageClass = jConf.StorageClass
			}
		})
		if err != nil {
			logger.Errorf("object storage: %s", err)
			return nil
		}
		logger.Infof("Data use %s", blob)

		var freeSpaceRatio = 0.1
		if jConf.FreeSpace != "" {
			freeSpaceRatio, _ = strconv.ParseFloat(jConf.FreeSpace, 64)
		}
		chunkConf := chunk.Config{
			BlockSize:         format.BlockSize * 1024,
			Compress:          format.Compression,
			CacheDir:          jConf.CacheDir,
			CacheMode:         0644, // all user can read cache
			CacheSize:         utils.ParseBytesStr("cache-size", jConf.CacheSize, 'M'),
			CacheItems:        jConf.CacheItems,
			FreeSpace:         float32(freeSpaceRatio),
			AutoCreate:        jConf.AutoCreate,
			CacheFullBlock:    jConf.CacheFullBlock,
			CacheChecksum:     jConf.CacheChecksum,
			CacheEviction:     jConf.CacheEviction,
			CacheScanInterval: utils.Duration(jConf.CacheScanInterval),
			CacheExpire:       utils.Duration(jConf.CacheExpire),
			OSCache:           true,
			MaxUpload:         jConf.MaxUploads,
			MaxRetries:        jConf.IORetries,
			UploadLimit:       utils.ParseMbpsStr("upload-limit", jConf.UploadLimit) * 1e6 / 8,
			DownloadLimit:     utils.ParseMbpsStr("download-limit", jConf.DownloadLimit) * 1e6 / 8,
			Prefetch:          jConf.Prefetch,
			Writeback:         jConf.Writeback,
			HashPrefix:        format.HashPrefix,
			GetTimeout:        utils.Duration(jConf.GetTimeout),
			PutTimeout:        utils.Duration(jConf.PutTimeout),
			BufferSize:        utils.ParseBytesStr("memory-size", jConf.MemorySize, 'M'),
			Readahead:         int(utils.ParseBytesStr("max-readahead", jConf.Readahead, 'M')),
		}
		if chunkConf.UploadLimit == 0 {
			chunkConf.UploadLimit = format.UploadLimit * 1e6 / 8
		}
		if chunkConf.DownloadLimit == 0 {
			chunkConf.DownloadLimit = format.DownloadLimit * 1e6 / 8
		}
		chunkConf.SelfCheck(format.UUID)
		store := chunk.NewCachedStore(blob, chunkConf, registerer)
		m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
			id := args[0].(uint64)
			length := args[1].(uint32)
			return store.Remove(id, int(length))
		})
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			id := args[1].(uint64)
			return vfs.Compact(chunkConf, store, slices, id)
		})
		err = m.NewSession(!jConf.NoSession)
		if err != nil {
			logger.Errorf("new session: %s", err)
			return nil
		}
		m.OnReload(func(fmt *meta.Format) {
			if chunkConf.UploadLimit > 0 {
				fmt.UploadLimit = chunkConf.UploadLimit
			}
			if chunkConf.DownloadLimit > 0 {
				fmt.DownloadLimit = chunkConf.DownloadLimit
			}
			store.UpdateLimit(fmt.UploadLimit, fmt.DownloadLimit)
		})

		conf := &vfs.Config{
			Meta:            metaConf,
			Format:          *format,
			Chunk:           &chunkConf,
			AttrTimeout:     utils.Duration(jConf.AttrTimeout),
			EntryTimeout:    utils.Duration(jConf.EntryTimeout),
			DirEntryTimeout: utils.Duration(jConf.DirEntryTimeout),
			AccessLog:       jConf.AccessLog,
			FastResolve:     jConf.FastResolve,
			BackupMeta:      utils.Duration(jConf.BackupMeta),
			BackupSkipTrash: jConf.BackupSkipTrash,
		}
		if !jConf.ReadOnly && !jConf.NoSession && !jConf.NoBGJob && conf.BackupMeta > 0 {
			go vfs.Backup(m, blob, conf.BackupMeta, conf.BackupSkipTrash)
		}
		if !jConf.NoUsageReport && !jConf.NoSession {
			go usage.ReportUsage(m, "java-sdk "+version.Version())
		}
		jfs, err := fs.NewFileSystem(conf, m, store)
		if err != nil {
			logger.Errorf("Initialize failed: %s", err)
			return nil
		}
		jfs.InitMetrics(registerer)
		return jfs
	})
}

func F(p int64) *wrapper {
	fslock.Lock()
	defer fslock.Unlock()
	return handlers[p]
}

//export jfs_update_uid_grouping
func jfs_update_uid_grouping(cname, uidstr *C.char, grouping *C.char) {
	name := C.GoString(cname)
	var uids []pwent
	if uidstr != nil {
		for _, line := range strings.Split(C.GoString(uidstr), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 2 {
				continue
			}
			username := strings.TrimSpace(fields[0])
			uid, _ := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 32)
			uids = append(uids, pwent{uint32(uid), username})
		}

		var buffer bytes.Buffer
		for _, u := range uids {
			buffer.WriteString(fmt.Sprintf("\t%v:%v\n", u.name, u.id))
		}
		logger.Debugf("Update uids mapping\n %s", buffer.String())
	}

	var userGroups = make(map[string][]string) // user -> groups

	var gids []pwent
	if grouping != nil {
		for _, line := range strings.Split(C.GoString(grouping), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 2 {
				continue
			}
			gname := strings.TrimSpace(fields[0])
			gid, _ := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 32)
			gids = append(gids, pwent{uint32(gid), gname})
			if len(fields) > 2 {
				for _, user := range strings.Split(fields[len(fields)-1], ",") {
					userGroups[user] = append(userGroups[user], gname)
				}
			}
		}
		var buffer bytes.Buffer
		for _, g := range gids {
			buffer.WriteString(fmt.Sprintf("\t%v:%v\n", g.name, g.id))
		}
		logger.Debugf("Update gids mapping\n %s", buffer.String())
	}

	fslock.Lock()
	defer fslock.Unlock()
	userGroupCache[name] = userGroups
	ws := activefs[name]
	if len(ws) > 0 {
		m := ws[0].m
		m.update(uids, gids, false)
		for _, w := range ws {
			logger.Debugf("Update groups of %s to %s", w.user, strings.Join(userGroups[w.user], ","))
			if w.isSuperuser(w.user, userGroups[w.user]) {
				w.ctx = meta.NewContext(uint32(os.Getpid()), 0, []uint32{0})
			} else {
				w.ctx = meta.NewContext(uint32(os.Getpid()), w.lookupUid(w.user), w.lookupGids(strings.Join(userGroups[w.user], ",")))
			}
		}
	}
}

//export jfs_getGroups
func jfs_getGroups(name, user string) string {
	fslock.Lock()
	defer fslock.Unlock()
	userGroups := userGroupCache[name]
	if userGroups != nil {
		gs := userGroups[user]
		if gs != nil {
			return strings.Join(gs, ",")
		}
	}
	return ""
}

//export jfs_term
func jfs_term(pid int64, h int64) int32 {
	w := F(h)
	if w == nil {
		return 0
	}
	ctx := w.withPid(pid)
	// sync all open files
	filesLock.Lock()
	var m sync.WaitGroup
	var toClose []int32
	for fd, f := range openFiles {
		if f.w == w {
			m.Add(1)
			go func(f *fs.File) {
				defer m.Done()
				_ = f.Close(ctx)
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
					_ = w.Flush()
					// don't close the filesystem, so it can be re-used later
					// w.Close()
					// delete(activefs, name)
				}
			}
		}
	}
	for _, bridge := range bridges {
		if err := bridge.Push(); err != nil {
			logger.Warnf("error pushing to Graphite: %s", err)
		}
	}
	for _, pusher := range pushers {
		if err := pusher.Push(); err != nil {
			logger.Warnf("error pushing to PushGatway: %s", err)
		}
	}
	return 0
}

//export jfs_open
func jfs_open(pid int64, h int64, cpath *C.char, lenPtr uintptr, flags int32) int32 {
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
	if lenPtr != 0 {
		buf := toBuf(lenPtr, 8)
		wb := utils.NewNativeBuffer(buf)
		wb.Put64(uint64(st.Size()))
	}
	return nextFileHandle(f, w)
}

//export jfs_access
func jfs_access(pid int64, h int64, cpath *C.char, flags int64) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Access(w.withPid(pid), C.GoString(cpath), int(flags)))
}

//export jfs_create
func jfs_create(pid int64, h int64, cpath *C.char, mode uint16, umask uint16) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	path := C.GoString(cpath)
	f, err := w.Create(w.withPid(pid), path, mode, umask)
	if err != 0 {
		return errno(err)
	}
	if w.ctx.Uid() == 0 && w.user != w.superuser {
		// belongs to supergroup
		_ = setOwner(w, w.withPid(pid), C.GoString(cpath), w.user, "")
	}
	return nextFileHandle(f, w)
}

//export jfs_mkdir
func jfs_mkdir(pid int64, h int64, cpath *C.char, mode uint16, umask uint16) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	err := errno(w.Mkdir(w.withPid(pid), C.GoString(cpath), mode, umask))
	if err == 0 && w.ctx.Uid() == 0 && w.user != w.superuser {
		// belongs to supergroup
		_ = setOwner(w, w.withPid(pid), C.GoString(cpath), w.user, "")
	}
	return err
}

//export jfs_mkdirAll
func jfs_mkdirAll(pid int64, h int64, cpath *C.char, mode, umask uint16, existOK bool) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	path := C.GoString(cpath)
	err := errno(w.MkdirAll0(w.withPid(pid), path, mode, umask, existOK))
	if err == 0 && w.ctx.Uid() == 0 && w.user != w.superuser {
		// belongs to supergroup
		if err := setOwner(w, w.withPid(pid), path, w.user, ""); err != 0 {
			logger.Errorf("change owner of %s to %s: %d", path, w.user, err)
		}
	}
	return err
}

//export jfs_delete
func jfs_delete(pid int64, h int64, cpath *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Delete(w.withPid(pid), C.GoString(cpath)))
}

//export jfs_rmr
func jfs_rmr(pid int64, h int64, cpath *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Rmr(w.withPid(pid), C.GoString(cpath), MaxDeletes))
}

//export jfs_rename
func jfs_rename(pid int64, h int64, oldpath *C.char, newpath *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Rename(w.withPid(pid), C.GoString(oldpath), C.GoString(newpath), meta.RenameNoReplace))
}

//export jfs_truncate
func jfs_truncate(pid int64, h int64, path *C.char, length uint64) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Truncate(w.withPid(pid), C.GoString(path), length))
}

//export jfs_setXattr
func jfs_setXattr(pid int64, h int64, path *C.char, name *C.char, value uintptr, vlen, mode int32) int32 {
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

//export jfs_setXattr2
func jfs_setXattr2(pid int64, h int64, path *C.char, name *C.char, value *C.char, mode int64) int32 {
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
	return errno(w.SetXattr(w.withPid(pid), C.GoString(path), C.GoString(name), []byte(C.GoString(value)), flags))
}

//export jfs_getXattr
func jfs_getXattr(pid int64, h int64, path *C.char, name *C.char, buf uintptr, bufsize int32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	buff, err := w.GetXattr(w.withPid(pid), C.GoString(path), C.GoString(name))
	if err != 0 {
		return errno(err)
	}
	if int32(len(buff)) >= bufsize {
		return bufsize
	}
	copy(toBuf(buf, bufsize), buff)
	return int32(len(buff))
}

//export jfs_getXattr2
func jfs_getXattr2(pid int64, h int64, path *C.char, name *C.char, value **C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	t, err := w.GetXattr(w.withPid(pid), C.GoString(path), C.GoString(name))
	if err == 0 {
		*value = C.CString(string(t))
	}
	return errno(err)
}

//export jfs_listXattr
func jfs_listXattr(pid int64, h int64, path *C.char, buf uintptr, bufsize int32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	buff, err := w.ListXattr(w.withPid(pid), C.GoString(path))
	if err != 0 {
		return errno(err)
	}
	if int32(len(buff)) >= bufsize {
		return bufsize
	}
	copy(toBuf(buf, bufsize), buff)
	return int32(len(buff))
}

//export jfs_listXattr2
func jfs_listXattr2(pid int64, h int64, path *C.char, value **C.char, size *int) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	t, err := w.ListXattr(w.withPid(pid), C.GoString(path))
	if err == 0 {
		*value = C.CString(string(t))
		*size = len(t)
	}
	return errno(err)
}

//export jfs_removeXattr
func jfs_removeXattr(pid int64, h int64, path *C.char, name *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.RemoveXattr(w.withPid(pid), C.GoString(path), C.GoString(name)))
}

//export jfs_getfacl
func jfs_getfacl(pid int64, h int64, path *C.char, acltype int32, buf uintptr, blen int32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	rule := acl.EmptyRule()
	err := w.GetFacl(w.withPid(pid), C.GoString(path), uint8(acltype), rule)
	if err != 0 {
		return errno(err)
	}
	wb := utils.NewNativeBuffer(toBuf(buf, blen))
	wb.Put16(rule.Owner)
	wb.Put16(rule.Group)
	wb.Put16(rule.Other)
	wb.Put16(rule.Mask)
	wb.Put16(uint16(len(rule.NamedUsers)))
	wb.Put16(uint16(len(rule.NamedGroups)))
	var off uintptr = 12
	for i, entry := range append(rule.NamedUsers, rule.NamedGroups...) {
		var name string
		if i < len(rule.NamedUsers) {
			name = w.uid2name(entry.Id)
		} else {
			name = w.gid2name(entry.Id)
		}
		if wb.Left() < len(name)+1+2 {
			return -100
		}
		wb.Put([]byte(name))
		wb.Put8(0)
		wb.Put16(entry.Perm)
	}
	return int32(off)
}

//export jfs_setfacl
func jfs_setfacl(pid int64, h int64, path *C.char, acltype int32, buf uintptr, alen int32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	rule := acl.EmptyRule()
	r := utils.NewNativeBuffer(toBuf(buf, alen))
	rule.Owner = r.Get16()
	rule.Group = r.Get16()
	rule.Other = r.Get16()
	rule.Mask = r.Get16()
	namedusers := r.Get16()
	namedgroups := r.Get16()
	for i := uint16(0); i < namedusers+namedgroups; i++ {
		name := string(r.Get(int(r.Get8())))
		var entry acl.Entry
		entry.Perm = uint16(r.Get8())
		if i < namedusers {
			entry.Id = w.lookupUid(name)
			rule.NamedUsers = append(rule.NamedUsers, entry)
		} else {
			entry.Id = w.lookupGid(name)
			rule.NamedGroups = append(rule.NamedGroups, entry)
		}
	}
	return errno(w.SetFacl(w.withPid(pid), C.GoString(path), uint8(acltype), rule))
}

//export jfs_link
func jfs_link(pid int64, h int64, src *C.char, dst *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return errno(w.Link(w.withPid(pid), C.GoString(src), C.GoString(dst)))
}

//export jfs_symlink
func jfs_symlink(pid int64, h int64, target_ *C.char, link_ *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	target := C.GoString(target_)
	link := C.GoString(link_)
	dir := path.Dir(strings.TrimRight(link, "/"))
	rel, e := filepath.Rel(dir, target)
	if e != nil {
		// external link
		rel = target
	}
	return errno(w.Symlink(w.withPid(pid), rel, link))
}

//export jfs_readlink
func jfs_readlink(pid int64, h int64, link *C.char, buf uintptr, bufsize int32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	target, err := w.Readlink(w.withPid(pid), C.GoString(link))
	if err != 0 {
		return errno(err)
	}
	if int32(len(target)+1) >= bufsize {
		target = target[:bufsize-1]
	}
	wb := utils.NewNativeBuffer(toBuf(buf, bufsize))
	wb.Put(target)
	wb.Put8(0)
	return int32(len(target))
}

// mode:4 length:8 mtime:8 atime:8 user:50 group:50
func fill_stat(w *wrapper, wb *utils.Buffer, st *fs.FileStat) int32 {
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
	return 30 + int32(len(user)) + int32(len(group))
}

//export jfs_stat1
func jfs_stat1(pid int64, h int64, cpath *C.char, buf uintptr) int32 {
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
func jfs_lstat1(pid int64, h int64, cpath *C.char, buf uintptr) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	fi, err := w.Lstat(w.withPid(pid), C.GoString(cpath))
	if err != 0 {
		return errno(err)
	}
	return fill_stat(w, utils.NewNativeBuffer(toBuf(buf, 130)), fi)
}

func attrToInfo(fi *fs.FileStat, info *C.fileInfo) {
	attr := fi.Sys().(*meta.Attr)
	info.mode = C.uint32_t(attr.SMode())
	info.uid = C.uint32_t(attr.Uid)
	info.gid = C.uint32_t(attr.Gid)
	info.atime = C.uint32_t(attr.Atime)
	info.mtime = C.uint32_t(attr.Mtime)
	info.ctime = C.uint32_t(attr.Ctime)
	info.nlink = C.uint32_t(attr.Nlink)
	info.length = C.uint64_t(attr.Length)
}

//export jfs_stat
func jfs_stat(pid int64, h int64, cpath *C.char, info *C.fileInfo) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	fi, err := w.Stat(w.withPid(pid), C.GoString(cpath))
	if err != 0 {
		return errno(err)
	}
	info.inode = C.uint64_t(fi.Inode())
	attrToInfo(fi, info)
	return 0
}

//export jfs_lstat
func jfs_lstat(pid int64, h int64, cpath *C.char, info *C.fileInfo) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	fi, err := w.Lstat(w.withPid(pid), C.GoString(cpath))
	if err != 0 {
		return errno(err)
	}
	info.inode = C.uint64_t(fi.Inode())
	attrToInfo(fi, info)
	return 0
}

//export jfs_summary
func jfs_summary(pid int64, h int64, cpath *C.char, buf uintptr) int32 {
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
	summary, err := f.Summary(ctx)
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
func jfs_statvfs(pid int64, h int64, buf uintptr) int32 {
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
func jfs_chmod(pid int64, h int64, cpath *C.char, mode C.mode_t) int32 {
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
func jfs_chown(pid int64, h int64, cpath *C.char, uid uint32, gid uint32) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	f, err := w.Open(w.withPid(pid), C.GoString(cpath), 0)
	if err != 0 {
		return errno(err)
	}
	return errno(f.Chown(w.withPid(pid), uid, gid))
}

//export jfs_utime
func jfs_utime(pid int64, h int64, cpath *C.char, mtime, atime int64) int32 {
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
func jfs_setOwner(pid int64, h int64, cpath *C.char, owner *C.char, group *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	return setOwner(w, w.withPid(pid), C.GoString(cpath), C.GoString(owner), C.GoString(group))
}

func setOwner(w *wrapper, ctx meta.Context, path string, owner, group string) int32 {
	f, err := w.Open(ctx, path, 0)
	if err != 0 {
		return errno(err)
	}
	defer f.Close(ctx)
	st, _ := f.Stat()
	uid := uint32(st.(*fs.FileStat).Uid())
	gid := uint32(st.(*fs.FileStat).Gid())
	if owner != "" {
		uid = w.lookupUid(owner)
	}
	if group != "" {
		gid = w.lookupGid(group)
	}
	return errno(f.Chown(ctx, uid, gid))
}

//export jfs_listdir
func jfs_listdir(pid int64, h int64, cpath *C.char, offset int64, buf uintptr, bufsize int32) int32 {
	var ctx meta.Context
	var f *fs.File
	var w *wrapper
	if offset > 0 {
		filesLock.Lock()
		fw := openFiles[int32(h)]
		filesLock.Unlock()
		if fw == nil {
			return EINVAL
		}
		freeHandle(int32(h))
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

	es, err := f.ReaddirPlus(ctx, int(offset))
	if err != 0 {
		return errno(err)
	}

	wb := utils.NewNativeBuffer(toBuf(buf, bufsize))
	for i, d := range es {
		if wb.Left() < 1+len(d.Name)+1+130+8 {
			wb.Put32(uint32(len(es) - i))
			wb.Put32(uint32(nextFileHandle(f, w)))
			return bufsize - int32(wb.Left()) - 8
		}
		wb.Put8(byte(len(d.Name)))
		wb.Put(d.Name)
		header := wb.Get(1)
		header[0] = uint8(fill_stat(w, wb, fs.AttrToFileInfo(d.Inode, d.Attr)))
	}
	wb.Put32(0)
	return bufsize - int32(wb.Left()) - 4
}

//export jfs_listdir2
func jfs_listdir2(pid int64, h int64, cpath *C.char, plus bool, buf **byte, size *int64) int32 {
	var ctx meta.Context
	var f *fs.File
	w := F(h)
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

	*size = 0
	if plus {
		es, err := f.ReaddirPlus(ctx, 0)
		if err != 0 {
			return errno(err)
		}
		for _, e := range es {
			*size += 2 + int64(len(e.Name)) + 4*11
		}
		*buf = (*byte)(C.malloc(C.size_t(*size)))
		out := utils.FromBuffer(unsafe.Slice(*buf, *size))
		for _, e := range es {
			out.Put16(uint16(len(e.Name)))
			out.Put([]byte(e.Name))
			out.Put32(e.Attr.SMode())
			out.Put64(uint64(e.Inode))
			out.Put32(e.Attr.Nlink)
			out.Put32(e.Attr.Uid)
			out.Put32(e.Attr.Gid)
			out.Put64(e.Attr.Length)
			out.Put32(uint32(e.Attr.Atime))
			out.Put32(uint32(e.Attr.Mtime))
			out.Put32(uint32(e.Attr.Ctime))
		}
	} else {
		es, err := f.Readdir(ctx, 0)
		if err != 0 {
			return errno(err)
		}
		for _, e := range es {
			*size += 2 + int64(len(e.Name()))
		}
		*buf = (*byte)(C.malloc(C.size_t(*size)))
		out := utils.FromBuffer(unsafe.Slice(*buf, *size))
		for _, e := range es {
			out.Put16(uint16(len(e.Name())))
			out.Put([]byte(e.Name()))
		}
	}
	return 0
}

func toBuf(s uintptr, sz int32) []byte {
	return (*[1 << 30]byte)(unsafe.Pointer(s))[:sz:sz]
}

//export jfs_concat
func jfs_concat(pid int64, h int64, _dst *C.char, buf uintptr, bufsize int32) int32 {
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
		fi, err := w.Create(ctx, tmp, 0666, 022)
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
	r := errno(err)
	if r == 0 {
		var wg sync.WaitGroup
		var limit = make(chan bool, 100)
		for _, src := range srcs {
			limit <- true
			wg.Add(1)
			go func(p string) {
				defer func() { <-limit }()
				defer wg.Done()
				if r := w.Delete(ctx, p); r != 0 {
					logger.Errorf("delete source %s: %s", p, r)
				}
			}(src)
		}
		wg.Wait()
	}
	return r
}

// TODO: implement real clone

//export jfs_clone
func jfs_clone(pid int64, h int64, _src *C.char, _dst *C.char) int32 {
	w := F(h)
	if w == nil {
		return EINVAL
	}
	src := C.GoString(_src)
	dst := C.GoString(_dst)
	ctx := w.withPid(pid)
	fi, err := w.Open(ctx, src, 0)
	if err != 0 {
		logger.Errorf("open %s: %s", src, err)
		return errno(err)
	}
	defer fi.Close(ctx)
	fo, err := w.Create(ctx, dst, 0666, 022)
	if err != 0 {
		logger.Errorf("create %s: %s", dst, err)
		return errno(err)
	}
	defer fo.Close(ctx)
	_, err = w.CopyFileRange(ctx, src, 0, dst, 0, 1<<63)
	if err != 0 {
		logger.Errorf("copy %s to %s: %s", src, dst, err)
	}
	return errno(err)
}

//export jfs_lseek
func jfs_lseek(pid int64, fd int32, offset int64, whence int64) int64 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if ok {
		filesLock.Unlock()
		off, _ := f.Seek(f.w.withPid(pid), offset, int(whence))
		return off
	}
	filesLock.Unlock()
	return int64(EINVAL)
}

//export jfs_read
func jfs_read(pid int64, fd int32, cbuf uintptr, count int32) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	n, err := f.Read(f.w.withPid(pid), toBuf(cbuf, int32(count)))
	if err != nil && err != io.EOF {
		logger.Errorf("read %s: %s", f.Name(), err)
		return errno(err)
	}
	return int32(n)
}

//export jfs_pread
func jfs_pread(pid int64, fd int32, cbuf uintptr, count int32, offset int64) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	if count > (1 << 30) {
		count = 1 << 30
	}
	n, err := f.Pread(f.w.withPid(pid), toBuf(cbuf, count), offset)
	if err != nil && err != io.EOF {
		logger.Errorf("read %s: %s", f.Name(), err)
		return errno(err)
	}
	return int32(n)
}

//export jfs_write
func jfs_write(pid int64, fd int32, cbuf uintptr, count int32) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	buf := toBuf(cbuf, count)
	n, err := f.Write(f.w.withPid(pid), buf)
	if err != 0 {
		logger.Errorf("write %s: %s", f.Name(), err)
		return errno(err)
	}
	return int32(n)
}

//export jfs_pwrite
func jfs_pwrite(pid int64, fd int32, cbuf uintptr, count int32, offset int64) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	buf := toBuf(cbuf, count)
	n, err := f.Pwrite(f.w.withPid(pid), buf, int64(offset))
	if err != 0 {
		logger.Errorf("pwrite %s: %s", f.Name(), err)
		return errno(err)
	}
	return int32(n)
}

//export jfs_ftruncate
func jfs_ftruncate(pid int64, fd int32, size uint64) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	filesLock.Unlock()
	if !ok {
		return EINVAL
	}
	return errno(f.Truncate(f.w.withPid(pid), size))
}

//export jfs_flush
func jfs_flush(pid int64, fd int32) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	return errno(f.Flush(f.w.withPid(pid)))
}

//export jfs_fsync
func jfs_fsync(pid int64, fd int32) int32 {
	filesLock.Lock()
	f, ok := openFiles[fd]
	if !ok {
		filesLock.Unlock()
		return EINVAL
	}
	filesLock.Unlock()

	return errno(f.Fsync(f.w.withPid(pid)))
}

//export jfs_close
func jfs_close(pid int64, fd int32) int32 {
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
