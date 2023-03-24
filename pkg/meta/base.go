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
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
)

const (
	inodeBatch    = 100
	sliceIdBatch  = 1000
	minUpdateTime = time.Millisecond * 10
	nlocks        = 1024
)

type engine interface {
	// Get the value of counter name.
	getCounter(name string) (int64, error)
	// Increase counter name by value. Do not use this if value is 0, use getCounter instead.
	incrCounter(name string, value int64) (int64, error)
	// Set counter name to value if old <= value - diff.
	setIfSmall(name string, value, diff int64) (bool, error)
	updateStats(space int64, inodes int64)
	flushStats()

	doLoad() ([]byte, error)

	doNewSession(sinfo []byte) error
	doRefreshSession() error
	doFindStaleSessions(limit int) ([]uint64, error) // limit < 0 means all
	doCleanStaleSession(sid uint64) error

	scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error
	compactChunk(inode Ino, indx uint32, force bool)
	doDeleteSustainedInode(sid uint64, inode Ino) error
	doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) // limit < 0 means all
	doDeleteFileData(inode Ino, length uint64)
	doCleanupSlices()
	doCleanupDelayedSlices(edge int64) (int, error)
	doDeleteSlice(id uint64, size uint32) error

	doGetQuota(ctx Context, inode Ino) (*Quota, error)
	doSetQuota(ctx Context, inode Ino, quota *Quota, create bool) error
	doDelQuota(ctx Context, inode Ino) error
	doLoadQuotas(ctx Context) (map[Ino]*Quota, error)
	doFlushQuota(ctx Context, inode Ino, space, inodes int64) error

	doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno
	doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno
	doUnlink(ctx Context, parent Ino, name string, attr *Attr, skipCheckTrash ...bool) syscall.Errno
	doRmdir(ctx Context, parent Ino, name string, inode *Ino, skipCheckTrash ...bool) syscall.Errno
	doReadlink(ctx Context, inode Ino) ([]byte, error)
	doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno
	doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno
	doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno
	doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno

	doGetParents(ctx Context, inode Ino) map[Ino]int
	doUpdateDirStat(ctx Context, batch map[Ino]dirStat) error
	// @trySync: try sync dir stat if broken or not existed
	doGetDirStat(ctx Context, ino Ino, trySync bool) (*dirStat, error)
	doSyncDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno)

	scanTrashSlices(Context, trashSliceScan) error
	scanPendingSlices(Context, pendingSliceScan) error
	scanPendingFiles(Context, pendingFileScan) error

	GetSession(sid uint64, detail bool) (*Session, error)
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

// stat of dir
type dirStat struct {
	length int64
	space  int64
	inodes int64
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
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	reloadCb     []func(*Format)
	umounting    bool
	sesMu        sync.Mutex

	dirStatsLock sync.Mutex
	dirStats     map[Ino]dirStat
	*fsStat

	parentMu   sync.Mutex     // protect dirParents
	quotaMu    sync.RWMutex   // protect dirQuotas
	dirParents map[Ino]Ino    // directory inode -> parent inode
	dirQuotas  map[Ino]*Quota // directory inode -> quota

	freeMu     sync.Mutex
	freeInodes freeID
	freeSlices freeID

	usedSpaceG  prometheus.Gauge
	usedInodesG prometheus.Gauge
	txDist      prometheus.Histogram
	txRestart   prometheus.Counter
	opDist      prometheus.Histogram

	en engine
}

func newBaseMeta(addr string, conf *Config) *baseMeta {
	return &baseMeta{
		addr:         utils.RemovePassword(addr),
		conf:         conf,
		root:         RootInode,
		of:           newOpenFiles(conf.OpenCache, conf.OpenCacheLimit),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		maxDeleting:  make(chan struct{}, 100),
		dslices:      make(chan Slice, conf.MaxDeletes*10240),
		symlinks:     &sync.Map{},
		fsStat:       new(fsStat),
		dirStats:     make(map[Ino]dirStat),
		dirParents:   make(map[Ino]Ino),
		dirQuotas:    make(map[Ino]*Quota),
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},

		usedSpaceG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "used_space",
			Help: "Total used space in bytes.",
		}),
		usedInodesG: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "used_inodes",
			Help: "Total number of inodes.",
		}),
		txDist: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "transaction_durations_histogram_seconds",
			Help:    "Transactions latency distributions.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
		}),
		txRestart: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "transaction_restart",
			Help: "The number of times a transaction is restarted.",
		}),
		opDist: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "meta_ops_durations_histogram_seconds",
			Help:    "Operation latency distributions.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
		}),
	}
}

func (m *baseMeta) InitMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(m.usedSpaceG)
	reg.MustRegister(m.usedInodesG)
	reg.MustRegister(m.txDist)
	reg.MustRegister(m.txRestart)
	reg.MustRegister(m.opDist)

	go func() {
		for {
			var totalSpace, availSpace, iused, iavail uint64
			err := m.StatFS(Background, &totalSpace, &availSpace, &iused, &iavail)
			if err == 0 {
				m.usedSpaceG.Set(float64(totalSpace - availSpace))
				m.usedInodesG.Set(float64(iused))
			}
			utils.SleepWithJitter(time.Second * 10)
		}
	}()
}

func (m *baseMeta) timeit(start time.Time) {
	m.opDist.Observe(time.Since(start).Seconds())
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

func (m *baseMeta) parallelSyncDirStat(ctx Context, inos map[Ino]bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	for i := range inos {
		wg.Add(1)
		go func(ino Ino) {
			defer wg.Done()
			_, st := m.en.doSyncDirStat(ctx, ino)
			if st != 0 {
				logger.Warnf("sync dir stat for %d: %s", ino, st)
			}
		}(i)
	}
	return &wg
}

func (m *baseMeta) groupBatch(batch map[Ino]dirStat, size int) [][]Ino {
	var inos []Ino
	for ino := range batch {
		inos = append(inos, ino)
	}
	sort.Slice(inos, func(i, j int) bool {
		return inos[i] < inos[j]
	})
	var batches [][]Ino
	for i := 0; i < len(inos); i += size {
		end := i + size
		if end > len(inos) {
			end = len(inos)
		}
		batches = append(batches, inos[i:end])
	}
	return batches
}

func (m *baseMeta) calcDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
	var entries []*Entry
	if eno := m.en.doReaddir(ctx, ino, 1, &entries, -1); eno != 0 {
		return nil, eno
	}

	stat := new(dirStat)
	for _, e := range entries {
		stat.inodes += 1
		var l uint64
		if e.Attr.Typ == TypeFile {
			l = e.Attr.Length
		}
		stat.length += int64(l)
		stat.space += align4K(l)
	}
	return stat, 0
}

func (m *baseMeta) GetDirStat(ctx Context, inode Ino) (stat *dirStat, err error) {
	stat, err = m.en.doGetDirStat(ctx, m.checkRoot(inode), !m.conf.ReadOnly)
	if err != nil {
		return
	}
	if stat == nil {
		stat, err = m.calcDirStat(ctx, inode)
	}
	return
}

func (m *baseMeta) updateDirStat(ctx Context, ino Ino, length, space, inodes int64) {
	m.dirStatsLock.Lock()
	defer m.dirStatsLock.Unlock()
	stat := m.dirStats[ino]
	stat.length += length
	stat.inodes += inodes
	stat.space += space
	m.dirStats[ino] = stat
}

func (m *baseMeta) updateParentStat(ctx Context, inode, parent Ino, length, space int64) {
	if length == 0 && space == 0 {
		return
	}
	m.en.updateStats(space, 0)
	if parent > 0 {
		m.updateDirStat(ctx, parent, length, space, 0)
		m.updateDirQuota(ctx, parent, space, 0)
	} else {
		go func() {
			for p := range m.en.doGetParents(ctx, inode) {
				m.updateDirStat(ctx, p, length, space, 0)
				m.updateDirQuota(ctx, parent, space, 0)
			}
		}()
	}
}

func (m *baseMeta) flushDirStat() {
	for {
		time.Sleep(time.Second * 1)
		m.doFlushDirStat()
	}
}

func (m *baseMeta) doFlushDirStat() {
	m.dirStatsLock.Lock()
	if len(m.dirStats) == 0 {
		m.dirStatsLock.Unlock()
		return
	}
	stats := m.dirStats
	m.dirStats = make(map[Ino]dirStat)
	m.dirStatsLock.Unlock()
	err := m.en.doUpdateDirStat(Background, stats)
	if err != nil {
		logger.Errorf("update dir stat failed: %v", err)
	}
}

func (r *baseMeta) txLock(idx uint) {
	r.txlocks[idx%nlocks].Lock()
}

func (r *baseMeta) txUnlock(idx uint) {
	r.txlocks[idx%nlocks].Unlock()
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
	var format Format
	if err = json.Unmarshal(body, &format); err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	if checkVersion {
		if err = format.CheckVersion(); err != nil {
			return nil, fmt.Errorf("check version: %s", err)
		}
	}
	m.fmt = &format
	return m.fmt, nil
}

func (m *baseMeta) newSessionInfo() []byte {
	host, err := os.Hostname()
	if err != nil {
		logger.Warnf("Failed to get hostname: %s", err)
		host = ""
	}
	buf, err := json.Marshal(&SessionInfo{
		Version:    version.Version(),
		HostName:   host,
		MountPoint: m.conf.MountPoint,
		ProcessID:  os.Getpid(),
	})
	if err != nil {
		panic(err) // marshal SessionInfo should never fail
	}
	return buf
}

func (m *baseMeta) NewSession() error {
	go m.refresh()
	if m.conf.ReadOnly {
		logger.Infof("Create read-only session OK with version: %s", version.Version())
		return nil
	}

	v, err := m.en.incrCounter("nextSession", 1)
	if err != nil {
		return fmt.Errorf("get session ID: %s", err)
	}
	m.sid = uint64(v)
	if err = m.en.doNewSession(m.newSessionInfo()); err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	logger.Infof("Create session %d OK with version: %s", m.sid, version.Version())

	m.loadQuotas()
	go m.en.flushStats()
	go m.flushDirStat()
	go m.flushQuotas() // TODO: improve it in Redis?
	for i := 0; i < m.conf.MaxDeletes; i++ {
		go m.deleteSlices()
	}
	if !m.conf.NoBGJob {
		go m.cleanupDeletedFiles()
		go m.cleanupSlices()
		go m.cleanupTrash()
	}
	return nil
}

func (m *baseMeta) expireTime() int64 {
	return time.Now().Add(5 * m.conf.Heartbeat).Unix()
}

func (m *baseMeta) OnReload(fn func(f *Format)) {
	m.msgCallbacks.Lock()
	defer m.msgCallbacks.Unlock()
	m.reloadCb = append(m.reloadCb, fn)
}

func (m *baseMeta) refresh() {
	for {
		utils.SleepWithJitter(m.conf.Heartbeat)
		m.sesMu.Lock()
		if m.umounting {
			m.sesMu.Unlock()
			return
		}
		if !m.conf.ReadOnly {
			if err := m.en.doRefreshSession(); err != nil {
				logger.Errorf("Refresh session %d: %s", m.sid, err)
			}
		}
		m.sesMu.Unlock()

		old := m.fmt
		if _, err := m.Load(false); err != nil {
			logger.Warnf("reload setting: %s", err)
		} else if m.fmt.MetaVersion > MaxVersion {
			logger.Fatalf("incompatible metadata version %d > max version %d", m.fmt.MetaVersion, MaxVersion)
		} else if m.fmt.UUID != old.UUID {
			logger.Fatalf("UUID changed from %s to %s", old, m.fmt.UUID)
		} else if !reflect.DeepEqual(m.fmt, old) {
			m.msgCallbacks.Lock()
			cbs := m.reloadCb
			m.msgCallbacks.Unlock()
			for _, cb := range cbs {
				cb(m.fmt)
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

		if m.conf.ReadOnly || m.conf.NoBGJob {
			continue
		}
		if ok, err := m.en.setIfSmall("lastCleanupSessions", time.Now().Unix(), int64((m.conf.Heartbeat * 9 / 10).Seconds())); err != nil {
			logger.Warnf("checking counter lastCleanupSessions: %s", err)
		} else if ok {
			go m.CleanStaleSessions()
		}
	}
}

func (m *baseMeta) CleanStaleSessions() {
	sids, err := m.en.doFindStaleSessions(1000)
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	for _, sid := range sids {
		s, err := m.en.GetSession(sid, false)
		if err != nil {
			logger.Warnf("Get session info %d: %s", sid, err)
			s = &Session{Sid: sid}
		}
		logger.Infof("clean up stale session %d %+v: %s", sid, s.SessionInfo, m.en.doCleanStaleSession(sid))
	}
}

func (m *baseMeta) CloseSession() error {
	if m.conf.ReadOnly {
		return nil
	}
	m.doFlushDirStat()
	m.sesMu.Lock()
	m.umounting = true
	m.sesMu.Unlock()
	logger.Infof("close session %d: %s", m.sid, m.en.doCleanStaleSession(m.sid))
	return nil
}

func (m *baseMeta) checkQuota(ctx Context, space, inodes int64, parent Ino) bool {
	if space > 0 && m.fmt.Capacity > 0 && atomic.LoadInt64(&m.usedSpace)+atomic.LoadInt64(&m.newSpace)+space > int64(m.fmt.Capacity) {
		return true
	}
	if inodes > 0 && m.fmt.Inodes > 0 && atomic.LoadInt64(&m.usedInodes)+atomic.LoadInt64(&m.newInodes)+inodes > int64(m.fmt.Inodes) {
		return true
	}
	if parent == 0 { // FIXME: check all parents of the file
		logger.Warnf("Quota check is skipped for hardlinked files")
		return false
	}
	return m.checkDirQuota(ctx, parent, space, inodes)
}

func (m *baseMeta) loadQuotas() {
	quotas, err := m.en.doLoadQuotas(Background)
	if err == nil {
		m.quotaMu.Lock()
		for ino := range m.dirQuotas {
			if _, ok := quotas[ino]; !ok {
				logger.Infof("Quota for inode %d is deleted", ino)
				delete(m.dirQuotas, ino)
			}
		}
		for ino, q := range quotas {
			logger.Debugf("Load quotas got %d -> %+v", ino, q)
			if _, ok := m.dirQuotas[ino]; !ok {
				m.dirQuotas[ino] = q
			}
		}
		m.quotaMu.Unlock()

		// skip lock since I'm the only one updating the m.dirQuotas
		for ino, q := range quotas {
			quota := m.dirQuotas[ino]
			atomic.SwapInt64(&quota.MaxSpace, q.MaxSpace)
			atomic.SwapInt64(&quota.MaxInodes, q.MaxInodes)
			atomic.SwapInt64(&quota.UsedSpace, q.UsedSpace)
			atomic.SwapInt64(&quota.UsedInodes, q.UsedInodes)
		}
	} else {
		logger.Warnf("Load quotas: %s", err)
	}
}

func (m *baseMeta) getDirParent(ctx Context, inode Ino) (Ino, syscall.Errno) {
	m.parentMu.Lock()
	parent, ok := m.dirParents[inode]
	m.parentMu.Unlock()
	if ok {
		return parent, 0
	}
	logger.Debugf("Get directory parent of inode %d: cache miss", inode)
	var attr Attr
	st := m.GetAttr(ctx, inode, &attr)
	return attr.Parent, st
}

func (m *baseMeta) hasDirQuota(ctx Context, inode Ino) bool {
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil {
			return true
		}
		if inode <= RootInode {
			break
		}
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", inode, st)
			break
		}
	}
	return false
}

func (m *baseMeta) checkDirQuota(ctx Context, inode Ino, space, inodes int64) bool {
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil && q.check(space, inodes) {
			return true
		}
		if inode <= RootInode {
			break
		}
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", inode, st)
			break
		}
	}
	return false
}

func (m *baseMeta) updateDirQuota(ctx Context, inode Ino, space, inodes int64) {
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil {
			q.update(space, inodes)
		}
		if inode <= RootInode {
			break
		}
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", inode, st)
			break
		}
	}
}

func (m *baseMeta) flushQuotas() {
	quotas := make(map[Ino]*Quota)
	var newSpace, newInodes int64
	for {
		time.Sleep(time.Second * 3)
		m.quotaMu.RLock()
		for ino, q := range m.dirQuotas {
			newSpace = atomic.SwapInt64(&q.newSpace, 0)
			newInodes = atomic.SwapInt64(&q.newInodes, 0)
			if newSpace != 0 || newInodes != 0 {
				quotas[ino] = &Quota{newSpace: newSpace, newInodes: newInodes}
			}
		}
		m.quotaMu.RUnlock()
		// FIXME: merge
		for ino, q := range quotas {
			if err := m.en.doFlushQuota(Background, ino, q.newSpace, q.newInodes); err != nil {
				logger.Warnf("Flush quota of inode %d: %s", ino, err)
				m.quotaMu.RLock()
				cur := m.dirQuotas[ino]
				m.quotaMu.RUnlock()
				if cur != nil {
					cur.update(q.newSpace, q.newInodes)
				}
			}
		}
		for ino := range quotas {
			delete(quotas, ino)
		}
	}
}

func (m *baseMeta) HandleQuota(ctx Context, cmd uint8, dpath string, quotas map[string]*Quota) error {
	var inode Ino
	if cmd != QuotaList {
		if st := m.resolve(ctx, dpath, &inode); st != 0 {
			return st
		}
		if isTrash(inode) {
			return errors.New("no quota for any trash directory")
		}
	}

	switch cmd {
	case QuotaSet:
		q, err := m.en.doGetQuota(ctx, inode)
		if err != nil {
			return err
		}
		quota := quotas[dpath]
		if q == nil {
			var sum Summary
			if st := m.FastGetSummary(ctx, inode, &sum, true); st != 0 {
				return st
			}
			quota.UsedSpace = int64(sum.Size) - align4K(0)
			quota.UsedInodes = int64(sum.Dirs+sum.Files) - 1
			if quota.MaxSpace < 0 {
				quota.MaxSpace = 0
			}
			if quota.MaxInodes < 0 {
				quota.MaxInodes = 0
			}
			return m.en.doSetQuota(ctx, inode, quota, true)
		} else {
			quota.UsedSpace, quota.UsedInodes = q.UsedSpace, q.UsedInodes
			if quota.MaxSpace < 0 {
				quota.MaxSpace = q.MaxSpace
			}
			if quota.MaxInodes < 0 {
				quota.MaxInodes = q.MaxInodes
			}
			if quota.MaxSpace == q.MaxSpace && quota.MaxInodes == q.MaxInodes {
				return nil // nothing to update
			}
			return m.en.doSetQuota(ctx, inode, quota, false)
		}
	case QuotaGet:
		q, err := m.en.doGetQuota(ctx, inode)
		if err != nil {
			return err
		}
		if q == nil {
			return fmt.Errorf("no quota for inode %d path %s", inode, dpath)
		}
		quotas[dpath] = q
	case QuotaDel:
		return m.en.doDelQuota(ctx, inode)
	case QuotaList:
		quotaMap, err := m.en.doLoadQuotas(ctx)
		if err != nil {
			return err
		}
		var p string
		for ino, quota := range quotaMap {
			if ps := m.GetPaths(ctx, ino); len(ps) > 0 {
				p = ps[0]
			} else {
				p = fmt.Sprintf("inode:%d", ino)
			}
			quotas[p] = quota
		}
	default: // FIXME: QuotaCheck
		return fmt.Errorf("invalid quota command: %d", cmd)
	}
	return nil
}

func (m *baseMeta) cleanupDeletedFiles() {
	for {
		utils.SleepWithJitter(time.Minute)
		if ok, err := m.en.setIfSmall("lastCleanupFiles", time.Now().Unix(), int64(time.Minute.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter lastCleanupFiles: %s", err)
		} else if ok {
			files, err := m.en.doFindDeletedFiles(time.Now().Add(-time.Hour).Unix(), 10000)
			if err != nil {
				logger.Warnf("scan deleted files: %s", err)
				continue
			}
			for inode, length := range files {
				logger.Debugf("cleanup chunks of inode %d with %d bytes", inode, length)
				m.en.doDeleteFileData(inode, length)
			}
		}
	}
}

func (m *baseMeta) cleanupSlices() {
	for {
		utils.SleepWithJitter(time.Hour)
		if ok, err := m.en.setIfSmall("nextCleanupSlices", time.Now().Unix(), int64(time.Hour.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter nextCleanupSlices: %s", err)
		} else if ok {
			m.en.doCleanupSlices()
		}
	}
}

func (m *baseMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	defer m.timeit(time.Now())
	var used, inodes int64
	var err error
	err = utils.WithTimeout(func() error {
		used, err = m.en.getCounter(usedSpace)
		return err
	}, time.Millisecond*150)
	if err != nil {
		used = atomic.LoadInt64(&m.usedSpace)
	}
	err = utils.WithTimeout(func() error {
		inodes, err = m.en.getCounter(totalInodes)
		return err
	}, time.Millisecond*150)
	if err != nil {
		inodes = atomic.LoadInt64(&m.usedInodes)
	}
	used += atomic.LoadInt64(&m.newSpace)
	inodes += atomic.LoadInt64(&m.newInodes)
	if used < 0 {
		used = 0
	}
	if m.fmt.Capacity > 0 {
		*totalspace = m.fmt.Capacity
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
	if m.fmt.Inodes > 0 {
		if *iused > m.fmt.Inodes {
			*iavail = 0
		} else {
			*iavail = m.fmt.Inodes - *iused
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

func (m *baseMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	if inode == nil || attr == nil {
		return syscall.EINVAL // bad request
	}
	defer m.timeit(time.Now())
	parent = m.checkRoot(parent)
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
	if st == 0 && attr.Typ == TypeDirectory && !isTrash(parent) {
		m.parentMu.Lock()
		m.dirParents[*inode] = parent
		m.parentMu.Unlock()
	}
	return st
}

func (m *baseMeta) parseAttr(buf []byte, attr *Attr) {
	if attr == nil || len(buf) == 0 {
		return
	}
	rb := utils.FromBuffer(buf)
	attr.Flags = rb.Get8()
	attr.Mode = rb.Get16()
	attr.Typ = uint8(attr.Mode >> 12)
	attr.Mode &= 0xfff
	attr.Uid = rb.Get32()
	attr.Gid = rb.Get32()
	attr.Atime = int64(rb.Get64())
	attr.Atimensec = rb.Get32()
	attr.Mtime = int64(rb.Get64())
	attr.Mtimensec = rb.Get32()
	attr.Ctime = int64(rb.Get64())
	attr.Ctimensec = rb.Get32()
	attr.Nlink = rb.Get32()
	attr.Length = rb.Get64()
	attr.Rdev = rb.Get32()
	if rb.Left() >= 8 {
		attr.Parent = Ino(rb.Get64())
	}
	attr.Full = true
	logger.Tracef("attr: %+v -> %+v", buf, attr)
}

func (m *baseMeta) marshal(attr *Attr) []byte {
	w := utils.NewBuffer(36 + 24 + 4 + 8)
	w.Put8(attr.Flags)
	w.Put16((uint16(attr.Typ) << 12) | (attr.Mode & 0xfff))
	w.Put32(attr.Uid)
	w.Put32(attr.Gid)
	w.Put64(uint64(attr.Atime))
	w.Put32(attr.Atimensec)
	w.Put64(uint64(attr.Mtime))
	w.Put32(attr.Mtimensec)
	w.Put64(uint64(attr.Ctime))
	w.Put32(attr.Ctimensec)
	w.Put32(attr.Nlink)
	w.Put64(attr.Length)
	w.Put32(attr.Rdev)
	w.Put64(uint64(attr.Parent))
	logger.Tracef("attr: %+v -> %+v", attr, w.Bytes())
	return w.Bytes()
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
	if attr == nil || !attr.Full {
		if attr == nil {
			attr = &Attr{}
		}
		err := m.GetAttr(ctx, inode, attr)
		if err != 0 {
			return err
		}
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
	defer m.timeit(time.Now())
	var err syscall.Errno
	if inode == RootInode {
		e := utils.WithTimeout(func() error {
			err = m.en.doGetAttr(ctx, inode, attr)
			return nil
		}, time.Millisecond*300)
		if e != nil || err != 0 {
			err = 0
			attr.Typ = TypeDirectory
			attr.Mode = 0777
			attr.Nlink = 2
			attr.Length = 4 << 10
		}
	} else {
		err = m.en.doGetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.of.Update(inode, attr)
		if attr.Typ == TypeDirectory && inode != RootInode && !isTrash(attr.Parent) {
			m.parentMu.Lock()
			m.dirParents[inode] = attr.Parent
			m.parentMu.Unlock()
		}
	}
	return err
}

func (m *baseMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next >= m.freeInodes.maxid {
		v, err := m.en.incrCounter("nextInode", inodeBatch)
		if err != nil {
			return 0, err
		}
		m.freeInodes.next = uint64(v) - inodeBatch
		m.freeInodes.maxid = uint64(v)
	}
	n := m.freeInodes.next
	m.freeInodes.next++
	for n <= 1 {
		n = m.freeInodes.next
		m.freeInodes.next++
	}
	return Ino(n), nil
}

func (m *baseMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == RootInode && name == TrashName {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.ENOENT
	}

	defer m.timeit(time.Now())
	parent = m.checkRoot(parent)
	var space, inodes int64 = align4K(0), 1
	if m.checkQuota(ctx, space, inodes, parent) {
		return syscall.ENOSPC
	}
	err := m.en.doMknod(ctx, parent, name, _type, mode, cumask, rdev, path, inode, attr)
	if err == 0 {
		m.en.updateStats(space, inodes)
		m.updateDirStat(ctx, parent, 0, space, inodes)
		m.updateDirQuota(ctx, parent, space, inodes)
	}
	return err
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
	// mode of symlink is ignored in POSIX
	return m.Mknod(ctx, parent, name, TypeSymlink, 0777, 0, 0, path, inode, attr)
}

func (m *baseMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == RootInode && name == TrashName {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.ENOENT
	}

	defer m.timeit(time.Now())
	if attr == nil {
		attr = &Attr{}
	}
	parent = m.checkRoot(parent)
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		return st
	}
	if m.checkQuota(ctx, align4K(attr.Length), 1, parent) {
		return syscall.ENOSPC
	}

	defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
	err := m.en.doLink(ctx, inode, parent, name, attr)
	if err == 0 {
		m.updateDirStat(ctx, parent, int64(attr.Length), align4K(attr.Length), 1)
		m.updateDirQuota(ctx, parent, align4K(attr.Length), 1)
	}
	return err
}

func (m *baseMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	defer m.timeit(time.Now())
	target, err := m.en.doReadlink(ctx, inode)
	if err != nil {
		return errno(err)
	}
	if len(target) == 0 {
		return syscall.ENOENT
	}
	*path = target
	m.symlinks.Store(inode, target)
	return 0
}

func (m *baseMeta) Unlink(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno {
	if parent == RootInode && name == TrashName || isTrash(parent) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}

	defer m.timeit(time.Now())
	parent = m.checkRoot(parent)
	var attr Attr
	err := m.en.doUnlink(ctx, parent, name, &attr, skipCheckTrash...)
	if err == 0 {
		var diffLength uint64
		if attr.Typ == TypeFile {
			diffLength = attr.Length
		}
		m.updateDirStat(ctx, parent, -int64(diffLength), -align4K(diffLength), -1)
		m.updateDirQuota(ctx, parent, -align4K(diffLength), -1)
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
	if parent == RootInode && name == TrashName || parent == TrashInode || isTrash(parent) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}

	defer m.timeit(time.Now())
	parent = m.checkRoot(parent)
	var inode Ino
	st := m.en.doRmdir(ctx, parent, name, &inode, skipCheckTrash...)
	if st == 0 {
		if !isTrash(parent) {
			m.parentMu.Lock()
			delete(m.dirParents, inode)
			m.parentMu.Unlock()
		}
		m.updateDirStat(ctx, parent, 0, -align4K(0), -1)
		m.updateDirQuota(ctx, parent, -align4K(0), -1)
	}
	return st
}

func (m *baseMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if parentSrc == RootInode && nameSrc == TrashName || parentDst == RootInode && nameDst == TrashName {
		return syscall.EPERM
	}
	if isTrash(parentDst) || isTrash(parentSrc) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if nameDst == "" {
		return syscall.ENOENT
	}
	switch flags {
	case 0, RenameNoReplace, RenameExchange:
	case RenameWhiteout, RenameNoReplace | RenameWhiteout:
		return syscall.ENOTSUP
	default:
		return syscall.EINVAL
	}

	defer m.timeit(time.Now())
	if inode == nil {
		inode = new(Ino)
	}
	if attr == nil {
		attr = &Attr{}
	}
	parentSrc = m.checkRoot(parentSrc)
	parentDst = m.checkRoot(parentDst)
	quotaSrc := !isTrash(parentSrc) && m.hasDirQuota(ctx, parentSrc)
	quotaDst := m.hasDirQuota(ctx, parentDst)
	var space, inodes int64
	if quotaSrc || quotaDst {
		if st := m.Lookup(ctx, parentSrc, nameSrc, inode, attr); st != 0 {
			return st
		}
		if attr.Typ == TypeDirectory {
			var sum Summary
			logger.Debugf("Start to get summary of inode %d", *inode)
			if st := m.FastGetSummary(ctx, *inode, &sum, true); st != 0 {
				logger.Warnf("Get summary of inode %d: %s", *inode, st)
				return st
			}
			space, inodes = int64(sum.Size), int64(sum.Dirs+sum.Files)
		} else {
			space, inodes = align4K(attr.Length), 1
		}
		// FIXME: dst exists and is replaced or exchanged
		if quotaDst && m.checkDirQuota(ctx, parentDst, space, inodes) {
			return syscall.ENOSPC
		}
	}
	st := m.en.doRename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, inode, attr)
	if st == 0 {
		var diffLengh uint64
		if attr.Typ == TypeDirectory {
			m.parentMu.Lock()
			m.dirParents[*inode] = parentDst
			m.parentMu.Unlock()
		} else if attr.Typ == TypeFile {
			diffLengh = attr.Length
		}
		// FIXME: dst exists and is replaced or exchanged
		m.updateDirStat(ctx, parentSrc, -int64(diffLengh), -align4K(diffLengh), -1)
		m.updateDirStat(ctx, parentDst, int64(diffLengh), align4K(diffLengh), 1)
		if quotaSrc {
			m.updateDirQuota(ctx, parentSrc, -space, -inodes)
		}
		if quotaDst {
			m.updateDirQuota(ctx, parentDst, space, inodes)
		}
	}
	return st
}

func (m *baseMeta) Open(ctx Context, inode Ino, flags uint32, attr *Attr) syscall.Errno {
	if m.conf.ReadOnly && flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0 {
		return syscall.EROFS
	}
	if m.conf.OpenCache > 0 && m.of.OpenCheck(inode, attr) {
		return 0
	}
	var err syscall.Errno
	// attr may be valid, see fs.Open()
	if attr != nil && !attr.Full {
		err = m.GetAttr(ctx, inode, attr)
	}
	if attr.Flags&FlagImmutable != 0 {
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
	if err == 0 {
		m.of.Open(inode, attr)
	}
	return err
}

func (m *baseMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	m.of.InvalidateChunk(inode, indx)
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

func (m *baseMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	inode = m.checkRoot(inode)
	var attr Attr
	if err := m.GetAttr(ctx, inode, &attr); err != 0 {
		return err
	}
	defer m.timeit(time.Now())
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
	return m.en.doReaddir(ctx, inode, plus, entries, -1)
}

func (m *baseMeta) SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.EINVAL
	}

	defer m.timeit(time.Now())
	return m.en.doSetXattr(ctx, m.checkRoot(inode), name, value, flags)
}

func (m *baseMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	if name == "" {
		return syscall.EINVAL
	}

	defer m.timeit(time.Now())
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
		logger.Errorf("readdir inode %d: %s", inode, st)
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

type metaWalkFunc func(ctx Context, inode Ino, path string, attr *Attr)

func (m *baseMeta) walk(ctx Context, inode Ino, path string, attr *Attr, walkFn metaWalkFunc) syscall.Errno {
	walkFn(ctx, inode, path, attr)
	var entries []*Entry
	st := m.en.doReaddir(ctx, inode, 1, &entries, -1)
	if st != 0 {
		logger.Errorf("list %s: %s", path, st)
		return st
	}
	for _, entry := range entries {
		if !entry.Attr.Full {
			entry.Attr.Parent = inode
		}
		if st := m.walk(ctx, entry.Inode, filepath.Join(path, string(entry.Name)), entry.Attr, walkFn); st != 0 {
			return st
		}
	}
	return 0
}

func (m *baseMeta) Check(ctx Context, fpath string, repair bool, recursive bool, statAll bool) (st syscall.Errno) {
	var attr Attr
	var inode = RootInode
	var parent = RootInode
	attr.Typ = TypeDirectory
	ps := strings.FieldsFunc(fpath, func(r rune) bool {
		return r == '/'
	})
	for i, name := range ps {
		parent = inode
		if st = m.Lookup(ctx, parent, name, &inode, &attr); st != 0 {
			logger.Errorf("Lookup parent %d name %s: %s", parent, name, st)
			return
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
	if !attr.Full {
		attr.Parent = parent
	}

	type node struct {
		inode Ino
		path  string
		attr  *Attr
	}
	nodes := make(chan *node, 1000)
	go func() {
		defer close(nodes)
		if recursive {
			st = m.walk(ctx, inode, fpath, &attr, func(ctx Context, inode Ino, path string, attr *Attr) {
				nodes <- &node{inode, path, attr}
			})
		} else {
			nodes <- &node{inode, fpath, &attr}
		}
	}()

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
					if st != 0 {
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
						if st1 := m.en.doRepair(ctx, inode, attr); st1 == 0 {
							logger.Debugf("Path %s (inode %d) is successfully repaired", path, inode)
						} else {
							logger.Errorf("Repair path %s inode %d: %s", path, inode, st1)
						}

					} else {
						logger.Warnf("Path %s (inode %d) can be repaired, please re-run with '--path %s --repair' to fix it", path, inode, path)
					}
				}

				stat, err := m.en.doGetDirStat(ctx, inode, false)
				if err != nil {
					logger.Errorf("get dir stat for inode %d: %v", inode, err)
					continue
				}
				if stat == nil || stat.space < 0 || stat.inodes < 0 {
					logger.Warnf("usage stat of %s is missing or broken", path)
					statBroken = true
				}

				if repair {
					if statBroken || statAll {
						if _, st := m.en.doSyncDirStat(ctx, inode); st == 0 {
							logger.Debugf("Stat of path %s (inode %d) is successfully synced", path, inode)
						} else {
							logger.Errorf("Sync stat of path %s inode %d: %s", path, inode, err)
						}
					}
				} else if statBroken {
					logger.Warnf("Stat of path %s (inode %d) should be synced, please re-run with '--path %s --repair' to fix it", path, inode, path)
				}
			}
		}()
	}
	wg.Wait()
	return
}

func (m *baseMeta) Chroot(ctx Context, subdir string) syscall.Errno {
	for subdir != "" {
		ps := strings.SplitN(subdir, "/", 2)
		if ps[0] != "" {
			var attr Attr
			var inode Ino
			r := m.Lookup(ctx, m.root, ps[0], &inode, &attr)
			if r == syscall.ENOENT {
				r = m.Mkdir(ctx, m.root, ps[0], 0777, 0, 0, &inode, &attr)
			}
			if r != 0 {
				return r
			}
			if attr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			m.root = inode
		}
		if len(ps) == 1 {
			break
		}
		subdir = ps[1]
	}
	return 0
}

func (m *baseMeta) resolve(ctx Context, dpath string, inode *Ino) syscall.Errno {
	var attr Attr
	*inode = RootInode
	for dpath != "" {
		ps := strings.SplitN(dpath, "/", 2)
		if ps[0] != "" {
			if st := m.en.doLookup(ctx, *inode, ps[0], inode, &attr); st != 0 {
				return st
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

func (m *baseMeta) GetFormat() Format {
	return *m.fmt
}

func (m *baseMeta) CompactAll(ctx Context, threads int, bar *utils.Bar) syscall.Errno {
	var wg sync.WaitGroup
	ch := make(chan cchunk, 1000000)
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			for c := range ch {
				logger.Debugf("Compacting chunk %d:%d (%d slices)", c.inode, c.indx, c.slices)
				m.en.compactChunk(c.inode, c.indx, true)
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

func (m *baseMeta) deleteSlices() {
	var err error
	for s := range m.dslices {
		if err = m.newMsg(DeleteSlice, s.Id, s.Size); err != nil {
			logger.Warnf("Delete data blocks of slice %d (%d bytes): %s", s.Id, s.Size, err)
			continue
		}
		if err = m.en.doDeleteSlice(s.Id, s.Size); err != nil {
			logger.Errorf("Delete meta entry of slice %d (%d bytes): %s", s.Id, s.Size, err)
		}
	}
}

func (m *baseMeta) deleteSlice(id uint64, size uint32) {
	if id == 0 || m.conf.MaxDeletes == 0 {
		return
	}
	m.dslices <- Slice{Id: id, Size: size}
}

func (m *baseMeta) toTrash(parent Ino) bool {
	return m.fmt.TrashDays > 0 && !isTrash(parent)
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

	st := m.en.doLookup(Background, TrashInode, name, trash, nil)
	if st == syscall.ENOENT {
		st = m.en.doMknod(Background, TrashInode, name, TypeDirectory, 0555, 0, 0, "", trash, nil)
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

func (m *baseMeta) cleanupTrash() {
	for {
		utils.SleepWithJitter(time.Hour)
		if st := m.en.doGetAttr(Background, TrashInode, nil); st != 0 {
			if st != syscall.ENOENT {
				logger.Warnf("getattr inode %d: %s", TrashInode, st)
			}
			continue
		}
		if ok, err := m.en.setIfSmall("lastCleanupTrash", time.Now().Unix(), int64(time.Hour.Seconds())*9/10); err != nil {
			logger.Warnf("checking counter lastCleanupTrash: %s", err)
		} else if ok {
			go m.doCleanupTrash(false)
			go m.cleanupDelayedSlices()
		}
	}
}

func (m *baseMeta) CleanupTrashBefore(ctx Context, edge time.Time, increProgress func()) {
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
				if se.Attr.Typ == TypeDirectory {
					st = m.en.doRmdir(ctx, e.Inode, string(se.Name), nil)
				} else {
					st = m.en.doUnlink(ctx, e.Inode, string(se.Name), nil)
				}
				if st == 0 {
					count++
					if increProgress != nil {
						increProgress()
					}
				} else {
					logger.Warnf("delete from trash %s/%s: %s", e.Name, se.Name, st)
					rmdir = false
					continue
				}
				if count%10000 == 0 && time.Since(now) > 50*time.Minute {
					return
				}
			}
			if rmdir {
				if st = m.en.doRmdir(ctx, TrashInode, string(e.Name), nil); st != 0 {
					logger.Warnf("rmdir subTrash %s: %s", e.Name, st)
				}
			}
		} else {
			break
		}
	}
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

func (m *baseMeta) doCleanupTrash(force bool) {
	edge := time.Now().Add(-time.Duration(24*m.fmt.TrashDays+1) * time.Hour)
	if force {
		edge = time.Now()
	}
	m.CleanupTrashBefore(Background, edge, nil)
}

func (m *baseMeta) cleanupDelayedSlices() {
	now := time.Now()
	edge := now.Unix() - int64(m.fmt.TrashDays)*24*3600
	logger.Debugf("Cleanup delayed slices: started with edge %d", edge)
	if count, err := m.en.doCleanupDelayedSlices(edge); err != nil {
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
			return m.en.scanPendingFiles(ctx, pfs)
		})
	}
	return eg.Wait()
}
