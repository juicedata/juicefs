/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"github.com/juicedata/juicefs/pkg/utils"
)

type kvTxn interface {
	get(key []byte) []byte
	gets(keys ...[]byte) [][]byte
	scanRange(begin, end []byte) map[string][]byte
	scanKeys(prefix []byte) [][]byte
	scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte
	exist(prefix []byte) bool
	set(key, value []byte)
	append(key []byte, value []byte) []byte
	incrBy(key []byte, value int64) int64
	dels(keys ...[]byte)
}

type tkvClient interface {
	name() string
	txn(f func(kvTxn) error) error
	scan(prefix []byte, handler func(key, value []byte)) error
	reset(prefix []byte) error
	close() error
	shouldRetry(err error) bool
}

type kvMeta struct {
	*baseMeta
	client tkvClient
	snap   map[Ino]*DumpedEntry
}

var drivers = make(map[string]func(string) (tkvClient, error))

func newTkvClient(driver, addr string) (tkvClient, error) {
	fn, ok := drivers[driver]
	if !ok {
		return nil, fmt.Errorf("unsupported driver %s", driver)
	}
	return fn(addr)
}

func newKVMeta(driver, addr string, conf *Config) (Meta, error) {
	client, err := newTkvClient(driver, addr)
	if err != nil {
		return nil, fmt.Errorf("connect to addr %s: %s", addr, err)
	}
	// TODO: ping server and check latency > Millisecond
	// logger.Warnf("The latency to database is too high: %s", time.Since(start))
	m := &kvMeta{
		baseMeta: newBaseMeta(addr, conf),
		client:   client,
	}
	m.en = m
	m.root, err = lookupSubdir(m, conf.Subdir)
	return m, err
}

func (m *kvMeta) Shutdown() error {
	return m.client.close()
}

func (m *kvMeta) Name() string {
	return m.client.name()
}

func (m *kvMeta) doDeleteSlice(id uint64, size uint32) error {
	return m.deleteKeys(m.sliceKey(id, size))
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
			panic(fmt.Sprintf("invalid type %T, value %v", a, a))
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
			panic(fmt.Sprintf("invalid type %T, value %v", a, a))
		}
	}
	return b.Bytes()
}

/**
  Ino     iiiiiiii
  Length  llllllll
  Indx    nnnn
  name    ...
  sliceId cccccccc
  session ssssssss

All keys:
  setting            format
  C...               counter
  AiiiiiiiiI         inode attribute
  AiiiiiiiiD...      dentry
  AiiiiiiiiPiiiiiiii parents // for hard links
  AiiiiiiiiCnnnn     file chunks
  AiiiiiiiiS         symlink target
  AiiiiiiiiX...      extented attribute
  Diiiiiiiillllllll  delete inodes
  Fiiiiiiii          Flocks
  Piiiiiiii          POSIX locks
  Kccccccccnnnn      slice refs
  Lttttttttcccccccc  delayed slices
  SEssssssss         session expire time
  SHssssssss         session heartbeat // for legacy client
  SIssssssss         session info
  SSssssssssiiiiiiii sustained inode
*/

func (m *kvMeta) inodeKey(inode Ino) []byte {
	return m.fmtKey("A", inode, "I")
}

func (m *kvMeta) entryKey(parent Ino, name string) []byte {
	return m.fmtKey("A", parent, "D", name)
}

func (m *kvMeta) parentKey(inode, parent Ino) []byte {
	return m.fmtKey("A", inode, "P", parent)
}

func (m *kvMeta) chunkKey(inode Ino, indx uint32) []byte {
	return m.fmtKey("A", inode, "C", indx)
}

func (m *kvMeta) sliceKey(id uint64, size uint32) []byte {
	return m.fmtKey("K", id, size)
}

func (m *kvMeta) delSliceKey(ts int64, id uint64) []byte {
	return m.fmtKey("L", uint64(ts), id)
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
	return m.fmtKey("SE", sid)
}

func (m *kvMeta) legacySessionKey(sid uint64) []byte {
	return m.fmtKey("SH", sid)
}

func (m *kvMeta) parseSid(key string) uint64 {
	buf := []byte(key[2:]) // "SE" or "SH"
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

// Used for values that are modified by directly set; mostly timestamps
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

// Used for most counter values that are modified by incrBy
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

func (m *kvMeta) get(key []byte) ([]byte, error) {
	var value []byte
	err := m.client.txn(func(tx kvTxn) error {
		value = tx.get(key)
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

func (m *kvMeta) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) (map[string][]byte, error) {
	var values map[string][]byte
	err := m.client.txn(func(tx kvTxn) error {
		values = tx.scanValues(prefix, limit, filter)
		return nil
	})
	return values, err
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
		if err = format.update(&old, force); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}

	m.fmt = format
	ts := time.Now().Unix()
	attr := &Attr{
		Typ:    TypeDirectory,
		Atime:  ts,
		Mtime:  ts,
		Ctime:  ts,
		Nlink:  2,
		Length: 4 << 10,
		Parent: 1,
	}
	return m.txn(func(tx kvTxn) error {
		if format.TrashDays > 0 {
			buf := tx.get(m.inodeKey(TrashInode))
			if buf == nil {
				attr.Mode = 0555
				tx.set(m.inodeKey(TrashInode), m.marshal(attr))
			}
		}
		tx.set(m.fmtKey("setting"), data)
		if body == nil || m.client.name() == "memkv" {
			attr.Mode = 0777
			tx.set(m.inodeKey(1), m.marshal(attr))
			tx.incrBy(m.counterKey("nextInode"), 2)
			tx.incrBy(m.counterKey("nextChunk"), 1)
		}
		return nil
	})
}

func (m *kvMeta) Reset() error {
	return m.client.reset(nil)
}

func (m *kvMeta) doLoad() ([]byte, error) {
	return m.get(m.fmtKey("setting"))
}

func (m *kvMeta) doNewSession(sinfo []byte) error {
	if err := m.setValue(m.sessionKey(m.sid), m.packInt64(m.expireTime())); err != nil {
		return fmt.Errorf("set session ID %d: %s", m.sid, err)
	}
	if err := m.setValue(m.sessionInfoKey(m.sid), sinfo); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}

	go m.flushStats()
	return nil
}

func (m *kvMeta) doRefreshSession() {
	_ = m.setValue(m.sessionKey(m.sid), m.packInt64(m.expireTime()))
}

func (m *kvMeta) doCleanStaleSession(sid uint64) error {
	var fail bool
	// release locks
	if flocks, err := m.scanValues(m.fmtKey("F"), -1, nil); err == nil {
		for k, v := range flocks {
			ls := unmarshalFlock(v)
			for o := range ls {
				if o.sid == sid {
					if err = m.txn(func(tx kvTxn) error {
						v := tx.get([]byte(k))
						ls := unmarshalFlock(v)
						delete(ls, o)
						if len(ls) > 0 {
							tx.set([]byte(k), marshalFlock(ls))
						} else {
							tx.dels([]byte(k))
						}
						return nil
					}); err != nil {
						logger.Warnf("Delete flock with sid %d: %s", sid, err)
						fail = true
					}
				}
			}
		}
	} else {
		logger.Warnf("Scan flock with sid %d: %s", sid, err)
		fail = true
	}

	if plocks, err := m.scanValues(m.fmtKey("P"), -1, nil); err == nil {
		for k, v := range plocks {
			ls := unmarshalPlock(v)
			for o := range ls {
				if o.sid == sid {
					if err = m.txn(func(tx kvTxn) error {
						v := tx.get([]byte(k))
						ls := unmarshalPlock(v)
						delete(ls, o)
						if len(ls) > 0 {
							tx.set([]byte(k), marshalPlock(ls))
						} else {
							tx.dels([]byte(k))
						}
						return nil
					}); err != nil {
						logger.Warnf("Delete plock with sid %d: %s", sid, err)
						fail = true
					}
				}
			}
		}
	} else {
		logger.Warnf("Scan plock with sid %d: %s", sid, err)
		fail = true
	}

	if keys, err := m.scanKeys(m.fmtKey("SS", sid)); err == nil {
		for _, key := range keys {
			inode := m.decodeInode(key[10:]) // "SS" + sid
			if err = m.doDeleteSustainedInode(sid, inode); err != nil {
				logger.Warnf("Delete sustained inode %d of sid %d: %s", inode, sid, err)
				fail = true
			}
		}
	} else {
		logger.Warnf("Scan sustained with sid %d: %s", sid, err)
		fail = true
	}

	if fail {
		return fmt.Errorf("failed to clean up sid %d", sid)
	} else {
		return m.deleteKeys(m.sessionKey(sid), m.legacySessionKey(sid), m.sessionInfoKey(sid))
	}
}

func (m *kvMeta) doFindStaleSessions(limit int) ([]uint64, error) {
	vals, err := m.scanValues(m.fmtKey("SE"), limit, func(k, v []byte) bool {
		return m.parseInt64(v) < time.Now().Unix()
	})
	if err != nil {
		return nil, err
	}
	sids := make([]uint64, 0, len(vals))
	for k := range vals {
		sids = append(sids, m.parseSid(k))
	}
	limit -= len(sids)
	if limit <= 0 {
		return sids, nil
	}

	// check clients with version before 1.0-beta3 as well
	vals, err = m.scanValues(m.fmtKey("SH"), limit, func(k, v []byte) bool {
		return m.parseInt64(v) < time.Now().Add(time.Minute*-5).Unix()
	})
	if err != nil {
		logger.Errorf("Scan stale legacy sessions: %s", err)
		return sids, nil
	}
	for k := range vals {
		sids = append(sids, m.parseSid(k))
	}
	return sids, nil
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
		flocks, err := m.scanValues(m.fmtKey("F"), -1, nil)
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
		plocks, err := m.scanValues(m.fmtKey("P"), -1, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range plocks {
			inode := m.decodeInode([]byte(k[1:])) // "P"
			ls := unmarshalPlock(v)
			for o, l := range ls {
				if o.sid == sid {
					s.Plocks = append(s.Plocks, Plock{inode, o.sid, loadLocks(l)})
				}
			}
		}
	}
	return &s, nil
}

func (m *kvMeta) GetSession(sid uint64, detail bool) (*Session, error) {
	var legacy bool
	value, err := m.get(m.sessionKey(sid))
	if err == nil && value == nil {
		legacy = true
		value, err = m.get(m.legacySessionKey(sid))
	}
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("session not found: %d", sid)
	}
	s, err := m.getSession(sid, detail)
	if err != nil {
		return nil, err
	}
	s.Expire = time.Unix(m.parseInt64(value), 0)
	if legacy {
		s.Expire = s.Expire.Add(time.Minute * 5)
	}
	return s, nil
}

func (m *kvMeta) ListSessions() ([]*Session, error) {
	vals, err := m.scanValues(m.fmtKey("SE"), -1, nil)
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
		s.Expire = time.Unix(m.parseInt64(v), 0)
		sessions = append(sessions, s)
	}

	// add clients with version before 1.0-beta3 as well
	vals, err = m.scanValues(m.fmtKey("SH"), -1, nil)
	if err != nil {
		logger.Errorf("Scan legacy sessions: %s", err)
		return sessions, nil
	}
	for k, v := range vals {
		s, err := m.getSession(m.parseSid(k), false)
		if err != nil {
			logger.Errorf("Get legacy session: %s", err)
			continue
		}
		s.Expire = time.Unix(m.parseInt64(v), 0).Add(time.Minute * 5)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (m *kvMeta) shouldRetry(err error) bool {
	return m.client.shouldRetry(err)
}

func (m *kvMeta) txn(f func(tx kvTxn) error, inodes ...Ino) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()
	if len(inodes) > 0 {
		m.txLock(uint(inodes[0]))
		defer m.txUnlock(uint(inodes[0]))
	}
	var lastErr error
	for i := 0; i < 50; i++ {
		err := m.client.txn(f)
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if err != nil && m.shouldRetry(err) {
			m.txRestart.Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(rand.Int()%((i+1)*(i+1))))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Transaction succeeded after %d tries (%s), inodes: %v, error: %s", i+1, time.Since(start), inodes, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
}

func (m *kvMeta) setValue(key, value []byte) error {
	return m.txn(func(tx kvTxn) error {
		tx.set(key, value)
		return nil
	})
}

func (m *kvMeta) getCounter(name string) (int64, error) {
	buf, err := m.get(m.counterKey(name))
	return parseCounter(buf), err
}

func (m *kvMeta) incrCounter(name string, value int64) (int64, error) {
	var new int64
	key := m.counterKey(name)
	err := m.txn(func(tx kvTxn) error {
		new = tx.incrBy(key, value)
		return nil
	})
	return new, err
}

func (m *kvMeta) setIfSmall(name string, value, diff int64) (bool, error) {
	var changed bool
	key := m.counterKey(name)
	err := m.txn(func(tx kvTxn) error {
		changed = false
		if m.parseInt64(tx.get(key)) > value-diff {
			return nil
		} else {
			changed = true
			tx.set(key, m.packInt64(value))
			return nil
		}
	})

	return changed, err
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

func (m *kvMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	buf, err := m.get(m.entryKey(parent, name))
	if err != nil {
		return errno(err)
	}
	if buf == nil {
		return syscall.ENOENT
	}
	foundType, foundIno := m.parseEntry(buf)
	a, err := m.get(m.inodeKey(foundIno))
	if a != nil {
		m.parseAttr(a, attr)
	} else if err == nil {
		logger.Warnf("no attribute for inode %d (%d, %s)", foundIno, parent, name)
		*attr = Attr{Typ: foundType}
	}
	*inode = foundIno
	return errno(err)
}

func (m *kvMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	a, err := m.get(m.inodeKey(inode))
	if a != nil {
		m.parseAttr(a, attr)
	} else if err == nil {
		err = syscall.ENOENT
	}
	return errno(err)
}

func (m *kvMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	defer m.timeit(time.Now())
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
			clearSUGID(ctx, &cur, attr)
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
		if set&SetAttrFlag != 0 {
			cur.Flags = attr.Flags
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
	defer m.timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		newSpace = 0
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
	defer m.timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		newSpace = 0
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
		if (t.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if (t.Flags&FlagAppend) != 0 && (mode&^fallocKeepSize) != 0 {
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
		if newSpace > 0 && m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(&t))
		if mode&(fallocZeroRange|fallocPunchHole) != 0 && off < old {
			off, size := off, size
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

func (m *kvMeta) doReadlink(ctx Context, inode Ino) ([]byte, error) {
	return m.get(m.symKey(inode))
}

func (m *kvMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	var ino Ino
	var err error
	if parent == TrashInode {
		var next int64
		next, err = m.incrCounter("nextTrash", 1)
		ino = TrashInode + Ino(next)
	} else {
		ino, err = m.nextInode()
	}
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
		if (pattr.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
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
			if _type == TypeFile || _type == TypeDirectory {
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

		var updateParent bool
		now := time.Now()
		if parent != TrashInode {
			if _type == TypeDirectory {
				pattr.Nlink++
				updateParent = true
			}
			if updateParent || now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= minUpdateTime {
				pattr.Mtime = now.Unix()
				pattr.Mtimensec = uint32(now.Nanosecond())
				pattr.Ctime = now.Unix()
				pattr.Ctimensec = uint32(now.Nanosecond())
				updateParent = true
			}
		}
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
		if updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		tx.set(m.inodeKey(ino), m.marshal(attr))
		if _type == TypeSymlink {
			tx.set(m.symKey(ino), []byte(path))
		}
		return nil
	}, parent)
	if err == nil {
		m.updateStats(align4K(0), 1)
	}
	return errno(err)
}

func (m *kvMeta) doUnlink(ctx Context, parent Ino, name string) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parent, &trash); st != 0 {
		return st
	}
	var _type uint8
	var inode Ino
	var attr Attr
	var opened bool
	var newSpace, newInode int64
	err := m.txn(func(tx kvTxn) error {
		opened = false
		attr = Attr{}
		newSpace, newInode = 0, 0
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
		if (pattr.Flags&FlagAppend) != 0 || (pattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		attr = Attr{}
		opened = false
		now := time.Now()
		if rs[1] != nil {
			m.parseAttr(rs[1], &attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			if (attr.Flags&FlagAppend) != 0 || (attr.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			attr.Ctime = now.Unix()
			attr.Ctimensec = uint32(now.Nanosecond())
			if trash == 0 {
				attr.Nlink--
				if _type == TypeFile && attr.Nlink == 0 {
					opened = m.of.IsOpen(inode)
				}
			} else if attr.Parent > 0 {
				attr.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
			trash = 0
		}

		defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
		var updateParent bool
		if !isTrash(parent) && now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= minUpdateTime {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}

		tx.dels(m.entryKey(parent, name))
		if updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		if attr.Nlink > 0 {
			tx.set(m.inodeKey(inode), m.marshal(&attr))
			if trash > 0 {
				tx.set(m.entryKey(trash, m.trashEntry(parent, inode, name)), buf)
				if attr.Parent == 0 {
					tx.incrBy(m.parentKey(inode, trash), 1)
				}
			}
			if attr.Parent == 0 {
				tx.incrBy(m.parentKey(inode, parent), -1)
			}
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
			if attr.Parent == 0 {
				tx.dels(tx.scanKeys(m.fmtKey("A", inode, "P"))...)
			}
		}
		return nil
	}, parent)
	if err == nil && trash == 0 {
		if _type == TypeFile && attr.Nlink == 0 {
			m.fileDeleted(opened, inode, attr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) doRmdir(ctx Context, parent Ino, name string) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parent, &trash); st != 0 {
		return st
	}
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
		if (pattr.Flags&FlagAppend) != 0 || (pattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if tx.exist(m.entryKey(inode, "")) {
			return syscall.ENOTEMPTY
		}

		now := time.Now()
		if rs[1] != nil {
			m.parseAttr(rs[1], &attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			if trash > 0 {
				attr.Ctime = now.Unix()
				attr.Ctimensec = uint32(now.Nanosecond())
				attr.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
			trash = 0
		}
		pattr.Nlink--
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())

		if !isTrash(parent) {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		tx.dels(m.entryKey(parent, name))
		if trash > 0 {
			tx.set(m.inodeKey(inode), m.marshal(&attr))
			tx.set(m.entryKey(trash, m.trashEntry(parent, inode, name)), buf)
		} else {
			tx.dels(m.inodeKey(inode))
			tx.dels(tx.scanKeys(m.xattrKey(inode, ""))...)
		}
		return nil
	}, parent)
	if err == nil && trash == 0 {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *kvMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parentDst, &trash); st != 0 {
		return st
	}
	exchange := flags == RenameExchange
	var opened bool
	var dino Ino
	var dtyp uint8
	var tattr Attr
	var newSpace, newInode int64
	err := m.txn(func(tx kvTxn) error {
		opened = false
		dino, dtyp = 0, 0
		tattr = Attr{}
		newSpace, newInode = 0, 0
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
		if (sattr.Flags&FlagAppend) != 0 || (sattr.Flags&FlagImmutable) != 0 || (dattr.Flags&FlagImmutable) != 0 || (iattr.Flags&FlagAppend) != 0 || (iattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}

		dbuf := tx.get(m.entryKey(parentDst, nameDst))
		if dbuf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				nameDst = string(e.Name)
				dbuf = m.packEntry(e.Attr.Typ, e.Inode)
			}
		}
		var supdate, dupdate bool
		now := time.Now()
		if dbuf != nil {
			if flags == RenameNoReplace {
				return syscall.EEXIST
			}
			dtyp, dino = m.parseEntry(dbuf)
			a := tx.get(m.inodeKey(dino))
			if a == nil { // corrupt entry
				logger.Warnf("no attribute for inode %d (%d, %s)", dino, parentDst, nameDst)
				trash = 0
			}
			m.parseAttr(a, &tattr)
			if (tattr.Flags&FlagAppend) != 0 || (tattr.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			tattr.Ctime = now.Unix()
			tattr.Ctimensec = uint32(now.Nanosecond())
			if exchange {
				if parentSrc != parentDst {
					if dtyp == TypeDirectory {
						tattr.Parent = parentSrc
						dattr.Nlink--
						sattr.Nlink++
						supdate, dupdate = true, true
					} else if tattr.Parent > 0 {
						tattr.Parent = parentSrc
					}
				}
			} else {
				if dtyp == TypeDirectory {
					if tx.exist(m.entryKey(dino, "")) {
						return syscall.ENOTEMPTY
					}
					dattr.Nlink--
					dupdate = true
					if trash > 0 {
						tattr.Parent = trash
					}
				} else {
					if trash == 0 {
						tattr.Nlink--
						if dtyp == TypeFile && tattr.Nlink == 0 {
							opened = m.of.IsOpen(dino)
						}
						defer func() { m.of.InvalidateChunk(dino, 0xFFFFFFFE) }()
					} else if tattr.Parent > 0 {
						tattr.Parent = trash
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
		}
		if ctx.Uid() != 0 && sattr.Mode&01000 != 0 && ctx.Uid() != sattr.Uid && ctx.Uid() != iattr.Uid {
			return syscall.EACCES
		}

		if parentSrc != parentDst {
			if typ == TypeDirectory {
				iattr.Parent = parentDst
				sattr.Nlink--
				dattr.Nlink++
				supdate, dupdate = true, true
			} else if iattr.Parent > 0 {
				iattr.Parent = parentDst
			}
		}
		if supdate || now.Sub(time.Unix(sattr.Mtime, int64(sattr.Mtimensec))) >= minUpdateTime {
			sattr.Mtime = now.Unix()
			sattr.Mtimensec = uint32(now.Nanosecond())
			sattr.Ctime = now.Unix()
			sattr.Ctimensec = uint32(now.Nanosecond())
			supdate = true
		}
		if dupdate || now.Sub(time.Unix(dattr.Mtime, int64(dattr.Mtimensec))) >= minUpdateTime {
			dattr.Mtime = now.Unix()
			dattr.Mtimensec = uint32(now.Nanosecond())
			dattr.Ctime = now.Unix()
			dattr.Ctimensec = uint32(now.Nanosecond())
			dupdate = true
		}
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		if inode != nil {
			*inode = ino
		}
		if attr != nil {
			*attr = iattr
		}

		if exchange { // dino > 0
			tx.set(m.entryKey(parentSrc, nameSrc), dbuf)
			tx.set(m.inodeKey(dino), m.marshal(&tattr))
			if parentSrc != parentDst && tattr.Parent == 0 {
				tx.incrBy(m.parentKey(dino, parentSrc), 1)
				tx.incrBy(m.parentKey(dino, parentDst), -1)
			}
		} else {
			tx.dels(m.entryKey(parentSrc, nameSrc))
			if dino > 0 {
				if trash > 0 {
					tx.set(m.inodeKey(dino), m.marshal(&tattr))
					tx.set(m.entryKey(trash, m.trashEntry(parentDst, dino, nameDst)), dbuf)
					if tattr.Parent == 0 {
						tx.incrBy(m.parentKey(dino, trash), 1)
						tx.incrBy(m.parentKey(dino, parentDst), -1)
					}
				} else if dtyp != TypeDirectory && tattr.Nlink > 0 {
					tx.set(m.inodeKey(dino), m.marshal(&tattr))
					if tattr.Parent == 0 {
						tx.incrBy(m.parentKey(dino, parentDst), -1)
					}
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
						if dtyp == TypeSymlink {
							tx.dels(m.symKey(dino))
						}
						tx.dels(m.inodeKey(dino))
						newSpace, newInode = -align4K(0), -1
					}
					tx.dels(tx.scanKeys(m.xattrKey(dino, ""))...)
					if tattr.Parent == 0 {
						tx.dels(tx.scanKeys(m.fmtKey("A", dino, "P"))...)
					}
				}
			}
		}
		if parentDst != parentSrc {
			if !isTrash(parentSrc) && supdate {
				tx.set(m.inodeKey(parentSrc), m.marshal(&sattr))
			}
			if iattr.Parent == 0 {
				tx.incrBy(m.parentKey(ino, parentDst), 1)
				tx.incrBy(m.parentKey(ino, parentSrc), -1)
			}
		}
		tx.set(m.inodeKey(ino), m.marshal(&iattr))
		tx.set(m.entryKey(parentDst, nameDst), buf)
		if dupdate {
			tx.set(m.inodeKey(parentDst), m.marshal(&dattr))
		}
		return nil
	}, parentSrc)
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dtyp == TypeFile && tattr.Nlink == 0 {
			m.fileDeleted(opened, dino, tattr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
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
		if pattr.Flags&FlagImmutable != 0 {
			return syscall.EPERM
		}
		m.parseAttr(rs[1], &iattr)
		if iattr.Typ == TypeDirectory {
			return syscall.EPERM
		}
		if (iattr.Flags&FlagAppend) != 0 || (iattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		buf := tx.get(m.entryKey(parent, name))
		if buf != nil || buf == nil && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		var updateParent bool
		now := time.Now()
		if now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= minUpdateTime {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}
		oldParent := iattr.Parent
		iattr.Parent = 0
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Nlink++
		tx.set(m.entryKey(parent, name), m.packEntry(iattr.Typ, inode))
		if updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		tx.set(m.inodeKey(inode), m.marshal(&iattr))
		if oldParent > 0 {
			tx.incrBy(m.parentKey(inode, oldParent), 1)
		}
		tx.incrBy(m.parentKey(inode, parent), 1)
		if attr != nil {
			*attr = iattr
		}
		return nil
	}, parent))
}

func (m *kvMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	// TODO: handle big directory
	vals, err := m.scanValues(m.entryKey(inode, ""), limit, nil)
	if err != nil {
		return errno(err)
	}
	prefix := len(m.entryKey(inode, ""))
	for name, buf := range vals {
		typ, ino := m.parseEntry(buf)
		if len(name) == prefix {
			logger.Errorf("Corrupt entry with empty name: inode %d parent %d", ino, inode)
			continue
		}
		*entries = append(*entries, &Entry{
			Inode: ino,
			Name:  []byte(name)[prefix:],
			Attr:  &Attr{Typ: typ},
		})
	}

	if plus != 0 && len(*entries) != 0 {
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

func (m *kvMeta) doDeleteSustainedInode(sid uint64, inode Ino) error {
	var attr Attr
	var newSpace int64
	err := m.txn(func(tx kvTxn) error {
		newSpace = 0
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return nil
		}
		m.parseAttr(a, &attr)
		tx.set(m.delfileKey(inode, attr.Length), m.packInt64(time.Now().Unix()))
		tx.dels(m.inodeKey(inode))
		tx.dels(m.sustainedKey(sid, inode))
		newSpace = -align4K(attr.Length)
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		m.tryDeleteFileData(inode, attr.Length)
	}
	return err
}

func (m *kvMeta) Read(ctx Context, inode Ino, indx uint32, slices *[]Slice) syscall.Errno {
	f := m.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if ss, ok := m.of.ReadChunk(inode, indx); ok {
		*slices = ss
		return 0
	}
	defer m.timeit(time.Now())
	val, err := m.get(m.chunkKey(inode, indx))
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(val)
	if ss == nil {
		return syscall.EIO
	}
	*slices = buildSlice(ss)
	m.of.CacheChunk(inode, indx, *slices)
	if !m.conf.ReadOnly && (len(val)/sliceBytes >= 5 || len(*slices) >= 5) {
		go m.compactChunk(inode, indx, false)
	}
	return 0
}

func (m *kvMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	defer m.timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var newSpace int64
	var needCompact bool
	err := m.txn(func(tx kvTxn) error {
		newSpace = 0
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
		val := tx.append(m.chunkKey(inode, indx), marshalSlice(off, slice.Id, slice.Size, slice.Off, slice.Len))
		tx.set(m.inodeKey(inode), m.marshal(&attr))
		needCompact = (len(val)/sliceBytes)%100 == 99
		return nil
	}, inode)
	if err == nil {
		if needCompact {
			go m.compactChunk(inode, indx, false)
		}
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *kvMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	defer m.timeit(time.Now())
	var newSpace int64
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(fout, 0xFFFFFFFF) }()
	err := m.txn(func(tx kvTxn) error {
		newSpace = 0
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
		size := size
		if offIn+size > sattr.Length {
			size = sattr.Length - offIn
		}
		var attr Attr
		m.parseAttr(rs[1], &attr)
		if attr.Typ != TypeFile {
			return syscall.EINVAL
		}
		if (attr.Flags&FlagImmutable) != 0 || (attr.Flags&FlagAppend) != 0 {
			return syscall.EPERM
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
						tx.append(m.chunkKey(fout, indx), marshalSlice(dpos, s.Id, s.Size, s.Off, ChunkSize-dpos))
						if s.Id > 0 {
							tx.incrBy(m.sliceKey(s.Id, s.Size), 1)
						}
						skip := ChunkSize - dpos
						tx.append(m.chunkKey(fout, indx+1), marshalSlice(0, s.Id, s.Size, s.Off+skip, s.Len-skip))
						if s.Id > 0 {
							tx.incrBy(m.sliceKey(s.Id, s.Size), 1)
						}
					} else {
						tx.append(m.chunkKey(fout, indx), marshalSlice(dpos, s.Id, s.Size, s.Off, s.Len))
						if s.Id > 0 {
							tx.incrBy(m.sliceKey(s.Id, s.Size), 1)
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

func (m *kvMeta) doGetParents(ctx Context, inode Ino) map[Ino]int {
	vals, err := m.scanValues(m.fmtKey("A", inode, "P"), -1, func(k, v []byte) bool {
		// parents: AiiiiiiiiPiiiiiiii
		return len(k) == 1+8+1+8 && parseCounter(v) > 0
	})
	if err != nil {
		logger.Warnf("Scan parent key of inode %d: %s", inode, err)
		return nil
	}
	ps := make(map[Ino]int)
	for k, v := range vals {
		ps[m.decodeInode([]byte(k[10:]))] = int(parseCounter(v))
	}
	return ps
}

func (m *kvMeta) doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) {
	klen := 1 + 8 + 8
	vals, err := m.scanValues(m.fmtKey("D"), limit, func(k, v []byte) bool {
		// filter out invalid ones
		return len(k) == klen && len(v) == 8 && m.parseInt64(v) < ts
	})
	if err != nil {
		return nil, err
	}
	files := make(map[Ino]uint64, len(vals))
	for k := range vals {
		rb := utils.FromBuffer([]byte(k)[1:])
		files[m.decodeInode(rb.Get(8))] = rb.Get64()
	}
	return files, nil
}

func (m *kvMeta) doCleanupSlices() {
	klen := 1 + 8 + 4
	vals, _ := m.scanValues(m.fmtKey("K"), -1, func(k, v []byte) bool {
		// filter out invalid ones
		return len(k) == klen && len(v) == 8 && parseCounter(v) <= 0
	})
	for k, v := range vals {
		rb := utils.FromBuffer([]byte(k)[1:])
		id := rb.Get64()
		size := rb.Get32()
		refs := parseCounter(v)
		if refs < 0 {
			m.deleteSlice(id, size)
		} else {
			m.cleanupZeroRef(id, size)
		}
	}
}

func (m *kvMeta) deleteChunk(inode Ino, indx uint32) error {
	key := m.chunkKey(inode, indx)
	var todel []*slice
	err := m.txn(func(tx kvTxn) error {
		todel = todel[:0]
		buf := tx.get(key)
		slices := readSliceBuf(buf)
		tx.dels(key)
		for _, s := range slices {
			if s.id > 0 && tx.incrBy(m.sliceKey(s.id, s.size), -1) < 0 {
				todel = append(todel, s)
			}
		}
		return nil
	}, inode)
	if err != nil {
		return err
	}
	for _, s := range todel {
		m.deleteSlice(s.id, s.size)
	}
	return nil
}

func (r *kvMeta) cleanupZeroRef(id uint64, size uint32) {
	_ = r.txn(func(tx kvTxn) error {
		v := tx.incrBy(r.sliceKey(id, size), 0)
		if v != 0 {
			return syscall.EINVAL
		}
		tx.dels(r.sliceKey(id, size))
		return nil
	})
}

func (m *kvMeta) doDeleteFileData(inode Ino, length uint64) {
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

func (m *kvMeta) doCleanupDelayedSlices(edge int64, limit int) (int, error) {
	var keys [][]byte
	if err := m.client.txn(func(tx kvTxn) error {
		vals := tx.scanRange(m.delSliceKey(0, 0), m.delSliceKey(edge, 0))
		for k := range vals {
			if len(k) != 1+8+8 { // delayed slices: Lttttttttcccccccc
				continue
			}
			keys = append(keys, []byte(k))
		}
		return nil
	}); err != nil {
		logger.Warnf("Scan delayed slices: %s", err)
		return 0, err
	}

	var count int
	var ss []Slice
	var rs []int64
	for _, key := range keys {
		if err := m.txn(func(tx kvTxn) error {
			ss, rs = ss[:0], rs[:0]
			buf := tx.get(key)
			if len(buf) == 0 {
				return nil
			}
			m.decodeDelayedSlices(buf, &ss)
			if len(ss) == 0 {
				return fmt.Errorf("invalid value for delayed slices %s: %v", key, buf)
			}
			for _, s := range ss {
				rs = append(rs, tx.incrBy(m.sliceKey(s.Id, s.Size), -1))
			}
			tx.dels(key)
			return nil
		}); err != nil {
			logger.Warnf("Cleanup delayed slices %s: %s", key, err)
			continue
		}
		for i, s := range ss {
			if rs[i] < 0 {
				m.deleteSlice(s.Id, s.Size)
				count++
			}
		}
		if count >= limit {
			break
		}
	}
	return count, nil
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

	if len(buf) > sliceBytes*100 {
		buf = buf[:sliceBytes*100]
	}
	ss := readSliceBuf(buf)
	skipped := skipSome(ss)
	ss = ss[skipped:]
	pos, size, slices := compactChunk(ss)
	if len(ss) < 2 || size == 0 {
		return
	}

	var id uint64
	st := m.NewSlice(Background, &id)
	if st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = m.newMsg(CompactChunk, slices, id)
	if err != nil {
		if !strings.Contains(err.Error(), "not exist") && !strings.Contains(err.Error(), "not found") {
			logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		}
		return
	}
	var dsbuf []byte
	trash := m.toTrash(0)
	if trash {
		for _, s := range ss {
			if s.id > 0 {
				dsbuf = append(dsbuf, m.encodeDelayedSlice(s.id, s.size)...)
			}
		}
	}
	err = m.txn(func(tx kvTxn) error {
		buf2 := tx.get(m.chunkKey(inode, indx))
		if len(buf2) < len(buf) || !bytes.Equal(buf, buf2[:len(buf)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(buf), len(buf2))
			return syscall.EINVAL
		}

		buf2 = append(append(buf2[:skipped*sliceBytes], marshalSlice(pos, id, size, 0, size)...), buf2[len(buf):]...)
		tx.set(m.chunkKey(inode, indx), buf2)
		// create the key to tracking it
		tx.set(m.sliceKey(id, size), make([]byte, 8))
		if trash {
			if len(dsbuf) > 0 {
				tx.set(m.delSliceKey(time.Now().Unix(), id), dsbuf)
			}
		} else {
			for _, s := range ss {
				if s.id > 0 {
					tx.incrBy(m.sliceKey(s.id, s.size), -1)
				}
			}
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if err != nil {
		logger.Warnf("compact %d:%d failed: %s", inode, indx, err)
		refs, e := m.get(m.sliceKey(id, size))
		if e == nil {
			if len(refs) > 0 {
				err = nil
			} else {
				logger.Infof("compacted chunk %d was not used", id)
				err = syscall.EINVAL
			}
		}
	}

	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, id, size)
		m.deleteSlice(id, size)
	} else if err == nil {
		m.of.InvalidateChunk(inode, indx)
		m.cleanupZeroRef(id, size)
		if !trash {
			var refs int64
			for _, s := range ss {
				if s.id > 0 && m.client.txn(func(tx kvTxn) error {
					refs = tx.incrBy(m.sliceKey(s.id, s.size), 0)
					return nil
				}) == nil && refs < 0 {
					m.deleteSlice(s.id, s.size)
				}
			}
		}
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}

	if force {
		m.compactChunk(inode, indx, force)
	} else {
		go func() {
			// wait for the current compaction to finish
			time.Sleep(time.Millisecond * 10)
			m.compactChunk(inode, indx, force)
		}()
	}
}

func (r *kvMeta) CompactAll(ctx Context, bar *utils.Bar) syscall.Errno {
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	result, err := r.scanValues(r.fmtKey("A"), -1, func(k, v []byte) bool {
		return len(k) == klen && k[1+8] == 'C' && len(v) > sliceBytes
	})
	if err != nil {
		logger.Warnf("scan chunks: %s", err)
		return errno(err)
	}

	bar.IncrTotal(int64(len(result)))
	for k, value := range result {
		key := []byte(k[1:])
		inode := r.decodeInode(key[:8])
		indx := binary.BigEndian.Uint32(key[9:])
		logger.Debugf("compact chunk %d:%d (%d slices)", inode, indx, len(value)/sliceBytes)
		r.compactChunk(inode, indx, true)
		bar.Increment()
	}
	return 0
}

func (m *kvMeta) ListSlices(ctx Context, slices map[Ino][]Slice, delete bool, showProgress func()) syscall.Errno {
	if delete {
		m.doCleanupSlices()
	}
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	result, err := m.scanValues(m.fmtKey("A"), -1, func(k, v []byte) bool {
		return len(k) == klen && k[1+8] == 'C'
	})
	if err != nil {
		logger.Warnf("scan chunks: %s", err)
		return errno(err)
	}
	for key, value := range result {
		inode := m.decodeInode([]byte(key)[1:9])
		ss := readSliceBuf(value)
		for _, s := range ss {
			if s.id > 0 {
				slices[inode] = append(slices[inode], Slice{Id: s.id, Size: s.size})
				if showProgress != nil {
					showProgress()
				}
			}
		}
	}
	if m.fmt.TrashDays == 0 {
		return 0
	}

	// delayed slices: Lttttttttcccccccc
	klen = 1 + 8 + 8
	result, err = m.scanValues(m.fmtKey("L"), -1, func(k, v []byte) bool {
		return len(k) == klen
	})
	if err != nil {
		logger.Warnf("Scan delayed slices: %s", err)
		return errno(err)
	}
	var ss []Slice
	for _, value := range result {
		ss = ss[:0]
		m.decodeDelayedSlices(value, &ss)
		if showProgress != nil {
			for range ss {
				showProgress()
			}
		}
		for _, s := range ss {
			if s.Id > 0 {
				slices[1] = append(slices[1], s)
			}
		}
	}
	return 0
}

func (m *kvMeta) doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return errno(m.txn(func(tx kvTxn) error {
		attr.Nlink = 2
		_ = tx.scanValues(m.entryKey(inode, ""), -1, func(k, v []byte) bool {
			typ, _ := m.parseEntry(v)
			if typ == TypeDirectory {
				attr.Nlink++
			}
			return false
		})
		tx.set(m.inodeKey(inode), m.marshal(attr))
		return nil
	}, inode))
}

func (m *kvMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer m.timeit(time.Now())
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
	defer m.timeit(time.Now())
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

func (m *kvMeta) doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	key := m.xattrKey(inode, name)
	return errno(m.txn(func(tx kvTxn) error {
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
	}))
}

func (m *kvMeta) doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	key := m.xattrKey(inode, name)
	return errno(m.txn(func(tx kvTxn) error {
		value := tx.get(key)
		if value == nil {
			return ENOATTR
		}
		tx.dels(key)
		return nil
	}))
}

func (m *kvMeta) dumpEntry(inode Ino, e *DumpedEntry) error {
	if m.snap != nil {
		return nil
	}
	return m.client.txn(func(tx kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			logger.Warnf("inode %d not found", inode)
		}

		attr := &Attr{Nlink: 1}
		m.parseAttr(a, attr)
		if a == nil && e.Attr != nil {
			attr.Typ = typeFromString(e.Attr.Type)
		}
		dumpAttr(attr, e.Attr)
		e.Attr.Inode = inode

		vals := tx.scanValues(m.xattrKey(inode, ""), -1, nil)
		if len(vals) > 0 {
			xattrs := make([]*DumpedXattr, 0, len(vals))
			for k, v := range vals {
				xattrs = append(xattrs, &DumpedXattr{k[10:], string(v)}) // "A" + inode + "X"
			}
			sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
			e.Xattrs = xattrs
		}

		if attr.Typ == TypeFile {
			e.Chunks = e.Chunks[:0]
			vals := tx.scanRange(m.chunkKey(inode, 0), m.chunkKey(inode, uint32(attr.Length/ChunkSize)+1))
			for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
				v, ok := vals[string(m.chunkKey(inode, indx))]
				if !ok {
					continue
				}
				ss := readSliceBuf(v)
				slices := make([]*DumpedSlice, 0, len(ss))
				for _, s := range ss {
					slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
				}
				e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
			}
		} else if attr.Typ == TypeSymlink {
			l := tx.get(m.symKey(inode))
			if l == nil {
				logger.Warnf("no link target for inode %d", inode)
			}
			e.Symlink = string(l)
		}
		return nil
	})
}

func (m *kvMeta) dumpDir(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	var entries map[string]*DumpedEntry
	var err error
	var sortedName []string
	if m.snap != nil {
		e := m.snap[inode]
		entries = e.Entries
		for n := range e.Entries {
			sortedName = append(sortedName, n)
		}
	} else {
		vals, err := m.scanValues(m.entryKey(inode, ""), -1, nil)
		if err != nil {
			return err
		}
		entries = map[string]*DumpedEntry{}
		for k, value := range vals {
			name := k[10:]
			typ, inode := m.parseEntry(value)
			sortedName = append(sortedName, name)
			entries[name] = &DumpedEntry{Name: name, Attr: &DumpedAttr{Inode: inode, Type: typeToString(typ)}}
		}
	}
	sort.Slice(sortedName, func(i, j int) bool { return sortedName[i] < sortedName[j] })
	if showProgress != nil && m.snap == nil {
		showProgress(int64(len(entries)), 0)
	}

	if err = tree.writeJsonWithOutEntry(bw, depth); err != nil {
		return err
	}

	for idx, name := range sortedName {
		entry := entries[name]
		entry.Name = name
		inode := entry.Attr.Inode
		err = m.dumpEntry(inode, entry)
		if err != nil {
			return err
		}
		if entry.Attr.Type == "directory" {
			err = m.dumpDir(inode, entry, bw, depth+2, showProgress)
		} else {
			err = entry.writeJSON(bw, depth+2)
		}
		if err != nil {
			return err
		}
		if idx != len(entries)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

func (m *kvMeta) DumpMeta(w io.Writer, root Ino, keepSecret bool) (err error) {
	defer func() {
		if p := recover(); p != nil {
			debug.PrintStack()
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = errors.Errorf("DumpMeta error: %v", p)
			}
		}
	}()
	vals, err := m.scanValues(m.fmtKey("D"), -1, nil)
	if err != nil {
		return err
	}
	dels := make([]*DumpedDelFile, 0, len(vals))
	for k, v := range vals {
		b := utils.FromBuffer([]byte(k[1:])) // "D"
		if b.Len() != 16 {
			logger.Warnf("invalid delfileKey: %s", k)
			continue
		}
		inode := m.decodeInode(b.Get(8))
		dels = append(dels, &DumpedDelFile{inode, b.Get64(), m.parseInt64(v)})
	}

	progress := utils.NewProgress(false, false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)

	if root == 1 { // make snap
		m.snap = make(map[Ino]*DumpedEntry)
		defer func() {
			m.snap = nil
		}()
		bar := progress.AddCountBar("Scan keys", 0)
		bUsed, _ := m.get(m.counterKey(usedSpace))
		bInodes, _ := m.get(m.counterKey(totalInodes))
		used := parseCounter(bUsed)
		inodeTotal := parseCounter(bInodes)
		var guessKeyTotal int64 = 3 // setting, nextInode, nextChunk
		if inodeTotal > 0 {
			guessKeyTotal += int64(math.Ceil((float64(used/inodeTotal/(64*1024*1024)) + float64(3)) * float64(inodeTotal)))
		}
		bar.SetCurrent(0) // Reset
		bar.SetTotal(guessKeyTotal)
		threshold := 0.1
		var cnt int
		err := m.client.scan(nil, func(key, value []byte) {
			if len(key) > 9 && key[0] == 'A' {
				ino := m.decodeInode(key[1:9])
				e := m.snap[ino]
				if e == nil {
					e = &DumpedEntry{}
					m.snap[ino] = e
				}
				switch key[9] {
				case 'I':
					attr := &Attr{Nlink: 1}
					m.parseAttr(value, attr)
					e.Attr = &DumpedAttr{}
					dumpAttr(attr, e.Attr)
					e.Attr.Inode = ino
				case 'C':
					indx := binary.BigEndian.Uint32(key[10:])
					ss := readSliceBuf(value)
					slices := make([]*DumpedSlice, 0, len(ss))
					for _, s := range ss {
						slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
					}
					e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
				case 'D':
					name := string(key[10:])
					_, inode := m.parseEntry(value)
					child := m.snap[inode]
					if child == nil {
						child = &DumpedEntry{}
						m.snap[inode] = child
					}
					if e.Entries == nil {
						e.Entries = map[string]*DumpedEntry{}
					}
					e.Entries[name] = child
				case 'X':
					e.Xattrs = append(e.Xattrs, &DumpedXattr{string(key[10:]), string(value)})
				case 'S':
					e.Symlink = string(value)
				}
			}
			cnt++
			if cnt%100 == 0 && bar.Current() > int64(math.Ceil(float64(guessKeyTotal)*(1-threshold))) {
				guessKeyTotal += int64(math.Ceil(float64(guessKeyTotal) * threshold))
				bar.SetTotal(guessKeyTotal)
			}
			bar.Increment()
		})
		if err != nil {
			return err
		}
		bar.Done()
		tree = m.snap[root]
		trash = m.snap[TrashInode]
	} else {
		tree = &DumpedEntry{
			Attr: &DumpedAttr{
				Inode: root,
				Type:  "directory",
			},
		}
		if err = m.dumpEntry(root, tree); err != nil {
			return err
		}
	}

	if tree == nil || tree.Attr == nil {
		return errors.New("The entry of the root inode was not found")
	}
	tree.Name = "FSTree"

	var rs [][]byte
	err = m.txn(func(tx kvTxn) error {
		rs = tx.gets(m.counterKey(usedSpace),
			m.counterKey(totalInodes),
			m.counterKey("nextInode"),
			m.counterKey("nextChunk"),
			m.counterKey("nextSession"),
			m.counterKey("nextTrash"))
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

	vals, err = m.scanValues(m.fmtKey("SS"), -1, nil)
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
		Setting: m.fmt,
		Counters: &DumpedCounters{
			UsedSpace:   cs[0],
			UsedInodes:  cs[1],
			NextInode:   cs[2],
			NextChunk:   cs[3],
			NextSession: cs[4],
			NextTrash:   cs[5],
		},
		Sustained: sessions,
		DelFiles:  dels,
	}
	if !keepSecret && dm.Setting.SecretKey != "" {
		dm.Setting.SecretKey = "removed"
		logger.Warnf("Secret key is removed for the sake of safety")
	}
	if !keepSecret && dm.Setting.SessionToken != "" {
		dm.Setting.SessionToken = "removed"
		logger.Warnf("Session token is removed for the sake of safety")
	}
	bw, err := dm.writeJsonWithOutTree(w)
	if err != nil {
		return err
	}

	bar := progress.AddCountBar("Dumped entries", 1) // with root
	bar.Increment()
	bar.SetTotal(int64(len(m.snap)))
	if trash != nil {
		trash.Name = "Trash"
		bar.IncrTotal(1)
		bar.Increment()
	}
	showProgress := func(totalIncr, currentIncr int64) {
		bar.IncrTotal(totalIncr)
		bar.IncrInt64(currentIncr)
	}
	if err = m.dumpDir(root, tree, bw, 1, showProgress); err != nil {
		return err
	}
	if trash != nil {
		if _, err = bw.WriteString(","); err != nil {
			return err
		}
		if err = m.dumpDir(TrashInode, trash, bw, 1, showProgress); err != nil {
			return err
		}
	}
	if _, err = bw.WriteString("\n}\n"); err != nil {
		return err
	}
	progress.Done()

	return bw.Flush()
}

type pair struct {
	key   []byte
	value []byte
}

func (m *kvMeta) loadEntry(e *DumpedEntry, kv chan *pair) {
	inode := e.Attr.Inode
	attr := loadAttr(e.Attr)
	attr.Parent = e.Parents[0]
	if attr.Typ == TypeFile {
		attr.Length = e.Attr.Length
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]byte, 0, sliceBytes*len(c.Slices))
			for _, s := range c.Slices {
				slices = append(slices, marshalSlice(s.Pos, s.Id, s.Size, s.Off, s.Len)...)
			}
			kv <- &pair{m.chunkKey(inode, c.Index), slices}
		}
	} else if attr.Typ == TypeDirectory {
		attr.Length = 4 << 10
		for name, c := range e.Entries {
			kv <- &pair{m.entryKey(inode, string(unescape(name))), m.packEntry(typeFromString(c.Attr.Type), c.Attr.Inode)}
		}
	} else if attr.Typ == TypeSymlink {
		symL := unescape(e.Symlink)
		attr.Length = uint64(len(symL))
		kv <- &pair{m.symKey(inode), []byte(symL)}
	}
	for _, x := range e.Xattrs {
		kv <- &pair{m.xattrKey(inode, x.Name), []byte(unescape(x.Value))}
	}
	kv <- &pair{m.inodeKey(inode), m.marshal(attr)}
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
		return fmt.Errorf("Database %s://%s is not empty", m.Name(), m.addr)
	}

	kv := make(chan *pair, 10000)
	batch := 10000
	if m.Name() == "etcd" {
		batch = 128
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buffer []*pair
			for p := range kv {
				buffer = append(buffer, p)
				if len(buffer) >= batch {
					err := m.txn(func(tx kvTxn) error {
						for _, p := range buffer {
							tx.set(p.key, p.value)
						}
						return nil
					})
					if err != nil {
						logger.Fatalf("write %d pairs: %s", len(buffer), err)
					}
					buffer = buffer[:0]
				}
			}
			if len(buffer) > 0 {
				err := m.txn(func(tx kvTxn) error {
					for _, p := range buffer {
						tx.set(p.key, p.value)
					}
					return nil
				})
				if err != nil {
					logger.Fatalf("write %d pairs: %s", len(buffer), err)
				}
			}
		}()
	}

	dm, counters, parents, refs, err := loadEntries(r, func(e *DumpedEntry) { m.loadEntry(e, kv) }, nil)
	if err != nil {
		return err
	}
	format, _ := json.MarshalIndent(dm.Setting, "", "")
	kv <- &pair{m.fmtKey("setting"), format}
	kv <- &pair{m.counterKey(usedSpace), packCounter(counters.UsedSpace)}
	kv <- &pair{m.counterKey(totalInodes), packCounter(counters.UsedInodes)}
	kv <- &pair{m.counterKey("nextInode"), packCounter(counters.NextInode)}
	kv <- &pair{m.counterKey("nextChunk"), packCounter(counters.NextChunk)}
	kv <- &pair{m.counterKey("nextSession"), packCounter(counters.NextSession)}
	kv <- &pair{m.counterKey("nextTrash"), packCounter(counters.NextTrash)}
	for _, d := range dm.DelFiles {
		kv <- &pair{m.delfileKey(d.Inode, d.Length), m.packInt64(d.Expire)}
	}
	for k, v := range refs {
		if v > 1 {
			kv <- &pair{m.sliceKey(k.id, k.size), packCounter(v - 1)}
		}
	}
	close(kv)
	wg.Wait()

	// update nlinks and parents for hardlinks
	st := make(map[Ino]int64)
	return m.txn(func(tx kvTxn) error {
		for i, ps := range parents {
			if len(ps) > 1 {
				a := tx.get(m.inodeKey(i))
				// reset nlink and parent
				binary.BigEndian.PutUint32(a[47:51], uint32(len(ps))) // nlink
				binary.BigEndian.PutUint64(a[63:71], 0)
				tx.set(m.inodeKey(i), a)
				for k := range st {
					delete(st, k)
				}
				for _, p := range ps {
					st[p] = st[p] + 1
				}
				for p, c := range st {
					tx.set(m.parentKey(i, p), packCounter(c))
				}
			}
		}
		return nil
	})
}
