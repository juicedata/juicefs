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
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
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
	Removed chunks: delchunks -> [$inode -> seconds]
*/

var logger = utils.GetLogger("juicefs")

const usedSpace = "usedSpace"
const totalInodes = "totalInodes"
const delchunks = "delchunks"
const allSessions = "sessions"

const scriptLookup = `
local parse = function(buf, idx, pos)
	return bit.lshift(string.byte(buf, idx), pos)
end

local buf = redis.call('HGET', KEYS[1], KEYS[2])
if not buf then
       return false
end
if string.len(buf) ~= 9 then
       return {err=string.format("Invalid entry data: %s", buf)}
end
buf = string.sub(buf, 2)
local ino =  parse(buf, 1, 56) +
             parse(buf, 2, 48) +
             parse(buf, 3, 40) +
             parse(buf, 4, 32) +
             parse(buf, 5, 24) +
             parse(buf, 6, 16) +
             parse(buf, 7, 8) +
             parse(buf, 8, 0)
return {ino, redis.call('GET', "i" .. tostring(ino))}
`

// RedisConfig is config for Redis client.
type RedisConfig struct {
	Strict  bool // update ctime
	Retries int
}

type redisMeta struct {
	sync.Mutex
	conf    *RedisConfig
	rdb     *redis.Client
	txlocks [1024]sync.Mutex // Pessimistic locks to reduce conflict on Redis

	sid          int64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks

	shaLookup string // The SHA returned by Redis for the loaded `scriptLookup`
}

var _ Meta = &redisMeta{}

type msgCallbacks struct {
	sync.Mutex
	callbacks map[uint32]MsgCallback
}

// NewRedisMeta return a meta store using Redis.
func NewRedisMeta(url string, conf *RedisConfig) (Meta, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", url, err)
	}
	if opt.Password == "" && os.Getenv("REDIS_PASSWORD") != "" {
		opt.Password = os.Getenv("REDIS_PASSWORD")
	}
	opt.MaxRetries = conf.Retries
	opt.MinRetryBackoff = time.Millisecond * 100
	opt.MaxRetryBackoff = time.Minute * 1
	rdb := redis.NewClient(opt)
	m := &redisMeta{
		conf:         conf,
		rdb:          rdb,
		openFiles:    make(map[Ino]int),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}

	m.shaLookup, err = m.rdb.ScriptLoad(Background, scriptLookup).Result()
	if err != nil {
		logger.Infof("Failed to load scriptLookup: %v", err)
		m.shaLookup = ""
	}

	m.checkServerConfig()
	m.sid, err = m.rdb.Incr(Background, "nextsession").Result()
	if err != nil {
		return nil, fmt.Errorf("create session: %s", err)
	}
	logger.Debugf("session is is %d", m.sid)
	go m.refreshSession()
	go m.cleanupChunks()
	go m.cleanupLeakedChunks()
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
			// only AccessKey and SecretKey can be safely updated.
			format.UUID = old.UUID
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
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

	// root inode
	var attr Attr
	attr.Flags = 0
	attr.Typ = TypeDirectory
	attr.Mode = 0777
	attr.Uid = 0
	attr.Uid = 0
	ts := time.Now().Unix()
	attr.Atime = ts
	attr.Mtime = ts
	attr.Ctime = ts
	attr.Nlink = 2
	attr.Length = 4 << 10
	attr.Rdev = 0
	attr.Parent = 1
	r.rdb.Set(Background, r.inodeKey(1), r.marshal(&attr), 0)
	return nil
}

func (r *redisMeta) Load() (*Format, error) {
	body, err := r.rdb.Get(Background, "setting").Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("no volume found")
	}
	if err != nil {
		return nil, err
	}
	var format Format
	err = json.Unmarshal(body, &format)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &format, nil
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

func (r *redisMeta) sessionKey(sid int64) string {
	return "session" + strconv.FormatInt(sid, 10)
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
		return 0
	}
	return int64((((length - 1) >> 12) + 1) << 12)
}

func (r *redisMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	*totalspace = 1 << 50
	c, cancel := context.WithTimeout(ctx, time.Millisecond*300)
	defer cancel()
	used, _ := r.rdb.IncrBy(c, usedSpace, 0).Result()
	used = ((used >> 16) + 1) << 16 // aligned to 64K
	*availspace = *totalspace - uint64(used)
	inodes, _ := r.rdb.IncrBy(c, totalInodes, 0).Result()
	*iused = uint64(inodes)
	*iavail = 10 << 20
	return 0
}

func (r *redisMeta) Summary(ctx Context, inode Ino, summary *Summary) syscall.Errno {
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
				if st := r.Summary(ctx, e.Inode, summary); st != 0 {
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

func (r *redisMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var foundIno Ino
	var encodedAttr []byte
	var err error

	entryKey := r.entryKey(parent)
	if len(r.shaLookup) > 0 && attr != nil {
		var res interface{}
		res, err = r.rdb.EvalSha(ctx, r.shaLookup, []string{entryKey, name}).Result()
		if err != nil {
			if strings.Contains(err.Error(), "NOSCRIPT") {
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
		foundIno = Ino(returnedIno)
		encodedAttr = []byte(returnedAttr)
	} else {
		var buf []byte
		buf, err = r.rdb.HGet(ctx, entryKey, name).Bytes()
		if err != nil {
			return errno(err)
		}
		_, foundIno = r.parseEntry(buf)
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

func (r *redisMeta) accessMode(attr *Attr, uid uint32, gid uint32) uint8 {
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

	mode := r.accessMode(attr, ctx.Uid(), ctx.Gid())
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
	logger.Errorf("error: %s", err)
	return syscall.EIO
}

func (r *redisMeta) txn(ctx Context, txf func(tx *redis.Tx) error, keys ...string) syscall.Errno {
	var err error
	var khash = fnv.New32()
	_, _ = khash.Write([]byte(keys[0]))
	l := &r.txlocks[int(khash.Sum32())%len(r.txlocks)]
	l.Lock()
	defer l.Unlock()
	for i := 0; i < 50; i++ {
		err = r.rdb.Watch(ctx, txf, keys...)
		if err == redis.TxFailedErr {
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
				w := utils.NewBuffer(24)
				w.Put32(uint32(old % ChunkSize))
				w.Put64(0)
				w.Put32(0)
				w.Put32(0)
				if length > (old/ChunkSize+1)*ChunkSize {
					w.Put32(ChunkSize - uint32(old%ChunkSize))
				} else {
					w.Put32(uint32(length - old))
				}
				pipe.RPush(ctx, r.chunkKey(inode, uint32(old/ChunkSize)), w.Bytes())
				w = utils.NewBuffer(24)
				w.Put32(0)
				w.Put64(0)
				w.Put32(0)
				w.Put32(0)
				w.Put32(ChunkSize)
				for _, indx := range zeroChunks {
					pipe.RPushX(ctx, r.chunkKey(inode, indx), w.Bytes())
				}
				if length > (old/ChunkSize+1)*ChunkSize && length%ChunkSize > 0 {
					w := utils.NewBuffer(24)
					w.Put32(0)
					w.Put64(0)
					w.Put32(0)
					w.Put32(0)
					w.Put32(uint32(length % ChunkSize))
					pipe.RPush(ctx, r.chunkKey(inode, uint32(length/ChunkSize)), w.Bytes())
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
					w := utils.NewBuffer(24)
					w.Put32(uint32(coff))
					w.Put64(0)
					w.Put32(0)
					w.Put32(0)
					w.Put32(uint32(l))
					pipe.RPush(ctx, r.chunkKey(inode, indx), w.Bytes())
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
		if (cur.Mode&06000) != 0 && (set&(SetAttrUID|SetAttrGID)) != 0 {
			cur.Mode &= 01777
			attr.Mode &= 01777
		}
		if set&SetAttrUID != 0 {
			cur.Uid = attr.Uid
		}
		if set&SetAttrGID != 0 {
			cur.Gid = attr.Gid
		}
		if set&SetAttrMode != 0 {
			if ctx.Uid() != 0 && (attr.Mode&02000) != 0 {
				if ctx.Gid() != cur.Gid {
					attr.Mode &= 05777
				}
			}
			cur.Mode = attr.Mode
		}
		now := time.Now()
		if set&SetAttrAtime != 0 {
			cur.Atime = attr.Atime
			cur.Atimensec = attr.Atimensec
		}
		if set&SetAttrAtimeNow != 0 {
			cur.Atime = now.Unix()
			cur.Atimensec = uint32(now.Nanosecond())
		}
		if set&SetAttrMtime != 0 {
			cur.Mtime = attr.Mtime
			cur.Mtimensec = attr.Mtimensec
		}
		if set&SetAttrMtimeNow != 0 {
			cur.Mtime = now.Unix()
			cur.Mtimensec = uint32(now.Nanosecond())
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
			} else if _type == TypeFile {
				pipe.IncrBy(ctx, usedSpace, align4K(0))
			}
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
	if err == 0 {
		r.Lock()
		r.openFiles[*inode] = 1
		r.Unlock()
	}
	return err
}

func (r *redisMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	buf, err := r.rdb.HGet(ctx, r.entryKey(parent), name).Bytes()
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
				case TypeSymlink:
					pipe.Del(ctx, r.symKey(inode))
					pipe.Del(ctx, r.inodeKey(inode))
				case TypeFile:
					if opened {
						pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
						pipe.SAdd(ctx, r.sessionKey(r.sid), strconv.Itoa(int(inode)))
					} else {
						pipe.ZAdd(ctx, delchunks, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(inode, attr.Length)})
						pipe.Del(ctx, r.inodeKey(inode))
						pipe.IncrBy(ctx, usedSpace, -align4K(attr.Length))
					}
				}
				pipe.IncrBy(ctx, totalInodes, -1)
			}
			return nil
		})
		if err == nil && _type == TypeFile && attr.Nlink == 0 {
			if opened {
				r.Lock()
				r.removedFiles[inode] = true
				r.Unlock()
			} else {
				go r.deleteChunks(inode, attr.Length, "")
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
	if err != nil {
		return errno(err)
	}
	typ, inode := r.parseEntry(buf)
	if typ != TypeDirectory {
		return syscall.ENOTDIR
	}

	return r.txn(ctx, func(tx *redis.Tx) error {
		a, err := tx.Get(ctx, r.inodeKey(parent)).Bytes()
		if err != nil {
			return err
		}
		var pattr Attr
		r.parseAttr(a, &pattr)
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
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HDel(ctx, r.entryKey(parent), name)
			pipe.Set(ctx, r.inodeKey(parent), r.marshal(&pattr), 0)
			pipe.Del(ctx, r.inodeKey(inode))
			pipe.Del(ctx, r.xattrKey(inode))
			// pipe.Del(ctx, r.entryKey(inode))
			pipe.IncrBy(ctx, totalInodes, -1)
			return nil
		})
		return err
	}, r.inodeKey(parent), r.entryKey(parent), r.inodeKey(inode), r.entryKey(inode))
}

func (r *redisMeta) Rmr(ctx Context, parent Ino, name string) syscall.Errno {
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
	var entries []*Entry
	if st := r.Readdir(ctx, inode, 0, &entries); st != 0 {
		return st
	}
	for _, e := range entries {
		// TODO: in parallel
		if e.Inode == inode || e.Inode == parent {
			continue
		}
		if st := r.Rmr(ctx, inode, string(e.Name)); st != 0 {
			return st
		}
	}
	st := r.Rmdir(ctx, parent, name)
	if st == syscall.ENOTEMPTY {
		return r.Rmr(ctx, parent, name)
	}
	return st
}

func (r *redisMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	buf, err := r.rdb.HGet(ctx, r.entryKey(parentSrc), nameSrc).Bytes()
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
			if typ1 == TypeDirectory {
				cnt, err := tx.HLen(ctx, r.entryKey(dino)).Result()
				if err != nil {
					return err
				}
				if cnt != 0 {
					return syscall.ENOTEMPTY
				}
			} else {
				a, err := tx.Get(ctx, r.inodeKey(dino)).Bytes()
				if err != nil {
					return err
				}
				r.parseAttr(a, &tattr)
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

		rs, _ := tx.MGet(ctx, r.inodeKey(parentSrc), r.inodeKey(parentDst), r.inodeKey(ino)).Result()
		if rs[0] == nil || rs[1] == nil || rs[2] == nil {
			return redis.Nil
		}
		var sattr, dattr, iattr Attr
		r.parseAttr([]byte(rs[0].(string)), &sattr)
		if sattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		now := time.Now()
		sattr.Mtime = now.Unix()
		sattr.Mtimensec = uint32(now.Nanosecond())
		sattr.Ctime = now.Unix()
		sattr.Ctimensec = uint32(now.Nanosecond())
		r.parseAttr([]byte(rs[1].(string)), &dattr)
		if dattr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		dattr.Mtime = now.Unix()
		dattr.Mtimensec = uint32(now.Nanosecond())
		dattr.Ctime = now.Unix()
		dattr.Ctimensec = uint32(now.Nanosecond())
		r.parseAttr([]byte(rs[2].(string)), &iattr)
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
					if dtyp == TypeDirectory {
						pipe.Del(ctx, r.inodeKey(dino))
						dattr.Nlink--
					} else if dtyp == TypeSymlink {
						pipe.Del(ctx, r.symKey(dino))
						pipe.Del(ctx, r.inodeKey(dino))
					} else if dtyp == TypeFile {
						if opened {
							pipe.Set(ctx, r.inodeKey(dino), r.marshal(&tattr), 0)
							pipe.SAdd(ctx, r.sessionKey(r.sid), strconv.Itoa(int(dino)))
						} else {
							pipe.ZAdd(ctx, delchunks, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(dino, dattr.Length)})
							pipe.Del(ctx, r.inodeKey(dino))
							pipe.IncrBy(ctx, usedSpace, -align4K(tattr.Length))
						}
					}
					pipe.IncrBy(ctx, totalInodes, -1)
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
				go r.deleteChunks(dino, dattr.Length, "")
			}
		}
		return err
	}, keys...)
}

func (r *redisMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return r.txn(ctx, func(tx *redis.Tx) error {
		rs, _ := tx.MGet(ctx, r.inodeKey(parent), r.inodeKey(inode)).Result()
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

		err := tx.HGet(ctx, r.entryKey(parent), name).Err()
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
	vals, err := r.rdb.HGetAll(ctx, r.entryKey(inode)).Result()
	if err != nil {
		return errno(err)
	}
	newEntries := make([]Entry, len(vals))
	newAttrs := make([]Attr, len(newEntries))
	var i int
	for name, val := range vals {
		typ, inode := r.parseEntry([]byte(val))
		ent := newEntries[i]
		ent.Inode = inode
		ent.Name = []byte(name)
		attr := newAttrs[i]
		attr.Typ = typ
		ent.Attr = &attr
		*entries = append(*entries, &ent)
		i++
	}
	if plus != 0 {
		batchSize := 4096
		if batchSize > len(*entries) {
			batchSize = len(*entries)
		}
		nEntries := len(*entries)
		indexCh := make(chan int, 10)
		go func() {
			for i := 0; i < nEntries; i += batchSize {
				indexCh <- i
			}
			close(indexCh)
		}()
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				keysBatch := make([]string, 0, batchSize)
				for idx := range indexCh {
					end := idx + batchSize
					if end > len(*entries) {
						end = len(*entries)
					}
					for _, e := range (*entries)[idx:end] {
						keysBatch = append(keysBatch, r.inodeKey(e.Inode))
					}
					rs, _ := r.rdb.MGet(ctx, keysBatch...).Result()
					for j, re := range rs {
						if re != nil {
							if a, ok := re.(string); ok {
								r.parseAttr([]byte(a), (*entries)[idx+j].Attr)
							}
						}
					}
					keysBatch = keysBatch[:0]
				}
			}()
		}
		wg.Wait()
	}
	return 0
}

func (r *redisMeta) cleanStaleSession(sid int64) {
	var ctx = Background
	inodes, err := r.rdb.LRange(ctx, r.sessionKey(sid), 0, 1000).Result()
	if err != nil {
		return
	}
	for _, sinode := range inodes {
		inode, _ := strconv.ParseInt(sinode, 10, 0)
		if err := r.deleteInode(Ino(inode)); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
		}
	}
	if len(inodes) == 0 {
		r.rdb.Del(ctx, r.sessionKey(sid))
		r.rdb.ZRem(ctx, allSessions, strconv.Itoa(int(sid)))
	}
}

func (r *redisMeta) cleanStaleSessions() {
	now := time.Now()
	var ctx = Background
	rng := &redis.ZRangeBy{Max: strconv.Itoa(int(now.Add(time.Minute * -10).Unix())), Count: 100}
	staleSessions, _ := r.rdb.ZRangeByScore(ctx, allSessions, rng).Result()
	for _, ssid := range staleSessions {
		sid, _ := strconv.Atoi(ssid)
		r.cleanStaleSession(int64(sid))
	}

	rng = &redis.ZRangeBy{Max: strconv.Itoa(int(now.Add(time.Minute * -3).Unix())), Count: 100}
	staleSessions, err := r.rdb.ZRangeByScore(ctx, allSessions, rng).Result()
	if err != nil || len(staleSessions) == 0 {
		return
	}
	sids := make(map[string]bool)
	for _, sid := range staleSessions {
		sids[sid] = true
	}
	var cursor uint64
	var keys []string
	for {
		keys, cursor, err = r.rdb.Scan(ctx, cursor, "lock*", 1000).Result()
		if err != nil {
			break
		}
		for _, k := range keys {
			owners, _ := r.rdb.HKeys(ctx, k).Result()
			for _, o := range owners {
				p := strings.Split(o, "_")[0]
				if _, ok := sids[p]; ok {
					err = r.rdb.HDel(ctx, k, o).Err()
					logger.Infof("cleanup lock on %s from session %s: %s", k, p, err)
				}
			}
		}
		if cursor == 0 {
			break
		}
	}
}

func (r *redisMeta) refreshSession() {
	for {
		now := time.Now()
		r.rdb.ZAdd(Background, allSessions, &redis.Z{Score: float64(now.Unix()), Member: strconv.Itoa(int(r.sid))})
		go r.cleanStaleSessions()
		time.Sleep(time.Minute)
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
		pipe.ZAdd(ctx, delchunks, &redis.Z{Score: float64(time.Now().Unix()), Member: r.toDelete(inode, attr.Length)})
		pipe.Del(ctx, r.inodeKey(inode))
		pipe.IncrBy(ctx, usedSpace, -align4K(attr.Length))
		return nil
	})
	if err == nil {
		go r.deleteChunks(inode, attr.Length, "")
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
					r.rdb.SRem(ctx, r.sessionKey(r.sid), strconv.Itoa(int(inode)))
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
	if len(vals) >= 5 {
		go r.compact(inode, indx)
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
		// TODO: refcount for chunkid
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
		now := time.Now()
		attr.Mtime = now.Unix()
		attr.Mtimensec = uint32(now.Nanosecond())
		attr.Ctime = now.Unix()
		attr.Ctimensec = uint32(now.Nanosecond())

		w := utils.NewBuffer(24)
		w.Put32(off)
		w.Put64(slice.Chunkid)
		w.Put32(slice.Size)
		w.Put32(slice.Off)
		w.Put32(slice.Len)

		var rpush *redis.IntCmd
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			rpush = pipe.RPush(ctx, r.chunkKey(inode, indx), w.Bytes())
			pipe.Set(ctx, r.inodeKey(inode), r.marshal(&attr), 0)
			if added > 0 {
				pipe.IncrBy(ctx, usedSpace, added)
			}
			return nil
		})
		if err == nil && rpush.Val()%20 == 0 {
			go r.compact(inode, indx)
		}
		return err
	}, r.inodeKey(inode))
}

func (r *redisMeta) cleanupChunks() {
	for {
		now := time.Now()
		members, _ := r.rdb.ZRangeByScore(Background, delchunks, &redis.ZRangeBy{Min: strconv.Itoa(0), Max: strconv.Itoa(int(now.Add(time.Hour).Unix())), Count: 1000}).Result()
		for _, member := range members {
			ps := strings.Split(member, ":")
			inode, _ := strconv.ParseInt(ps[0], 10, 0)
			var length int64 = 1 << 30
			if len(ps) == 2 {
				length, _ = strconv.ParseInt(ps[1], 10, 0)
			} else if len(ps) > 2 {
				length, _ = strconv.ParseInt(ps[2], 10, 0)
			}
			r.deleteChunks(Ino(inode), uint64(length), member)
		}
		time.Sleep(time.Minute)
	}
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
		_, err = p.Exec(ctx)
		if err != nil {
			logger.Errorf("check inodes: %s", err)
			return
		}
		for i, rr := range rs {
			if rr.Val() == 0 {
				key := ikeys[i]
				logger.Debugf("found leaked chunk %s", key)
				ps := strings.Split(key, "_")
				ino, _ := strconv.ParseInt(ps[0][1:], 10, 0)
				indx, _ := strconv.Atoi(ps[1])
				_ = r.deleteChunk(Ino(ino), uint32(indx))
			}
		}
		if cursor == 0 {
			break
		}
	}
}

func (r *redisMeta) toDelete(inode Ino, length uint64) string {
	return inode.String() + ":" + strconv.Itoa(int(length))
}

func (r *redisMeta) deleteChunk(inode Ino, indx uint32) error {
	var ctx = Background
	key := r.chunkKey(inode, indx)
	for {
		slices, err := r.rdb.LRange(ctx, key, 0, 1000).Result()
		if err == redis.Nil {
			return nil
		}
		for _, slice := range slices {
			rb := utils.ReadBuffer([]byte(slice))
			_ = rb.Get32() // pos
			chunkid := rb.Get64()
			size := rb.Get32()
			var err error
			if chunkid > 0 {
				err = r.newMsg(DeleteChunk, chunkid, size)
			}
			if err == nil {
				err = r.txn(ctx, func(tx *redis.Tx) error {
					val, err := tx.LRange(ctx, key, 0, 0).Result()
					if err != nil {
						return err
					}
					if len(val) == 1 && val[0] == slice {
						_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
							pipe.LPop(ctx, key)
							return nil
						})
						return err
					}
					return fmt.Errorf("chunk %s changed", key)
				}, key)
			}
			if err != nil && err != syscall.Errno(0) {
				return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", key, err)
			}
		}
		if len(slices) < 100 {
			return nil
		}
	}
}

func (r *redisMeta) deleteChunks(inode Ino, length uint64, tracking string) {
	var ctx = Background
	var indx uint32
	for uint64(indx*ChunkSize) < length {
		p := r.rdb.Pipeline()
		var rs []*redis.IntCmd
		var keys []string
		for i := 0; uint64(indx)*ChunkSize < length && i < 1000; i++ {
			key := r.chunkKey(inode, indx)
			keys = append(keys, key)
			rs = append(rs, p.LLen(ctx, key))
			indx++
		}
		vals, err := p.Exec(ctx)
		if err != nil {
			logger.Errorf("delete chunks of inode %d: %s", inode, err)
			return
		}
		for i := range vals {
			val, err := rs[i].Result()
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
		tracking = inode.String() + ":" + strconv.Itoa(int(indx))
	}
	_ = r.rdb.ZRem(ctx, delchunks, tracking)
}

func (r *redisMeta) compact(inode Ino, indx uint32) {
	// avoid too many or duplicated compaction
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

	var ctx = Background
	vals, err := r.rdb.LRange(ctx, r.chunkKey(inode, indx), 0, 200).Result()
	if err != nil {
		return
	}
	chunkid, err := r.rdb.Incr(ctx, "nextchunk").Uint64()
	if err != nil {
		return
	}

	ss := readSlices(vals)
	chunks := buildSlice(ss)
	var size uint32
	for _, s := range chunks {
		size += s.Len
	}
	// TODO: skip first few large slices
	logger.Debugf("compact %d %d %d %d", inode, indx, len(vals), len(chunks))
	err = r.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(vals), err)
		return
	}
	errno := r.txn(ctx, func(tx *redis.Tx) error {
		vals2, err := tx.LRange(ctx, r.chunkKey(inode, indx), 0, int64(len(vals)-1)).Result()
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

		w := utils.NewBuffer(24)
		w.Put32(0)
		w.Put64(chunkid)
		w.Put32(size)
		w.Put32(0)
		w.Put32(size)
		_, err = tx.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.LTrim(ctx, r.chunkKey(inode, indx), int64(len(vals)), -1)
			pipe.LPush(ctx, r.chunkKey(inode, indx), w.Bytes())
			return nil
		})
		return err
	}, r.chunkKey(inode, indx))

	if errno != 0 {
		r.deleteSlice(ctx, chunkid, size)
	} else {
		for _, s := range ss {
			r.deleteSlice(ctx, s.chunkid, s.size)
		}
		if r.rdb.LLen(ctx, r.chunkKey(inode, indx)).Val() > 5 {
			go func() {
				// wait for the current compaction to finish
				time.Sleep(time.Millisecond * 10)
				r.compact(inode, indx)
			}()
		}
	}
}

func (r *redisMeta) deleteSlice(ctx Context, chunkid uint64, size uint32) {
	err := r.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
		// track the unused chunk
		w := utils.NewBuffer(24)
		w.Put32(0)
		w.Put64(chunkid)
		w.Put32(size)
		w.Put32(0)
		w.Put32(size)
		_, err = r.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.RPush(ctx, r.chunkKey(0, 0), w.Bytes())
			r.rdb.ZAdd(ctx, delchunks, &redis.Z{Score: float64(time.Now().Unix()), Member: "0:1024"})
			return nil
		})
		if err != nil {
			logger.Warnf("chunk %d (%d bytes) will be lost", chunkid, size)
		}
	}
}

func readSlices(vals []string) []*slice {
	slices := make([]slice, len(vals))
	ss := make([]*slice, len(vals))
	for i, val := range vals {
		s := &slices[i]
		s.read([]byte(val))
		ss[i] = s
	}
	return ss
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

func (r *redisMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	lkey := r.ownerKey(owner)
	if ltype == syscall.F_UNLCK {
		_, err := r.rdb.HDel(ctx, r.flockKey(inode), lkey).Result()
		return errno(err)
	}
	var err syscall.Errno
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			owners, err := tx.HGetAll(ctx, r.flockKey(inode)).Result()
			if err != nil {
				return err
			}
			if ltype == syscall.F_RDLCK {
				for _, v := range owners {
					if v == "W" {
						return syscall.EAGAIN
					}
				}
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					pipe.HSet(ctx, r.flockKey(inode), lkey, "R")
					return nil
				})
				return err
			}
			delete(owners, lkey)
			if len(owners) > 0 {
				return syscall.EAGAIN
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, r.flockKey(inode), lkey, "W")
				return nil
			})
			return err
		}, r.flockKey(inode))

		if !block || err != syscall.EAGAIN {
			break
		}
		if ltype == syscall.F_WRLCK {
			time.Sleep(time.Millisecond * 1)
		} else {
			time.Sleep(time.Millisecond * 10)
		}
		if ctx.Canceled() {
			return syscall.EINTR
		}
	}
	return err
}

type plock struct {
	ltype uint32
	pid   uint32
	start uint64
	end   uint64
}

func (r *redisMeta) loadLocks(d []byte) []plock {
	var ls []plock
	rb := utils.FromBuffer(d)
	for rb.HasMore() {
		ls = append(ls, plock{rb.Get32(), rb.Get32(), rb.Get64(), rb.Get64()})
	}
	return ls
}

func (r *redisMeta) dumpLocks(ls []plock) []byte {
	wb := utils.NewBuffer(uint32(len(ls)) * 24)
	for _, l := range ls {
		wb.Put32(l.ltype)
		wb.Put32(l.pid)
		wb.Put64(l.start)
		wb.Put64(l.end)
	}
	return wb.Bytes()
}

func (r *redisMeta) insertLocks(ls []plock, i int, nl plock) []plock {
	nls := make([]plock, len(ls)+1)
	copy(nls[:i], ls[:i])
	nls[i] = nl
	copy(nls[i+1:], ls[i:])
	ls = nls
	return ls
}

func (r *redisMeta) updateLocks(ls []plock, nl plock) []plock {
	// ls is ordered by l.start without overlap
	var i int
	for i < len(ls) && nl.end > nl.start {
		l := ls[i]
		if l.end < nl.start {
		} else if l.start < nl.start {
			ls = r.insertLocks(ls, i+1, plock{nl.ltype, nl.pid, nl.start, l.end})
			ls[i].end = nl.start
			i++
			nl.start = l.end
		} else if l.end < nl.end {
			ls[i].ltype = nl.ltype
			ls[i].start = nl.start
			nl.start = l.end
		} else if l.start < nl.end {
			ls = r.insertLocks(ls, i, nl)
			ls[i+1].start = nl.end
			nl.start = nl.end
		} else {
			ls = r.insertLocks(ls, i, nl)
			nl.start = nl.end
		}
		i++
	}
	if nl.start < nl.end {
		ls = append(ls, nl)
	}
	i = 0
	for i < len(ls) {
		if ls[i].ltype == syscall.F_UNLCK || ls[i].start == ls[i].end {
			// remove empty one
			copy(ls[i:], ls[i+1:])
			ls = ls[:len(ls)-1]
		} else {
			if i+1 < len(ls) && ls[i].ltype == ls[i+1].ltype && ls[i].end == ls[i+1].start {
				// combine continuous range
				ls[i].end = ls[i+1].end
				ls[i+1].start = ls[i+1].end
			}
			i++
		}
	}
	return ls
}

func (r *redisMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	if *ltype == syscall.F_UNLCK {
		*start = 0
		*end = 0
		*pid = 0
		return 0
	}
	lkey := r.ownerKey(owner)
	owners, err := r.rdb.HGetAll(ctx, r.plockKey(inode)).Result()
	if err != nil {
		return errno(err)
	}
	delete(owners, lkey) // exclude itself
	for k, d := range owners {
		ls := r.loadLocks([]byte(d))
		for _, l := range ls {
			// find conflicted locks
			if (*ltype == syscall.F_WRLCK || l.ltype == syscall.F_WRLCK) && *end > l.start && *start < l.end {
				*ltype = l.ltype
				*start = l.start
				*end = l.end
				sid, _ := strconv.Atoi(strings.Split(k, "_")[0])
				if int64(sid) == r.sid {
					*pid = l.pid
				} else {
					*pid = 0
				}
				return 0
			}
		}
	}
	*ltype = syscall.F_UNLCK
	*start = 0
	*end = 0
	*pid = 0
	return 0
}

func (r *redisMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	lkey := r.ownerKey(owner)
	var err syscall.Errno
	lock := plock{ltype, pid, start, end}
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			if ltype == syscall.F_UNLCK {
				d, err := tx.HGet(ctx, r.plockKey(inode), lkey).Result()
				if err != nil {
					return err
				}
				ls := r.loadLocks([]byte(d))
				if len(ls) == 0 {
					return nil
				}
				ls = r.updateLocks(ls, lock)
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					if len(ls) == 0 {
						pipe.HDel(ctx, r.plockKey(inode), lkey)
					} else {
						pipe.HSet(ctx, r.plockKey(inode), lkey, r.dumpLocks(ls))
					}
					return nil
				})
				return err
			}
			owners, err := tx.HGetAll(ctx, r.plockKey(inode)).Result()
			if err != nil {
				return err
			}
			ls := r.loadLocks([]byte(owners[lkey]))
			delete(owners, lkey)
			for _, d := range owners {
				ls := r.loadLocks([]byte(d))
				for _, l := range ls {
					// find conflicted locks
					if (ltype == syscall.F_WRLCK || l.ltype == syscall.F_WRLCK) && end > l.start && start < l.end {
						return syscall.EAGAIN
					}
				}
			}
			ls = r.updateLocks(ls, lock)
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, r.plockKey(inode), lkey, r.dumpLocks(ls))
				return nil
			})
			return err
		}, r.plockKey(inode))

		if !block || err != syscall.EAGAIN {
			break
		}
		if ltype == syscall.F_WRLCK {
			time.Sleep(time.Millisecond * 1)
		} else {
			time.Sleep(time.Millisecond * 10)
		}
		if ctx.Canceled() {
			return syscall.EINTR
		}
	}
	return err
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
}
