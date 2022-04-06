//go:build !noredis
// +build !noredis

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
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pkg/errors"

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
	  Hash Set: 4.0+
	  Transaction: 2.2+
	  Scripting: 2.6+
	  Scan: 2.8+
*/

type redisMeta struct {
	baseMeta
	rdb        redis.UniversalClient
	prefix     string
	txlocks    [1024]sync.Mutex // Pessimistic locks to reduce conflict on Redis
	shaLookup  string           // The SHA returned by Redis for the loaded `scriptLookup`
	shaResolve string           // The SHA returned by Redis for the loaded `scriptResolve`
	snap       *redisSnap
}

var _ Meta = &redisMeta{}

func init() {
	Register("redis", newRedisMeta)
	Register("rediss", newRedisMeta)
}

// newRedisMeta return a meta store using Redis.
func newRedisMeta(driver, addr string, conf *Config) (Meta, error) {
	uri := driver + "://" + addr
	var query queryMap
	if p := strings.Index(uri, "?"); p > 0 && p+1 < len(uri) {
		if q, err := url.ParseQuery(uri[p+1:]); err == nil {
			logger.Debugf("parsed query parameters: %v", q)
			query = queryMap{q}
			uri = uri[:p]
		} else {
			return nil, fmt.Errorf("parse query %s: %s", uri[p+1:], err)
		}
	}
	opt, err := redis.ParseURL(uri)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", uri, err)
	}
	if strings.Contains(opt.Addr, ",") {
		// redis.ParseURL does not support multiple hosts
		opt.Addr, _, _ = net.SplitHostPort(opt.Addr)
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("REDIS_PASSWORD")
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("META_PASSWORD")
	}
	opt.MaxRetries = conf.Retries
	opt.MinRetryBackoff = query.duration("min-retry-backoff", time.Millisecond*20)
	opt.MaxRetryBackoff = query.duration("max-retry-backoff", time.Second*10)
	opt.ReadTimeout = query.duration("read-timeout", time.Second*30)
	opt.WriteTimeout = query.duration("write-timeout", time.Second*5)
	var rdb redis.UniversalClient
	var prefix string
	if strings.Contains(opt.Addr, ",") && strings.Index(opt.Addr, ",") < strings.Index(opt.Addr, ":") {
		var fopt redis.FailoverOptions
		ps := strings.Split(opt.Addr, ",")
		fopt.MasterName = ps[0]
		fopt.SentinelAddrs = ps[1:]
		_, port, _ := net.SplitHostPort(fopt.SentinelAddrs[len(fopt.SentinelAddrs)-1])
		if port == "" {
			port = "26379"
		}
		for i, addr := range fopt.SentinelAddrs {
			h, p, e := net.SplitHostPort(addr)
			if e != nil {
				fopt.SentinelAddrs[i] = net.JoinHostPort(addr, port)
			} else if p == "" {
				fopt.SentinelAddrs[i] = net.JoinHostPort(h, port)
			}
		}
		fopt.SentinelPassword = os.Getenv("SENTINEL_PASSWORD")
		fopt.DB = opt.DB
		fopt.Username = opt.Username
		fopt.Password = opt.Password
		fopt.TLSConfig = opt.TLSConfig
		fopt.MaxRetries = opt.MaxRetries
		fopt.MinRetryBackoff = opt.MinRetryBackoff
		fopt.MaxRetryBackoff = opt.MaxRetryBackoff
		fopt.ReadTimeout = opt.ReadTimeout
		fopt.WriteTimeout = opt.WriteTimeout
		if conf.ReadOnly {
			// NOTE: RouteByLatency and RouteRandomly are not supported since they require cluster client
			fopt.SlaveOnly = query.Get("route-read") == "replica"
		}
		rdb = redis.NewFailoverClient(&fopt)
	} else {
		if !strings.Contains(opt.Addr, ",") {
			c := redis.NewClient(opt)
			info, err := c.ClusterInfo(Background).Result()
			if err != nil && strings.Contains(err.Error(), "cluster mode") || err == nil && strings.Contains(info, "cluster_state:") {
				logger.Infof("redis %s is in cluster mode", opt.Addr)
			} else {
				rdb = c
			}
		}
		if rdb == nil {
			var copt redis.ClusterOptions
			copt.Addrs = strings.Split(opt.Addr, ",")
			copt.MaxRedirects = 1
			copt.Username = opt.Username
			copt.Password = opt.Password
			copt.TLSConfig = opt.TLSConfig
			copt.MaxRetries = opt.MaxRetries
			copt.MinRetryBackoff = opt.MinRetryBackoff
			copt.MaxRetryBackoff = opt.MaxRetryBackoff
			copt.ReadTimeout = opt.ReadTimeout
			copt.WriteTimeout = opt.WriteTimeout
			if conf.ReadOnly {
				switch query.Get("route-read") {
				case "random":
					copt.RouteRandomly = true
				case "latency":
					copt.RouteByLatency = true
				case "replica":
					copt.ReadOnly = true
				default:
					// route to primary
				}
			}
			rdb = redis.NewClusterClient(&copt)
			prefix = fmt.Sprintf("{%d}", opt.DB)
		}
	}

	m := &redisMeta{
		baseMeta: newBaseMeta(conf),
		rdb:      rdb,
		prefix:   prefix,
	}
	m.en = m
	m.checkServerConfig()
	m.root, err = lookupSubdir(m, conf.Subdir)
	return m, err
}

func (r *redisMeta) Shutdown() error {
	return r.rdb.Close()
}

func (m *redisMeta) doDeleteSlice(chunkid uint64, size uint32) error {
	return m.rdb.HDel(Background, m.sliceRefs(), m.sliceKey(chunkid, size)).Err()
}

func (r *redisMeta) Name() string {
	return "redis"
}

func (r *redisMeta) Init(format Format, force bool) error {
	ctx := Background
	body, err := r.rdb.Get(ctx, r.setting()).Bytes()
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
			old.RemoveSecret()
			logger.Warnf("Existing volume will be overwrited: %+v", old)
		} else {
			format.UUID = old.UUID
			// these can be safely updated.
			old.Bucket = format.Bucket
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
			old.EncryptKey = format.EncryptKey
			old.KeyEncrypted = format.KeyEncrypted
			old.Capacity = format.Capacity
			old.Inodes = format.Inodes
			old.TrashDays = format.TrashDays
			old.MinClientVersion = format.MinClientVersion
			old.MaxClientVersion = format.MaxClientVersion
			if format != old {
				old.RemoveSecret()
				format.RemoveSecret()
				return fmt.Errorf("cannot update format from %+v to %+v", old, format)
			}
		}
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}
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
	if format.TrashDays > 0 {
		attr.Mode = 0555
		if err = r.rdb.SetNX(ctx, r.inodeKey(TrashInode), r.marshal(attr), 0).Err(); err != nil {
			return err
		}
	}
	if err = r.rdb.Set(ctx, r.setting(), data, 0).Err(); err != nil {
		return err
	}
	r.fmt = format
	if body != nil {
		return nil
	}

	// root inode
	attr.Mode = 0777
	return r.rdb.Set(ctx, r.inodeKey(1), r.marshal(attr), 0).Err()
}

func (r *redisMeta) Reset() error {
	if r.prefix != "" {
		return r.scan(Background, "*", func(keys []string) error {
			return r.rdb.Del(Background, keys...).Err()
		})
	}
	return r.rdb.FlushDB(Background).Err()
}

func (r *redisMeta) doLoad() ([]byte, error) {
	body, err := r.rdb.Get(Background, r.setting()).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return body, err
}

func (r *redisMeta) doNewSession(sinfo []byte) error {
	err := r.rdb.ZAdd(Background, r.allSessions(), &redis.Z{
		Score:  float64(r.expireTime()),
		Member: strconv.FormatUint(r.sid, 10)}).Err()
	if err != nil {
		return fmt.Errorf("set session ID %d: %s", r.sid, err)
	}
	if err = r.rdb.HSet(Background, r.sessionInfos(), r.sid, sinfo).Err(); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}

	if r.shaLookup, err = r.rdb.ScriptLoad(Background, scriptLookup).Result(); err != nil {
		logger.Warnf("load scriptLookup: %v", err)
		r.shaLookup = ""
	}
	if r.shaResolve, err = r.rdb.ScriptLoad(Background, scriptResolve).Result(); err != nil {
		logger.Warnf("load scriptResolve: %v", err)
		r.shaResolve = ""
	}

	if !r.conf.NoBGJob {
		go r.cleanupLegacies()
	}
	return nil
}

func (m *redisMeta) getCounter(name string) (int64, error) {
	v, err := m.rdb.Get(Background, m.prefix+name).Int64()
	if err == redis.Nil {
		err = nil
	}
	return v, err
}

func (m *redisMeta) incrCounter(name string, value int64) (int64, error) {
	if m.conf.ReadOnly {
		return 0, syscall.EROFS
	}
	if name == "nextInode" || name == "nextChunk" {
		// for nextinode, nextchunk
		// the current one is already used
		v, err := m.rdb.IncrBy(Background, m.prefix+strings.ToLower(name), value).Result()
		return v + 1, err
	} else if name == "nextSession" {
		name = "nextsession"
	}
	return m.rdb.IncrBy(Background, m.prefix+name, value).Result()
}

func (m *redisMeta) setIfSmall(name string, value, diff int64) (bool, error) {
	var changed bool
	err := m.txn(Background, func(tx *redis.Tx) error {
		changed = false
		old, err := tx.Get(Background, m.prefix+name).Int64()
		if err != nil && err != redis.Nil {
			return err
		}
		if old > value-diff {
			return nil
		} else {
			changed = true
			return tx.Set(Background, name, value, 0).Err()
		}
	}, name)

	return changed, err
}

func (r *redisMeta) getSession(sid string, detail bool) (*Session, error) {
	ctx := Background
	info, err := r.rdb.HGet(ctx, r.sessionInfos(), sid).Bytes()
	if err == redis.Nil { // legacy client has no info
		info = []byte("{}")
	} else if err != nil {
		return nil, fmt.Errorf("HGet sessionInfos %s: %s", sid, err)
	}
	var s Session
	if err := json.Unmarshal(info, &s); err != nil {
		return nil, fmt.Errorf("corrupted session info; json error: %s", err)
	}
	s.Sid, _ = strconv.ParseUint(sid, 10, 64)
	if detail {
		inodes, err := r.rdb.SMembers(ctx, r.sustained(s.Sid)).Result()
		if err != nil {
			return nil, fmt.Errorf("SMembers %s: %s", sid, err)
		}
		s.Sustained = make([]Ino, 0, len(inodes))
		for _, sinode := range inodes {
			inode, _ := strconv.ParseUint(sinode, 10, 64)
			s.Sustained = append(s.Sustained, Ino(inode))
		}

		locks, err := r.rdb.SMembers(ctx, r.lockedKey(s.Sid)).Result()
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
			isFlock := strings.HasPrefix(lock, r.prefix+"lockf")
			inode, _ := strconv.ParseUint(lock[len(r.prefix)+5:], 10, 64)
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
	score, err := r.rdb.ZScore(Background, r.allSessions(), key).Result()
	if err != nil {
		return nil, err
	}
	s, err := r.getSession(key, true)
	if err != nil {
		return nil, err
	}
	s.Expire = time.Unix(int64(score), 0)
	return s, nil
}

func (r *redisMeta) ListSessions() ([]*Session, error) {
	keys, err := r.rdb.ZRangeWithScores(Background, r.allSessions(), 0, -1).Result()
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
		s.Expire = time.Unix(int64(k.Score), 0)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *redisMeta) sustained(sid uint64) string {
	return r.prefix + "session" + strconv.FormatUint(sid, 10)
}

func (r *redisMeta) lockedKey(sid uint64) string {
	return r.prefix + "locked" + strconv.FormatUint(sid, 10)
}

func (r *redisMeta) symKey(inode Ino) string {
	return r.prefix + "s" + inode.String()
}

func (r *redisMeta) inodeKey(inode Ino) string {
	return r.prefix + "i" + inode.String()
}

func (r *redisMeta) entryKey(parent Ino) string {
	return r.prefix + "d" + parent.String()
}

func (r *redisMeta) chunkKey(inode Ino, indx uint32) string {
	return r.prefix + "c" + inode.String() + "_" + strconv.FormatInt(int64(indx), 10)
}

func (r *redisMeta) sliceKey(chunkid uint64, size uint32) string {
	// inside hashset
	return "k" + strconv.FormatUint(chunkid, 10) + "_" + strconv.FormatUint(uint64(size), 10)
}

func (r *redisMeta) xattrKey(inode Ino) string {
	return r.prefix + "x" + inode.String()
}

func (r *redisMeta) flockKey(inode Ino) string {
	return r.prefix + "lockf" + inode.String()
}

func (r *redisMeta) ownerKey(owner uint64) string {
	return fmt.Sprintf("%d_%016X", r.sid, owner)
}

func (r *redisMeta) plockKey(inode Ino) string {
	return r.prefix + "lockp" + inode.String()
}

func (r *redisMeta) setting() string {
	return r.prefix + "setting"
}

func (r *redisMeta) usedSpaceKey() string {
	return r.prefix + usedSpace
}

func (r *redisMeta) totalInodesKey() string {
	return r.prefix + totalInodes
}

func (r *redisMeta) delfiles() string {
	return r.prefix + "delfiles"
}

func (r *redisMeta) allSessions() string {
	return r.prefix + "allSessions"
}

func (r *redisMeta) sessionInfos() string {
	return r.prefix + "sessionInfos"
}

func (r *redisMeta) sliceRefs() string {
	return r.prefix + "sliceRef"
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

func (m *redisMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.usedSpace, space)
	atomic.AddInt64(&m.usedInodes, inodes)
}

func (r *redisMeta) handleLuaResult(op string, res interface{}, err error, returnedIno *int64, returnedAttr *string) syscall.Errno {
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "NOSCRIPT") {
			var err2 error
			switch op {
			case "lookup":
				r.shaLookup, err2 = r.rdb.ScriptLoad(Background, scriptLookup).Result()
			case "resolve":
				r.shaResolve, err2 = r.rdb.ScriptLoad(Background, scriptResolve).Result()
			default:
				return syscall.ENOTSUP
			}
			if err2 == nil {
				logger.Infof("loaded script succeed for %s", op)
				return syscall.EAGAIN
			} else {
				logger.Warnf("load script %s: %s", op, err2)
				return syscall.ENOTSUP
			}
		}

		fields := strings.Fields(msg)
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
			logger.Warnf("unexpected error for %s: %s", op, msg)
			switch op {
			case "lookup":
				r.shaLookup = ""
			case "resolve":
				r.shaResolve = ""
			}
			return syscall.ENOTSUP
		}
	}
	vals, ok := res.([]interface{})
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	*returnedIno, ok = vals[0].(int64)
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	if vals[1] == nil {
		return syscall.ENOTSUP
	}
	*returnedAttr, ok = vals[1].(string)
	if !ok {
		logger.Errorf("invalid script result: %v", res)
		return syscall.ENOTSUP
	}
	return 0
}

func (r *redisMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var foundIno Ino
	var foundType uint8
	var encodedAttr []byte
	var err error
	entryKey := r.entryKey(parent)
	if len(r.shaLookup) > 0 && attr != nil && !r.conf.CaseInsensi && r.prefix == "" {
		var res interface{}
		var returnedIno int64
		var returnedAttr string
		res, err = r.rdb.EvalSha(ctx, r.shaLookup, []string{entryKey, name}).Result()
		if st := r.handleLuaResult("lookup", res, err, &returnedIno, &returnedAttr); st == 0 {
			foundIno = Ino(returnedIno)
			encodedAttr = []byte(returnedAttr)
		} else if st == syscall.EAGAIN {
			return r.doLookup(ctx, parent, name, inode, attr)
		} else if st != syscall.ENOTSUP {
			return st
		}
	}
	if foundIno == 0 || len(encodedAttr) == 0 {
		var buf []byte
		buf, err = r.rdb.HGet(ctx, entryKey, name).Bytes()
		if err != nil {
			return errno(err)
		}
		foundType, foundIno = r.parseEntry(buf)
		encodedAttr, err = r.rdb.Get(ctx, r.inodeKey(foundIno)).Bytes()
	}

	if err == nil {
		r.parseAttr(encodedAttr, attr)
	} else if err == redis.Nil { // corrupt entry
		logger.Warnf("no attribute for inode %d (%d, %s)", foundIno, parent, name)
		*attr = Attr{Typ: foundType}
		err = nil
	}
	*inode = foundIno
	return errno(err)
}

func (r *redisMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	if len(r.shaResolve) == 0 || r.conf.CaseInsensi || r.prefix != "" {
		return syscall.ENOTSUP
	}
	defer timeit(time.Now())
	parent = r.checkRoot(parent)
	args := []string{parent.String(), path,
		strconv.FormatUint(uint64(ctx.Uid()), 10),
		strconv.FormatUint(uint64(ctx.Gid()), 10)}
	res, err := r.rdb.EvalSha(ctx, r.shaResolve, args).Result()
	var returnedIno int64
	var returnedAttr string
	st := r.handleLuaResult("resolve", res, err, &returnedIno, &returnedAttr)
	if st == 0 {
		if inode != nil {
			*inode = Ino(returnedIno)
		}
		r.parseAttr([]byte(returnedAttr), attr)
	} else if st == syscall.EAGAIN {
		return r.Resolve(ctx, parent, path, inode, attr)
	}
	return st
}

func (r *redisMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	a, err := r.rdb.Get(ctx, r.inodeKey(inode)).Bytes()
	if err == nil {
		r.parseAttr(a, attr)
	}
	return errno(err)
}

type timeoutError interface {
	Timeout() bool
}

func (r *redisMeta) shouldRetry(err error, retryOnFailure bool) bool {
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
	if s == "ERR max number of clients reached" ||
		strings.Contains(s, "Conn is in a bad state") ||
		strings.Contains(s, "EXECABORT") {
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

func (r *redisMeta) txn(ctx Context, txf func(tx *redis.Tx) error, keys ...string) error {
	if r.conf.ReadOnly {
		return syscall.EROFS
	}
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
		if r.shouldRetry(err, retryOnFailture) {
			txRestart.Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(rand.Int()%((i+1)*(i+1))))
			continue
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", err)
	return err
}

func (r *redisMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	f := r.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { r.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := r.txn(ctx, func(tx *redis.Tx) error {
		var t Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &t)
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
		if newSpace > 0 && r.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		var zeroChunks []uint32
		var left, right = t.Length, length
		if left > right {
			right, left = left, right
		}
		if (right-left)/ChunkSize >= 100 {
			// super large
			var cursor uint64
			var keys []string
			for {
				keys, cursor, err = tx.Scan(ctx, cursor, r.prefix+fmt.Sprintf("c%d_*", inode), 10000).Result()
				if err != nil {
					return err
				}
				for _, key := range keys {
					indx, err := strconv.Atoi(strings.Split(key[len(r.prefix):], "_")[1])
					if err != nil {
						logger.Errorf("parse %s: %s", key, err)
						continue
					}
					if uint64(indx) > left/ChunkSize && uint64(indx) < right/ChunkSize {
						zeroChunks = append(zeroChunks, uint32(indx))
					}
				}
				if cursor <= 0 {
					break
				}
			}
		} else {
			for i := left/ChunkSize + 1; i < right/ChunkSize; i++ {
				zeroChunks = append(zeroChunks, uint32(i))
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
			// zero out from left to right
			var l = uint32(right - left)
			if right > (left/ChunkSize+1)*ChunkSize {
				l = ChunkSize - uint32(left%ChunkSize)
			}
			pipe.RPush(ctx, r.chunkKey(inode, uint32(left/ChunkSize)), marshalSlice(uint32(left%ChunkSize), 0, 0, 0, l))
			buf := marshalSlice(0, 0, 0, 0, ChunkSize)
			for _, indx := range zeroChunks {
				pipe.RPushX(ctx, r.chunkKey(inode, indx), buf)
			}
			if right > (left/ChunkSize+1)*ChunkSize && right%ChunkSize > 0 {
				pipe.RPush(ctx, r.chunkKey(inode, uint32(right/ChunkSize)), marshalSlice(0, 0, 0, 0, uint32(right%ChunkSize)))
			}
			pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
			return nil
		})
		if err == nil {
			if attr != nil {
				*attr = t
			}
		}
		return err
	}, r.inodeKey(inode))
	if err == nil {
		r.updateStats(newSpace, 0)
	}
	return errno(err)
}

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
	defer timeit(time.Now())
	f := r.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { r.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
	var newSpace int64
	err := r.txn(ctx, func(tx *redis.Tx) error {
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
		newSpace = align4K(length) - align4K(old)
		if newSpace > 0 && r.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
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
			pipe.IncrBy(ctx, r.usedSpaceKey(), align4K(length)-align4K(old))
			return nil
		})
		return err
	}, r.inodeKey(inode))
	if err == nil {
		r.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (r *redisMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	inode = r.checkRoot(inode)
	defer func() { r.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	return errno(r.txn(ctx, func(tx *redis.Tx) error {
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
	}, r.inodeKey(inode)))
}

func (m *redisMeta) doReadlink(ctx Context, inode Ino) ([]byte, error) {
	return m.rdb.Get(ctx, m.symKey(inode)).Bytes()
}

func (r *redisMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	var ino Ino
	var err error
	if parent == TrashInode {
		var next int64
		next, err = r.incrCounter("nextTrash", 1)
		ino = TrashInode + Ino(next)
	} else {
		ino, err = r.nextInode()
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

	err = r.txn(ctx, func(tx *redis.Tx) error {
		var pattr Attr
		a, err := tx.Get(ctx, r.inodeKey(parent)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}

		buf, err := tx.HGet(ctx, r.entryKey(parent), name).Bytes()
		if err != nil && err != redis.Nil {
			return err
		}
		var foundIno Ino
		var foundType uint8
		if err == nil {
			foundType, foundIno = r.parseEntry(buf)
		} else if r.conf.CaseInsensi { // err == redis.Nil
			if entry := r.resolveCase(ctx, parent, name); entry != nil {
				foundType, foundIno = entry.Attr.Typ, entry.Inode
			}
		}
		if foundIno != 0 {
			if _type == TypeFile || _type == TypeDirectory { // file for create, directory for subTrash
				a, err = tx.Get(ctx, r.inodeKey(foundIno)).Bytes()
				if err == nil {
					r.parseAttr(a, attr)
				} else if err == redis.Nil {
					*attr = Attr{Typ: foundType, Parent: parent} // corrupt entry
				} else {
					return err
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, r.entryKey(parent), name, r.packEntry(_type, ino))
			if parent != TrashInode {
				pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			}
			pipe.Set(ctx, r.inodeKey(ino), r.marshal(attr), 0)
			if _type == TypeSymlink {
				pipe.Set(ctx, r.symKey(ino), path, 0)
			}
			pipe.IncrBy(ctx, r.usedSpaceKey(), align4K(0))
			pipe.Incr(ctx, r.totalInodesKey())
			return nil
		})
		return err
	}, r.inodeKey(parent), r.entryKey(parent))
	if err == nil {
		r.updateStats(align4K(0), 1)
	}
	return errno(err)
}

func (r *redisMeta) doUnlink(ctx Context, parent Ino, name string) syscall.Errno {
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
	keys := []string{r.entryKey(parent), r.inodeKey(parent), r.inodeKey(inode)}
	var trash Ino
	if st := r.checkTrash(parent, &trash); st != 0 {
		return st
	}
	if trash > 0 {
		keys = append(keys, r.entryKey(trash))
	} else {
		defer func() { r.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
	}
	var opened bool
	var attr Attr
	var newSpace, newInode int64
	err = r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
		if rs[0] == nil {
			return redis.Nil
		}
		var pattr Attr
		r.parseAttr([]byte(rs[0].(string)), &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		now := time.Now()
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())
		attr = Attr{}
		opened = false
		if rs[1] != nil {
			r.parseAttr([]byte(rs[1].(string)), &attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			attr.Ctime = now.Unix()
			attr.Ctimensec = uint32(now.Nanosecond())
			if trash == 0 {
				attr.Nlink--
				if _type == TypeFile && attr.Nlink == 0 {
					opened = r.of.IsOpen(inode)
				}
			} else if attr.Nlink == 1 { // don't change parent if it has hard links
				attr.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
			trash = 0
		}

		buf, err := tx.HGet(ctx, r.entryKey(parent), name).Bytes()
		if err != nil {
			return err
		}
		_type2, inode2 := r.parseEntry(buf)
		if _type2 != _type || inode2 != inode {
			return syscall.EAGAIN
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parent), name)
			if !isTrash(parent) {
				pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			}
			if attr.Nlink > 0 {
				pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
				if trash > 0 {
					pipe.HSet(ctx, r.entryKey(trash), fmt.Sprintf("%d-%d-%s", parent, inode, name), buf)
				}
			} else {
				switch _type {
				case TypeFile:
					if opened {
						pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
						pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(inode)))
					} else {
						pipe.ZAdd(ctx, r.delfiles(), &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(inode, attr.Length)})
						pipe.Del(ctx, r.inodeKey(inode))
						newSpace, newInode = -align4K(attr.Length), -1
						pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
						pipe.Decr(ctx, r.totalInodesKey())
					}
				case TypeSymlink:
					pipe.Del(ctx, r.symKey(inode))
					fallthrough
				default:
					pipe.Del(ctx, r.inodeKey(inode))
					newSpace, newInode = -align4K(0), -1
					pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
					pipe.Decr(ctx, r.totalInodesKey())
				}
				pipe.Del(ctx, r.xattrKey(inode))
			}
			return nil
		})

		return err
	}, keys...)
	if err == nil && trash == 0 {
		if _type == TypeFile && attr.Nlink == 0 {
			r.fileDeleted(opened, inode, attr.Length)
		}
		r.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (r *redisMeta) doRmdir(ctx Context, parent Ino, name string) syscall.Errno {
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

	keys := []string{r.inodeKey(parent), r.entryKey(parent), r.inodeKey(inode), r.entryKey(inode)}
	var trash Ino
	if st := r.checkTrash(parent, &trash); st != 0 {
		return st
	}
	if trash > 0 {
		keys = append(keys, r.entryKey(trash))
	}
	err = r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
		if rs[0] == nil {
			return redis.Nil
		}
		var pattr, attr Attr
		r.parseAttr([]byte(rs[0].(string)), &pattr)
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
		if rs[1] != nil {
			r.parseAttr([]byte(rs[1].(string)), &attr)
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parent), name)
			if !isTrash(parent) {
				pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			}
			if trash > 0 {
				pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
				pipe.HSet(ctx, r.entryKey(trash), fmt.Sprintf("%d-%d-%s", parent, inode, name), buf)
			} else {
				pipe.Del(ctx, r.inodeKey(inode))
				pipe.Del(ctx, r.xattrKey(inode))
				pipe.IncrBy(ctx, r.usedSpaceKey(), -align4K(0))
				pipe.Decr(ctx, r.totalInodesKey())
			}
			return nil
		})
		return err
	}, keys...)
	if err == nil && trash == 0 {
		r.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (r *redisMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	exchange := flags == RenameExchange
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
	var opened bool
	var trash, dino Ino
	var dtyp uint8
	var tattr Attr
	if err == nil {
		if st := r.checkTrash(parentDst, &trash); st != 0 {
			return st
		}
		if trash > 0 {
			keys = append(keys, r.entryKey(trash))
		}
		dtyp, dino = r.parseEntry(buf)
		keys = append(keys, r.inodeKey(dino))
		if dtyp == TypeDirectory {
			keys = append(keys, r.entryKey(dino))
		}
	}
	var newSpace, newInode int64
	err = r.txn(ctx, func(tx *redis.Tx) error {
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

		dbuf, err := tx.HGet(ctx, r.entryKey(parentDst), nameDst).Bytes()
		if err != nil && err != redis.Nil {
			return err
		}
		now := time.Now()
		tattr = Attr{}
		opened = false
		if err == nil {
			if flags == RenameNoReplace {
				return syscall.EEXIST
			}
			dtyp1, dino1 := r.parseEntry(dbuf)
			if dino1 != dino || dtyp1 != dtyp {
				return syscall.EAGAIN
			}
			a, err := tx.Get(ctx, r.inodeKey(dino)).Bytes()
			if err == redis.Nil {
				logger.Warnf("no attribute for inode %d (%d, %s)", dino, parentDst, nameDst)
				trash = 0
			} else if err != nil {
				return err
			}
			r.parseAttr(a, &tattr)
			tattr.Ctime = now.Unix()
			tattr.Ctimensec = uint32(now.Nanosecond())
			if exchange {
				tattr.Parent = parentSrc
				if dtyp == TypeDirectory && parentSrc != parentDst {
					dattr.Nlink--
					sattr.Nlink++
				}
			} else {
				if dtyp == TypeDirectory {
					cnt, err := tx.HLen(ctx, r.entryKey(dino)).Result()
					if err != nil {
						return err
					}
					if cnt != 0 {
						return syscall.ENOTEMPTY
					}
					dattr.Nlink--
					if trash > 0 {
						tattr.Parent = trash
					}
				} else {
					if trash == 0 {
						tattr.Nlink--
						if dtyp == TypeFile && tattr.Nlink == 0 {
							opened = r.of.IsOpen(dino)
						}
						defer func() { r.of.InvalidateChunk(dino, 0xFFFFFFFE) }()
					} else if tattr.Nlink == 1 {
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
			dino, dtyp = 0, 0
		}
		buf, err := tx.HGet(ctx, r.entryKey(parentSrc), nameSrc).Bytes()
		if err != nil {
			return err
		}
		typ1, ino1 := r.parseEntry(buf)
		if ino1 != ino || typ1 != typ {
			return syscall.EAGAIN
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
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			if exchange { // dbuf, tattr are valid
				pipe.HSet(ctx, r.entryKey(parentSrc), nameSrc, dbuf)
				pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
			} else {
				pipe.HDel(ctx, r.entryKey(parentSrc), nameSrc)
				if dino > 0 {
					if trash > 0 {
						pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
						pipe.HSet(ctx, r.entryKey(trash), fmt.Sprintf("%d-%d-%s", parentDst, dino, nameDst), dbuf)
					} else if dtyp != TypeDirectory && tattr.Nlink > 0 {
						pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
					} else {
						if dtyp == TypeFile {
							if opened {
								pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
								pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(dino)))
							} else {
								pipe.ZAdd(ctx, r.delfiles(), &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(dino, tattr.Length)})
								pipe.Del(ctx, r.inodeKey(dino))
								newSpace, newInode = -align4K(tattr.Length), -1
								pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
								pipe.Decr(ctx, r.totalInodesKey())
							}
						} else {
							if dtyp == TypeSymlink {
								pipe.Del(ctx, r.symKey(dino))
							}
							pipe.Del(ctx, r.inodeKey(dino))
							newSpace, newInode = -align4K(0), -1
							pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
							pipe.Decr(ctx, r.totalInodesKey())
						}
						pipe.Del(ctx, r.xattrKey(dino))
					}
				}
			}
			if parentDst != parentSrc && !isTrash(parentSrc) {
				pipe.Set(ctx, r.inodeKey(parentSrc), r.marshal(&sattr), 0)
			}
			pipe.Set(ctx, r.inodeKey(ino), r.marshal(&iattr), 0)
			pipe.HSet(ctx, r.entryKey(parentDst), nameDst, buf)
			pipe.Set(ctx, r.inodeKey(parentDst), r.marshal(&dattr), 0)
			return nil
		})
		return err
	}, keys...)
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dtyp == TypeFile && tattr.Nlink == 0 {
			r.fileDeleted(opened, dino, tattr.Length)
		}
		r.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (r *redisMeta) doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return errno(r.txn(ctx, func(tx *redis.Tx) error {
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
		} else if err == redis.Nil && r.conf.CaseInsensi && r.resolveCase(ctx, parent, name) != nil {
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
	}, r.inodeKey(inode), r.entryKey(parent), r.inodeKey(parent)))
}

func (r *redisMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
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
			typ, ino := r.parseEntry([]byte(keys[i+1]))
			if keys[i] == "" {
				logger.Errorf("Corrupt entry with empty name: inode %d parent %d", ino, inode)
				continue
			}
			ent := &newEntries[i/2]
			ent.Inode = ino
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

func (r *redisMeta) doCleanStaleSession(sid uint64) error {
	var fail bool
	// release locks
	var ctx = Background
	ssid := strconv.FormatInt(int64(sid), 10)
	key := r.lockedKey(sid)
	if inodes, err := r.rdb.SMembers(ctx, key).Result(); err == nil {
		for _, k := range inodes {
			owners, err := r.rdb.HKeys(ctx, k).Result()
			if err != nil {
				logger.Warnf("HKeys %s: %s", k, err)
				fail = true
				continue
			}
			var fields []string
			for _, o := range owners {
				if strings.Split(o, "_")[0] == ssid {
					fields = append(fields, o)
				}
			}
			if len(fields) > 0 {
				if err = r.rdb.HDel(ctx, k, fields...).Err(); err != nil {
					logger.Warnf("HDel %s %s: %s", k, fields, err)
					fail = true
					continue
				}
			}
			if err = r.rdb.SRem(ctx, key, k).Err(); err != nil {
				logger.Warnf("SRem %s %s: %s", key, k, err)
				fail = true
			}
		}
	} else {
		logger.Warnf("SMembers %s: %s", key, err)
		fail = true
	}

	key = r.sustained(sid)
	if inodes, err := r.rdb.SMembers(ctx, key).Result(); err == nil {
		for _, sinode := range inodes {
			inode, _ := strconv.ParseInt(sinode, 10, 0)
			if err = r.doDeleteSustainedInode(sid, Ino(inode)); err != nil {
				logger.Warnf("Delete sustained inode %d of sid %d: %s", inode, sid, err)
				fail = true
			}
		}
	} else {
		logger.Warnf("SMembers %s: %s", key, err)
		fail = true
	}

	if !fail {
		if err := r.rdb.HDel(ctx, r.sessionInfos(), ssid).Err(); err != nil {
			logger.Warnf("HDel sessionInfos %s: %s", ssid, err)
			fail = true
		}
	}
	if fail {
		return fmt.Errorf("failed to clean up sid %d", sid)
	} else {
		return r.rdb.ZRem(ctx, r.allSessions(), ssid).Err()
	}
}

func (r *redisMeta) doFindStaleSessions(limit int) ([]uint64, error) {
	vals, err := r.rdb.ZRangeByScore(Background, r.allSessions(), &redis.ZRangeBy{
		Max:   strconv.FormatInt(time.Now().Unix(), 10),
		Count: int64(limit)}).Result()
	if err != nil {
		return nil, err
	}
	sids := make([]uint64, len(vals))
	for i, v := range vals {
		sids[i], _ = strconv.ParseUint(v, 10, 64)
	}
	return sids, nil
}

func (r *redisMeta) doRefreshSession() {
	r.rdb.ZAdd(Background, r.allSessions(), &redis.Z{
		Score:  float64(r.expireTime()),
		Member: strconv.FormatUint(r.sid, 10)})
}

func (r *redisMeta) doDeleteSustainedInode(sid uint64, inode Ino) error {
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
	var newSpace int64
	_, err = r.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, r.delfiles(), &redis.Z{Score: float64(time.Now().Unix()), Member: r.toDelete(inode, attr.Length)})
		pipe.Del(ctx, r.inodeKey(inode))
		newSpace = -align4K(attr.Length)
		pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
		pipe.Decr(ctx, r.totalInodesKey())
		pipe.SRem(ctx, r.sustained(sid), strconv.Itoa(int(inode)))
		return nil
	})
	if err == nil {
		r.updateStats(newSpace, -1)
		go r.doDeleteFileData(inode, attr.Length)
	}
	return err
}

func (r *redisMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	f := r.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if cs, ok := r.of.ReadChunk(inode, indx); ok {
		*chunks = cs
		return 0
	}
	defer timeit(time.Now())
	vals, err := r.rdb.LRange(ctx, r.chunkKey(inode, indx), 0, 1000000).Result()
	if err != nil {
		return errno(err)
	}
	ss := readSlices(vals)
	*chunks = buildSlice(ss)
	r.of.CacheChunk(inode, indx, *chunks)
	if !r.conf.ReadOnly && (len(vals) >= 5 || len(*chunks) >= 5) {
		go r.compactChunk(inode, indx, false)
	}
	return 0
}

func (r *redisMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	defer timeit(time.Now())
	f := r.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { r.of.InvalidateChunk(inode, indx) }()
	var newSpace int64
	var needCompact bool
	err := r.txn(ctx, func(tx *redis.Tx) error {
		var attr Attr
		a, err := tx.Get(ctx, r.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		r.parseAttr(a, &attr)
		if attr.Typ != TypeFile {
			return syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		if newleng > attr.Length {
			newSpace = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if r.checkQuota(newSpace, 0) {
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
			if newSpace > 0 {
				pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
			}
			return nil
		})
		if err == nil {
			needCompact = rpush.Val()%100 == 99
		}
		return err
	}, r.inodeKey(inode))
	if err == nil {
		if needCompact {
			go r.compactChunk(inode, indx, false)
		}
		r.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (r *redisMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	defer timeit(time.Now())
	f := r.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	var newSpace int64
	defer func() { r.of.InvalidateChunk(fout, 0xFFFFFFFF) }()
	err := r.txn(ctx, func(tx *redis.Tx) error {
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
		if newleng > attr.Length {
			newSpace = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if r.checkQuota(newSpace, 0) {
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
								pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(s.Chunkid, s.Size), 1)
							}

							skip := ChunkSize - dpos
							pipe.RPush(ctx, r.chunkKey(fout, indx+1), marshalSlice(0, s.Chunkid, s.Size, s.Off+skip, s.Len-skip))
							if s.Chunkid > 0 {
								pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(s.Chunkid, s.Size), 1)
							}
						} else {
							pipe.RPush(ctx, r.chunkKey(fout, indx), marshalSlice(dpos, s.Chunkid, s.Size, s.Off, s.Len))
							if s.Chunkid > 0 {
								pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(s.Chunkid, s.Size), 1)
							}
						}
					}
				}
				coff += ChunkSize
			}
			pipe.Set(ctx, r.inodeKey(fout), r.marshal(&attr), 0)
			if newSpace > 0 {
				pipe.IncrBy(ctx, r.usedSpaceKey(), newSpace)
			}
			return nil
		})
		if err == nil {
			*copied = size
		}
		return err
	}, r.inodeKey(fout), r.inodeKey(fin))
	if err == nil {
		r.updateStats(newSpace, 0)
	}
	return errno(err)
}

// For now only deleted files
func (r *redisMeta) cleanupLegacies() {
	for {
		utils.SleepWithJitter(time.Minute)
		rng := &redis.ZRangeBy{Max: strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10), Count: 1000}
		vals, err := r.rdb.ZRangeByScore(Background, r.delfiles(), rng).Result()
		if err != nil {
			continue
		}
		var count int
		for _, v := range vals {
			ps := strings.Split(v, ":")
			if len(ps) != 2 {
				inode, _ := strconv.ParseUint(ps[0], 10, 64)
				var length uint64 = 1 << 30
				if len(ps) > 2 {
					length, _ = strconv.ParseUint(ps[2], 10, 64)
				}
				logger.Infof("cleanup legacy delfile inode %d with %d bytes (%s)", inode, length, v)
				r.doDeleteFileData_(Ino(inode), length, v)
				count++
			}
		}
		if count == 0 {
			return
		}
	}
}

func (r *redisMeta) doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) {
	rng := &redis.ZRangeBy{Max: strconv.FormatInt(ts, 10), Count: int64(limit)}
	vals, err := r.rdb.ZRangeByScore(Background, r.delfiles(), rng).Result()
	if err != nil {
		return nil, err
	}
	files := make(map[Ino]uint64, len(vals))
	for _, v := range vals {
		ps := strings.Split(v, ":")
		if len(ps) != 2 { // will be cleaned up as legacy
			continue
		}
		inode, _ := strconv.ParseUint(ps[0], 10, 64)
		files[Ino(inode)], _ = strconv.ParseUint(ps[1], 10, 64)
	}
	return files, nil
}

func (r *redisMeta) doCleanupSlices() {
	var ctx = Background
	var ckeys []string
	var cursor uint64
	var err error
	for {
		ckeys, cursor, err = r.rdb.HScan(ctx, r.sliceRefs(), cursor, "*", 1000).Result()
		if err != nil {
			logger.Errorf("scan slices: %s", err)
			break
		}
		if len(ckeys) > 0 {
			values, err := r.rdb.HMGet(ctx, r.sliceRefs(), ckeys...).Result()
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
						chunkid, _ := strconv.ParseUint(ps[0][1:], 10, 64)
						size, _ := strconv.ParseUint(ps[1], 10, 32)
						if chunkid > 0 && size > 0 {
							r.deleteSlice(chunkid, uint32(size))
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

func (r *redisMeta) cleanupZeroRef(key string) {
	var ctx = Background
	_ = r.txn(ctx, func(tx *redis.Tx) error {
		v, err := tx.HGet(ctx, r.sliceRefs(), key).Int()
		if err != nil {
			return err
		}
		if v != 0 {
			return syscall.EINVAL
		}
		_, err = tx.Pipelined(ctx, func(p redis.Pipeliner) error {
			p.HDel(ctx, r.sliceRefs(), key)
			return nil
		})
		return err
	}, r.sliceRefs())
}

func (r *redisMeta) cleanupLeakedChunks() {
	var ctx = Background
	_ = r.scan(ctx, "c*", func(ckeys []string) error {
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
			_, err := p.Exec(ctx)
			if err != nil {
				logger.Errorf("check inodes: %s", err)
				return err
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
		return nil
	})
}

func (r *redisMeta) cleanupOldSliceRefs() {
	var ctx = Background
	_ = r.scan(ctx, "k*", func(ckeys []string) error {
		values, err := r.rdb.MGet(ctx, ckeys...).Result()
		if err != nil {
			logger.Warnf("mget slices: %s", err)
			return err
		}
		var todel []string
		for i, v := range values {
			if v == nil {
				continue
			}
			if strings.HasPrefix(v.(string), r.prefix+"-") || v == "0" { // < 0
				// the objects will be deleted by gc
				todel = append(todel, ckeys[i])
			} else {
				vv, _ := strconv.Atoi(v.(string))
				r.rdb.HIncrBy(ctx, r.sliceRefs(), ckeys[i], int64(vv))
				r.rdb.DecrBy(ctx, ckeys[i], int64(vv))
				logger.Infof("move refs %d for slice %s", vv, ckeys[i])
			}
		}
		r.rdb.Del(ctx, todel...)
		return nil
	})
}

func (r *redisMeta) toDelete(inode Ino, length uint64) string {
	return inode.String() + ":" + strconv.Itoa(int(length))
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
					rs = append(rs, pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(chunkid, size), -1))
				}
				return nil
			})
			return err
		}, key)
		if err != nil {
			return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", key, err)
		}
		for i, s := range slices {
			if rs[i].Val() < 0 {
				r.deleteSlice(s.chunkid, s.size)
			}
		}
		if len(slices) < 100 {
			break
		}
	}
	return nil
}

func (r *redisMeta) doDeleteFileData(inode Ino, length uint64) {
	r.doDeleteFileData_(inode, length, "")
}

func (r *redisMeta) doDeleteFileData_(inode Ino, length uint64, tracking string) {
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
			idx, _ := strconv.Atoi(strings.Split(keys[i][len(r.prefix):], "_")[1])
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
	_ = r.rdb.ZRem(ctx, r.delfiles(), tracking)
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
	vals, err := r.rdb.LRange(ctx, r.chunkKey(inode, indx), 0, 1000).Result()
	if err != nil {
		return
	}

	ss := readSlices(vals)
	skipped := skipSome(ss)
	ss = ss[skipped:]
	pos, size, chunks := compactChunk(ss)
	if len(ss) < 2 || size == 0 {
		return
	}

	var chunkid uint64
	st := r.NewChunk(ctx, &chunkid)
	if st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = r.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		if !strings.Contains(err.Error(), "not exist") && !strings.Contains(err.Error(), "not found") {
			logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		}
		return
	}
	var rs []*redis.IntCmd
	key := r.chunkKey(inode, indx)
	errno := errno(r.txn(ctx, func(tx *redis.Tx) error {
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
			pipe.HSet(ctx, r.sliceRefs(), r.sliceKey(chunkid, size), "0") // create the key to tracking it
			for _, s := range ss {
				rs = append(rs, pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(s.chunkid, s.size), -1))
			}
			return nil
		})
		return err
	}, key))
	// there could be false-negative that the compaction is successful, double-check
	if errno != 0 && errno != syscall.EINVAL {
		if e := r.rdb.HGet(ctx, r.sliceRefs(), r.sliceKey(chunkid, size)).Err(); e == redis.Nil {
			errno = syscall.EINVAL // failed
		} else if e == nil {
			errno = 0 // successful
		}
	}

	if errno == syscall.EINVAL {
		r.rdb.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(chunkid, size), -1)
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		r.deleteSlice(chunkid, size)
	} else if errno == 0 {
		r.of.InvalidateChunk(inode, indx)
		r.cleanupZeroRef(r.sliceKey(chunkid, size))
		for i, s := range ss {
			if rs[i].Err() == nil && rs[i].Val() < 0 {
				r.deleteSlice(s.chunkid, s.size)
			}
		}
	} else {
		logger.Warnf("compact %s: %s", key, errno)
	}

	if force {
		r.compactChunk(inode, indx, force)
	} else {
		go func() {
			// wait for the current compaction to finish
			time.Sleep(time.Millisecond * 10)
			r.compactChunk(inode, indx, force)
		}()
	}
}

func (r *redisMeta) CompactAll(ctx Context, bar *utils.Bar) syscall.Errno {
	p := r.rdb.Pipeline()
	return errno(r.scan(ctx, "c*_*", func(keys []string) error {
		bar.IncrTotal(int64(len(keys)))
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
			bar.Increment()
		}
		return nil
	}))
}

func (r *redisMeta) cleanupLeakedInodes(delete bool) {
	var ctx = Background
	var foundInodes = make(map[Ino]struct{})
	cutoff := time.Now().Add(time.Hour * -1)

	_ = r.scan(ctx, "d*", func(keys []string) error {
		for _, key := range keys {
			ino, _ := strconv.Atoi(key[1:])
			var entries []*Entry
			eno := r.Readdir(ctx, Ino(ino), 0, &entries)
			if eno != syscall.ENOENT && eno != 0 {
				logger.Errorf("readdir %d: %s", ino, eno)
				return eno
			}
			for _, e := range entries {
				foundInodes[e.Inode] = struct{}{}
			}
		}
		return nil
	})
	_ = r.scan(ctx, "i*", func(keys []string) error {
		values, err := r.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			logger.Warnf("mget inodes: %s", err)
			return nil
		}
		for i, v := range values {
			if v == nil {
				continue
			}
			var attr Attr
			r.parseAttr([]byte(v.(string)), &attr)
			ino, _ := strconv.Atoi(keys[i][1:])
			if _, ok := foundInodes[Ino(ino)]; !ok && time.Unix(attr.Ctime, 0).Before(cutoff) {
				logger.Infof("found dangling inode: %s %+v", keys[i], attr)
				if delete {
					err = r.doDeleteSustainedInode(0, Ino(ino))
					if err != nil {
						logger.Errorf("delete leaked inode %d : %s", ino, err)
					}
				}
			}
		}
		return nil
	})
}

func (r *redisMeta) scan(ctx context.Context, pattern string, f func([]string) error) error {
	var rdb *redis.Client
	if c, ok := r.rdb.(*redis.ClusterClient); ok {
		var err error
		rdb, err = c.MasterForKey(ctx, r.prefix)
		if err != nil {
			return err
		}
	} else {
		rdb = r.rdb.(*redis.Client)
	}
	var cursor uint64
	for {
		keys, c, err := rdb.Scan(ctx, cursor, r.prefix+pattern, 10000).Result()
		if err != nil {
			logger.Warnf("scan %s: %s", pattern, err)
			return err
		}
		if len(keys) > 0 {
			err = f(keys)
			if err != nil {
				return err
			}
		}
		if c == 0 {
			break
		}
		cursor = c
	}
	return nil
}

func (r *redisMeta) ListSlices(ctx Context, slices map[Ino][]Slice, delete bool, showProgress func()) syscall.Errno {
	r.cleanupLeakedInodes(delete)
	r.cleanupLeakedChunks()
	r.cleanupOldSliceRefs()
	if delete {
		r.doCleanupSlices()
	}

	p := r.rdb.Pipeline()
	return errno(r.scan(ctx, "c*_*", func(keys []string) error {
		for _, key := range keys {
			_ = p.LRange(ctx, key, 0, 100000000)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			logger.Warnf("list slices: %s", err)
			return errno(err)
		}
		for _, cmd := range cmds {
			key := cmd.(*redis.StringSliceCmd).Args()[1].(string)
			inode, _ := strconv.Atoi(strings.Split(key[len(r.prefix)+1:], "_")[0])
			vals := cmd.(*redis.StringSliceCmd).Val()
			ss := readSlices(vals)
			for _, s := range ss {
				if s.chunkid > 0 {
					slices[Ino(inode)] = append(slices[Ino(inode)], Slice{Chunkid: s.chunkid, Size: s.size})
					if showProgress != nil {
						showProgress()
					}
				}
			}
		}
		return nil
	}))
}

func (r *redisMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer timeit(time.Now())
	inode = r.checkRoot(inode)
	var err error
	*vbuff, err = r.rdb.HGet(ctx, r.xattrKey(inode), name).Bytes()
	if err == redis.Nil {
		err = ENOATTR
	}
	return errno(err)
}

func (r *redisMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	defer timeit(time.Now())
	inode = r.checkRoot(inode)
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

func (r *redisMeta) doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	c := Background
	key := r.xattrKey(inode)
	return errno(r.txn(ctx, func(tx *redis.Tx) error {
		switch flags {
		case XattrCreate:
			ok, err := tx.HSetNX(c, key, name, value).Result()
			if err != nil {
				return err
			}
			if !ok {
				return syscall.EEXIST
			}
			return nil
		case XattrReplace:
			if ok, err := tx.HExists(c, key, name).Result(); err != nil {
				return err
			} else if !ok {
				return ENOATTR
			}
			_, err := r.rdb.HSet(ctx, key, name, value).Result()
			return err
		default: // XattrCreateOrReplace
			_, err := r.rdb.HSet(ctx, key, name, value).Result()
			return err
		}
	}, key))
}

func (r *redisMeta) doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	n, err := r.rdb.HDel(ctx, r.xattrKey(inode), name).Result()
	if err != nil {
		return errno(err)
	} else if n == 0 {
		return ENOATTR
	} else {
		return 0
	}
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

func (m *redisMeta) dumpEntry(inode Ino, typ uint8) (*DumpedEntry, error) {
	ctx := Background
	e := &DumpedEntry{}
	return e, m.txn(ctx, func(tx *redis.Tx) error {
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			if err != redis.Nil {
				return err
			}
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
		}
		attr := &Attr{Typ: typ, Nlink: 1}
		m.parseAttr(a, attr)
		e.Attr = dumpAttr(attr)
		e.Attr.Inode = inode

		keys, err := tx.HGetAll(ctx, m.xattrKey(inode)).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			xattrs := make([]*DumpedXattr, 0, len(keys))
			for k, v := range keys {
				xattrs = append(xattrs, &DumpedXattr{k, v})
			}
			sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
			e.Xattrs = xattrs
		}

		if attr.Typ == TypeFile {
			for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
				vals, err := tx.LRange(ctx, m.chunkKey(inode, indx), 0, 1000000).Result()
				if err != nil {
					return err
				}
				ss := readSlices(vals)
				slices := make([]*DumpedSlice, 0, len(ss))
				for _, s := range ss {
					slices = append(slices, &DumpedSlice{Chunkid: s.chunkid, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
				}
				e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
			}
		} else if attr.Typ == TypeSymlink {
			if e.Symlink, err = tx.Get(ctx, m.symKey(inode)).Result(); err != nil {
				if err != redis.Nil {
					return err
				}
				logger.Warnf("The symlink of inode %d is not found", inode)
			}
		}
		return nil
	}, m.inodeKey(inode))
}

func (m *redisMeta) dumpEntryFast(inode Ino, typ uint8) *DumpedEntry {
	e := &DumpedEntry{}
	a := []byte(m.snap.stringMap[m.inodeKey(inode)])
	if len(a) == 0 {
		if inode != TrashInode {
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
		}
	}
	attr := &Attr{Typ: typ, Nlink: 1}
	m.parseAttr(a, attr)
	e.Attr = dumpAttr(attr)
	e.Attr.Inode = inode

	keys := m.snap.hashMap[m.xattrKey(inode)]
	if len(keys) > 0 {
		xattrs := make([]*DumpedXattr, 0, len(keys))
		for k, v := range keys {
			xattrs = append(xattrs, &DumpedXattr{k, v})
		}
		sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
		e.Xattrs = xattrs
	}

	if attr.Typ == TypeFile {
		for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
			vals := m.snap.listMap[m.chunkKey(inode, indx)]
			ss := readSlices(vals)
			slices := make([]*DumpedSlice, 0, len(ss))
			for _, s := range ss {
				slices = append(slices, &DumpedSlice{Chunkid: s.chunkid, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
			}
			e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
		}
	} else if attr.Typ == TypeSymlink {
		if m.snap.stringMap[m.symKey(inode)] == "" {
			logger.Warnf("The symlink of inode %d is not found", inode)
		}
		e.Symlink = m.snap.stringMap[m.symKey(inode)]
	}
	return e
}

func (m *redisMeta) dumpDir(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	var err error
	var dirs map[string]string
	if m.snap != nil {
		dirs = m.snap.hashMap[m.entryKey(inode)]
	} else {
		dirs, err = m.rdb.HGetAll(context.Background(), m.entryKey(inode)).Result()
		if err != nil {
			return err
		}
	}

	if showProgress != nil {
		showProgress(int64(len(dirs)), 0)
	}
	if err = tree.writeJsonWithOutEntry(bw, depth); err != nil {
		return err
	}
	var sortedName []string
	for name := range dirs {
		sortedName = append(sortedName, name)
	}
	sort.Slice(sortedName, func(i, j int) bool { return sortedName[i] < sortedName[j] })
	for idx, name := range sortedName {
		typ, inode := m.parseEntry([]byte(dirs[name]))
		var entry *DumpedEntry
		if m.snap != nil {
			entry = m.dumpEntryFast(inode, typ)
		} else {
			entry, err = m.dumpEntry(inode, typ)
			if err != nil {
				return err
			}
		}
		if entry == nil {
			continue
		}

		entry.Name = name
		if typ == TypeDirectory {
			err = m.dumpDir(inode, entry, bw, depth+2, showProgress)
		} else {
			err = entry.writeJSON(bw, depth+2)
		}
		if err != nil {
			return err
		}
		if idx != len(sortedName)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

type redisSnap struct {
	stringMap map[string]string            //i* s*
	listMap   map[string][]string          //c*
	hashMap   map[string]map[string]string //d*(included delfiles) x*
}

func (m *redisMeta) makeSnap(bar *utils.Bar) error {
	m.snap = &redisSnap{
		stringMap: make(map[string]string),
		listMap:   make(map[string][]string),
		hashMap:   make(map[string]map[string]string),
	}
	ctx := context.Background()

	listType := func(keys []string) error {
		p := m.rdb.Pipeline()
		for _, key := range keys {
			p.LRange(ctx, key, 0, -1)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			return err
		}
		for _, cmd := range cmds {
			if sliceCmd, ok := cmd.(*redis.StringSliceCmd); ok {
				if key, ok := cmd.Args()[1].(string); ok {
					m.snap.listMap[key] = sliceCmd.Val()
				}
			}
			bar.Increment()
		}

		return nil
	}

	stringType := func(keys []string) error {
		values, err := m.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return err
		}
		for i := 0; i < len(keys); i++ {
			if s, ok := values[i].(string); ok {
				m.snap.stringMap[keys[i]] = s
			}
			bar.Increment()
		}
		return nil
	}

	hashType := func(keys []string) error {
		p := m.rdb.Pipeline()
		for _, key := range keys {
			if key == m.delfiles() {
				continue
			}
			p.HGetAll(ctx, key)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			return err
		}
		for _, cmd := range cmds {
			if stringMapCmd, ok := cmd.(*redis.StringStringMapCmd); ok {
				if key, ok := cmd.Args()[1].(string); ok {
					m.snap.hashMap[key] = stringMapCmd.Val()
				}
			}
			bar.Increment()
		}
		return nil
	}

	typeMap := map[string]func([]string) error{
		"c*": listType,
		"i*": stringType,
		"s*": stringType,
		"d*": hashType,
		"x*": hashType,
	}

	scanner := func(match string, handlerKey func([]string) error) error {
		return m.scan(ctx, match, func(keys []string) error {
			if err := handlerKey(keys); err != nil {
				return err
			}
			return nil
		})
	}

	for match, typ := range typeMap {
		if err := scanner(match, typ); err != nil {
			return err
		}
	}
	return nil
}

func (m *redisMeta) DumpMeta(w io.Writer, root Ino) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = errors.Errorf("DumpMeta error: %v", p)
			}
		}
	}()
	ctx := Background
	zs, err := m.rdb.ZRangeWithScores(ctx, m.delfiles(), 0, -1).Result()
	if err != nil {
		return err
	}
	dels := make([]*DumpedDelFile, 0, len(zs))
	for _, z := range zs {
		parts := strings.Split(z.Member.(string), ":")
		if len(parts) != 2 {
			logger.Warnf("invalid delfile string: %s", z.Member.(string))
			continue
		}
		inode, _ := strconv.ParseUint(parts[0], 10, 64)
		length, _ := strconv.ParseUint(parts[1], 10, 64)
		dels = append(dels, &DumpedDelFile{Ino(inode), length, int64(z.Score)})
	}

	progress := utils.NewProgress(false, false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)
	if root == 1 {
		defer func() { m.snap = nil }()
		bar := progress.AddCountBar("Snapshot keys", m.rdb.DBSize(ctx).Val())
		if err = m.makeSnap(bar); err != nil {
			return errors.Errorf("Fetch all metadata from Redis: %s", err)
		}
		bar.Done()
		tree = m.dumpEntryFast(root, TypeDirectory)
		trash = m.dumpEntryFast(TrashInode, TypeDirectory)
	} else {
		if tree, err = m.dumpEntry(root, TypeDirectory); err != nil {
			return err
		}
	}
	if tree == nil {
		return errors.New("The entry of the root inode was not found")
	}
	tree.Name = "FSTree"

	names := []string{usedSpace, totalInodes, "nextinode", "nextchunk", "nextsession", "nextTrash"}
	for i := range names {
		names[i] = m.prefix + names[i]
	}
	rs, _ := m.rdb.MGet(ctx, names...).Result()
	cs := make([]int64, len(rs))
	for i, r := range rs {
		if r != nil {
			cs[i], _ = strconv.ParseInt(r.(string), 10, 64)
		}
	}

	keys, err := m.rdb.ZRange(ctx, m.allSessions(), 0, -1).Result()
	if err != nil {
		return err
	}
	sessions := make([]*DumpedSustained, 0, len(keys))
	for _, k := range keys {
		sid, _ := strconv.ParseUint(k, 10, 64)
		var ss []string
		if root == 1 {
			ss = m.snap.listMap[m.sustained(sid)]
		} else {
			ss, err = m.rdb.SMembers(ctx, m.sustained(sid)).Result()
			if err != nil {
				return err
			}
		}
		if len(ss) > 0 {
			inodes := make([]Ino, 0, len(ss))
			for _, s := range ss {
				inode, _ := strconv.ParseUint(s, 10, 64)
				inodes = append(inodes, Ino(inode))
			}
			sessions = append(sessions, &DumpedSustained{sid, inodes})
		}
	}

	dm := &DumpedMeta{
		Setting: m.fmt,
		Counters: &DumpedCounters{
			UsedSpace:   cs[0],
			UsedInodes:  cs[1],
			NextInode:   cs[2] + 1, // Redis nextInode/nextChunk is 1 smaller than sql/tkv
			NextChunk:   cs[3] + 1,
			NextSession: cs[4],
			NextTrash:   cs[5],
		},
		Sustained: sessions,
		DelFiles:  dels,
	}
	if dm.Setting.SecretKey != "" {
		dm.Setting.SecretKey = "removed"
		logger.Warnf("Secret key is removed for the sake of safety")
	}
	bw, err := dm.writeJsonWithOutTree(w)
	if err != nil {
		return err
	}

	bar := progress.AddCountBar("Dumped entries", 1) // with root
	bar.Increment()
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

func (m *redisMeta) loadEntry(e *DumpedEntry, cs *DumpedCounters, refs map[string]int) error {
	inode := e.Attr.Inode
	logger.Debugf("Loading entry inode %d name %s", inode, unescape(e.Name))
	ctx := Background
	attr := loadAttr(e.Attr)
	attr.Parent = e.Parent
	p := m.rdb.Pipeline()
	if attr.Typ == TypeFile {
		attr.Length = e.Attr.Length
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]string, 0, len(c.Slices))
			m.Lock()
			for _, s := range c.Slices {
				slices = append(slices, string(marshalSlice(s.Pos, s.Chunkid, s.Size, s.Off, s.Len)))
				refs[m.sliceKey(s.Chunkid, s.Size)]++
				if cs.NextChunk < int64(s.Chunkid) {
					cs.NextChunk = int64(s.Chunkid)
				}
			}
			m.Unlock()
			p.RPush(ctx, m.chunkKey(inode, c.Index), slices)
		}
	} else if attr.Typ == TypeDirectory {
		attr.Length = 4 << 10
		if len(e.Entries) > 0 {
			dentries := make(map[string]interface{})
			for _, c := range e.Entries {
				dentries[unescape(c.Name)] = m.packEntry(typeFromString(c.Attr.Type), c.Attr.Inode)
			}
			p.HSet(ctx, m.entryKey(inode), dentries)
		}
	} else if attr.Typ == TypeSymlink {
		symL := unescape(e.Symlink)
		attr.Length = uint64(len(symL))
		p.Set(ctx, m.symKey(inode), symL, 0)
	}
	m.Lock()
	if inode > 1 && inode != TrashInode {
		cs.UsedSpace += align4K(attr.Length)
		cs.UsedInodes += 1
	}
	if inode < TrashInode {
		if cs.NextInode < int64(inode) {
			cs.NextInode = int64(inode)
		}
	} else {
		if cs.NextTrash < int64(inode)-TrashInode {
			cs.NextTrash = int64(inode) - TrashInode
		}
	}
	m.Unlock()

	if len(e.Xattrs) > 0 {
		xattrs := make(map[string]interface{})
		for _, x := range e.Xattrs {
			xattrs[x.Name] = unescape(x.Value)
		}
		p.HSet(ctx, m.xattrKey(inode), xattrs)
	}
	p.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
	_, err := p.Exec(ctx)
	return err
}

func (m *redisMeta) LoadMeta(r io.Reader) error {
	ctx := Background
	dbsize, err := m.rdb.DBSize(ctx).Result()
	if err != nil {
		return err
	}
	if dbsize > 0 {
		return fmt.Errorf("Database %s is not empty", m.Name())
	}

	dec := json.NewDecoder(r)
	dm := &DumpedMeta{}
	if err = dec.Decode(dm); err != nil {
		return err
	}
	format, err := json.MarshalIndent(dm.Setting, "", "")
	if err != nil {
		return err
	}

	progress := utils.NewProgress(false, false)
	bar := progress.AddCountBar("Collected entries", 1) // with root
	showProgress := func(totalIncr, currentIncr int64) {
		bar.IncrTotal(totalIncr)
		bar.IncrInt64(currentIncr)
	}
	dm.FSTree.Attr.Inode = 1
	entries := make(map[Ino]*DumpedEntry)
	if err = collectEntry(dm.FSTree, entries, showProgress); err != nil {
		return err
	}
	if dm.Trash != nil {
		bar.IncrTotal(1)
		if err = collectEntry(dm.Trash, entries, showProgress); err != nil {
			return err
		}
	}
	bar.Done()

	counters := &DumpedCounters{}
	refs := make(map[string]int)
	bar = progress.AddCountBar("Loaded entries", int64(len(entries)))
	maxNum := 100
	pool := make(chan struct{}, maxNum)
	errCh := make(chan error, 100)
	done := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for _, entry := range entries {
		select {
		case err = <-errCh:
			return err
		default:
		}
		pool <- struct{}{}
		wg.Add(1)
		go func(entry *DumpedEntry) {
			defer func() {
				wg.Done()
				bar.Increment()
				<-pool
			}()
			if err = m.loadEntry(entry, counters, refs); err != nil {
				errCh <- err
			}
		}(entry)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err = <-errCh:
		return err
	case <-done:
	}
	progress.Done()
	logger.Infof("Dumped counters: %+v", *dm.Counters)
	logger.Infof("Loaded counters: %+v", *counters)

	p := m.rdb.Pipeline()
	p.Set(ctx, m.setting(), format, 0)
	cs := make(map[string]interface{})
	cs[usedSpace] = counters.UsedSpace
	cs[totalInodes] = counters.UsedInodes
	cs["nextinode"] = counters.NextInode
	cs["nextchunk"] = counters.NextChunk
	cs["nextsession"] = counters.NextSession
	cs["nextTrash"] = counters.NextTrash
	p.MSet(ctx, cs)
	if len(dm.DelFiles) > 0 {
		zs := make([]*redis.Z, 0, len(dm.DelFiles))
		for _, d := range dm.DelFiles {
			zs = append(zs, &redis.Z{
				Score:  float64(d.Expire),
				Member: m.toDelete(d.Inode, d.Length),
			})
		}
		p.ZAdd(ctx, m.delfiles(), zs...)
	}
	slices := make(map[string]interface{})
	for k, v := range refs {
		if v > 1 {
			slices[k] = v - 1
		}
	}
	if len(slices) > 0 {
		p.HSet(ctx, m.sliceRefs(), slices)
	}
	_, err = p.Exec(ctx)
	return err
}
