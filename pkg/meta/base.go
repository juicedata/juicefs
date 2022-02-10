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
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	inodeBatch   = 100
	chunkIDBatch = 1000
)

type engine interface {
	incrCounter(name string, value int64) (int64, error)
	// Set name to value if old <= value - diff
	setIfSmall(name string, value, diff int64) (bool, error)

	doLoad() ([]byte, error)

	doNewSession(sinfo []byte) error
	doRefreshSession()
	doFindStaleSessions(ts int64, limit int) ([]uint64, error) // limit < 0 means all
	doCleanStaleSession(sid uint64)

	doDeleteSustainedInode(sid uint64, inode Ino) error
	doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) // limit < 0 means all
	doDeleteFileData(inode Ino, length uint64)
	doCleanupSlices()
	doDeleteSlice(chunkid uint64, size uint32) error

	doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno
	doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
	doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno
	doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno
	doUnlink(ctx Context, parent Ino, name string) syscall.Errno
	doRmdir(ctx Context, parent Ino, name string) syscall.Errno
	doReadlink(ctx Context, inode Ino) ([]byte, error)
	doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno
	doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno
	GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno
	SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno
}

type baseMeta struct {
	sync.Mutex
	conf *Config
	fmt  Format

	root         Ino
	subTrash     internalNode
	sid          uint64
	of           *openfiles
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	newSpace     int64
	newInodes    int64
	usedSpace    int64
	usedInodes   int64
	umounting    bool

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID

	en engine
}

func newBaseMeta(conf *Config) baseMeta {
	if conf.Retries == 0 {
		conf.Retries = 30
	}
	return baseMeta{
		conf:         conf,
		root:         1,
		of:           newOpenFiles(conf.OpenCache),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		deleting:     make(chan int, conf.MaxDeletes),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}
}

func (m *baseMeta) checkRoot(inode Ino) Ino {
	switch inode {
	case 0:
		return 1 // force using Root inode
	case 1:
		return m.root
	default:
		return inode
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

func (m *baseMeta) Load() (*Format, error) {
	body, err := m.en.doLoad()
	if err == nil && len(body) == 0 {
		err = fmt.Errorf("database is not formatted")
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(body, &m.fmt); err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &m.fmt, nil
}

func (m *baseMeta) NewSession() error {
	go m.refreshUsage()
	if m.conf.ReadOnly {
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
	logger.Debugf("create session %d OK", m.sid)

	go m.refreshSession()
	go m.cleanupDeletedFiles()
	go m.cleanupSlices()
	go m.cleanupTrash()
	return nil
}

func (m *baseMeta) refreshSession() {
	for {
		time.Sleep(time.Minute)
		m.Lock()
		if m.umounting {
			m.Unlock()
			return
		}
		m.en.doRefreshSession()
		m.Unlock()
		if _, err := m.Load(); err != nil {
			logger.Warnf("reload setting: %s", err)
		}
		if ok, err := m.en.setIfSmall("lastCleanupSessions", time.Now().Unix(), 60); err != nil {
			logger.Warnf("checking counter lastCleanupSessions: %s", err)
		} else if ok {
			go m.CleanStaleSessions()
		}
	}
}

func (m *baseMeta) CleanStaleSessions() {
	sids, err := m.en.doFindStaleSessions(time.Now().Add(time.Minute*-5).Unix(), 100)
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	for _, sid := range sids {
		m.en.doCleanStaleSession(sid)
	}
}

func (m *baseMeta) CloseSession() error {
	if m.conf.ReadOnly {
		return nil
	}
	m.Lock()
	m.umounting = true
	m.Unlock()
	m.en.doCleanStaleSession(m.sid)
	return nil
}

func (m *baseMeta) refreshUsage() {
	for {
		if v, err := m.en.incrCounter(usedSpace, 0); err == nil {
			atomic.StoreInt64(&m.usedSpace, v)
		}
		if v, err := m.en.incrCounter(totalInodes, 0); err == nil {
			atomic.StoreInt64(&m.usedInodes, v)
		}
		time.Sleep(time.Second * 10)
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
		time.Sleep(time.Minute)
		if ok, err := m.en.setIfSmall("lastCleanupFiles", time.Now().Unix(), 60); err != nil {
			logger.Warnf("checking counter lastCleanupFiles: %s", err)
		} else if ok {
			files, err := m.en.doFindDeletedFiles(time.Now().Add(-time.Hour).Unix(), 1000)
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
		time.Sleep(time.Hour)
		if ok, err := m.en.setIfSmall("nextCleanupSlices", time.Now().Unix(), 3600); err != nil {
			logger.Warnf("checking counter nextCleanupSlices: %s", err)
		} else if ok {
			m.en.doCleanupSlices()
		}
	}
}

func (m *baseMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	defer timeit(time.Now())
	var used, inodes int64
	var err error
	err = utils.WithTimeout(func() error {
		used, err = m.en.incrCounter(usedSpace, 0)
		return err
	}, time.Millisecond*150)
	if err != nil {
		used = atomic.LoadInt64(&m.usedSpace)
	}
	err = utils.WithTimeout(func() error {
		inodes, err = m.en.incrCounter(totalInodes, 0)
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
	_ = m.en.doReaddir(ctx, parent, 0, &entries)
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
	defer timeit(time.Now())
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
		if attr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		*inode = parent
		return 0
	}
	if parent == 1 && name == TrashName {
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
	if attr == nil {
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
	defer timeit(time.Now())
	var err syscall.Errno
	if inode == 1 {
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

func (m *baseMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == 1 && name == TrashName {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	return m.en.doMknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (m *baseMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == 1 && name == TrashName {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	if attr == nil {
		attr = &Attr{}
	}
	err := m.en.doMknod(ctx, parent, name, TypeFile, mode, cumask, 0, "", inode, attr)
	if err == syscall.EEXIST && (flags&syscall.O_EXCL) == 0 && attr.Typ == TypeFile {
		err = 0
	}
	if err == 0 && inode != nil {
		m.of.Open(*inode, attr)
	}
	return err
}

func (m *baseMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == 1 && name == TrashName {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	return m.en.doMknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, "", inode, attr)
}

func (m *baseMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == 1 && name == TrashName {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	return m.en.doMknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *baseMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	if isTrash(parent) {
		return syscall.EPERM
	}
	if parent == 1 && name == TrashName {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return m.en.doLink(ctx, inode, parent, name, attr)
}

func (m *baseMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	defer timeit(time.Now())
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
	if parent == 1 && name == TrashName || isTrash(parent) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	return m.en.doUnlink(ctx, parent, name)
}

func (m *baseMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}
	if parent == 1 && name == TrashName || parent == TrashInode || isTrash(parent) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	return m.en.doRmdir(ctx, parent, name)
}

func (m *baseMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if parentSrc == 1 && nameSrc == TrashName || parentDst == 1 && nameDst == TrashName {
		return syscall.EPERM
	}
	if isTrash(parentDst) || isTrash(parentSrc) && ctx.Uid() != 0 {
		return syscall.EPERM
	}
	switch flags {
	case 0, RenameNoReplace, RenameExchange:
	case RenameWhiteout, RenameNoReplace | RenameWhiteout:
		return syscall.ENOTSUP
	default:
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	parentSrc = m.checkRoot(parentSrc)
	parentDst = m.checkRoot(parentDst)
	return m.en.doRename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, inode, attr)
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
	if err == 0 {
		m.of.Open(inode, attr)
	}
	return err
}

func (m *baseMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	m.of.InvalidateChunk(inode, indx)
	return 0
}

func (m *baseMeta) NewChunk(ctx Context, chunkid *uint64) syscall.Errno {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeChunks.next >= m.freeChunks.maxid {
		v, err := m.en.incrCounter("nextChunk", chunkIDBatch)
		if err != nil {
			return errno(err)
		}
		m.freeChunks.next = uint64(v) - chunkIDBatch
		m.freeChunks.maxid = uint64(v)
	}
	*chunkid = m.freeChunks.next
	m.freeChunks.next++
	return 0
}

func (m *baseMeta) Close(ctx Context, inode Ino) syscall.Errno {
	if m.of.Close(inode) {
		m.Lock()
		defer m.Unlock()
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
			go func() {
				_ = m.en.doDeleteSustainedInode(m.sid, inode)
			}()
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
	defer timeit(time.Now())
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
	return m.en.doReaddir(ctx, inode, plus, entries)
}

func (m *baseMeta) fileDeleted(opened bool, inode Ino, length uint64) {
	if opened {
		m.Lock()
		m.removedFiles[inode] = true
		m.Unlock()
	} else {
		go m.en.doDeleteFileData(inode, length)
	}
}

func (m *baseMeta) deleteSlice(chunkid uint64, size uint32) {
	if m.conf.MaxDeletes == 0 {
		return
	}
	m.deleting <- 1
	defer func() { <-m.deleting }()
	err := m.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
	} else {
		err := m.en.doDeleteSlice(chunkid, size)
		if err != nil {
			logger.Errorf("delete slice %d: %s", chunkid, err)
		}
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

func (m *baseMeta) cleanupTrash() {
	for {
		time.Sleep(time.Hour)
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
		}
	}
}

func (m *baseMeta) doCleanupTrash(force bool) {
	logger.Debugf("cleanup trash: started")
	ctx := Background
	now := time.Now()
	var st syscall.Errno
	var entries []*Entry
	if st = m.en.doReaddir(ctx, TrashInode, 0, &entries); st != 0 {
		logger.Warnf("readdir trash %d: %s", TrashInode, st)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Inode < entries[j].Inode })
	var count int
	defer func() {
		if count > 0 {
			logger.Infof("cleanup trash: deleted %d files in %v", count, time.Since(now))
		}
	}()

	edge := now.Add(-time.Duration(24*m.fmt.TrashDays+1) * time.Hour)
	for _, e := range entries {
		ts, err := time.Parse("2006-01-02-15", string(e.Name))
		if err != nil {
			logger.Warnf("bad entry as a subTrash: %s", e.Name)
			continue
		}
		if ts.Before(edge) || force {
			var subEntries []*Entry
			if st = m.en.doReaddir(ctx, e.Inode, 0, &subEntries); st != 0 {
				logger.Warnf("readdir subTrash %d: %s", e.Inode, st)
				continue
			}
			rmdir := true
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
