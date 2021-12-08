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
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sort"
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
	scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte
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
	umounting    bool

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID
}

func newKVMeta(driver, addr string, conf *Config) (Meta, error) {
	p := strings.Index(addr, "/")
	var prefix string
	if p > 0 {
		prefix = addr[p+1:]
		addr = addr[:p]
	}
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
	if driver != "memkv" {
		m.client = withPrefix(m.client, append([]byte(prefix), 0xFD))
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
	b := utils.NewBuffer(uint32(m.keyLen(args...)))
	for _, a := range args {
		switch a := a.(type) {
		case byte:
			b.Put8(a)
		case uint32:
			b.Put32(a)
		case uint64:
			b.Put64(a)
		case Ino:
			m.encodeInode(a, b.Get(8))
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
  Length llllllll
  Indx nnnn
  name ...
  chunkid cccccccc
  session  ssssssss

All keys:
  setting            format
  C...               counter
  AiiiiiiiiI         inode attribute
  AiiiiiiiiD...      dentry
  AiiiiiiiiCnnnn     file chunks
  AiiiiiiiiS         symlink target
  AiiiiiiiiX...      extented attribute
  Diiiiiiiillllllll  delete inodes
  Fiiiiiiii          Flocks
  Piiiiiiii          POSIX locks
  Kccccccccnnnn      slice refs
  SHssssssss         session heartbeat
  SIssssssss         session info
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
	return m.fmtKey("K", chunkid, size)
}

func (m *kvMeta) symKey(inode Ino) []byte {
	return m.fmtKey("A", inode, "S")
}

func (m *kvMeta) xattrKey(inode Ino, name string) []byte {
	return m.fmtKey("A", inode, "X", name)
}

func (m *kvMeta) flockKey(inode Ino) []byte {
	return m.fmtKey("F", inode)
}

func (m *kvMeta) plockKey(inode Ino) []byte {
	return m.fmtKey("P", inode)
}

func (m *kvMeta) sessionKey(sid uint64) []byte {
	return m.fmtKey("SH", sid)
}

func (m *kvMeta) parseSid(key string) uint64 {
	buf := []byte(key[2:]) // "SH"
	if len(buf) != 8 {
		panic("invalid sid value")
	}
	return binary.BigEndian.Uint64(buf)
}

func (m *kvMeta) sessionInfoKey(sid uint64) []byte {
	return m.fmtKey("SI", sid)
}

func (m *kvMeta) sustainedKey(sid uint64, inode Ino) []byte {
	return m.fmtKey("SS", sid, inode)
}

func (m *kvMeta) encodeInode(ino Ino, buf []byte) {
	binary.LittleEndian.PutUint64(buf, uint64(ino))
}

func (m *kvMeta) decodeInode(buf []byte) Ino {
	return Ino(binary.LittleEndian.Uint64(buf))
}

func (m *kvMeta) delfileKey(inode Ino, length uint64) []byte {
	return m.fmtKey("D", inode, length)
}

func (m *kvMeta) counterKey(key string) []byte {
	return m.fmtKey("C", key)
}

func (m *kvMeta) packInt64(value int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(value))
	return b
}

func (m *kvMeta) parseInt64(buf []byte) int64 {
	if len(buf) == 0 {
		return 0
	}
	if len(buf) != 8 {
		panic("invalid value")
	}
	return int64(binary.BigEndian.Uint64(buf))
}

func packCounter(value int64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(value))
	return b
}

func parseCounter(buf []byte) int64 {
	if len(buf) == 0 {
		return 0
	}
	if len(buf) != 8 {
		panic("invalid counter value")
	}
	return int64(binary.LittleEndian.Uint64(buf))
}

func (m *kvMeta) packEntry(_type uint8, inode Ino) []byte {
	b := utils.NewBuffer(9)
	b.Put8(_type)
	b.Put64(uint64(inode))
	return b.Bytes()
}

func (m *kvMeta) parseEntry(buf []byte) (uint8, Ino) {
	b := utils.FromBuffer(buf)
	return b.Get8(), Ino(b.Get64())
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
	attr.Parent = Ino(rb.Get64())
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
	err := m.client.txn(func(tx kvTxn) error {
		value = tx.get(key)
		return nil
	})
	return value, err
}

func (m *kvMeta) getCounter(key []byte) (int64, error) {
	var value int64
	err := m.client.txn(func(tx kvTxn) error {
		value = tx.incrBy(key, 0)
		return nil
	})
	return value, err
}

func (m *kvMeta) scanKeys(prefix []byte) ([][]byte, error) {
	var keys [][]byte
	err := m.client.txn(func(tx kvTxn) error {
		keys = tx.scanKeys(prefix)
		return nil
	})
	return keys, err
}

func (m *kvMeta) scanValues(prefix []byte, filter func(k, v []byte) bool) (map[string][]byte, error) {
	var values map[string][]byte
	err := m.client.txn(func(tx kvTxn) error {
		values = tx.scanValues(prefix, filter)
		return nil
	})
	return values, err
}

func (m *kvMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next >= m.freeInodes.maxid {
		v, err := m.incrCounter(m.counterKey("nextInode"), 100)
		if err != nil {
			return 0, err
		}
		m.freeInodes.next = uint64(v) - 100
		m.freeInodes.maxid = uint64(v)
	}
	n := m.freeInodes.next
	m.freeInodes.next++
	return Ino(n), nil
}

func (m *kvMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeChunks.next >= m.freeChunks.maxid {
		v, err := m.incrCounter(m.counterKey("nextChunk"), 1000)
		if err != nil {
			return errno(err)
		}
		m.freeChunks.next = uint64(v) - 1000
		m.freeChunks.maxid = uint64(v)
	}
	*chunkid = m.freeChunks.next
	m.freeChunks.next++
	return 0
}

func (m *kvMeta) Init(format Format, force bool) error {
	body, err := m.get(m.fmtKey("setting"))
	if err != nil {
		return err
	}

	if body != nil {
		var old Format
		err = json.Unmarshal(body, &old)
		if err != nil {
			return fmt.Errorf("json: %s", err)
		}
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
			if format != old {
				old.SecretKey = ""
				format.SecretKey = ""
				return fmt.Errorf("cannot update format from %+v to %+v", old, format)
			}
		}
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}

	m.fmt = format
	return m.txn(func(tx kvTxn) error {
		tx.set(m.fmtKey("setting"), data)
		if body == nil || m.client.name() == "memkv" {
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
			tx.set(m.inodeKey(1), m.marshal(&attr))
			tx.incrBy(m.counterKey("nextInode"), 2)
			tx.incrBy(m.counterKey("nextChunk"), 1)
			tx.incrBy(m.counterKey("nextSession"), 1)
		}
		return nil
	})
}

func (m *kvMeta) Load() (*Format, error) {
	body, err := m.get(m.fmtKey("setting"))
	if err == nil && body == nil {
		err = fmt.Errorf("database is not formatted")
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &m.fmt)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &m.fmt, nil
}

func (m *kvMeta) NewSession() error {
	go m.refreshUsage()
	if m.conf.ReadOnly {
		return nil
	}
	v, err := m.incrCounter(m.counterKey("nextSession"), 1)
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	m.sid = uint64(v)
	logger.Debugf("session is %d", m.sid)
	_ = m.setValue(m.sessionKey(m.sid), m.packInt64(time.Now().Unix()))
	info := newSessionInfo()
	info.MountPoint = m.conf.MountPoint
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	if err = m.setValue(m.sessionInfoKey(m.sid), data); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}

	go m.refreshSession()
	go m.cleanupDeletedFiles()
	go m.cleanupSlices()
	go m.flushStats()
	return nil
}

func (m *kvMeta) CloseSession() error {
	if m.conf.ReadOnly {
		return nil
	}
	m.Lock()
	m.umounting = true
	m.Unlock()
	m.cleanStaleSession(m.sid, true)
	return nil
}

func (m *kvMeta) refreshSession() {
	for {
		time.Sleep(time.Minute)
		m.Lock()
		if m.umounting {
			m.Unlock()
			return
		}
		_ = m.setValue(m.sessionKey(m.sid), m.packInt64(time.Now().Unix()))
		m.Unlock()
		if _, err := m.Load(); err != nil {
			logger.Warnf("reload setting: %s", err)
		}
		go m.cleanStaleSessions()
	}
}

func (m *kvMeta) cleanStaleSession(sid uint64, sync bool) {
	// release locks
	flocks, err := m.scanValues(m.fmtKey("F"), nil)
	if err != nil {
		logger.Warnf("scan flock for stale session %d: %s", sid, err)
		return
	}
	for k, v := range flocks {
		ls := unmarshalFlock(v)
		for o := range ls {
			if o.sid == sid {
				err = m.txn(func(tx kvTxn) error {
					v := tx.get([]byte(k))
					ls := unmarshalFlock(v)
					delete(ls, o)
					if len(ls) > 0 {
						tx.set([]byte(k), marshalFlock(ls))
					} else {
						tx.dels([]byte(k))
					}
					return nil
				})
				if err != nil {
					logger.Warnf("remove flock for stale session %d: %s", sid, err)
					return
				}
			}
		}
	}
	plocks, err := m.scanValues(m.fmtKey("P"), nil)
	if err != nil {
		logger.Warnf("scan plock for stale session %d: %s", sid, err)
		return
	}
	for k, v := range plocks {
		ls := unmarshalPlock(v)
		for o := range ls {
			if o.sid == sid {
				err = m.txn(func(tx kvTxn) error {
					v := tx.get([]byte(k))
					ls := unmarshalPlock(v)
					delete(ls, o)
					if len(ls) > 0 {
						tx.set([]byte(k), marshalPlock(ls))
					} else {
						tx.dels([]byte(k))
					}
					return nil
				})
				if err != nil {
					logger.Warnf("remove plock for stale session %d: %s", sid, err)
					return
				}
			}
		}
	}

	keys, err := m.scanKeys(m.fmtKey("SS", sid))
	if err != nil {
		logger.Warnf("scan stale session %d: %s", sid, err)
		return
	}
	var todel [][]byte
	for _, key := range keys {
		inode := m.decodeInode(key[10:]) // "SS" + sid
		if err := m.deleteInode(inode, sync); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
		} else {
			todel = append(todel, key)
		}
	}
	err = m.deleteKeys(todel...)
	if err == nil && len(keys) == len(todel) {
		err = m.deleteKeys(m.sessionKey(sid), m.sessionInfoKey(sid))
		logger.Infof("cleanup session %d: %s", sid, err)
	}
}

func (m *kvMeta) cleanStaleSessions() {
	vals, err := m.scanValues(m.fmtKey("SH"), nil)
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	var ids []uint64
	for k, v := range vals {
		if m.parseInt64(v) < time.Now().Add(time.Minute*-5).Unix() {
			ids = append(ids, m.parseSid(k))
		}
	}
	for _, sid := range ids {
		m.cleanStaleSession(sid, false)
	}
}

func (m *kvMeta) getSession(sid uint64, detail bool) (*Session, error) {
	info, err := m.get(m.sessionInfoKey(sid))
	if err != nil {
		return nil, err
	}
	if info == nil {
		info = []byte("{}")
	}
	var s Session
	if err = json.Unmarshal(info, &s); err != nil {
		return nil, fmt.Errorf("corrupted session info; json error: %s", err)
	}
	s.Sid = sid
	if detail {
		inodes, err := m.scanKeys(m.fmtKey("SS", sid))
		if err != nil {
			return nil, err
		}
		s.Sustained = make([]Ino, 0, len(inodes))
		for _, sinode := range inodes {
			inode := m.decodeInode(sinode[10:]) // "SS" + sid
			s.Sustained = append(s.Sustained, inode)
		}
		flocks, err := m.scanValues(m.fmtKey("F"), nil)
		if err != nil {
			return nil, err
		}
		for k, v := range flocks {
			inode := m.decodeInode([]byte(k[1:])) // "F"
			ls := unmarshalFlock(v)
			for o, l := range ls {
				if o.sid == sid {
					s.Flocks = append(s.Flocks, Flock{inode, o.sid, string(l)})
				}
			}
		}
		plocks, err := m.scanValues(m.fmtKey("P"), nil)
		if err != nil {
			return nil, err
		}
		for k, v := range plocks {
			inode := m.decodeInode([]byte(k[1:])) // "P"
			ls := unmarshalPlock(v)
			for o, l := range ls {
				if o.sid == sid {
					s.Plocks = append(s.Plocks, Plock{inode, o.sid, l})
				}
			}
		}
	}
	return &s, nil
}

func (m *kvMeta) GetSession(sid uint64) (*Session, error) {
	value, err := m.get(m.sessionKey(sid))
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("session not found: %d", sid)
	}
	s, err := m.getSession(sid, true)
	if err != nil {
		return nil, err
	}
	s.Heartbeat = time.Unix(m.parseInt64(value), 0)
	return s, nil
}

func (m *kvMeta) ListSessions() ([]*Session, error) {
	vals, err := m.scanValues(m.fmtKey("SH"), nil)
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(vals))
	for k, v := range vals {
		s, err := m.getSession(m.parseSid(k), false)
		if err != nil {
			logger.Errorf("get session: %s", err)
			continue
		}
		s.Heartbeat = time.Unix(m.parseInt64(v), 0)
		sessions = append(sessions, s)
	}
	return sessions, nil
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
			err := m.txn(func(tx kvTxn) error {
				tx.incrBy(m.counterKey(usedSpace), newSpace)
				tx.incrBy(m.counterKey(totalInodes), newInodes)
				return nil
			})
			if err != nil {
				logger.Warnf("update stats: %s", err)
				m.updateStats(newSpace, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *kvMeta) refreshUsage() {
	for {
		used, err := m.getCounter(m.counterKey(usedSpace))
		if err == nil {
			atomic.StoreInt64(&m.usedSpace, used)
		}
		inodes, err := m.getCounter(m.counterKey(totalInodes))
		if err == nil {
			atomic.StoreInt64(&m.usedInodes, inodes)
		}
		time.Sleep(time.Second * 10)
	}
}

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

func (m *kvMeta) newMsg(mid uint32, args ...interface{}) error {
	m.msgCallbacks.Lock()
	cb, ok := m.msgCallbacks.callbacks[mid]
	m.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (m *kvMeta) shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(syscall.Errno); ok {
		return false
	}
	// TODO: add other retryable errors here
	return strings.Contains(err.Error(), "write conflict") || strings.Contains(err.Error(), "TxnLockNotFound")
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
			time.Sleep(time.Millisecond * time.Duration(rand.Int()%((i+1)*(i+1))))
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
	var new int64
	err := m.txn(func(tx kvTxn) error {
		new = tx.incrBy(key, value)
		return nil
	})
	return new, err
}

func (m *kvMeta) deleteKeys(keys ...[]byte) error {
	if len(keys) == 0 {
		return nil
	}
	return m.txn(func(tx kvTxn) error {
		tx.dels(keys...)
		return nil
	})
}

func (m *kvMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	defer timeit(time.Now())
	var used, inodes int64
	err := m.client.txn(func(tx kvTxn) error {
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
		for *iused*10 > (*iused+*iavail)*8 {
			*iavail *= 2
		}
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
	if inode == nil || attr == nil {
		return syscall.EINVAL // bad request
	}
	defer timeit(time.Now())
	var foundIno Ino
	var foundType uint8
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
		st := m.GetAttr(ctx, parent, attr)
		if st != 0 {
			return st
		}
		if attr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		*inode = parent
		return 0
	}
	buf, err := m.get(m.entryKey(parent, name))
	if err != nil {
		return errno(err)
	}
	if buf == nil {
		if m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				foundIno = e.Inode
				foundType = e.Attr.Typ
				name = string(e.Name)
			}
		}
	} else {
		foundType, foundIno = m.parseEntry(buf)
	}
	if foundIno == 0 {
		return syscall.ENOENT
	}
	*inode = foundIno
	st := m.GetAttr(ctx, *inode, attr)
	if st == syscall.ENOENT {
		logger.Warnf("no attribute for inode %d (%d, %s)", foundIno, parent, name)
		*attr = Attr{Typ: foundType}
		st = 0
	}
	return st
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
	defer timeit(time.Now())
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
	m.of.Update(inode, attr)
	return 0
}

func (m *kvMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
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
			if ctx.Uid() != 0 || (cur.Mode>>3)&1 != 0 {
				// clear SUID and SGID
				cur.Mode &= 01777
				attr.Mode &= 01777
			} else {
				// keep SGID if the file is non-group-executable
				cur.Mode &= 03777
				attr.Mode &= 03777
			}
			changed = true
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
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
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
		if t.Typ != TypeFile {
			return syscall.EPERM
		}
		if length == t.Length {
			if attr != nil {
				*attr = t
			}
			return nil
		}
		newSpace = align4K(length) - align4K(t.Length)
		if newSpace > 0 && m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		var left, right = t.Length, length
		if left > right {
			right, left = left, right
		}
		if right/ChunkSize-left/ChunkSize > 1 {
			zeroChunks := tx.scanRange(m.chunkKey(inode, uint32(left/ChunkSize)+1), m.chunkKey(inode, uint32(right/ChunkSize)))
			buf := marshalSlice(0, 0, 0, 0, ChunkSize)
			for key, value := range zeroChunks {
				tx.set([]byte(key), append(value, buf...))
			}
		}
		l := uint32(right - left)
		if right > (left/ChunkSize+1)*ChunkSize {
			l = ChunkSize - uint32(left%ChunkSize)
		}
		tx.append(m.chunkKey(inode, uint32(left/ChunkSize)), marshalSlice(uint32(left%ChunkSize), 0, 0, 0, l))
		if right > (left/ChunkSize+1)*ChunkSize && right%ChunkSize > 0 {
			tx.append(m.chunkKey(inode, uint32(right/ChunkSize)), marshalSlice(0, 0, 0, 0, uint32(right%ChunkSize)))
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
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
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
	defer timeit(time.Now())
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
	defer timeit(time.Now())
	return m.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *kvMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
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
		var foundIno Ino
		var foundType uint8
		if buf != nil {
			foundType, foundIno = m.parseEntry(buf)
		} else if m.conf.CaseInsensi {
			if entry := m.resolveCase(ctx, parent, name); entry != nil {
				foundType, foundIno = entry.Attr.Typ, entry.Inode
			}
		}
		if foundIno != 0 {
			if _type == TypeFile {
				a = tx.get(m.inodeKey(foundIno))
				if a != nil {
					m.parseAttr(a, attr)
				} else {
					*attr = Attr{Typ: foundType, Parent: parent} // corrupt entry
				}
				if inode != nil {
					*inode = foundIno
				}
			}
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
		if pattr.Mode&02000 != 0 || ctx.Value(CtxKey("behavior")) == "Hadoop" || runtime.GOOS == "darwin" {
			attr.Gid = pattr.Gid
			if _type == TypeDirectory && runtime.GOOS == "linux" {
				attr.Mode |= pattr.Mode & 02000
			}
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
	defer timeit(time.Now())
	return m.mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, "", inode, attr)
}

func (m *kvMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	if attr == nil {
		attr = &Attr{}
	}
	err := m.mknod(ctx, parent, name, TypeFile, mode, cumask, 0, "", inode, attr)
	if err == syscall.EEXIST && (flags&syscall.O_EXCL) == 0 && attr.Typ == TypeFile {
		err = 0
	}
	if err == 0 && inode != nil {
		m.of.Open(*inode, attr)
	}
	return err
}

func (m *kvMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	var _type uint8
	var inode Ino
	var attr Attr
	var opened bool
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
		_type, inode = m.parseEntry(buf)
		if _type == TypeDirectory {
			return syscall.EPERM
		}
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if rs[0] == nil {
			return syscall.ENOENT
		}
		var pattr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		attr = Attr{}
		opened = false
		now := time.Now()
		if rs[1] != nil {
			m.parseAttr(rs[1], &attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			attr.Ctime = now.Unix()
			attr.Ctimensec = uint32(now.Nanosecond())
			attr.Nlink--
			if _type == TypeFile && attr.Nlink == 0 {
				opened = m.of.IsOpen(inode)
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
		}
		defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())

		tx.dels(m.entryKey(parent, name))
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
					tx.set(m.delfileKey(inode, attr.Length), m.packInt64(now.Unix()))
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
			tx.dels(tx.scanKeys(m.xattrKey(inode, ""))...)
		}
		return nil
	})
	if err == nil {
		if _type == TypeFile && attr.Nlink == 0 {
			if opened {
				m.Lock()
				m.removedFiles[inode] = true
				m.Unlock()
			} else {
				go m.deleteFile(inode, attr.Length)
			}
		}
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
	defer timeit(time.Now())
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
		if rs[0] == nil {
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
		if rs[1] != nil {
			m.parseAttr(rs[1], &attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
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

func (m *kvMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	switch flags {
	case 0, RenameNoReplace, RenameExchange:
	case RenameWhiteout, RenameNoReplace | RenameWhiteout:
		return syscall.ENOTSUP
	default:
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	exchange := flags == RenameExchange
	parentSrc = m.checkRoot(parentSrc)
	parentDst = m.checkRoot(parentDst)
	var opened bool
	var dino Ino
	var dtyp uint8
	var tattr Attr
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
		if rs[0] == nil || rs[1] == nil || rs[2] == nil {
			return syscall.ENOENT
		}
		var sattr, dattr, iattr Attr
		m.parseAttr(rs[0], &sattr)
		if sattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[1], &dattr)
		if dattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		m.parseAttr(rs[2], &iattr)

		dbuf := tx.get(m.entryKey(parentDst, nameDst))
		if dbuf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				nameDst = string(e.Name)
				dbuf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		now := time.Now()
		tattr = Attr{}
		opened = false
		if dbuf != nil {
			if flags == RenameNoReplace {
				return syscall.EEXIST
			}
			dtyp, dino = m.parseEntry(dbuf)
			a := tx.get(m.inodeKey(dino))
			if a == nil {
				return syscall.ENOENT // corrupt entry
			}
			m.parseAttr(a, &tattr)
			if exchange {
				tattr.Ctime = now.Unix()
				tattr.Parent = parentSrc
				tattr.Ctimensec = uint32(now.Nanosecond())
				if dtyp == TypeDirectory && parentSrc != parentDst {
					dattr.Nlink--
					sattr.Nlink++
				}
			} else {
				if dtyp == TypeDirectory {
					if tx.exist(m.entryKey(dino, "")) {
						return syscall.ENOTEMPTY
					}
				} else {
					tattr.Nlink--
					if tattr.Nlink > 0 {
						tattr.Ctime = now.Unix()
						tattr.Ctimensec = uint32(now.Nanosecond())
					} else if dtyp == TypeFile {
						opened = m.of.IsOpen(dino)
					}
				}
			}
			if ctx.Uid() != 0 && dattr.Mode&01000 != 0 && ctx.Uid() != dattr.Uid && ctx.Uid() != tattr.Uid {
				return syscall.EACCES
			}
		} else {
			if exchange {
				return syscall.ENOENT
			}
			dino, dtyp = 0, 0
		}
		if ctx.Uid() != 0 && sattr.Mode&01000 != 0 && ctx.Uid() != sattr.Uid && ctx.Uid() != iattr.Uid {
			return syscall.EACCES
		}

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

		if exchange { // dino > 0
			tx.set(m.entryKey(parentSrc, nameSrc), dbuf)
			tx.set(m.inodeKey(dino), m.marshal(&tattr))
		} else {
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
							tx.set(m.delfileKey(dino, tattr.Length), m.packInt64(now.Unix()))
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
			}
		}
		if parentDst != parentSrc {
			tx.set(m.inodeKey(parentSrc), m.marshal(&sattr))
		}
		tx.set(m.inodeKey(ino), m.marshal(&iattr))
		tx.set(m.entryKey(parentDst, nameDst), buf)
		tx.set(m.inodeKey(parentDst), m.marshal(&dattr))
		return nil
	})
	if err == nil && !exchange {
		if dino > 0 && dtyp == TypeFile && tattr.Nlink == 0 {
			if opened {
				m.Lock()
				m.removedFiles[dino] = true
				m.Unlock()
			} else {
				go m.deleteFile(dino, tattr.Length)
			}
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return errno(m.txn(func(tx kvTxn) error {
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if rs[0] == nil || rs[1] == nil {
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

	// TODO: handle big directory
	vals, err := m.scanValues(m.entryKey(inode, ""), nil)
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
			err := m.client.txn(func(tx kvTxn) error {
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

func (m *kvMeta) deleteInode(inode Ino, sync bool) error {
	var attr Attr
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return nil
		}
		m.parseAttr(a, &attr)
		tx.set(m.delfileKey(inode, attr.Length), m.packInt64(time.Now().Unix()))
		tx.dels(m.inodeKey(inode))
		newSpace = -align4K(attr.Length)
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		if sync {
			m.deleteFile(inode, attr.Length)
		} else {
			go m.deleteFile(inode, attr.Length)
		}
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
	return err
}

func (m *kvMeta) Close(ctx Context, inode Ino) syscall.Errno {
	if m.of.Close(inode) {
		m.Lock()
		defer m.Unlock()
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
			go func() {
				if err := m.deleteInode(inode, false); err == nil {
					_ = m.deleteKeys(m.sustainedKey(m.sid, inode))
				}
			}()
		}
	}
	return 0
}

func (m *kvMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	f := m.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if cs, ok := m.of.ReadChunk(inode, indx); ok {
		*chunks = cs
		return 0
	}
	defer timeit(time.Now())
	val, err := m.get(m.chunkKey(inode, indx))
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(val)
	if ss == nil {
		return syscall.EIO
	}
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
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var newSpace int64
	var needCompact bool
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
		needCompact = (len(val)/sliceBytes)%100 == 99
		return nil
	})
	if err == nil {
		if needCompact {
			go m.compactChunk(inode, indx, false)
		}
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	defer timeit(time.Now())
	var newSpace int64
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(fout, 0xFFFFFFFF) }()
	err := m.txn(func(tx kvTxn) error {
		rs := tx.gets(m.inodeKey(fin), m.inodeKey(fout))
		if rs[0] == nil || rs[1] == nil {
			return syscall.ENOENT
		}
		var sattr Attr
		m.parseAttr(rs[0], &sattr)
		if sattr.Typ != TypeFile {
			return syscall.EINVAL
		}
		if offIn >= sattr.Length {
			*copied = 0
			return nil
		}
		if offIn+size > sattr.Length {
			size = sattr.Length - offIn
		}
		var attr Attr
		m.parseAttr(rs[1], &attr)
		if attr.Typ != TypeFile {
			return syscall.EINVAL
		}

		newleng := offOut + size
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

		vals := tx.scanRange(m.chunkKey(fin, uint32(offIn/ChunkSize)), m.chunkKey(fin, uint32(offIn+size/ChunkSize)+1))
		chunks := make(map[uint32][]*slice)
		for indx := uint32(offIn / ChunkSize); indx <= uint32((offIn+size)/ChunkSize); indx++ {
			if v, ok := vals[string(m.chunkKey(fin, indx))]; ok {
				chunks[indx] = readSliceBuf(v)
			}
		}

		coff := offIn / ChunkSize * ChunkSize
		for coff < offIn+size {
			if coff%ChunkSize != 0 {
				panic("coff")
			}
			// Add a zero chunk for hole
			ss := append([]*slice{{len: ChunkSize}}, chunks[uint32(coff/ChunkSize)]...)
			cs := buildSlice(ss)
			for _, s := range cs {
				pos := coff
				coff += uint64(s.Len)
				if pos < offIn+size && pos+uint64(s.Len) > offIn {
					if pos < offIn {
						dec := offIn - pos
						s.Off += uint32(dec)
						pos += dec
						s.Len -= uint32(dec)
					}
					if pos+uint64(s.Len) > offIn+size {
						dec := pos + uint64(s.Len) - (offIn + size)
						s.Len -= uint32(dec)
					}
					doff := pos - offIn + offOut
					indx := uint32(doff / ChunkSize)
					dpos := uint32(doff % ChunkSize)
					if dpos+s.Len > ChunkSize {
						tx.append(m.chunkKey(fout, indx), marshalSlice(dpos, s.Chunkid, s.Size, s.Off, ChunkSize-dpos))
						if s.Chunkid > 0 {
							tx.incrBy(m.sliceKey(s.Chunkid, s.Size), 1)
						}
						skip := ChunkSize - dpos
						tx.append(m.chunkKey(fout, indx+1), marshalSlice(0, s.Chunkid, s.Size, s.Off+skip, s.Len-skip))
						if s.Chunkid > 0 {
							tx.incrBy(m.sliceKey(s.Chunkid, s.Size), 1)
						}
					} else {
						tx.append(m.chunkKey(fout, indx), marshalSlice(dpos, s.Chunkid, s.Size, s.Off, s.Len))
						if s.Chunkid > 0 {
							tx.incrBy(m.sliceKey(s.Chunkid, s.Size), 1)
						}
					}
				}
			}
		}
		tx.set(m.inodeKey(fout), m.marshal(&attr))
		*copied = size
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) cleanupDeletedFiles() {
	for {
		time.Sleep(time.Minute)
		klen := 1 + 8 + 8
		now := time.Now().Unix()
		vals, _ := m.scanValues(m.fmtKey("D"), func(k, v []byte) bool {
			// filter out invalid ones
			return len(k) == klen && len(v) == 8 && m.parseInt64(v)+60 < now
		})
		for k := range vals {
			rb := utils.FromBuffer([]byte(k)[1:])
			inode := m.decodeInode(rb.Get(8))
			length := rb.Get64()
			logger.Debugf("cleanup chunks of inode %d with %d bytes", inode, length)
			m.deleteFile(inode, length)
		}
	}
}

func (m *kvMeta) cleanupSlices() {
	for {
		time.Sleep(time.Hour)

		// once per hour
		now := time.Now().Unix()
		last, err := m.get(m.counterKey("nextCleanupSlices"))
		if err != nil || m.parseInt64(last)+3600 > now {
			continue
		}
		_ = m.setValue(m.counterKey("nextCleanupSlices"), m.packInt64(now))

		klen := 1 + 8 + 4
		vals, _ := m.scanValues(m.fmtKey("K"), func(k, v []byte) bool {
			// filter out invalid ones
			return len(k) == klen && len(v) == 8 && parseCounter(v) <= 0
		})
		for k, v := range vals {
			rb := utils.FromBuffer([]byte(k)[1:])
			chunkid := rb.Get64()
			size := rb.Get32()
			refs := parseCounter(v)
			if refs < 0 {
				m.deleteSlice(chunkid, size)
			} else {
				m.cleanupZeroRef(chunkid, size)
			}
		}
	}
}

func (m *kvMeta) deleteChunk(inode Ino, indx uint32) error {
	key := m.chunkKey(inode, indx)
	var todel []*slice
	err := m.txn(func(tx kvTxn) error {
		buf := tx.get(key)
		slices := readSliceBuf(buf)
		tx.dels(key)
		for _, s := range slices {
			r := tx.incrBy(m.sliceKey(s.chunkid, s.size), -1)
			if r < 0 {
				todel = append(todel, s)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, s := range todel {
		m.deleteSlice(s.chunkid, s.size)
	}
	return nil
}

func (r *kvMeta) cleanupZeroRef(chunkid uint64, size uint32) {
	_ = r.txn(func(tx kvTxn) error {
		v := tx.incrBy(r.fmtKey(chunkid, size), 0)
		if v != 0 {
			return syscall.EINVAL
		}
		tx.dels(r.fmtKey(chunkid, size))
		return nil
	})
}

func (m *kvMeta) deleteSlice(chunkid uint64, size uint32) {
	m.deleting <- 1
	defer func() { <-m.deleting }()
	err := m.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
	} else {
		err := m.deleteKeys(m.sliceKey(chunkid, size))
		if err != nil {
			logger.Errorf("delete slice %d: %s", chunkid, err)
		}
	}
}

func (m *kvMeta) deleteFile(inode Ino, length uint64) {
	keys, err := m.scanKeys(m.fmtKey("A", inode, "C"))
	if err != nil {
		logger.Warnf("delete chunks of inode %d: %s", inode, err)
		return
	}
	for i := range keys {
		idx := binary.BigEndian.Uint32(keys[i][10:])
		err := m.deleteChunk(inode, idx)
		if err != nil {
			logger.Warnf("delete chunk %d:%d: %s", inode, idx, err)
			return
		}
	}
	_ = m.deleteKeys(m.delfileKey(inode, length))
}

func (m *kvMeta) compactChunk(inode Ino, indx uint32, force bool) {
	if !force {
		// avoid too many or duplicated compaction
		m.Lock()
		k := uint64(inode) + (uint64(indx) << 32)
		if len(m.compacting) > 10 || m.compacting[k] {
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
	}

	buf, err := m.get(m.chunkKey(inode, indx))
	if err != nil {
		return
	}

	ss := readSliceBuf(buf)
	skipped := skipSome(ss)
	ss = ss[skipped:]
	pos, size, chunks := compactChunk(ss)
	if len(ss) < 2 || size == 0 {
		return
	}

	var chunkid uint64
	st := m.NewChunk(Background, 0, 0, 0, &chunkid)
	if st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = m.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		if !strings.Contains(err.Error(), "not exist") && !strings.Contains(err.Error(), "not found") {
			logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		}
		return
	}
	err = m.txn(func(tx kvTxn) error {
		buf2 := tx.get(m.chunkKey(inode, indx))
		if len(buf2) < len(buf) || !bytes.Equal(buf, buf2[:len(buf)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(buf), len(buf2))
			return syscall.EINVAL
		}

		buf2 = append(append(buf2[:skipped*sliceBytes], marshalSlice(pos, chunkid, size, 0, size)...), buf2[len(buf):]...)
		tx.set(m.chunkKey(inode, indx), buf2)
		// create the key to tracking it
		tx.set(m.sliceKey(chunkid, size), make([]byte, 8))
		for _, s := range ss {
			tx.incrBy(m.sliceKey(s.chunkid, s.size), -1)
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if err != nil {
		logger.Warnf("compact %d:%d failed: %s", inode, indx, err)
		refs, e := m.get(m.sliceKey(chunkid, size))
		if e == nil {
			if len(refs) > 0 {
				err = nil
			} else {
				logger.Infof("compacted chunk %d was not used", chunkid)
				err = syscall.EINVAL
			}
		}
	}

	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		m.deleteSlice(chunkid, size)
	} else if err == nil {
		m.of.InvalidateChunk(inode, indx)
		m.cleanupZeroRef(chunkid, size)
		for _, s := range ss {
			refs, err := m.getCounter(m.sliceKey(s.chunkid, s.size))
			if err == nil && refs < 0 {
				m.deleteSlice(s.chunkid, s.size)
			}
		}
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}
	go func() {
		// wait for the current compaction to finish
		time.Sleep(time.Millisecond * 10)
		m.compactChunk(inode, indx, force)
	}()
}

func (r *kvMeta) CompactAll(ctx Context) syscall.Errno {
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	result, err := r.scanValues(r.fmtKey("A"), func(k, v []byte) bool {
		return len(k) == klen && k[1+8] == 'C' && len(v) > sliceBytes
	})
	if err != nil {
		logger.Warnf("scan chunks: %s", err)
		return errno(err)
	}
	for k, value := range result {
		key := []byte(k[1:])
		inode := r.decodeInode(key[:8])
		indx := binary.BigEndian.Uint32(key[9:])
		logger.Debugf("compact chunk %d:%d (%d slices)", inode, indx, len(value)/sliceBytes)
		r.compactChunk(inode, indx, true)
	}
	return 0
}

func (r *kvMeta) ListSlices(ctx Context, slices *[]Slice, delete bool, showProgress func()) syscall.Errno {
	*slices = nil
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	result, err := r.scanValues(r.fmtKey("A"), func(k, v []byte) bool {
		return len(k) == klen && k[1+8] == 'C'
	})
	if err != nil {
		logger.Warnf("scan chunks: %s", err)
		return errno(err)
	}
	for _, value := range result {
		ss := readSliceBuf(value)
		for _, s := range ss {
			if s.chunkid > 0 {
				*slices = append(*slices, Slice{Chunkid: s.chunkid, Size: s.size})
				if showProgress != nil {
					showProgress()
				}
			}
		}
	}
	return 0
}

func (m *kvMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer timeit(time.Now())
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
	defer timeit(time.Now())
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

func (m *kvMeta) SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	key := m.xattrKey(inode, name)
	err := m.txn(func(tx kvTxn) error {
		switch flags {
		case XattrCreate:
			v := tx.get(key)
			if v != nil {
				return syscall.EEXIST
			}
		case XattrReplace:
			v := tx.get(key)
			if v == nil {
				return ENOATTR
			}
		}
		tx.set(key, value)
		return nil
	})
	return errno(err)
}

func (m *kvMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	value, err := m.get(m.xattrKey(inode, name))
	if err != nil {
		return errno(err)
	}
	if value == nil {
		return ENOATTR
	}
	return errno(m.deleteKeys(m.xattrKey(inode, name)))
}

func (m *kvMeta) dumpEntry(inode Ino) (*DumpedEntry, error) {
	e := &DumpedEntry{}
	return e, m.txn(func(tx kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return fmt.Errorf("inode %d not found", inode)
		}
		attr := &Attr{}
		m.parseAttr(a, attr)
		e.Attr = dumpAttr(attr)
		e.Attr.Inode = inode

		vals := tx.scanValues(m.xattrKey(inode, ""), nil)
		if len(vals) > 0 {
			xattrs := make([]*DumpedXattr, 0, len(vals))
			for k, v := range vals {
				xattrs = append(xattrs, &DumpedXattr{k[10:], string(v)}) // "A" + inode + "X"
			}
			sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
			e.Xattrs = xattrs
		}

		if attr.Typ == TypeFile {
			vals = tx.scanRange(m.chunkKey(inode, 0), m.chunkKey(inode, uint32(attr.Length/ChunkSize)+1))
			for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
				v, ok := vals[string(m.chunkKey(inode, indx))]
				if !ok {
					continue
				}
				ss := readSliceBuf(v)
				slices := make([]*DumpedSlice, 0, len(ss))
				for _, s := range ss {
					slices = append(slices, &DumpedSlice{s.pos, s.chunkid, s.size, s.off, s.len})
				}
				e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
			}
		} else if attr.Typ == TypeSymlink {
			l := tx.get(m.symKey(inode))
			if l == nil {
				return fmt.Errorf("no link target for inode %d", inode)
			}
			e.Symlink = string(l)
		}

		return nil
	})
}

func (m *kvMeta) dumpDir(inode Ino, showProgress func(totalIncr, currentIncr int64)) (map[string]*DumpedEntry, error) {
	vals, err := m.scanValues(m.entryKey(inode, ""), nil)
	if err != nil {
		return nil, err
	}
	if showProgress != nil {
		showProgress(int64(len(vals)), 0)
	}
	entries := make(map[string]*DumpedEntry)
	for k, v := range vals {
		typ, inode := m.parseEntry([]byte(v))
		entry, err := m.dumpEntry(inode)
		if err != nil {
			return nil, err
		}
		if typ == TypeDirectory {
			if entry.Entries, err = m.dumpDir(inode, showProgress); err != nil {
				return nil, err
			}
		}
		entries[k[10:]] = entry // "A" + inode + "D"
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	return entries, nil
}

func (m *kvMeta) DumpMeta(w io.Writer) error {
	vals, err := m.scanValues(m.fmtKey("D"), nil)
	if err != nil {
		return err
	}
	dels := make([]*DumpedDelFile, 0, len(vals))
	for k, v := range vals {
		b := utils.FromBuffer([]byte(k[1:])) // "D"
		if b.Len() != 16 {
			return fmt.Errorf("invalid delfileKey: %s", k)
		}
		inode := m.decodeInode(b.Get(8))
		dels = append(dels, &DumpedDelFile{inode, b.Get64(), m.parseInt64(v)})
	}

	tree, err := m.dumpEntry(m.root)
	if err != nil {
		return err
	}

	var total int64 = 1 //root
	progress, bar := utils.NewDynProgressBar("Dump dir progress: ", false)
	bar.Increment()
	if tree.Entries, err = m.dumpDir(m.root, func(totalIncr, currentIncr int64) {
		total += totalIncr
		bar.SetTotal(total, false)
		bar.IncrInt64(currentIncr)
	}); err != nil {
		return err
	}
	if bar.Current() != total {
		logger.Warnf("Dumped %d / total %d, some entries are not dumped", bar.Current(), total)
	}
	bar.SetTotal(0, true)
	progress.Wait()

	format, err := m.Load()
	if err != nil {
		return err
	}

	var rs [][]byte
	err = m.txn(func(tx kvTxn) error {
		rs = tx.gets(m.counterKey(usedSpace),
			m.counterKey(totalInodes),
			m.counterKey("nextInode"),
			m.counterKey("nextChunk"),
			m.counterKey("nextSession"))
		return nil
	})
	if err != nil {
		return err
	}
	cs := make([]int64, len(rs))
	for i, r := range rs {
		if r != nil {
			cs[i] = parseCounter(r)
		}
	}

	vals, err = m.scanValues(m.fmtKey("SS"), nil)
	if err != nil {
		return err
	}
	ss := make(map[uint64][]Ino)
	for k := range vals {
		b := utils.FromBuffer([]byte(k[2:])) // "SS"
		if b.Len() != 16 {
			return fmt.Errorf("invalid sustainedKey: %s", k)
		}
		sid := b.Get64()
		inode := m.decodeInode(b.Get(8))
		ss[sid] = append(ss[sid], inode)
	}
	sessions := make([]*DumpedSustained, 0, len(ss))
	for k, v := range ss {
		sessions = append(sessions, &DumpedSustained{k, v})
	}

	dm := DumpedMeta{
		format,
		&DumpedCounters{
			UsedSpace:   cs[0],
			UsedInodes:  cs[1],
			NextInode:   cs[2],
			NextChunk:   cs[3],
			NextSession: cs[4],
		},
		sessions,
		dels,
		tree,
	}
	return dm.writeJSON(w)
}

func (m *kvMeta) loadEntry(e *DumpedEntry, cs *DumpedCounters, refs map[string]int64) error {
	inode := e.Attr.Inode
	logger.Debugf("Loading entry inode %d name %s", inode, e.Name)
	attr := loadAttr(e.Attr)
	attr.Parent = e.Parent
	return m.txn(func(tx kvTxn) error {
		if attr.Typ == TypeFile {
			attr.Length = e.Attr.Length
			for _, c := range e.Chunks {
				if len(c.Slices) == 0 {
					continue
				}
				slices := make([]byte, 0, sliceBytes*len(c.Slices))
				for _, s := range c.Slices {
					slices = append(slices, marshalSlice(s.Pos, s.Chunkid, s.Size, s.Off, s.Len)...)
					refs[string(m.sliceKey(s.Chunkid, s.Size))]++
					if cs.NextChunk <= int64(s.Chunkid) {
						cs.NextChunk = int64(s.Chunkid) + 1
					}
				}
				tx.set(m.chunkKey(inode, c.Index), slices)
			}
		} else if attr.Typ == TypeDirectory {
			attr.Length = 4 << 10
			for _, c := range e.Entries {
				tx.set(m.entryKey(inode, c.Name), m.packEntry(typeFromString(c.Attr.Type), c.Attr.Inode))
			}
		} else if attr.Typ == TypeSymlink {
			attr.Length = uint64(len(e.Symlink))
			tx.set(m.symKey(inode), []byte(e.Symlink))
		}
		if inode > 1 {
			cs.UsedSpace += align4K(attr.Length)
			cs.UsedInodes += 1
		}
		if cs.NextInode <= int64(inode) {
			cs.NextInode = int64(inode) + 1
		}

		for _, x := range e.Xattrs {
			tx.set(m.xattrKey(inode, x.Name), []byte(x.Value))
		}
		tx.set(m.inodeKey(inode), m.marshal(attr))
		return nil
	})
}

func (m *kvMeta) LoadMeta(r io.Reader) error {
	var exist bool
	err := m.txn(func(tx kvTxn) error {
		exist = tx.exist(m.fmtKey())
		return nil
	})
	if err != nil {
		return err
	}
	if exist {
		return fmt.Errorf("Database %s is not empty", m.Name())
	}

	dec := json.NewDecoder(r)
	dm := &DumpedMeta{}
	if err := dec.Decode(dm); err != nil {
		return err
	}
	format, err := json.MarshalIndent(dm.Setting, "", "")
	if err != nil {
		return err
	}

	var total int64 = 1 // root
	progress, bar := utils.NewDynProgressBar("CollectEntry progress: ", false)
	dm.FSTree.Attr.Inode = 1
	entries := make(map[Ino]*DumpedEntry)
	if err = collectEntry(dm.FSTree, entries, func(totalIncr, currentIncr int64) {
		total += totalIncr
		bar.SetTotal(total, false)
		bar.IncrInt64(currentIncr)
	}); err != nil {
		return err
	}
	if bar.Current() != total {
		logger.Warnf("Collected %d / total %d, some entries are not collected", bar.Current(), total)
	}
	bar.SetTotal(0, true)
	progress.Wait()

	counters := &DumpedCounters{
		NextInode:   2,
		NextChunk:   1,
		NextSession: 1,
	}
	refs := make(map[string]int64)
	for _, entry := range entries {
		if err = m.loadEntry(entry, counters, refs); err != nil {
			return err
		}
	}
	logger.Infof("Dumped counters: %+v", *dm.Counters)
	logger.Infof("Loaded counters: %+v", *counters)

	return m.txn(func(tx kvTxn) error {
		tx.set(m.fmtKey("setting"), format)
		tx.set(m.counterKey(usedSpace), packCounter(counters.UsedSpace))
		tx.set(m.counterKey(totalInodes), packCounter(counters.UsedInodes))
		tx.set(m.counterKey("nextInode"), packCounter(counters.NextInode))
		tx.set(m.counterKey("nextChunk"), packCounter(counters.NextChunk))
		tx.set(m.counterKey("nextSession"), packCounter(counters.NextSession))
		for _, d := range dm.DelFiles {
			tx.set(m.delfileKey(d.Inode, d.Length), m.packInt64(d.Expire))
		}
		for k, v := range refs {
			if v > 1 {
				tx.set([]byte(k), packCounter(v-1))
			}
		}
		return nil
	})
}
