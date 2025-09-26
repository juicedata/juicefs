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
	"context"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/pkg/errors"

	"github.com/juicedata/juicefs/pkg/utils"
)

type kvtxn interface {
	get(key []byte) []byte
	gets(keys ...[]byte) [][]byte
	// scan stops when handler returns false; begin and end must not be nil
	scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool)
	exist(prefix []byte) bool
	set(key, value []byte)
	append(key []byte, value []byte)
	incrBy(key []byte, value int64) int64
	delete(key []byte)
}

type tkvClient interface {
	name() string
	simpleTxn(ctx context.Context, f func(*kvTxn) error, retry int) error // should only be used for point get scenarios
	txn(ctx context.Context, f func(*kvTxn) error, retry int) error
	scan(prefix []byte, handler func(key, value []byte) bool) error
	reset(prefix []byte) error
	close() error
	shouldRetry(err error) bool
	gc()
	config(key string) interface{}
}

type kvTxn struct {
	kvtxn
	retry int
}

func (tx *kvTxn) deleteKeys(prefix []byte) {
	tx.scan(prefix, nextKey(prefix), true, func(k, v []byte) bool {
		tx.delete(k)
		return true
	})
}

type kvMeta struct {
	*baseMeta
	client tkvClient
	snap   map[Ino]*DumpedEntry
}

var _ Meta = (*kvMeta)(nil)
var _ engine = (*kvMeta)(nil)

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
	return m, nil
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
  aclId   aaaa

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
  Uiiiiiiii          data length, space and inodes usage in directory
  Niiiiiiii          detached inde
  QDiiiiiiii         directory quota
  Raaaa			     POSIX acl
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

func (m *kvMeta) dirStatKey(inode Ino) []byte {
	return m.fmtKey("U", inode)
}

func (m *kvMeta) detachedKey(inode Ino) []byte {
	return m.fmtKey("N", inode)
}

func (m *kvMeta) dirQuotaKey(inode Ino) []byte {
	return m.fmtKey("QD", inode)
}

func (m *kvMeta) userQuotaKey(uid uint64) []byte {
	return m.fmtKey("QU", uid)
}

func (m *kvMeta) groupQuotaKey(gid uint64) []byte {
	return m.fmtKey("QG", gid)
}

func (m *kvMeta) aclKey(id uint32) []byte {
	return m.fmtKey("R", id)
}

func (m *kvMeta) parseACLId(key string) uint32 {
	// trim "R"
	rb := utils.ReadBuffer([]byte(key[1:]))
	return rb.Get32()
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

func (m *kvMeta) packDirStat(st *dirStat) []byte {
	b := utils.NewBuffer(24)
	b.Put64(uint64(st.length))
	b.Put64(uint64(st.space))
	b.Put64(uint64(st.inodes))
	return b.Bytes()
}

func (m *kvMeta) parseDirStat(buf []byte) *dirStat {
	b := utils.FromBuffer(buf)
	return &dirStat{int64(b.Get64()), int64(b.Get64()), int64(b.Get64())}
}

func (m *kvMeta) packQuota(q *Quota) []byte {
	b := utils.NewBuffer(32)
	b.Put64(uint64(q.MaxSpace))
	b.Put64(uint64(q.MaxInodes))
	b.Put64(uint64(q.UsedSpace))
	b.Put64(uint64(q.UsedInodes))
	return b.Bytes()
}

func (m *kvMeta) parseQuota(buf []byte) *Quota {
	b := utils.FromBuffer(buf)
	return &Quota{
		MaxSpace:   int64(b.Get64()),
		MaxInodes:  int64(b.Get64()),
		UsedSpace:  int64(b.Get64()),
		UsedInodes: int64(b.Get64()),
	}
}

func (m *kvMeta) get(key []byte) ([]byte, error) {
	var value []byte
	err := m.client.simpleTxn(Background(), func(tx *kvTxn) error {
		value = tx.get(key)
		return nil
	}, 0)
	return value, err
}

func (m *kvMeta) scanKeys(ctx context.Context, prefix []byte) ([][]byte, error) {
	var keys [][]byte
	err := m.client.txn(ctx, func(tx *kvTxn) error {
		tx.scan(prefix, nextKey(prefix), true, func(k, v []byte) bool {
			keys = append(keys, k)
			return true
		})
		return nil
	}, 0)
	return keys, err
}

func (m *kvMeta) scanValues(ctx context.Context, prefix []byte, limit int, filter func(k, v []byte) bool) (map[string][]byte, error) {
	if limit == 0 {
		return nil, nil
	}
	values := make(map[string][]byte)
	err := m.client.txn(ctx, func(tx *kvTxn) error {
		var c int
		tx.scan(prefix, nextKey(prefix), false, func(k, v []byte) bool {
			if filter == nil || filter(k, v) {
				values[string(k)] = v
				c++
			}
			return limit < 0 || c < limit
		})
		return nil
	}, 0)
	return values, err
}

func (m *kvMeta) scan(startKey, endKey []byte, limit int, filter func(k, v []byte) bool) ([][]byte, [][]byte, error) {
	if limit == 0 {
		return nil, nil, nil
	}
	var keys, vals [][]byte
	err := m.client.txn(Background(), func(tx *kvTxn) error {
		var c int
		tx.scan(startKey, endKey, false, func(k, v []byte) bool {
			if filter == nil || filter(k, v) {
				keys = append(keys, k)
				vals = append(vals, v)
				c++
			}
			return limit < 0 || c < limit
		})
		return nil
	}, 0)
	return keys, vals, err
}

func (m *kvMeta) doInit(format *Format, force bool) error {
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
		if !old.DirStats && format.DirStats {
			// remove dir stats as they are outdated
			var keys [][]byte
			prefix := m.fmtKey("U")
			err := m.client.txn(Background(), func(tx *kvTxn) error {
				tx.scan(prefix, nextKey(prefix), true, func(k, v []byte) bool {
					if len(k) == 9 {
						keys = append(keys, k)
					}
					return true
				})
				return nil
			}, 0)
			if err != nil {
				return errors.Wrap(err, "scan dir stats")
			}
			err = m.deleteKeys(keys...)
			if err != nil {
				return errors.Wrap(err, "delete dir stats")
			}
		}
		if !old.UserGroupQuota && format.UserGroupQuota {
			// remove user group quota as they are outdated
			userPrefix := m.fmtKey("QU")
			groupPrefix := m.fmtKey("QG")
			err := m.client.txn(Background(), func(tx *kvTxn) error {
				tx.deleteKeys(userPrefix)
				tx.deleteKeys(groupPrefix)
				return nil
			}, 0)
			if err != nil {
				return errors.Wrap(err, "delete user group quota")
			}
		}
		if err = format.update(&old, force); err != nil {
			return errors.Wrap(err, "update format")
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
	return m.txn(Background(), func(tx *kvTxn) error {
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

func (m *kvMeta) cacheACLs(ctx Context) error {
	if !m.getFormat().EnableACL {
		return nil
	}

	acls, err := m.scanValues(ctx, m.fmtKey("R"), -1, nil)
	if err != nil {
		return err
	}
	for key, val := range acls {
		tmpRule := &aclAPI.Rule{}
		tmpRule.Decode(val)
		m.aclCache.Put(m.parseACLId(key), tmpRule)
	}
	return nil
}

func (m *kvMeta) Reset() error {
	return m.client.reset(nil)
}

func (m *kvMeta) doLoad() ([]byte, error) {
	return m.get(m.fmtKey("setting"))
}

func (m *kvMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *kvMeta) doFlushStats() {
	if space := atomic.LoadInt64(&m.newSpace); space != 0 {
		if v, err := m.incrCounter(usedSpace, space); err == nil {
			atomic.AddInt64(&m.newSpace, -space)
			atomic.StoreInt64(&m.usedSpace, v)
		} else {
			logger.Warnf("Update space stats: %s", err)
		}
	}
	if inodes := atomic.LoadInt64(&m.newInodes); inodes != 0 {
		if v, err := m.incrCounter(totalInodes, inodes); err == nil {
			atomic.AddInt64(&m.newInodes, -inodes)
			atomic.StoreInt64(&m.usedInodes, v)
		} else {
			logger.Warnf("Update inodes stats: %s", err)
		}
	}
}

func (m *kvMeta) doNewSession(sinfo []byte, update bool) error {
	if err := m.setValue(m.sessionKey(m.sid), m.packInt64(m.expireTime())); err != nil {
		return fmt.Errorf("set session ID %d: %s", m.sid, err)
	}
	if err := m.setValue(m.sessionInfoKey(m.sid), sinfo); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}
	return nil
}

func (m *kvMeta) doRefreshSession() error {
	return m.txn(Background(), func(tx *kvTxn) error {
		buf := tx.get(m.sessionKey(m.sid))
		if buf == nil {
			logger.Warnf("Session %d was stale and cleaned up, but now it comes back again", m.sid)
			tx.set(m.sessionInfoKey(m.sid), m.newSessionInfo())
		}
		tx.set(m.sessionKey(m.sid), m.packInt64(m.expireTime()))
		return nil
	})
}

func (m *kvMeta) doCleanStaleSession(sid uint64) error {
	var fail bool
	// release locks
	ctx := Background()
	if flocks, err := m.scanValues(ctx, m.fmtKey("F"), -1, nil); err == nil {
		for k, v := range flocks {
			ls := unmarshalFlock(v)
			for o := range ls {
				if o.sid == sid {
					if err = m.txn(ctx, func(tx *kvTxn) error {
						v := tx.get([]byte(k))
						ls := unmarshalFlock(v)
						delete(ls, o)
						if len(ls) > 0 {
							tx.set([]byte(k), marshalFlock(ls))
						} else {
							tx.delete([]byte(k))
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

	if plocks, err := m.scanValues(ctx, m.fmtKey("P"), -1, nil); err == nil {
		for k, v := range plocks {
			ls := unmarshalPlock(v)
			for o := range ls {
				if o.sid == sid {
					if err = m.txn(ctx, func(tx *kvTxn) error {
						v := tx.get([]byte(k))
						ls := unmarshalPlock(v)
						delete(ls, o)
						if len(ls) > 0 {
							tx.set([]byte(k), marshalPlock(ls))
						} else {
							tx.delete([]byte(k))
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

	if keys, err := m.scanKeys(ctx, m.fmtKey("SS", sid)); err == nil {
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
	ctx := Background()
	vals, err := m.scanValues(ctx, m.fmtKey("SE"), limit, func(k, v []byte) bool {
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
	vals, err = m.scanValues(ctx, m.fmtKey("SH"), limit, func(k, v []byte) bool {
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
		ctx := Background()
		inodes, err := m.scanKeys(ctx, m.fmtKey("SS", sid))
		if err != nil {
			return nil, err
		}
		s.Sustained = make([]Ino, 0, len(inodes))
		for _, sinode := range inodes {
			inode := m.decodeInode(sinode[10:]) // "SS" + sid
			s.Sustained = append(s.Sustained, inode)
		}
		flocks, err := m.scanValues(ctx, m.fmtKey("F"), -1, nil)
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
		plocks, err := m.scanValues(ctx, m.fmtKey("P"), -1, nil)
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
	ctx := Background()
	vals, err := m.scanValues(ctx, m.fmtKey("SE"), -1, nil)
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
	vals, err = m.scanValues(ctx, m.fmtKey("SH"), -1, nil)
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

func (m *kvMeta) txn(ctx context.Context, f func(tx *kvTxn) error, inodes ...Ino) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()
	defer m.txBatchLock(inodes...)()
	var (
		lastErr error
		method  string
	)
	for i := 0; i < 50; i++ {
		err := m.client.txn(ctx, f, i)
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if err != nil && m.shouldRetry(err) {
			if method == "" {
				method = callerName(ctx) // lazy evaluation
			}
			m.txRestart.WithLabelValues(method).Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(rand.Int()%((i+1)*(i+1))))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Transaction succeeded after %d tries (%s), inodes: %v, method: %s, error: %s", i+1, time.Since(start), inodes, method, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
}

func (m *kvMeta) setValue(key, value []byte) error {
	return m.txn(Background(), func(tx *kvTxn) error {
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
	err := m.txn(Background().WithValue(txMethodKey{}, "incrCounter:"+name), func(tx *kvTxn) error {
		new = tx.incrBy(key, value)
		return nil
	})
	return new, err
}

func (m *kvMeta) setIfSmall(name string, value, diff int64) (bool, error) {
	var changed bool
	key := m.counterKey(name)
	err := m.txn(Background().WithValue(txMethodKey{}, "setIfSmall:"+name), func(tx *kvTxn) error {
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
	return m.txn(Background(), func(tx *kvTxn) error {
		for _, key := range keys {
			tx.delete(key)
		}
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
		m.of.Update(foundIno, attr)
	} else if err == nil {
		logger.Warnf("no attribute for inode %d (%d, %s)", foundIno, parent, name)
		*attr = Attr{Typ: foundType}
	}
	*inode = foundIno
	return errno(err)
}

func (m *kvMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return errno(m.client.simpleTxn(ctx, func(tx *kvTxn) error {
		val := tx.get(m.inodeKey(inode))
		if val == nil {
			return syscall.ENOENT
		}
		m.parseAttr(val, attr)
		return nil
	}, 0))
}

func (m *kvMeta) doSetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr, oldAttr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		var cur Attr
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, &cur)
		if oldAttr != nil {
			*oldAttr = cur
		}
		if cur.Parent > TrashInode {
			return syscall.EPERM
		}
		now := time.Now()

		rule, err := m.getACL(tx, cur.AccessACL)
		if err != nil {
			return err
		}

		rule = rule.Dup()
		dirtyAttr, st := m.mergeAttr(ctx, inode, set, &cur, attr, now, rule)
		if st != 0 {
			return st
		}
		if dirtyAttr == nil {
			return nil
		}

		dirtyAttr.AccessACL, err = m.insertACL(tx, rule)
		if err != nil {
			return err
		}

		dirtyAttr.Ctime = now.Unix()
		dirtyAttr.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(dirtyAttr))
		*attr = *dirtyAttr
		return nil
	}, inode))
}

func (m *kvMeta) doTruncate(ctx Context, inode Ino, flags uint8, length uint64, delta *dirStat, attr *Attr, skipPermCheck bool) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		*delta = dirStat{}
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		t := Attr{}
		m.parseAttr(a, &t)
		if t.Typ != TypeFile || t.Flags&(FlagImmutable|t.Flags&FlagAppend) != 0 || (flags == 0 && t.Parent > TrashInode) {
			return syscall.EPERM
		}
		if !skipPermCheck {
			if st := m.Access(ctx, inode, MODE_MASK_W, &t); st != 0 {
				return st
			}
		}
		if length == t.Length {
			*attr = t
			return nil
		}
		delta.length = int64(length) - int64(t.Length)
		delta.space = align4K(length) - align4K(t.Length)
		if err := m.checkQuota(ctx, delta.space, 0, t.Uid, t.Gid, m.getParents(tx, inode, t.Parent)...); err != 0 {
			return err
		}
		var left, right = t.Length, length
		if left > right {
			right, left = left, right
		}
		if right/ChunkSize-left/ChunkSize > 1 {
			buf := marshalSlice(0, 0, 0, 0, ChunkSize)
			tx.scan(m.chunkKey(inode, uint32(left/ChunkSize)+1), m.chunkKey(inode, uint32(right/ChunkSize)),
				false, func(k, v []byte) bool {
					tx.set(k, append(v, buf...))
					return true
				})
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
		*attr = t
		return nil
	}, inode))
}

func (m *kvMeta) doFallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		*delta = dirStat{}
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		t := Attr{}
		m.parseAttr(a, &t)
		if t.Typ == TypeFIFO {
			return syscall.EPIPE
		}
		if t.Typ != TypeFile || (t.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if st := m.Access(ctx, inode, MODE_MASK_W, &t); st != 0 {
			return st
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
		delta.length = int64(length) - int64(t.Length)
		delta.space = align4K(length) - align4K(t.Length)
		if err := m.checkQuota(ctx, delta.space, 0, t.Uid, t.Gid, m.getParents(tx, inode, t.Parent)...); err != 0 {
			return err
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
		*attr = t
		return nil
	}, inode))
}

func (m *kvMeta) doReadlink(ctx Context, inode Ino, noatime bool) (atime int64, target []byte, err error) {
	if noatime {
		target, err = m.get(m.symKey(inode))
		return
	}

	attr := &Attr{}
	now := time.Now()
	err = m.txn(ctx, func(tx *kvTxn) error {
		rs := tx.gets(m.inodeKey(inode), m.symKey(inode))
		if rs[0] == nil {
			return syscall.ENOENT
		}
		m.parseAttr(rs[0], attr)
		if attr.Typ != TypeSymlink {
			return syscall.EINVAL
		}
		if rs[1] == nil {
			return syscall.EIO
		}
		target = rs[1]
		if !m.atimeNeedsUpdate(attr, now) {
			atime = attr.Atime*int64(time.Second) + int64(attr.Atimensec)
			return nil
		}
		attr.Atime = now.Unix()
		attr.Atimensec = uint32(now.Nanosecond())
		atime = now.UnixNano()
		tx.set(m.inodeKey(inode), m.marshal(attr))
		return nil
	}, inode)
	return
}

func (m *kvMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, path string, inode *Ino, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		var pattr Attr
		rs := tx.gets(m.inodeKey(parent), m.entryKey(parent, name))
		if rs[0] == nil {
			return syscall.ENOENT
		}
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if pattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if (pattr.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if (pattr.Flags & FlagSkipTrash) != 0 {
			attr.Flags |= FlagSkipTrash
		}

		buf := rs[1]
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
				a := tx.get(m.inodeKey(foundIno))
				if a != nil {
					m.parseAttr(a, attr)
				} else {
					*attr = Attr{Typ: foundType, Parent: parent} // corrupt entry
				}
				*inode = foundIno
			}
			return syscall.EEXIST
		} else if parent == TrashInode { // user's inode is allocated by prefetch, trash inode is allocated on demand
			key := m.counterKey("nextTrash")
			next := tx.incrBy(key, 1)
			*inode = TrashInode + Ino(next)
		}

		mode &= 07777
		if pattr.DefaultACL != aclAPI.None && _type != TypeSymlink {
			// inherit default acl
			if _type == TypeDirectory {
				attr.DefaultACL = pattr.DefaultACL
			}

			// set access acl by parent's default acl
			rule, err := m.getACL(tx, pattr.DefaultACL)
			if err != nil {
				return err
			}

			if rule.IsMinimal() {
				// simple acl as default
				attr.Mode = mode & (0xFE00 | rule.GetMode())
			} else {
				cRule := rule.ChildAccessACL(mode)
				id, err := m.insertACL(tx, cRule)
				if err != nil {
					return err
				}

				attr.AccessACL = id
				attr.Mode = (mode & 0xFE00) | cRule.GetMode()
			}
		} else {
			attr.Mode = mode & ^cumask
		}

		var updateParent bool
		now := time.Now()
		if parent != TrashInode {
			if _type == TypeDirectory {
				pattr.Nlink++
				if m.conf.SkipDirNlink <= 0 || tx.retry < m.conf.SkipDirNlink {
					updateParent = true
				} else {
					logger.Warnf("Skip updating nlink of directory %d to reduce conflict", parent)
				}
			}
			if updateParent || now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
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
		if ctx.Value(CtxKey("behavior")) == "Hadoop" || runtime.GOOS == "darwin" {
			attr.Gid = pattr.Gid
		} else if runtime.GOOS == "linux" && pattr.Mode&02000 != 0 {
			attr.Gid = pattr.Gid
			if _type == TypeDirectory {
				attr.Mode |= 02000
			} else if attr.Mode&02010 == 02010 && ctx.Uid() != 0 {
				var found bool
				for _, gid := range ctx.Gids() {
					if gid == pattr.Gid {
						found = true
					}
				}
				if !found {
					attr.Mode &= ^uint16(02000)
				}
			}
		}

		tx.set(m.entryKey(parent, name), m.packEntry(_type, *inode))
		if updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		tx.set(m.inodeKey(*inode), m.marshal(attr))
		if _type == TypeSymlink {
			tx.set(m.symKey(*inode), []byte(path))
		}
		if _type == TypeDirectory {
			tx.set(m.dirStatKey(*inode), m.packDirStat(&dirStat{}))
		}
		return nil
	}, parent))
}

func (m *kvMeta) doUnlink(ctx Context, parent Ino, name string, attr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
	}

	if attr == nil {
		attr = &Attr{}
	}
	var _type uint8
	var inode Ino
	var opened bool
	var newSpace, newInode int64
	err := m.txn(ctx, func(tx *kvTxn) error {
		opened = false
		*attr = Attr{}
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
		keys := [][]byte{m.inodeKey(parent), m.inodeKey(inode)}
		if trash > 0 {
			keys = append(keys, m.entryKey(trash, m.trashEntry(parent, inode, name)))
		}
		rs := tx.gets(keys...)
		if rs[0] == nil {
			return syscall.ENOENT
		}
		var pattr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if (pattr.Flags&FlagAppend) != 0 || (pattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		opened = false
		now := time.Now()
		if rs[1] != nil {
			m.parseAttr(rs[1], attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			if (attr.Flags&FlagAppend) != 0 || (attr.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			if (attr.Flags & FlagSkipTrash) != 0 {
				trash = 0
			}
			if trash > 0 && attr.Nlink > 1 && rs[2] != nil {
				trash = 0
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

		defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
		var updateParent bool
		if !parent.IsTrash() && now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}

		tx.delete(m.entryKey(parent, name))
		if updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		if attr.Nlink > 0 {
			tx.set(m.inodeKey(inode), m.marshal(attr))
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
					tx.set(m.inodeKey(inode), m.marshal(attr))
					tx.set(m.sustainedKey(m.sid, inode), []byte{1})
				} else {
					tx.set(m.delfileKey(inode, attr.Length), m.packInt64(now.Unix()))
					tx.delete(m.inodeKey(inode))
					newSpace, newInode = -align4K(attr.Length), -1
				}
			case TypeSymlink:
				tx.delete(m.symKey(inode))
				fallthrough
			default:
				tx.delete(m.inodeKey(inode))
				newSpace, newInode = -align4K(0), -1
			}
			tx.deleteKeys(m.xattrKey(inode, ""))
			if attr.Parent == 0 {
				tx.deleteKeys(m.fmtKey("A", inode, "P"))
			}
		}
		return nil
	}, parent)
	if err == nil && trash == 0 {
		if _type == TypeFile && attr.Nlink == 0 {
			m.fileDeleted(opened, parent.IsTrash(), inode, attr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) doRmdir(ctx Context, parent Ino, name string, pinode *Ino, oldAttr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
	}
	err := m.txn(ctx, func(tx *kvTxn) error {
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
		if pinode != nil {
			*pinode = inode
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
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
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
			if oldAttr != nil {
				*oldAttr = attr
			}
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			if (attr.Flags & FlagSkipTrash) != 0 {
				trash = 0
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
		var updateParent bool
		if m.conf.SkipDirNlink <= 0 || tx.retry < m.conf.SkipDirNlink {
			updateParent = true
		} else {
			logger.Warnf("Skip updating nlink of directory %d to reduce conflict", parent)
		}
		if updateParent || now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}

		if !parent.IsTrash() && updateParent {
			tx.set(m.inodeKey(parent), m.marshal(&pattr))
		}
		tx.delete(m.entryKey(parent, name))
		tx.delete(m.dirStatKey(inode))
		tx.delete(m.dirQuotaKey(inode))
		if trash > 0 {
			tx.set(m.inodeKey(inode), m.marshal(&attr))
			tx.set(m.entryKey(trash, m.trashEntry(parent, inode, name)), buf)
		} else {
			tx.delete(m.inodeKey(inode))
			tx.deleteKeys(m.xattrKey(inode, ""))
		}
		return nil
	}, parent)
	if err == nil && trash == 0 {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *kvMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode, tInode *Ino, attr, tAttr *Attr) syscall.Errno {
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
	parentLocks := []Ino{parentDst}
	if !parentSrc.IsTrash() { // there should be no conflict if parentSrc is in trash, relax lock to accelerate `restore` subcommand
		parentLocks = append(parentLocks, parentSrc)
	}
	err := m.txn(ctx, func(tx *kvTxn) error {
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
		if st := m.Access(ctx, parentSrc, MODE_MASK_W|MODE_MASK_X, &sattr); st != 0 {
			return st
		}
		m.parseAttr(rs[1], &dattr)
		if dattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if flags&RenameRestore == 0 && dattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parentDst, MODE_MASK_W|MODE_MASK_X, &dattr); st != 0 {
			return st
		}
		// TODO: check parentDst is a subdir of source node
		if ino == parentDst || ino == dattr.Parent {
			return syscall.EPERM
		}
		m.parseAttr(rs[2], &iattr)
		if (sattr.Flags&FlagAppend) != 0 || (sattr.Flags&FlagImmutable) != 0 || (dattr.Flags&FlagImmutable) != 0 || (iattr.Flags&FlagAppend) != 0 || (iattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if parentSrc != parentDst && sattr.Mode&0o1000 != 0 && ctx.Uid() != 0 &&
			ctx.Uid() != iattr.Uid && (ctx.Uid() != sattr.Uid || iattr.Typ == TypeDirectory) {
			return syscall.EACCES
		}

		dbuf := tx.get(m.entryKey(parentDst, nameDst))
		if dbuf == nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				if string(e.Name) != nameSrc || parentDst != parentSrc {
					nameDst = string(e.Name)
					dbuf = m.packEntry(e.Attr.Typ, e.Inode)
				}
			}
		}
		var supdate, dupdate bool
		now := time.Now()
		if dbuf != nil {
			if flags&RenameNoReplace != 0 {
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
			if (tattr.Flags & FlagSkipTrash) != 0 {
				trash = 0
			}
			tattr.Ctime = now.Unix()
			tattr.Ctimensec = uint32(now.Nanosecond())
			if exchange {
				if parentSrc != parentDst {
					if dtyp == TypeDirectory {
						tattr.Parent = parentSrc
						dattr.Nlink--
						sattr.Nlink++
						if m.conf.SkipDirNlink <= 0 || tx.retry < m.conf.SkipDirNlink {
							supdate, dupdate = true, true
						} else {
							logger.Warnf("Skip updating nlink of directory %d,%d to reduce conflict", parentSrc, parentDst)
						}
					} else if tattr.Parent > 0 {
						tattr.Parent = parentSrc
					}
				}
			} else if dino == ino {
				return nil
			} else if typ == TypeDirectory && dtyp != TypeDirectory {
				return syscall.ENOTDIR
			} else if typ != TypeDirectory && dtyp == TypeDirectory {
				return syscall.EISDIR
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
						if dtyp == TypeFile && tattr.Nlink == 0 && m.sid > 0 {
							opened = m.of.IsOpen(dino)
						}
						defer func() { m.of.InvalidateChunk(dino, invalidateAttrOnly) }()
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
				if m.conf.SkipDirNlink <= 0 || tx.retry < m.conf.SkipDirNlink {
					supdate, dupdate = true, true
				} else {
					logger.Warnf("Skip updating nlink of directory %d,%d to reduce conflict", parentSrc, parentDst)
				}
			} else if iattr.Parent > 0 {
				iattr.Parent = parentDst
			}
		}
		if supdate || now.Sub(time.Unix(sattr.Mtime, int64(sattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
			sattr.Mtime = now.Unix()
			sattr.Mtimensec = uint32(now.Nanosecond())
			sattr.Ctime = now.Unix()
			sattr.Ctimensec = uint32(now.Nanosecond())
			supdate = true
		}
		if dupdate || now.Sub(time.Unix(dattr.Mtime, int64(dattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
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
		if dino > 0 {
			*tInode = dino
			*tAttr = tattr
		}

		if exchange { // dino > 0
			tx.set(m.entryKey(parentSrc, nameSrc), dbuf)
			tx.set(m.inodeKey(dino), m.marshal(&tattr))
			if parentSrc != parentDst && tattr.Parent == 0 {
				tx.incrBy(m.parentKey(dino, parentSrc), 1)
				tx.incrBy(m.parentKey(dino, parentDst), -1)
			}
		} else {
			tx.delete(m.entryKey(parentSrc, nameSrc))
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
							tx.delete(m.inodeKey(dino))
							newSpace, newInode = -align4K(tattr.Length), -1
						}
					} else {
						if dtyp == TypeSymlink {
							tx.delete(m.symKey(dino))
						}
						tx.delete(m.inodeKey(dino))
						newSpace, newInode = -align4K(0), -1
					}
					tx.deleteKeys(m.xattrKey(dino, ""))
					if tattr.Parent == 0 {
						tx.deleteKeys(m.fmtKey("A", dino, "P"))
					}
				}
				if dtyp == TypeDirectory {
					tx.delete(m.dirQuotaKey(dino))
				}
			}
		}
		if parentDst != parentSrc {
			if !parentSrc.IsTrash() && supdate {
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
	}, parentLocks...)
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dtyp == TypeFile && tattr.Nlink == 0 {
			m.fileDeleted(opened, false, dino, tattr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *kvMeta) doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		rs := tx.gets(m.inodeKey(parent), m.inodeKey(inode))
		if rs[0] == nil || rs[1] == nil {
			return syscall.ENOENT
		}
		var pattr, iattr Attr
		m.parseAttr(rs[0], &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if pattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
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
		if buf != nil || m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		var updateParent bool
		now := time.Now()
		if now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime*time.Duration(tx.retry+1) {
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

func (m *kvMeta) fillAttr(entries []*Entry) (err error) {
	if len(entries) == 0 {
		return nil
	}
	var keys = make([][]byte, len(entries))
	for i, e := range entries {
		keys[i] = m.inodeKey(e.Inode)
	}
	var rs [][]byte
	err = m.client.simpleTxn(Background(), func(tx *kvTxn) error {
		rs = tx.gets(keys...)
		return nil
	}, 0)
	if err != nil {
		return err
	}
	for j, re := range rs {
		if re != nil {
			m.parseAttr(re, entries[j].Attr)
			// If `readdirplus` returns complete attributes, kernel may not invoke `GetAttr`. Therefore, we must also validate chunk cache here to prevent stale cache, which may lead to data corruption.
			m.of.Update(entries[j].Inode, entries[j].Attr)
		}
	}
	return err
}

func (m *kvMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	// TODO: handle big directory
	vals, err := m.scanValues(ctx, m.entryKey(inode, ""), limit, nil)
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
		if ctx.Canceled() {
			return errno(ctx.Err())
		}
		batchSize := 4096
		nEntries := len(*entries)
		if nEntries <= batchSize {
			err = m.fillAttr(*entries)
		} else {
			indexCh := make(chan []*Entry, 10)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for es := range indexCh {
						if e := m.fillAttr(es); e != nil {
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
	err := m.txn(Background(), func(tx *kvTxn) error {
		newSpace = 0
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return nil
		}
		m.parseAttr(a, &attr)
		newSpace = -align4K(attr.Length)
		tx.set(m.delfileKey(inode, attr.Length), m.packInt64(time.Now().Unix()))
		tx.delete(m.inodeKey(inode))
		tx.delete(m.sustainedKey(sid, inode))
		return nil
	}, inode)
	if err == nil && newSpace < 0 {
		m.updateStats(newSpace, -1)
		m.tryDeleteFileData(inode, attr.Length, false)
	}
	return err
}

func (m *kvMeta) doRead(ctx Context, inode Ino, indx uint32) ([]*slice, syscall.Errno) {
	val, err := m.get(m.chunkKey(inode, indx))
	if err != nil {
		return nil, errno(err)
	}
	return readSliceBuf(val), 0
}

func (m *kvMeta) doWrite(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time, numSlices *int, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		*delta = dirStat{}
		*attr = Attr{}
		rs := tx.gets(m.inodeKey(inode), m.chunkKey(inode, indx))
		if rs[0] == nil {
			return syscall.ENOENT
		}
		m.parseAttr(rs[0], attr)
		if attr.Typ != TypeFile {
			return syscall.EPERM
		}
		if len(rs[1])%sliceBytes != 0 {
			logger.Errorf("Invalid chunk value for inode %d indx %d: %d", inode, indx, len(rs[1]))
			return syscall.EIO
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		if newleng > attr.Length {
			delta.length = int64(newleng - attr.Length)
			delta.space = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if err := m.checkQuota(ctx, delta.space, 0, attr.Uid, attr.Gid, m.getParents(tx, inode, attr.Parent)...); err != 0 {
			return err
		}
		now := time.Now()
		attr.Mtime = mtime.Unix()
		attr.Mtimensec = uint32(mtime.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		val := marshalSlice(off, slice.Id, slice.Size, slice.Off, slice.Len)
		for i := 0; i < len(rs[1]); i += sliceBytes {
			if bytes.Equal(rs[1][i:i+sliceBytes], val) {
				logger.Warnf("Write same slice for inode %d indx %d sliceId %d", inode, indx, slice.Id)
				return nil
			}
		}
		val = append(rs[1], val...)
		tx.set(m.inodeKey(inode), m.marshal(attr))
		tx.set(m.chunkKey(inode, indx), val)
		*numSlices = len(val) / sliceBytes
		return nil
	}, inode))
}

func (m *kvMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied, outLength *uint64) syscall.Errno {
	defer m.timeit("CopyFileRange", time.Now())
	var newLength, newSpace int64
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(fout, invalidateAllChunks) }()
	var sattr, attr Attr
	err := m.txn(ctx, func(tx *kvTxn) error {
		newLength, newSpace = 0, 0
		rs := tx.gets(m.inodeKey(fin), m.inodeKey(fout))
		if rs[0] == nil || rs[1] == nil {
			return syscall.ENOENT
		}
		sattr = Attr{}
		m.parseAttr(rs[0], &sattr)
		if sattr.Typ != TypeFile {
			return syscall.EINVAL
		}
		if offIn >= sattr.Length {
			if copied != nil {
				*copied = 0
			}
			return nil
		}
		size := size
		if offIn+size > sattr.Length {
			size = sattr.Length - offIn
		}
		attr = Attr{}
		m.parseAttr(rs[1], &attr)
		if attr.Typ != TypeFile {
			return syscall.EINVAL
		}
		if (attr.Flags&FlagImmutable) != 0 || (attr.Flags&FlagAppend) != 0 {
			return syscall.EPERM
		}

		newleng := offOut + size
		if newleng > attr.Length {
			newLength = int64(newleng - attr.Length)
			newSpace = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if err := m.checkQuota(ctx, newSpace, 0, attr.Uid, attr.Gid, m.getParents(tx, fout, attr.Parent)...); err != 0 {
			return err
		}
		now := time.Now()
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		if outLength != nil {
			*outLength = attr.Length
		}

		vals := make(map[string][]byte)
		tx.scan(m.chunkKey(fin, uint32(offIn/ChunkSize)), m.chunkKey(fin, uint32((offIn+size)/ChunkSize)+1),
			false, func(k, v []byte) bool {
				vals[string(k)] = v
				return true
			})
		chunks := make(map[uint32][]*slice)
		for indx := uint32(offIn / ChunkSize); indx <= uint32((offIn+size)/ChunkSize); indx++ {
			if v, ok := vals[string(m.chunkKey(fin, indx))]; ok {
				chunks[indx] = readSliceBuf(v)
				if chunks[indx] == nil {
					return syscall.EIO
				}
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
		if copied != nil {
			*copied = size
		}
		return nil
	}, fout)
	if err == nil {
		m.updateParentStat(ctx, fout, attr.Parent, newLength, newSpace)
	}
	return errno(err)
}

func (m *kvMeta) getParents(tx *kvTxn, inode, parent Ino) []Ino {
	if parent > 0 {
		return []Ino{parent}
	}
	var ps []Ino
	prefix := m.fmtKey("A", inode, "P")
	tx.scan(prefix, nextKey(prefix), false, func(k, v []byte) bool {
		if len(k) == 1+8+1+8 && parseCounter(v) > 0 {
			ps = append(ps, m.decodeInode([]byte(k[10:])))
		}
		return true
	})
	return ps
}

func (m *kvMeta) doGetParents(ctx Context, inode Ino) map[Ino]int {
	vals, err := m.scanValues(ctx, m.fmtKey("A", inode, "P"), -1, func(k, v []byte) bool {
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

func (m *kvMeta) doSyncDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
	if m.conf.ReadOnly {
		return nil, syscall.EROFS
	}
	stat, st := m.calcDirStat(ctx, ino)
	if st != 0 {
		return nil, st
	}
	err := m.client.txn(ctx, func(tx *kvTxn) error {
		if tx.get(m.inodeKey(ino)) == nil {
			return syscall.ENOENT
		}
		tx.set(m.dirStatKey(ino), m.packDirStat(stat))
		return nil
	}, 0)
	if err != nil && m.shouldRetry(err) {
		// other clients have synced
		err = nil
	}
	return stat, errno(err)
}

func (m *kvMeta) doUpdateDirStat(ctx Context, batch map[Ino]dirStat) error {
	syncMap := make(map[Ino]bool, 0)
	for _, group := range m.groupBatch(batch, 20) {
		err := m.txn(ctx, func(tx *kvTxn) error {
			keys := make([][]byte, 0, len(group))
			for _, ino := range group {
				keys = append(keys, m.dirStatKey(ino))
			}
			for i, rawStat := range tx.gets(keys...) {
				ino := group[i]
				if rawStat == nil {
					syncMap[ino] = true
					continue
				}
				st := m.parseDirStat(rawStat)
				stat := batch[ino]
				st.length += stat.length
				st.space += stat.space
				st.inodes += stat.inodes
				if st.length < 0 || st.space < 0 || st.inodes < 0 {
					logger.Warnf("dir stat of inode %d is invalid: %+v, try to sync", ino, st)
					syncMap[ino] = true
					continue
				}
				tx.set(keys[i], m.packDirStat(st))
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if len(syncMap) > 0 {
		m.parallelSyncDirStat(ctx, syncMap).Wait()
	}
	return nil
}

func (m *kvMeta) doGetDirStat(ctx Context, ino Ino, trySync bool) (*dirStat, syscall.Errno) {
	rawStat, err := m.get(m.dirStatKey(ino))
	if err != nil {
		return nil, errno(err)
	}
	if rawStat != nil {
		return m.parseDirStat(rawStat), 0
	}
	if trySync {
		return m.doSyncDirStat(ctx, ino)
	}
	return nil, 0
}

func (m *kvMeta) doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) {
	if limit == 0 {
		return nil, nil
	}
	klen := 1 + 8 + 8
	files := make(map[Ino]uint64)
	var count int
	err := m.client.scan(m.fmtKey("D"), func(k, v []byte) bool {
		if len(k) == klen && len(v) == 8 && m.parseInt64(v) < ts {
			rb := utils.FromBuffer([]byte(k)[1:])
			files[m.decodeInode(rb.Get(8))] = rb.Get64()
			count++
		}
		return limit < 0 || count < limit
	})
	return files, err
}

func (m *kvMeta) doCleanupSlices(ctx Context) {
	if m.Name() == "tikv" {
		m.client.gc()
	}
	klen := 1 + 8 + 4
	_ = m.client.scan(m.fmtKey("K"), func(k, v []byte) bool {
		if len(k) == klen && len(v) == 8 && parseCounter(v) <= 0 {
			rb := utils.FromBuffer(k[1:])
			id := rb.Get64()
			size := rb.Get32()
			refs := parseCounter(v)
			if refs < 0 {
				m.deleteSlice(id, size)
			} else {
				m.cleanupZeroRef(id, size)
			}
			if ctx.Canceled() {
				return false
			}
		}
		return true
	})
}

func (m *kvMeta) deleteChunk(inode Ino, indx uint32) error {
	key := m.chunkKey(inode, indx)
	var todel []*slice
	err := m.txn(Background(), func(tx *kvTxn) error {
		todel = todel[:0]
		buf := tx.get(key)
		slices := readSliceBuf(buf)
		if slices == nil {
			logger.Errorf("Corrupt value for inode %d chunk index %d, use `gc` to clean up leaked slices", inode, indx)
		}
		tx.delete(key)
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

func (m *kvMeta) cleanupZeroRef(id uint64, size uint32) {
	_ = m.txn(Background(), func(tx *kvTxn) error {
		v := tx.incrBy(m.sliceKey(id, size), 0)
		if v != 0 {
			return syscall.EINVAL
		}
		tx.delete(m.sliceKey(id, size))
		return nil
	})
}

func (m *kvMeta) doDeleteFileData(inode Ino, length uint64) {
	keys, err := m.scanKeys(Background(), m.fmtKey("A", inode, "C"))
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

func (m *kvMeta) doCleanupDelayedSlices(ctx Context, edge int64) (int, error) {
	var count int
	var ss []Slice
	var rs []int64
	var keys [][]byte
	var batch int = 1e5
	for {
		if err := m.client.txn(ctx, func(tx *kvTxn) error {
			keys = keys[:0]
			var c int
			tx.scan(m.delSliceKey(0, 0), m.delSliceKey(edge, 0),
				true, func(k, v []byte) bool {
					if len(k) == 1+8+8 { // delayed slices: Lttttttttcccccccc
						keys = append(keys, k)
						c++
					}
					return c < batch
				})
			return nil
		}, 0); err != nil {
			logger.Warnf("Scan delayed slices: %s", err)
			return count, err
		}

		for _, key := range keys {
			if err := m.txn(ctx, func(tx *kvTxn) error {
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
				tx.delete(key)
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
				if ctx.Canceled() {
					return count, nil
				}
			}
		}
		if len(keys) < batch {
			break
		}
	}
	return count, nil
}

func (m *kvMeta) doCompactChunk(inode Ino, indx uint32, buf []byte, ss []*slice, skipped int, pos uint32, id uint64, size uint32, delayed []byte) syscall.Errno {
	st := errno(m.txn(Background(), func(tx *kvTxn) error {
		buf2 := tx.get(m.chunkKey(inode, indx))
		if len(buf2) < len(buf) || !bytes.Equal(buf, buf2[:len(buf)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(buf), len(buf2))
			return syscall.EINVAL
		}

		buf2 = append(append(buf2[:skipped*sliceBytes], marshalSlice(pos, id, size, 0, size)...), buf2[len(buf):]...)
		tx.set(m.chunkKey(inode, indx), buf2)
		// create the key to tracking it
		tx.set(m.sliceKey(id, size), make([]byte, 8))
		if delayed != nil {
			if len(delayed) > 0 {
				tx.set(m.delSliceKey(time.Now().Unix(), id), delayed)
			}
		} else {
			for _, s := range ss {
				if s.id > 0 {
					tx.incrBy(m.sliceKey(s.id, s.size), -1)
				}
			}
		}
		return nil
	}, inode)) // less conflicts with `write`
	// there could be false-negative that the compaction is successful, double-check
	if st != 0 && st != syscall.EINVAL {
		refs, e := m.get(m.sliceKey(id, size))
		if e == nil {
			if len(refs) > 0 {
				st = 0
			} else {
				logger.Infof("compacted chunk %d was not used", id)
				st = syscall.EINVAL
			}
		}
	}

	if st == syscall.EINVAL {
		_ = m.txn(Background(), func(tx *kvTxn) error {
			tx.incrBy(m.sliceKey(id, size), -1)
			return nil
		})
	} else if st == 0 {
		m.cleanupZeroRef(id, size)
		if delayed == nil {
			var refs int64
			for _, s := range ss {
				if s.id > 0 && m.client.txn(Background(), func(tx *kvTxn) error {
					refs = tx.incrBy(m.sliceKey(s.id, s.size), 0)
					return nil
				}, 0) == nil && refs < 0 {
					m.deleteSlice(s.id, s.size)
				}
			}
		}
	}
	return st
}

func (m *kvMeta) scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error {
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	return m.client.scan(m.fmtKey("A"), func(k, v []byte) bool {
		if len(k) == klen && k[1+8] == 'C' && len(v) > sliceBytes {
			bar.IncrTotal(1)
			ch <- cchunk{
				inode:  m.decodeInode(k[1:9]),
				indx:   binary.BigEndian.Uint32(k[10:]),
				slices: len(v) / sliceBytes,
			}
		}
		return true
	})
}

func (m *kvMeta) ListSlices(ctx Context, slices map[Ino][]Slice, scanPending, delete bool, showProgress func()) syscall.Errno {
	if delete {
		m.doCleanupSlices(ctx)
	}
	// AiiiiiiiiCnnnn     file chunks
	klen := 1 + 8 + 1 + 4
	_ = m.client.scan(m.fmtKey("A"), func(key, value []byte) bool {
		if len(key) != klen || key[1+8] != 'C' {
			return true
		}
		inode := m.decodeInode([]byte(key)[1:9])
		ss := readSliceBuf(value)
		if ss == nil {
			logger.Errorf("Corrupt value for inode %d chunk key %s", inode, key)
			return true
		}
		for _, s := range ss {
			if s.id > 0 {
				slices[inode] = append(slices[inode], Slice{Id: s.id, Size: s.size})
				if showProgress != nil {
					showProgress()
				}
			}
		}
		return true
	})

	if scanPending {
		// slice refs: Kccccccccnnnn
		klen = 1 + 8 + 4
		_ = m.client.scan(m.fmtKey("K"), func(k, v []byte) bool {
			if len(k) == klen && len(v) == 8 && parseCounter(v) < 0 {
				rb := utils.FromBuffer([]byte(k)[1:])
				slices[0] = append(slices[0], Slice{Id: rb.Get64(), Size: rb.Get32()})
			}
			return true

		})
	}

	if m.getFormat().TrashDays == 0 {
		return 0
	}
	return errno(m.scanTrashSlices(ctx, func(ss []Slice, _ int64) (bool, error) {
		slices[1] = append(slices[1], ss...)
		if showProgress != nil {
			for range ss {
				showProgress()
			}
		}
		return false, nil
	}))
}

func (m *kvMeta) scanTrashSlices(ctx Context, scan trashSliceScan) error {
	if scan == nil {
		return nil
	}

	// delayed slices: Lttttttttcccccccc
	klen := 1 + 8 + 8
	var ss []Slice
	var rs []int64
	return m.client.scan(m.fmtKey("L"), func(key, value []byte) bool {
		if len(key) != klen || len(value) == 0 {
			return true
		}
		var clean bool
		var err error
		err = m.txn(ctx, func(tx *kvTxn) error {
			ss, rs = ss[:0], rs[:0]
			v := tx.get(key)
			if len(v) == 0 {
				return nil
			}
			b := utils.ReadBuffer(key[1:])
			ts := b.Get64()
			m.decodeDelayedSlices(v, &ss)
			clean, err = scan(ss, int64(ts))
			if err != nil {
				return err
			}
			if clean {
				for _, s := range ss {
					rs = append(rs, tx.incrBy(m.sliceKey(s.Id, s.Size), -1))
				}
				tx.delete(key)
			}
			return nil
		})
		if err != nil {
			logger.Warnf("scan trash slices %s: %s", key, err)
			return true
		}
		if clean && len(rs) == len(ss) {
			for i, s := range ss {
				if rs[i] < 0 {
					m.deleteSlice(s.Id, s.Size)
				}
			}
		}
		return true
	})
}

func (m *kvMeta) scanPendingSlices(ctx Context, scan pendingSliceScan) error {
	if scan == nil {
		return nil
	}

	// slice refs: Kiiiiiiiissss
	klen := 1 + 8 + 4
	return m.client.scan(m.fmtKey("K"), func(key, v []byte) bool {
		refs := parseCounter(v)
		if len(key) == klen && refs < 0 {
			b := utils.ReadBuffer([]byte(key)[1:])
			id := b.Get64()
			size := b.Get32()
			clean, err := scan(id, size)
			if err != nil {
				logger.Warnf("scan pending deleted slices %d %d: %s", id, size, err)
				return true
			}
			if clean {
				// TODO: m.deleteSlice(id, size)
				// avoid lint warning
				_ = clean
			}
		}
		return true
	})
}

func (m *kvMeta) scanPendingFiles(ctx Context, scan pendingFileScan) error {
	if scan == nil {
		return nil
	}
	// deleted files: Diiiiiiiissssssss
	klen := 1 + 8 + 8

	var scanErr error
	if err := m.client.scan(m.fmtKey("D"), func(key, val []byte) bool {
		if scanErr != nil {
			return true
		}
		if len(key) != klen {
			scanErr = fmt.Errorf("invalid key %x", key)
			return true
		}
		ino := m.decodeInode(key[1:9])
		size := binary.BigEndian.Uint64(key[9:])
		ts := m.parseInt64(val)
		_, scanErr = scan(ino, size, ts)
		return true
	}); err != nil {
		return err
	}

	return scanErr
}

func (m *kvMeta) doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	prefix := m.entryKey(inode, "")
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		attr.Nlink = 2
		tx.scan(prefix, nextKey(prefix), false, func(k, v []byte) bool {
			typ, _ := m.parseEntry(v)
			if typ == TypeDirectory {
				attr.Nlink++
			}
			return true
		})
		tx.set(m.inodeKey(inode), m.marshal(attr))
		return nil
	}, inode))
}

func (m *kvMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer m.timeit("GetXattr", time.Now())
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
	defer m.timeit("ListXattr", time.Now())
	inode = m.checkRoot(inode)
	keys, err := m.scanKeys(ctx, m.xattrKey(inode, ""))
	if err != nil {
		return errno(err)
	}
	*names = nil
	prefix := len(m.xattrKey(inode, ""))
	for _, name := range keys {
		*names = append(*names, name[prefix:]...)
		*names = append(*names, 0)
	}

	val, err := m.get(m.inodeKey(inode))
	if err != nil {
		return errno(err)
	}
	if val == nil {
		return syscall.ENOENT
	}
	attr := &Attr{}
	m.parseAttr(val, attr)
	setXAttrACL(names, attr.AccessACL, attr.DefaultACL)
	return 0
}

func (m *kvMeta) doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	if len(value) == 0 && m.Name() == "tikv" {
		return syscall.EINVAL
	}
	key := m.xattrKey(inode, name)
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		v := tx.get(key)
		switch flags {
		case XattrCreate:
			if v != nil {
				return syscall.EEXIST
			}
		case XattrReplace:
			if v == nil {
				return ENOATTR
			}
		}
		if v == nil || !bytes.Equal(v, value) {
			tx.set(key, value)
		}
		return nil
	}))
}

func (m *kvMeta) doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	key := m.xattrKey(inode, name)
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		value := tx.get(key)
		if value == nil {
			return ENOATTR
		}
		tx.delete(key)
		return nil
	}))
}

func (m *kvMeta) getQuotaKey(qtype uint32, key uint64) ([]byte, error) {
	switch qtype {
	case DirQuotaType:
		return m.dirQuotaKey(Ino(key)), nil
	case UserQuotaType:
		return m.userQuotaKey(key), nil
	case GroupQuotaType:
		return m.groupQuotaKey(key), nil
	default:
		return nil, fmt.Errorf("invalid quota type: %d", qtype)
	}
}

func (m *kvMeta) doGetQuota(ctx Context, qtype uint32, key uint64) (*Quota, error) {
	quotaKey, err := m.getQuotaKey(qtype, key)
	if err != nil {
		return nil, err
	}

	buf, err := m.get(quotaKey)
	if err != nil {
		return nil, err
	}
	if buf == nil {
		return nil, nil
	}
	if len(buf) != 32 {
		return nil, fmt.Errorf("invalid quota value: %v", buf)
	}

	return m.parseQuota(buf), nil
}

func (m *kvMeta) doSetQuota(ctx Context, qtype uint32, key uint64, quota *Quota) (bool, error) {
	quotaKey, err := m.getQuotaKey(qtype, key)
	if err != nil {
		return false, err
	}

	var created bool
	err = m.txn(ctx, func(tx *kvTxn) error {
		buf := tx.get(quotaKey)
		var origin *Quota
		var exists bool
		if len(buf) == 32 {
			origin = m.parseQuota(buf)
			exists = true
		} else if len(buf) != 0 {
			return fmt.Errorf("invalid quota value: %v", buf)
		}

		if !exists {
			created = true
			origin = new(Quota)
			origin.MaxInodes, origin.MaxSpace = -1, -1
		} else {
			created = false
		}

		if quota.MaxSpace >= 0 {
			origin.MaxSpace = quota.MaxSpace
		}
		if quota.MaxInodes >= 0 {
			origin.MaxInodes = quota.MaxInodes
		}
		if quota.UsedSpace >= 0 {
			origin.UsedSpace = quota.UsedSpace
		}
		if quota.UsedInodes >= 0 {
			origin.UsedInodes = quota.UsedInodes
		}
		tx.set(quotaKey, m.packQuota(origin))
		return nil
	})
	return created, err
}

func (m *kvMeta) doDelQuota(ctx Context, qtype uint32, key uint64) error {
	quotaKey, err := m.getQuotaKey(qtype, key)
	if err != nil {
		return err
	}

	if qtype == UserQuotaType || qtype == GroupQuotaType {
		quota := &Quota{}
		val, err := m.get(quotaKey)
		if err != nil {
			return err
		}
		if len(val) > 0 {
			quota = m.parseQuota(val)
		}
		quota.MaxSpace = -1
		quota.MaxInodes = -1
		return m.txn(ctx, func(tx *kvTxn) error {
			tx.set(quotaKey, m.packQuota(quota))
			return nil
		})
	} else {
		// For dir quotas, remove all data
		return m.deleteKeys(quotaKey)
	}
}

func (m *kvMeta) doLoadQuotas(ctx Context) (map[uint64]*Quota, map[uint64]*Quota, map[uint64]*Quota, error) {
	quotaTypes := []struct {
		prefix string
		name   string
	}{
		{"QD", "dir"},
		{"QU", "user"},
		{"QG", "group"},
	}

	quotaMaps := make([]map[uint64]*Quota, 3)
	for i, qt := range quotaTypes {
		pairs, err := m.scanValues(ctx, m.fmtKey(qt.prefix), -1, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to load %s quotas: %w", qt.name, err)
		}
		var quotas map[uint64]*Quota
		if len(pairs) == 0 {
			quotas = make(map[uint64]*Quota)
		} else {
			quotas = make(map[uint64]*Quota, len(pairs))
			for k, v := range pairs {
				var id uint64
				if qt.prefix == "QD" {
					id = uint64(m.decodeInode([]byte(k[2:]))) // skip prefix
				} else {
					id = binary.BigEndian.Uint64([]byte(k[2:])) // skip prefix
				}
				quota := m.parseQuota(v)
				quotas[id] = quota
			}
		}
		quotaMaps[i] = quotas
	}

	return quotaMaps[0], quotaMaps[1], quotaMaps[2], nil
}

func (m *kvMeta) doSyncVolumeStat(ctx Context) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	var used, inodes int64
	if err := m.client.txn(ctx, func(tx *kvTxn) error {
		prefix := m.fmtKey("U")
		tx.scan(prefix, nextKey(prefix), false, func(k, v []byte) bool {
			stat := m.parseDirStat(v)
			used += stat.space
			inodes += stat.inodes
			return true
		})
		return nil
	}, 0); err != nil {
		return err
	}
	// need add sustained file size
	vals, err := m.scanKeys(ctx, m.fmtKey("SS"))
	if err != nil {
		return err
	}
	var attr Attr
	for _, k := range vals {
		b := utils.FromBuffer(k[2:])
		if b.Len() != 16 {
			logger.Warnf("Invalid sustainedKey: %v", k)
			continue
		}
		_ = b.Get64()
		inode := m.decodeInode(b.Get(8))
		if eno := m.doGetAttr(ctx, inode, &attr); eno != 0 {
			logger.Warnf("Get attr of inode %d: %s", inode, eno)
			continue
		}
		used += align4K(attr.Length)
		inodes += 1
	}

	if err := m.scanTrashEntry(ctx, func(_ Ino, length uint64) {
		used += align4K(length)
		inodes += 1
	}); err != nil {
		return err
	}
	logger.Debugf("Used space: %s, inodes: %d", humanize.IBytes(uint64(used)), inodes)
	err = m.setValue(m.counterKey(totalInodes), packCounter(inodes))
	if err != nil {
		return fmt.Errorf("set total inodes: %w", err)
	}
	return m.setValue(m.counterKey(usedSpace), packCounter(used))
}

func (m *kvMeta) doFlushQuotas(ctx Context, quotas []*iQuota) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		keys := make([][]byte, 0, len(quotas))
		qs := make([]*Quota, 0, len(quotas))
		for _, q := range quotas {
			key, err := m.getQuotaKey(q.qtype, q.qkey)
			if err != nil {
				return err
			}
			keys = append(keys, key)
			qs = append(qs, q.quota)
		}
		for i, v := range tx.gets(keys...) {
			if len(v) == 0 {
				continue
			}
			if len(v) != 32 {
				logger.Errorf("Invalid quota value: %v", v)
				continue
			}
			q := m.parseQuota(v)
			q.UsedSpace += qs[i].newSpace
			q.UsedInodes += qs[i].newInodes
			tx.set(keys[i], m.packQuota(q))
		}
		return nil
	})
}

func (m *kvMeta) dumpEntry(inode Ino, e *DumpedEntry, showProgress func(totalIncr, currentIncr int64)) error {
	ctx := Background()
	return m.client.txn(ctx, func(tx *kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			logger.Warnf("inode %d not found", inode)
		}

		attr := &Attr{Nlink: 1}
		m.parseAttr(a, attr)
		if a == nil && e.Attr != nil {
			attr.Typ = typeFromString(e.Attr.Type)
			if attr.Typ == TypeDirectory {
				attr.Nlink = 2
			}
		}
		dumpAttr(attr, e.Attr)
		e.Attr.Inode = inode

		var xattrs []*DumpedXattr
		tx.scan(m.xattrKey(inode, ""), nextKey(m.xattrKey(inode, "")), false, func(k, v []byte) bool {
			xattrs = append(xattrs, &DumpedXattr{string(k[10:]), string(v)}) // "A" + inode + "X"
			return true
		})
		if len(xattrs) > 0 {
			sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
			e.Xattrs = xattrs
		}

		accessACl, err := m.getACL(tx, attr.AccessACL)
		if err != nil {
			return err
		}
		e.AccessACL = dumpACL(accessACl)
		defaultACL, err := m.getACL(tx, attr.DefaultACL)
		if err != nil {
			return err
		}
		e.DefaultACL = dumpACL(defaultACL)

		if attr.Typ == TypeFile {
			e.Chunks = e.Chunks[:0]
			vals := make(map[string][]byte)
			tx.scan(m.chunkKey(inode, 0), m.chunkKey(inode, uint32(attr.Length/ChunkSize)+1),
				false, func(k, v []byte) bool {
					vals[string(k)] = v
					return true
				})
			for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
				v, ok := vals[string(m.chunkKey(inode, indx))]
				if !ok {
					continue
				}
				ss := readSliceBuf(v)
				if ss == nil {
					logger.Errorf("Corrupt value for inode %d chunk index %d", inode, indx)
				}
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
		} else if attr.Typ == TypeDirectory {
			vals, err := m.scanValues(ctx, m.entryKey(inode, ""), 10000, nil)
			if err != nil {
				return err
			}
			if showProgress != nil {
				showProgress(int64(len(e.Entries)), 0)
			}
			if len(vals) < 10000 {
				e.Entries = make(map[string]*DumpedEntry, len(vals))
				for k, value := range vals {
					name := k[10:]
					ce := entryPool.Get()
					ce.Name = name
					typ, inode := m.parseEntry(value)
					ce.Attr.Inode = inode
					ce.Attr.Type = typeToString(typ)
					e.Entries[name] = ce
				}
			}
		}
		return nil
	}, 0)
}

func (m *kvMeta) dumpDir(ctx Context, inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth, threads int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	if tree.Entries == nil {
		// retry for large directory
		vals, err := m.scanValues(ctx, m.entryKey(inode, ""), -1, nil)
		if err != nil {
			return err
		}
		tree.Entries = make(map[string]*DumpedEntry, len(vals))
		for k, value := range vals {
			name := k[10:]
			ce := entryPool.Get()
			ce.Name = name
			typ, inode := m.parseEntry(value)
			ce.Attr.Inode = inode
			ce.Attr.Type = typeToString(typ)
			tree.Entries[name] = ce
		}
		if showProgress != nil {
			showProgress(int64(len(tree.Entries))-10000, 0)
		}
	}
	var entries []*DumpedEntry
	for _, e := range tree.Entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	_ = tree.writeJsonWithOutEntry(bw, depth)

	ms := make([]sync.Mutex, threads)
	conds := make([]*sync.Cond, threads)
	ready := make([]bool, threads)
	var err error
	for c := 0; c < threads; c++ {
		conds[c] = sync.NewCond(&ms[c])
		if c < len(entries) {
			go func(c int) {
				for i := c; i < len(entries) && err == nil; i += threads {
					e := entries[i]
					er := m.dumpEntry(e.Attr.Inode, e, showProgress)
					ms[c].Lock()
					ready[c] = true
					if er != nil {
						err = er
					}
					conds[c].Signal()
					for ready[c] && err == nil {
						conds[c].Wait()
					}
					ms[c].Unlock()
				}
			}(c)
		}
	}

	for i, e := range entries {
		c := i % threads
		ms[c].Lock()
		for !ready[c] && err == nil {
			conds[c].Wait()
		}
		ready[c] = false
		conds[c].Signal()
		ms[c].Unlock()
		if err != nil {
			return err
		}
		if e.Attr.Type == "directory" {
			err = m.dumpDir(ctx, e.Attr.Inode, e, bw, depth+2, threads, showProgress)
		} else {
			err = e.writeJSON(bw, depth+2)
		}
		if err != nil {
			return err
		}
		entries[i] = nil
		entryPool.Put(e)
		if i != len(entries)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

func (m *kvMeta) dumpDirFast(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	var names []string
	entries := tree.Entries
	for n, de := range entries {
		if !de.Attr.full && de.Attr.Inode != TrashInode {
			logger.Warnf("Corrupt inode: %d, missing attribute", inode)
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	_ = tree.writeJsonWithOutEntry(bw, depth)
	for i, name := range names {
		e := entries[name]
		e.Name = name
		inode := e.Attr.Inode
		if e.Attr.Type == "directory" {
			_ = m.dumpDirFast(inode, e, bw, depth+2, showProgress)
		} else {
			_ = e.writeJSON(bw, depth+2)
		}
		if i != len(entries)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

func (m *kvMeta) DumpMeta(w io.Writer, root Ino, threads int, keepSecret, fast, skipTrash bool) (err error) {
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
	ctx := Background()
	vals, err := m.scanValues(ctx, m.fmtKey("D"), -1, nil)
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

	progress := utils.NewProgress(false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)

	bInodes, _ := m.get(m.counterKey(totalInodes))
	inodeTotal := parseCounter(bInodes)
	if root == 1 && fast { // make snap
		m.snap = make(map[Ino]*DumpedEntry)
		defer func() {
			m.snap = nil
		}()
		bar := progress.AddCountBar("Scan keys", 0)
		bUsed, _ := m.get(m.counterKey(usedSpace))
		used := parseCounter(bUsed)
		var guessKeyTotal int64 = 3 // setting, nextInode, nextChunk
		if inodeTotal > 0 {
			guessKeyTotal += int64(math.Ceil((float64(used/inodeTotal/(64*1024*1024)) + float64(3)) * float64(inodeTotal)))
		}
		bar.SetCurrent(0) // Reset
		bar.SetTotal(guessKeyTotal)
		threshold := 0.1
		var cnt int

		if err = m.cacheACLs(Background()); err != nil {
			return err
		}

		err := m.client.scan(nil, func(key, value []byte) bool {
			if len(key) > 9 && key[0] == 'A' {
				ino := m.decodeInode(key[1:9])
				e := m.snap[ino]
				if e == nil {
					e = &DumpedEntry{Attr: &DumpedAttr{Inode: ino}}
					m.snap[ino] = e
				}
				switch key[9] {
				case 'I':
					attr := &Attr{Nlink: 1}
					m.parseAttr(value, attr)
					dumpAttr(attr, e.Attr)
					e.Attr.Inode = ino
					e.AccessACL = dumpACL(m.aclCache.Get(attr.AccessACL))
					e.DefaultACL = dumpACL(m.aclCache.Get(attr.DefaultACL))
				case 'C':
					indx := binary.BigEndian.Uint32(key[10:])
					ss := readSliceBuf(value)
					if ss == nil {
						logger.Errorf("Corrupt value for inode %d chunk index %d", ino, indx)
					}
					slices := make([]*DumpedSlice, 0, len(ss))
					for _, s := range ss {
						slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
					}
					e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
				case 'D':
					name := string(key[10:])
					typ, inode := m.parseEntry(value)
					child := m.snap[inode]
					if child == nil {
						child = &DumpedEntry{Attr: &DumpedAttr{Inode: inode, Type: typeToString(typ)}}
						m.snap[inode] = child
					} else if child.Attr.Type == "" {
						child.Attr.Type = typeToString(typ)
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
			return true
		})
		if err != nil {
			return err
		}
		bar.Done()
		tree = m.snap[root]
		if !skipTrash {
			trash = m.snap[TrashInode]
			if trash == nil {
				trash = &DumpedEntry{
					Attr: &DumpedAttr{
						Inode: TrashInode,
						Type:  "directory",
						Nlink: 2,
					},
				}
				m.snap[TrashInode] = trash
			}
		}
	} else {
		tree = &DumpedEntry{
			Attr: &DumpedAttr{
				Inode: root,
				Type:  "directory",
			},
		}
		if err = m.dumpEntry(root, tree, nil); err != nil {
			return err
		}
		if root == 1 && !skipTrash {
			trash = &DumpedEntry{
				Attr: &DumpedAttr{
					Inode: TrashInode,
					Type:  "directory",
				},
			}
			if err = m.dumpEntry(TrashInode, trash, nil); err != nil {
				return err
			}
		}
	}

	if tree == nil || tree.Attr == nil {
		return errors.New("The entry of the root inode was not found")
	}
	tree.Name = "FSTree"

	var rs [][]byte
	err = m.txn(Background(), func(tx *kvTxn) error {
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

	vals, err = m.scanValues(ctx, m.fmtKey("SS"), -1, nil)
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

	pairs, err := m.scanValues(ctx, m.fmtKey("QD"), -1, func(k, v []byte) bool {
		return len(k) == 10 && len(v) == 32
	})
	if err != nil {
		return err
	}
	quotas := make(map[Ino]*DumpedQuota, len(pairs))
	for k, v := range pairs {
		inode := m.decodeInode([]byte(k[2:]))
		quota := m.parseQuota(v)
		quotas[inode] = &DumpedQuota{quota.MaxSpace, quota.MaxInodes, 0, 0}
	}

	dm := DumpedMeta{
		Setting: *m.getFormat(),
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
		Quotas:    quotas,
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
	useTotal := root == RootInode && !skipTrash
	bar := progress.AddCountBar("Dumped entries", 1) // with root
	if useTotal {
		bar.SetTotal(inodeTotal)
	}
	bar.Increment()
	if trash != nil {
		trash.Name = "Trash"
		bar.IncrTotal(1)
		bar.Increment()
	}
	showProgress := func(totalIncr, currentIncr int64) {
		if !useTotal {
			bar.IncrTotal(totalIncr)
		}
		bar.IncrInt64(currentIncr)
	}
	if m.snap != nil {
		if err = m.dumpDirFast(root, tree, bw, 1, showProgress); err != nil {
			return err
		}
	} else {
		showProgress(int64(len(tree.Entries)), 0)
		if err = m.dumpDir(ctx, root, tree, bw, 1, threads, showProgress); err != nil {
			return err
		}
	}
	if trash != nil {
		if _, err = bw.WriteString(","); err != nil {
			return err
		}
		if m.snap != nil {
			if err = m.dumpDirFast(TrashInode, trash, bw, 1, showProgress); err != nil {
				return err
			}
		} else {
			showProgress(int64(len(tree.Entries)), 0)
			if err = m.dumpDir(ctx, TrashInode, trash, bw, 1, threads, showProgress); err != nil {
				return err
			}
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

func (m *kvMeta) loadEntry(e *DumpedEntry, kv chan *pair, aclMaxId *uint32) {
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
		var stat dirStat
		for name, c := range e.Entries {
			length := uint64(0)
			if typeFromString(c.Attr.Type) == TypeFile {
				length = c.Attr.Length
			}
			stat.length += int64(length)
			stat.space += align4K(length)
			stat.inodes++

			kv <- &pair{m.entryKey(inode, string(unescape(name))), m.packEntry(typeFromString(c.Attr.Type), c.Attr.Inode)}
		}
		kv <- &pair{m.dirStatKey(inode), m.packDirStat(&stat)}
	} else if attr.Typ == TypeSymlink {
		symL := unescape(e.Symlink)
		attr.Length = uint64(len(symL))
		kv <- &pair{m.symKey(inode), []byte(symL)}
	}
	for _, x := range e.Xattrs {
		kv <- &pair{m.xattrKey(inode, x.Name), []byte(unescape(x.Value))}
	}

	attr.AccessACL = m.saveACL(loadACL(e.AccessACL), aclMaxId)
	attr.DefaultACL = m.saveACL(loadACL(e.DefaultACL), aclMaxId)
	kv <- &pair{m.inodeKey(inode), m.marshal(attr)}
}

func (m *kvMeta) LoadMeta(r io.Reader) error {
	var exist bool
	err := m.txn(Background(), func(tx *kvTxn) error {
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
			var total int
			for p := range kv {
				buffer = append(buffer, p)
				total += len(p.key) + len(p.value)
				if len(buffer) >= batch || total > 5<<20 {
					err := m.txn(Background(), func(tx *kvTxn) error {
						for _, p := range buffer {
							tx.set(p.key, p.value)
						}
						return nil
					})
					if err != nil {
						logger.Fatalf("write %d pairs: %s", len(buffer), err)
					}
					buffer = buffer[:0]
					total = 0
				}
			}
			if len(buffer) > 0 {
				err := m.txn(Background(), func(tx *kvTxn) error {
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

	var aclMaxId uint32
	dm, counters, parents, refs, err := loadEntries(r, func(e *DumpedEntry) { m.loadEntry(e, kv, &aclMaxId) }, nil)
	if err != nil {
		return err
	}

	if err = m.loadDumpedACLs(Background()); err != nil {
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
	defer m.loadDumpedQuotas(Background(), dm.Quotas)
	return m.txn(Background(), func(tx *kvTxn) error {
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

func (m *kvMeta) doCloneEntry(ctx Context, srcIno Ino, parent Ino, name string, ino Ino, originAttr *Attr, cmode uint8, cumask uint16, top bool) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		a := tx.get(m.inodeKey(srcIno))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, originAttr)
		attr := *originAttr
		if eno := m.Access(ctx, srcIno, MODE_MASK_R, &attr); eno != 0 {
			return eno
		}
		attr.Parent = parent
		now := time.Now()
		if cmode&CLONE_MODE_PRESERVE_ATTR == 0 {
			attr.Uid = ctx.Uid()
			attr.Gid = ctx.Gid()
			attr.Mode &= ^cumask
			attr.Atime = now.Unix()
			attr.Mtime = now.Unix()
			attr.Ctime = now.Unix()
			attr.Atimensec = uint32(now.Nanosecond())
			attr.Mtimensec = uint32(now.Nanosecond())
			attr.Ctimensec = uint32(now.Nanosecond())
		}
		// TODO: preserve hardlink
		if attr.Typ == TypeFile && attr.Nlink > 1 {
			attr.Nlink = 1
		}

		if top {
			var pattr Attr
			a = tx.get(m.inodeKey(parent))
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
			if tx.get(m.entryKey(parent, name)) != nil {
				return syscall.EEXIST
			}
			if eno := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); eno != 0 {
				return eno
			}
			if attr.Typ != TypeDirectory {
				now := time.Now()
				pattr.Mtime = now.Unix()
				pattr.Mtimensec = uint32(now.Nanosecond())
				pattr.Ctime = now.Unix()
				pattr.Ctimensec = uint32(now.Nanosecond())
				tx.set(m.inodeKey(parent), m.marshal(&pattr))
			}
		}

		tx.set(m.inodeKey(ino), m.marshal(&attr))
		prefix := m.xattrKey(srcIno, "")
		tx.scan(prefix, nextKey(prefix), false, func(k, v []byte) bool {
			tx.set(m.xattrKey(ino, string(k[len(prefix):])), v)
			return true
		})
		if top && attr.Typ == TypeDirectory {
			tx.set(m.detachedKey(ino), m.packInt64(time.Now().Unix()))
		} else {
			tx.set(m.entryKey(parent, name), m.packEntry(attr.Typ, ino))
		}

		switch attr.Typ {
		case TypeDirectory:
			tx.set(m.dirStatKey(ino), tx.get(m.dirStatKey(srcIno)))
		case TypeFile:
			if attr.Length != 0 {
				vals := make(map[string][]byte)
				tx.scan(m.chunkKey(srcIno, 0), m.chunkKey(srcIno, uint32(attr.Length/ChunkSize)+1),
					false, func(k, v []byte) bool {
						vals[string(k)] = v
						return true
					})

				refKeys := make([][]byte, 0, len(vals))
				for indx := uint32(0); indx <= uint32(attr.Length/ChunkSize); indx++ {
					if v, ok := vals[string(m.chunkKey(srcIno, indx))]; ok {
						tx.set(m.chunkKey(ino, indx), v)
						ss := readSliceBuf(v)
						for _, s := range ss {
							if s.id > 0 {
								refKeys = append(refKeys, m.sliceKey(s.id, s.size))
							}
						}
					}
				}
				refs := tx.gets(refKeys...)
				for i := range refKeys {
					tx.set(refKeys[i], packCounter(parseCounter(refs[i])+1))
				}
			}
		case TypeSymlink:
			tx.set(m.symKey(ino), tx.get(m.symKey(srcIno)))
		}
		return nil
	}, srcIno))
}

func (m *kvMeta) doFindDetachedNodes(t time.Time) []Ino {
	vals, err := m.scanValues(Background(), m.fmtKey("N"), -1, func(k, v []byte) bool {
		return len(k) == 9 && m.parseInt64(v) < t.Unix()
	})
	if err != nil {
		logger.Errorf("Scan detached nodes error: %s", err)
		return nil
	}
	var inodes []Ino
	for k := range vals {
		inodes = append(inodes, m.decodeInode([]byte(k)[1:]))
	}
	return inodes
}

func (m *kvMeta) doCleanupDetachedNode(ctx Context, ino Ino) syscall.Errno {
	buf, err := m.get(m.inodeKey(ino))
	if err != nil || buf == nil {
		return errno(err)
	}
	rmConcurrent := make(chan int, 10)
	if eno := m.emptyDir(ctx, ino, true, nil, rmConcurrent); eno != 0 {
		return eno
	}
	m.updateStats(-align4K(0), -1)
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		tx.delete(m.inodeKey(ino))
		tx.deleteKeys(m.xattrKey(ino, ""))
		tx.delete(m.dirStatKey(ino))
		tx.delete(m.detachedKey(ino))
		return nil
	}, ino))
}

func (m *kvMeta) doAttachDirNode(ctx Context, parent Ino, inode Ino, name string) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		a := tx.get(m.inodeKey(parent))
		if a == nil {
			return syscall.ENOENT
		}
		var pattr Attr
		m.parseAttr(a, &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if pattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if (pattr.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if tx.get(m.entryKey(parent, name)) != nil {
			return syscall.EEXIST
		}

		pattr.Nlink++
		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(parent), m.marshal(&pattr))
		tx.set(m.entryKey(parent, name), m.packEntry(TypeDirectory, inode))
		tx.delete(m.detachedKey(inode))
		return nil
	}, parent))
}

func (m *kvMeta) doTouchAtime(ctx Context, inode Ino, attr *Attr, now time.Time) (bool, error) {
	var updated bool
	err := m.txn(ctx, func(tx *kvTxn) error {
		a := tx.get(m.inodeKey(inode))
		if a == nil {
			return syscall.ENOENT
		}
		m.parseAttr(a, attr)
		if !m.atimeNeedsUpdate(attr, now) {
			return nil
		}
		attr.Atime = now.Unix()
		attr.Atimensec = uint32(now.Nanosecond())
		tx.set(m.inodeKey(inode), m.marshal(attr))
		updated = true
		return nil
	}, inode)
	return updated, err
}

func (m *kvMeta) doSetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno {
	return errno(m.txn(ctx, func(tx *kvTxn) error {
		val := tx.get(m.inodeKey(ino))
		if val == nil {
			return syscall.ENOENT
		}
		attr := &Attr{}
		m.parseAttr(val, attr)

		if ctx.Uid() != 0 && ctx.Uid() != attr.Uid {
			return syscall.EPERM
		}

		if attr.Flags&FlagImmutable != 0 {
			return syscall.EPERM
		}

		oriACL, oriMode := getAttrACLId(attr, aclType), attr.Mode

		// https://github.com/torvalds/linux/blob/480e035fc4c714fb5536e64ab9db04fedc89e910/fs/fuse/acl.c#L143-L151
		// TODO: check linux capabilities
		if ctx.Uid() != 0 && !inGroup(ctx, attr.Gid) {
			// clear sgid
			attr.Mode &= 05777
		}

		if rule.IsEmpty() {
			// remove acl
			setAttrACLId(attr, aclType, aclAPI.None)
		} else if rule.IsMinimal() && aclType == aclAPI.TypeAccess {
			// remove acl
			setAttrACLId(attr, aclType, aclAPI.None)
			// set mode
			attr.Mode &= 07000
			attr.Mode |= ((rule.Owner & 7) << 6) | ((rule.Group & 7) << 3) | (rule.Other & 7)
		} else {
			// set acl
			rule.InheritPerms(attr.Mode)
			aclId, err := m.insertACL(tx, rule)
			if err != nil {
				return err
			}
			setAttrACLId(attr, aclType, aclId)

			// set mode
			if aclType == aclAPI.TypeAccess {
				attr.Mode &= 07000
				attr.Mode |= ((rule.Owner & 7) << 6) | ((rule.Mask & 7) << 3) | (rule.Other & 7)
			}
		}

		// update attr
		if oriACL != getAttrACLId(attr, aclType) || oriMode != attr.Mode {
			now := time.Now()
			attr.Ctime = now.Unix()
			attr.Ctimensec = uint32(now.Nanosecond())
			tx.set(m.inodeKey(ino), m.marshal(attr))
		}
		return nil
	}, ino))
}

func (m *kvMeta) doGetFacl(ctx Context, ino Ino, aclType uint8, aclId uint32, rule *aclAPI.Rule) syscall.Errno {
	return errno(m.client.txn(ctx, func(tx *kvTxn) error {
		if aclId == aclAPI.None {
			val := tx.get(m.inodeKey(ino))
			if val == nil {
				return syscall.ENOENT
			}
			attr := &Attr{}
			m.parseAttr(val, attr)
			m.of.Update(ino, attr)

			aclId = getAttrACLId(attr, aclType)
		}

		a, err := m.getACL(tx, aclId)
		if err != nil {
			return err
		}
		if a == nil {
			return ENOATTR
		}
		*rule = *a
		return nil
	}, 0))
}

func (m *kvMeta) insertACL(tx *kvTxn, rule *aclAPI.Rule) (uint32, error) {
	if rule == nil || rule.IsEmpty() {
		return aclAPI.None, nil
	}

	if err := m.tryLoadMissACLs(tx); err != nil {
		logger.Warnf("load miss acls error: %s", err)
	}

	var aclId uint32
	if aclId = m.aclCache.GetId(rule); aclId == aclAPI.None {
		newId, err := m.incrCounter(aclCounter, 1)
		if err != nil {
			return aclAPI.None, err
		}
		aclId = uint32(newId)

		tx.set(m.aclKey(aclId), rule.Encode())
		m.aclCache.Put(aclId, rule)
	}
	return aclId, nil
}

func (m *kvMeta) tryLoadMissACLs(tx *kvTxn) error {
	missIds := m.aclCache.GetMissIds()
	if len(missIds) > 0 {
		missKeys := make([][]byte, len(missIds))
		for i, id := range missIds {
			missKeys[i] = m.aclKey(id)
		}

		acls := tx.gets(missKeys...)
		for i, data := range acls {
			var rule aclAPI.Rule
			if len(data) > 0 {
				rule.Decode(data)
			}
			m.aclCache.Put(missIds[i], &rule)
		}
	}
	return nil
}

func (m *kvMeta) getACL(tx *kvTxn, id uint32) (*aclAPI.Rule, error) {
	if id == aclAPI.None {
		return nil, nil
	}
	if cRule := m.aclCache.Get(id); cRule != nil {
		return cRule, nil
	}

	val := tx.get(m.aclKey(id))
	if val == nil {
		return nil, syscall.EIO
	}

	rule := &aclAPI.Rule{}
	rule.Decode(val)
	m.aclCache.Put(id, rule)
	return rule, nil
}

func (m *kvMeta) loadDumpedACLs(ctx Context) error {
	id2Rule := m.aclCache.GetAll()
	if len(id2Rule) == 0 {
		return nil
	}

	return m.txn(ctx, func(tx *kvTxn) error {
		maxId := uint32(0)
		for id, rule := range id2Rule {
			if id > maxId {
				maxId = id
			}
			tx.set(m.aclKey(id), rule.Encode())
		}
		tx.set(m.counterKey(aclCounter), packCounter(int64(maxId)))
		return nil
	})
}

type kvDirHandler struct {
	dirHandler
}

func (m *kvMeta) newDirHandler(inode Ino, plus bool, entries []*Entry) DirHandler {
	s := &kvDirHandler{
		dirHandler: dirHandler{
			inode:       inode,
			plus:        plus,
			initEntries: entries,
			fetcher:     m.getDirFetcher(),
			batchNum:    DirBatchNum["kv"],
		},
	}
	s.batch, _ = s.fetch(Background(), 0)
	return s
}

func (m *kvMeta) getDirFetcher() dirFetcher {
	return func(ctx Context, inode Ino, cursor interface{}, offset, limit int, plus bool) (interface{}, []*Entry, error) {
		var startKey []byte
		sCursor := ""
		var total int
		if cursor == nil {
			if offset > 0 {
				total += offset
			}
		} else {
			limit += 1 // skip the cursor
			sCursor = string(cursor.([]byte))
		}
		total += limit
		startKey = m.entryKey(inode, sCursor)
		endKey := nextKey(m.entryKey(inode, ""))

		keys, vals, err := m.scan(startKey, endKey, total, nil)
		if err != nil {
			return nil, nil, err
		}

		if cursor != nil {
			keys, vals = keys[1:], vals[1:]
		}

		if total > limit && offset <= len(keys) {
			keys, vals = keys[offset:], vals[offset:]
		}

		prefix := len(m.entryKey(inode, ""))
		entries := make([]*Entry, 0, len(keys))
		var name []byte
		var typ uint8
		var ino Ino
		for i, buf := range vals {
			name = keys[i]
			typ, ino = m.parseEntry(buf)
			if len(name) == prefix {
				logger.Errorf("Corrupt entry with empty name: inode %d parent %d", ino, inode)
				continue
			}
			entries = append(entries, &Entry{
				Inode: ino,
				Name:  []byte(name)[prefix:],
				Attr:  &Attr{Typ: typ},
			})
		}

		if plus {
			if err = m.fillAttr(entries); err != nil {
				return nil, nil, err
			}
		}

		if len(entries) == 0 {
			return nil, nil, nil
		}
		return entries[len(entries)-1].Name, entries, nil
	}
}
