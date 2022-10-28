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
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/prometheus/client_golang/prometheus"
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

	doLoad() ([]byte, error)

	doNewSession(sinfo []byte) error
	doRefreshSession()
	doFindStaleSessions(limit int) ([]uint64, error) // limit < 0 means all
	doCleanStaleSession(sid uint64) error

	doDeleteSustainedInode(sid uint64, inode Ino) error
	doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) // limit < 0 means all
	doDeleteFileData(inode Ino, length uint64)
	doCleanupSlices()
	doCleanupDelayedSlices(edge int64, limit int) (int, error)
	doDeleteSlice(id uint64, size uint32) error

	doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno
	doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno
	doUnlink(ctx Context, parent Ino, name string) syscall.Errno
	doRmdir(ctx Context, parent Ino, name string) syscall.Errno
	doReadlink(ctx Context, inode Ino) ([]byte, error)
	doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno
	doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno
	doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno
	doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno
	doGetParents(ctx Context, inode Ino) map[Ino]int
	doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno

	GetSession(sid uint64, detail bool) (*Session, error)
}

// fsStat aligned for atomic operations
// nolint:structcheck
type fsStat struct {
	newSpace   int64
	newInodes  int64
	usedSpace  int64
	usedInodes int64
}

type baseMeta struct {
	sync.Mutex
	addr string
	conf *Config
	fmt  Format

	root         Ino
	txlocks      [nlocks]sync.Mutex // Pessimistic locks to reduce conflict
	subTrash     internalNode
	sid          uint64
	of           *openfiles
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	maxDeleting  chan struct{}
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	umounting    bool

	*fsStat

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
	if conf.Retries == 0 {
		conf.Retries = 10
	}
	if conf.Heartbeat == 0 {
		conf.Heartbeat = 12 * time.Second
	}
	return &baseMeta{
		addr:         utils.RemovePassword(addr),
		conf:         conf,
		root:         RootInode,
		of:           newOpenFiles(conf.OpenCache),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		maxDeleting:  make(chan struct{}, 100),
		symlinks:     &sync.Map{},
		fsStat:       new(fsStat),
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
	if err = json.Unmarshal(body, &m.fmt); err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	if checkVersion {
		if err = m.fmt.CheckVersion(); err != nil {
			return nil, fmt.Errorf("check version: %s", err)
		}
	}
	return &m.fmt, nil
}

func (m *baseMeta) NewSession() error {
	go m.refreshUsage()
	if m.conf.ReadOnly {
		logger.Infof("Create read-only session OK with version: %s", version.Version())
		return nil
	}

	v, err := m.en.incrCounter("nextSession", 1)
	if err != nil {
		return fmt.Errorf("get session ID: %s", err)
	}
	m.sid = uint64(v)
	info := newSessionInfo()
	info.MountPoint = m.conf.MountPoint
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	if err = m.en.doNewSession(data); err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	logger.Infof("Create session %d OK with version: %s", m.sid, version.Version())

	go m.refreshSession()
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

func (m *baseMeta) refreshSession() {
	for {
		utils.SleepWithJitter(m.conf.Heartbeat)
		m.Lock()
		if m.umounting {
			m.Unlock()
			return
		}
		m.en.doRefreshSession()
		m.Unlock()
		old := m.fmt.UUID
		if _, err := m.Load(false); err != nil {
			logger.Warnf("reload setting: %s", err)
		} else if m.fmt.MetaVersion > MaxVersion {
			logger.Fatalf("incompatible metadata version %d > max version %d", m.fmt.MetaVersion, MaxVersion)
		} else if m.fmt.UUID != old {
			logger.Fatalf("UUID changed from %s to %s", old, m.fmt.UUID)
		}
		if m.conf.NoBGJob {
			continue
		}
		if ok, err := m.en.setIfSmall("lastCleanupSessions", time.Now().Unix(), int64(m.conf.Heartbeat/time.Second)); err != nil {
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
	m.Lock()
	m.umounting = true
	m.Unlock()
	logger.Infof("close session %d: %s", m.sid, m.en.doCleanStaleSession(m.sid))
	return nil
}

func (m *baseMeta) refreshUsage() {
	for {
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
		utils.SleepWithJitter(time.Second * 10)
	}
}

func (m *baseMeta) checkQuota(size, inodes int64) bool {
	if size > 0 && m.fmt.Capacity > 0 && atomic.LoadInt64(&m.usedSpace)+atomic.LoadInt64(&m.newSpace)+size > int64(m.fmt.Capacity) {
		return true
	}
	return inodes > 0 && m.fmt.Inodes > 0 && atomic.LoadInt64(&m.usedInodes)+atomic.LoadInt64(&m.newInodes)+inodes > int64(m.fmt.Inodes)
}

func (m *baseMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *baseMeta) flushStats() {
	for {
		newSpace := atomic.SwapInt64(&m.newSpace, 0)
		if newSpace != 0 {
			if _, err := m.en.incrCounter(usedSpace, newSpace); err != nil {
				logger.Warnf("update space stats: %s", err)
				m.updateStats(newSpace, 0)
			}
		}
		newInodes := atomic.SwapInt64(&m.newInodes, 0)
		if newInodes != 0 {
			if _, err := m.en.incrCounter(totalInodes, newInodes); err != nil {
				logger.Warnf("update inodes stats: %s", err)
				m.updateStats(0, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *baseMeta) cleanupDeletedFiles() {
	for {
		utils.SleepWithJitter(time.Minute)
		if ok, err := m.en.setIfSmall("lastCleanupFiles", time.Now().Unix(), 60); err != nil {
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
		if ok, err := m.en.setIfSmall("nextCleanupSlices", time.Now().Unix(), 3600); err != nil {
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
	if m.checkQuota(4<<10, 1) {
		return syscall.ENOSPC
	}
	return m.en.doMknod(ctx, m.checkRoot(parent), name, _type, mode, cumask, rdev, path, inode, attr)
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
	return m.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, "", inode, attr)
}

func (m *baseMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	return m.Mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
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
	parent = m.checkRoot(parent)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return m.en.doLink(ctx, inode, parent, name, attr)
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

func (m *baseMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	if parent == RootInode && name == TrashName || isTrash(parent) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	if m.conf.ReadOnly {
		return syscall.EROFS
	}

	defer m.timeit(time.Now())
	return m.en.doUnlink(ctx, m.checkRoot(parent), name)
}

func (m *baseMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
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
	return m.en.doRmdir(ctx, m.checkRoot(parent), name)
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
	return m.en.doRename(ctx, m.checkRoot(parentSrc), nameSrc, m.checkRoot(parentDst), nameDst, flags, inode, attr)
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
		defer m.Unlock()
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
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

func (m *baseMeta) Check(ctx Context, fpath string, repair bool, recursive bool) (st syscall.Errno) {
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
				if attr.Full {
					nlink, st := m.countDirNlink(ctx, inode)
					if st != 0 {
						logger.Errorf("Count nlink for inode %d: %s", inode, st)
						continue
					}
					if attr.Nlink == nlink {
						continue
					}
					logger.Warnf("nlink of %s should be %d, but got %d", path, nlink, attr.Nlink)
				} else {
					logger.Warnf("attribute of %s is missing", path)
				}

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
						logger.Infof("Path %s (inode %d) is successfully repaired", path, inode)
					} else {
						logger.Errorf("Repair path %s inode %d: %s", path, inode, st1)
					}
				} else {
					logger.Warnf("Path %s (inode %d) can be repaired, please re-run with '--path %s --repair' to fix it", path, inode, path)
				}
			}
		}()
	}
	wg.Wait()
	return
}

func (m *baseMeta) fileDeleted(opened bool, inode Ino, length uint64) {
	if opened {
		m.Lock()
		m.removedFiles[inode] = true
		m.Unlock()
	} else {
		m.tryDeleteFileData(inode, length)
	}
}

func (m *baseMeta) tryDeleteFileData(inode Ino, length uint64) {
	select {
	case m.maxDeleting <- struct{}{}:
		go func() {
			m.en.doDeleteFileData(inode, length)
			<-m.maxDeleting
		}()
	default:
		// will be cleanup later
	}
}

func (m *baseMeta) deleteSlice(id uint64, size uint32) {
	if id == 0 {
		return
	}
	if err := m.newMsg(DeleteSlice, id, size); err == nil || strings.Contains(err.Error(), "NoSuchKey") || strings.Contains(err.Error(), "not found") {
		if err = m.en.doDeleteSlice(id, size); err != nil {
			logger.Errorf("delete slice %d: %s", id, err)
		}
	} else if !strings.Contains(err.Error(), "skip deleting") {
		logger.Warnf("delete slice %d (%d bytes): %s", id, size, err)
	}
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
		if ok, err := m.en.setIfSmall("lastCleanupTrash", time.Now().Unix(), 3600); err != nil {
			logger.Warnf("checking counter lastCleanupTrash: %s", err)
		} else if ok {
			go m.doCleanupTrash(false)
			go m.cleanupDelayedSlices()
		}
	}
}

func (m *baseMeta) doCleanupTrash(force bool) {
	logger.Debugf("cleanup trash: started")
	ctx := Background
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
	edge := now.Add(-time.Duration(24*m.fmt.TrashDays+1) * time.Hour)
	for len(entries) > 0 {
		e := entries[0]
		ts, err := time.Parse("2006-01-02-15", string(e.Name))
		if err != nil {
			logger.Warnf("bad entry as a subTrash: %s", e.Name)
			continue
		}
		if ts.Before(edge) || force {
			var subEntries []*Entry
			if st = m.en.doReaddir(ctx, e.Inode, 0, &subEntries, batch); st != 0 {
				logger.Warnf("readdir subTrash %d: %s", e.Inode, st)
				continue
			}
			rmdir := len(subEntries) < batch
			if rmdir {
				entries = entries[1:]
			}
			for _, se := range subEntries {
				if se.Attr.Typ == TypeDirectory {
					st = m.en.doRmdir(ctx, e.Inode, string(se.Name))
				} else {
					st = m.en.doUnlink(ctx, e.Inode, string(se.Name))
				}
				if st == 0 {
					count++
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
				if st = m.en.doRmdir(ctx, TrashInode, string(e.Name)); st != 0 {
					logger.Warnf("rmdir subTrash %s: %s", e.Name, st)
				}
			}
		} else {
			break
		}
	}
}

func (m *baseMeta) cleanupDelayedSlices() {
	now := time.Now()
	edge := now.Unix() - int64(m.fmt.TrashDays)*24*3600
	logger.Debugf("Cleanup delayed slices: started with edge %d", edge)
	if count, err := m.en.doCleanupDelayedSlices(edge, 3e5); err == nil {
		msg := fmt.Sprintf("Cleanup delayed slices: deleted %d slices in %v", count, time.Since(now))
		if count >= 3e5 {
			logger.Warnf("%s (reached max limit, stop)", msg)
		} else {
			logger.Debugf(msg)
		}
	} else {
		logger.Warnf("Cleanup delayed slices: deleted %d slices in %v, but got error: %s", count, time.Since(now), err)
	}
}
