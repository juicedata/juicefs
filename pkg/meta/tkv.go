// +build tikv fdb

/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

type kvTxn interface {
	get(key []byte) []byte
	gets(keys ...[]byte) [][]byte
	scanRange(begin, end []byte) map[string][]byte
	scanKeys(prefix []byte) [][]byte
	scanValues(prefix []byte) map[string][]byte
	exist(prefix []byte) bool
	set(key, value []byte)
	append(key []byte, value []byte) []byte
	incrBy(key []byte, value int64) int64
	dels(keys ...[]byte)
}

type tkvClient interface {
	name() string
	txn(f func(kvTxn) error) error
}

type kvMeta struct {
	sync.Mutex
	conf   *Config
	fmt    Format
	prefix []byte
	client tkvClient

	sid          uint64
	of           *openfiles
	root         Ino
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	newSpace     int64
	newInodes    int64
	usedSpace    int64
	usedInodes   int64

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID
}

func newKVMeta(driver, addr string, conf *Config) (Meta, error) {
	client, err := newTkvClient(driver, addr)
	if err != nil {
		return nil, fmt.Errorf("unable to connect driver %s addr %s: %s", driver, addr, err)
	}
	// TODO: ping server and check latency > Millisecond
	// logger.Warnf("The latency to database is too high: %s", time.Since(start))
	if conf.Retries == 0 {
		conf.Retries = 30
	}
	m := &kvMeta{
		conf:         conf,
		client:       client,
		of:           newOpenFiles(conf.OpenCache),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		deleting:     make(chan int, 2),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}
	p := strings.Index(addr, "/")
	if p > 0 {
		m.prefix = []byte(addr[p+1:])
	}
	m.root = 1
	m.root, err = lookupSubdir(m, conf.Subdir)
	return m, err
}

func (m *kvMeta) checkRoot(inode Ino) Ino {
	if inode == 1 {
		return m.root
	}
	return inode
}

func (m *kvMeta) Name() string {
	return m.client.name()
}

func (m *kvMeta) keyLen(args ...interface{}) int {
	var c int
	for _, a := range args {
		switch a := a.(type) {
		case byte:
			c++
		case uint32:
			c += 4
		case uint64:
			c += 8
		case Ino:
			c += 8
		case string:
			c += len(a)
		default:
			logger.Fatalf("invalid type %T, value %v", a, a)
		}
	}
	return c
}

func (m *kvMeta) fmtKey(args ...interface{}) []byte {
	b := utils.NewBuffer(uint32(len(m.prefix) + m.keyLen(args...)))
	b.Put(m.prefix)
	for _, a := range args {
		switch a := a.(type) {
		case byte:
			b.Put8(a)
		case uint32:
			b.Put32(a)
		case uint64:
			b.Put64(a)
		case Ino:
			binary.LittleEndian.PutUint64(b.Get(8), uint64(a))
		case string:
			b.Put([]byte(a))
		default:
			logger.Fatalf("invalid type %T, value %v", a, a)
		}
	}
	return b.Bytes()
}

/**
  Ino iiiiiiii
  Indx nnnn
  name ...
  chunkid cccccccc
  session  ssssssss

All keys:
  setting         format
  C...            counter
  AiiiiiiiiI      inode attribute
  AiiiiiiiiD...   dentry
  AiiiiiiiiCnnnn  file chunks
  AiiiiiiiiS      symlink target
  AiiiiiiiiX...   extented attribute
  Diiiiiiiinnnn   delete inodes
  Kccccccccnnnn   slice refs
  SHssssssss      session heartbeat
  SIssssssss      session info
  SSssssssssiiiiiiii sustained inode
*/

func (m *kvMeta) inodeKey(inode Ino) []byte {
	return m.fmtKey("A", inode, "I")
}

func (m *kvMeta) entryKey(parent Ino, name string) []byte {
	return m.fmtKey("A", parent, "D", name)
}

func (m *kvMeta) chunkKey(inode Ino, indx uint32) []byte {
	return m.fmtKey("A", inode, "C", indx)
}

func (m *kvMeta) sliceKey(chunkid uint64, size uint32) []byte {
	return m.fmtKey('K', chunkid, size)
}

func (m *kvMeta) symKey(inode Ino) []byte {
	return m.fmtKey("A", inode, "S")
}

func (m *kvMeta) xattrKey(inode Ino, name string) []byte {
	return m.fmtKey("A", inode, "X", name)
}

func (m *kvMeta) sessionKey(sid uint64) []byte {
	return m.fmtKey("SH", sid)
}

func (m *kvMeta) parseSid(key []byte) uint64 {
	prefix := len(m.prefix) + 2 // "SH"
	b := utils.FromBuffer(key[prefix:])
	if b.Len() != 8 {
		panic("invalid sid value")
	}
	return b.Get64()
}

func (m *kvMeta) sessionInfoKey(sid uint64) []byte {
	return m.fmtKey("SI", sid)
}

func (m *kvMeta) sustainedKey(sid uint64, inode Ino) []byte {
	return m.fmtKey("SS", sid, inode)
}

func (m *kvMeta) parseInode(key []byte) Ino {
	prefix := len(m.prefix) + 10 // "SS" + sid
	b := utils.FromBuffer(key[prefix:])
	if b.Len() != 8 {
		panic("invalid inode value")
	}
	return Ino(b.Get64())
}

func (m *kvMeta) delfileKey(inode Ino, length uint64) []byte {
	return m.fmtKey("D", inode, length)
}

func (m *kvMeta) counterKey(key string) []byte {
	return m.fmtKey("C", key)
}

func (m *kvMeta) packTime(now int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(now))
	return b
}

func (m *kvMeta) parseTime(buf []byte) int64 {
	if len(buf) != 8 {
		panic("invalid time value")
	}
	return int64(binary.BigEndian.Uint64(buf))
}

func (m *kvMeta) packEntry(_type uint8, inode Ino) []byte {
	b := utils.NewBuffer(9)
	b.Put8(_type)
	b.Put64(uint64(inode))
	return b.Bytes()
}

func (m *kvMeta) parseEntry(buf []byte) (uint8, Ino) {
	if len(buf) != 9 {
		panic("invalid entry")
	}
	return buf[0], Ino(binary.BigEndian.Uint64(buf[1:]))
}

func (m *kvMeta) parseAttr(buf []byte, attr *Attr) {
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
	logger.Tracef("attr: %v -> %v", buf, attr)
}

func (m *kvMeta) marshal(attr *Attr) []byte {
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

func (m *kvMeta) get(key []byte) ([]byte, error) {
	var value []byte
	err := m.txn(func(tx kvTxn) error {
		value = tx.get(key)
		return nil
	})
	return value, err
}

func (m *kvMeta) scanKeys(prefix []byte) ([][]byte, error) {
	var keys [][]byte
	err := m.txn(func(tx kvTxn) error {
		keys = tx.scanKeys(prefix)
		return nil
	})
	return keys, err
}

func (m *kvMeta) scanValues(prefix []byte) (map[string][]byte, error) {
	var values map[string][]byte
	err := m.txn(func(tx kvTxn) error {
		values = tx.scanValues(prefix)
		return nil
	})
	return values, err
}

func (m *kvMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next < m.freeInodes.maxid {
		v := m.freeInodes.next
		m.freeInodes.next++
		return Ino(v), nil
	}
	v, err := m.incrCounter(m.counterKey("nextInode"), 100)
	if err == nil {
		m.freeInodes.next = uint64(v) + 1
		m.freeInodes.maxid = uint64(v) + 100
	}
	return Ino(v), err
}

func (m *kvMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeChunks.next < m.freeChunks.maxid {
		*chunkid = m.freeChunks.next
		m.freeChunks.next++
		return 0
	}
	v, err := m.incrCounter(m.counterKey("nextChunk"), 1000)
	if err == nil {
		*chunkid = uint64(v)
		m.freeChunks.next = uint64(v) + 1
		m.freeChunks.maxid = uint64(v) + 1000
	}
	return errno(err)
}

func (m *kvMeta) Init(format Format, force bool) error {
	old, err := m.Load()
	if err != nil {
		return err
	}
	if old != nil {
		if force {
			old.SecretKey = "removed"
			logger.Warnf("Existing volume will be overwrited: %+v", old)
		} else {
			format.UUID = old.UUID
			// these can be safely updated.
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
			old.Capacity = format.Capacity
			old.Inodes = format.Inodes
			if format != *old {
				old.SecretKey = ""
				format.SecretKey = ""
				return fmt.Errorf("cannot update format from %+v to %+v", old, format)
			}
		}
		data, err := json.MarshalIndent(format, "", "")
		if err != nil {
			logger.Fatalf("json: %s", err)
		}
		return m.setValue(m.fmtKey("setting"), data)
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}

	m.fmt = format
	// root inode
	var attr Attr
	attr.Typ = TypeDirectory
	attr.Mode = 0777
	ts := time.Now().Unix()
	attr.Atime = ts
	attr.Mtime = ts
	attr.Ctime = ts
	attr.Nlink = 2
	attr.Length = 4 << 10
	attr.Parent = 1
	return m.txn(func(tx kvTxn) error {
		tx.set(m.fmtKey("setting"), data)
		tx.set(m.inodeKey(1), m.marshal(&attr))
		if tx.incrBy(m.counterKey("nextInode"), 2) != 0 || tx.incrBy(m.counterKey("nextChunk"), 1) != 0 {
			return fmt.Errorf("counter was not zero")
		}
		return nil
	})
}

func (m *kvMeta) Load() (*Format, error) {
	body, err := m.get(m.fmtKey("setting"))
	if err != nil || body == nil {
		return nil, err
	}
	err = json.Unmarshal(body, &m.fmt)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &m.fmt, nil
}

func (m *kvMeta) NewSession() error {
	if m.conf.ReadOnly {
		return nil
	}
	v, err := m.incrCounter(m.counterKey("nextSession"), 1)
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	m.sid = uint64(v)
	logger.Debugf("session is %d", m.sid)
	info, err := newSessionInfo()
	if err != nil {
		return fmt.Errorf("new session info: %s", err)
	}
	info.MountPoint = m.conf.MountPoint
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	if err = m.setValue(m.sessionInfoKey(m.sid), data); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}

	// go m.refreshUsage() // NOTE: probably not need this anymore
	go m.refreshSession()
	// go m.cleanupDeletedFiles()
	// go m.cleanupSlices()
	go m.flushStats()
	return nil
}

func (m *kvMeta) refreshSession() {
	for {
		_ = m.setValue(m.sessionKey(m.sid), m.packTime(time.Now().Unix()))
		time.Sleep(time.Minute)
		if _, err := m.Load(); err != nil {
			logger.Warnf("reload setting: %s", err)
		}
		go m.cleanStaleSessions()
	}
}

func (m *kvMeta) cleanStaleSession(sid uint64) {
	// TODO: release locks
	keys, err := m.scanKeys(m.fmtKey(10, "SS", sid))
	if err != nil {
		logger.Warnf("scan stale session %d: %s", sid, err)
		return
	}
	var todel [][]byte
	for _, key := range keys {
		inode := m.parseInode(key)
		if err := m.deleteInode(inode); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
		} else {
			todel = append(todel, key)
		}
	}
	_, err = m.deleteKeys(todel...)
	if err == nil && len(keys) == len(todel) {
		_, err = m.deleteKeys(m.sessionKey(sid), m.sessionInfoKey(sid))
		logger.Infof("cleanup session %d: %s", sid, err)
	}
}

func (m *kvMeta) cleanStaleSessions() {
	// TODO: once per minute
	vals, err := m.scanValues(m.fmtKey(2, "SH"))
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	var ids []uint64
	for k, v := range vals {
		if m.parseTime(v) < time.Now().Add(time.Minute*-5).Unix() {
			ids = append(ids, m.parseSid([]byte(k)))
		}
	}
	for _, sid := range ids {
		m.cleanStaleSession(sid)
	}
}

func (m *kvMeta) GetSession(sid uint64) (*Session, error) {
	return nil, fmt.Errorf("not supported")
}

func (m *kvMeta) ListSessions() ([]*Session, error) {
	return nil, fmt.Errorf("not supported")
}

func (m *kvMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *kvMeta) flushStats() {
	for {
		newSpace := atomic.SwapInt64(&m.newSpace, 0)
		newInodes := atomic.SwapInt64(&m.newInodes, 0)
		if newSpace != 0 || newInodes != 0 {
			var used, inodes int64
			err := m.txn(func(tx kvTxn) error {
				used = tx.incrBy(m.counterKey(usedSpace), newSpace)
				inodes = tx.incrBy(m.counterKey(totalInodes), newInodes)
				return nil
			})
			if err == nil {
				if used+newSpace >= 0 {
					atomic.StoreInt64(&m.usedSpace, used+newSpace)
				} else {
					logger.Warnf("negative usedSpace: used %d newSpace %d", used, newSpace)
					atomic.StoreInt64(&m.usedSpace, 0)
				}
				if inodes+newInodes >= 0 {
					atomic.StoreInt64(&m.usedInodes, inodes+newInodes)
				} else {
					logger.Warnf("negative usedInodes: used %d newSpace %d", inodes, newInodes)
					atomic.StoreInt64(&m.usedInodes, 0)
				}
			} else {
				logger.Warnf("update stats: %s", err)
				m.updateStats(newSpace, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

/*
func (m *kvMeta) refreshUsage() {
	for {
		used, err := m.incrCounter(m.counterKey(usedSpace), 0)
		if err == nil {
			atomic.StoreInt64(&m.usedSpace, used)
		}
		inodes, err := m.incrCounter(m.counterKey(totalInodes), 0)
		if err == nil {
			atomic.StoreInt64(&m.usedInodes, inodes)
		}
		time.Sleep(time.Second * 10)
	}
}
*/

func (m *kvMeta) checkQuota(size, inodes int64) bool {
	if size > 0 && m.fmt.Capacity > 0 && atomic.LoadInt64(&m.usedSpace)+atomic.LoadInt64(&m.newSpace)+size > int64(m.fmt.Capacity) {
		return true
	}
	return inodes > 0 && m.fmt.Inodes > 0 && atomic.LoadInt64(&m.usedInodes)+atomic.LoadInt64(&m.newInodes)+inodes > int64(m.fmt.Inodes)
}

func (m *kvMeta) OnMsg(mtype uint32, cb MsgCallback) {
	m.msgCallbacks.Lock()
	defer m.msgCallbacks.Unlock()
	m.msgCallbacks.callbacks[mtype] = cb
}

/* TODO: add back later when needed
func (m *kvMeta) newMsg(mid uint32, args ...interface{}) error {
	m.msgCallbacks.Lock()
	cb, ok := m.msgCallbacks.callbacks[mid]
	m.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}
*/

func (m *kvMeta) shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(syscall.Errno); ok {
		return false
	}
	// TODO: add other retryable errors here
	return strings.Contains(err.Error(), "write conflict")
}

func (m *kvMeta) txn(f func(tx kvTxn) error) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	var err error
	for i := 0; i < 50; i++ {
		if err = m.client.txn(f); m.shouldRetry(err) {
			txRestart.Add(1)
			logger.Debugf("conflicted transaction, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		break
	}
	return err
}

func (m *kvMeta) setValue(key, value []byte) error {
	return m.txn(func(tx kvTxn) error {
		tx.set(key, value)
		return nil
	})
}

func (m *kvMeta) incrCounter(key []byte, value int64) (int64, error) {
	var old int64
	err := m.txn(func(tx kvTxn) error {
		old = tx.incrBy(key, value)
		return nil
	})
	return old, err
}

func (m *kvMeta) deleteKeys(keys ...[]byte) (int, error) {
	var count int
	err := m.txn(func(tx kvTxn) error {
		count = len(tx.gets(keys...))
		tx.dels(keys...)
		return nil
	})
	return count, err
}

func (m *kvMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	var used, inodes int64
	err := m.txn(func(tx kvTxn) error {
		used = tx.incrBy(m.counterKey(usedSpace), 0)
		inodes = tx.incrBy(m.counterKey(totalInodes), 0)
		return nil
	})
	if err != nil {
		logger.Warnf("get used space and inodes: %s", err)
		used = atomic.LoadInt64(&m.usedSpace)
		inodes = atomic.LoadInt64(&m.usedInodes)
	}
	used += atomic.LoadInt64(&m.newSpace)
	inodes += atomic.LoadInt64(&m.newInodes)
	if used < 0 {
		used = 0
	}
	used = ((used >> 16) + 1) << 16 // aligned to 64K
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
	}
	return 0
}

func (m *kvMeta) resolveCase(ctx Context, parent Ino, name string) *Entry {
	var entries []*Entry
	_ = m.Readdir(ctx, parent, 0, &entries)
	for _, e := range entries {
		n := string(e.Name)
		if strings.EqualFold(name, n) {
			return e
		}
	}
	return nil
}

func (m *kvMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	parent = m.checkRoot(parent)
	buf, err := m.get(m.entryKey(parent, name))
	if err != nil {
		return errno(err)
	}
	if buf == nil {
		if m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				if inode != nil {
					*inode = e.Inode
				}
				if attr != nil {
					return m.GetAttr(ctx, *inode, attr)
				}
				return 0
			}
		}
		return syscall.ENOENT
	}
	_, foundIno := m.parseEntry(buf)
	if inode != nil {
		*inode = foundIno
	}
	if attr != nil {
		a, err := m.get(m.inodeKey(foundIno))
		if err != nil {
			return errno(err)
		}
		m.parseAttr(a, attr)
	}
	return 0
}

func (r *kvMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) Access(ctx Context, inode Ino, mmask uint8, attr *Attr) syscall.Errno {
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
	mode := accessMode(attr, ctx.Uid(), ctx.Gid())
	if mode&mmask != mmask {
		logger.Debugf("Access inode %d %o, mode %o, request mode %o", inode, attr.Mode, mode, mmask)
		return syscall.EACCES
	}
	return 0
}

func (m *kvMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	inode = m.checkRoot(inode)
	if m.conf.OpenCache > 0 && m.of.Check(inode, attr) {
		return 0
	}
	a, err := m.get(m.inodeKey(inode))
	if err != nil && inode == 1 {
		err = nil
		attr.Typ = TypeDirectory
		attr.Mode = 0777
		attr.Nlink = 2
		attr.Length = 4 << 10
	}
	if err != nil {
		return errno(err)
	}
	if a == nil {
		return syscall.ENOENT
	}
	m.parseAttr(a, attr)
	if m.conf.OpenCache > 0 {
		m.of.Update(inode, attr)
	}
	return 0
}

func (m *kvMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	inode = m.checkRoot(inode)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return errno(m.txn(func(tx kvTxn) error {
		var cur Attr
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &cur)
		if (set&(SetAttrUID|SetAttrGID)) != 0 && (set&SetAttrMode) != 0 {
			attr.Mode |= (cur.Mode & 06000)
		}
		var changed bool
		if (cur.Mode&06000) != 0 && (set&(SetAttrUID|SetAttrGID)) != 0 {
			if cur.Mode&01777 != cur.Mode {
				cur.Mode &= 01777
				changed = true
			}
			attr.Mode &= 01777
		}
		if set&SetAttrUID != 0 && cur.Uid != attr.Uid {
			cur.Uid = attr.Uid
			changed = true
		}
		if set&SetAttrGID != 0 && cur.Gid != attr.Gid {
			cur.Gid = attr.Gid
			changed = true
		}
		if set&SetAttrMode != 0 {
			if ctx.Uid() != 0 && (attr.Mode&02000) != 0 {
				if ctx.Gid() != cur.Gid {
					attr.Mode &= 05777
				}
			}
			if attr.Mode != cur.Mode {
				cur.Mode = attr.Mode
				changed = true
			}
		}
		now := time.Now()
		if set&SetAttrAtime != 0 && (cur.Atime != attr.Atime || cur.Atimensec != attr.Atimensec) {
			cur.Atime = attr.Atime
			cur.Atimensec = attr.Atimensec
			changed = true
		}
		if set&SetAttrAtimeNow != 0 {
			cur.Atime = now.Unix()
			cur.Atimensec = uint32(now.Nanosecond())
			changed = true
		}
		if set&SetAttrMtime != 0 && (cur.Mtime != attr.Mtime || cur.Mtimensec != attr.Mtimensec) {
			cur.Mtime = attr.Mtime
			cur.Mtimensec = attr.Mtimensec
			changed = true
		}
		if set&SetAttrMtimeNow != 0 {
			cur.Mtime = now.Unix()
			cur.Mtimensec = uint32(now.Nanosecond())
			changed = true
		}
		if !changed {
			*attr = cur
			return nil
		}
		cur.Ctime = now.Unix()
		cur.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(&cur))
		*attr = cur
		return nil
	}))
}

func (m *kvMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		var t Attr
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &t)
		if t.Typ != TypeFile {
			return syscall.EPERM
		}
		if length == t.Length {
			if attr != nil {
				*attr = t
			}
			return nil
		}
		old := t.Length
		newSpace = align4K(length) - align4K(old)
		if length > old {
			if m.checkQuota(newSpace, 0) {
				return syscall.ENOSPC
			}
			if length/ChunkSize-old/ChunkSize > 1 {
				zeroChunks := tx.scanRange(m.chunkKey(inode, uint32(old/ChunkSize)+1), m.chunkKey(inode, uint32(length/ChunkSize)))
				buf := marshalSlice(0, 0, 0, 0, ChunkSize)
				for key, value := range zeroChunks {
					tx.set([]byte(key), append(value, buf...))
				}
			}
			l := uint32(length - old)
			if length > (old/ChunkSize+1)*ChunkSize {
				l = ChunkSize - uint32(old%ChunkSize)
			}
			tx.append(m.chunkKey(inode, uint32(old/ChunkSize)), marshalSlice(uint32(old%ChunkSize), 0, 0, 0, l))
			if length > (old/ChunkSize+1)*ChunkSize && length%ChunkSize > 0 {
				tx.append(m.chunkKey(inode, uint32(length/ChunkSize)), marshalSlice(0, 0, 0, 0, uint32(length%ChunkSize)))
			}
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(&t))
		if attr != nil {
			*attr = t
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
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
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		var t Attr
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &t)
		if t.Typ == TypeFIFO {
			return syscall.EPIPE
		}
		if t.Typ != TypeFile {
			return syscall.EPERM
		}
		length := t.Length
		if off+size > t.Length {
			if mode&fallocKeepSize == 0 {
				length = off + size
			}
		}

		old := t.Length
		newSpace = align4K(length) - align4K(t.Length)
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(&t))
		if mode&(fallocZeroRange|fallocPunchHole) != 0 {
			if off+size > old {
				size = old - off
			}
			for size > 0 {
				indx := uint32(off / ChunkSize)
				coff := off % ChunkSize
				l := size
				if coff+size > ChunkSize {
					l = ChunkSize - coff
				}
				tx.append(m.chunkKey(inode, indx), marshalSlice(uint32(coff), 0, 0, 0, uint32(l)))
				off += l
				size -= l
			}
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	target, err := m.get(m.symKey(inode))
	if err != nil {
		return errno(err)
	}
	if target == nil {
		return syscall.ENOENT
	}
	*path = target
	m.symlinks.Store(inode, target)
	return 0
}

func (m *kvMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *kvMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (m *kvMeta) mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	if m.checkQuota(4<<10, 1) {
		return syscall.ENOSPC
	}
	parent = m.checkRoot(parent)
	ino, err := m.nextInode()
	if err != nil {
		return errno(err)
	}
	if attr == nil {
		attr = &Attr{}
	}
	attr.Typ = _type
	attr.Mode = mode & ^cumask
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
	if inode != nil {
		*inode = ino
	}

	err = m.txn(func(tx kvTxn) error {
		var pattr Attr
		a := tx.get(m.inodeKey(parent))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}

		buf := tx.get(m.entryKey(parent, name))
		if buf != nil || buf == nil && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		now := time.Now()
		if _type == TypeDirectory {
			pattr.Nlink++
		}
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		attr.Atime = now.Unix()
		attr.Atimensec = uint32(now.Nanosecond())
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		if ctx.Value(CtxKey("behavior")) == "Hadoop" {
			attr.Gid = pattr.Gid
		}

		tx.set(m.entryKey(parent, name), m.packEntry(_type, ino))
		tx.set(m.inodeKey(parent), m.marshal(&pattr))
		tx.set(m.inodeKey(ino), m.marshal(attr))
		if _type == TypeSymlink {
			tx.set(m.symKey(ino), []byte(path))
		}
		return nil
	})
	if err == nil {
		m.updateStats(align4K(0), 1)
	}
	return errno(err)
}

func (m *kvMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	return m.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, inode, attr)
}

func (m *kvMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno {
	err := m.Mknod(ctx, parent, name, TypeFile, mode, cumask, 0, inode, attr)
	if err == 0 && inode != nil {
		m.of.Open(*inode, attr)
	}
	return err
}

func (m *kvMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	parent = m.checkRoot(parent)
	var newSpace, newInode int64
	err := m.txn(func(tx kvTxn) error {
		buf := tx.get(m.entryKey(parent, name))
		if buf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				name = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		if buf == nil {
			return syscall.ENOENT
		}
		_type, inode := m.parseEntry(buf)
		if _type == TypeDirectory {
			return syscall.EPERM
		}
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if len(rs) < 2 {
			return syscall.ENOENT
		}
		var pattr, attr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[1], &attr)
		if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
			return syscall.EACCES
		}
		defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		attr.Nlink--
		var opened bool
		if _type == TypeFile && attr.Nlink == 0 {
			opened = m.of.IsOpen(inode)
		}

		tx.dels(m.entryKey(parent, name))
		tx.dels(tx.scanKeys(m.xattrKey(inode, ""))...)
		tx.set(m.inodeKey(parent), m.marshal(&pattr))
		if attr.Nlink > 0 {
			tx.set(m.inodeKey(inode), m.marshal(&attr))
		} else {
			switch _type {
			case TypeFile:
				if opened {
					tx.set(m.inodeKey(inode), m.marshal(&attr))
					tx.set(m.sustainedKey(m.sid, inode), []byte{1})
				} else {
					tx.set(m.delfileKey(inode, attr.Length), m.packTime(now.Unix()))
					tx.dels(m.inodeKey(inode))
					newSpace, newInode = -align4K(attr.Length), -1
				}
			case TypeSymlink:
				tx.dels(m.symKey(inode))
				fallthrough
			default:
				tx.dels(m.inodeKey(inode))
				newSpace, newInode = -align4K(0), -1
			}
		}
		if _type == TypeFile && attr.Nlink == 0 {
			if opened {
				m.Lock()
				m.removedFiles[inode] = true
				m.Unlock()
			} else {
				go m.deleteFile(inode, attr.Length)
			}
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}
	parent = m.checkRoot(parent)
	err := m.txn(func(tx kvTxn) error {
		buf := tx.get(m.entryKey(parent, name))
		if buf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				name = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		if buf == nil {
			return syscall.ENOENT
		}
		_type, inode := m.parseEntry(buf)
		if _type != TypeDirectory {
			return syscall.ENOTDIR
		}
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if len(rs) < 2 {
			return syscall.ENOENT
		}
		var pattr, attr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if tx.exist(m.entryKey(inode, "")) {
			return syscall.ENOTEMPTY
		}
		m.parseAttr(rs[1], &attr)
		if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
			return syscall.EACCES
		}

		now := time.Now()
		pattr.Nlink--
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		tx.dels(m.entryKey(parent, name), m.inodeKey(inode))
		tx.dels(tx.scanKeys(m.xattrKey(inode, ""))...)
		tx.set(m.inodeKey(parent), m.marshal(&pattr))
		return nil
	})
	if err == nil {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *kvMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	parentSrc = m.checkRoot(parentSrc)
	parentDst = m.checkRoot(parentDst)
	var newSpace, newInode int64
	err := m.txn(func(tx kvTxn) error {
		buf := tx.get(m.entryKey(parentSrc, nameSrc))
		if buf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentSrc, nameSrc); e != nil {
				nameSrc = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		if buf == nil {
			return syscall.ENOENT
		}
		typ, ino := m.parseEntry(buf)
		if parentSrc == parentDst && nameSrc == nameDst {
			if inode != nil {
				*inode = ino
			}
			return nil
		}
		rs := tx.gets(m.inodeKey(parentSrc), m.inodeKey(parentDst), m.inodeKey(ino))
		if len(rs) < 3 {
			return syscall.ENOENT
		}
		var sattr, dattr, iattr, tattr Attr
		m.parseAttr(rs[0], &sattr)
		if sattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[1], &dattr)
		if dattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[2], &iattr)

		var opened bool
		var dino Ino
		var dtyp uint8
		dbuf := tx.get(m.entryKey(parentDst, nameDst))
		if dbuf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				nameDst = string(e.Name)
				dbuf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		if dbuf != nil {
			if ctx.Value(CtxKey("behavior")) == "Hadoop" {
				return syscall.EEXIST
			}
			dtyp, dino = m.parseEntry(dbuf)
			a := tx.get(m.inodeKey(dino))
			if a == nil {
				return syscall.ENOENT
			}
			m.parseAttr(a, &tattr)
			if dtyp == TypeDirectory {
				if tx.exist(m.entryKey(dino, "")) {
					return syscall.ENOTEMPTY
				}
			} else {
				tattr.Nlink--
				if tattr.Nlink > 0 {
					now := time.Now()
					tattr.Ctime = now.Unix()
					tattr.Ctimensec = uint32(now.Nanosecond())
				} else if dtyp == TypeFile {
					opened = m.of.IsOpen(dino)
				}
			}
			if ctx.Uid() != 0 && dattr.Mode&01000 != 0 && ctx.Uid() != dattr.Uid && ctx.Uid() != tattr.Uid {
				return syscall.EACCES
			}
		}
		if ctx.Uid() != 0 && sattr.Mode&01000 != 0 && ctx.Uid() != sattr.Uid && ctx.Uid() != iattr.Uid {
			return syscall.EACCES
		}

		now := time.Now()
		sattr.Mtime = now.Unix()
		sattr.Mtimensec = uint32(now.Nanosecond())
		sattr.Ctime = now.Unix()
		sattr.Ctimensec = uint32(now.Nanosecond())
		dattr.Mtime = now.Unix()
		dattr.Mtimensec = uint32(now.Nanosecond())
		dattr.Ctime = now.Unix()
		dattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Parent = parentDst
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		if typ == TypeDirectory && parentSrc != parentDst {
			sattr.Nlink--
			dattr.Nlink++
		}
		if inode != nil {
			*inode = ino
		}
		if attr != nil {
			*attr = iattr
		}

		tx.dels(m.entryKey(parentSrc, nameSrc))
		if dino > 0 {
			if dtyp != TypeDirectory && tattr.Nlink > 0 {
				tx.set(m.inodeKey(dino), m.marshal(&tattr))
			} else {
				if dtyp == TypeFile {
					if opened {
						tx.set(m.inodeKey(dino), m.marshal(&tattr))
						tx.set(m.sustainedKey(m.sid, dino), []byte{1})
					} else {
						tx.set(m.delfileKey(dino, tattr.Length), m.packTime(now.Unix()))
						tx.dels(m.inodeKey(dino))
						newSpace, newInode = -align4K(tattr.Length), -1
					}
				} else {
					if dtyp == TypeDirectory {
						dattr.Nlink--
					} else if dtyp == TypeSymlink {
						tx.dels(m.symKey(dino))
					}
					tx.dels(m.inodeKey(dino))
					newSpace, newInode = -align4K(0), -1
				}
				tx.dels(tx.scanKeys(m.xattrKey(dino, ""))...)
			}
			tx.dels(m.entryKey(parentDst, nameDst))
		}
		tx.set(m.entryKey(parentDst, nameDst), buf)
		tx.set(m.inodeKey(parentSrc), m.marshal(&sattr))
		tx.set(m.inodeKey(ino), m.marshal(&iattr))
		if parentDst != parentSrc {
			tx.set(m.inodeKey(parentDst), m.marshal(&dattr))
		}
		if dino > 0 && dtyp == TypeFile {
			if opened {
				m.Lock()
				m.removedFiles[dino] = true
				m.Unlock()
			} else {
				go m.deleteFile(dino, tattr.Length)
			}
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	parent = m.checkRoot(parent)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return errno(m.txn(func(tx kvTxn) error {
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if len(rs) < 2 {
			return syscall.ENOENT
		}
		var pattr, iattr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[1], &iattr)
		if iattr.Typ == TypeDirectory {
			return syscall.EPERM
		}
		buf := tx.get(m.entryKey(parent, name))
		if buf != nil || buf == nil && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Nlink++
		tx.set(m.entryKey(parent, name), m.packEntry(iattr.Typ, inode))
		tx.set(m.inodeKey(parent), m.marshal(&pattr))
		tx.set(m.inodeKey(inode), m.marshal(&iattr))
		if attr != nil {
			*attr = iattr
		}
		return nil
	}))
}

func (m *kvMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	inode = m.checkRoot(inode)
	var attr Attr
	if err := m.GetAttr(ctx, inode, &attr); err != 0 {
		return err
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

	// TODO: handle big directory
	vals, err := m.scanValues(m.entryKey(inode, ""))
	if err != nil {
		return errno(err)
	}
	prefix := len(m.entryKey(inode, ""))
	for name, buf := range vals {
		typ, inode := m.parseEntry(buf)
		*entries = append(*entries, &Entry{
			Inode: inode,
			Name:  []byte(name)[prefix:],
			Attr:  &Attr{Typ: typ},
		})
	}

	if plus != 0 {
		fillAttr := func(es []*Entry) error {
			var keys = make([][]byte, len(es))
			for i, e := range es {
				keys[i] = m.inodeKey(e.Inode)
			}
			var rs [][]byte
			err := m.txn(func(tx kvTxn) error {
				rs = tx.gets(keys...)
				return nil
			})
			if err != nil {
				return err
			}
			for j, re := range rs {
				if re != nil {
					m.parseAttr(re, es[j].Attr)
				}
			}
			return nil
		}
		batchSize := 4096
		nEntries := len(*entries)
		if nEntries <= batchSize {
			err = fillAttr(*entries)
		} else {
			indexCh := make(chan []*Entry, 10)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for es := range indexCh {
						if e := fillAttr(es); e != nil {
							err = e
							break
						}
					}
				}()
			}
			for i := 0; i < nEntries; i += batchSize {
				if i+batchSize > nEntries {
					indexCh <- (*entries)[i:]
				} else {
					indexCh <- (*entries)[i : i+batchSize]
				}
			}
			close(indexCh)
			wg.Wait()
		}
		if err != nil {
			return errno(err)
		}
	}
	return 0
}

func (m *kvMeta) deleteInode(inode Ino) error {
	var attr Attr
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return nil
		}
		m.parseAttr(a, &attr)
		tx.set(m.delfileKey(inode, attr.Length), m.packTime(time.Now().Unix()))
		tx.dels(m.inodeKey(inode))
		newSpace = -align4K(attr.Length)
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		go m.deleteFile(inode, attr.Length)
	}
	return err
}

func (m *kvMeta) Open(ctx Context, inode Ino, flags uint32, attr *Attr) syscall.Errno {
	if m.conf.ReadOnly && flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0 {
		return syscall.EROFS
	}
	if m.conf.OpenCache > 0 && m.of.OpenCheck(inode, attr) {
		return 0
	}
	var err syscall.Errno
	if attr != nil && !attr.Full {
		err = m.GetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.of.Open(inode, attr)
	}
	return 0
}

func (m *kvMeta) Close(ctx Context, inode Ino) syscall.Errno {
	if m.of.Close(inode) {
		m.Lock()
		defer m.Unlock()
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
			go func() {
				if err := m.deleteInode(inode); err == nil {
					_, _ = m.deleteKeys(m.sustainedKey(m.sid, inode))
				}
			}()
		}
	}
	return 0
}

func (m *kvMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	if cs, ok := m.of.ReadChunk(inode, indx); ok {
		*chunks = cs
		return 0
	}
	val, err := m.get(m.chunkKey(inode, indx))
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(val)
	*chunks = buildSlice(ss)
	m.of.CacheChunk(inode, indx, *chunks)
	if !m.conf.ReadOnly && (len(val)/sliceBytes >= 5 || len(*chunks) >= 5) {
		go m.compactChunk(inode, indx, false)
	}
	return 0
}

func (m *kvMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	m.of.InvalidateChunk(inode, indx)
	return 0
}

func (m *kvMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		var attr Attr
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &attr)
		if attr.Typ != TypeFile {
			return syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		if newleng > attr.Length {
			newSpace = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		now := time.Now()
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		val := tx.append(m.chunkKey(inode, indx), marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len))
		tx.set(m.inodeKey(inode), m.marshal(&attr))
		if (len(val)/sliceBytes)%20 == 0 {
			go m.compactChunk(inode, indx, false)
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	return syscall.ENOTSUP
}

/* TODO
func (m *dbMeta) cleanupDeletedFiles() {}

func (m *dbMeta) cleanupSlices() {}

func (m *dbMeta) deleteSlice(chunkid uint64, size uint32) {}

func (m *dbMeta) deleteChunk(inode Ino, indx uint32) error {}
*/

func (m *kvMeta) deleteFile(inode Ino, length uint64) {}

func (m *kvMeta) compactChunk(inode Ino, indx uint32, force bool) {}

func (m *kvMeta) CompactAll(ctx Context) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) ListSlices(ctx Context, slices *[]Slice, delete bool) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	inode = m.checkRoot(inode)
	buf, err := m.get(m.xattrKey(inode, name))
	if err != nil {
		return errno(err)
	}
	if buf == nil {
		return ENOATTR
	}
	*vbuff = buf
	return 0
}

func (m *kvMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	inode = m.checkRoot(inode)
	keys, err := m.scanKeys(m.xattrKey(inode, ""))
	if err != nil {
		return errno(err)
	}
	*names = nil
	prefix := len(m.xattrKey(inode, ""))
	for _, name := range keys {
		*names = append(*names, name[prefix:]...)
		*names = append(*names, 0)
	}
	return 0
}

func (m *kvMeta) SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	inode = m.checkRoot(inode)
	return errno(m.setValue(m.xattrKey(inode, name), value))
}

func (m *kvMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	inode = m.checkRoot(inode)
	n, err := m.deleteKeys(m.xattrKey(inode, name))
	if err != nil {
		return errno(err)
	} else if n == 0 {
		return ENOATTR
	} else {
		return 0
	}
}

func (m *kvMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *kvMeta) DumpMeta(w io.Writer) error {
	return syscall.ENOTSUP
}
func (m *kvMeta) LoadMeta(r io.Reader) error {
	return syscall.ENOTSUP
}
