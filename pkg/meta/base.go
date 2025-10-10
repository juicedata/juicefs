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
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

const (
	inodeBatch     = 1 << 10
	sliceIdBatch   = 4 << 10
	nlocks         = 1024
	maxSymCacheNum = int32(10000)
	unknownUsage   = -1
)

var (
	DirBatchNum = map[string]int{
		"redis": 4096,
		"kv":    4096,
		"db":    40960,
	}
	maxCompactSlices  = 1000
	maxSlices         = 2500
	inodeNeedPrefetch = uint64(utils.JitterIt(inodeBatch * 0.1)) // Add jitter to reduce probability of txn conflicts
)

func checkInodeName(name string) syscall.Errno {
	if len(name) == 0 || strings.ContainsAny(name, "/\x00") {
		return syscall.EINVAL
	}
	return 0
}

type engine interface {
	// Get the value of counter name.
	getCounter(name string) (int64, error)
	// Increase counter name by value. Do not use this if value is 0, use getCounter instead.
	incrCounter(name string, value int64) (int64, error)
	// Set counter name to value if old <= value - diff.
	setIfSmall(name string, value, diff int64) (bool, error)
	updateStats(space int64, inodes int64)
	doFlushStats()

	doLoad() ([]byte, error)

	doNewSession(sinfo []byte, update bool) error
	doRefreshSession() error
	doFindStaleSessions(limit int) ([]uint64, error) // limit < 0 means all
	doCleanStaleSession(sid uint64) error
	doInit(format *Format, force bool) error

	scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error
	doDeleteSustainedInode(sid uint64, inode Ino) error
	doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) // limit < 0 means all
	doDeleteFileData(inode Ino, length uint64)
	doCleanupSlices(ctx Context)
	doCleanupDelayedSlices(ctx Context, edge int64) (int, error)
	doDeleteSlice(id uint64, size uint32) error

	doCloneEntry(ctx Context, srcIno Ino, parent Ino, name string, ino Ino, attr *Attr, cmode uint8, cumask uint16, top bool) syscall.Errno
	doAttachDirNode(ctx Context, parent Ino, dstIno Ino, name string) syscall.Errno
	doFindDetachedNodes(t time.Time) []Ino
	doCleanupDetachedNode(ctx Context, detachedNode Ino) syscall.Errno

	doGetQuota(ctx Context, qtype uint32, key uint64) (*Quota, error)
	// set quota, return true if there is no quota exists before
	doSetQuota(ctx Context, qtype uint32, key uint64, quota *Quota) (created bool, err error)
	doDelQuota(ctx Context, qtype uint32, key uint64) error
	doLoadQuotas(ctx Context) (map[uint64]*Quota, map[uint64]*Quota, map[uint64]*Quota, error)
	doFlushQuotas(ctx Context, quotas []*iQuota) error

	doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	doSetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr, oldAttr *Attr) syscall.Errno
	doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, path string, inode *Ino, attr *Attr) syscall.Errno
	doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno
	doUnlink(ctx Context, parent Ino, name string, attr *Attr, skipCheckTrash ...bool) syscall.Errno
	doRmdir(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, skipCheckTrash ...bool) syscall.Errno
	doReadlink(ctx Context, inode Ino, noatime bool) (int64, []byte, error)
	doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno
	doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode, tinode *Ino, attr, tattr *Attr) syscall.Errno
	doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno
	doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno
	doTouchAtime(ctx Context, inode Ino, attr *Attr, ts time.Time) (bool, error)
	doRead(ctx Context, inode Ino, indx uint32) ([]*slice, syscall.Errno)
	doWrite(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time, numSlices *int, delta *dirStat, attr *Attr) syscall.Errno
	doTruncate(ctx Context, inode Ino, flags uint8, length uint64, delta *dirStat, attr *Attr, skipPermCheck bool) syscall.Errno
	doFallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, delta *dirStat, attr *Attr) syscall.Errno
	doCompactChunk(inode Ino, indx uint32, origin []byte, ss []*slice, skipped int, pos uint32, id uint64, size uint32, delayed []byte) syscall.Errno

	doGetParents(ctx Context, inode Ino) map[Ino]int
	doUpdateDirStat(ctx Context, batch map[Ino]dirStat) error
	// @trySync: try sync dir stat if broken or not existed
	doGetDirStat(ctx Context, ino Ino, trySync bool) (*dirStat, syscall.Errno)
	doSyncDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno)
	doSyncVolumeStat(ctx Context) error

	scanTrashSlices(Context, trashSliceScan) error
	scanPendingSlices(Context, pendingSliceScan) error
	scanPendingFiles(Context, pendingFileScan) error

	GetSession(sid uint64, detail bool) (*Session, error)

	doSetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno
	doGetFacl(ctx Context, ino Ino, aclType uint8, aclId uint32, rule *aclAPI.Rule) syscall.Errno
	cacheACLs(ctx Context) error

	newDirHandler(inode Ino, plus bool, entries []*Entry) DirHandler

	dump(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error
	load(ctx Context, typ int, opt *LoadOption, val proto.Message) error
	prepareLoad(ctx Context, opt *LoadOption) error
}

type trashSliceScan func(ss []Slice, ts int64) (clean bool, err error)
type pendingSliceScan func(id uint64, size uint32) (clean bool, err error)
type trashFileScan func(inode Ino, size uint64, ts time.Time) (clean bool, err error)
type pendingFileScan func(ino Ino, size uint64, ts int64) (clean bool, err error)

// fsStat aligned for atomic operations
// nolint:structcheck
type fsStat struct {
	newSpace   int64
	newInodes  int64
	usedSpace  int64
	usedInodes int64
}

// chunk for compaction
type cchunk struct {
	inode  Ino
	indx   uint32
	slices int
}

type symlinkCache struct {
	*sync.Map
	size atomic.Int32
	cap  int32
}

func newSymlinkCache(cap int32) *symlinkCache {
	return &symlinkCache{
		Map: &sync.Map{},
		cap: cap,
	}
}

func (symCache *symlinkCache) Store(inode Ino, path []byte) {
	if _, loaded := symCache.Swap(inode, path); !loaded {
		symCache.size.Add(1)
	}
}

func (symCache *symlinkCache) clean(ctx Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			symCache.doClean()
		}
	}
}

func (symCache *symlinkCache) doClean() {
	if symCache.size.Load() < int32(float64(symCache.cap)*0.75) {
		return
	}

	todo := symCache.size.Load() / 5
	cnt := int32(0)
	symCache.Range(func(key, value interface{}) bool {
		symCache.Delete(key)
		symCache.size.Add(-1)
		cnt++
		return cnt < todo
	})
}

type baseMeta struct {
	sync.Mutex
	addr string
	conf *Config
	fmt  *Format

	root         Ino
	txlocks      [nlocks]sync.Mutex // Pessimistic locks to reduce conflict
	subTrash     internalNode
	sid          uint64
	of           *openfiles
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	maxDeleting  chan struct{}
	dslices      chan Slice // slices to delete
	symlinks     *symlinkCache
	msgCallbacks *msgCallbacks
	reloadCb     []func(*Format)
	umounting    bool
	sesMu        sync.Mutex
	aclCache     aclAPI.Cache

	sessCtx Context
	sessWG  sync.WaitGroup

	dSliceMu sync.Mutex
	dSliceWG sync.WaitGroup

	dirStatsLock sync.Mutex
	dirStats     map[Ino]dirStat

	fsStatsLock sync.Mutex
	*fsStat

	parentMu    sync.Mutex        // protect dirParents
	quotaMu     sync.RWMutex      // protect dirQuotas
	dirParents  map[Ino]Ino       // directory inode -> parent inode
	dirQuotas   map[uint64]*Quota // directory inode -> quota
	userQuotas  map[uint64]*Quota // uid -> quota
	groupQuotas map[uint64]*Quota // gid -> quota

	freeMu           sync.Mutex
	freeInodes       freeID
	freeSlices       freeID
	prefetchMu       sync.Mutex
	prefetchedInodes freeID

	usedSpaceG   prometheus.Gauge
	usedInodesG  prometheus.Gauge
	totalSpaceG  prometheus.Gauge
	totalInodesG prometheus.Gauge
	txDist       prometheus.Histogram
	txRestart    *prometheus.CounterVec
	opDist       prometheus.Histogram
	opCount      *prometheus.CounterVec
	opDuration   *prometheus.CounterVec

	en engine
}

func newBaseMeta(addr string, conf *Config) *baseMeta {
	return &baseMeta{
		addr:         utils.RemovePassword(addr),
		conf:         conf,
		sid:          conf.Sid,
		root:         RootInode,
		of:           newOpenFiles(conf.OpenCache, conf.OpenCacheLimit),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		maxDeleting:  make(chan struct{}, 100),
		symlinks:     newSymlinkCache(maxSymCacheNum),
		fsStat: &fsStat{
			usedSpace:  unknownUsage,
			usedInodes: unknownUsage,
		},
		dirStats:    make(map[Ino]dirStat),
		dirParents:  make(map[Ino]Ino),
		dirQuotas:   make(map[uint64]*Quota),
		userQuotas:  make(map[uint64]*Quota),
		groupQuotas: make(map[uint64]*Quota),
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
		aclCache: aclAPI.NewCache(),

		usedSpaceG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "used_space",
			Help: "Total used space in bytes.",
		}),
		usedInodesG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "used_inodes",
			Help: "Total used number of inodes.",
		}),
		totalSpaceG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "total_space",
			Help: "Total space in bytes.",
		}),
		totalInodesG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "total_inodes",
			Help: "Total number of inodes.",
		}),
		txDist: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "transaction_durations_histogram_seconds",
			Help:    "Transactions latency distributions.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
		}),
		txRestart: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "transaction_restart",
			Help: "The number of times a transaction is restarted.",
		}, []string{"method"}),
		opDist: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "meta_ops_durations_histogram_seconds",
			Help:    "Operation latency distributions.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
		}),
		opCount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meta_ops_total",
			Help: "Meta operation count",
		}, []string{"method"}),
		opDuration: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meta_ops_duration_seconds",
			Help: "Meta operation duration in seconds.",
		}, []string{"method"}),
	}
}

func (m *baseMeta) InitMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(m.usedSpaceG)
	reg.MustRegister(m.usedInodesG)
	reg.MustRegister(m.totalSpaceG)
	reg.MustRegister(m.totalInodesG)
	reg.MustRegister(m.txDist)
	reg.MustRegister(m.txRestart)
	reg.MustRegister(m.opDist)
	reg.MustRegister(m.opCount)
	reg.MustRegister(m.opDuration)

	go func() {
		for {
			if m.sessCtx != nil && m.sessCtx.Canceled() {
				return
			}
			var totalSpace, availSpace, iused, iavail uint64
			err := m.StatFS(Background(), m.root, &totalSpace, &availSpace, &iused, &iavail)
			if err == 0 {
				m.usedSpaceG.Set(float64(totalSpace - availSpace))
				m.usedInodesG.Set(float64(iused))
				m.totalSpaceG.Set(float64(totalSpace))
				m.totalInodesG.Set(float64(iused + iavail))
			}
			utils.SleepWithJitter(time.Second * 10)
		}
	}()
}

func (m *baseMeta) timeit(method string, start time.Time) {
	used := time.Since(start).Seconds()
	m.opDist.Observe(used)
	m.opCount.WithLabelValues(method).Inc()
	m.opDuration.WithLabelValues(method).Add(used)
}

func (m *baseMeta) getBase() *baseMeta {
	return m
}

func (m *baseMeta) checkRoot(inode Ino) Ino {
	switch inode {
	case 0:
		return RootInode // force using Root inode
	case RootInode:
		return m.root
	default:
		return inode
	}
}

func (r *baseMeta) txLock(idx uint) {
	r.txlocks[idx%nlocks].Lock()
}

func (r *baseMeta) txUnlock(idx uint) {
	r.txlocks[idx%nlocks].Unlock()
}

func (r *baseMeta) txBatchLock(inodes ...Ino) func() {
	switch len(inodes) {
	case 0:
		return func() {}
	case 1: // most cases
		r.txLock(uint(inodes[0]))
		return func() { r.txUnlock(uint(inodes[0])) }
	default: // for rename and more
		inodeSlots := make([]int, len(inodes))
		for i, ino := range inodes {
			inodeSlots[i] = int(ino % nlocks)
		}
		sort.Ints(inodeSlots)
		uniqInodeSlots := inodeSlots[:0]
		for i := 0; i < len(inodeSlots); i++ { // Go does not support recursive locks
			if i == 0 || inodeSlots[i] != inodeSlots[i-1] {
				uniqInodeSlots = append(uniqInodeSlots, inodeSlots[i])
			}
		}
		for _, idx := range uniqInodeSlots {
			r.txlocks[idx].Lock()
		}
		return func() {
			for _, idx := range uniqInodeSlots {
				r.txlocks[idx].Unlock()
			}
		}
	}
}

func (r *baseMeta) OnMsg(mtype uint32, cb MsgCallback) {
	r.msgCallbacks.Lock()
	defer r.msgCallbacks.Unlock()
	r.msgCallbacks.callbacks[mtype] = cb
}

func (r *baseMeta) newMsg(mid uint32, args ...interface{}) error {
	r.msgCallbacks.Lock()
	cb, ok := r.msgCallbacks.callbacks[mid]
	r.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (m *baseMeta) Load(checkVersion bool) (*Format, error) {
	body, err := m.en.doLoad()
	if err == nil && len(body) == 0 {
		err = fmt.Errorf("database is not formatted, please run `juicefs format ...` first")
	}
	if err != nil {
		return nil, err
	}
	var format = new(Format)
	if err = json.Unmarshal(body, format); err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	if checkVersion {
		if err = format.CheckVersion(); err != nil {
			return nil, fmt.Errorf("check version: %s", err)
		}
	}
	m.Lock()
	m.fmt = format
	m.Unlock()
	return format, nil
}

func (m *baseMeta) newSessionInfo() []byte {
	host, err := os.Hostname()
	if err != nil {
		logger.Warnf("Failed to get hostname: %s", err)
	}
	ips, err := utils.FindLocalIPs()
	if err != nil {
		logger.Warnf("Failed to get local IP: %s", err)
	}
	addrs := make([]string, 0, len(ips))
	for _, i := range ips {
		if ip := i.String(); ip[0] == '?' {
			logger.Warnf("Invalid IP address: %s", ip)
		} else {
			addrs = append(addrs, ip)
		}
	}
	buf, err := json.Marshal(&SessionInfo{
		Version:    version.Version(),
		HostName:   host,
		IPAddrs:    addrs,
		MountPoint: m.conf.MountPoint,
		MountTime:  time.Now(),
		ProcessID:  os.Getpid(),
	})
	if err != nil {
		panic(err) // marshal SessionInfo should never fail
	}
	return buf
}

func (m *baseMeta) NewSession(record bool) error {
	m.sessCtx = Background()
	ctx := m.sessCtx
	go m.refresh(ctx)

	if err := m.en.cacheACLs(ctx); err != nil {
		return err
	}

	if m.conf.ReadOnly {
		logger.Infof("Create read-only session OK with version: %s", version.Version())
		return nil
	}

	if record {
		// use the original sid if it's not 0
		action := "Update"
		if m.sid == 0 {
			v, err := m.en.incrCounter("nextSession", 1)
			if err != nil {
				return fmt.Errorf("get session ID: %s", err)
			}
			m.sid = uint64(v)
			m.conf.Sid = m.sid
			action = "Create"
		}
		if err := m.en.doNewSession(m.newSessionInfo(), action == "Update"); err != nil {
			return fmt.Errorf("create session: %s", err)
		}
		logger.Infof("%s session %d OK with version: %s", action, m.sid, version.Version())
	}

	m.loadQuotas()

	m.sessWG.Add(3)
	go m.flushStats(ctx)
	go m.flushDirStat(ctx)
	go m.flushQuotas(ctx)
	m.startDeleteSliceTasks() // start MaxDeletes tasks

	if !m.conf.NoBGJob {
		m.sessWG.Add(4)
		go m.cleanupDeletedFiles(ctx)
		go m.cleanupSlices(ctx)
		go m.cleanupTrash(ctx)
		go m.symlinks.clean(ctx, &m.sessWG)
	}
	return nil
}

func (m *baseMeta) startDeleteSliceTasks() {
	m.Lock()
	defer m.Unlock()
	if m.conf.MaxDeletes <= 0 || m.dslices != nil {
		return
	}
	m.sessWG.Add(m.conf.MaxDeletes)
	m.dSliceWG.Add(m.conf.MaxDeletes)
	m.dslices = make(chan Slice, m.conf.MaxDeletes*10240)
	for i := 0; i < m.conf.MaxDeletes; i++ {
		go func(dslices chan Slice) {
			defer m.sessWG.Done()
			defer m.dSliceWG.Done()
			for {
				select {
				case <-m.sessCtx.Done():
					return
				case s, ok := <-dslices:
					if !ok {
						return
					}
					m.deleteSlice_(s.Id, s.Size)
				}
			}
		}(m.dslices)
	}
}

func (m *baseMeta) stopDeleteSliceTasks() {
	m.dSliceMu.Lock()
	if m.conf.MaxDeletes <= 0 || m.dslices == nil {
		m.dSliceMu.Unlock()
		return
	}
	close(m.dslices)
	m.dslices = nil
	m.dSliceMu.Unlock()
	m.dSliceWG.Wait()
}

func (m *baseMeta) expireTime() int64 {
	if m.conf.Heartbeat > 0 {
		return time.Now().Add(m.conf.Heartbeat * 5).Unix()
	} else {
		return time.Now().Add(time.Hour * 24 * 365).Unix()
	}
}

func (m *baseMeta) OnReload(fn func(f *Format)) {
	m.msgCallbacks.Lock()
	defer m.msgCallbacks.Unlock()
	m.reloadCb = append(m.reloadCb, fn)
}

const UmountCode = 11

func (m *baseMeta) refresh(ctx Context) {
	for {
		if ctx.Canceled() {
			return
		}
		if m.conf.Heartbeat > 0 {
			utils.SleepWithJitter(m.conf.Heartbeat)
		} else { // use default value
			utils.SleepWithJitter(time.Second * 12)
		}
		m.sesMu.Lock()
		if m.umounting {
			m.sesMu.Unlock()
			return
		}
		if !m.conf.ReadOnly && m.conf.Heartbeat > 0 && m.sid > 0 {
			if err := m.en.doRefreshSession(); err != nil {
				logger.Errorf("Refresh session %d: %s", m.sid, err)
			}
		}
		m.sesMu.Unlock()

		old := m.getFormat()
		if format, err := m.Load(false); err != nil {
			if strings.HasPrefix(err.Error(), "database is not formatted") {
				logger.Errorf("reload setting: %s", err)
				os.Exit(UmountCode)
			}
			logger.Warnf("reload setting: %s", err)
		} else if format.MetaVersion > MaxVersion {
			logger.Errorf("incompatible metadata version %d > max version %d", format.MetaVersion, MaxVersion)
			os.Exit(UmountCode)
		} else if format.UUID != old.UUID {
			logger.Errorf("UUID changed from %s to %s", old.UUID, format.UUID)
			os.Exit(UmountCode)
		} else if !reflect.DeepEqual(format, old) {
			m.msgCallbacks.Lock()
			cbs := m.reloadCb
			m.msgCallbacks.Unlock()
			for _, cb := range cbs {
				cb(format)
			}
		}

		if v, err := m.en.getCounter(usedSpace); err == nil {
			atomic.StoreInt64(&m.usedSpace, v)
		} else {
			logger.Warnf("Get counter %s: %s", usedSpace, err)
		}
		if v, err := m.en.getCounter(totalInodes); err == nil {
			atomic.StoreInt64(&m.usedInodes, v)
		} else {
			logger.Warnf("Get counter %s: %s", totalInodes, err)
		}
		m.loadQuotas()

		if m.conf.ReadOnly || m.conf.NoBGJob || m.conf.Heartbeat == 0 {
			continue
		}
		if ok, err := m.en.setIfSmall("lastCleanupSessions", time.Now().Unix(), int64((m.conf.Heartbeat * 9 / 10).Seconds())); err != nil {
			logger.Warnf("checking counter lastCleanupSessions: %s", err)
		} else if ok {
			go m.CleanStaleSessions(ctx)
		}
	}
}

func (m *baseMeta) CleanStaleSessions(ctx Context) {
	sids, err := m.en.doFindStaleSessions(1000)
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	for _, sid := range sids {
		if ctx.Canceled() {
			return
		}
		s, err := m.en.GetSession(sid, false)
		if err != nil {
			logger.Warnf("Get session info %d: %v", sid, err)
			s = &Session{Sid: sid}
		}
		logger.Infof("clean up stale session %d %+v: %v", sid, s.SessionInfo, m.en.doCleanStaleSession(sid))
	}
}

func (m *baseMeta) CloseSession() error {
	m.FlushSession()
	m.sesMu.Lock()
	m.umounting = true
	m.sesMu.Unlock()
	var err error
	if m.sid > 0 {
		err = m.en.doCleanStaleSession(m.sid)
	}
	m.sessCtx.Cancel()
	m.sessWG.Wait()
	m.stopDeleteSliceTasks()
	logger.Infof("close session %d: %v", m.sid, err)
	return err
}

func (m *baseMeta) FlushSession() {
	if m.conf.ReadOnly {
		return
	}
	m.doFlushStats()
	m.doFlushDirStat()
	m.doFlushQuotas()
	logger.Infof("flush session %d:", m.sid)
}

func (m *baseMeta) Init(format *Format, force bool) error {
	return m.en.doInit(format, force)
}

func (m *baseMeta) cleanupDeletedFiles(ctx Context) {
	defer m.sessWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(utils.JitterIt(time.Hour)):
		}
		if ok, err := m.en.setIfSmall("lastCleanupFiles", time.Now().Unix(), int64(time.Hour.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter lastCleanupFiles: %s", err)
		} else if ok {
			files, err := m.en.doFindDeletedFiles(time.Now().Add(-time.Hour).Unix(), 6e5)
			if err != nil {
				logger.Warnf("scan deleted files: %s", err)
				continue
			}
			start := time.Now()
			for inode, length := range files {
				logger.Debugf("cleanup chunks of inode %d with %d bytes", inode, length)
				m.en.doDeleteFileData(inode, length)
				if time.Since(start) > 50*time.Minute { // Yield my time slice to avoid conflicts with other clients
					break
				}
			}
		}
	}
}

func (m *baseMeta) cleanupSlices(ctx Context) {
	defer m.sessWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(utils.JitterIt(time.Hour)):
		}
		if ok, err := m.en.setIfSmall("nextCleanupSlices", time.Now().Unix(), int64(time.Hour.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter nextCleanupSlices: %s", err)
		} else if ok {
			cCtx := WrapWithTimeout(ctx, time.Minute*50)
			m.en.doCleanupSlices(cCtx)
			cCtx.Cancel()
		}
	}
}

func (m *baseMeta) StatFS(ctx Context, ino Ino, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	defer m.timeit("StatFS", time.Now())
	if st := m.statRootFs(ctx, totalspace, availspace, iused, iavail); st != 0 {
		return st
	}
	ino = m.checkRoot(ino)
	var usage, quota *Quota
	for ino >= RootInode {
		ino, quota = m.getQuotaParent(ctx, ino)
		if quota == nil {
			break
		}
		q := quota.snap()
		q.sanitize()
		if usage == nil {
			usage = &q
		}
		if q.MaxSpace > 0 {
			ls := uint64(q.MaxSpace - q.UsedSpace)
			if ls < *availspace {
				*availspace = ls
			}
		}
		if q.MaxInodes > 0 {
			li := uint64(q.MaxInodes - q.UsedInodes)
			if li < *iavail {
				*iavail = li
			}
		}
		if ino == RootInode {
			break
		}
		if parent, st := m.getDirParent(ctx, ino); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", ino, st)
			break
		} else {
			ino = parent
		}
	}
	if usage != nil {
		*totalspace = uint64(usage.UsedSpace) + *availspace
		*iused = uint64(usage.UsedInodes)
	}
	return 0
}

func (m *baseMeta) statRootFs(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	used, inodes := atomic.LoadInt64(&m.usedSpace), atomic.LoadInt64(&m.usedInodes)
	var err error
	if !m.conf.FastStatfs || used == unknownUsage || inodes == unknownUsage {
		var remoteUsed int64 // using an additional variable here to ensure the assignment inside `utils.WithTimeout` does not change the `used` variable again after a timeout.
		err = utils.WithTimeout(func(context.Context) error {
			remoteUsed, err = m.en.getCounter(usedSpace)
			return err
		}, time.Millisecond*150)
		if err == nil {
			used = remoteUsed
		}
		var remoteInodes int64
		err = utils.WithTimeout(func(context.Context) error {
			remoteInodes, err = m.en.getCounter(totalInodes)
			return err
		}, time.Millisecond*150)
		if err == nil {
			inodes = remoteInodes
		}
	}

	used += atomic.LoadInt64(&m.newSpace)
	inodes += atomic.LoadInt64(&m.newInodes)
	if used < 0 {
		used = 0
	}
	format := m.getFormat()
	if format.Capacity > 0 {
		*totalspace = format.Capacity
		if *totalspace < uint64(used) {
			*totalspace = uint64(used)
		}
	} else {
		*totalspace = 1 << 50
		for *totalspace*8 < uint64(used)*10 {
			*totalspace *= 2
		}
	}
	*availspace = *totalspace - uint64(used)
	if inodes < 0 {
		inodes = 0
	}
	*iused = uint64(inodes)
	if format.Inodes > 0 {
		if *iused > format.Inodes {
			*iavail = 0
		} else {
			*iavail = format.Inodes - *iused
		}
	} else {
		*iavail = 10 << 20
		for *iused*10 > (*iused+*iavail)*8 {
			*iavail *= 2
		}
	}
	return 0
}

func (m *baseMeta) resolveCase(ctx Context, parent Ino, name string) *Entry {
	var entries []*Entry
	_ = m.en.doReaddir(ctx, parent, 0, &entries, -1)
	for _, e := range entries {
		n := string(e.Name)
		if strings.EqualFold(name, n) {
			return e
		}
	}
	return nil
}

func (m *baseMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno {
	if inode == nil || attr == nil {
		return syscall.EINVAL // bad request
	}
	defer m.timeit("Lookup", time.Now())
	parent = m.checkRoot(parent)
	if checkPerm {
		if st := m.Access(ctx, parent, MODE_MASK_X, nil); st != 0 {
			return st
		}
	}
	if name == ".." {
		if parent == m.root {
			name = "."
		} else {
			if st := m.GetAttr(ctx, parent, attr); st != 0 {
				return st
			}
			if attr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			*inode = attr.Parent
			return m.GetAttr(ctx, *inode, attr)
		}
	}
	if name == "." {
		if st := m.GetAttr(ctx, parent, attr); st != 0 {
			return st
		}
		*inode = parent
		return 0
	}
	if parent == RootInode && name == TrashName {
		if st := m.GetAttr(ctx, TrashInode, attr); st != 0 {
			return st
		}
		*inode = TrashInode
		return 0
	}
	st := m.en.doLookup(ctx, parent, name, inode, attr)
	if st == syscall.ENOENT && m.conf.CaseInsensi {
		if e := m.resolveCase(ctx, parent, name); e != nil {
			*inode = e.Inode
			if st = m.GetAttr(ctx, *inode, attr); st == syscall.ENOENT {
				logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, e.Name)
				*attr = *e.Attr
				st = 0
			}
		}
	}
	if st == 0 && attr.Typ == TypeDirectory && !parent.IsTrash() {
		m.parentMu.Lock()
		m.dirParents[*inode] = parent
		m.parentMu.Unlock()
	}
	return st
}

func (attr *Attr) reset() {
	attr.Flags = 0
	attr.Mode = 0
	attr.Typ = 0
	attr.Uid = 0
	attr.Gid = 0
	attr.Atime = 0
	attr.Atimensec = 0
	attr.Mtime = 0
	attr.Mtimensec = 0
	attr.Ctime = 0
	attr.Ctimensec = 0
	attr.Nlink = 0
	attr.Length = 0
	attr.Rdev = 0
	attr.Parent = 0
	attr.AccessACL = aclAPI.None
	attr.DefaultACL = aclAPI.None
	attr.Full = false
}

func (m *baseMeta) parseAttr(buf []byte, attr *Attr) {
	attr.Unmarshal(buf)
}

func (m *baseMeta) marshal(attr *Attr) []byte {
	return attr.Marshal()
}

func (m *baseMeta) encodeDelayedSlice(id uint64, size uint32) []byte {
	w := utils.NewBuffer(8 + 4)
	w.Put64(id)
	w.Put32(size)
	return w.Bytes()
}

func (m *baseMeta) decodeDelayedSlices(buf []byte, ss *[]Slice) {
	if len(buf) == 0 || len(buf)%12 != 0 {
		return
	}
	for rb := utils.FromBuffer(buf); rb.HasMore(); {
		*ss = append(*ss, Slice{Id: rb.Get64(), Size: rb.Get32()})
	}
}

func clearSUGID(ctx Context, cur *Attr, set *Attr) {
	switch runtime.GOOS {
	case "darwin":
		if ctx.Uid() != 0 {
			// clear SUID and SGID
			cur.Mode &= 01777
			set.Mode &= 01777
		}
	case "linux":
		// same as ext
		if cur.Typ != TypeDirectory {
			if ctx.Uid() != 0 || (cur.Mode>>3)&1 != 0 {
				// clear SUID and SGID
				cur.Mode &= 01777
				set.Mode &= 01777
			} else {
				// keep SGID if the file is non-group-executable
				cur.Mode &= 03777
				set.Mode &= 03777
			}
		}
	}
}

func (r *baseMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *baseMeta) Access(ctx Context, inode Ino, mmask uint8, attr *Attr) syscall.Errno {
	if ctx.Uid() == 0 {
		return 0
	}
	if !ctx.CheckPermission() {
		return 0
	}

	if attr == nil || !attr.Full {
		if attr == nil {
			attr = &Attr{}
		}
		err := m.GetAttr(ctx, inode, attr)
		if err != 0 {
			return err
		}
	}

	// ref: https://github.com/torvalds/linux/blob/e5eb28f6d1afebed4bb7d740a797d0390bd3a357/fs/namei.c#L352-L357
	// dont check acl if mask is 0
	if attr.AccessACL != aclAPI.None && (attr.Mode&00070) != 0 {
		rule := &aclAPI.Rule{}
		if st := m.en.doGetFacl(ctx, inode, aclAPI.TypeAccess, attr.AccessACL, rule); st != 0 {
			return st
		}
		if rule.CanAccess(ctx.Uid(), ctx.Gids(), attr.Uid, attr.Gid, mmask) {
			return 0
		}
		return syscall.EACCES
	}

	mode := accessMode(attr, ctx.Uid(), ctx.Gids())
	if mode&mmask != mmask {
		logger.Debugf("Access inode %d %o, mode %o, request mode %o", inode, attr.Mode, mode, mmask)
		return syscall.EACCES
	}
	return 0
}

func (m *baseMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	inode = m.checkRoot(inode)
	if m.conf.OpenCache > 0 && m.of.Check(inode, attr) {
		return 0
	}
	defer m.timeit("GetAttr", time.Now())
	var err syscall.Errno
	if inode == RootInode || inode == TrashInode {
		// doGetAttr could overwrite the `attr` after timeout
		var a Attr
		e := utils.WithTimeout(func(context.Context) error {
			err = m.en.doGetAttr(ctx, inode, &a)
			return nil
		}, time.Millisecond*300)
		if e == nil && err == 0 {
			*attr = a
		} else {
			err = 0
			attr.Typ = TypeDirectory
			attr.Mode = 0777
			attr.Nlink = 2
			attr.Length = 4 << 10
			if inode == TrashInode {
				attr.Mode = 0555
			}
			attr.Parent = RootInode
			attr.Full = true
		}
	} else {
		err = m.en.doGetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.of.Update(inode, attr)
		if attr.Typ == TypeDirectory && inode != RootInode && !attr.Parent.IsTrash() {
			m.parentMu.Lock()
			m.dirParents[inode] = attr.Parent
			m.parentMu.Unlock()
		}
	}
	return err
}

func (m *baseMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	defer m.timeit("SetAttr", time.Now())
	inode = m.checkRoot(inode)
	var oldAttr Attr

	err := m.en.doSetAttr(ctx, inode, set, sugidclearmode, attr, &oldAttr)
	if err == 0 {
		m.of.InvalidateChunk(inode, invalidateAttrOnly)
		m.of.Update(inode, attr)

		uidChanged := oldAttr.Uid != attr.Uid
		gidChanged := oldAttr.Gid != attr.Gid
		if uidChanged || gidChanged {
			var space, inodes int64
			if attr.Typ == TypeFile {
				space = align4K(attr.Length)
				inodes = 1
			} else if attr.Typ == TypeDirectory {
				space = align4K(0)
				inodes = 1
			}

			if uidChanged {
				m.updateUserGroupQuota(ctx, oldAttr.Uid, 0, -space, -inodes)
				m.updateUserGroupQuota(ctx, attr.Uid, 0, space, inodes)
			}
			if gidChanged {
				m.updateUserGroupQuota(ctx, 0, oldAttr.Gid, -space, -inodes)
				m.updateUserGroupQuota(ctx, 0, attr.Gid, space, inodes)
			}
		}
	}
	return err
}

func (m *baseMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next >= m.freeInodes.maxid {

		m.prefetchMu.Lock() // Wait until prefetchInodes() is done
		if m.prefetchedInodes.maxid > m.freeInodes.maxid {
			m.freeInodes = m.prefetchedInodes
			m.prefetchedInodes = freeID{}
		}
		m.prefetchMu.Unlock()

		if m.freeInodes.next >= m.freeInodes.maxid { // Prefetch missed, try again
			nextInodes, err := m.allocateInodes()
			if err != nil {
				return 0, err
			}
			m.freeInodes = nextInodes
		}
	}
	n := m.freeInodes.next
	m.freeInodes.next++
	for n <= 1 {
		n = m.freeInodes.next
		m.freeInodes.next++
	}
	if m.freeInodes.maxid-m.freeInodes.next == inodeNeedPrefetch {
		go m.prefetchInodes()
	}
	return Ino(n), nil
}

func (m *baseMeta) prefetchInodes() {
	m.prefetchMu.Lock()
	defer m.prefetchMu.Unlock()
	if m.prefetchedInodes.maxid > m.freeInodes.maxid {
		return // Someone else has done the job
	}
	nextInodes, err := m.allocateInodes()
	if err == nil {
		m.prefetchedInodes = nextInodes
	} else {
		logger.Warnf("Failed to prefetch inodes: %s, current limit: %d", err, m.freeInodes.maxid)
	}
}

func (m *baseMeta) allocateInodes() (freeID, error) {
	v, err := m.en.incrCounter("nextInode", inodeBatch)
	if err != nil {
		return freeID{}, err
	}
	return freeID{next: uint64(v) - inodeBatch, maxid: uint64(v)}, nil
}

func (m *baseMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	if _type < TypeFile || _type > TypeSocket {
		return syscall.EINVAL
	}
	if parent.IsTrash() {
		return syscall.EPERM
	}
	if parent == RootInode && name == TrashName {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "." || name == ".." {
		return syscall.EEXIST
	}
	if errno := checkInodeName(name); errno != 0 {
		return errno
	}

	defer m.timeit("Mknod", time.Now())
	parent = m.checkRoot(parent)
	var space, inodes int64 = align4K(0), 1
	if err := m.checkQuota(ctx, space, inodes, ctx.Uid(), ctx.Gid(), parent); err != 0 {
		return err
	}

	ino, err := m.nextInode()
	if err != nil {
		return errno(err)
	}
	if inode == nil {
		inode = &ino
	}
	*inode = ino
	if attr == nil {
		attr = &Attr{}
	}
	attr.Typ = _type
	attr.Uid = ctx.Uid()
	attr.Gid = ctx.Gid()
	if _type == TypeDirectory {
		attr.Nlink = 2
		attr.Length = 4 << 10
	} else {
		attr.Nlink = 1
		if _type == TypeSymlink {
			attr.Length = uint64(len(path))
		} else {
			attr.Length = 0
			attr.Rdev = rdev
		}
	}
	attr.Parent = parent
	attr.Full = true
	st := m.en.doMknod(ctx, parent, name, _type, mode, cumask, path, inode, attr)
	if st == 0 {
		m.en.updateStats(space, inodes)
		m.updateDirStat(ctx, parent, 0, space, inodes)
		m.updateDirQuota(ctx, parent, space, inodes)
		m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, space, inodes)
	}
	return st
}

func (m *baseMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if attr == nil {
		attr = &Attr{}
	}
	eno := m.Mknod(ctx, parent, name, TypeFile, mode, cumask, 0, "", inode, attr)
	if eno == syscall.EEXIST && (flags&syscall.O_EXCL) == 0 && attr.Typ == TypeFile {
		eno = 0
	}
	if eno == 0 && inode != nil {
		m.of.Open(*inode, attr)
	}
	return eno
}

func (m *baseMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	st := m.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, "", inode, attr)
	if st == 0 {
		m.parentMu.Lock()
		m.dirParents[*inode] = parent
		m.parentMu.Unlock()
	}
	return st
}

func (m *baseMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	if len(path) == 0 || len(path) > MaxSymlink {
		return syscall.EINVAL
	}
	for _, c := range path {
		if c == 0 {
			return syscall.EINVAL
		}
	}
	// mode of symlink is ignored in POSIX
	return m.Mknod(ctx, parent, name, TypeSymlink, 0777, 0, 0, path, inode, attr)
}

func (m *baseMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	if parent.IsTrash() {
		return syscall.EPERM
	}
	if parent == RootInode && name == TrashName {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if errno := checkInodeName(name); errno != 0 {
		return errno
	}
	if name == "." || name == ".." {
		return syscall.EEXIST
	}

	defer m.timeit("Link", time.Now())
	if attr == nil {
		attr = &Attr{}
	}
	parent = m.checkRoot(parent)
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		return st
	}
	if attr.Typ == TypeDirectory {
		return syscall.EPERM
	}

	if m.checkUserQuota(ctx, uint64(attr.Uid), 0, 1) {
		return syscall.EDQUOT
	}
	if m.checkGroupQuota(ctx, uint64(attr.Gid), 0, 1) {
		return syscall.EDQUOT
	}
	if m.checkDirQuota(ctx, parent, align4K(attr.Length), 1) {
		return syscall.EDQUOT
	}

	defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
	err := m.en.doLink(ctx, inode, parent, name, attr)
	if err == 0 {
		m.updateDirStat(ctx, parent, int64(attr.Length), align4K(attr.Length), 1)
		m.updateDirQuota(ctx, parent, align4K(attr.Length), 1)
		m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, 0, 1)
	}
	return err
}

func (m *baseMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	noatime := m.conf.AtimeMode == NoAtime || m.conf.ReadOnly
	if target, ok := m.symlinks.Load(inode); ok {
		if noatime {
			*path = target.([]byte)
			return 0
		} else {
			buf := target.([]byte)
			// ctime and mtime are ignored since symlink can't be modified
			atime := int64(binary.BigEndian.Uint64(buf[:8]))
			attr := &Attr{Atime: atime / int64(time.Second), Atimensec: uint32(atime % int64(time.Second))}
			if !m.atimeNeedsUpdate(attr, time.Now()) {
				*path = buf[8:]
				return 0
			}
		}
	}
	defer m.timeit("ReadLink", time.Now())
	atime, target, err := m.en.doReadlink(ctx, inode, noatime)
	if err != nil {
		return errno(err)
	}
	if len(target) == 0 {
		var attr Attr
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			return st
		}
		if attr.Typ != TypeSymlink {
			return syscall.EINVAL
		}
		return syscall.EIO
	}
	*path = target
	if noatime {
		m.symlinks.Store(inode, target)
	} else {
		buf := make([]byte, 8+len(target))
		binary.BigEndian.PutUint64(buf[:8], uint64(atime))
		copy(buf[8:], target)
		m.symlinks.Store(inode, buf)
	}
	return 0
}

func (m *baseMeta) Unlink(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno {
	if parent == RootInode && name == TrashName || parent.IsTrash() && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}

	defer m.timeit("Unlink", time.Now())
	parent = m.checkRoot(parent)
	var attr Attr
	err := m.en.doUnlink(ctx, parent, name, &attr, skipCheckTrash...)
	if err == 0 {
		var diffLength uint64
		if attr.Typ == TypeFile {
			diffLength = attr.Length
		}
		m.updateDirStat(ctx, parent, -int64(diffLength), -align4K(diffLength), -1)
		if !parent.IsTrash() {
			m.updateDirQuota(ctx, parent, -align4K(diffLength), -1)
			if attr.Typ == TypeFile && attr.Nlink > 0 {
				m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, 0, -1)
			} else {
				m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, -align4K(diffLength), -1)
			}
		}
	}
	return err
}

func (m *baseMeta) Rmdir(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}
	if parent == RootInode && name == TrashName || parent == TrashInode || parent.IsTrash() && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}

	defer m.timeit("Rmdir", time.Now())
	parent = m.checkRoot(parent)
	var inode Ino
	var oldAttr Attr
	st := m.en.doRmdir(ctx, parent, name, &inode, &oldAttr, skipCheckTrash...)
	if st == 0 {
		if !parent.IsTrash() {
			m.parentMu.Lock()
			delete(m.dirParents, inode)
			m.parentMu.Unlock()
		}
		m.updateDirStat(ctx, parent, 0, -align4K(0), -1)
		m.updateDirQuota(ctx, parent, -align4K(0), -1)
		m.updateUserGroupQuota(ctx, oldAttr.Uid, oldAttr.Gid, -align4K(0), -1)
	}
	return st
}

func (m *baseMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if parentSrc == RootInode && nameSrc == TrashName || parentDst == RootInode && nameDst == TrashName {
		return syscall.EPERM
	}
	if parentDst.IsTrash() || parentSrc.IsTrash() && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if errno := checkInodeName(nameDst); errno != 0 {
		return errno
	}

	switch flags {
	case 0, RenameNoReplace, RenameExchange, RenameNoReplace | RenameRestore:
	case RenameWhiteout, RenameNoReplace | RenameWhiteout:
		return syscall.ENOTSUP
	default:
		return syscall.EINVAL
	}

	defer m.timeit("Rename", time.Now())
	if inode == nil {
		inode = new(Ino)
	}
	if attr == nil {
		attr = &Attr{}
	}
	parentSrc = m.checkRoot(parentSrc)
	parentDst = m.checkRoot(parentDst)
	var quotaSrc, quotaDst Ino
	if !parentSrc.IsTrash() {
		quotaSrc, _ = m.getQuotaParent(ctx, parentSrc)
	}
	if parentSrc == parentDst {
		quotaDst = quotaSrc
	} else {
		quotaDst, _ = m.getQuotaParent(ctx, parentDst)
	}
	var space, inodes int64
	if quotaSrc != quotaDst {
		if st := m.Lookup(ctx, parentSrc, nameSrc, inode, attr, false); st != 0 {
			return st
		}
		if attr.Typ == TypeDirectory {
			m.quotaMu.RLock()
			q := m.dirQuotas[uint64(*inode)]
			m.quotaMu.RUnlock()
			if q != nil {
				space, inodes = q.UsedSpace+align4K(0), q.UsedInodes+1
			} else {
				var sum Summary
				logger.Debugf("Start to get summary of inode %d", *inode)
				if st := m.GetSummary(ctx, *inode, &sum, true, false); st != 0 {
					logger.Warnf("Get summary of inode %d: %s", *inode, st)
					return st
				}
				space, inodes = int64(sum.Size), int64(sum.Dirs+sum.Files)
			}
		} else {
			space, inodes = align4K(attr.Length), 1
		}
		// TODO: dst exists and is replaced or exchanged
		if quotaDst > 0 && m.checkDirQuota(ctx, parentDst, space, inodes) {
			return syscall.EDQUOT
		}
	}
	tinode := new(Ino)
	tattr := new(Attr)
	st := m.en.doRename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, inode, tinode, attr, tattr)
	if st == 0 {
		var diffLength uint64
		if attr.Typ == TypeDirectory {
			m.parentMu.Lock()
			m.dirParents[*inode] = parentDst
			m.parentMu.Unlock()
		} else if attr.Typ == TypeFile {
			diffLength = attr.Length
		}
		if parentSrc != parentDst {
			m.updateDirStat(ctx, parentSrc, -int64(diffLength), -align4K(diffLength), -1)
			m.updateDirStat(ctx, parentDst, int64(diffLength), align4K(diffLength), 1)
			if quotaSrc != quotaDst {
				if quotaSrc > 0 {
					m.updateDirQuota(ctx, parentSrc, -space, -inodes)
				}
				if quotaDst > 0 {
					m.updateDirQuota(ctx, parentDst, space, inodes)
				}
			}
			if flags&RenameRestore != 0 && parentSrc.IsTrash() {
				m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, align4K(diffLength), 1)
			}
		}
		if *tinode > 0 && flags != RenameExchange {
			diffLength = 0
			if tattr.Typ == TypeDirectory {
				m.parentMu.Lock()
				delete(m.dirParents, *tinode)
				m.parentMu.Unlock()
			} else if attr.Typ == TypeFile {
				diffLength = tattr.Length
			}
			m.updateDirStat(ctx, parentDst, -int64(diffLength), -align4K(diffLength), -1)
			if quotaDst > 0 {
				m.updateDirQuota(ctx, parentDst, -align4K(diffLength), -1)
			}
			m.updateUserGroupQuota(ctx, tattr.Uid, tattr.Gid, -align4K(diffLength), -1)
		}
	}
	return st
}

// caller makes sure inode is not special inode.
func (m *baseMeta) touchAtime(ctx Context, inode Ino, attr *Attr) {
	if m.conf.AtimeMode == NoAtime || m.conf.ReadOnly {
		return
	}

	if attr == nil {
		attr = new(Attr)
		if of := m.of.find(inode); of != nil {
			*attr = of.attr
		}
	}
	now := time.Now()
	if attr.Full && !m.atimeNeedsUpdate(attr, now) {
		return
	}

	updated, err := m.en.doTouchAtime(ctx, inode, attr, now)
	if updated {
		m.of.Update(inode, attr)
	} else if err != nil {
		logger.Warnf("Update atime of inode %d: %s", inode, err)
	}
}

func (m *baseMeta) Open(ctx Context, inode Ino, flags uint32, attr *Attr) (st syscall.Errno) {
	if m.conf.ReadOnly && flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0 {
		return syscall.EROFS
	}
	defer func() {
		if st == 0 {
			m.touchAtime(ctx, inode, attr)
		}
	}()
	if m.conf.OpenCache > 0 && m.of.OpenCheck(inode, attr) {
		return 0
	}
	// attr may be valid, see fs.Open()
	if attr != nil && !attr.Full {
		if st = m.GetAttr(ctx, inode, attr); st != 0 {
			return
		}
	}
	var mmask uint8 = 0
	switch flags & (syscall.O_RDONLY | syscall.O_WRONLY | syscall.O_RDWR) {
	case syscall.O_RDONLY:
		mmask = MODE_MASK_R
		// 0x20 means O_FMODE_EXEC
		if (flags & 0x20) != 0 {
			mmask = MODE_MASK_X
		}
	case syscall.O_WRONLY:
		mmask = MODE_MASK_W
	case syscall.O_RDWR:
		mmask = MODE_MASK_R | MODE_MASK_W
	}
	if st = m.Access(ctx, inode, mmask, attr); st != 0 {
		return
	}

	if attr.Flags&FlagImmutable != 0 || attr.Parent > TrashInode {
		if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
			return syscall.EPERM
		}
	}
	if attr.Flags&FlagAppend != 0 {
		if (flags&(syscall.O_WRONLY|syscall.O_RDWR)) != 0 && (flags&syscall.O_APPEND) == 0 {
			return syscall.EPERM
		}
		if flags&syscall.O_TRUNC != 0 {
			return syscall.EPERM
		}
	}
	m.of.Open(inode, attr)
	return 0
}

func (m *baseMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	m.of.InvalidateChunk(inode, indx)
	return 0
}

func (m *baseMeta) Read(ctx Context, inode Ino, indx uint32, slices *[]Slice) (st syscall.Errno) {
	defer func() {
		if st == 0 {
			m.touchAtime(ctx, inode, nil)
		}
	}()

	f := m.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if ss, ok := m.of.ReadChunk(inode, indx); ok {
		*slices = ss
		return 0
	}

	*slices = nil
	defer m.timeit("Read", time.Now())
	ss, st := m.en.doRead(ctx, inode, indx)
	if st != 0 {
		return st
	}
	if ss == nil {
		return syscall.EIO
	}
	if len(ss) == 0 {
		var attr Attr
		if st = m.en.doGetAttr(ctx, inode, &attr); st != 0 {
			return st
		}
		if attr.Typ != TypeFile {
			return syscall.EPERM
		}
		return 0
	}

	*slices = buildSlice(ss)
	m.of.CacheChunk(inode, indx, *slices)
	if !m.conf.ReadOnly && (len(ss) >= 5 || len(*slices) >= 5) {
		go m.compactChunk(inode, indx, false, false)
	}
	return 0
}

func (m *baseMeta) NewSlice(ctx Context, id *uint64) syscall.Errno {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeSlices.next >= m.freeSlices.maxid {
		v, err := m.en.incrCounter("nextChunk", sliceIdBatch)
		if err != nil {
			return errno(err)
		}
		m.freeSlices.next = uint64(v) - sliceIdBatch
		m.freeSlices.maxid = uint64(v)
	}
	*id = m.freeSlices.next
	m.freeSlices.next++
	return 0
}

func (m *baseMeta) Close(ctx Context, inode Ino) syscall.Errno {
	if m.of.Close(inode) {
		m.Lock()
		_, removed := m.removedFiles[inode]
		if removed {
			delete(m.removedFiles, inode)
		}
		m.Unlock()
		if removed {
			_ = m.en.doDeleteSustainedInode(m.sid, inode)
		}
	}
	return 0
}

func (m *baseMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time) syscall.Errno {
	defer m.timeit("Write", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var numSlices int
	var delta dirStat
	var attr Attr
	st := m.en.doWrite(ctx, inode, indx, off, slice, mtime, &numSlices, &delta, &attr)
	if st == 0 {
		m.updateParentStat(ctx, inode, attr.Parent, delta.length, delta.space)
		if delta.space != 0 {
			m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, delta.space, 0)
		}
		if numSlices%100 == 99 || numSlices > 350 {
			if numSlices < maxSlices {
				go m.compactChunk(inode, indx, false, false)
			} else {
				m.compactChunk(inode, indx, true, false)
			}
		}
	}
	return st
}

func (m *baseMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr, skipPermCheck bool) syscall.Errno {
	defer m.timeit("Truncate", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, invalidateAllChunks) }()
	if attr == nil {
		attr = &Attr{}
	}
	var delta dirStat
	st := m.en.doTruncate(ctx, inode, flags, length, &delta, attr, skipPermCheck)
	if st == 0 {
		m.updateParentStat(ctx, inode, attr.Parent, delta.length, delta.space)
		if delta.space != 0 {
			m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, delta.space, 0)
		}
	}
	return st
}

func (m *baseMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, flength *uint64) syscall.Errno {
	if mode&fallocCollapesRange != 0 && mode != fallocCollapesRange {
		return syscall.EINVAL
	}
	if mode&fallocInsertRange != 0 && mode != fallocInsertRange {
		return syscall.EINVAL
	}
	if mode == fallocInsertRange || mode == fallocCollapesRange {
		return syscall.ENOTSUP
	}
	if mode&fallocPunchHole != 0 && mode&fallocKeepSize == 0 {
		return syscall.EINVAL
	}
	if size == 0 {
		return syscall.EINVAL
	}
	defer m.timeit("Fallocate", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, invalidateAllChunks) }()
	var delta dirStat
	var attr Attr
	st := m.en.doFallocate(ctx, inode, mode, off, size, &delta, &attr)
	if st == 0 {
		if flength != nil {
			*flength = attr.Length
		}
		m.updateParentStat(ctx, inode, attr.Parent, delta.length, delta.space)
		if delta.space != 0 {
			m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, delta.space, 0)
		}
	}
	return st
}

func (m *baseMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) (rerr syscall.Errno) {
	var attr Attr
	defer func() {
		if rerr == 0 {
			m.touchAtime(ctx, inode, &attr)
		}
	}()
	inode = m.checkRoot(inode)
	if err := m.GetAttr(ctx, inode, &attr); err != 0 {
		return err
	}
	defer m.timeit("Readdir", time.Now())
	var mmask uint8 = MODE_MASK_R
	if plus != 0 {
		mmask |= MODE_MASK_X
	}
	if st := m.Access(ctx, inode, mmask, &attr); st != 0 {
		return st
	}
	if inode == m.root {
		attr.Parent = m.root
	}
	*entries = []*Entry{
		{
			Inode: inode,
			Name:  []byte("."),
			Attr:  &Attr{Typ: TypeDirectory},
		},
	}
	*entries = append(*entries, &Entry{
		Inode: attr.Parent,
		Name:  []byte(".."),
		Attr:  &Attr{Typ: TypeDirectory},
	})
	st := m.en.doReaddir(ctx, inode, plus, entries, -1)
	if st == syscall.ENOENT && inode == TrashInode {
		st = 0
	}
	return st
}

func (m *baseMeta) SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.EINVAL
	}
	switch flags {
	case 0, XattrCreate, XattrReplace:
	default:
		return syscall.EINVAL
	}

	defer m.timeit("SetXattr", time.Now())
	return m.en.doSetXattr(ctx, m.checkRoot(inode), name, value, flags)
}

func (m *baseMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.EINVAL
	}

	defer m.timeit("RemoveXattr", time.Now())
	return m.en.doRemoveXattr(ctx, m.checkRoot(inode), name)
}

func (m *baseMeta) GetParents(ctx Context, inode Ino) map[Ino]int {
	if inode == RootInode || inode == TrashInode {
		return map[Ino]int{1: 1}
	}
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		logger.Warnf("GetAttr inode %d: %s", inode, st)
		return nil
	}
	if attr.Parent > 0 {
		return map[Ino]int{attr.Parent: 1}
	} else {
		return m.en.doGetParents(ctx, inode)
	}
}

func (m *baseMeta) GetPaths(ctx Context, inode Ino) []string {
	if inode == RootInode {
		return []string{"/"}
	}

	if inode == TrashInode {
		return []string{"/.trash"}
	}

	outside := "path not shown because it's outside of the mounted root"
	getDirPath := func(ino Ino) (string, error) {
		var names []string
		var attr Attr
		for ino != RootInode && ino != m.root {
			if st := m.en.doGetAttr(ctx, ino, &attr); st != 0 {
				return "", fmt.Errorf("getattr inode %d: %s", ino, st)
			}
			if attr.Typ != TypeDirectory {
				return "", fmt.Errorf("inode %d is not a directory", ino)
			}
			var entries []*Entry
			if st := m.en.doReaddir(ctx, attr.Parent, 0, &entries, -1); st != 0 {
				return "", fmt.Errorf("readdir inode %d: %s", ino, st)
			}
			var name string
			for _, e := range entries {
				if e.Inode == ino {
					name = string(e.Name)
					break
				}
			}
			if attr.Parent == RootInode && ino == TrashInode {
				name = TrashName
			}
			if name == "" {
				return "", fmt.Errorf("entry %d/%d not found", attr.Parent, ino)
			}
			names = append(names, name)
			ino = attr.Parent
		}
		if m.root != RootInode && ino == RootInode {
			return outside, nil
		}
		names = append(names, "/") // add root

		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 { // reverse
			names[i], names[j] = names[j], names[i]
		}
		return path.Join(names...), nil
	}

	var paths []string
	// inode != RootInode, parent is the real parent inode
	for parent, count := range m.GetParents(ctx, inode) {
		if count <= 0 {
			continue
		}
		dir, err := getDirPath(parent)
		if err != nil {
			logger.Warnf("Get directory path of %d: %s", parent, err)
			continue
		} else if dir == outside {
			paths = append(paths, outside)
			continue
		}
		var entries []*Entry
		if st := m.en.doReaddir(ctx, parent, 0, &entries, -1); st != 0 {
			logger.Warnf("Readdir inode %d: %s", parent, st)
			continue
		}
		var c int
		for _, e := range entries {
			if e.Inode == inode {
				c++
				paths = append(paths, path.Join(dir, string(e.Name)))
			}
		}
		if c != count {
			logger.Warnf("Expect to find %d entries under parent %d, but got %d", count, parent, c)
		}
	}
	return paths
}

func (m *baseMeta) countDirNlink(ctx Context, inode Ino) (uint32, syscall.Errno) {
	var entries []*Entry
	if st := m.en.doReaddir(ctx, inode, 0, &entries, -1); st != 0 {
		return 0, st
	}
	var dirCounter uint32 = 2
	for _, e := range entries {
		if e.Attr.Typ == TypeDirectory {
			dirCounter++
		}
	}
	return dirCounter, 0
}

type metaWalkFunc func(ctx Context, inode Ino, p string, attr *Attr)

func (m *baseMeta) walk(ctx Context, inode Ino, p string, attr *Attr, walkFn metaWalkFunc) syscall.Errno {
	walkFn(ctx, inode, p, attr)
	if attr.Full && attr.Typ != TypeDirectory {
		return 0
	}
	var entries []*Entry
	st := m.en.doReaddir(ctx, inode, 1, &entries, -1)
	if st != 0 && st != syscall.ENOENT {
		logger.Errorf("list %s: %s", p, st)
		return st
	}
	for _, entry := range entries {
		if ctx.Canceled() {
			return syscall.EINTR
		}
		if !entry.Attr.Full {
			entry.Attr.Parent = inode
		}
		if st := m.walk(ctx, entry.Inode, path.Join(p, string(entry.Name)), entry.Attr, walkFn); st != 0 {
			return st
		}
	}
	return 0
}

func (m *baseMeta) Check(ctx Context, fpath string, repair bool, recursive bool, statAll bool) error {
	var attr Attr
	var inode = RootInode
	var parent = RootInode
	attr.Typ = TypeDirectory
	if fpath == "/" {
		if st := m.GetAttr(ctx, inode, &attr); st != 0 && st != syscall.ENOENT {
			logger.Errorf("GetAttr inode %d: %s", inode, st)
			return st
		}
	} else {
		ps := strings.FieldsFunc(fpath, func(r rune) bool {
			return r == '/'
		})
		for i, name := range ps {
			parent = inode
			if st := m.Lookup(ctx, parent, name, &inode, &attr, false); st != 0 {
				logger.Errorf("Lookup parent %d name %s: %s", parent, name, st)
				return st
			}
			if !attr.Full && i < len(ps)-1 {
				// missing attribute
				p := "/" + path.Join(ps[:i+1]...)
				if attr.Typ != TypeDirectory { // TODO: determine file size?
					logger.Warnf("Attribute of %s (inode %d type %d) is missing and cannot be auto-repaired, please repair it manually or remove it", p, inode, attr.Typ)
				} else {
					logger.Warnf("Attribute of %s (inode %d) is missing, please re-run with '--path %s --repair' to fix it", p, inode, p)
				}
			}
		}
	}
	if !attr.Full {
		attr.Parent = parent
	}

	progress := utils.NewProgress(false)
	defer progress.Done()
	nodeBar := progress.AddCountBar("Checked nodes", 0)

	var hasError bool
	type node struct {
		inode Ino
		path  string
		attr  *Attr
	}
	nodes := make(chan *node, 1000)
	go func() {
		defer close(nodes)
		var count int64
		if recursive {
			if st := m.walk(ctx, inode, fpath, &attr, func(ctx Context, inode Ino, path string, attr *Attr) {
				nodes <- &node{inode, path, attr}
				atomic.AddInt64(&count, 1)
			}); st != 0 {
				hasError = true
				logger.Errorf("Walk %s: %s", fpath, st)
			}
		} else {
			nodes <- &node{inode, fpath, &attr}
			count = 1
		}
		nodeBar.SetTotal(count)
	}()

	format, err := m.Load(false)
	if err != nil {
		return errors.Wrap(err, "load meta format")
	}
	if statAll && !format.DirStats {
		logger.Warn("dir stats is disabled, flag '--sync-dir-stat' will be ignored")
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range nodes {
				inode := e.inode
				path := e.path
				attr := e.attr
				if attr.Typ != TypeDirectory {
					// TODO
					continue
				}

				var attrBroken, statBroken bool
				if attr.Full {
					nlink, st := m.countDirNlink(ctx, inode)
					if st == syscall.ENOENT {
						continue
					}
					if st != 0 {
						hasError = true
						logger.Errorf("Count nlink for inode %d: %s", inode, st)
						continue
					}
					if attr.Nlink != nlink {
						logger.Warnf("nlink of %s should be %d, but got %d", path, nlink, attr.Nlink)
						attrBroken = true
					}
				} else {
					logger.Warnf("attribute of %s is missing", path)
					attrBroken = true
				}

				if attrBroken {
					if repair {
						if !attr.Full {
							now := time.Now().Unix()
							attr.Mode = 0644
							attr.Uid = ctx.Uid()
							attr.Gid = ctx.Gid()
							attr.Atime = now
							attr.Mtime = now
							attr.Ctime = now
							attr.Length = 4 << 10
						}
						if st1 := m.en.doRepair(ctx, inode, attr); st1 == 0 || st1 == syscall.ENOENT {
							logger.Debugf("Path %s (inode %d) is successfully repaired", path, inode)
						} else {
							hasError = true
							logger.Errorf("Repair path %s inode %d: %s", path, inode, st1)
						}
					} else {
						logger.Warnf("Path %s (inode %d) can be repaired, please re-run with '--path %s --repair' to fix it", path, inode, path)
						hasError = true
					}
				}

				if format.DirStats {
					stat, st := m.en.doGetDirStat(ctx, inode, false)
					if st == syscall.ENOENT {
						continue
					}
					if st != 0 {
						hasError = true
						logger.Errorf("get dir stat for inode %d: %v", inode, st)
						continue
					}
					if stat == nil || stat.space < 0 || stat.inodes < 0 {
						logger.Warnf("usage stat of %s is missing or broken", path)
						statBroken = true
					}

					if !repair && statAll {
						s, st := m.calcDirStat(ctx, inode)
						if st != 0 {
							hasError = true
							logger.Errorf("calc dir stat for inode %d: %v", inode, st)
							continue
						}
						if stat.space != s.space || stat.inodes != s.inodes {
							logger.Warnf("usage stat of %s should be %v, but got %v", path, s, stat)
							statBroken = true
						}
					}

					if repair {
						if statBroken || statAll {
							if _, st := m.en.doSyncDirStat(ctx, inode); st == 0 || st == syscall.ENOENT {
								logger.Debugf("Stat of path %s (inode %d) is successfully synced", path, inode)
							} else {
								hasError = true
								logger.Errorf("Sync stat of path %s inode %d: %s", path, inode, st)
							}
						}
					} else if statBroken {
						logger.Warnf("Stat of path %s (inode %d) should be synced, please re-run with '--path %s --repair --sync-dir-stat' to fix it", path, inode, path)
						hasError = true
					}
				}
				nodeBar.Increment()
			}
		}()
	}
	wg.Wait()
	if fpath == "/" && repair && recursive && statAll {
		if err := m.syncVolumeStat(ctx); err != nil {
			logger.Errorf("Sync used space: %s", err)
			hasError = true
		}
	}
	if hasError {
		return errors.New("some errors occurred, please check the log of fsck")
	}

	if progress.Quiet {
		logger.Infof("Checked %d nodes", nodeBar.Current())
	}

	return nil
}

func (m *baseMeta) Chroot(ctx Context, subdir string) syscall.Errno {
	for subdir != "" {
		ps := strings.SplitN(subdir, "/", 2)
		if ps[0] != "" {
			var attr Attr
			var inode Ino
			r := m.Lookup(ctx, m.root, ps[0], &inode, &attr, true)
			if r == syscall.ENOENT {
				r = m.Mkdir(ctx, m.root, ps[0], 0777, 0, 0, &inode, &attr)
			}
			if r != 0 {
				return r
			}
			if attr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			m.chroot(inode)
		}
		if len(ps) == 1 {
			break
		}
		subdir = ps[1]
	}
	return 0
}

func (m *baseMeta) chroot(inode Ino) {
	m.root = inode
}

func (m *baseMeta) resolve(ctx Context, dpath string, inode *Ino, create bool) syscall.Errno {
	var attr Attr
	*inode = RootInode
	umask := utils.GetUmask()
	for dpath != "" {
		ps := strings.SplitN(dpath, "/", 2)
		if ps[0] != "" {
			r := m.en.doLookup(ctx, *inode, ps[0], inode, &attr)
			if errors.Is(r, syscall.ENOENT) && create {
				r = m.Mkdir(ctx, *inode, ps[0], 0777, uint16(umask), 0, inode, &attr)
			}
			if r != 0 {
				return r
			}
			if attr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
		}
		if len(ps) == 1 {
			break
		}
		dpath = ps[1]
	}
	return 0
}

func (m *baseMeta) getFormat() *Format {
	m.Lock()
	defer m.Unlock()
	return m.fmt
}

func (m *baseMeta) GetFormat() Format {
	return *m.getFormat()
}

func (m *baseMeta) CompactAll(ctx Context, threads int, bar *utils.Bar) syscall.Errno {
	var wg sync.WaitGroup
	ch := make(chan cchunk, 1000000)
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			for c := range ch {
				logger.Debugf("Compacting chunk %d:%d (%d slices)", c.inode, c.indx, c.slices)
				m.compactChunk(c.inode, c.indx, false, true)
				bar.Increment()
			}
			wg.Done()
		}()
	}

	err := m.en.scanAllChunks(ctx, ch, bar)
	close(ch)
	wg.Wait()
	if err != nil {
		logger.Warnf("Scan chunks: %s", err)
		return errno(err)
	}
	return 0
}

func (m *baseMeta) compactChunk(inode Ino, indx uint32, once, force bool) {
	// avoid too many or duplicated compaction
	k := uint64(inode) + (uint64(indx) << 40)
	m.Lock()
	if m.sessCtx != nil && m.sessCtx.Canceled() {
		m.Unlock()
		return
	}
	if once || force {
		for m.compacting[k] {
			m.Unlock()
			time.Sleep(time.Millisecond * 10)
			m.Lock()
		}
	} else if len(m.compacting) > 10 || m.compacting[k] {
		m.Unlock()
		return
	}
	m.compacting[k] = true
	m.Unlock()
	defer func() {
		m.Lock()
		delete(m.compacting, k)
		m.Unlock()
	}()

	ss, st := m.en.doRead(Background(), inode, indx)
	if st != 0 {
		return
	}
	if ss == nil {
		logger.Errorf("Corrupt value for inode %d chunk indx %d", inode, indx)
		return
	}
	if once && len(ss) < maxSlices {
		return
	}
	if len(ss) > maxCompactSlices {
		ss = ss[:maxCompactSlices]
	}
	skipped := skipSome(ss)
	compacted := ss[skipped:]
	pos, size, slices := compactChunk(compacted)
	if len(compacted) < 2 || size == 0 {
		return
	}
	for _, s := range ss[:skipped] {
		if pos+size > s.pos && s.pos+s.len > pos {
			var sstring string
			for _, s := range ss {
				sstring += fmt.Sprintf("\n%+v", *s)
			}
			panic(fmt.Sprintf("invalid compaction skipped %d, pos %d, size %d; slices: %s", skipped, pos, size, sstring))
		}
	}

	var id uint64
	if st = m.NewSlice(Background(), &id); st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(compacted), size)
	err := m.newMsg(CompactChunk, slices, id)
	if err != nil {
		if !strings.Contains(err.Error(), "not exist") && !strings.Contains(err.Error(), "not found") {
			logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(compacted), err)
		}
		return
	}

	var dsbuf []byte
	trash := m.toTrash(0)
	if trash {
		dsbuf = make([]byte, 0, len(compacted)*12)
		for _, s := range compacted {
			if s.id > 0 {
				dsbuf = append(dsbuf, m.encodeDelayedSlice(s.id, s.size)...)
			}
		}
	}
	origin := make([]byte, 0, len(ss)*sliceBytes)
	for _, s := range ss {
		origin = append(origin, marshalSlice(s.pos, s.id, s.size, s.off, s.len)...)
	}
	st = m.en.doCompactChunk(inode, indx, origin, compacted, skipped, pos, id, size, dsbuf)
	if st == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, id, size)
		m.deleteSlice(id, size)
	} else if st == 0 {
		m.of.InvalidateChunk(inode, indx)
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}

	if force {
		m.Lock()
		delete(m.compacting, k)
		m.Unlock()
		m.compactChunk(inode, indx, once, force)
	}
}

func (m *baseMeta) Compact(ctx Context, inode Ino, concurrency int, preFunc, postFunc func()) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		logger.Errorf("get attr error [inode %v]: %v", inode, st)
		return st
	}

	var wg sync.WaitGroup
	// compact
	chunkChan := make(chan cchunk, 10000)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range chunkChan {
				m.compactChunk(c.inode, c.indx, false, true)
				postFunc()
				if ctx.Canceled() {
					return
				}
			}
		}()
	}

	// scan
	st := m.walk(ctx, inode, "", &attr, func(ctx Context, fIno Ino, path string, fAttr *Attr) {
		if fAttr.Typ != TypeFile {
			return
		}
		// calc chunk index in local
		chunkCnt := uint32((fAttr.Length + ChunkSize - 1) / ChunkSize)
		for i := uint32(0); i < chunkCnt; i++ {
			select {
			case <-ctx.Done():
				return
			case chunkChan <- cchunk{inode: fIno, indx: i}:
				preFunc()
			}
		}
	})

	// finish
	close(chunkChan)
	wg.Wait()

	if st != 0 {
		logger.Errorf("walk error [inode %v]: %v", inode, st)
	}
	return st
}

func (m *baseMeta) fileDeleted(opened, force bool, inode Ino, length uint64) {
	if opened {
		m.Lock()
		m.removedFiles[inode] = true
		m.Unlock()
	} else {
		m.tryDeleteFileData(inode, length, force)
	}
}

func (m *baseMeta) tryDeleteFileData(inode Ino, length uint64, force bool) {
	if force {
		m.maxDeleting <- struct{}{}
	} else {
		select {
		case m.maxDeleting <- struct{}{}:
		default:
			return // will be cleanup later
		}
	}
	go func() {
		m.en.doDeleteFileData(inode, length)
		<-m.maxDeleting
	}()
}

func (m *baseMeta) deleteSlice_(id uint64, size uint32) {
	if err := m.newMsg(DeleteSlice, id, size); err != nil {
		logger.Warnf("Delete data blocks of slice %d (%d bytes): %s", id, size, err)
		return
	}
	if err := m.en.doDeleteSlice(id, size); err != nil {
		logger.Errorf("Delete meta entry of slice %d (%d bytes): %s", id, size, err)
	}
}

func (m *baseMeta) deleteSlice(id uint64, size uint32) {
	if id == 0 || m.conf.MaxDeletes == 0 {
		return
	}
	m.dSliceMu.Lock()
	if m.dslices == nil {
		m.dSliceMu.Unlock()
		m.deleteSlice_(id, size)
		return
	}
	select {
	case <-m.sessCtx.Done():
	case m.dslices <- Slice{Id: id, Size: size}:
	}
	m.dSliceMu.Unlock()
}

func (m *baseMeta) toTrash(parent Ino) bool {
	if parent.IsTrash() {
		return false
	}
	return m.getFormat().TrashDays > 0
}

func (m *baseMeta) checkTrash(parent Ino, trash *Ino) syscall.Errno {
	if !m.toTrash(parent) {
		return 0
	}
	name := time.Now().UTC().Format("2006-01-02-15")
	m.Lock()
	defer m.Unlock()
	if name == m.subTrash.name {
		*trash = m.subTrash.inode
		return 0
	}
	m.Unlock()

	st := m.en.doLookup(Background(), TrashInode, name, trash, nil)
	if st == syscall.ENOENT {
		attr := Attr{Typ: TypeDirectory, Nlink: 2, Length: 4 << 10, Parent: TrashInode, Full: true}
		st = m.en.doMknod(Background(), TrashInode, name, TypeDirectory, 0555, 0, "", trash, &attr)
		m.en.updateStats(align4K(0), 1)
	}

	m.Lock()
	if st != 0 && st != syscall.EEXIST {
		logger.Warnf("create subTrash %s: %s", name, st)
	} else if *trash <= TrashInode {
		logger.Warnf("invalid trash inode: %d", *trash)
		st = syscall.EBADF
	} else {
		m.subTrash.inode = *trash
		m.subTrash.name = name
		st = 0
	}
	return st
}

func (m *baseMeta) trashEntry(parent, inode Ino, name string) string {
	s := fmt.Sprintf("%d-%d-%s", parent, inode, name)
	if len(s) > MaxName {
		s = s[:MaxName]
		logger.Warnf("File name is too long as a trash entry, truncating it: %s -> %s", name, s)
	}
	return s
}

func (m *baseMeta) cleanupTrash(ctx Context) {
	defer m.sessWG.Done()
	var cCtx Context
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(utils.JitterIt(time.Hour)):
		}
		if st := m.en.doGetAttr(ctx, TrashInode, nil); st != 0 {
			if st != syscall.ENOENT {
				logger.Warnf("getattr inode %d: %s", TrashInode, st)
			}
			continue
		}
		if ok, err := m.en.setIfSmall("lastCleanupTrash", time.Now().Unix(), int64(time.Hour.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter lastCleanupTrash: %s", err)
		} else if ok {
			if cCtx != nil {
				cCtx.Cancel()
				cCtx = WrapWithTimeout(ctx, 50*time.Minute)
			}
			days := m.getFormat().TrashDays
			go m.doCleanupTrash(cCtx, days, false)
			go m.cleanupDelayedSlices(cCtx, days)
		}
	}
}

func (m *baseMeta) CleanupDetachedNodesBefore(ctx Context, edge time.Time, increProgress func()) {
	for _, inode := range m.en.doFindDetachedNodes(edge) {
		if eno := m.en.doCleanupDetachedNode(Background(), inode); eno != 0 {
			logger.Errorf("cleanupDetachedNode: remove detached tree (%d) error: %s", inode, eno)
		} else {
			if increProgress != nil {
				increProgress()
			}
		}
	}
}

func (m *baseMeta) CleanupTrashBefore(ctx Context, edge time.Time, increProgress func(int)) {
	logger.Debugf("cleanup trash: started")
	now := time.Now()
	var st syscall.Errno
	var entries []*Entry
	if st = m.en.doReaddir(ctx, TrashInode, 0, &entries, -1); st != 0 {
		logger.Warnf("readdir trash %d: %s", TrashInode, st)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Inode < entries[j].Inode })
	var count int
	defer func() {
		if count > 0 {
			logger.Infof("cleanup trash: deleted %d files in %v", count, time.Since(now))
		} else {
			logger.Debugf("cleanup trash: nothing to delete")
		}
	}()
	batch := 1000000
	for len(entries) > 0 {
		if ctx.Canceled() {
			return
		}
		e := entries[0]
		ts, err := time.Parse("2006-01-02-15", string(e.Name))
		if err != nil {
			logger.Warnf("bad entry as a subTrash: %s", e.Name)
			entries = entries[1:]
			continue
		}
		if ts.Before(edge) {
			var subEntries []*Entry
			if st = m.en.doReaddir(ctx, e.Inode, 0, &subEntries, batch); st != 0 {
				logger.Warnf("readdir subTrash %d: %s", e.Inode, st)
				entries = entries[1:]
				continue
			}
			rmdir := len(subEntries) < batch
			if rmdir {
				entries = entries[1:]
			}
			for _, se := range subEntries {
				var c uint64
				st = m.Remove(ctx, e.Inode, string(se.Name), false, m.conf.MaxDeletes, &c)
				if st == 0 {
					count += int(c)
					if increProgress != nil {
						increProgress(int(c))
					}
				} else {
					logger.Warnf("delete from trash %s/%s: %s", e.Name, se.Name, st)
					rmdir = false
					continue
				}
				if ctx.Canceled() {
					return
				}
			}
			if rmdir {
				if st = m.en.doRmdir(ctx, TrashInode, string(e.Name), nil, nil); st != 0 {
					logger.Warnf("rmdir subTrash %s: %s", e.Name, st)
				}
			}
		} else {
			break
		}
	}
}

func (m *baseMeta) scanTrashEntry(ctx Context, scan func(inode Ino, size uint64)) error {
	var st syscall.Errno
	var entries []*Entry
	if st = m.en.doReaddir(ctx, TrashInode, 1, &entries, -1); st != 0 {
		return errors.Wrap(st, "read trash")
	}

	var subEntries []*Entry
	for _, entry := range entries {
		scan(entry.Inode, entry.Attr.Length)
		subEntries = subEntries[:0]
		if st = m.en.doReaddir(ctx, entry.Inode, 1, &subEntries, -1); st != 0 {
			logger.Warnf("readdir subEntry %d: %s", entry.Inode, st)
			continue
		}
		for _, se := range subEntries {
			scan(se.Inode, se.Attr.Length)
		}
	}
	return nil
}

func (m *baseMeta) scanTrashFiles(ctx Context, scan trashFileScan) error {
	var st syscall.Errno
	var entries []*Entry
	if st = m.en.doReaddir(ctx, TrashInode, 1, &entries, -1); st != 0 {
		return errors.Wrap(st, "read trash")
	}

	var subEntries []*Entry
	for _, entry := range entries {
		ts, err := time.Parse("2006-01-02-15", string(entry.Name))
		if err != nil {
			logger.Warnf("bad entry as a subTrash: %s", entry.Name)
			continue
		}
		subEntries = subEntries[:0]
		if st = m.en.doReaddir(ctx, entry.Inode, 1, &subEntries, -1); st != 0 {
			logger.Warnf("readdir subEntry %d: %s", entry.Inode, st)
			continue
		}
		for _, se := range subEntries {
			if se.Attr.Typ == TypeFile {
				clean, err := scan(se.Inode, se.Attr.Length, ts)
				if err != nil {
					return errors.Wrap(err, "scan trash files")
				}
				if clean {
					// TODO: m.en.doUnlink(ctx, entry.Attr.Parent, string(entry.Name))
					// avoid lint warning
					_ = clean
				}
			}
		}
	}
	return nil
}

func (m *baseMeta) doCleanupTrash(ctx Context, days int, force bool) {
	edge := time.Now().Add(-time.Duration(24*days+2) * time.Hour)
	if force {
		edge = time.Now()
	}
	m.CleanupTrashBefore(ctx, edge, nil)
}

func (m *baseMeta) cleanupDelayedSlices(ctx Context, days int) {
	now := time.Now()
	edge := now.Unix() - int64(days)*24*3600
	logger.Debugf("Cleanup delayed slices: started with edge %d", edge)
	if count, err := m.en.doCleanupDelayedSlices(ctx, edge); err != nil {
		logger.Warnf("Cleanup delayed slices: deleted %d slices in %v, but got error: %s", count, time.Since(now), err)
	} else if count > 0 {
		logger.Infof("Cleanup delayed slices: deleted %d slices in %v", count, time.Since(now))
	}
}

func (m *baseMeta) ScanDeletedObject(ctx Context, tss trashSliceScan, pss pendingSliceScan, tfs trashFileScan, pfs pendingFileScan) error {
	eg := errgroup.Group{}
	if tss != nil {
		eg.Go(func() error {
			return m.en.scanTrashSlices(ctx, tss)
		})
	}
	if pss != nil {
		eg.Go(func() error {
			return m.en.scanPendingSlices(ctx, pss)
		})
	}
	if tfs != nil {
		eg.Go(func() error {
			return m.scanTrashFiles(ctx, tfs)
		})
	}
	if pfs != nil {
		eg.Go(func() error {
			concurrency := m.conf.MaxDeletes
			cleanChan := make(chan struct {
				ino  Ino
				size uint64
			}, concurrency)
			var wg sync.WaitGroup

			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for p := range cleanChan {
						m.en.doDeleteFileData(p.ino, p.size)
					}
				}()
			}

			cpfs := func(ino Ino, size uint64, ts int64) (bool, error) {
				clean, err := pfs(ino, size, ts)
				if err != nil {
					return false, err
				}
				if clean {
					cleanChan <- struct {
						ino  Ino
						size uint64
					}{ino, size}
				}
				return clean, nil
			}

			err := m.en.scanPendingFiles(ctx, cpfs)
			close(cleanChan)
			wg.Wait()
			return err
		})
	}
	return eg.Wait()
}

func (m *baseMeta) Clone(ctx Context, srcParentIno, srcIno, parent Ino, name string, cmode uint8, cumask uint16, count, total *uint64) syscall.Errno {

	if srcIno.IsTrash() || srcParentIno.IsTrash() || parent.IsTrash() || (parent == RootInode && name == TrashName) {
		return syscall.EPERM
	}

	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.ENOENT
	}

	defer m.timeit("Clone", time.Now())
	parent = m.checkRoot(parent)

	var attr Attr
	var eno syscall.Errno
	if eno = m.en.doGetAttr(ctx, srcIno, &attr); eno != 0 {
		return eno
	}
	if eno = m.Access(ctx, srcIno, MODE_MASK_R, &attr); eno != 0 {
		return eno
	}
	if eno = m.Access(ctx, parent, MODE_MASK_X|MODE_MASK_W, nil); eno != 0 {
		return eno
	}
	var dstIno Ino
	var _a Attr
	if eno = m.en.doLookup(ctx, parent, name, &dstIno, &_a); eno == 0 {
		return syscall.EEXIST
	} else if eno != syscall.ENOENT {
		return eno
	}
	var sum Summary
	eno = m.GetSummary(ctx, srcIno, &sum, true, false)
	if eno != 0 {
		return eno
	}
	if err := m.checkQuota(ctx, int64(sum.Size), int64(sum.Dirs)+int64(sum.Files), ctx.Uid(), ctx.Gid(), parent); err != 0 {
		return err
	}
	*total = sum.Dirs + sum.Files
	concurrent := make(chan struct{}, 4)
	if attr.Typ == TypeDirectory {
		eno = m.cloneEntry(ctx, srcIno, parent, name, &dstIno, cmode, cumask, count, true, concurrent)
		if eno == 0 {
			eno = m.en.doAttachDirNode(ctx, parent, dstIno, name)
		}
		if eno != 0 && dstIno != 0 {
			if eno := m.en.doCleanupDetachedNode(ctx, dstIno); eno != 0 {
				logger.Errorf("remove detached tree (%d): %s", dstIno, eno)
			}
		}
	} else {
		eno = m.cloneEntry(ctx, srcIno, parent, name, nil, cmode, cumask, count, true, concurrent)
	}
	if eno == 0 {
		m.updateDirStat(ctx, parent, int64(attr.Length), align4K(attr.Length), 1)
		m.updateDirQuota(ctx, parent, int64(sum.Size), int64(sum.Dirs)+int64(sum.Files))
	}
	return eno
}

func (m *baseMeta) cloneEntry(ctx Context, srcIno Ino, parent Ino, name string, dstIno *Ino, cmode uint8, cumask uint16, count *uint64, top bool, concurrent chan struct{}) syscall.Errno {
	ino, err := m.nextInode()
	if err != nil {
		return errno(err)
	}
	if dstIno != nil {
		*dstIno = ino
	}
	var attr Attr
	eno := m.en.doCloneEntry(ctx, srcIno, parent, name, ino, &attr, cmode, cumask, top)
	if eno != 0 {
		return eno
	}
	m.en.updateStats(align4K(attr.Length), 1)
	atomic.AddUint64(count, 1)
	m.updateUserGroupQuota(ctx, attr.Uid, attr.Gid, align4K(attr.Length), 1)
	if attr.Typ != TypeDirectory {
		return 0
	}
	if eno = m.Access(ctx, srcIno, MODE_MASK_R|MODE_MASK_X, &attr); eno != 0 {
		return eno
	}
	var entries []*Entry
	eno = m.en.doReaddir(ctx, srcIno, 0, &entries, -1)
	if eno == syscall.ENOENT {
		eno = 0 // empty dir
	}
	if eno != 0 {
		return eno
	}
	// try directories first to increase parallel
	var dirs int
	for i, e := range entries {
		if e.Attr.Typ == TypeDirectory {
			entries[dirs], entries[i] = entries[i], entries[dirs]
			dirs++
		}
	}

	var wg sync.WaitGroup
	var skipped uint32
	var errCh = make(chan syscall.Errno, cap(concurrent))
	cloneChild := func(e *Entry) syscall.Errno {
		eno := m.cloneEntry(ctx, e.Inode, ino, string(e.Name), nil, cmode, cumask, count, false, concurrent)
		if eno == syscall.ENOENT {
			logger.Warnf("ignore deleted %s in dir %d", string(e.Name), srcIno)
			if e.Attr.Typ == TypeDirectory {
				atomic.AddUint32(&skipped, 1)
			}
			eno = 0
		}
		return eno
	}
LOOP:
	for i, entry := range entries {
		select {
		case e := <-errCh:
			eno = e
			ctx.Cancel()
			break LOOP
		case concurrent <- struct{}{}:
			wg.Add(1)
			go func(e *Entry) {
				defer wg.Done()
				eno := cloneChild(e)
				if eno != 0 {
					errCh <- eno
				}
				<-concurrent
			}(entry)
		default:
			if e := cloneChild(entry); e != 0 {
				eno = e
				break LOOP
			}
		}
		entries[i] = nil // release memory
		if ctx.Canceled() {
			eno = syscall.EINTR
			break
		}
	}
	wg.Wait()
	if eno == 0 {
		select {
		case eno = <-errCh:
		default:
		}
	}
	if eno == 0 && skipped > 0 {
		attr.Nlink -= skipped
		if eno := m.en.doRepair(ctx, ino, &attr); eno != 0 {
			logger.Warnf("fix nlink of %d: %s", ino, eno)
		}
	}
	return eno
}

func (m *baseMeta) mergeAttr(ctx Context, inode Ino, set uint16, cur, attr *Attr, now time.Time, rule *aclAPI.Rule) (*Attr, syscall.Errno) {
	dirtyAttr := *cur
	if (set&(SetAttrUID|SetAttrGID)) != 0 && (set&SetAttrMode) != 0 {
		attr.Mode |= (cur.Mode & 06000)
	}
	var changed bool
	if (cur.Mode&06000) != 0 && (set&(SetAttrUID|SetAttrGID)) != 0 {
		clearSUGID(ctx, &dirtyAttr, attr)
		changed = true
	}
	if set&SetAttrGID != 0 {
		if ctx.Uid() != 0 && ctx.Uid() != cur.Uid {
			return nil, syscall.EPERM
		}
		if cur.Gid != attr.Gid {
			if ctx.CheckPermission() && ctx.Uid() != 0 && !containsGid(ctx, attr.Gid) {
				return nil, syscall.EPERM
			}
			dirtyAttr.Gid = attr.Gid
			changed = true
		}
	}
	if set&SetAttrUID != 0 && cur.Uid != attr.Uid {
		if ctx.CheckPermission() && ctx.Uid() != 0 {
			return nil, syscall.EPERM
		}
		dirtyAttr.Uid = attr.Uid
		changed = true
	}
	if set&SetAttrMode != 0 {
		if ctx.Uid() != 0 && (attr.Mode&02000) != 0 {
			if ctx.Gid() != cur.Gid {
				attr.Mode &= 05777
			}
		}

		if rule != nil {
			rule.SetMode(attr.Mode)
			dirtyAttr.Mode = attr.Mode&07000 | rule.GetMode()
			changed = true
		} else if attr.Mode != cur.Mode {
			if ctx.Uid() != 0 && ctx.Uid() != cur.Uid &&
				(cur.Mode&01777 != attr.Mode&01777 || attr.Mode&02000 > cur.Mode&02000 || attr.Mode&04000 > cur.Mode&04000) {
				return nil, syscall.EPERM
			}
			dirtyAttr.Mode = attr.Mode
			changed = true
		}
	}
	if set&SetAttrAtimeNow != 0 || (set&SetAttrAtime) != 0 && attr.Atime < 0 {
		if st := m.Access(ctx, inode, MODE_MASK_W, cur); ctx.Uid() != cur.Uid && st != 0 {
			return nil, syscall.EACCES
		}
		dirtyAttr.Atime = now.Unix()
		dirtyAttr.Atimensec = uint32(now.Nanosecond())
		changed = true
	} else if set&SetAttrAtime != 0 && (cur.Atime != attr.Atime || cur.Atimensec != attr.Atimensec) {
		if cur.Uid == 0 && ctx.Uid() != 0 {
			return nil, syscall.EPERM
		}
		if st := m.Access(ctx, inode, MODE_MASK_W, cur); ctx.Uid() != cur.Uid && st != 0 {
			return nil, syscall.EACCES
		}
		dirtyAttr.Atime = attr.Atime
		dirtyAttr.Atimensec = attr.Atimensec
		changed = true
	}
	if set&SetAttrMtimeNow != 0 || (set&SetAttrMtime) != 0 && attr.Mtime < 0 {
		if st := m.Access(ctx, inode, MODE_MASK_W, cur); ctx.Uid() != cur.Uid && st != 0 {
			return nil, syscall.EACCES
		}
		dirtyAttr.Mtime = now.Unix()
		dirtyAttr.Mtimensec = uint32(now.Nanosecond())
		changed = true
	} else if set&SetAttrMtime != 0 && (cur.Mtime != attr.Mtime || cur.Mtimensec != attr.Mtimensec) {
		if cur.Uid == 0 && ctx.Uid() != 0 {
			return nil, syscall.EPERM
		}
		if st := m.Access(ctx, inode, MODE_MASK_W, cur); ctx.Uid() != cur.Uid && st != 0 {
			return nil, syscall.EACCES
		}
		dirtyAttr.Mtime = attr.Mtime
		dirtyAttr.Mtimensec = attr.Mtimensec
		changed = true
	}
	if set&SetAttrFlag != 0 {
		dirtyAttr.Flags = attr.Flags
		changed = true
	}
	if !changed {
		*attr = *cur
		return nil, 0
	}
	return &dirtyAttr, 0
}

func (m *baseMeta) CheckSetAttr(ctx Context, inode Ino, set uint16, attr Attr) syscall.Errno {
	var cur Attr
	inode = m.checkRoot(inode)
	if st := m.en.doGetAttr(ctx, inode, &cur); st != 0 {
		return st
	}
	_, st := m.mergeAttr(ctx, inode, set, &cur, &attr, time.Now(), nil)
	return st
}

var errACLNotInCache = errors.New("acl not in cache")

func (m *baseMeta) getFaclFromCache(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) error {
	ino = m.checkRoot(ino)
	cAttr := &Attr{}
	if m.conf.OpenCache > 0 && m.of.Check(ino, cAttr) {
		aclId := getAttrACLId(cAttr, aclType)
		if aclId == aclAPI.None {
			return ENOATTR
		}

		if cRule := m.aclCache.Get(aclId); cRule != nil {
			*rule = *cRule
			return nil
		}
	}
	return errACLNotInCache
}

func setAttrACLId(attr *Attr, aclType uint8, id uint32) {
	switch aclType {
	case aclAPI.TypeAccess:
		attr.AccessACL = id
	case aclAPI.TypeDefault:
		attr.DefaultACL = id
	}
}

func getAttrACLId(attr *Attr, aclType uint8) uint32 {
	switch aclType {
	case aclAPI.TypeAccess:
		return attr.AccessACL
	case aclAPI.TypeDefault:
		return attr.DefaultACL
	}
	return aclAPI.None
}

func setXAttrACL(xattrs *[]byte, accessACL, defaultACL uint32) {
	if accessACL != aclAPI.None {
		*xattrs = append(*xattrs, []byte("system.posix_acl_access")...)
		*xattrs = append(*xattrs, 0)
	}
	if defaultACL != aclAPI.None {
		*xattrs = append(*xattrs, []byte("system.posix_acl_default")...)
		*xattrs = append(*xattrs, 0)
	}
}

func (m *baseMeta) saveACL(rule *aclAPI.Rule, aclMaxId *uint32) uint32 {
	if rule == nil {
		return aclAPI.None
	}
	id := m.aclCache.GetId(rule)
	if id == aclAPI.None {
		(*aclMaxId)++
		id = *aclMaxId
		m.aclCache.Put(id, rule)
	}
	return id
}

func (m *baseMeta) SetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno {
	if aclType != aclAPI.TypeAccess && aclType != aclAPI.TypeDefault {
		return syscall.EINVAL
	}

	if !ino.IsNormal() {
		return syscall.EPERM
	}

	now := time.Now()
	defer func() {
		m.timeit("SetFacl", now)
		m.of.InvalidateChunk(ino, invalidateAttrOnly)
	}()

	return m.en.doSetFacl(ctx, ino, aclType, rule)
}

func (m *baseMeta) GetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno {
	var err error
	if err = m.getFaclFromCache(ctx, ino, aclType, rule); err == nil {
		return 0
	}

	if !errors.Is(err, errACLNotInCache) {
		return errno(err)
	}

	now := time.Now()
	defer m.timeit("GetFacl", now)

	return m.en.doGetFacl(ctx, ino, aclType, aclAPI.None, rule)
}

func inGroup(ctx Context, gid uint32) bool {
	for _, egid := range ctx.Gids() {
		if egid == gid {
			return true
		}
	}
	return false
}

type DirHandler interface {
	List(ctx Context, offset int) ([]*Entry, syscall.Errno)
	Insert(inode Ino, name string, attr *Attr)
	Delete(name string)
	Read(offset int)
	Close()
}

func (m *baseMeta) NewDirHandler(ctx Context, inode Ino, plus bool, initEntries []*Entry) (DirHandler, syscall.Errno) {
	var attr Attr
	var st syscall.Errno
	defer func() {
		if st == 0 {
			m.touchAtime(ctx, inode, &attr)
		}
	}()

	inode = m.checkRoot(inode)
	if st = m.GetAttr(ctx, inode, &attr); st != 0 {
		return nil, st
	}
	defer m.timeit("NewDirHandler", time.Now())
	var mmask uint8 = MODE_MASK_R
	if plus {
		mmask |= MODE_MASK_X
	}

	if st = m.Access(ctx, inode, mmask, &attr); st != 0 {
		return nil, st
	}
	if inode == m.root {
		attr.Parent = m.root
	}

	initEntries = append(initEntries, &Entry{
		Inode: inode,
		Name:  []byte("."),
		Attr:  &attr,
	})

	parent := &Entry{
		Inode: attr.Parent,
		Name:  []byte(".."),
		Attr:  &Attr{Typ: TypeDirectory},
	}
	if plus {
		if attr.Parent == inode {
			parent.Attr = &attr
		} else {
			if st := m.GetAttr(ctx, attr.Parent, parent.Attr); st != 0 {
				return nil, st
			}
		}
	}
	initEntries = append(initEntries, parent)

	return m.en.newDirHandler(inode, plus, initEntries), 0
}

type dirBatch struct {
	isEnd   bool
	offset  int
	cursor  interface{}
	entries []*Entry
	indexes map[string]int
}

func (b *dirBatch) contain(offset int) bool {
	if b == nil {
		return false
	}
	return b.offset <= offset && offset < b.offset+len(b.entries) || (len(b.entries) == 0 && b.offset == offset)
}

func (b *dirBatch) predecessor(offset int) bool {
	return b.offset+len(b.entries) == offset
}

type dirFetcher func(ctx Context, inode Ino, cursor interface{}, offset, limit int, plus bool) (interface{}, []*Entry, error)

type dirHandler struct {
	sync.Mutex
	inode       Ino
	plus        bool
	initEntries []*Entry
	batch       *dirBatch
	fetcher     dirFetcher
	readOff     int
	batchNum    int
}

func (h *dirHandler) fetch(ctx Context, offset int) (*dirBatch, error) {
	var cursor interface{}
	if h.batch != nil && h.batch.predecessor(offset) {
		if h.batch.isEnd {
			return h.batch, nil
		}
		cursor = h.batch.cursor
	}
	nextCursor, entries, err := h.fetcher(ctx, h.inode, cursor, offset, h.batchNum, h.plus)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []*Entry{}
		nextCursor = cursor
	}
	indexes := make(map[string]int, len(entries))
	for i, e := range entries {
		indexes[string(e.Name)] = i
	}
	return &dirBatch{isEnd: len(entries) < h.batchNum, offset: offset, cursor: nextCursor, entries: entries, indexes: indexes}, nil
}

func (h *dirHandler) List(ctx Context, offset int) ([]*Entry, syscall.Errno) {
	var prefix []*Entry
	if offset < len(h.initEntries) {
		prefix = h.initEntries[offset:]
		offset = 0
	} else {
		offset -= len(h.initEntries)
	}

	var err error
	h.Lock()
	defer h.Unlock()
	if !h.batch.contain(offset) {
		h.batch, err = h.fetch(ctx, offset)
	}

	if err != nil {
		return nil, errno(err)
	}

	h.readOff = h.batch.offset + len(h.batch.entries)
	if len(prefix) > 0 {
		return append(prefix, h.batch.entries...), 0
	}
	return h.batch.entries[offset-h.batch.offset:], 0
}

func (h *dirHandler) Delete(name string) {
	h.Lock()
	defer h.Unlock()
	if h.batch == nil || len(h.batch.entries) == 0 {
		return
	}

	if idx, ok := h.batch.indexes[name]; ok && idx+h.batch.offset >= h.readOff {
		delete(h.batch.indexes, name)
		n := len(h.batch.entries)
		if idx < n-1 {
			// TODO: sorted
			h.batch.entries[idx] = h.batch.entries[n-1]
			h.batch.indexes[string(h.batch.entries[idx].Name)] = idx
		}
		h.batch.entries = h.batch.entries[:n-1]
	}
}

func (h *dirHandler) Insert(inode Ino, name string, attr *Attr) {
	h.Lock()
	defer h.Unlock()
	if h.batch == nil {
		return
	}
	if h.batch.isEnd || bytes.Compare([]byte(name), h.batch.cursor.([]byte)) < 0 {
		// TODO: sorted
		h.batch.entries = append(h.batch.entries, &Entry{Inode: inode, Name: []byte(name), Attr: attr})
		h.batch.indexes[name] = len(h.batch.entries) - 1
	}
}

func (h *dirHandler) Read(offset int) {
	h.readOff = offset - len(h.initEntries) // TODO: what if fuse only reads one entry?
}

func (h *dirHandler) Close() {
	h.Lock()
	h.batch = nil
	h.readOff = 0
	h.Unlock()
}

func (m *baseMeta) DumpMetaV2(ctx Context, w io.Writer, opt *DumpOption) error {
	opt = opt.check()

	bak := newBakFormat()
	ch := make(chan *dumpedResult, 100)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := m.en.dump(ctx, opt, ch)
		if err != nil {
			logger.Errorf("dump meta err: %v", err)
			ctx.Cancel()
		} else {
			close(ch)
		}
	}()

	var res *dumpedResult
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case res = <-ch:
		}
		if res == nil {
			break
		}
		seg := newBakSegment(res.msg)
		if err := bak.writeSegment(w, seg); err != nil {
			logger.Errorf("write %d err: %v", seg.typ, err)
			ctx.Cancel()
			wg.Wait()
			return err
		}
		if opt.Progress != nil {
			opt.Progress(seg.Name(), int(seg.num()))
		}
		if res.release != nil {
			res.release(res.msg)
		}
	}

	wg.Wait()
	return bak.writeFooter(w)
}

func (m *baseMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	if opt == nil {
		opt = &LoadOption{}
	}
	if err := m.en.prepareLoad(ctx, opt); err != nil {
		return err
	}

	type task struct {
		typ int
		msg proto.Message
	}

	var wg sync.WaitGroup
	taskCh := make(chan *task, 100)

	workerFunc := func(ctx Context, taskCh <-chan *task) {
		defer wg.Done()
		var task *task
		for {
			select {
			case <-ctx.Done():
				return
			case task = <-taskCh:
			}
			if task == nil {
				break
			}
			err := m.en.load(ctx, task.typ, opt, task.msg)
			if err != nil {
				logger.Errorf("failed to insert %d: %s", task.typ, err)
				ctx.Cancel()
				return
			}
		}
	}

	for i := 0; i < opt.Threads; i++ {
		wg.Add(1)
		go workerFunc(ctx, taskCh)
	}

	bak := &BakFormat{}
	for {
		seg, err := bak.ReadSegment(r)
		if err != nil {
			if errors.Is(err, errBakEOF) {
				close(taskCh)
				break
			}
			ctx.Cancel()
			wg.Wait()
			return err
		}

		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case taskCh <- &task{int(seg.typ), seg.val}:
			if opt.Progress != nil {
				opt.Progress(seg.Name(), int(seg.num()))
			}
		}
	}
	wg.Wait()
	return nil
}
