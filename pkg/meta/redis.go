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
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

/*
	Node:       i$inode -> Attribute{type,mode,uid,gid,atime,mtime,ctime,nlink,length,rdev}
	Dir:        d$inode -> {name -> {inode,type}}
	Parent:     p$inode -> {parent -> count} // for hard links
	File:       c$inode_$indx -> [Slice{pos,id,length,off,len}]
	Symlink:    s$inode -> target
	Xattr:      x$inode -> {name -> value}
	Flock:      lockf$inode -> { $sid_$owner -> ltype }
	POSIX lock: lockp$inode -> { $sid_$owner -> Plock(pid,ltype,start,end) }
	Sessions:   sessions -> [ $sid -> heartbeat ]
	sustained:  session$sid -> [$inode]
	locked:     locked$sid -> { lockf$inode or lockp$inode }

	Removed files: delfiles -> [$inode:$length -> seconds]
	detached nodes: detachedNodes -> [$inode -> seconds]
	Slices refs: sliceRef -> {k$sliceId_$size -> refcount}

	Dir data length:   dirDataLength -> { $inode -> length }
	Dir used space:    dirUsedSpace -> { $inode -> usedSpace }
	Dir used inodes:   dirUsedInodes -> { $inode -> usedInodes }
	Quota:             dirQuota -> { $inode -> {maxSpace, maxInodes} }
	Quota used space:  dirQuotaUsedSpace -> { $inode -> usedSpace }
	Quota used inodes: dirQuotaUsedInodes -> { $inode -> usedInodes }
	Acl: acl -> { $acl_id -> acl }

	Redis features:
	  Sorted Set: 1.2+
	  Hash Set: 4.0+
	  Transaction: 2.2+
	  Scripting: 2.6+
	  Scan: 2.8+
*/

type redisMeta struct {
	*baseMeta
	rdb        redis.UniversalClient
	prefix     string
	shaLookup  string // The SHA returned by Redis for the loaded `scriptLookup`
	shaResolve string // The SHA returned by Redis for the loaded `scriptResolve`
}

var _ Meta = (*redisMeta)(nil)
var _ engine = (*redisMeta)(nil)

func init() {
	Register("redis", newRedisMeta)
	Register("rediss", newRedisMeta)
	Register("unix", newRedisMeta)
}

// newRedisMeta return a meta store using Redis.
func newRedisMeta(driver, addr string, conf *Config) (Meta, error) {
	uri := driver + "://" + addr
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("url parse %s: %s", uri, err)
	}
	values := u.Query()
	query := queryMap{&values}
	minRetryBackoff := query.duration("min-retry-backoff", "min_retry_backoff", time.Millisecond*20)
	maxRetryBackoff := query.duration("max-retry-backoff", "max_retry_backoff", time.Second*10)
	readTimeout := query.duration("read-timeout", "read_timeout", time.Second*30)
	writeTimeout := query.duration("write-timeout", "write_timeout", time.Second*5)
	routeRead := query.pop("route-read")
	skipVerify := query.pop("insecure-skip-verify")
	certFile := query.pop("tls-cert-file")
	keyFile := query.pop("tls-key-file")
	caCertFile := query.pop("tls-ca-cert-file")
	tlsServerName := query.pop("tls-server-name")
	u.RawQuery = values.Encode()

	hosts := u.Host
	opt, err := redis.ParseURL(u.String())
	if err != nil {
		return nil, fmt.Errorf("redis parse %s: %s", uri, err)
	}
	if opt.TLSConfig != nil {
		opt.TLSConfig.ServerName = tlsServerName // use the host of each connection as ServerName
		opt.TLSConfig.InsecureSkipVerify = skipVerify != ""
		if certFile != "" {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("get certificate error certFile:%s keyFile:%s error:%s", certFile, keyFile, err)
			}
			opt.TLSConfig.Certificates = []tls.Certificate{cert}
		}
		if caCertFile != "" {
			caCert, err := os.ReadFile(caCertFile)
			if err != nil {
				return nil, fmt.Errorf("read ca cert file error path:%s error:%s", caCertFile, err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)
			opt.TLSConfig.RootCAs = caCertPool
		}
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("REDIS_PASSWORD")
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("META_PASSWORD")
	}
	opt.MaxRetries = conf.Retries
	if opt.MaxRetries == 0 {
		opt.MaxRetries = -1 // Redis use -1 to disable retries
	}
	opt.MinRetryBackoff = minRetryBackoff
	opt.MaxRetryBackoff = maxRetryBackoff
	opt.ReadTimeout = readTimeout
	opt.WriteTimeout = writeTimeout
	var rdb redis.UniversalClient
	var prefix string
	if strings.Contains(hosts, ",") && strings.Index(hosts, ",") < strings.Index(hosts, ":") {
		var fopt redis.FailoverOptions
		ps := strings.Split(hosts, ",")
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
		fopt.DialTimeout = opt.DialTimeout
		fopt.ReadTimeout = opt.ReadTimeout
		fopt.WriteTimeout = opt.WriteTimeout
		fopt.PoolFIFO = opt.PoolFIFO               // default: false
		fopt.PoolSize = opt.PoolSize               // default: GOMAXPROCS * 10
		fopt.PoolTimeout = opt.PoolTimeout         // default: ReadTimeout + 1 second.
		fopt.MinIdleConns = opt.MinIdleConns       // disable by default
		fopt.MaxIdleConns = opt.MaxIdleConns       // disable by default
		fopt.MaxActiveConns = opt.MaxActiveConns   // default: 0, no limit
		fopt.ConnMaxIdleTime = opt.ConnMaxIdleTime // default: 30 minutes
		fopt.ConnMaxLifetime = opt.ConnMaxLifetime // disable by default
		if conf.ReadOnly {
			// NOTE: RouteByLatency and RouteRandomly are not supported since they require cluster client
			fopt.ReplicaOnly = routeRead == "replica"
		}
		rdb = redis.NewFailoverClient(&fopt)
	} else {
		if !strings.Contains(hosts, ",") {
			c := redis.NewClient(opt)
			info, err := c.ClusterInfo(Background()).Result()
			if err != nil && strings.Contains(err.Error(), "cluster mode") || err == nil && strings.Contains(info, "cluster_state:") {
				logger.Infof("redis %s is in cluster mode", hosts)
			} else {
				rdb = c
			}
		}
		if rdb == nil {
			var copt redis.ClusterOptions
			copt.Addrs = strings.Split(hosts, ",")
			copt.MaxRedirects = 1
			copt.Username = opt.Username
			copt.Password = opt.Password
			copt.TLSConfig = opt.TLSConfig
			copt.MaxRetries = opt.MaxRetries
			copt.MinRetryBackoff = opt.MinRetryBackoff
			copt.MaxRetryBackoff = opt.MaxRetryBackoff
			copt.DialTimeout = opt.DialTimeout
			copt.ReadTimeout = opt.ReadTimeout
			copt.WriteTimeout = opt.WriteTimeout
			copt.PoolFIFO = opt.PoolFIFO               // default: false
			copt.PoolSize = opt.PoolSize               // default: GOMAXPROCS * 10
			copt.PoolTimeout = opt.PoolTimeout         // default: ReadTimeout + 1 second.
			copt.MinIdleConns = opt.MinIdleConns       // disable by default
			copt.MaxIdleConns = opt.MaxIdleConns       // disable by default
			copt.MaxActiveConns = opt.MaxActiveConns   // default: 0, no limit
			copt.ConnMaxIdleTime = opt.ConnMaxIdleTime // default: 30 minutes
			copt.ConnMaxLifetime = opt.ConnMaxLifetime // disable by default
			if conf.ReadOnly {
				switch routeRead {
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
		baseMeta: newBaseMeta(addr, conf),
		rdb:      rdb,
		prefix:   prefix,
	}
	m.en = m
	m.checkServerConfig()
	return m, nil
}

func (m *redisMeta) Shutdown() error {
	return m.rdb.Close()
}

func (m *redisMeta) doDeleteSlice(id uint64, size uint32) error {
	return m.rdb.HDel(Background(), m.sliceRefs(), m.sliceKey(id, size)).Err()
}

func (m *redisMeta) Name() string {
	return "redis"
}

func (m *redisMeta) doInit(format *Format, force bool) error {
	ctx := Background()
	body, err := m.rdb.Get(ctx, m.setting()).Bytes()
	if err != nil && err != redis.Nil {
		return err
	}
	if err == nil {
		var old Format
		err = json.Unmarshal(body, &old)
		if err != nil {
			return fmt.Errorf("existing format is broken: %s", err)
		}
		if !old.DirStats && format.DirStats {
			// remove dir stats as they are outdated
			err := m.rdb.Del(ctx, m.dirUsedInodesKey(), m.dirUsedSpaceKey()).Err()
			if err != nil {
				return errors.Wrap(err, "remove dir stats")
			}
		}
		if !old.UserGroupQuota && format.UserGroupQuota {
			// remove user group quota as they are outdated
			err := m.rdb.Del(ctx, m.userQuotaKey(), m.userQuotaUsedSpaceKey(), m.userQuotaUsedInodesKey(),
				m.groupQuotaKey(), m.groupQuotaUsedSpaceKey(), m.groupQuotaUsedInodesKey()).Err()
			if err != nil {
				return errors.Wrap(err, "remove user group quota")
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
		if err = m.rdb.SetNX(ctx, m.inodeKey(TrashInode), m.marshal(attr), 0).Err(); err != nil {
			return err
		}
	}
	if err = m.rdb.Set(ctx, m.setting(), data, 0).Err(); err != nil {
		return err
	}
	m.fmt = format
	if body != nil {
		return nil
	}

	// root inode
	attr.Mode = 0777
	return m.rdb.Set(ctx, m.inodeKey(1), m.marshal(attr), 0).Err()
}

func (m *redisMeta) cacheACLs(ctx Context) error {
	if !m.getFormat().EnableACL {
		return nil
	}

	vals, err := m.rdb.HGetAll(ctx, m.aclKey()).Result()
	if err != nil {
		return err
	}

	for k, v := range vals {
		id, _ := strconv.ParseUint(k, 10, 32)
		tmpRule := &aclAPI.Rule{}
		tmpRule.Decode([]byte(v))
		m.aclCache.Put(uint32(id), tmpRule)
	}
	return nil
}

func (m *redisMeta) Reset() error {
	if m.prefix != "" {
		return m.scan(Background(), "*", func(keys []string) error {
			return m.rdb.Del(Background(), keys...).Err()
		})
	}
	return m.rdb.FlushDB(Background()).Err()
}

func (m *redisMeta) doLoad() ([]byte, error) {
	body, err := m.rdb.Get(Background(), m.setting()).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return body, err
}

func (m *redisMeta) doNewSession(sinfo []byte, update bool) error {
	err := m.rdb.ZAdd(Background(), m.allSessions(), redis.Z{
		Score:  float64(m.expireTime()),
		Member: strconv.FormatUint(m.sid, 10)}).Err()
	if err != nil {
		return fmt.Errorf("set session ID %d: %s", m.sid, err)
	}
	if err = m.rdb.HSet(Background(), m.sessionInfos(), m.sid, sinfo).Err(); err != nil {
		return fmt.Errorf("set session info: %s", err)
	}

	if m.shaLookup, err = m.rdb.ScriptLoad(Background(), scriptLookup).Result(); err != nil {
		logger.Warnf("load scriptLookup: %v", err)
		m.shaLookup = ""
	}
	if m.shaResolve, err = m.rdb.ScriptLoad(Background(), scriptResolve).Result(); err != nil {
		logger.Warnf("load scriptResolve: %v", err)
		m.shaResolve = ""
	}

	if !m.conf.NoBGJob {
		go m.cleanupLegacies()
	}
	return nil
}

func (m *redisMeta) getCounter(name string) (int64, error) {
	v, err := m.rdb.Get(Background(), m.counterKey(name)).Int64()
	if err == redis.Nil {
		err = nil
	}
	return v, err
}

func (m *redisMeta) incrCounter(name string, value int64) (int64, error) {
	if m.conf.ReadOnly {
		return 0, syscall.EROFS
	}
	key := m.counterKey(name)
	if name == "nextInode" || name == "nextChunk" {
		// for nextinode, nextchunk
		// the current one is already used
		v, err := m.rdb.IncrBy(Background(), key, value).Result()
		return v + 1, err
	}
	return m.rdb.IncrBy(Background(), key, value).Result()
}

func (m *redisMeta) setIfSmall(name string, value, diff int64) (bool, error) {
	var changed bool
	ctx := Background()
	name = m.prefix + name
	err := m.txn(ctx.WithValue(txMethodKey{}, "setIfSmall:"+name), func(tx *redis.Tx) error {
		changed = false
		old, err := tx.Get(ctx, name).Int64()
		if err != nil && err != redis.Nil {
			return err
		}
		if old > value-diff {
			return nil
		} else {
			changed = true
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, name, value, 0)
				return nil
			})
			return err
		}
	}, name)

	return changed, err
}

func (m *redisMeta) getSession(sid string, detail bool) (*Session, error) {
	ctx := Background()
	info, err := m.rdb.HGet(ctx, m.sessionInfos(), sid).Bytes()
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
		inodes, err := m.rdb.SMembers(ctx, m.sustained(s.Sid)).Result()
		if err != nil {
			return nil, fmt.Errorf("SMembers %s: %s", sid, err)
		}
		s.Sustained = make([]Ino, 0, len(inodes))
		for _, sinode := range inodes {
			inode, _ := strconv.ParseUint(sinode, 10, 64)
			s.Sustained = append(s.Sustained, Ino(inode))
		}

		locks, err := m.rdb.SMembers(ctx, m.lockedKey(s.Sid)).Result()
		if err != nil {
			return nil, fmt.Errorf("SMembers %s: %s", sid, err)
		}
		s.Flocks = make([]Flock, 0, len(locks)) // greedy
		s.Plocks = make([]Plock, 0, len(locks))
		for _, lock := range locks {
			owners, err := m.rdb.HGetAll(ctx, lock).Result()
			if err != nil {
				return nil, fmt.Errorf("HGetAll %s: %s", lock, err)
			}
			isFlock := strings.HasPrefix(lock, m.prefix+"lockf")
			inode, _ := strconv.ParseUint(lock[len(m.prefix)+5:], 10, 64)
			for k, v := range owners {
				parts := strings.Split(k, "_")
				if parts[0] != sid {
					continue
				}
				owner, _ := strconv.ParseUint(parts[1], 16, 64)
				if isFlock {
					s.Flocks = append(s.Flocks, Flock{Ino(inode), owner, v})
				} else {
					s.Plocks = append(s.Plocks, Plock{Ino(inode), owner, loadLocks([]byte(v))})
				}
			}
		}
	}
	return &s, nil
}

func (m *redisMeta) GetSession(sid uint64, detail bool) (*Session, error) {
	var legacy bool
	key := strconv.FormatUint(sid, 10)
	score, err := m.rdb.ZScore(Background(), m.allSessions(), key).Result()
	if err == redis.Nil {
		legacy = true
		score, err = m.rdb.ZScore(Background(), legacySessions, key).Result()
	}
	if err == redis.Nil {
		err = fmt.Errorf("session not found: %d", sid)
	}
	if err != nil {
		return nil, err
	}
	s, err := m.getSession(key, detail)
	if err != nil {
		return nil, err
	}
	s.Expire = time.Unix(int64(score), 0)
	if legacy {
		s.Expire = s.Expire.Add(time.Minute * 5)
	}
	return s, nil
}

func (m *redisMeta) ListSessions() ([]*Session, error) {
	keys, err := m.rdb.ZRangeWithScores(Background(), m.allSessions(), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(keys))
	for _, k := range keys {
		s, err := m.getSession(k.Member.(string), false)
		if err != nil {
			logger.Errorf("get session: %s", err)
			continue
		}
		s.Expire = time.Unix(int64(k.Score), 0)
		sessions = append(sessions, s)
	}

	// add clients with version before 1.0-beta3 as well
	keys, err = m.rdb.ZRangeWithScores(Background(), legacySessions, 0, -1).Result()
	if err != nil {
		logger.Errorf("Scan legacy sessions: %s", err)
		return sessions, nil
	}
	for _, k := range keys {
		s, err := m.getSession(k.Member.(string), false)
		if err != nil {
			logger.Errorf("Get legacy session: %s", err)
			continue
		}
		s.Expire = time.Unix(int64(k.Score), 0).Add(time.Minute * 5)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (m *redisMeta) sustained(sid uint64) string {
	return m.prefix + "session" + strconv.FormatUint(sid, 10)
}

func (m *redisMeta) lockedKey(sid uint64) string {
	return m.prefix + "locked" + strconv.FormatUint(sid, 10)
}

func (m *redisMeta) symKey(inode Ino) string {
	return m.prefix + "s" + inode.String()
}

func (m *redisMeta) inodeKey(inode Ino) string {
	return m.prefix + "i" + inode.String()
}

func (m *redisMeta) entryKey(parent Ino) string {
	return m.prefix + "d" + parent.String()
}

func (m *redisMeta) parentKey(inode Ino) string {
	return m.prefix + "p" + inode.String()
}

func (m *redisMeta) chunkKey(inode Ino, indx uint32) string {
	return m.prefix + "c" + inode.String() + "_" + strconv.FormatInt(int64(indx), 10)
}

func (m *redisMeta) sliceKey(id uint64, size uint32) string {
	// inside hashset
	return "k" + strconv.FormatUint(id, 10) + "_" + strconv.FormatUint(uint64(size), 10)
}

func (m *redisMeta) xattrKey(inode Ino) string {
	return m.prefix + "x" + inode.String()
}

func (m *redisMeta) flockKey(inode Ino) string {
	return m.prefix + "lockf" + inode.String()
}

func (m *redisMeta) ownerKey(owner uint64) string {
	return fmt.Sprintf("%d_%016X", m.sid, owner)
}

func (m *redisMeta) plockKey(inode Ino) string {
	return m.prefix + "lockp" + inode.String()
}

func (m *redisMeta) setting() string {
	return m.prefix + "setting"
}

func (m *redisMeta) usedSpaceKey() string {
	return m.prefix + usedSpace
}

func (m *redisMeta) nextTrashKey() string {
	return m.prefix + "nextTrash"
}

func (m *redisMeta) counterKey(name string) string {
	if name == "nextInode" || name == "nextChunk" || name == "nextSession" {
		name = strings.ToLower(name)
	}
	return m.prefix + name
}

func (m *redisMeta) dirDataLengthKey() string {
	return m.prefix + "dirDataLength"
}

func (m *redisMeta) dirUsedSpaceKey() string {
	return m.prefix + "dirUsedSpace"
}

func (m *redisMeta) dirUsedInodesKey() string {
	return m.prefix + "dirUsedInodes"
}

func (m *redisMeta) dirQuotaUsedSpaceKey() string {
	return m.prefix + "dirQuotaUsedSpace"
}

func (m *redisMeta) dirQuotaUsedInodesKey() string {
	return m.prefix + "dirQuotaUsedInodes"
}

func (m *redisMeta) dirQuotaKey() string {
	return m.prefix + "dirQuota"
}

func (m *redisMeta) userQuotaUsedSpaceKey() string {
	return m.prefix + "userQuotaUsedSpace"
}

func (m *redisMeta) userQuotaUsedInodesKey() string {
	return m.prefix + "userQuotaUsedInodes"
}

func (m *redisMeta) userQuotaKey() string {
	return m.prefix + "userQuota"
}

func (m *redisMeta) groupQuotaUsedSpaceKey() string {
	return m.prefix + "groupQuotaUsedSpace"
}

func (m *redisMeta) groupQuotaUsedInodesKey() string {
	return m.prefix + "groupQuotaUsedInodes"
}

func (m *redisMeta) groupQuotaKey() string {
	return m.prefix + "groupQuota"
}

func (m *redisMeta) totalInodesKey() string {
	return m.prefix + totalInodes
}

func (m *redisMeta) aclKey() string {
	return m.prefix + "acl"
}

func (m *redisMeta) delfiles() string {
	return m.prefix + "delfiles"
}

func (m *redisMeta) detachedNodes() string {
	return m.prefix + "detachedNodes"
}

func (r *redisMeta) delSlices() string {
	return r.prefix + "delSlices"
}

func (r *redisMeta) allSessions() string {
	return r.prefix + "allSessions"
}

func (m *redisMeta) sessionInfos() string {
	return m.prefix + "sessionInfos"
}

func (m *redisMeta) sliceRefs() string {
	return m.prefix + "sliceRef"
}

func (m *redisMeta) packQuota(space, inodes int64) []byte {
	wb := utils.NewBuffer(16)
	wb.Put64(uint64(space))
	wb.Put64(uint64(inodes))
	return wb.Bytes()
}

func (m *redisMeta) parseQuota(buf []byte) (space, inodes int64) {
	if len(buf) == 0 {
		return 0, 0
	}
	if len(buf) != 16 {
		logger.Errorf("invalid quota value: %v", buf)
		return 0, 0
	}
	rb := utils.ReadBuffer(buf)
	return int64(rb.Get64()), int64(rb.Get64())
}

func (m *redisMeta) packEntry(_type uint8, inode Ino) []byte {
	wb := utils.NewBuffer(9)
	wb.Put8(_type)
	wb.Put64(uint64(inode))
	return wb.Bytes()
}

func (m *redisMeta) parseEntry(buf []byte) (uint8, Ino) {
	if len(buf) != 9 {
		panic("invalid entry")
	}
	return buf[0], Ino(binary.BigEndian.Uint64(buf[1:]))
}

func (m *redisMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.usedSpace, space)
	atomic.AddInt64(&m.usedInodes, inodes)
}

func (m *redisMeta) doSyncVolumeStat(ctx Context) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	var used, inodes int64
	if err := m.hscan(ctx, m.dirUsedSpaceKey(), func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			v, err := strconv.ParseInt(keys[i+1], 10, 64)
			if err != nil {
				logger.Warnf("invalid used space: %s->%s", keys[i], keys[i+1])
				continue
			}
			used += v
		}
		return nil
	}); err != nil {
		return err
	}
	if err := m.hscan(ctx, m.dirUsedInodesKey(), func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			v, err := strconv.ParseInt(keys[i+1], 10, 64)
			if err != nil {
				logger.Warnf("invalid used inode: %s->%s", keys[i], keys[i+1])
				continue
			}
			inodes += v
		}
		return nil
	}); err != nil {
		return err
	}

	var inoKeys []string
	if err := m.scan(ctx, m.prefix+"session*", func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			key := keys[i]
			if key == "sessions" {
				continue
			}

			inodes, err := m.rdb.SMembers(ctx, key).Result()
			if err != nil {
				logger.Warnf("SMembers %s: %s", key, err)
				continue
			}
			for _, sinode := range inodes {
				ino, err := strconv.ParseInt(sinode, 10, 64)
				if err != nil {
					logger.Warnf("invalid sustained: %s->%s", key, sinode)
					continue
				}
				inoKeys = append(inoKeys, m.inodeKey(Ino(ino)))
			}
		}
		return nil
	}); err != nil {
		return err
	}

	batch := 1000
	for i := 0; i < len(inoKeys); i += batch {
		end := i + batch
		if end > len(inoKeys) {
			end = len(inoKeys)
		}
		values, err := m.rdb.MGet(ctx, inoKeys[i:end]...).Result()
		if err != nil {
			return err
		}
		var attr Attr
		for _, v := range values {
			if v != nil {
				m.parseAttr([]byte(v.(string)), &attr)
				used += align4K(attr.Length)
				inodes += 1
			}
		}
	}
	if err := m.scanTrashEntry(ctx, func(_ Ino, length uint64) {
		used += align4K(length)
		inodes += 1
	}); err != nil {
		return err
	}
	logger.Debugf("Used space: %s, inodes: %d", humanize.IBytes(uint64(used)), inodes)
	if err := m.rdb.Set(ctx, m.totalInodesKey(), strconv.FormatInt(inodes, 10), 0).Err(); err != nil {
		return fmt.Errorf("set total inodes: %s", err)
	}
	return m.rdb.Set(ctx, m.usedSpaceKey(), strconv.FormatInt(used, 10), 0).Err()
}

// redisMeta updates the usage in each transaction
func (m *redisMeta) doFlushStats() {}

func (m *redisMeta) handleLuaResult(op string, res interface{}, err error, returnedIno *int64, returnedAttr *string) syscall.Errno {
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "NOSCRIPT") {
			var err2 error
			switch op {
			case "lookup":
				m.shaLookup, err2 = m.rdb.ScriptLoad(Background(), scriptLookup).Result()
			case "resolve":
				m.shaResolve, err2 = m.rdb.ScriptLoad(Background(), scriptResolve).Result()
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
		} else if strings.Contains(msg, "ENOENT") {
			return syscall.ENOENT
		} else if strings.Contains(msg, "EACCESS") {
			return syscall.EACCES
		} else if strings.Contains(msg, "ENOTDIR") {
			return syscall.ENOTDIR
		} else if strings.Contains(msg, "ENOTSUP") {
			return syscall.ENOTSUP
		} else {
			logger.Warnf("unexpected error for %s: %s", op, msg)
			switch op {
			case "lookup":
				m.shaLookup = ""
			case "resolve":
				m.shaResolve = ""
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

func (m *redisMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var foundIno Ino
	var foundType uint8
	var encodedAttr []byte
	var err error
	entryKey := m.entryKey(parent)
	if len(m.shaLookup) > 0 && attr != nil && !m.conf.CaseInsensi && m.prefix == "" {
		var res interface{}
		var returnedIno int64
		var returnedAttr string
		res, err = m.rdb.EvalSha(ctx, m.shaLookup, []string{entryKey, name}).Result()
		if st := m.handleLuaResult("lookup", res, err, &returnedIno, &returnedAttr); st == 0 {
			foundIno = Ino(returnedIno)
			encodedAttr = []byte(returnedAttr)
		} else if st == syscall.EAGAIN {
			return m.doLookup(ctx, parent, name, inode, attr)
		} else if st != syscall.ENOTSUP {
			return st
		}
	}
	if foundIno == 0 || len(encodedAttr) == 0 {
		var buf []byte
		buf, err = m.rdb.HGet(ctx, entryKey, name).Bytes()
		if err != nil {
			return errno(err)
		}
		foundType, foundIno = m.parseEntry(buf)
		encodedAttr, err = m.rdb.Get(ctx, m.inodeKey(foundIno)).Bytes()
	}

	if err == nil {
		m.parseAttr(encodedAttr, attr)
		m.of.Update(foundIno, attr)
	} else if err == redis.Nil { // corrupt entry
		logger.Warnf("no attribute for inode %d (%d, %s)", foundIno, parent, name)
		*attr = Attr{Typ: foundType}
		err = nil
	}
	*inode = foundIno
	return errno(err)
}

func (m *redisMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	if len(m.shaResolve) == 0 || m.conf.CaseInsensi || m.prefix != "" {
		return syscall.ENOTSUP
	}
	defer m.timeit("Resolve", time.Now())
	parent = m.checkRoot(parent)
	keys := []string{parent.String(), path,
		strconv.FormatUint(uint64(ctx.Uid()), 10)}
	var gids []interface{}
	for _, gid := range ctx.Gids() {
		gids = append(gids, strconv.FormatUint(uint64(gid), 10))
	}
	res, err := m.rdb.EvalSha(ctx, m.shaResolve, keys, gids...).Result()
	var returnedIno int64
	var returnedAttr string
	st := m.handleLuaResult("resolve", res, err, &returnedIno, &returnedAttr)
	if st == 0 {
		if inode != nil {
			*inode = Ino(returnedIno)
		}
		m.parseAttr([]byte(returnedAttr), attr)
	} else if st == syscall.EAGAIN {
		return m.Resolve(ctx, parent, path, inode, attr)
	}
	return st
}

func (m *redisMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	a, err := m.rdb.Get(ctx, m.inodeKey(inode)).Bytes()
	if err == nil {
		m.parseAttr(a, attr)
	}
	return errno(err)
}

type timeoutError interface {
	Timeout() bool
}

func (m *redisMeta) shouldRetry(err error, retryOnFailure bool) bool {
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

// errNo is an alias to syscall.Errno to disable retry in Redis Cluster
type errNo uintptr

func (e errNo) Error() string {
	return syscall.Errno(e).Error()
}

// replaceErrno replace returned syscall.Errno as errNo
func replaceErrno(txf func(tx *redis.Tx) error) func(tx *redis.Tx) error {
	return func(tx *redis.Tx) error {
		err := txf(tx)
		if eno, ok := err.(syscall.Errno); ok {
			err = errNo(eno)
		}
		return err
	}
}

func (m *redisMeta) txn(ctx Context, txf func(tx *redis.Tx) error, keys ...string) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, m.prefix) {
			panic(fmt.Sprintf("Invalid key %s not starts with prefix %s", k, m.prefix))
		}
	}
	var khash = fnv.New32()
	_, _ = khash.Write([]byte(keys[0]))
	h := uint(khash.Sum32())

	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()

	m.txLock(h)
	defer m.txUnlock(h)
	// TODO: enable retry for some of idempodent transactions
	var (
		retryOnFailture = false
		lastErr         error
		method          string
	)
	for i := 0; i < 50; i++ {
		if ctx.Canceled() {
			return syscall.EINTR
		}
		err := m.rdb.Watch(ctx, replaceErrno(txf), keys...)
		if eno, ok := err.(errNo); ok {
			if eno == 0 {
				err = nil
			} else {
				err = syscall.Errno(eno)
			}
		}
		if err != nil && m.shouldRetry(err, retryOnFailture) {
			if method == "" {
				method = callerName(ctx) // lazy evaluation
			}
			m.txRestart.WithLabelValues(method).Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(rand.Int()%((i+1)*(i+1))))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Transaction succeeded after %d tries (%s), keys: %v, method: %s, last error: %s", i+1, time.Since(start), keys, method, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
}

func (m *redisMeta) doTruncate(ctx Context, inode Ino, flags uint8, length uint64, delta *dirStat, attr *Attr, skipPermCheck bool) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		*delta = dirStat{}
		var t Attr
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		m.parseAttr(a, &t)
		if t.Typ != TypeFile || t.Flags&(FlagImmutable|FlagAppend) != 0 || (flags == 0 && t.Parent > TrashInode) {
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
		if err := m.checkQuota(ctx, delta.space, 0, t.Uid, t.Gid, m.getParents(ctx, tx, inode, t.Parent)...); err != 0 {
			return err
		}
		var zeroChunks []uint32
		var left, right = t.Length, length
		if left > right {
			right, left = left, right
		}
		if (right-left)/ChunkSize >= 10000 {
			// super large
			var cursor uint64
			var keys []string
			for {
				keys, cursor, err = tx.Scan(ctx, cursor, m.prefix+fmt.Sprintf("c%d_*", inode), 10000).Result()
				if err != nil {
					return err
				}
				for _, key := range keys {
					indx, err := strconv.Atoi(strings.Split(key[len(m.prefix):], "_")[1])
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
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(&t), 0)
			// zero out from left to right
			var l = uint32(right - left)
			if right > (left/ChunkSize+1)*ChunkSize {
				l = ChunkSize - uint32(left%ChunkSize)
			}
			pipe.RPush(ctx, m.chunkKey(inode, uint32(left/ChunkSize)), marshalSlice(uint32(left%ChunkSize), 0, 0, 0, l))
			buf := marshalSlice(0, 0, 0, 0, ChunkSize)
			for _, indx := range zeroChunks {
				pipe.RPushX(ctx, m.chunkKey(inode, indx), buf)
			}
			if right > (left/ChunkSize+1)*ChunkSize && right%ChunkSize > 0 {
				pipe.RPush(ctx, m.chunkKey(inode, uint32(right/ChunkSize)), marshalSlice(0, 0, 0, 0, uint32(right%ChunkSize)))
			}
			pipe.IncrBy(ctx, m.usedSpaceKey(), delta.space)
			return nil
		})
		if err == nil {
			*attr = t
		}
		return err
	}, m.inodeKey(inode)))
}

func (m *redisMeta) doFallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		*delta = dirStat{}
		t := Attr{}
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
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
		delta.length = int64(length) - int64(old)
		delta.space = align4K(length) - align4K(old)
		if err := m.checkQuota(ctx, delta.space, 0, t.Uid, t.Gid, m.getParents(ctx, tx, inode, t.Parent)...); err != 0 {
			return err
		}
		t.Length = length
		now := time.Now()
		t.Mtime = now.Unix()
		t.Mtimensec = uint32(now.Nanosecond())
		t.Ctime = now.Unix()
		t.Ctimensec = uint32(now.Nanosecond())
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(&t), 0)
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
					pipe.RPush(ctx, m.chunkKey(inode, indx), marshalSlice(uint32(coff), 0, 0, 0, uint32(l)))
					off += l
					size -= l
				}
			}
			pipe.IncrBy(ctx, m.usedSpaceKey(), align4K(length)-align4K(old))
			return nil
		})
		if err == nil {
			*attr = t
		}
		return err
	}, m.inodeKey(inode)))
}

func (m *redisMeta) doSetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr, oldAttr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		var cur Attr
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		m.parseAttr(a, &cur)
		if oldAttr != nil {
			*oldAttr = cur
		}
		if cur.Parent > TrashInode {
			return syscall.EPERM
		}
		now := time.Now()

		rule, err := m.getACL(ctx, tx, cur.AccessACL)
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

		dirtyAttr.AccessACL, err = m.insertACL(ctx, tx, rule)
		if err != nil {
			return err
		}

		dirtyAttr.Ctime = now.Unix()
		dirtyAttr.Ctimensec = uint32(now.Nanosecond())
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(dirtyAttr), 0)
			return nil
		})
		if err == nil {
			*attr = *dirtyAttr
		}
		return err
	}, m.inodeKey(inode)))
}

func (m *redisMeta) doReadlink(ctx Context, inode Ino, noatime bool) (atime int64, target []byte, err error) {
	if noatime {
		target, err = m.rdb.Get(ctx, m.symKey(inode)).Bytes()
		if err == redis.Nil {
			err = nil
		}
		return
	}

	attr := &Attr{}
	now := time.Now()
	err = m.txn(ctx, func(tx *redis.Tx) error {
		rs, e := tx.MGet(ctx, m.inodeKey(inode), m.symKey(inode)).Result()
		if e != nil {
			return e
		}
		if rs[0] == nil {
			return syscall.ENOENT
		}
		m.parseAttr([]byte(rs[0].(string)), attr)
		if attr.Typ != TypeSymlink {
			return syscall.EINVAL
		}
		if rs[1] == nil {
			return syscall.EIO
		}
		target = []byte(rs[1].(string))
		if !m.atimeNeedsUpdate(attr, now) {
			atime = attr.Atime*int64(time.Second) + int64(attr.Atimensec)
			return nil
		}
		attr.Atime = now.Unix()
		attr.Atimensec = uint32(now.Nanosecond())
		atime = now.UnixNano()
		_, e = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
			return nil
		})
		return e
	}, m.inodeKey(inode))
	return
}

func (m *redisMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, path string, inode *Ino, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		var pattr Attr
		a, err := tx.Get(ctx, m.inodeKey(parent)).Bytes()
		if err != nil {
			return err
		}
		m.parseAttr(a, &pattr)
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

		buf, err := tx.HGet(ctx, m.entryKey(parent), name).Bytes()
		if err != nil && err != redis.Nil {
			return err
		}
		var foundIno Ino
		var foundType uint8
		if err == nil {
			foundType, foundIno = m.parseEntry(buf)
		} else if m.conf.CaseInsensi { // err == redis.Nil
			if entry := m.resolveCase(ctx, parent, name); entry != nil {
				foundType, foundIno = entry.Attr.Typ, entry.Inode
			}
		}
		if foundIno != 0 {
			if _type == TypeFile || _type == TypeDirectory { // file for create, directory for subTrash
				a, err = tx.Get(ctx, m.inodeKey(foundIno)).Bytes()
				if err == nil {
					m.parseAttr(a, attr)
				} else if err == redis.Nil {
					*attr = Attr{Typ: foundType, Parent: parent} // corrupt entry
				} else {
					return err
				}
				*inode = foundIno
			}
			return syscall.EEXIST
		} else if parent == TrashInode {
			if next, err := tx.Incr(ctx, m.nextTrashKey()).Result(); err != nil { // Some inode will be wasted if conflict happens
				return err
			} else {
				*inode = TrashInode + Ino(next)
			}
		}

		mode &= 07777
		if pattr.DefaultACL != aclAPI.None && _type != TypeSymlink {
			// inherit default acl
			if _type == TypeDirectory {
				attr.DefaultACL = pattr.DefaultACL
			}

			// set access acl by parent's default acl
			rule, err := m.getACL(ctx, tx, pattr.DefaultACL)
			if err != nil {
				return err
			}

			if rule.IsMinimal() {
				// simple acl as default
				attr.Mode = mode & (0xFE00 | rule.GetMode())
			} else {
				cRule := rule.ChildAccessACL(mode)
				id, err := m.insertACL(ctx, tx, cRule)
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
				updateParent = true
			}
			if updateParent || now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime {
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(*inode), m.marshal(attr), 0)
			if updateParent {
				pipe.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
			}
			if _type == TypeSymlink {
				pipe.Set(ctx, m.symKey(*inode), path, 0)
			}
			pipe.HSet(ctx, m.entryKey(parent), name, m.packEntry(_type, *inode))
			if _type == TypeDirectory {
				field := (*inode).String()
				pipe.HSet(ctx, m.dirUsedInodesKey(), field, "0")
				pipe.HSet(ctx, m.dirDataLengthKey(), field, "0")
				pipe.HSet(ctx, m.dirUsedSpaceKey(), field, "0")
			}
			pipe.IncrBy(ctx, m.usedSpaceKey(), align4K(0))
			pipe.Incr(ctx, m.totalInodesKey())
			return nil
		})
		return err
	}, m.inodeKey(parent), m.entryKey(parent)))
}

func (m *redisMeta) doUnlink(ctx Context, parent Ino, name string, attr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash, inode Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
	}
	if trash == 0 {
		defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
	}
	if attr == nil {
		attr = &Attr{}
	}
	var _type uint8
	var opened bool
	var newSpace, newInode int64
	err := m.txn(ctx, func(tx *redis.Tx) error {
		opened = false
		*attr = Attr{}
		newSpace, newInode = 0, 0
		buf, err := tx.HGet(ctx, m.entryKey(parent), name).Bytes()
		if err == redis.Nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				name = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
				err = nil
			}
		}
		if err != nil {
			return err
		}
		_type, inode = m.parseEntry(buf)
		if _type == TypeDirectory {
			return syscall.EPERM
		}
		if err := tx.Watch(ctx, m.inodeKey(inode)).Err(); err != nil {
			return err
		}
		rs, err := tx.MGet(ctx, m.inodeKey(parent), m.inodeKey(inode)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil {
			return redis.Nil
		}
		var pattr Attr
		m.parseAttr([]byte(rs[0].(string)), &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if (pattr.Flags&FlagAppend) != 0 || (pattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		var updateParent bool
		now := time.Now()
		if !parent.IsTrash() && now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}
		if rs[1] != nil {
			m.parseAttr([]byte(rs[1].(string)), attr)
			if ctx.Uid() != 0 && pattr.Mode&01000 != 0 && ctx.Uid() != pattr.Uid && ctx.Uid() != attr.Uid {
				return syscall.EACCES
			}
			if (attr.Flags&FlagAppend) != 0 || (attr.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			if (attr.Flags&FlagSkipTrash) != 0 && trash > 0 {
				trash = 0
				defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
			}
			if trash > 0 && attr.Nlink > 1 && tx.HExists(ctx, m.entryKey(trash), m.trashEntry(parent, inode, name)).Val() {
				trash = 0
				defer func() { m.of.InvalidateChunk(inode, invalidateAttrOnly) }()
			}
			attr.Ctime = now.Unix()
			attr.Ctimensec = uint32(now.Nanosecond())
			if trash == 0 {
				attr.Nlink--
				if _type == TypeFile && attr.Nlink == 0 && m.sid > 0 {
					opened = m.of.IsOpen(inode)
				}
			} else if attr.Parent > 0 {
				attr.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", inode, parent, name)
			trash = 0
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, m.entryKey(parent), name)
			if updateParent {
				pipe.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
			}
			if attr.Nlink > 0 {
				pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
				if trash > 0 {
					pipe.HSet(ctx, m.entryKey(trash), m.trashEntry(parent, inode, name), buf)
					if attr.Parent == 0 {
						pipe.HIncrBy(ctx, m.parentKey(inode), trash.String(), 1)
					}
				}
				if attr.Parent == 0 {
					pipe.HIncrBy(ctx, m.parentKey(inode), parent.String(), -1)
				}
			} else {
				switch _type {
				case TypeFile:
					if opened {
						pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
						pipe.SAdd(ctx, m.sustained(m.sid), strconv.Itoa(int(inode)))
					} else {
						pipe.ZAdd(ctx, m.delfiles(), redis.Z{Score: float64(now.Unix()), Member: m.toDelete(inode, attr.Length)})
						pipe.Del(ctx, m.inodeKey(inode))
						newSpace, newInode = -align4K(attr.Length), -1
						pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
						pipe.Decr(ctx, m.totalInodesKey())
					}
				case TypeSymlink:
					pipe.Del(ctx, m.symKey(inode))
					fallthrough
				default:
					pipe.Del(ctx, m.inodeKey(inode))
					newSpace, newInode = -align4K(0), -1
					pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
					pipe.Decr(ctx, m.totalInodesKey())
				}
				pipe.Del(ctx, m.xattrKey(inode))
				if attr.Parent == 0 {
					pipe.Del(ctx, m.parentKey(inode))
				}
			}
			return nil
		})

		return err
	}, m.inodeKey(parent), m.entryKey(parent))
	if err == nil && trash == 0 {
		if _type == TypeFile && attr.Nlink == 0 {
			m.fileDeleted(opened, parent.IsTrash(), inode, attr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *redisMeta) doRmdir(ctx Context, parent Ino, name string, pinode *Ino, oldAttr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
	}
	err := m.txn(ctx, func(tx *redis.Tx) error {
		buf, err := tx.HGet(ctx, m.entryKey(parent), name).Bytes()
		if err == redis.Nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				name = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
				err = nil
			}
		}
		if err != nil {
			return err
		}
		typ, inode := m.parseEntry(buf)
		if typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if pinode != nil {
			*pinode = inode
		}
		if err = tx.Watch(ctx, m.inodeKey(inode), m.entryKey(inode)).Err(); err != nil {
			return err
		}

		rs, err := tx.MGet(ctx, m.inodeKey(parent), m.inodeKey(inode)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil {
			return redis.Nil
		}
		var pattr, attr Attr
		m.parseAttr([]byte(rs[0].(string)), &pattr)
		if pattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if (pattr.Flags&FlagAppend) != 0 || (pattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		now := time.Now()
		pattr.Nlink--
		pattr.Mtime = now.Unix()
		pattr.Mtimensec = uint32(now.Nanosecond())
		pattr.Ctime = now.Unix()
		pattr.Ctimensec = uint32(now.Nanosecond())

		cnt, err := tx.HLen(ctx, m.entryKey(inode)).Result()
		if err != nil {
			return err
		}
		if cnt > 0 {
			return syscall.ENOTEMPTY
		}
		if rs[1] != nil {
			m.parseAttr([]byte(rs[1].(string)), &attr)
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, m.entryKey(parent), name)
			if !parent.IsTrash() {
				pipe.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
			}
			if trash > 0 {
				pipe.Set(ctx, m.inodeKey(inode), m.marshal(&attr), 0)
				pipe.HSet(ctx, m.entryKey(trash), m.trashEntry(parent, inode, name), buf)
			} else {
				pipe.Del(ctx, m.inodeKey(inode))
				pipe.Del(ctx, m.xattrKey(inode))
				pipe.IncrBy(ctx, m.usedSpaceKey(), -align4K(0))
				pipe.Decr(ctx, m.totalInodesKey())
			}

			field := inode.String()
			pipe.HDel(ctx, m.dirDataLengthKey(), field)
			pipe.HDel(ctx, m.dirUsedSpaceKey(), field)
			pipe.HDel(ctx, m.dirUsedInodesKey(), field)
			pipe.HDel(ctx, m.dirQuotaKey(), field)
			pipe.HDel(ctx, m.dirQuotaUsedSpaceKey(), field)
			pipe.HDel(ctx, m.dirQuotaUsedInodesKey(), field)
			return nil
		})
		return err
	}, m.inodeKey(parent), m.entryKey(parent))
	if err == nil && trash == 0 {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *redisMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode, tInode *Ino, attr, tAttr *Attr) syscall.Errno {
	exchange := flags == RenameExchange
	var opened bool
	var trash, dino Ino
	var dtyp uint8
	var tattr Attr
	var newSpace, newInode int64
	keys := []string{m.inodeKey(parentSrc), m.entryKey(parentSrc), m.inodeKey(parentDst), m.entryKey(parentDst)}
	if parentSrc.IsTrash() {
		// lock the parentDst
		keys[0], keys[2] = keys[2], keys[0]
	}
	err := m.txn(ctx, func(tx *redis.Tx) error {
		opened = false
		dino, dtyp = 0, 0
		tattr = Attr{}
		newSpace, newInode = 0, 0
		buf, err := tx.HGet(ctx, m.entryKey(parentSrc), nameSrc).Bytes()
		if err == redis.Nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentSrc, nameSrc); e != nil {
				nameSrc = string(e.Name)
				buf = m.packEntry(e.Attr.Typ, e.Inode)
				err = nil
			}
		}
		if err != nil {
			return err
		}
		typ, ino := m.parseEntry(buf)
		if parentSrc == parentDst && nameSrc == nameDst {
			if inode != nil {
				*inode = ino
			}
			return nil
		}
		keys := []string{m.inodeKey(ino)}

		dbuf, err := tx.HGet(ctx, m.entryKey(parentDst), nameDst).Bytes()
		if err == redis.Nil && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				if (nameSrc != string(e.Name)) || parentDst != parentSrc {
					nameDst = string(e.Name)
					dbuf = m.packEntry(e.Attr.Typ, e.Inode)
					err = nil
				}
			}
		}
		if err != nil && err != redis.Nil {
			return err
		}
		if err == nil {
			if flags&RenameNoReplace != 0 {
				return syscall.EEXIST
			}
			dtyp, dino = m.parseEntry(dbuf)
			keys = append(keys, m.inodeKey(dino))
			if dtyp == TypeDirectory {
				keys = append(keys, m.entryKey(dino))
			}
			if !exchange {
				if st := m.checkTrash(parentDst, &trash); st != 0 {
					return st
				}
			}
		}
		if err := tx.Watch(ctx, keys...).Err(); err != nil {
			return err
		}
		if dino > 0 {
			if ino == dino {
				return errno(nil)
			}
			if exchange {
			} else if typ == TypeDirectory && dtyp != TypeDirectory {
				return syscall.ENOTDIR
			} else if typ != TypeDirectory && dtyp == TypeDirectory {
				return syscall.EISDIR
			}
		}

		keys = []string{m.inodeKey(parentSrc), m.inodeKey(parentDst), m.inodeKey(ino)}
		if dino > 0 {
			keys = append(keys, m.inodeKey(dino))
		}
		rs, err := tx.MGet(ctx, keys...).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil || rs[1] == nil || rs[2] == nil {
			return redis.Nil
		}
		var sattr, dattr, iattr Attr
		m.parseAttr([]byte(rs[0].(string)), &sattr)
		if sattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		if st := m.Access(ctx, parentSrc, MODE_MASK_W|MODE_MASK_X, &sattr); st != 0 {
			return st
		}
		m.parseAttr([]byte(rs[1].(string)), &dattr)
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
		m.parseAttr([]byte(rs[2].(string)), &iattr)
		if (sattr.Flags&FlagAppend) != 0 || (sattr.Flags&FlagImmutable) != 0 || (dattr.Flags&FlagImmutable) != 0 || (iattr.Flags&FlagAppend) != 0 || (iattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if parentSrc != parentDst && sattr.Mode&0o1000 != 0 && ctx.Uid() != 0 &&
			ctx.Uid() != iattr.Uid && (ctx.Uid() != sattr.Uid || iattr.Typ == TypeDirectory) {
			return syscall.EACCES
		}

		var supdate, dupdate bool
		now := time.Now()
		if dino > 0 {
			if rs[3] == nil {
				logger.Warnf("no attribute for inode %d (%d, %s)", dino, parentDst, nameDst)
				trash = 0
			} else {
				m.parseAttr([]byte(rs[3].(string)), &tattr)
			}
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
						supdate, dupdate = true, true
					} else if tattr.Parent > 0 {
						tattr.Parent = parentSrc
					}
				}
			} else {
				if dtyp == TypeDirectory {
					cnt, err := tx.HLen(ctx, m.entryKey(dino)).Result()
					if err != nil {
						return err
					}
					if cnt != 0 {
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
				supdate, dupdate = true, true
			} else if iattr.Parent > 0 {
				iattr.Parent = parentDst
			}
		}
		if supdate || now.Sub(time.Unix(sattr.Mtime, int64(sattr.Mtimensec))) >= m.conf.SkipDirMtime {
			sattr.Mtime = now.Unix()
			sattr.Mtimensec = uint32(now.Nanosecond())
			sattr.Ctime = now.Unix()
			sattr.Ctimensec = uint32(now.Nanosecond())
			supdate = true
		}
		if dupdate || now.Sub(time.Unix(dattr.Mtime, int64(dattr.Mtimensec))) >= m.conf.SkipDirMtime {
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

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			if exchange { // dbuf, tattr are valid
				pipe.Set(ctx, m.inodeKey(dino), m.marshal(&tattr), 0)
				pipe.HSet(ctx, m.entryKey(parentSrc), nameSrc, dbuf)
				if parentSrc != parentDst && tattr.Parent == 0 {
					pipe.HIncrBy(ctx, m.parentKey(dino), parentSrc.String(), 1)
					pipe.HIncrBy(ctx, m.parentKey(dino), parentDst.String(), -1)
				}
			} else {
				pipe.HDel(ctx, m.entryKey(parentSrc), nameSrc)
				if dino > 0 {
					if trash > 0 {
						pipe.Set(ctx, m.inodeKey(dino), m.marshal(&tattr), 0)
						pipe.HSet(ctx, m.entryKey(trash), m.trashEntry(parentDst, dino, nameDst), dbuf)
						if tattr.Parent == 0 {
							pipe.HIncrBy(ctx, m.parentKey(dino), trash.String(), 1)
							pipe.HIncrBy(ctx, m.parentKey(dino), parentDst.String(), -1)
						}
					} else if dtyp != TypeDirectory && tattr.Nlink > 0 {
						pipe.Set(ctx, m.inodeKey(dino), m.marshal(&tattr), 0)
						if tattr.Parent == 0 {
							pipe.HIncrBy(ctx, m.parentKey(dino), parentDst.String(), -1)
						}
					} else {
						if dtyp == TypeFile {
							if opened {
								pipe.Set(ctx, m.inodeKey(dino), m.marshal(&tattr), 0)
								pipe.SAdd(ctx, m.sustained(m.sid), strconv.Itoa(int(dino)))
							} else {
								pipe.ZAdd(ctx, m.delfiles(), redis.Z{Score: float64(now.Unix()), Member: m.toDelete(dino, tattr.Length)})
								pipe.Del(ctx, m.inodeKey(dino))
								newSpace, newInode = -align4K(tattr.Length), -1
								pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
								pipe.Decr(ctx, m.totalInodesKey())
							}
						} else {
							if dtyp == TypeSymlink {
								pipe.Del(ctx, m.symKey(dino))
							}
							pipe.Del(ctx, m.inodeKey(dino))
							newSpace, newInode = -align4K(0), -1
							pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
							pipe.Decr(ctx, m.totalInodesKey())
						}
						pipe.Del(ctx, m.xattrKey(dino))
						if tattr.Parent == 0 {
							pipe.Del(ctx, m.parentKey(dino))
						}
					}
					if dtyp == TypeDirectory {
						field := dino.String()
						pipe.HDel(ctx, m.dirQuotaKey(), field)
						pipe.HDel(ctx, m.dirQuotaUsedSpaceKey(), field)
						pipe.HDel(ctx, m.dirQuotaUsedInodesKey(), field)
					}
				}
			}
			if parentDst != parentSrc {
				if !parentSrc.IsTrash() && supdate {
					pipe.Set(ctx, m.inodeKey(parentSrc), m.marshal(&sattr), 0)
				}
				if iattr.Parent == 0 {
					pipe.HIncrBy(ctx, m.parentKey(ino), parentDst.String(), 1)
					pipe.HIncrBy(ctx, m.parentKey(ino), parentSrc.String(), -1)
				}
			}
			pipe.Set(ctx, m.inodeKey(ino), m.marshal(&iattr), 0)
			pipe.HSet(ctx, m.entryKey(parentDst), nameDst, buf)
			if dupdate {
				pipe.Set(ctx, m.inodeKey(parentDst), m.marshal(&dattr), 0)
			}
			return nil
		})
		return err
	}, keys...)
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dtyp == TypeFile && tattr.Nlink == 0 {
			m.fileDeleted(opened, false, dino, tattr.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *redisMeta) doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		rs, err := tx.MGet(ctx, m.inodeKey(parent), m.inodeKey(inode)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		var pattr, iattr Attr
		m.parseAttr([]byte(rs[0].(string)), &pattr)
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
		var updateParent bool
		now := time.Now()
		if now.Sub(time.Unix(pattr.Mtime, int64(pattr.Mtimensec))) >= m.conf.SkipDirMtime {
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			updateParent = true
		}
		m.parseAttr([]byte(rs[1].(string)), &iattr)
		if iattr.Typ == TypeDirectory {
			return syscall.EPERM
		}
		if (iattr.Flags&FlagAppend) != 0 || (iattr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		oldParent := iattr.Parent
		iattr.Parent = 0
		iattr.Ctime = now.Unix()
		iattr.Ctimensec = uint32(now.Nanosecond())
		iattr.Nlink++

		err = tx.HGet(ctx, m.entryKey(parent), name).Err()
		if err != nil && err != redis.Nil {
			return err
		} else if err == nil {
			return syscall.EEXIST
		} else if err == redis.Nil && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, m.entryKey(parent), name, m.packEntry(iattr.Typ, inode))
			if updateParent {
				pipe.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
			}
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(&iattr), 0)
			if oldParent > 0 {
				pipe.HIncrBy(ctx, m.parentKey(inode), oldParent.String(), 1)
			}
			pipe.HIncrBy(ctx, m.parentKey(inode), parent.String(), 1)
			return nil
		})
		if err == nil && attr != nil {
			*attr = iattr
		}
		return err
	}, m.inodeKey(parent), m.entryKey(parent), m.inodeKey(inode)))
}

func (m *redisMeta) fillAttr(ctx Context, es []*Entry) error {
	if len(es) == 0 {
		return nil
	}
	var keys = make([]string, len(es))
	for i, e := range es {
		keys[i] = m.inodeKey(e.Inode)
	}
	rs, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	for j, re := range rs {
		if re != nil {
			if a, ok := re.(string); ok {
				m.parseAttr([]byte(a), es[j].Attr)
				m.of.Update(es[j].Inode, es[j].Attr)
			}
		}
	}
	return nil
}

func (m *redisMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	var stop = errors.New("stop")
	err := m.hscan(ctx, m.entryKey(inode), func(keys []string) error {
		newEntries := make([]Entry, len(keys)/2)
		newAttrs := make([]Attr, len(keys)/2)
		for i := 0; i < len(keys); i += 2 {
			typ, ino := m.parseEntry([]byte(keys[i+1]))
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
			if limit > 0 && len(*entries) >= limit {
				return stop
			}
		}
		return nil
	})
	if errors.Is(err, stop) {
		err = nil
	}
	if err != nil {
		return errno(err)
	}

	if plus != 0 && len(*entries) != 0 {
		batchSize := 4096
		nEntries := len(*entries)
		if nEntries <= batchSize {
			err = m.fillAttr(ctx, *entries)
		} else {
			indexCh := make(chan []*Entry, 10)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for es := range indexCh {
						e := m.fillAttr(ctx, es)
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

func (m *redisMeta) doCleanStaleSession(sid uint64) error {
	var fail bool
	// release locks
	var ctx = Background()
	ssid := strconv.FormatInt(int64(sid), 10)
	key := m.lockedKey(sid)
	if inodes, err := m.rdb.SMembers(ctx, key).Result(); err == nil {
		for _, k := range inodes {
			owners, err := m.rdb.HKeys(ctx, k).Result()
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
				if err = m.rdb.HDel(ctx, k, fields...).Err(); err != nil {
					logger.Warnf("HDel %s %s: %s", k, fields, err)
					fail = true
					continue
				}
			}
			if err = m.rdb.SRem(ctx, key, k).Err(); err != nil {
				logger.Warnf("SRem %s %s: %s", key, k, err)
				fail = true
			}
		}
	} else {
		logger.Warnf("SMembers %s: %s", key, err)
		fail = true
	}

	key = m.sustained(sid)
	if inodes, err := m.rdb.SMembers(ctx, key).Result(); err == nil {
		for _, sinode := range inodes {
			inode, _ := strconv.ParseUint(sinode, 10, 64)
			if err = m.doDeleteSustainedInode(sid, Ino(inode)); err != nil {
				logger.Warnf("Delete sustained inode %d of sid %d: %s", inode, sid, err)
				fail = true
			}
		}
	} else {
		logger.Warnf("SMembers %s: %s", key, err)
		fail = true
	}

	if !fail {
		if err := m.rdb.HDel(ctx, m.sessionInfos(), ssid).Err(); err != nil {
			logger.Warnf("HDel sessionInfos %s: %s", ssid, err)
			fail = true
		}
	}
	if fail {
		return fmt.Errorf("failed to clean up sid %d", sid)
	} else {
		if n, err := m.rdb.ZRem(ctx, m.allSessions(), ssid).Result(); err != nil {
			return err
		} else if n == 1 {
			return nil
		}
		return m.rdb.ZRem(ctx, legacySessions, ssid).Err()
	}
}

func (m *redisMeta) doFindStaleSessions(limit int) ([]uint64, error) {
	vals, err := m.rdb.ZRangeByScore(Background(), m.allSessions(), &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(time.Now().Unix(), 10),
		Count: int64(limit)}).Result()
	if err != nil {
		return nil, err
	}
	sids := make([]uint64, len(vals))
	for i, v := range vals {
		sids[i], _ = strconv.ParseUint(v, 10, 64)
	}
	limit -= len(sids)
	if limit <= 0 {
		return sids, nil
	}

	// check clients with version before 1.0-beta3 as well
	vals, err = m.rdb.ZRangeByScore(Background(), legacySessions, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(time.Now().Add(time.Minute*-5).Unix(), 10),
		Count: int64(limit)}).Result()
	if err != nil {
		logger.Errorf("Scan stale legacy sessions: %s", err)
		return sids, nil
	}
	for _, v := range vals {
		sid, _ := strconv.ParseUint(v, 10, 64)
		sids = append(sids, sid)
	}
	return sids, nil
}

func (m *redisMeta) doRefreshSession() error {
	ctx := Background()
	ssid := strconv.FormatUint(m.sid, 10)
	// we have to check sessionInfo here because the operations are not within a transaction
	ok, err := m.rdb.HExists(ctx, m.sessionInfos(), ssid).Result()
	if err == nil && !ok {
		logger.Warnf("Session %d was stale and cleaned up, but now it comes back again", m.sid)
		err = m.rdb.HSet(ctx, m.sessionInfos(), m.sid, m.newSessionInfo()).Err()
	}
	if err != nil {
		return err
	}
	return m.rdb.ZAdd(ctx, m.allSessions(), redis.Z{
		Score:  float64(m.expireTime()),
		Member: ssid}).Err()
}

func (m *redisMeta) doDeleteSustainedInode(sid uint64, inode Ino) error {
	var attr Attr
	var ctx = Background()
	var newSpace int64
	err := m.txn(ctx, func(tx *redis.Tx) error {
		newSpace = 0
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err == redis.Nil {
			return nil
		}
		if err != nil {
			return err
		}
		m.parseAttr(a, &attr)
		newSpace = -align4K(attr.Length)
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.ZAdd(ctx, m.delfiles(), redis.Z{Score: float64(time.Now().Unix()), Member: m.toDelete(inode, attr.Length)})
			pipe.Del(ctx, m.inodeKey(inode))
			pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
			pipe.Decr(ctx, m.totalInodesKey())
			pipe.SRem(ctx, m.sustained(sid), strconv.Itoa(int(inode)))
			return nil
		})
		return err
	}, m.inodeKey(inode))
	if err == nil && newSpace < 0 {
		m.updateStats(newSpace, -1)
		m.tryDeleteFileData(inode, attr.Length, false)
	}
	return err
}

func (m *redisMeta) doRead(ctx Context, inode Ino, indx uint32) ([]*slice, syscall.Errno) {
	vals, err := m.rdb.LRange(ctx, m.chunkKey(inode, indx), 0, -1).Result()
	if err != nil {
		return nil, errno(err)
	}
	return readSlices(vals), 0
}

func (m *redisMeta) doWrite(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time, numSlices *int, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		*delta = dirStat{}
		*attr = Attr{}
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		m.parseAttr(a, attr)
		if attr.Typ != TypeFile {
			return syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		if newleng > attr.Length {
			delta.length = int64(newleng - attr.Length)
			delta.space = align4K(newleng) - align4K(attr.Length)
			attr.Length = newleng
		}
		if err := m.checkQuota(ctx, delta.space, 0, attr.Uid, attr.Gid, m.getParents(ctx, tx, inode, attr.Parent)...); err != 0 {
			return err
		}
		now := time.Now()
		attr.Mtime = mtime.Unix()
		attr.Mtimensec = uint32(mtime.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())

		var rpush *redis.IntCmd
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			rpush = pipe.RPush(ctx, m.chunkKey(inode, indx), marshalSlice(off, slice.Id, slice.Size, slice.Off, slice.Len))
			// most of chunk are used by single inode, so use that as the default (1 == not exists)
			// pipe.Incr(ctx, r.sliceKey(slice.ID, slice.Size))
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
			if delta.space > 0 {
				pipe.IncrBy(ctx, m.usedSpaceKey(), delta.space)
			}
			return nil
		})
		if err == nil {
			*numSlices = int(rpush.Val())
		}
		return err
	}, m.inodeKey(inode)))
}

func (m *redisMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied, outLength *uint64) syscall.Errno {
	defer m.timeit("CopyFileRange", time.Now())
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	var newLength, newSpace int64
	var sattr, attr Attr
	defer func() { m.of.InvalidateChunk(fout, invalidateAllChunks) }()
	err := m.txn(ctx, func(tx *redis.Tx) error {
		newLength, newSpace = 0, 0
		rs, err := tx.MGet(ctx, m.inodeKey(fin), m.inodeKey(fout)).Result()
		if err != nil {
			return err
		}
		if rs[0] == nil || rs[1] == nil {
			return redis.Nil
		}
		sattr = Attr{}
		m.parseAttr([]byte(rs[0].(string)), &sattr)
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
		m.parseAttr([]byte(rs[1].(string)), &attr)
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
		if err := m.checkQuota(ctx, newSpace, 0, attr.Uid, attr.Gid, m.getParents(ctx, tx, fout, attr.Parent)...); err != 0 {
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

		var vals [][]string
		for i := offIn / ChunkSize; i <= (offIn+size)/ChunkSize; i++ {
			val, err := tx.LRange(ctx, m.chunkKey(fin, uint32(i)), 0, -1).Result()
			if err != nil {
				return err
			}
			vals = append(vals, val)
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			coff := offIn / ChunkSize * ChunkSize
			for _, sv := range vals {
				// Add a zero chunk for hole
				ss := readSlices(sv)
				if ss == nil {
					return syscall.EIO
				}
				ss = append([]*slice{{len: ChunkSize}}, ss...)
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
							pipe.RPush(ctx, m.chunkKey(fout, indx), marshalSlice(dpos, s.Id, s.Size, s.Off, ChunkSize-dpos))
							if s.Id > 0 {
								pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.Id, s.Size), 1)
							}

							skip := ChunkSize - dpos
							pipe.RPush(ctx, m.chunkKey(fout, indx+1), marshalSlice(0, s.Id, s.Size, s.Off+skip, s.Len-skip))
							if s.Id > 0 {
								pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.Id, s.Size), 1)
							}
						} else {
							pipe.RPush(ctx, m.chunkKey(fout, indx), marshalSlice(dpos, s.Id, s.Size, s.Off, s.Len))
							if s.Id > 0 {
								pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.Id, s.Size), 1)
							}
						}
					}
				}
				coff += ChunkSize
			}
			pipe.Set(ctx, m.inodeKey(fout), m.marshal(&attr), 0)
			if newSpace > 0 {
				pipe.IncrBy(ctx, m.usedSpaceKey(), newSpace)
			}
			return nil
		})
		if err == nil {
			if copied != nil {
				*copied = size
			}
		}
		return err
	}, m.inodeKey(fout), m.inodeKey(fin))
	if err == nil {
		m.updateParentStat(ctx, fout, attr.Parent, newLength, newSpace)
	}
	return errno(err)
}

func (m *redisMeta) getParents(ctx Context, tx *redis.Tx, inode, parent Ino) []Ino {
	if parent > 0 {
		return []Ino{parent}
	}
	vals, err := tx.HGetAll(ctx, m.parentKey(inode)).Result()
	if err != nil {
		logger.Warnf("Scan parent key of inode %d: %s", inode, err)
		return nil
	}
	ps := make([]Ino, 0, len(vals))
	for k, v := range vals {
		if n, _ := strconv.Atoi(v); n > 0 {
			ino, _ := strconv.ParseUint(k, 10, 64)
			ps = append(ps, Ino(ino))
		}
	}
	return ps
}

func (m *redisMeta) doGetParents(ctx Context, inode Ino) map[Ino]int {
	vals, err := m.rdb.HGetAll(ctx, m.parentKey(inode)).Result()
	if err != nil {
		logger.Warnf("Scan parent key of inode %d: %s", inode, err)
		return nil
	}
	ps := make(map[Ino]int)
	for k, v := range vals {
		if n, _ := strconv.Atoi(v); n > 0 {
			ino, _ := strconv.ParseUint(k, 10, 64)
			ps[Ino(ino)] = n
		}
	}
	return ps
}

func (m *redisMeta) doSyncDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
	if m.conf.ReadOnly {
		return nil, syscall.EROFS
	}
	field := ino.String()
	stat, st := m.calcDirStat(ctx, ino)
	if st != 0 {
		return nil, st
	}
	err := m.txn(ctx, func(tx *redis.Tx) error {
		n, err := tx.Exists(ctx, m.inodeKey(ino)).Result()
		if err != nil {
			return err
		}
		if n <= 0 {
			return syscall.ENOENT
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, m.dirDataLengthKey(), field, stat.length)
			pipe.HSet(ctx, m.dirUsedSpaceKey(), field, stat.space)
			pipe.HSet(ctx, m.dirUsedInodesKey(), field, stat.inodes)
			return nil
		})
		return err
	}, m.inodeKey(ino))
	return stat, errno(err)
}

func (m *redisMeta) doUpdateDirStat(ctx Context, batch map[Ino]dirStat) error {
	spaceKey := m.dirUsedSpaceKey()
	lengthKey := m.dirDataLengthKey()
	inodesKey := m.dirUsedInodesKey()
	nonexist := make(map[Ino]bool, 0)
	statList := make([]Ino, 0, len(batch))
	pipeline := m.rdb.Pipeline()
	for ino := range batch {
		pipeline.HExists(ctx, spaceKey, ino.String())
		statList = append(statList, ino)
	}
	rets, err := pipeline.Exec(ctx)
	if err != nil {
		return err
	}
	for i, ret := range rets {
		if ret.Err() != nil {
			return ret.Err()
		}
		if exist, _ := ret.(*redis.BoolCmd).Result(); !exist {
			nonexist[statList[i]] = true
		}
	}
	if len(nonexist) > 0 {
		wg := m.parallelSyncDirStat(ctx, nonexist)
		defer wg.Wait()
	}

	for _, group := range m.groupBatch(batch, 1000) {
		_, err := m.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, ino := range group {
				field := ino.String()
				if nonexist[ino] {
					continue
				}
				stat := batch[ino]
				if stat.length != 0 {
					pipe.HIncrBy(ctx, lengthKey, field, stat.length)
				}
				if stat.space != 0 {
					pipe.HIncrBy(ctx, spaceKey, field, stat.space)
				}
				if stat.inodes != 0 {
					pipe.HIncrBy(ctx, inodesKey, field, stat.inodes)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *redisMeta) doGetDirStat(ctx Context, ino Ino, trySync bool) (*dirStat, syscall.Errno) {
	field := ino.String()
	dataLength, errLength := m.rdb.HGet(ctx, m.dirDataLengthKey(), field).Int64()
	if errLength != nil && errLength != redis.Nil {
		return nil, errno(errLength)
	}
	usedSpace, errSpace := m.rdb.HGet(ctx, m.dirUsedSpaceKey(), field).Int64()
	if errSpace != nil && errSpace != redis.Nil {
		return nil, errno(errSpace)
	}
	usedInodes, errInodes := m.rdb.HGet(ctx, m.dirUsedInodesKey(), field).Int64()
	if errInodes != nil && errSpace != redis.Nil {
		return nil, errno(errInodes)
	}
	if errLength != redis.Nil && errSpace != redis.Nil && errInodes != redis.Nil {
		if trySync && (dataLength < 0 || usedSpace < 0 || usedInodes < 0) {
			return m.doSyncDirStat(ctx, ino)
		}
		return &dirStat{dataLength, usedSpace, usedInodes}, 0
	}

	if trySync {
		return m.doSyncDirStat(ctx, ino)
	}
	return nil, 0
}

// For now only deleted files
func (m *redisMeta) cleanupLegacies() {
	for {
		utils.SleepWithJitter(time.Minute)
		rng := &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10), Count: 1000}
		vals, err := m.rdb.ZRangeByScore(Background(), m.delfiles(), rng).Result()
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
				m.doDeleteFileData_(Ino(inode), length, v)
				count++
			}
		}
		if count == 0 {
			return
		}
	}
}

func (m *redisMeta) doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) {
	rng := &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(ts, 10), Count: int64(limit)}
	vals, err := m.rdb.ZRangeByScore(Background(), m.delfiles(), rng).Result()
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

func (m *redisMeta) doCleanupSlices(ctx Context) {
	_ = m.hscan(ctx, m.sliceRefs(), func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			key, val := keys[i], keys[i+1]
			if strings.HasPrefix(val, "-") { // < 0
				ps := strings.Split(key, "_")
				if len(ps) == 2 {
					id, _ := strconv.ParseUint(ps[0][1:], 10, 64)
					size, _ := strconv.ParseUint(ps[1], 10, 32)
					if id > 0 && size > 0 {
						m.deleteSlice(id, uint32(size))
					}
				}
			} else if val == "0" {
				m.cleanupZeroRef(key)
			}
			if ctx.Canceled() {
				return ctx.Err()
			}
		}
		return nil
	})
}

func (m *redisMeta) cleanupZeroRef(key string) {
	var ctx = Background()
	_ = m.txn(ctx, func(tx *redis.Tx) error {
		v, err := tx.HGet(ctx, m.sliceRefs(), key).Int()
		if err != nil && err != redis.Nil {
			return err
		}
		if v != 0 {
			return syscall.EINVAL
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HDel(ctx, m.sliceRefs(), key)
			return nil
		})
		return err
	}, m.sliceRefs())
}

func (m *redisMeta) cleanupLeakedChunks(delete bool) {
	var ctx = Background()
	prefix := len(m.prefix)
	_ = m.scan(ctx, "c*", func(ckeys []string) error {
		var ikeys []string
		var rs []*redis.IntCmd
		p := m.rdb.Pipeline()
		for _, k := range ckeys {
			ps := strings.Split(k, "_")
			if len(ps) != 2 {
				continue
			}
			ino, _ := strconv.ParseUint(ps[0][prefix+1:], 10, 64)
			ikeys = append(ikeys, k)
			rs = append(rs, p.Exists(ctx, m.inodeKey(Ino(ino))))
		}
		if len(rs) > 0 {
			cmds, err := p.Exec(ctx)
			if err != nil {
				for _, c := range cmds {
					if c.Err() != nil {
						logger.Errorf("Check inodes with command %s: %s", c.String(), c.Err())
					}
				}
				return err
			}
			for i, rr := range rs {
				if rr.Val() == 0 {
					key := ikeys[i]
					logger.Infof("found leaked chunk %s", key)
					if delete {
						ps := strings.Split(key, "_")
						ino, _ := strconv.ParseUint(ps[0][prefix+1:], 10, 64)
						indx, _ := strconv.Atoi(ps[1])
						_ = m.deleteChunk(Ino(ino), uint32(indx))
					}
				}
			}
		}
		return nil
	})
}

func (m *redisMeta) cleanupOldSliceRefs(delete bool) {
	var ctx = Background()
	_ = m.scan(ctx, "k*", func(ckeys []string) error {
		values, err := m.rdb.MGet(ctx, ckeys...).Result()
		if err != nil {
			logger.Warnf("mget slices: %s", err)
			return err
		}
		var todel []string
		for i, v := range values {
			if v == nil {
				continue
			}
			if strings.HasPrefix(v.(string), m.prefix+"-") || v == "0" { // < 0
				// the objects will be deleted by gc
				todel = append(todel, ckeys[i])
			} else {
				vv, _ := strconv.Atoi(v.(string))
				m.rdb.HIncrBy(ctx, m.sliceRefs(), ckeys[i], int64(vv))
				m.rdb.DecrBy(ctx, ckeys[i], int64(vv))
				logger.Infof("move refs %d for slice %s", vv, ckeys[i])
			}
		}
		if delete && len(todel) > 0 {
			m.rdb.Del(ctx, todel...)
		}
		return nil
	})
}

func (m *redisMeta) toDelete(inode Ino, length uint64) string {
	return inode.String() + ":" + strconv.Itoa(int(length))
}

func (m *redisMeta) deleteChunk(inode Ino, indx uint32) error {
	var ctx = Background()
	key := m.chunkKey(inode, indx)
	var todel []*slice
	var rs []*redis.IntCmd
	err := m.txn(ctx, func(tx *redis.Tx) error {
		todel = todel[:0]
		rs = rs[:0]
		vals, err := tx.LRange(ctx, key, 0, -1).Result()
		if err != nil || len(vals) == 0 {
			return err
		}
		slices := readSlices(vals)
		if slices == nil {
			logger.Errorf("Corrupt value for inode %d chunk index %d, use `gc` to clean up leaked slices", inode, indx)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, key)
			for _, s := range slices {
				if s.id > 0 {
					todel = append(todel, s)
					rs = append(rs, pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.id, s.size), -1))
				}
			}
			return nil
		})
		return err
	}, key)
	if err != nil {
		return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", key, err)
	}
	for i, s := range todel {
		if rs[i].Val() < 0 {
			m.deleteSlice(s.id, s.size)
		}
	}
	return nil
}

func (m *redisMeta) doDeleteFileData(inode Ino, length uint64) {
	m.doDeleteFileData_(inode, length, "")
}

func (m *redisMeta) doDeleteFileData_(inode Ino, length uint64, tracking string) {
	var ctx = Background()
	var indx uint32
	p := m.rdb.Pipeline()
	for uint64(indx)*ChunkSize < length {
		var keys []string
		for i := 0; uint64(indx)*ChunkSize < length && i < 1000; i++ {
			key := m.chunkKey(inode, indx)
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
			idx, _ := strconv.Atoi(strings.Split(keys[i][len(m.prefix):], "_")[1])
			err = m.deleteChunk(inode, uint32(idx))
			if err != nil {
				logger.Warnf("delete chunk %s: %s", keys[i], err)
				return
			}
		}
	}
	if tracking == "" {
		tracking = inode.String() + ":" + strconv.FormatInt(int64(length), 10)
	}
	_ = m.rdb.ZRem(ctx, m.delfiles(), tracking)
}

func (r *redisMeta) doCleanupDelayedSlices(ctx Context, edge int64) (int, error) {
	var count int
	var ss []Slice
	var rs []*redis.IntCmd
	err := r.hscan(ctx, r.delSlices(), func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			if ctx.Canceled() {
				return ctx.Err()
			}
			key := keys[i]
			ps := strings.Split(key, "_")
			if len(ps) != 2 {
				logger.Warnf("Invalid key %s", key)
				continue
			}
			if ts, e := strconv.ParseUint(ps[1], 10, 64); e != nil {
				logger.Warnf("Invalid key %s", key)
				continue
			} else if ts >= uint64(edge) {
				continue
			}

			if err := r.txn(ctx, func(tx *redis.Tx) error {
				ss, rs = ss[:0], rs[:0]
				val, e := tx.HGet(ctx, r.delSlices(), key).Result()
				if e == redis.Nil {
					return nil
				} else if e != nil {
					return e
				}
				buf := []byte(val)
				r.decodeDelayedSlices(buf, &ss)
				if len(ss) == 0 {
					return fmt.Errorf("invalid value for delSlices %s: %v", key, buf)
				}
				_, e = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					for _, s := range ss {
						rs = append(rs, pipe.HIncrBy(ctx, r.sliceRefs(), r.sliceKey(s.Id, s.Size), -1))
					}
					pipe.HDel(ctx, r.delSlices(), key)
					return nil
				})
				return e
			}, r.delSlices()); err != nil {
				logger.Warnf("Cleanup delSlices %s: %s", key, err)
				continue
			}
			for i, s := range ss {
				if rs[i].Err() == nil && rs[i].Val() < 0 {
					r.deleteSlice(s.Id, s.Size)
					count++
				}
				if ctx.Canceled() {
					return ctx.Err()
				}
			}
		}
		return nil
	})
	if errors.Is(err, context.DeadlineExceeded) {
		err = nil
	}
	return count, err
}

func (m *redisMeta) doCompactChunk(inode Ino, indx uint32, origin []byte, ss []*slice, skipped int, pos uint32, id uint64, size uint32, delayed []byte) syscall.Errno {
	var rs []*redis.IntCmd // trash disabled: check reference of slices
	if delayed == nil {
		rs = make([]*redis.IntCmd, len(ss))
	}
	key := m.chunkKey(inode, indx)
	ctx := Background()
	st := errno(m.txn(ctx, func(tx *redis.Tx) error {
		n := len(origin) / sliceBytes
		vals2, err := tx.LRange(ctx, key, 0, int64(n-1)).Result()
		if err != nil {
			return err
		}
		if len(vals2) != n {
			return syscall.EINVAL
		}
		for i, val := range vals2 {
			if val != string(origin[i*sliceBytes:(i+1)*sliceBytes]) {
				return syscall.EINVAL
			}
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.LTrim(ctx, key, int64(n), -1)
			pipe.LPush(ctx, key, marshalSlice(pos, id, size, 0, size))
			for i := skipped; i > 0; i-- {
				pipe.LPush(ctx, key, origin[(i-1)*sliceBytes:i*sliceBytes])
			}
			pipe.HSet(ctx, m.sliceRefs(), m.sliceKey(id, size), "0") // create the key to tracking it
			if delayed != nil {
				if len(delayed) > 0 {
					pipe.HSet(ctx, m.delSlices(), fmt.Sprintf("%d_%d", id, time.Now().Unix()), delayed)
				}
			} else {
				for i, s := range ss {
					if s.id > 0 {
						rs[i] = pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.id, s.size), -1)
					}
				}
			}
			return nil
		})
		return err
	}, key))
	// there could be false-negative that the compaction is successful, double-check
	if st != 0 && st != syscall.EINVAL {
		if e := m.rdb.HGet(ctx, m.sliceRefs(), m.sliceKey(id, size)).Err(); e == nil {
			st = 0 // successful
		} else if e == redis.Nil {
			logger.Infof("compacted chunk %d was not used", id)
			st = syscall.EINVAL // failed
		}
	}

	if st == syscall.EINVAL {
		m.rdb.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(id, size), -1)
	} else if st == 0 {
		m.cleanupZeroRef(m.sliceKey(id, size))
		if delayed == nil {
			for i, s := range ss {
				if s.id > 0 && rs[i].Err() == nil && rs[i].Val() < 0 {
					m.deleteSlice(s.id, s.size)
				}
			}
		}
	}
	return st
}

func (m *redisMeta) scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error {
	p := m.rdb.Pipeline()
	return m.scan(ctx, "c*_*", func(keys []string) error {
		for _, key := range keys {
			_ = p.LLen(ctx, key)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			for _, c := range cmds {
				if c.Err() != nil {
					logger.Warnf("Scan chunks with command %s: %s", c.String(), c.Err())
				}
			}
			return err
		}
		for i, cmd := range cmds {
			cnt := cmd.(*redis.IntCmd).Val()
			if cnt > 1 {
				var inode uint64
				var indx uint32
				n, err := fmt.Sscanf(keys[i], m.prefix+"c%d_%d", &inode, &indx)
				if err == nil && n == 2 {
					bar.IncrTotal(1)
					ch <- cchunk{Ino(inode), indx, int(cnt)}
				}
			}
		}
		return nil
	})
}

func (m *redisMeta) cleanupLeakedInodes(delete bool) {
	var ctx = Background()
	var foundInodes = make(map[Ino]struct{})
	foundInodes[RootInode] = struct{}{}
	foundInodes[TrashInode] = struct{}{}
	cutoff := time.Now().Add(time.Hour * -1)
	prefix := len(m.prefix)

	_ = m.scan(ctx, "d[0-9]*", func(keys []string) error {
		for _, key := range keys {
			ino, _ := strconv.Atoi(key[prefix+1:])
			var entries []*Entry
			eno := m.doReaddir(ctx, Ino(ino), 0, &entries, 0)
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
	_ = m.scan(ctx, "i*", func(keys []string) error {
		values, err := m.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			logger.Warnf("mget inodes: %s", err)
			return nil
		}
		for i, v := range values {
			if v == nil {
				continue
			}
			var attr Attr
			m.parseAttr([]byte(v.(string)), &attr)
			ino, _ := strconv.Atoi(keys[i][prefix+1:])
			if _, ok := foundInodes[Ino(ino)]; !ok && time.Unix(attr.Ctime, 0).Before(cutoff) {
				logger.Infof("found dangling inode: %s %+v", keys[i], attr)
				if delete {
					err = m.doDeleteSustainedInode(0, Ino(ino))
					if err != nil {
						logger.Errorf("delete leaked inode %d : %s", ino, err)
					}
				}
			}
		}
		return nil
	})
}

func (m *redisMeta) scan(ctx context.Context, pattern string, f func([]string) error) error {
	var rdb *redis.Client
	if c, ok := m.rdb.(*redis.ClusterClient); ok {
		var err error
		rdb, err = c.MasterForKey(ctx, m.prefix)
		if err != nil {
			return err
		}
	} else {
		rdb = m.rdb.(*redis.Client)
	}
	var cursor uint64
	for {
		keys, c, err := rdb.Scan(ctx, cursor, m.prefix+pattern, 10000).Result()
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

func (m *redisMeta) hscan(ctx context.Context, key string, f func([]string) error) error {
	var cursor uint64
	for {
		keys, c, err := m.rdb.HScan(ctx, key, cursor, "*", 10000).Result()
		if err != nil {
			logger.Warnf("HSCAN %s: %s", key, err)
			return err
		}
		if len(keys) > 0 {
			if err = f(keys); err != nil {
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

func (m *redisMeta) ListSlices(ctx Context, slices map[Ino][]Slice, scanPending, delete bool, showProgress func()) syscall.Errno {
	m.cleanupLeakedInodes(delete)
	m.cleanupLeakedChunks(delete)
	m.cleanupOldSliceRefs(delete)
	if delete {
		m.doCleanupSlices(ctx)
	}

	p := m.rdb.Pipeline()
	err := m.scan(ctx, "c*_*", func(keys []string) error {
		for _, key := range keys {
			_ = p.LRange(ctx, key, 0, -1)
		}
		cmds, err := p.Exec(ctx)
		if err != nil {
			for _, c := range cmds {
				if c.Err() != nil {
					logger.Warnf("List slices with command %s: %s", c.String(), c.Err())
				}
			}
			return err
		}
		for _, cmd := range cmds {
			key := cmd.(*redis.StringSliceCmd).Args()[1].(string)
			inode, _ := strconv.Atoi(strings.Split(key[len(m.prefix)+1:], "_")[0])
			vals := cmd.(*redis.StringSliceCmd).Val()
			ss := readSlices(vals)
			if ss == nil {
				logger.Errorf("Corrupt value for inode %d chunk key %s", inode, key)
				continue
			}
			for _, s := range ss {
				if s.id > 0 {
					slices[Ino(inode)] = append(slices[Ino(inode)], Slice{Id: s.id, Size: s.size})
					if showProgress != nil {
						showProgress()
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		logger.Warnf("scan chunks: %s", err)
		return errno(err)
	}

	if scanPending {
		_ = m.hscan(Background(), m.sliceRefs(), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				key, val := keys[i], keys[i+1]
				if strings.HasPrefix(val, "-") { // < 0
					ps := strings.Split(key, "_")
					if len(ps) == 2 {
						id, _ := strconv.ParseUint(ps[0][1:], 10, 64)
						size, _ := strconv.ParseUint(ps[1], 10, 32)
						if id > 0 && size > 0 {
							slices[0] = append(slices[0], Slice{Id: id, Size: uint32(size)})
						}
					}
				}
			}
			return nil
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

func (m *redisMeta) scanTrashSlices(ctx Context, scan trashSliceScan) error {
	if scan == nil {
		return nil
	}

	delKeys := make(chan string, 1000)
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = m.hscan(c, m.delSlices(), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				delKeys <- keys[i]
			}
			return nil
		})
		close(delKeys)
	}()

	var ss []Slice
	var rs []*redis.IntCmd
	for key := range delKeys {
		var clean bool
		task := func(tx *redis.Tx) error {
			ss = ss[:0]
			rs = rs[:0]
			val, err := tx.HGet(ctx, m.delSlices(), key).Result()
			if err == redis.Nil {
				return nil
			} else if err != nil {
				return err
			}
			ps := strings.Split(key, "_")
			if len(ps) != 2 {
				return fmt.Errorf("invalid key %s", key)
			}
			ts, err := strconv.ParseInt(ps[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid key %s, fail to parse timestamp", key)
			}

			m.decodeDelayedSlices([]byte(val), &ss)
			clean, err = scan(ss, ts)
			if err != nil {
				return err
			}
			if clean {
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					for _, s := range ss {
						rs = append(rs, pipe.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.Id, s.Size), -1))
					}
					pipe.HDel(ctx, m.delSlices(), key)
					return nil
				})
			}
			return err
		}
		err := m.txn(ctx, task, m.delSlices())
		if err != nil {
			return err
		}
		if clean && len(rs) == len(ss) {
			for i, s := range ss {
				if rs[i].Err() == nil && rs[i].Val() < 0 {
					m.deleteSlice(s.Id, s.Size)
				}
			}
		}
	}

	return nil
}

func (m *redisMeta) scanPendingSlices(ctx Context, scan pendingSliceScan) error {
	if scan == nil {
		return nil
	}

	pendingKeys := make(chan string, 1000)
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = m.hscan(c, m.sliceRefs(), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				val := keys[i+1]
				refs, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					// ignored
					logger.Warn(errors.Wrapf(err, "parse slice ref: %s", val))
					return nil
				}
				if refs < 0 {
					pendingKeys <- keys[i]
				}
			}
			return nil
		})
		close(pendingKeys)
	}()

	for key := range pendingKeys {
		ps := strings.Split(key[1:], "_")
		if len(ps) != 2 {
			return fmt.Errorf("invalid key %s", key)
		}
		id, err := strconv.ParseUint(ps[0], 10, 64)
		if err != nil {
			return errors.Wrapf(err, "invalid key %s, fail to parse id", key)
		}
		size, err := strconv.ParseUint(ps[1], 10, 64)
		if err != nil {
			return errors.Wrapf(err, "invalid key %s, fail to parse size", key)
		}
		clean, err := scan(id, uint32(size))
		if err != nil {
			return errors.Wrap(err, "scan pending slices")
		}
		if clean {
			// TODO: m.deleteSlice(id, uint32(size))
			// avoid lint warning
			_ = clean
		}
	}
	return nil
}

func (m *redisMeta) scanPendingFiles(ctx Context, scan pendingFileScan) error {
	if scan == nil {
		return nil
	}

	visited := make(map[Ino]bool)
	start := int64(0)
	const batchSize = 1000

	for {
		pairs, err := m.rdb.ZRangeWithScores(Background(), m.delfiles(), start, start+batchSize).Result()
		if err != nil {
			return err
		}

		for _, p := range pairs {
			v := p.Member.(string)
			ps := strings.Split(v, ":")
			if len(ps) != 2 { // will be cleaned up as legacy
				continue
			}
			inode, _ := strconv.ParseUint(ps[0], 10, 64)
			if visited[Ino(inode)] {
				continue
			}
			visited[Ino(inode)] = true
			size, _ := strconv.ParseUint(ps[1], 10, 64)
			if _, err := scan(Ino(inode), size, int64(p.Score)); err != nil {
				return err
			}
		}

		start += batchSize
		if len(pairs) < batchSize {
			break
		}
	}

	return nil
}

func (m *redisMeta) doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		attr.Nlink = 2
		vals, err := tx.HGetAll(ctx, m.entryKey(inode)).Result()
		if err != nil {
			return err
		}
		for _, v := range vals {
			typ, _ := m.parseEntry([]byte(v))
			if typ == TypeDirectory {
				attr.Nlink++
			}
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
			return nil
		})
		return err
	}, m.inodeKey(inode), m.entryKey(inode)))
}

func (m *redisMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer m.timeit("GetXattr", time.Now())
	inode = m.checkRoot(inode)
	var err error
	*vbuff, err = m.rdb.HGet(ctx, m.xattrKey(inode), name).Bytes()
	if err == redis.Nil {
		err = ENOATTR
	}
	return errno(err)
}

func (m *redisMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	defer m.timeit("ListXattr", time.Now())
	inode = m.checkRoot(inode)
	vals, err := m.rdb.HKeys(ctx, m.xattrKey(inode)).Result()
	if err != nil {
		return errno(err)
	}
	*names = nil
	for _, name := range vals {
		*names = append(*names, []byte(name)...)
		*names = append(*names, 0)
	}

	val, err := m.rdb.Get(ctx, m.inodeKey(inode)).Bytes()
	if err != nil {
		return errno(err)
	}
	attr := &Attr{}
	m.parseAttr(val, attr)
	setXAttrACL(names, attr.AccessACL, attr.DefaultACL)
	return 0
}

func (m *redisMeta) doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	key := m.xattrKey(inode)
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		switch flags {
		case XattrCreate:
			ok, err := tx.HSetNX(ctx, key, name, value).Result()
			if err != nil {
				return err
			}
			if !ok {
				return syscall.EEXIST
			}
			return nil
		case XattrReplace:
			if ok, err := tx.HExists(ctx, key, name).Result(); err != nil {
				return err
			} else if !ok {
				return ENOATTR
			}
			_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, key, name, value)
				return nil
			})
			return err
		default: // XattrCreateOrReplace
			_, err := tx.HSet(ctx, key, name, value).Result()
			return err
		}
	}, key))
}

func (m *redisMeta) doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	n, err := m.rdb.HDel(ctx, m.xattrKey(inode), name).Result()
	if err != nil {
		return errno(err)
	} else if n == 0 {
		return ENOATTR
	} else {
		return 0
	}
}

type quotaKeys struct {
	quotaKey      string
	usedSpaceKey  string
	usedInodesKey string
}

func (m *redisMeta) getQuotaKeys(qtype uint32) (*quotaKeys, error) {
	switch qtype {
	case DirQuotaType:
		return &quotaKeys{
			quotaKey:      m.dirQuotaKey(),
			usedSpaceKey:  m.dirQuotaUsedSpaceKey(),
			usedInodesKey: m.dirQuotaUsedInodesKey(),
		}, nil
	case UserQuotaType:
		return &quotaKeys{
			quotaKey:      m.userQuotaKey(),
			usedSpaceKey:  m.userQuotaUsedSpaceKey(),
			usedInodesKey: m.userQuotaUsedInodesKey(),
		}, nil
	case GroupQuotaType:
		return &quotaKeys{
			quotaKey:      m.groupQuotaKey(),
			usedSpaceKey:  m.groupQuotaUsedSpaceKey(),
			usedInodesKey: m.groupQuotaUsedInodesKey(),
		}, nil
	default:
		return nil, fmt.Errorf("unknown quota type: %d", qtype)
	}
}

func (m *redisMeta) doGetQuota(ctx Context, qtype uint32, key uint64) (*Quota, error) {
	config, err := m.getQuotaKeys(qtype)
	if err != nil {
		return nil, err
	}

	field := strconv.FormatUint(key, 10)
	cmds, err := m.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HGet(ctx, config.quotaKey, field)
		pipe.HGet(ctx, config.usedSpaceKey, field)
		pipe.HGet(ctx, config.usedInodesKey, field)
		return nil
	})
	if err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	buf, _ := cmds[0].(*redis.StringCmd).Bytes()
	if len(buf) != 16 {
		return nil, fmt.Errorf("invalid quota value: %v", buf)
	}

	var quota Quota
	quota.MaxSpace, quota.MaxInodes = m.parseQuota(buf)
	if quota.UsedSpace, err = cmds[1].(*redis.StringCmd).Int64(); err != nil {
		return nil, err
	}
	if quota.UsedInodes, err = cmds[2].(*redis.StringCmd).Int64(); err != nil {
		return nil, err
	}
	return &quota, nil
}

func (m *redisMeta) doSetQuota(ctx Context, qtype uint32, key uint64, quota *Quota) (bool, error) {
	config, err := m.getQuotaKeys(qtype)
	if err != nil {
		return false, err
	}

	var created bool
	err = m.txn(ctx, func(tx *redis.Tx) error {
		origin := &Quota{MaxSpace: -1, MaxInodes: -1}
		field := strconv.FormatUint(key, 10)

		buf, e := tx.HGet(ctx, config.quotaKey, field).Bytes()
		if e == nil {
			created = false
			origin.MaxSpace, origin.MaxInodes = m.parseQuota(buf)
		} else if e == redis.Nil {
			created = true
		} else {
			return e
		}

		if quota.MaxSpace >= 0 {
			origin.MaxSpace = quota.MaxSpace
		}
		if quota.MaxInodes >= 0 {
			origin.MaxInodes = quota.MaxInodes
		}

		_, e = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, config.quotaKey, field, m.packQuota(origin.MaxSpace, origin.MaxInodes))
			if quota.UsedSpace >= 0 {
				pipe.HSet(ctx, config.usedSpaceKey, field, quota.UsedSpace)
			} else if created {
				pipe.HSet(ctx, config.usedSpaceKey, field, 0)
			}
			if quota.UsedInodes >= 0 {
				pipe.HSet(ctx, config.usedInodesKey, field, quota.UsedInodes)
			} else if created {
				pipe.HSet(ctx, config.usedInodesKey, field, 0)
			}
			return nil
		})
		return e
	}, m.inodeKey(Ino(key)))
	return created, err
}

func (m *redisMeta) doDelQuota(ctx Context, qtype uint32, key uint64) error {
	config, err := m.getQuotaKeys(qtype)
	if err != nil {
		return err
	}

	field := strconv.FormatUint(key, 10)
	_, err = m.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		if qtype == UserQuotaType || qtype == GroupQuotaType {
			quotaData := m.packQuota(-1, -1) // -1 means unlimited
			pipe.HSet(ctx, config.quotaKey, field, quotaData)
		} else {
			pipe.HDel(ctx, config.quotaKey, field)
			pipe.HDel(ctx, config.usedSpaceKey, field)
			pipe.HDel(ctx, config.usedInodesKey, field)
		}
		return nil
	})
	return err
}

func (m *redisMeta) doLoadQuotas(ctx Context) (map[uint64]*Quota, map[uint64]*Quota, map[uint64]*Quota, error) {
	quotaTypes := []struct {
		qtype uint32
		name  string
	}{
		{DirQuotaType, "dir"},
		{UserQuotaType, "user"},
		{GroupQuotaType, "group"},
	}

	quotaMaps := make([]map[uint64]*Quota, 3)
	for i, qt := range quotaTypes {
		config, err := m.getQuotaKeys(qt.qtype)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to load %s quotas: %w", qt.name, err)
		}

		quotas := make(map[uint64]*Quota)
		if err := m.hscan(ctx, config.quotaKey, func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				key, val := keys[i], []byte(keys[i+1])
				id, err := strconv.ParseUint(key, 10, 64)
				if err != nil {
					logger.Errorf("invalid inode: %s", key)
					continue
				}
				if len(val) != 16 {
					logger.Errorf("invalid quota: %s=%s", key, val)
					continue
				}

				maxSpace, maxInodes := m.parseQuota(val)
				usedSpace, err := m.rdb.HGet(ctx, config.usedSpaceKey, key).Int64()
				if err != nil && err != redis.Nil {
					return err
				}
				usedInodes, err := m.rdb.HGet(ctx, config.usedInodesKey, key).Int64()
				if err != nil && err != redis.Nil {
					return err
				}

				quotas[id] = &Quota{
					MaxSpace:   int64(maxSpace),
					MaxInodes:  int64(maxInodes),
					UsedSpace:  usedSpace,
					UsedInodes: usedInodes,
				}
			}
			return nil
		}); err != nil {
			return nil, nil, nil, err
		}
		quotaMaps[i] = quotas
	}

	return quotaMaps[0], quotaMaps[1], quotaMaps[2], nil
}

func (m *redisMeta) doFlushQuotas(ctx Context, quotas []*iQuota) error {
	_, err := m.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, q := range quotas {
			config, err := m.getQuotaKeys(q.qtype)
			if err != nil {
				return err
			}

			field := strconv.FormatUint(q.qkey, 10)
			pipe.HIncrBy(ctx, config.usedSpaceKey, field, q.quota.newSpace)
			pipe.HIncrBy(ctx, config.usedInodesKey, field, q.quota.newInodes)
		}
		return nil
	})
	return err
}

func (m *redisMeta) checkServerConfig() {
	rawInfo, err := m.rdb.Info(Background()).Result()
	if err != nil {
		logger.Warnf("parse info: %s", err)
		return
	}
	rInfo, err := checkRedisInfo(rawInfo)
	if err != nil {
		logger.Warnf("parse info: %s", err)
	}
	if rInfo.storageProvider == "" && rInfo.maxMemoryPolicy != "" && rInfo.maxMemoryPolicy != "noeviction" {
		logger.Warnf("maxmemory_policy is %q,  we will try to reconfigure it to 'noeviction'.", rInfo.maxMemoryPolicy)
		if _, err := m.rdb.ConfigSet(Background(), "maxmemory-policy", "noeviction").Result(); err != nil {
			logger.Errorf("try to reconfigure maxmemory-policy to 'noeviction' failed: %s", err)
		} else if result, err := m.rdb.ConfigGet(Background(), "maxmemory-policy").Result(); err != nil {
			logger.Warnf("get config maxmemory-policy failed: %s", err)
		} else if len(result) == 1 && result["maxmemory-policy"] != "noeviction" {
			logger.Warnf("reconfigured maxmemory-policy to 'noeviction', but it's still %s", result["maxmemory-policy"])
		} else {
			logger.Infof("set maxmemory-policy to 'noeviction' successfully")
		}
	}
	start := time.Now()
	_, err = m.rdb.Ping(Background()).Result()
	if err != nil {
		logger.Errorf("Ping redis: %s", err.Error())
		return
	}
	logger.Infof("Ping redis latency: %s", time.Since(start))
}

func (m *redisMeta) dumpEntries(es ...*DumpedEntry) error {
	ctx := Background()
	var keys []string
	for _, e := range es {
		keys = append(keys, m.inodeKey(e.Attr.Inode))
	}
	return m.txn(ctx, func(tx *redis.Tx) error {
		p := tx.Pipeline()
		var ar = make([]*redis.StringCmd, len(es))
		var xr = make([]*redis.MapStringStringCmd, len(es))
		var sr = make([]*redis.StringCmd, len(es))
		var cr = make([]*redis.StringSliceCmd, len(es))
		var dr = make([]*redis.ScanCmd, len(es))
		for i, e := range es {
			inode := e.Attr.Inode
			ar[i] = p.Get(ctx, m.inodeKey(inode))
			xr[i] = p.HGetAll(ctx, m.xattrKey(inode))
			switch e.Attr.Type {
			case "regular":
				cr[i] = p.LRange(ctx, m.chunkKey(inode, 0), 0, -1)
			case "directory":
				dr[i] = p.HScan(ctx, m.entryKey(inode), 0, "*", 1000)
			case "symlink":
				sr[i] = p.Get(ctx, m.symKey(inode))
			}
		}
		if _, err := p.Exec(ctx); err != nil && err != redis.Nil {
			return err
		}

		type lchunk struct {
			inode Ino
			indx  uint32
			i     uint32
		}
		var lcs []*lchunk
		for i, e := range es {
			inode := e.Attr.Inode
			typ := typeFromString(e.Attr.Type)
			a, err := ar[i].Bytes()
			if err != nil {
				if err != redis.Nil {
					return err
				}
				if inode != TrashInode {
					logger.Warnf("Corrupt inode: %d, missing attribute", inode)
				}
			}

			var attr Attr
			attr.Typ = typ
			attr.Nlink = 1
			m.parseAttr(a, &attr)
			if attr.Typ != typ {
				e.Attr.Type = typeToString(attr.Typ)
				return redis.TxFailedErr // retry
			}
			if err == redis.Nil && attr.Typ == TypeDirectory {
				attr.Nlink = 2
			}
			dumpAttr(&attr, e.Attr)

			keys, err := xr[i].Result()
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

			accessACl, err := m.getACL(ctx, tx, attr.AccessACL)
			if err != nil {
				return err
			}
			e.AccessACL = dumpACL(accessACl)
			defaultACL, err := m.getACL(ctx, tx, attr.DefaultACL)
			if err != nil {
				return err
			}
			e.DefaultACL = dumpACL(defaultACL)

			switch typ {
			case TypeFile:
				e.Chunks = e.Chunks[:0]
				if attr.Length > 0 {
					vals, err := cr[i].Result()
					if err != nil {
						return err
					}
					if len(vals) > 0 {
						ss := readSlices(vals)
						if ss == nil {
							logger.Errorf("Corrupt value for inode %d chunk index %d", inode, 0)
						}
						slices := make([]*DumpedSlice, 0, len(ss))
						for _, s := range ss {
							slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
						}
						e.Chunks = append(e.Chunks, &DumpedChunk{0, slices})
					}
				}
				if attr.Length > ChunkSize {
					for indx := uint32(1); uint64(indx)*ChunkSize < attr.Length; indx++ {
						lcs = append(lcs, &lchunk{inode, indx, uint32(i)})
					}
				}
			case TypeDirectory:
				keys, cursor, err := dr[i].Result()
				if err != nil {
					return err
				}
				if cursor == 0 {
					e.Entries = make(map[string]*DumpedEntry)
					for i := 0; i < len(keys); i += 2 {
						name := keys[i]
						t, inode := m.parseEntry([]byte(keys[i+1]))
						ce := entryPool.Get()
						ce.Name = name
						ce.Attr.Inode = inode
						ce.Attr.Type = typeToString(t)
						e.Entries[name] = ce
					}
				}
			case TypeSymlink:
				if e.Symlink, err = sr[i].Result(); err != nil {
					if err != redis.Nil {
						return err
					}
					logger.Warnf("The symlink of inode %d is not found", inode)
				}
			}
		}

		cr = make([]*redis.StringSliceCmd, len(es)*3)
		for len(lcs) > 0 {
			if len(cr) > len(lcs) {
				cr = cr[:len(lcs)]
			}
			for i := range cr {
				c := lcs[i]
				cr[i] = p.LRange(ctx, m.chunkKey(c.inode, c.indx), 0, -1)
			}
			if _, err := p.Exec(ctx); err != nil {
				return err
			}
			for i := range cr {
				vals, err := cr[i].Result()
				if err != nil {
					return err
				}
				if len(vals) > 0 {
					e := es[lcs[i].i]
					ss := readSlices(vals)
					if ss == nil {
						logger.Errorf("Corrupt value for inode %d chunk index %d", e.Attr.Inode, lcs[i].indx)
					}
					slices := make([]*DumpedSlice, 0, len(ss))
					for _, s := range ss {
						slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
					}
					e.Chunks = append(e.Chunks, &DumpedChunk{lcs[i].indx, slices})
				}
			}
			lcs = lcs[len(cr):]
		}
		return nil
	}, keys...)
}

func (m *redisMeta) dumpDir(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth, threads int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}

	if tree.Entries == nil {
		tree.Entries = make(map[string]*DumpedEntry)
		err := m.hscan(Background(), m.entryKey(inode), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				name := keys[i]
				t, inode := m.parseEntry([]byte(keys[i+1]))
				e := entryPool.Get()
				e.Name = name
				e.Attr.Inode = inode
				e.Attr.Type = typeToString(t)
				tree.Entries[name] = e
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	var err error
	if err = tree.writeJsonWithOutEntry(bw, depth); err != nil {
		return err
	}
	entries := make([]*DumpedEntry, 0, len(tree.Entries))
	for _, e := range tree.Entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if showProgress != nil {
		showProgress(int64(len(entries)), 0)
	}

	var batch = 100
	ms := make([]sync.Mutex, threads)
	conds := make([]*sync.Cond, threads)
	ready := make([]int, threads)
	for c := 0; c < threads; c++ {
		conds[c] = sync.NewCond(&ms[c])
		if c*batch < len(entries) {
			go func(c int) {
				for i := c * batch; i < len(entries) && err == nil; i += threads * batch {
					es := entries[i:]
					if len(es) > batch {
						es = es[:batch]
					}
					e := m.dumpEntries(es...)
					ms[c].Lock()
					ready[c] = len(es)
					if e != nil {
						err = e
					}
					conds[c].Signal()
					for ready[c] > 0 && err == nil {
						conds[c].Wait()
					}
					ms[c].Unlock()
				}
			}(c)
		}
	}
	for i, e := range entries {
		b := i / batch
		c := b % threads
		ms[c].Lock()
		for ready[c] == 0 && err == nil {
			conds[c].Wait()
		}
		ready[c]--
		if ready[c] == 0 {
			conds[c].Signal()
		}
		ms[c].Unlock()
		if err != nil {
			return err
		}
		if e.Attr.Type == "directory" {
			err = m.dumpDir(e.Attr.Inode, e, bw, depth+2, threads, showProgress)
		} else {
			err = e.writeJSON(bw, depth+2)
		}
		entries[i] = nil
		entryPool.Put(e)
		if err != nil {
			return err
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

func (m *redisMeta) DumpMeta(w io.Writer, root Ino, threads int, keepSecret, fast, skipTrash bool) (err error) {
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
		ss, err = m.rdb.SMembers(ctx, m.sustained(sid)).Result()
		if err != nil {
			return err
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
	quotas := make(map[Ino]*DumpedQuota)
	for k, v := range m.rdb.HGetAll(ctx, m.dirQuotaKey()).Val() {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse inode: %s: %v", k, err)
			continue
		}
		if len(v) != 16 {
			logger.Warnf("invalid quota string: %s", hex.EncodeToString([]byte(v)))
			continue
		}
		var quota DumpedQuota
		quota.MaxSpace, quota.MaxInodes = m.parseQuota([]byte(v))
		quotas[Ino(inode)] = &quota
	}

	dm := &DumpedMeta{
		Setting: *m.getFormat(),
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
	root = m.checkRoot(root)
	progress := utils.NewProgress(false)
	bar := progress.AddCountBar("Dumped entries", 1) // with root
	useTotal := root == RootInode && !skipTrash
	if useTotal {
		bar.SetTotal(dm.Counters.UsedInodes)
	}

	showProgress := func(totalIncr, currentIncr int64) {
		if !useTotal {
			bar.IncrTotal(totalIncr)
		}
		bar.IncrInt64(currentIncr)
	}

	var tree = &DumpedEntry{
		Name: "FSTree",
		Attr: &DumpedAttr{
			Inode: root,
			Type:  typeToString(TypeDirectory),
		},
	}
	if err = m.dumpEntries(tree); err != nil {
		return err
	}
	bar.Increment()
	if err = m.dumpDir(root, tree, bw, 1, threads, showProgress); err != nil {
		return err
	}
	if root == RootInode && !skipTrash {
		trash := &DumpedEntry{
			Name: "Trash",
			Attr: &DumpedAttr{
				Inode: TrashInode,
				Type:  typeToString(TypeDirectory),
			},
		}
		if err = m.dumpEntries(trash); err != nil {
			return err
		}
		if _, err = bw.WriteString(","); err != nil {
			return err
		}
		if err = m.dumpDir(TrashInode, trash, bw, 1, threads, showProgress); err != nil {
			return err
		}
	}
	if _, err = bw.WriteString("\n}\n"); err != nil {
		return err
	}
	progress.Done()

	return bw.Flush()
}

func (m *redisMeta) loadEntry(e *DumpedEntry, p redis.Pipeliner, tryExec func(), aclMaxId *uint32) {
	ctx := Background()
	inode := e.Attr.Inode
	attr := loadAttr(e.Attr)
	attr.Parent = e.Parents[0]
	batch := 100
	if attr.Typ == TypeFile {
		attr.Length = e.Attr.Length
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]string, 0, len(c.Slices))
			for _, s := range c.Slices {
				slices = append(slices, string(marshalSlice(s.Pos, s.Id, s.Size, s.Off, s.Len)))
				if len(slices) > batch {
					p.RPush(ctx, m.chunkKey(inode, c.Index), slices)
					tryExec()
					slices = slices[:0]
				}
			}
			if len(slices) > 0 {
				p.RPush(ctx, m.chunkKey(inode, c.Index), slices)
			}
		}
	} else if attr.Typ == TypeDirectory {
		attr.Length = 4 << 10
		dentries := make(map[string]interface{}, batch)
		var stat dirStat
		for name, c := range e.Entries {
			length := uint64(0)
			if typeFromString(c.Attr.Type) == TypeFile {
				length = c.Attr.Length
			}
			stat.length += int64(length)
			stat.space += align4K(length)
			stat.inodes++

			dentries[string(unescape(name))] = m.packEntry(typeFromString(c.Attr.Type), c.Attr.Inode)
			if len(dentries) >= batch {
				p.HSet(ctx, m.entryKey(inode), dentries)
				tryExec()
				dentries = make(map[string]interface{}, batch)
			}
		}
		if len(dentries) > 0 {
			p.HSet(ctx, m.entryKey(inode), dentries)
		}
		field := inode.String()
		p.HSet(ctx, m.dirDataLengthKey(), field, stat.length)
		p.HSet(ctx, m.dirUsedSpaceKey(), field, stat.space)
		p.HSet(ctx, m.dirUsedInodesKey(), field, stat.inodes)
	} else if attr.Typ == TypeSymlink {
		symL := unescape(e.Symlink)
		attr.Length = uint64(len(symL))
		p.Set(ctx, m.symKey(inode), symL, 0)
	}

	if len(e.Xattrs) > 0 {
		xattrs := make(map[string]interface{})
		for _, x := range e.Xattrs {
			xattrs[x.Name] = unescape(x.Value)
		}
		p.HSet(ctx, m.xattrKey(inode), xattrs)
	}

	attr.AccessACL = m.saveACL(loadACL(e.AccessACL), aclMaxId)
	attr.DefaultACL = m.saveACL(loadACL(e.DefaultACL), aclMaxId)

	p.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
	tryExec()
}

func (m *redisMeta) LoadMeta(r io.Reader) (err error) {
	ctx := Background()
	if _, ok := m.rdb.(*redis.ClusterClient); ok {
		err = m.scan(ctx, "*", func(keys []string) error {
			return fmt.Errorf("found key with same prefix: %s", keys[0])
		})
		if err != nil {
			return err
		}
	} else {
		dbsize, err := m.rdb.DBSize(ctx).Result()
		if err != nil {
			return err
		}
		if dbsize > 0 {
			return fmt.Errorf("Database redis://%s is not empty", m.addr)
		}
	}

	p := m.rdb.TxPipeline()
	tryExec := func() {
		if p.Len() > 1000 {
			if rs, err := p.Exec(ctx); err != nil {
				for i, r := range rs {
					if r.Err() != nil {
						logger.Errorf("failed command %d %+v: %s", i, r, r.Err())
						break
					}
				}
				panic(err)
			}
		}
	}
	defer func() {
		if e := recover(); e != nil {
			if ee, ok := e.(error); ok {
				err = ee
			} else {
				panic(e)
			}
		}
	}()

	var aclMaxId uint32
	dm, counters, parents, refs, err := loadEntries(r, func(e *DumpedEntry) { m.loadEntry(e, p, tryExec, &aclMaxId) }, nil)
	if err != nil {
		return err
	}
	m.loadDumpedQuotas(ctx, dm.Quotas)
	if err = m.loadDumpedACLs(ctx); err != nil {
		return err
	}
	format, _ := json.MarshalIndent(dm.Setting, "", "")
	p.Set(ctx, m.setting(), format, 0)
	cs := make(map[string]interface{})
	cs[m.prefix+usedSpace] = counters.UsedSpace
	cs[m.prefix+totalInodes] = counters.UsedInodes
	cs[m.prefix+"nextinode"] = counters.NextInode - 1
	cs[m.prefix+"nextchunk"] = counters.NextChunk - 1
	cs[m.prefix+"nextsession"] = counters.NextSession
	cs[m.prefix+"nextTrash"] = counters.NextTrash
	p.MSet(ctx, cs)
	if l := len(dm.DelFiles); l > 0 {
		if l > 100 {
			l = 100
		}
		zs := make([]redis.Z, 0, l)
		for _, d := range dm.DelFiles {
			if len(zs) >= 100 {
				p.ZAdd(ctx, m.delfiles(), zs...)
				tryExec()
				zs = zs[:0]
			}
			zs = append(zs, redis.Z{
				Score:  float64(d.Expire),
				Member: m.toDelete(d.Inode, d.Length),
			})
		}
		p.ZAdd(ctx, m.delfiles(), zs...)
	}
	slices := make(map[string]interface{})
	for k, v := range refs {
		if v > 1 {
			if len(slices) > 100 {
				p.HSet(ctx, m.sliceRefs(), slices)
				tryExec()
				slices = make(map[string]interface{})
			}
			slices[m.sliceKey(k.id, k.size)] = v - 1
		}
	}
	if len(slices) > 0 {
		p.HSet(ctx, m.sliceRefs(), slices)
	}
	if _, err = p.Exec(ctx); err != nil {
		return err
	}

	// update nlinks and parents for hardlinks
	st := make(map[Ino]int64)
	for i, ps := range parents {
		if len(ps) > 1 {
			a, _ := m.rdb.Get(ctx, m.inodeKey(i)).Bytes()
			// reset nlink and parent
			binary.BigEndian.PutUint32(a[47:51], uint32(len(ps))) // nlink
			binary.BigEndian.PutUint64(a[63:71], 0)
			p.Set(ctx, m.inodeKey(i), a, 0)
			for k := range st {
				delete(st, k)
			}
			for _, p := range ps {
				st[p] = st[p] + 1
			}
			for parent, c := range st {
				p.HIncrBy(ctx, m.parentKey(i), parent.String(), c)
			}
		}
	}
	_, err = p.Exec(ctx)
	return err
}

func (m *redisMeta) doCloneEntry(ctx Context, srcIno Ino, parent Ino, name string, ino Ino, originAttr *Attr, cmode uint8, cumask uint16, top bool) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		a, err := tx.Get(ctx, m.inodeKey(srcIno)).Bytes()
		if err != nil {
			return err
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
		srcXattr, err := tx.HGetAll(ctx, m.xattrKey(srcIno)).Result()
		if err != nil {
			return err
		}

		var pattr Attr
		if top {
			if a, err := tx.Get(ctx, m.inodeKey(parent)).Bytes(); err != nil {
				return err
			} else {
				m.parseAttr(a, &pattr)
			}
			if pattr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			if (pattr.Flags & FlagImmutable) != 0 {
				return syscall.EPERM
			}
			if exist, err := tx.HExists(ctx, m.entryKey(parent), name).Result(); err != nil {
				return err
			} else if exist {
				return syscall.EEXIST
			}
			if eno := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); eno != 0 {
				return eno
			}
		}

		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, m.inodeKey(ino), m.marshal(&attr), 0)
			p.IncrBy(ctx, m.usedSpaceKey(), align4K(attr.Length))
			p.Incr(ctx, m.totalInodesKey())
			if len(srcXattr) > 0 {
				p.HMSet(ctx, m.xattrKey(ino), srcXattr)
			}
			if top && attr.Typ == TypeDirectory {
				p.ZAdd(ctx, m.detachedNodes(), redis.Z{Member: ino.String(), Score: float64(time.Now().Unix())})
			} else {
				p.HSet(ctx, m.entryKey(parent), name, m.packEntry(attr.Typ, ino))
				if top {
					now := time.Now()
					pattr.Mtime = now.Unix()
					pattr.Mtimensec = uint32(now.Nanosecond())
					pattr.Ctime = now.Unix()
					pattr.Ctimensec = uint32(now.Nanosecond())
					p.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
				}
			}

			switch attr.Typ {
			case TypeDirectory:
				sfield := srcIno.String()
				field := ino.String()
				if v, err := tx.HGet(ctx, m.dirUsedInodesKey(), sfield).Result(); err == nil {
					p.HSet(ctx, m.dirUsedInodesKey(), field, v)
					p.HSet(ctx, m.dirDataLengthKey(), field, tx.HGet(ctx, m.dirDataLengthKey(), sfield).Val())
					p.HSet(ctx, m.dirUsedSpaceKey(), field, tx.HGet(ctx, m.dirUsedSpaceKey(), sfield).Val())
				}
			case TypeFile:
				// copy chunks
				if attr.Length != 0 {
					var vals [][]string
					for i := 0; i <= int(attr.Length/ChunkSize); i++ {
						val, err := tx.LRange(ctx, m.chunkKey(srcIno, uint32(i)), 0, -1).Result()
						if err != nil {
							return err
						}
						vals = append(vals, val)
					}

					for i, sv := range vals {
						if len(sv) == 0 {
							continue
						}
						ss := readSlices(sv)
						if ss == nil {
							return syscall.EIO
						}
						p.RPush(ctx, m.chunkKey(ino, uint32(i)), sv)
						for _, s := range ss {
							if s.id > 0 {
								p.HIncrBy(ctx, m.sliceRefs(), m.sliceKey(s.id, s.size), 1)
							}
						}
					}
				}
			case TypeSymlink:
				path, err := tx.Get(ctx, m.symKey(srcIno)).Result()
				if err != nil {
					return err
				}
				p.Set(ctx, m.symKey(ino), path, 0)
			}
			return nil
		})
		return err
	}, m.inodeKey(srcIno), m.xattrKey(srcIno)))
}

func (m *redisMeta) doCleanupDetachedNode(ctx Context, ino Ino) syscall.Errno {
	exists, err := m.rdb.Exists(ctx, m.inodeKey(ino)).Result()
	if err != nil || exists == 0 {
		return errno(err)
	}
	rmConcurrent := make(chan int, 10)
	if eno := m.emptyDir(ctx, ino, true, nil, rmConcurrent); eno != 0 {
		return eno
	}
	m.updateStats(-align4K(0), -1)
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		_, err := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Del(ctx, m.inodeKey(ino))
			p.Del(ctx, m.xattrKey(ino))
			p.DecrBy(ctx, m.usedSpaceKey(), align4K(0))
			p.Decr(ctx, m.totalInodesKey())
			field := ino.String()
			p.HDel(ctx, m.dirUsedInodesKey(), field)
			p.HDel(ctx, m.dirDataLengthKey(), field)
			p.HDel(ctx, m.dirUsedSpaceKey(), field)
			p.ZRem(ctx, m.detachedNodes(), field)
			return nil
		})
		return err
	}, m.inodeKey(ino), m.xattrKey(ino)))
}

func (m *redisMeta) doFindDetachedNodes(t time.Time) []Ino {
	var inodes []Ino
	vals, err := m.rdb.ZRangeByScore(Background(), m.detachedNodes(), &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(t.Unix(), 10)}).Result()
	if err != nil {
		logger.Errorf("Scan detached nodes error: %s", err)
		return nil
	}
	for _, node := range vals {
		inode, _ := strconv.ParseUint(node, 10, 64)
		inodes = append(inodes, Ino(inode))
	}
	return inodes
}

func (m *redisMeta) doAttachDirNode(ctx Context, parent Ino, dstIno Ino, name string) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		var pattr Attr
		a, err := tx.Get(ctx, m.inodeKey(parent)).Bytes()
		if err != nil {
			return err
		}
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
		if tx.HExists(ctx, m.entryKey(parent), name).Val() {
			return syscall.EEXIST
		}

		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, m.entryKey(parent), name, m.packEntry(TypeDirectory, dstIno))
			pattr.Nlink++
			now := time.Now()
			pattr.Mtime = now.Unix()
			pattr.Mtimensec = uint32(now.Nanosecond())
			pattr.Ctime = now.Unix()
			pattr.Ctimensec = uint32(now.Nanosecond())
			p.Set(ctx, m.inodeKey(parent), m.marshal(&pattr), 0)
			p.ZRem(ctx, m.detachedNodes(), dstIno.String())
			return nil
		})
		return err
	}, m.inodeKey(parent), m.entryKey(parent)))
}

func (m *redisMeta) doTouchAtime(ctx Context, inode Ino, attr *Attr, now time.Time) (bool, error) {
	var updated bool
	err := m.txn(ctx, func(tx *redis.Tx) error {
		a, err := tx.Get(ctx, m.inodeKey(inode)).Bytes()
		if err != nil {
			return err
		}
		m.parseAttr(a, attr)
		if !m.atimeNeedsUpdate(attr, now) {
			return nil
		}
		attr.Atime = now.Unix()
		attr.Atimensec = uint32(now.Nanosecond())
		if _, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(inode), m.marshal(attr), 0)
			return nil
		}); err == nil {
			updated = true
		}
		return err
	}, m.inodeKey(inode))
	return updated, err
}

func (m *redisMeta) doSetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno {
	return errno(m.txn(ctx, func(tx *redis.Tx) error {
		val, err := tx.Get(ctx, m.inodeKey(ino)).Bytes()
		if err != nil {
			return err
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
			rule.InheritPerms(attr.Mode)
			aclId, err := m.insertACL(ctx, tx, rule)
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
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, m.inodeKey(ino), m.marshal(attr), 0)
				return nil
			})
			return err
		}
		return nil
	}, m.inodeKey(ino)))
}

func (m *redisMeta) doGetFacl(ctx Context, ino Ino, aclType uint8, aclId uint32, rule *aclAPI.Rule) syscall.Errno {
	if aclId == aclAPI.None {
		val, err := m.rdb.Get(ctx, m.inodeKey(ino)).Bytes()
		if err != nil {
			return errno(err)
		}
		attr := &Attr{}
		m.parseAttr(val, attr)
		m.of.Update(ino, attr)

		aclId = getAttrACLId(attr, aclType)
	}

	a, err := m.getACL(ctx, nil, aclId)
	if err != nil {
		return errno(err)
	}
	if a == nil {
		return ENOATTR
	}
	*rule = *a
	return 0
}

func (m *redisMeta) getACL(ctx Context, tx *redis.Tx, id uint32) (*aclAPI.Rule, error) {
	if id == aclAPI.None {
		return nil, nil
	}
	if cRule := m.aclCache.Get(id); cRule != nil {
		return cRule, nil
	}

	var val []byte
	var err error
	if tx != nil {
		val, err = tx.HGet(ctx, m.aclKey(), strconv.FormatUint(uint64(id), 10)).Bytes()
	} else {
		val, err = m.rdb.HGet(ctx, m.aclKey(), strconv.FormatUint(uint64(id), 10)).Bytes()
	}
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, syscall.EIO
	}

	rule := &aclAPI.Rule{}
	rule.Decode(val)
	m.aclCache.Put(id, rule)
	return rule, nil
}

func (m *redisMeta) insertACL(ctx Context, tx *redis.Tx, rule *aclAPI.Rule) (uint32, error) {
	if rule == nil || rule.IsEmpty() {
		return aclAPI.None, nil
	}

	if err := m.tryLoadMissACLs(ctx, tx); err != nil {
		logger.Warnf("SetFacl: load miss acls error: %s", err)
	}

	// set acl
	var aclId uint32
	if aclId = m.aclCache.GetId(rule); aclId == aclAPI.None {
		// TODO failures may result in some id wastage.
		newId, err := m.incrCounter(aclCounter, 1)
		if err != nil {
			return aclAPI.None, err
		}
		aclId = uint32(newId)

		if err = tx.HSetNX(ctx, m.aclKey(), strconv.FormatUint(uint64(aclId), 10), rule.Encode()).Err(); err != nil {
			return aclAPI.None, err
		}
		m.aclCache.Put(aclId, rule)
	}
	return aclId, nil
}

func (m *redisMeta) tryLoadMissACLs(ctx Context, tx *redis.Tx) error {
	missIds := m.aclCache.GetMissIds()
	if len(missIds) > 0 {
		missKeys := make([]string, len(missIds))
		for i, id := range missIds {
			missKeys[i] = strconv.FormatUint(uint64(id), 10)
		}

		vals, err := tx.HMGet(ctx, m.aclKey(), missKeys...).Result()
		if err != nil {
			return err
		}
		for i, data := range vals {
			var rule aclAPI.Rule
			if data != nil {
				rule.Decode([]byte(data.(string)))
			}
			m.aclCache.Put(missIds[i], &rule)
		}
	}
	return nil
}

func (m *redisMeta) loadDumpedACLs(ctx Context) error {
	id2Rule := m.aclCache.GetAll()
	if len(id2Rule) == 0 {
		return nil
	}

	return m.txn(ctx, func(tx *redis.Tx) error {
		maxId := uint32(0)
		acls := make(map[string]interface{}, len(id2Rule))
		for id, rule := range id2Rule {
			if id > maxId {
				maxId = id
			}
			acls[strconv.FormatUint(uint64(id), 10)] = rule.Encode()
		}
		if err := tx.HSet(ctx, m.aclKey(), acls).Err(); err != nil {
			return err
		}
		return tx.Set(ctx, m.prefix+aclCounter, maxId, 0).Err()
	}, m.inodeKey(RootInode))
}

func (m *redisMeta) newDirHandler(inode Ino, plus bool, entries []*Entry) DirHandler {
	return &redisDirHandler{
		en:          m,
		inode:       inode,
		plus:        plus,
		initEntries: entries,
		batchNum:    DirBatchNum["redis"],
	}
}

type redisDirHandler struct {
	sync.Mutex
	inode       Ino
	plus        bool
	en          *redisMeta
	initEntries []*Entry
	entries     []*Entry
	indexes     map[string]int
	readOff     int
	batchNum    int
}

func (s *redisDirHandler) Close() {
	s.Lock()
	s.entries = nil
	s.readOff = 0
	s.Unlock()
}

func (s *redisDirHandler) Delete(name string) {
	s.Lock()
	defer s.Unlock()

	if len(s.entries) == 0 {
		return
	}

	if idx, ok := s.indexes[name]; ok && idx >= s.readOff {
		delete(s.indexes, name)
		n := len(s.entries)
		if idx < n-1 {
			// TODO: sorted
			s.entries[idx] = s.entries[n-1]
			s.indexes[string(s.entries[idx].Name)] = idx
		}
		s.entries = s.entries[:n-1]
	}
}

func (s *redisDirHandler) Insert(inode Ino, name string, attr *Attr) {
	s.Lock()
	defer s.Unlock()

	if len(s.entries) == 0 {
		return
	}

	// TODO: sorted
	s.entries = append(s.entries, &Entry{Inode: inode, Name: []byte(name), Attr: attr})
	s.indexes[name] = len(s.entries) - 1
}

func (s *redisDirHandler) List(ctx Context, offset int) ([]*Entry, syscall.Errno) {
	var prefix []*Entry
	if offset < len(s.initEntries) {
		prefix = s.initEntries[offset:]
		offset = 0
	} else {
		offset -= len(s.initEntries)
	}

	s.Lock()
	defer s.Unlock()
	if s.entries == nil {
		var entries []*Entry
		err := s.en.hscan(ctx, s.en.entryKey(s.inode), func(keys []string) error {
			newEntries := make([]Entry, len(keys)/2)
			newAttrs := make([]Attr, len(keys)/2)
			for i := 0; i < len(keys); i += 2 {
				typ, ino := s.en.parseEntry([]byte(keys[i+1]))
				if keys[i] == "" {
					logger.Errorf("Corrupt entry with empty name: inode %d parent %d", ino, s.inode)
					continue
				}
				ent := &newEntries[i/2]
				ent.Inode = ino
				ent.Name = []byte(keys[i])
				ent.Attr = &newAttrs[i/2]
				ent.Attr.Typ = typ
				entries = append(entries, ent)
			}
			return nil
		})
		if err != nil {
			return nil, errno(err)
		}

		if s.en.conf.SortDir {
			sort.Slice(entries, func(i, j int) bool {
				return string(entries[i].Name) < string(entries[j].Name)
			})
		}
		if s.plus {
			nEntries := len(entries)
			if nEntries <= s.batchNum {
				err = s.en.fillAttr(ctx, entries)
			} else {
				eg := errgroup.Group{}
				eg.SetLimit(2)
				for i := 0; i < nEntries; i += s.batchNum {
					var es []*Entry
					if i+s.batchNum > nEntries {
						es = entries[i:]

					} else {
						es = entries[i : i+s.batchNum]
					}
					eg.Go(func() error {
						return s.en.fillAttr(ctx, es)
					})
				}
				err = eg.Wait()
			}
			if err != nil {
				return nil, errno(err)
			}
		}
		s.entries = entries

		indexes := make(map[string]int, len(entries))
		for i, e := range entries {
			indexes[string(e.Name)] = i
		}
		s.indexes = indexes
	}

	size := len(s.entries) - offset
	if size > s.batchNum {
		size = s.batchNum
	}
	s.readOff = offset + size
	entries := s.entries[offset : offset+size]
	if len(prefix) > 0 {
		entries = append(prefix, entries...)
	}
	return entries, 0
}

func (s *redisDirHandler) Read(offset int) {
	s.readOff = offset - len(s.initEntries)
}
