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

package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/juicedata/juicefs/pkg/utils"
)

/*
	Node: i$inode -> Attribute{type,mode,uid,gid,atime,mtime,ctime,nlink,length,rdev}
	Dir:   d$inode -> {name -> {inode,type}}
	File:  c$inode_$indx -> [Slice{pos,id,length,off,len}]
	Symlink: s$inode -> target
	Xattr: x$inode -> {name -> value}
	Flock: lockf$inode -> { $sid_$owner -> ltype }
	POSIX lock: lockp$inode -> { $sid_$owner -> Plock(pid,ltype,start,end) }
	Sessions: sessions -> [ $sid -> heartbeat ]
	sustained: session$sid -> [$inode]
	locked: locked$sid -> { lockf$inode or lockp$inode }

	Removed files: delfiles -> [$inode:$length -> seconds]
	Slices refs: k$chunkid_$size -> refcount

	Redis features:
	  Sorted Set: 1.2+
	  Hash Set: 2.0+
	  Transaction: 2.2+
	  Scripting: 2.6+
	  Scan: 2.8+
*/

var logger = utils.GetLogger("juicefs")

const usedSpace = "usedSpace"
const totalInodes = "totalInodes"
const delfiles = "delfiles"
const allSessions = "sessions"
const sessionInfos = "sessionInfos"
const sliceRefs = "sliceRef"

type redisMeta struct {
	sync.Mutex
	conf    *Config
	fmt     Format
	rdb     *redis.Client
	txlocks [1024]sync.Mutex // Pessimistic locks to reduce conflict on Redis

	sid          int64
	usedSpace    uint64
	usedInodes   uint64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks

	shaLookup  string // The SHA returned by Redis for the loaded `scriptLookup`
	shaResolve string // The SHA returned by Redis for the loaded `scriptResolve`
}

var _ Meta = &redisMeta{}

type msgCallbacks struct {
	sync.Mutex
	callbacks map[uint32]MsgCallback
}

// newRedisMeta return a meta store using Redis.
func newRedisMeta(url string, conf *Config) (Meta, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", url, err)
	}
	var rdb *redis.Client
	if strings.Contains(opt.Addr, ",") {
		var fopt redis.FailoverOptions
		ps := strings.Split(opt.Addr, ",")
		fopt.MasterName = ps[0]
		fopt.SentinelAddrs = ps[1:]
		_, port, _ := net.SplitHostPort(fopt.SentinelAddrs[len(fopt.SentinelAddrs)-1])
		if port != "" {
			for i := range fopt.SentinelAddrs {
				h, p, _ := net.SplitHostPort(fopt.SentinelAddrs[i])
				if p == "" {
					fopt.SentinelAddrs[i] = net.JoinHostPort(h, port)
				}
			}
		}
		// Assume Redis server and sentinel have the same password.
		fopt.SentinelPassword = opt.Password
		fopt.Username = opt.Username
		fopt.Password = opt.Password
		if fopt.SentinelPassword == "" && os.Getenv("SENTINEL_PASSWORD") != "" {
			fopt.SentinelPassword = os.Getenv("SENTINEL_PASSWORD")
		}
		if fopt.Password == "" && os.Getenv("REDIS_PASSWORD") != "" {
			fopt.Password = os.Getenv("REDIS_PASSWORD")
		}
		fopt.DB = opt.DB
		fopt.TLSConfig = opt.TLSConfig
		fopt.MaxRetries = conf.Retries
		fopt.MinRetryBackoff = time.Millisecond * 100
		fopt.MaxRetryBackoff = time.Minute * 1
		fopt.ReadTimeout = time.Second * 30
		fopt.WriteTimeout = time.Second * 5
		rdb = redis.NewFailoverClient(&fopt)
	} else {
		if opt.Password == "" && os.Getenv("REDIS_PASSWORD") != "" {
			opt.Password = os.Getenv("REDIS_PASSWORD")
		}
		opt.MaxRetries = conf.Retries
		opt.MinRetryBackoff = time.Millisecond * 100
		opt.MaxRetryBackoff = time.Minute * 1
		opt.ReadTimeout = time.Second * 30
		opt.WriteTimeout = time.Second * 5
		rdb = redis.NewClient(opt)
	}

	m := &redisMeta{
		conf:         conf,
		rdb:          rdb,
		openFiles:    make(map[Ino]int),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		deleting:     make(chan int, 2),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}

	m.checkServerConfig()
	return m, nil
}

func (r *redisMeta) Init(format Format, force bool) error {
	body, err := r.rdb.Get(Background, "setting").Bytes()
	if err != nil && err != redis.Nil {
		return err
	}
	if err == nil {
		var old Format
		err = json.Unmarshal(body, &old)
		if err != nil {
			logger.Fatalf("existing format is broken: %s", err)
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
	err = r.rdb.Set(Background, "setting", data, 0).Err()
	if err != nil {
		return err
	}
	r.fmt = format
	if body != nil {
		return nil
	}

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
	return r.rdb.Set(Background, r.inodeKey(1), r.marshal(&attr), 0).Err()
}

func (r *redisMeta) Load() (*Format, error) {
	body, err := r.rdb.Get(Background, "setting").Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("no volume found")
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &r.fmt)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &r.fmt, nil
}

func (r *redisMeta) NewSession() error {
	var err error
	r.sid, err = r.rdb.Incr(Background, "nextsession").Result()
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	logger.Debugf("session is %d", r.sid)
	info, err := newSessionInfo()
	if err != nil {
		return fmt.Errorf("new session info: %s", err)
	}
	info.MountPoint = r.conf.MountPoint
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	r.rdb.HSet(Background, sessionInfos, r.sid, data)

	r.shaLookup, err = r.rdb.ScriptLoad(Background, scriptLookup).Result()
	if err != nil {
		logger.Warnf("load scriptLookup: %v", err)
		r.shaLookup = ""
	}
	r.shaResolve, err = r.rdb.ScriptLoad(Background, scriptResolve).Result()
	if err != nil {
		logger.Warnf("load scriptResolve: %v", err)
		r.shaResolve = ""
	}

	go r.refreshUsage()
	go r.refreshSession()
	go r.cleanupDeletedFiles()
	go r.cleanupSlices()
	return nil
}

func (r *redisMeta) refreshUsage() {
	for {
		used, _ := r.rdb.IncrBy(Background, usedSpace, 0).Result()
		atomic.StoreUint64(&r.usedSpace, uint64(used))
		inodes, _ := r.rdb.IncrBy(Background, totalInodes, 0).Result()
		atomic.StoreUint64(&r.usedInodes, uint64(inodes))
		time.Sleep(time.Second * 10)
	}
}

func (r *redisMeta) checkQuota(size, inodes int64) bool {
	if size > 0 && r.fmt.Capacity > 0 && atomic.LoadUint64(&r.usedSpace)+uint64(size) > r.fmt.Capacity {
		return true
	}
	return inodes > 0 && r.fmt.Inodes > 0 && atomic.LoadUint64(&r.usedInodes)+uint64(inodes) > r.fmt.Inodes
}

func (r *redisMeta) getSession(sid string, detail bool) (*Session, error) {
	ctx := Background
	info, err := r.rdb.HGet(ctx, sessionInfos, sid).Bytes()
	if err == redis.Nil { // legacy client has no info
		info = []byte("{}")
	} else if err != nil {
		return nil, fmt.Errorf("HGet %s %s: %s", sessionInfos, sid, err)
	}
	var s Session
	if err := json.Unmarshal(info, &s); err != nil {
		return nil, fmt.Errorf("corrupted session info; json error: %s", err)
	}
	s.Sid, _ = strconv.ParseUint(sid, 10, 64)
	if detail {
		inodes, err := r.rdb.SMembers(ctx, r.sustained(int64(s.Sid))).Result()
		if err != nil {
			return nil, fmt.Errorf("SMembers %s: %s", sid, err)
		}
		s.Sustained = make([]Ino, 0, len(inodes))
		for _, sinode := range inodes {
			inode, _ := strconv.ParseUint(sinode, 10, 64)
			s.Sustained = append(s.Sustained, Ino(inode))
		}

		locks, err := r.rdb.SMembers(ctx, r.lockedKey(int64(s.Sid))).Result()
		if err != nil {
			return nil, fmt.Errorf("SMembers %s: %s", sid, err)
		}
		s.Flocks = make([]Flock, 0, len(locks)) // greedy
		s.Plocks = make([]Plock, 0, len(locks))
		for _, lock := range locks {
			owners, err := r.rdb.HGetAll(ctx, lock).Result()
			if err != nil {
				return nil, fmt.Errorf("HGetAll %s: %s", lock, err)
			}
			isFlock := strings.HasPrefix(lock, "lockf")
			inode, _ := strconv.ParseUint(lock[5:], 10, 64)
			for k, v := range owners {
				parts := strings.Split(k, "_")
				if parts[0] != sid {
					continue
				}
				owner, _ := strconv.ParseUint(parts[1], 16, 64)
				if isFlock {
					s.Flocks = append(s.Flocks, Flock{Ino(inode), owner, v})
				} else {
					s.Plocks = append(s.Plocks, Plock{Ino(inode), owner, []byte(v)})
				}
			}
		}
	}
	return &s, nil
}

func (r *redisMeta) GetSession(sid uint64) (*Session, error) {
	key := strconv.FormatUint(sid, 10)
	score, err := r.rdb.ZScore(Background, allSessions, key).Result()
	if err != nil {
		return nil, err
	}
	s, err := r.getSession(key, true)
	if err != nil {
		return nil, err
	}
	s.Heartbeat = time.Unix(int64(score), 0)
	return s, nil
}

func (r *redisMeta) ListSessions() ([]*Session, error) {
	keys, err := r.rdb.ZRangeWithScores(Background, allSessions, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(keys))
	for _, k := range keys {
		s, err := r.getSession(k.Member.(string), false)
		if err != nil {
			logger.Errorf("get session: %s", err)
			continue
		}
		s.Heartbeat = time.Unix(int64(k.Score), 0)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *redisMeta) OnMsg(mtype uint32, cb MsgCallback) {
	r.msgCallbacks.Lock()
	defer r.msgCallbacks.Unlock()
	r.msgCallbacks.callbacks[mtype] = cb
}

func (r *redisMeta) newMsg(mid uint32, args ...interface{}) error {
	r.msgCallbacks.Lock()
	cb, ok := r.msgCallbacks.callbacks[mid]
	r.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (r *redisMeta) sustained(sid int64) string {
	return "session" + strconv.FormatInt(sid, 10)
}

func (r *redisMeta) lockedKey(sid int64) string {
	return "locked" + strconv.FormatInt(sid, 10)
}

func (r *redisMeta) symKey(inode Ino) string {
	return "s" + inode.String()
}

func (r *redisMeta) inodeKey(inode Ino) string {
	return "i" + inode.String()
}

func (r *redisMeta) entryKey(parent Ino) string {
	return "d" + parent.String()
}

func (r *redisMeta) chunkKey(inode Ino, indx uint32) string {
	return "c" + inode.String() + "_" + strconv.FormatInt(int64(indx), 10)
}

func (r *redisMeta) sliceKey(chunkid uint64, size uint32) string {
	return "k" + strconv.FormatUint(chunkid, 10) + "_" + strconv.FormatUint(uint64(size), 10)
}

func (r *redisMeta) xattrKey(inode Ino) string {
	return "x" + inode.String()
}

func (r *redisMeta) flockKey(inode Ino) string {
	return "lockf" + inode.String()
}

func (r *redisMeta) ownerKey(owner uint64) string {
	return fmt.Sprintf("%d_%016X", r.sid, owner)
}

func (r *redisMeta) plockKey(inode Ino) string {
	return "lockp" + inode.String()
}

func (r *redisMeta) nextInode() (Ino, error) {
	ino, err := r.rdb.Incr(Background, "nextinode").Uint64()
	if ino == 1 {
		ino, err = r.rdb.Incr(Background, "nextinode").Uint64()
	}
	return Ino(ino), err
}

func (r *redisMeta) packEntry(_type uint8, inode Ino) []byte {
	wb := utils.NewBuffer(9)
	wb.Put8(_type)
	wb.Put64(uint64(inode))
	return wb.Bytes()
}

func (r *redisMeta) parseEntry(buf []byte) (uint8, Ino) {
	if len(buf) != 9 {
		panic("invalid entry")
	}
	return buf[0], Ino(binary.BigEndian.Uint64(buf[1:]))
}

func (r *redisMeta) parseAttr(buf []byte, attr *Attr) {
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

func (r *redisMeta) marshal(attr *Attr) []byte {
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

func align4K(length uint64) int64 {
	if length == 0 {
		return 1 << 12
	}
	return int64((((length - 1) >> 12) + 1) << 12)
}

func (r *redisMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	if r.fmt.Capacity > 0 {
		*totalspace = r.fmt.Capacity
	} else {
		*totalspace = 1 << 50
	}
	c, cancel := context.WithTimeout(ctx, time.Millisecond*300)
	defer cancel()
	used, _ := r.rdb.IncrBy(c, usedSpace, 0).Result()
	if used < 0 {
		used = 0
	}
	used = ((used >> 16) + 1) << 16 // aligned to 64K
	if r.fmt.Capacity > 0 {
		if used > int64(*totalspace) {
			*totalspace = uint64(used)
		}
	} else {
		for used*10 > int64(*totalspace)*8 {
			*totalspace *= 2
		}
	}
	*availspace = *totalspace - uint64(used)
	inodes, _ := r.rdb.IncrBy(c, totalInodes, 0).Result()
	if inodes < 0 {
		inodes = 0
	}
	*iused = uint64(inodes)
	if r.fmt.Inodes > 0 {
		if *iused > r.fmt.Inodes {
			*iavail = 0
		} else {
			*iavail = r.fmt.Inodes - *iused
		}
	} else {
		*iavail = 10 << 20
	}
	return 0
}

func GetSummary(r Meta, ctx Context, inode Ino, summary *Summary) syscall.Errno {
	var attr Attr
	if st := r.GetAttr(ctx, inode, &attr); st != 0 {
		return st
	}
	if attr.Typ == TypeDirectory {
		var entries []*Entry
		if st := r.Readdir(ctx, inode, 1, &entries); st != 0 {
			return st
		}
		for _, e := range entries {
			if e.Inode == inode || len(e.Name) == 2 && bytes.Equal(e.Name, []byte("..")) {
				continue
			}
			if e.Attr.Typ == TypeDirectory {
				if st := GetSummary(r, ctx, e.Inode, summary); st != 0 {
					return st
				}
			} else {
				summary.Files++
				summary.Length += e.Attr.Length
				summary.Size += uint64(align4K(e.Attr.Length))
			}
		}
		summary.Dirs++
		summary.Size += 4096
	} else {
		summary.Files++
		summary.Length += attr.Length
		summary.Size += uint64(align4K(attr.Length))
	}
	return 0
}

func (r *redisMeta) resolveCase(ctx Context, parent Ino, name string) *Entry {
	var entries []*Entry
	_ = r.Readdir(ctx, parent, 0, &entries)
	for _, e := range entries {
		n := string(e.Name)
		if strings.EqualFold(name, n) {
			return e
		}
	}
	return nil
}

func (r *redisMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var foundIno Ino
	var encodedAttr []byte
	var err error

	entryKey := r.entryKey(parent)
	if len(r.shaLookup) > 0 && attr != nil && !r.conf.CaseInsensi {
		var res interface{}
		res, err = r.rdb.EvalSha(ctx, r.shaLookup, []string{entryKey, name}).Result()
		if err != nil {
			if strings.Contains(err.Error(), "NOSCRIPT") {
				var err2 error
				r.shaLookup, err2 = r.rdb.ScriptLoad(Background, scriptLookup).Result()
				if err2 != nil {
					logger.Warnf("load scriptLookup: %s", err2)
				} else {
					logger.Info("loaded script for lookup")
				}
				return r.Lookup(ctx, parent, name, inode, attr)
			}
			if strings.Contains(err.Error(), "Error running script") {
				logger.Warnf("eval lookup: %s", err)
				r.shaLookup = ""
				return r.Lookup(ctx, parent, name, inode, attr)
			}
			return errno(err)
		}
		vals, ok := res.([]interface{})
		if !ok {
			return errno(fmt.Errorf("invalid script result: %v", res))
		}
		returnedIno, ok := vals[0].(int64)
		if !ok {
			return errno(fmt.Errorf("invalid script result: %v", res))
		}
		returnedAttr, ok := vals[1].(string)
		if !ok {
			return errno(fmt.Errorf("invalid script result: %v", res))
		}
		if returnedAttr == "" {
			return syscall.ENOENT
		}
		foundIno = Ino(returnedIno)
		encodedAttr = []byte(returnedAttr)
	} else {
		var buf []byte
		buf, err = r.rdb.HGet(ctx, entryKey, name).Bytes()
		if err == nil {
			_, foundIno = r.parseEntry(buf)
		}
		if err == redis.Nil && r.conf.CaseInsensi {
			e := r.resolveCase(ctx, parent, name)
			if e != nil {
				foundIno = e.Inode
				err = nil
			}
		}
		if err != nil {
			return errno(err)
		}
		if attr != nil {
			encodedAttr, err = r.rdb.Get(ctx, r.inodeKey(foundIno)).Bytes()
		}
	}

	if err == nil && attr != nil {
		r.parseAttr(encodedAttr, attr)
	}
	if inode != nil {
		*inode = foundIno
	}
	return errno(err)
}

func (r *redisMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	if len(r.shaResolve) == 0 || r.conf.CaseInsensi {
		return syscall.ENOTSUP
	}
	args := []string{parent.String(), path,
		strconv.FormatUint(uint64(ctx.Uid()), 10),
		strconv.FormatUint(uint64(ctx.Gid()), 10)}
	res, err := r.rdb.EvalSha(ctx, r.shaResolve, args).Result()
	if err != nil {
		fields := strings.Fields(err.Error())
		lastError := fields[len(fields)-1]
		switch lastError {
		case "ENOENT":
			return syscall.ENOENT
		case "EACCESS":
			return syscall.EACCES
		case "ENOTDIR":
			return syscall.ENOTDIR
		case "ENOTSUP":
			return syscall.ENOTSUP
		default:
			logger.Warnf("resolve %d %s: %s", parent, path, err)
			r.shaResolve = ""
			return syscall.ENOTSUP
		}
	}
	vals, ok := res.([]interface{})
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	returnedIno, ok := vals[0].(int64)
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	returnedAttr, ok := vals[1].(string)
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	if returnedAttr == "" {
		return syscall.ENOENT
	}
	if inode != nil {
		*inode = Ino(returnedIno)
	}
	r.parseAttr([]byte(returnedAttr), attr)
	return 0
}

func accessMode(attr *Attr, uid uint32, gid uint32) uint8 {
	if uid == 0 {
		return 0x7
	}
	mode := attr.Mode
	if uid == attr.Uid {
		return uint8(mode>>6) & 7
	}
	if gid == attr.Gid {
		return uint8(mode>>3) & 7
	}
	return uint8(mode & 7)
}

func (r *redisMeta) Access(ctx Context, inode Ino, mmask uint8, attr *Attr) syscall.Errno {
	if ctx.Uid() == 0 {
		return 0
	}

	if attr == nil || !attr.Full {
		if attr == nil {
			attr = &Attr{}
		}
		err := r.GetAttr(ctx, inode, attr)
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

func (r *redisMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	var c context.Context = ctx
	if inode == 1 {
		var cancel func()
		c, cancel = context.WithTimeout(ctx, time.Millisecond*300)
		defer cancel()
	}
	a, err := r.rdb.Get(c, r.inodeKey(inode)).Bytes()
	if err == nil {
		r.parseAttr(a, attr)
	}
	if err != nil && inode == 1 {
		err = nil
		attr.Typ = TypeDirectory
		attr.Mode = 0777
		attr.Nlink = 2
		attr.Length = 4 << 10
	}
	return errno(err)
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if eno, ok := err.(syscall.Errno); ok {
		return eno
	}
	if err == redis.Nil {
		return syscall.ENOENT
	}
	if strings.HasPrefix(err.Error(), "OOM") {
		return syscall.ENOSPC
	}
	logger.Errorf("error: %s", err)
	return syscall.EIO
}

type timeoutError interface {
	Timeout() bool
}

func shouldRetry(err error, retryOnFailure bool) bool {
	switch err {
	case redis.TxFailedErr:
		return true
	case io.EOF, io.ErrUnexpectedEOF:
		return retryOnFailure
	case nil, context.Canceled, context.DeadlineExceeded:
		return false
	}

	if v, ok := err.(timeoutError); ok && v.Timeout() {
		return retryOnFailure
	}

	s := err.Error()
	if s == "ERR max number of clients reached" {
		return true
	}
	ps := strings.SplitN(s, " ", 3)
	switch ps[0] {
	case "LOADING":
	case "READONLY":
	case "CLUSTERDOWN":
	case "TRYAGAIN":
	case "MOVED":
	case "ASK":
	case "ERR":
		if len(ps) > 1 {
			switch ps[1] {
			case "DISABLE":
				fallthrough
			case "NOWRITE":
				fallthrough
			case "NOREAD":
				return true
			}
		}
		return false
	default:
		return false
	}
	return true
}

func (r *redisMeta) txn(ctx Context, txf func(tx *redis.Tx) error, keys ...string) syscall.Errno {
	var err error
	var khash = fnv.New32()
	_, _ = khash.Write([]byte(keys[0]))
	l := &r.txlocks[int(khash.Sum32())%len(r.txlocks)]
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	l.Lock()
	defer l.Unlock()
	// TODO: enable retry for some of idempodent transactions
	var retryOnFailture = false
	for i := 0; i < 50; i++ {
		err = r.rdb.Watch(ctx, txf, keys...)
		if shouldRetry(err, retryOnFailture) {
			txRestart.Add(1)
			time.Sleep(time.Microsecond * 100 * time.Duration(rand.Int()%(i+1)))
			continue
		}
		return errno(err)
	}
	return errno(err)
}

func (r *redisMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		var t Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &t)
		if t.Typ != TypeFile {
			return syscall.EPERM
		}
		old := t.Length
		var zeroChunks []uint32
		if length > old {
			if r.checkQuota(align4K(length)-align4K(old), 0) {
				return syscall.ENOSPC
			}
			if (length-old)/ChunkSize >= 100 {
				// super large
				var cursor uint64
				var keys []string
				for {
					keys, cursor, err = tx.Scan(ctx, cursor, fmt.Sprintf("c%d_*", inode), 10000).Result()
					if err != nil {
						return err
					}
					for _, key := range keys {
						indx, err := strconv.Atoi(strings.Split(key, "_")[1])
						if err != nil {
							logger.Errorf("parse %s: %s", key, err)
							continue
						}
						if uint64(indx) > old/ChunkSize && uint64(indx) < length/ChunkSize {
							zeroChunks = append(zeroChunks, uint32(indx))
						}
					}
					if cursor <= 0 {
						break
					}
				}
			} else {
				for i := old/ChunkSize + 1; i < length/ChunkSize; i++ {
					zeroChunks = append(zeroChunks, uint32(i))
				}
			}
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&t), 0)
			if length > old {
				// zero out from old to length
				var l = uint32(length - old)
				if length > (old/ChunkSize+1)*ChunkSize {
					l = ChunkSize - uint32(old%ChunkSize)
				}
				pipe.RPush(ctx, r.chunkKey(inode, uint32(old/ChunkSize)), marshalSlice(uint32(old%ChunkSize), 0, 0, 0, l))
				buf := marshalSlice(0, 0, 0, 0, ChunkSize)
				for _, indx := range zeroChunks {
					pipe.RPushX(ctx, r.chunkKey(inode, indx), buf)
				}
				if length > (old/ChunkSize+1)*ChunkSize && length%ChunkSize > 0 {
					pipe.RPush(ctx, r.chunkKey(inode, uint32(length/ChunkSize)), marshalSlice(0, 0, 0, 0, uint32(length%ChunkSize)))
				}
			}
			pipe.IncrBy(ctx, usedSpace, align4K(length)-align4K(old))
			return nil
		})
		if err == nil {
			if attr != nil {
				*attr = t
			}
		}
		return err
	}, r.inodeKey(inode))
}

const (
	// fallocate
	fallocKeepSize  = 0x01
	fallocPunchHole = 0x02
	// RESERVED: fallocNoHideStale   = 0x04
	fallocCollapesRange = 0x08
	fallocZeroRange     = 0x10
	fallocInsertRange   = 0x20
)

func (r *redisMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
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
	return r.txn(ctx, func(tx *redis.Tx) error {
		var t Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &t)
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
		if length > old && r.checkQuota(align4K(length)-align4K(old), 0) {
			return syscall.ENOSPC
		}
		t.Length = length
		now := time.Now()
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&t), 0)
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
					pipe.RPush(ctx, r.chunkKey(inode, indx), marshalSlice(uint32(coff), 0, 0, 0, uint32(l)))
					off += l
					size -= l
				}
			}
			pipe.IncrBy(ctx, usedSpace, align4K(length)-align4K(old))
			return nil
		})
		return err
	}, r.inodeKey(inode))
}

func (r *redisMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		var cur Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &cur)
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
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&cur), 0)
			return nil
		})
		if err == nil {
			*attr = cur
		}
		return err
	}, r.inodeKey(inode))
}

func (r *redisMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := r.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	target, err := r.rdb.Get(ctx, r.symKey(inode)).Bytes()
	if err == nil {
		*path = target
		r.symlinks.Store(inode, target)
	}
	return errno(err)
}

func (r *redisMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	return r.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (r *redisMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	return r.mknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (r *redisMeta) mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	if r.checkQuota(4<<10, 1) {
		return syscall.ENOSPC
	}
	ino, err := r.nextInode()
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
	if inode != nil {
		*inode = ino
	}

	return r.txn(ctx, func(tx *redis.Tx) error {
		var pattr Attr
		a, err := tx.Get(ctx, r.inodeKey(parent)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}

		err = tx.HGet(ctx, r.entryKey(parent), name).Err()
		if err != nil && err != redis.Nil {
			return err
		} else if err == nil {
			return syscall.EEXIST
		} else if err == redis.Nil && r.conf.CaseInsensi && r.resolveCase(ctx, parent, name) != nil {
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, r.entryKey(parent), name, r.packEntry(_type, ino))
			pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			pipe.Set(ctx, r.inodeKey(ino), r.marshal(attr), 0)
			if _type == TypeSymlink {
				pipe.Set(ctx, r.symKey(ino), path, 0)
			}
			pipe.IncrBy(ctx, usedSpace, align4K(0))
			pipe.Incr(ctx, totalInodes)
			return nil
		})
		return err
	}, r.inodeKey(parent), r.entryKey(parent))
}

func (r *redisMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	return r.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, inode, attr)
}

func (r *redisMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno {
	err := r.Mknod(ctx, parent, name, TypeFile, mode, cumask, 0, inode, attr)
	if err == 0 && inode != nil {
		r.Lock()
		r.openFiles[*inode] = 1
		r.Unlock()
	}
	return err
}

func (r *redisMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	buf, err := r.rdb.HGet(ctx, r.entryKey(parent), name).Bytes()
	if err == redis.Nil && r.conf.CaseInsensi {
		if e := r.resolveCase(ctx, parent, name); e != nil {
			name = string(e.Name)
			buf = r.packEntry(e.Attr.Typ, e.Inode)
			err = nil
		}
	}
	if err != nil {
		return errno(err)
	}
	_type, inode := r.parseEntry(buf)
	if _type == TypeDirectory {
		return syscall.EPERM
	}

	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		var pattr, attr Attr
		r.parseAttr([]byte(rs[0].(string)), &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		r.parseAttr([]byte(rs[1].(string)), &attr)
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())
		if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
			return syscall.EACCES
		}

		buf, err := tx.HGet(ctx, r.entryKey(parent), name).Bytes()
		if err != nil {
			return err
		}
		_type2, inode2 := r.parseEntry(buf)
		if _type2 != _type || inode2 != inode {
			return syscall.EAGAIN
		}

		attr.Nlink--
		var opened bool
		if _type == TypeFile && attr.Nlink == 0 {
			r.Lock()
			opened = r.openFiles[inode] > 0
			r.Unlock()
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parent), name)
			pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			pipe.Del(ctx, r.xattrKey(inode))
			if attr.Nlink > 0 {
				pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
			} else {
				switch _type {
				case TypeFile:
					if opened {
						pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
						pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(inode)))
					} else {
						pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(inode, attr.Length)})
						pipe.Del(ctx, r.inodeKey(inode))
						pipe.IncrBy(ctx, usedSpace, -align4K(attr.Length))
						pipe.Decr(ctx, totalInodes)
					}
				case TypeSymlink:
					pipe.Del(ctx, r.symKey(inode))
					fallthrough
				default:
					pipe.Del(ctx, r.inodeKey(inode))
					pipe.IncrBy(ctx, usedSpace, -align4K(0))
					pipe.Decr(ctx, totalInodes)
				}
			}
			return nil
		})
		if err == nil && _type == TypeFile && attr.Nlink == 0 {
			if opened {
				r.Lock()
				r.removedFiles[inode] = true
				r.Unlock()
			} else {
				go r.deleteFile(inode, attr.Length, "")
			}
		}
		return err
	}, r.entryKey(parent), r.inodeKey(parent), r.inodeKey(inode))
}

func (r *redisMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}
	buf, err := r.rdb.HGet(ctx, r.entryKey(parent), name).Bytes()
	if err == redis.Nil && r.conf.CaseInsensi {
		if e := r.resolveCase(ctx, parent, name); e != nil {
			name = string(e.Name)
			buf = r.packEntry(e.Attr.Typ, e.Inode)
			err = nil
		}
	}
	if err != nil {
		return errno(err)
	}
	typ, inode := r.parseEntry(buf)
	if typ != TypeDirectory {
		return syscall.ENOTDIR
	}

	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		var pattr, attr Attr
		r.parseAttr([]byte(rs[0].(string)), &pattr)
		r.parseAttr([]byte(rs[1].(string)), &attr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		now := time.Now()
		pattr.Nlink--
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())

		buf, err := tx.HGet(ctx, r.entryKey(parent), name).Bytes()
		if err != nil {
			return err
		}
		typ, inode = r.parseEntry(buf)
		if typ != TypeDirectory {
			return syscall.ENOTDIR
		}

		cnt, err := tx.HLen(ctx, r.entryKey(inode)).Result()
		if err != nil {
			return err
		}
		if cnt > 0 {
			return syscall.ENOTEMPTY
		}
		if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
			return syscall.EACCES
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parent), name)
			pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			pipe.Del(ctx, r.inodeKey(inode))
			pipe.Del(ctx, r.xattrKey(inode))
			// pipe.Del(ctx, r.entryKey(inode))
			pipe.IncrBy(ctx, usedSpace, -align4K(0))
			pipe.Decr(ctx, totalInodes)
			return nil
		})
		return err
	}, r.inodeKey(parent), r.entryKey(parent), r.inodeKey(inode), r.entryKey(inode))
}

func emptyDir(r Meta, ctx Context, inode Ino, concurrent chan int) syscall.Errno {
	if st := r.Access(ctx, inode, 3, nil); st != 0 {
		return st
	}
	var entries []*Entry
	if st := r.Readdir(ctx, inode, 0, &entries); st != 0 {
		return st
	}
	var wg sync.WaitGroup
	var status syscall.Errno
	for _, e := range entries {
		if e.Inode == inode || len(e.Name) == 2 && string(e.Name) == ".." {
			continue
		}
		if e.Attr.Typ == TypeDirectory {
			select {
			case concurrent <- 1:
				wg.Add(1)
				go func(child Ino, name string) {
					defer wg.Done()
					e := emptyEntry(r, ctx, inode, name, child, concurrent)
					if e != 0 {
						status = e
					}
					<-concurrent
				}(e.Inode, string(e.Name))
			default:
				if st := emptyEntry(r, ctx, inode, string(e.Name), e.Inode, concurrent); st != 0 {
					return st
				}
			}
		} else {
			if st := r.Unlink(ctx, inode, string(e.Name)); st != 0 {
				return st
			}
		}
	}
	wg.Wait()
	return status
}

func emptyEntry(r Meta, ctx Context, parent Ino, name string, inode Ino, concurrent chan int) syscall.Errno {
	st := emptyDir(r, ctx, inode, concurrent)
	if st == 0 {
		st = r.Rmdir(ctx, parent, name)
		if st == syscall.ENOTEMPTY {
			st = emptyEntry(r, ctx, parent, name, inode, concurrent)
		}
	}
	return st
}

func Remove(r Meta, ctx Context, parent Ino, name string) syscall.Errno {
	if st := r.Access(ctx, parent, 3, nil); st != 0 {
		return st
	}
	var inode Ino
	var attr Attr
	if st := r.Lookup(ctx, parent, name, &inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		return r.Unlink(ctx, parent, name)
	}
	concurrent := make(chan int, 50)
	return emptyEntry(r, ctx, parent, name, inode, concurrent)
}

func (r *redisMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	buf, err := r.rdb.HGet(ctx, r.entryKey(parentSrc), nameSrc).Bytes()
	if err == redis.Nil && r.conf.CaseInsensi {
		if e := r.resolveCase(ctx, parentSrc, nameSrc); e != nil {
			nameSrc = string(e.Name)
			buf = r.packEntry(e.Attr.Typ, e.Inode)
			err = nil
		}
	}
	if err != nil {
		return errno(err)
	}
	typ, ino := r.parseEntry(buf)
	if parentSrc == parentDst && nameSrc == nameDst {
		if inode != nil {
			*inode = ino
		}
		return 0
	}
	buf, err = r.rdb.HGet(ctx, r.entryKey(parentDst), nameDst).Bytes()
	if err == redis.Nil && r.conf.CaseInsensi {
		if e := r.resolveCase(ctx, parentDst, nameDst); e != nil {
			nameDst = string(e.Name)
			buf = r.packEntry(e.Attr.Typ, e.Inode)
			err = nil
		}
	}
	if err != nil && err != redis.Nil {
		return errno(err)
	}
	keys := []string{r.entryKey(parentSrc), r.inodeKey(parentSrc), r.inodeKey(ino), r.entryKey(parentDst), r.inodeKey(parentDst)}

	var dino Ino
	var dtyp uint8
	if err == nil {
		dtyp, dino = r.parseEntry(buf)
		keys = append(keys, r.inodeKey(dino))
		if dtyp == TypeDirectory {
			keys = append(keys, r.entryKey(dino))
		}
	}

	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parentSrc), r.inodeKey(parentDst), r.inodeKey(ino)).Result()
		if rs[0] == nil || rs[1] == nil || rs[2] == nil {
			return redis.Nil
		}
		var sattr, dattr, iattr Attr
		r.parseAttr([]byte(rs[0].(string)), &sattr)
		if sattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		r.parseAttr([]byte(rs[1].(string)), &dattr)
		if dattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		r.parseAttr([]byte(rs[2].(string)), &iattr)

		buf, err = tx.HGet(ctx, r.entryKey(parentDst), nameDst).Bytes()
		if err != nil && err != redis.Nil {
			return err
		}
		var tattr Attr
		var opened bool
		if err == nil {
			if ctx.Value(CtxKey("behavior")) == "Hadoop" {
				return syscall.EEXIST
			}
			typ1, dino1 := r.parseEntry(buf)
			if dino1 != dino || typ1 != dtyp {
				return syscall.EAGAIN
			}
			a, err := tx.Get(ctx, r.inodeKey(dino)).Bytes()
			if err != nil {
				return err
			}
			r.parseAttr(a, &tattr)
			if typ1 == TypeDirectory {
				cnt, err := tx.HLen(ctx, r.entryKey(dino)).Result()
				if err != nil {
					return err
				}
				if cnt != 0 {
					return syscall.ENOTEMPTY
				}
			} else {
				tattr.Nlink--
				if tattr.Nlink > 0 {
					now := time.Now()
					tattr.Ctime = now.Unix()
					tattr.Ctimensec = uint32(now.Nanosecond())
				} else if dtyp == TypeFile {
					r.Lock()
					opened = r.openFiles[dino] > 0
					r.Unlock()
				}
			}
			if ctx.Uid() != 0 && dattr.Mode&01000 != 0 && ctx.Uid() != dattr.Uid && ctx.Uid() != tattr.Uid {
				return syscall.EACCES
			}
		} else {
			dino = 0
		}

		buf, err := tx.HGet(ctx, r.entryKey(parentSrc), nameSrc).Bytes()
		if err != nil {
			return err
		}
		_, ino1 := r.parseEntry(buf)
		if ino != ino1 {
			return syscall.EAGAIN
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
		if attr != nil {
			*attr = iattr
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parentSrc), nameSrc)
			pipe.Set(ctx, r.inodeKey(parentSrc), r.marshal(&sattr), 0)
			if dino > 0 {
				if dtyp != TypeDirectory && tattr.Nlink > 0 {
					pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
				} else {
					if dtyp == TypeFile {
						if opened {
							pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
							pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(dino)))
						} else {
							pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(dino, dattr.Length)})
							pipe.Del(ctx, r.inodeKey(dino))
							pipe.IncrBy(ctx, usedSpace, -align4K(tattr.Length))
							pipe.Decr(ctx, totalInodes)
						}
					} else {
						if dtyp == TypeDirectory {
							dattr.Nlink--
						} else if dtyp == TypeSymlink {
							pipe.Del(ctx, r.symKey(dino))
						}
						pipe.Del(ctx, r.inodeKey(dino))
						pipe.IncrBy(ctx, usedSpace, -align4K(0))
						pipe.Decr(ctx, totalInodes)
					}
					pipe.Del(ctx, r.xattrKey(dino))
				}
				pipe.HDel(ctx, r.entryKey(parentDst), nameDst)
			}
			pipe.HSet(ctx, r.entryKey(parentDst), nameDst, buf)
			if parentDst != parentSrc {
				pipe.Set(ctx, r.inodeKey(parentDst), r.marshal(&dattr), 0)
			}
			pipe.Set(ctx, r.inodeKey(ino), r.marshal(&iattr), 0)
			return nil
		})
		if err == nil && dino > 0 && dtyp == TypeFile {
			if opened {
				r.Lock()
				r.removedFiles[dino] = true
				r.Unlock()
			} else {
				go r.deleteFile(dino, dattr.Length, "")
			}
		}
		return err
	}, keys...)
}

func (r *redisMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, err := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		var pattr, iattr Attr
		r.parseAttr([]byte(rs[0].(string)), &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		r.parseAttr([]byte(rs[1].(string)), &iattr)
		if iattr.Typ == TypeDirectory {
			return syscall.EPERM
		}
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Nlink++

		err = tx.HGet(ctx, r.entryKey(parent), name).Err()
		if err != nil && err != redis.Nil {
			return err
		} else if err == nil {
			return syscall.EEXIST
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, r.entryKey(parent), name, r.packEntry(iattr.Typ, inode))
			pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&iattr), 0)
			return nil
		})
		if err == nil && attr != nil {
			*attr = iattr
		}
		return err
	}, r.inodeKey(inode), r.entryKey(parent), r.inodeKey(parent))
}

func (r *redisMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	var attr Attr
	if err := r.GetAttr(ctx, inode, &attr); err != 0 {
		return err
	}
	*entries = []*Entry{
		{
			Inode: inode,
			Name:  []byte("."),
			Attr:  &Attr{Typ: TypeDirectory},
		},
	}
	if attr.Parent > 0 {
		*entries = append(*entries, &Entry{
			Inode: attr.Parent,
			Name:  []byte(".."),
			Attr:  &Attr{Typ: TypeDirectory},
		})
	}

	var keys []string
	var cursor uint64
	var err error
	for {
		keys, cursor, err = r.rdb.HScan(ctx, r.entryKey(inode), cursor, "*", 10000).Result()
		if err != nil {
			return errno(err)
		}
		newEntries := make([]Entry, len(keys)/2)
		newAttrs := make([]Attr, len(keys)/2)
		for i := 0; i < len(keys); i += 2 {
			typ, inode := r.parseEntry([]byte(keys[i+1]))
			ent := &newEntries[i/2]
			ent.Inode = inode
			ent.Name = []byte(keys[i])
			ent.Attr = &newAttrs[i/2]
			ent.Attr.Typ = typ
			*entries = append(*entries, ent)
		}
		if cursor == 0 {
			break
		}
	}

	if plus != 0 {
		fillAttr := func(es []*Entry) error {
			var keys = make([]string, len(es))
			for i, e := range es {
				keys[i] = r.inodeKey(e.Inode)
			}
			rs, err := r.rdb.MGet(ctx, keys...).Result()
			if err != nil {
				return err
			}
			for j, re := range rs {
				if re != nil {
					if a, ok := re.(string); ok {
						r.parseAttr([]byte(a), es[j].Attr)
					}
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
						e := fillAttr(es)
						if e != nil {
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

func (r *redisMeta) cleanStaleSession(sid int64) {
	var ctx = Background
	key := r.sustained(sid)
	inodes, err := r.rdb.SMembers(ctx, key).Result()
	if err != nil {
		logger.Warnf("SMembers %s: %s", key, err)
		return
	}
	for _, sinode := range inodes {
		inode, _ := strconv.ParseInt(sinode, 10, 0)
		if err := r.deleteInode(Ino(inode)); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
		} else {
			r.rdb.SRem(ctx, key, sinode)
		}
	}
	if len(inodes) == 0 {
		r.rdb.ZRem(ctx, allSessions, strconv.Itoa(int(sid)))
		r.rdb.HDel(ctx, sessionInfos, strconv.Itoa(int(sid)))
		logger.Infof("cleanup session %d", sid)
	}
}

func (r *redisMeta) cleanStaleLocks(ssid string) {
	var ctx = Background
	key := "locked" + ssid
	inodes, err := r.rdb.SMembers(ctx, key).Result()
	if err != nil {
		logger.Warnf("SMembers %s: %s", key, err)
		return
	}
	for _, k := range inodes {
		owners, _ := r.rdb.HKeys(ctx, k).Result()
		for _, o := range owners {
			if strings.Split(o, "_")[0] == ssid {
				err = r.rdb.HDel(ctx, k, o).Err()
				logger.Infof("cleanup lock on %s from session %s: %s", k, ssid, err)
			}
		}
		r.rdb.SRem(ctx, key, k)
	}
}

func (r *redisMeta) cleanStaleSessions() {
	// TODO: once per minute
	now := time.Now()
	var ctx = Background
	rng := &redis.ZRangeBy{Max: strconv.Itoa(int(now.Add(time.Minute * -5).Unix())), Count: 100}
	staleSessions, _ := r.rdb.ZRangeByScore(ctx, allSessions, rng).Result()
	for _, ssid := range staleSessions {
		sid, _ := strconv.Atoi(ssid)
		r.cleanStaleSession(int64(sid))
	}

	rng = &redis.ZRangeBy{Max: strconv.Itoa(int(now.Add(time.Minute * -3).Unix())), Count: 100}
	staleSessions, _ = r.rdb.ZRangeByScore(ctx, allSessions, rng).Result()
	for _, sid := range staleSessions {
		r.cleanStaleLocks(sid)
	}
}

func (r *redisMeta) refreshSession() {
	for {
		now := time.Now()
		r.rdb.ZAdd(Background, allSessions, &redis.Z{Score: float64(now.Unix()), Member: strconv.Itoa(int(r.sid))})
		time.Sleep(time.Minute)
		go r.cleanStaleSessions()
	}
}

func (r *redisMeta) deleteInode(inode Ino) error {
	var attr Attr
	var ctx = Background
	a, err := r.rdb.Get(ctx, r.inodeKey(inode)).Bytes()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	r.parseAttr(a, &attr)
	_, err = r.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(time.Now().Unix()), Member: r.toDelete(inode, attr.Length)})
		pipe.Del(ctx, r.inodeKey(inode))
		pipe.IncrBy(ctx, usedSpace, -align4K(attr.Length))
		pipe.Decr(ctx, totalInodes)
		return nil
	})
	if err == nil {
		go r.deleteFile(inode, attr.Length, "")
	}
	return err
}

func (r *redisMeta) Open(ctx Context, inode Ino, flags uint8, attr *Attr) syscall.Errno {
	var err syscall.Errno
	if attr != nil {
		err = r.GetAttr(ctx, inode, attr)
	}
	if err == 0 {
		r.Lock()
		r.openFiles[inode] = r.openFiles[inode] + 1
		r.Unlock()
	}
	return 0
}

func (r *redisMeta) Close(ctx Context, inode Ino) syscall.Errno {
	r.Lock()
	defer r.Unlock()
	refs := r.openFiles[inode]
	if refs <= 1 {
		delete(r.openFiles, inode)
		if r.removedFiles[inode] {
			delete(r.removedFiles, inode)
			go func() {
				if err := r.deleteInode(inode); err == nil {
					r.rdb.SRem(ctx, r.sustained(r.sid), strconv.Itoa(int(inode)))
				}
			}()
		}
	} else {
		r.openFiles[inode] = refs - 1
	}
	return 0
}

func buildSlice(ss []*slice) []Slice {
	var root *slice
	for _, s := range ss {
		if root != nil {
			var right *slice
			s.left, right = root.cut(s.pos)
			_, s.right = right.cut(s.pos + s.len)
		}
		root = s
	}
	var pos uint32
	var chunks []Slice
	root.visit(func(s *slice) {
		if s.pos > pos {
			chunks = append(chunks, Slice{Size: s.pos - pos, Len: s.pos - pos})
			pos = s.pos
		}
		chunks = append(chunks, Slice{Chunkid: s.chunkid, Size: s.size, Off: s.off, Len: s.len})
		pos += s.len
	})
	return chunks
}

func (r *redisMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	vals, err := r.rdb.LRange(ctx, r.chunkKey(inode, indx), 0, 1000000).Result()
	if err != nil {
		return errno(err)
	}
	ss := readSlices(vals)
	*chunks = buildSlice(ss)
	if len(vals) >= 5 || len(*chunks) >= 5 {
		go r.compactChunk(inode, indx, false)
	}
	return 0
}

func (r *redisMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	cid, err := r.rdb.Incr(ctx, "nextchunk").Uint64()
	if err == nil {
		*chunkid = cid
	}
	return errno(err)
}

func (r *redisMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		var attr Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &attr)
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		var added int64
		if newleng > attr.Length {
			added = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if r.checkQuota(added, 0) {
			return syscall.ENOSPC
		}
		now := time.Now()
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())

		var rpush *redis.IntCmd
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			rpush = pipe.RPush(ctx, r.chunkKey(inode, indx), marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len))
			// most of chunk are used by single inode, so use that as the default (1 == not exists)
			// pipe.Incr(ctx, r.sliceKey(slice.Chunkid, slice.Size))
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
			if added > 0 {
				pipe.IncrBy(ctx, usedSpace, added)
			}
			return nil
		})
		if err == nil && rpush.Val()%20 == 0 {
			go r.compactChunk(inode, indx, false)
		}
		return err
	}, r.inodeKey(inode))
}

func (r *redisMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, err := tx.MGet(ctx, r.inodeKey(fin), r.inodeKey(fout)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		var sattr Attr
		r.parseAttr([]byte(rs[0].(string)), &sattr)
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
		r.parseAttr([]byte(rs[1].(string)), &attr)
		if attr.Typ != TypeFile {
			return syscall.EINVAL
		}

		newleng := offOut + size
		var added int64
		if newleng > attr.Length {
			added = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if r.checkQuota(added, 0) {
			return syscall.ENOSPC
		}
		now := time.Now()
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())

		p := tx.Pipeline()
		for i := offIn / ChunkSize; i <= (offIn+size)/ChunkSize; i++ {
			p.LRange(ctx, r.chunkKey(fin, uint32(i)), 0, 1000000)
		}
		vals, err := p.Exec(ctx)
		if err != nil {
			return err
		}

		_, err = tx.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			coff := offIn / ChunkSize * ChunkSize
			for _, v := range vals {
				sv := v.(*redis.StringSliceCmd).Val()
				// Add a zero chunk for hole
				ss := append([]*slice{{len: ChunkSize}}, readSlices(sv)...)
				cs := buildSlice(ss)
				tpos := coff
				for _, s := range cs {
					pos := tpos
					tpos += uint64(s.Len)
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
							pipe.RPush(ctx, r.chunkKey(fout, indx), marshalSlice(dpos, s.Chunkid, s.Size, s.Off, ChunkSize-dpos))
							if s.Chunkid > 0 {
								pipe.HIncrBy(ctx, sliceRefs, r.sliceKey(s.Chunkid, s.Size), 1)
							}

							skip := ChunkSize - dpos
							pipe.RPush(ctx, r.chunkKey(fout, indx+1), marshalSlice(0, s.Chunkid, s.Size, s.Off+skip, s.Len-skip))
							if s.Chunkid > 0 {
								pipe.HIncrBy(ctx, sliceRefs, r.sliceKey(s.Chunkid, s.Size), 1)
							}
						} else {
							pipe.RPush(ctx, r.chunkKey(fout, indx), marshalSlice(dpos, s.Chunkid, s.Size, s.Off, s.Len))
							if s.Chunkid > 0 {
								pipe.HIncrBy(ctx, sliceRefs, r.sliceKey(s.Chunkid, s.Size), 1)
							}
						}
					}
				}
				coff += ChunkSize
			}
			pipe.Set(ctx, r.inodeKey(fout), r.marshal(&attr), 0)
			if added > 0 {
				pipe.IncrBy(ctx, usedSpace, added)
			}
			return nil
		})
		if err == nil {
			*copied = size
		}
		return err
	}, r.inodeKey(fout), r.inodeKey(fin))
}

func (r *redisMeta) cleanupDeletedFiles() {
	for {
		time.Sleep(time.Minute)
		now := time.Now()
		members, _ := r.rdb.ZRangeByScore(Background, delfiles, &redis.ZRangeBy{Min: strconv.Itoa(0), Max: strconv.Itoa(int(now.Add(-time.Hour).Unix())), Count: 1000}).Result()
		for _, member := range members {
			ps := strings.Split(member, ":")
			inode, _ := strconv.ParseInt(ps[0], 10, 0)
			var length int64 = 1 << 30
			if len(ps) == 2 {
				length, _ = strconv.ParseInt(ps[1], 10, 0)
			} else if len(ps) > 2 {
				length, _ = strconv.ParseInt(ps[2], 10, 0)
			}
			logger.Debugf("cleanup chunks of inode %d with %d bytes (%s)", inode, length, member)
			r.deleteFile(Ino(inode), uint64(length), member)
		}
	}
}

func (r *redisMeta) cleanupSlices() {
	for {
		time.Sleep(time.Hour)

		// once per hour
		var ctx = Background
		last, _ := r.rdb.Get(ctx, "nextCleanupSlices").Uint64()
		now := time.Now().Unix()
		if last+3600 > uint64(now) {
			continue
		}
		r.rdb.Set(ctx, "nextCleanupSlices", now, 0)

		var ckeys []string
		var cursor uint64
		var err error
		for {
			ckeys, cursor, err = r.rdb.HScan(ctx, sliceRefs, cursor, "*", 1000).Result()
			if err != nil {
				logger.Errorf("scan slices: %s", err)
				break
			}
			if len(ckeys) > 0 {
				values, err := r.rdb.HMGet(ctx, sliceRefs, ckeys...).Result()
				if err != nil {
					logger.Warnf("mget slices: %s", err)
					break
				}
				for i, v := range values {
					if v == nil {
						continue
					}
					if strings.HasPrefix(v.(string), "-") { // < 0
						ps := strings.Split(ckeys[i], "_")
						if len(ps) == 2 {
							chunkid, _ := strconv.Atoi(ps[0][1:])
							size, _ := strconv.Atoi(ps[1])
							if chunkid > 0 && size > 0 {
								r.deleteSlice(ctx, uint64(chunkid), uint32(size))
							}
						}
					} else if v == "0" {
						r.cleanupZeroRef(ckeys[i])
					}
				}
			}
			if cursor == 0 {
				break
			}
		}
	}
}

func (r *redisMeta) cleanupZeroRef(key string) {
	var ctx = Background
	_ = r.txn(ctx, func(tx *redis.Tx) error {
		v, err := tx.HGet(ctx, sliceRefs, key).Int()
		if err != nil {
			return err
		}
		if v != 0 {
			return syscall.EINVAL
		}
		_, err = tx.Pipelined(ctx, func(p redis.Pipeliner) error {
			p.HDel(ctx, sliceRefs, key)
			return nil
		})
		return err
	}, sliceRefs)
}

func (r *redisMeta) cleanupLeakedChunks() {
	var ctx = Background
	var ckeys []string
	var cursor uint64
	var err error
	for {
		ckeys, cursor, err = r.rdb.Scan(ctx, cursor, "c*", 1000).Result()
		if err != nil {
			logger.Errorf("scan all chunks: %s", err)
			break
		}
		var ikeys []string
		var rs []*redis.IntCmd
		p := r.rdb.Pipeline()
		for _, k := range ckeys {
			ps := strings.Split(k, "_")
			if len(ps) != 2 {
				continue
			}
			ino, _ := strconv.ParseInt(ps[0][1:], 10, 0)
			ikeys = append(ikeys, k)
			rs = append(rs, p.Exists(ctx, r.inodeKey(Ino(ino))))
		}
		if len(rs) > 0 {
			_, err = p.Exec(ctx)
			if err != nil {
				logger.Errorf("check inodes: %s", err)
				return
			}
			for i, rr := range rs {
				if rr.Val() == 0 {
					key := ikeys[i]
					logger.Infof("found leaked chunk %s", key)
					ps := strings.Split(key, "_")
					ino, _ := strconv.ParseInt(ps[0][1:], 10, 0)
					indx, _ := strconv.Atoi(ps[1])
					_ = r.deleteChunk(Ino(ino), uint32(indx))
				}
			}
		}
		if cursor == 0 {
			break
		}
	}
}

func (r *redisMeta) cleanupOldSliceRefs() {
	var ctx = Background
	var ckeys []string
	var cursor uint64
	var err error
	for {
		ckeys, cursor, err = r.rdb.Scan(ctx, cursor, "k*", 1000).Result()
		if err != nil {
			logger.Errorf("scan slices: %s", err)
			break
		}
		if len(ckeys) > 0 {
			values, err := r.rdb.MGet(ctx, ckeys...).Result()
			if err != nil {
				logger.Warnf("mget slices: %s", err)
				break
			}
			var todel []string
			for i, v := range values {
				if v == nil {
					continue
				}
				if strings.HasPrefix(v.(string), "-") || v == "0" { // < 0
					// the objects will be deleted by gc
					todel = append(todel, ckeys[i])
				} else {
					vv, _ := strconv.Atoi(v.(string))
					r.rdb.HIncrBy(ctx, sliceRefs, ckeys[i], int64(vv))
					r.rdb.DecrBy(ctx, ckeys[i], int64(vv))
					logger.Infof("move refs %d for slice %s", vv, ckeys[i])
				}
			}
			r.rdb.Del(ctx, todel...)
		}
		if cursor == 0 {
			break
		}
	}
}

func (r *redisMeta) toDelete(inode Ino, length uint64) string {
	return inode.String() + ":" + strconv.Itoa(int(length))
}

func (r *redisMeta) deleteSlice(ctx Context, chunkid uint64, size uint32) {
	r.deleting <- 1
	defer func() { <-r.deleting }()
	err := r.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
	} else {
		_ = r.rdb.HDel(ctx, sliceRefs, r.sliceKey(chunkid, size))
	}
}

func (r *redisMeta) deleteChunk(inode Ino, indx uint32) error {
	var ctx = Background
	key := r.chunkKey(inode, indx)
	for {
		var slices []*slice
		var rs []*redis.IntCmd
		err := r.txn(ctx, func(tx *redis.Tx) error {
			slices = nil
			vals, err := tx.LRange(ctx, key, 0, 100).Result()
			if err == redis.Nil {
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				for _, v := range vals {
					rb := utils.ReadBuffer([]byte(v))
					_ = rb.Get32() // pos
					chunkid := rb.Get64()
					size := rb.Get32()
					slices = append(slices, &slice{chunkid: chunkid, size: size})
					pipe.LPop(ctx, key)
					rs = append(rs, pipe.HIncrBy(ctx, sliceRefs, r.sliceKey(chunkid, size), -1))
				}
				return nil
			})
			return err
		}, key)
		if err != syscall.Errno(0) {
			return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", key, err)
		}
		for i, s := range slices {
			if rs[i].Val() < 0 {
				r.deleteSlice(ctx, s.chunkid, s.size)
			}
		}
		if len(slices) < 100 {
			break
		}
	}
	return nil
}

func (r *redisMeta) deleteFile(inode Ino, length uint64, tracking string) {
	var ctx = Background
	var indx uint32
	p := r.rdb.Pipeline()
	for uint64(indx)*ChunkSize < length {
		var keys []string
		for i := 0; uint64(indx)*ChunkSize < length && i < 1000; i++ {
			key := r.chunkKey(inode, indx)
			keys = append(keys, key)
			_ = p.LLen(ctx, key)
			indx++
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			logger.Warnf("delete chunks of inode %d: %s", inode, err)
			return
		}
		for i, cmd := range cmds {
			val, err := cmd.(*redis.IntCmd).Result()
			if err == redis.Nil || val == 0 {
				continue
			}
			idx, _ := strconv.Atoi(strings.Split(keys[i], "_")[1])
			err = r.deleteChunk(inode, uint32(idx))
			if err != nil {
				logger.Warnf("delete chunk %s: %s", keys[i], err)
				return
			}
		}
	}
	if tracking == "" {
		tracking = inode.String() + ":" + strconv.FormatInt(int64(length), 10)
	}
	_ = r.rdb.ZRem(ctx, delfiles, tracking)
}

func (r *redisMeta) compactChunk(inode Ino, indx uint32, force bool) {
	// avoid too many or duplicated compaction
	if !force {
		r.Lock()
		k := uint64(inode) + (uint64(indx) << 32)
		if len(r.compacting) > 10 || r.compacting[k] {
			r.Unlock()
			return
		}
		r.compacting[k] = true
		r.Unlock()
		defer func() {
			r.Lock()
			delete(r.compacting, k)
			r.Unlock()
		}()
	}

	var ctx = Background
	vals, err := r.rdb.LRange(ctx, r.chunkKey(inode, indx), 0, 200).Result()
	if err != nil {
		return
	}
	chunkid, err := r.rdb.Incr(ctx, "nextchunk").Uint64()
	if err != nil {
		return
	}

	var ss []*slice
	var chunks []Slice
	var skipped int
	var pos, size uint32
	for skipped < len(vals) {
		// the slices will be formed as a tree after buildSlice(),
		// we should create new one (or remove the link in tree)
		ss = readSlices(vals[skipped:])
		// copy the first slice so it will not be updated by buildSlice
		first := *ss[0]
		chunks = buildSlice(ss)
		pos, size = 0, 0
		if chunks[0].Chunkid == 0 {
			pos = chunks[0].Len
			chunks = chunks[1:]
		}
		for _, s := range chunks {
			size += s.Len
		}
		if first.len < (1<<20) || first.len*5 < size {
			// it's too small
			break
		}
		isFirst := func(pos uint32, s Slice) bool {
			return pos == first.pos && s.Chunkid == first.chunkid && s.Off == first.off && s.Len == first.len
		}
		if !isFirst(pos, chunks[0]) {
			// it's not the first slice, compact it
			break
		}
		skipped++
	}
	if len(ss) < 2 {
		return
	}

	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = r.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		return
	}
	var rs []*redis.IntCmd
	key := r.chunkKey(inode, indx)
	errno := r.txn(ctx, func(tx *redis.Tx) error {
		rs = nil
		vals2, err := tx.LRange(ctx, key, 0, int64(len(vals)-1)).Result()
		if err != nil {
			return err
		}
		if len(vals2) != len(vals) {
			return syscall.EINVAL
		}
		for i, val := range vals2 {
			if val != vals[i] {
				return syscall.EINVAL
			}
		}

		_, err = tx.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.LTrim(ctx, key, int64(len(vals)), -1)
			pipe.LPush(ctx, key, marshalSlice(pos, chunkid, size, 0, size))
			for i := skipped; i > 0; i-- {
				pipe.LPush(ctx, key, vals[i-1])
			}
			pipe.HIncrBy(ctx, sliceRefs, r.sliceKey(chunkid, size), 1) // create the key to tracking it
			for _, s := range ss {
				rs = append(rs, pipe.Decr(ctx, r.sliceKey(s.chunkid, s.size)))
			}
			return nil
		})
		return err
	}, key)
	// there could be false-negative that the compaction is successful, double-check
	if errno != 0 && errno != syscall.EINVAL {
		if e := r.rdb.HGet(ctx, sliceRefs, r.sliceKey(chunkid, size)).Err(); e == redis.Nil {
			errno = syscall.EINVAL // failed
		} else if e == nil {
			errno = 0 // successful
		}
	}

	if errno == syscall.EINVAL {
		r.rdb.HIncrBy(ctx, sliceRefs, r.sliceKey(chunkid, size), -2)
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		r.deleteSlice(ctx, chunkid, size)
	} else if errno == 0 {
		// reset it to zero
		r.rdb.HIncrBy(ctx, sliceRefs, r.sliceKey(chunkid, size), -1)
		r.cleanupZeroRef(r.sliceKey(chunkid, size))
		for i, s := range ss {
			if rs[i].Err() == nil && rs[i].Val() < 0 {
				r.deleteSlice(ctx, s.chunkid, s.size)
			}
		}
		if r.rdb.LLen(ctx, r.chunkKey(inode, indx)).Val() > 5 {
			go func() {
				// wait for the current compaction to finish
				time.Sleep(time.Millisecond * 10)
				r.compactChunk(inode, indx, force)
			}()
		}
	} else {
		logger.Warnf("compact %s: %s", key, errno)
	}
}

func (r *redisMeta) CompactAll(ctx Context) syscall.Errno {
	var cursor uint64
	p := r.rdb.Pipeline()
	for {
		keys, c, err := r.rdb.Scan(ctx, cursor, "c*_*", 10000).Result()
		if err != nil {
			logger.Warnf("scan chunks: %s", err)
			return errno(err)
		}
		for _, key := range keys {
			_ = p.LLen(ctx, key)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			logger.Warnf("list slices: %s", err)
			return errno(err)
		}
		for i, cmd := range cmds {
			cnt := cmd.(*redis.IntCmd).Val()
			if cnt > 1 {
				var inode uint64
				var indx uint32
				n, err := fmt.Sscanf(keys[i], "c%d_%d", &inode, &indx)
				if err == nil && n == 2 {
					logger.Debugf("compact chunk %d:%d (%d slices)", inode, indx, cnt)
					r.compactChunk(Ino(inode), indx, true)
				}
			}
		}
		if c == 0 {
			break
		}
		cursor = c
	}
	return 0
}

func (r *redisMeta) cleanupLeakedInodes(delete bool) {
	var ctx = Background
	var keys []string
	var cursor uint64
	var err error
	var foundInodes = make(map[Ino]struct{})
	cutoff := time.Now().Add(time.Hour * -1)
	for {
		keys, cursor, err = r.rdb.Scan(ctx, cursor, "d*", 1000).Result()
		if err != nil {
			logger.Errorf("scan dentry: %s", err)
			return
		}
		if len(keys) > 0 {
			for _, key := range keys {
				ino, _ := strconv.Atoi(key[1:])
				var entries []*Entry
				eno := r.Readdir(ctx, Ino(ino), 0, &entries)
				if eno != syscall.ENOENT && eno != 0 {
					logger.Errorf("readdir %d: %s", ino, eno)
					return
				}
				for _, e := range entries {
					foundInodes[e.Inode] = struct{}{}
				}
			}
		}
		if cursor == 0 {
			break
		}
	}
	for {
		keys, cursor, err = r.rdb.Scan(ctx, cursor, "i*", 1000).Result()
		if err != nil {
			logger.Errorf("scan inodes: %s", err)
			break
		}
		if len(keys) > 0 {
			values, err := r.rdb.MGet(ctx, keys...).Result()
			if err != nil {
				logger.Warnf("mget inodes: %s", err)
				break
			}
			for i, v := range values {
				if v == nil {
					continue
				}
				var attr Attr
				r.parseAttr([]byte(v.(string)), &attr)
				ino, _ := strconv.Atoi(keys[i][1:])
				if _, ok := foundInodes[Ino(ino)]; !ok && time.Unix(attr.Atime, 0).Before(cutoff) {
					logger.Infof("found dangling inode: %s %+v", keys[i], attr)
					if delete {
						err = r.deleteInode(Ino(ino))
						if err != nil {
							logger.Errorf("delete leaked inode %d : %s", ino, err)
						}
					}
				}
			}
		}
		if cursor == 0 {
			break
		}
	}
}

func (r *redisMeta) ListSlices(ctx Context, slices *[]Slice, delete bool) syscall.Errno {
	r.cleanupLeakedInodes(delete)
	r.cleanupLeakedChunks()
	r.cleanupOldSliceRefs()
	*slices = nil
	var cursor uint64
	p := r.rdb.Pipeline()
	for {
		keys, c, err := r.rdb.Scan(ctx, cursor, "c*_*", 10000).Result()
		if err != nil {
			logger.Warnf("scan chunks: %s", err)
			return errno(err)
		}
		for _, key := range keys {
			_ = p.LRange(ctx, key, 0, 100000000)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			logger.Warnf("list slices: %s", err)
			return errno(err)
		}
		for _, cmd := range cmds {
			vals := cmd.(*redis.StringSliceCmd).Val()
			ss := readSlices(vals)
			for _, s := range ss {
				if s.chunkid > 0 {
					*slices = append(*slices, Slice{Chunkid: s.chunkid, Size: s.size})
				}
			}
		}
		if c == 0 {
			break
		}
		cursor = c
	}
	return 0
}

func (r *redisMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	var err error
	*vbuff, err = r.rdb.HGet(ctx, r.xattrKey(inode), name).Bytes()
	if err == redis.Nil {
		err = ENOATTR
	}
	return errno(err)
}

func (r *redisMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	vals, err := r.rdb.HKeys(ctx, r.xattrKey(inode)).Result()
	if err != nil {
		return errno(err)
	}
	*names = nil
	for _, name := range vals {
		*names = append(*names, []byte(name)...)
		*names = append(*names, 0)
	}
	return 0
}

func (r *redisMeta) SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno {
	_, err := r.rdb.HSet(ctx, r.xattrKey(inode), name, value).Result()
	return errno(err)
}

func (r *redisMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	n, err := r.rdb.HDel(ctx, r.xattrKey(inode), name).Result()
	if n == 0 {
		err = ENOATTR
	}
	return errno(err)
}

func (r *redisMeta) checkServerConfig() {
	rawInfo, err := r.rdb.Info(Background).Result()
	if err != nil {
		logger.Warnf("parse info: %s", err)
		return
	}
	_, err = checkRedisInfo(rawInfo)
	if err != nil {
		logger.Warnf("parse info: %s", err)
	}

	start := time.Now()
	_ = r.rdb.Ping(Background)
	logger.Infof("Ping redis: %s", time.Since(start))
}
