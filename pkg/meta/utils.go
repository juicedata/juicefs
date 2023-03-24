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
	"bytes"
	"fmt"
	"net/url"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

const (
	usedSpace      = "usedSpace"
	totalInodes    = "totalInodes"
	legacySessions = "sessions"
)

const (
	// fallocate
	fallocKeepSize  = 0x01
	fallocPunchHole = 0x02
	// RESERVED: fallocNoHideStale   = 0x04
	fallocCollapesRange = 0x08
	fallocZeroRange     = 0x10
	fallocInsertRange   = 0x20
)
const (
	// clone mode
	CLONE_MODE_CAN_OVERWRITE      = 0x01
	CLONE_MODE_PRESERVE_ATTR      = 0x02
	CLONE_MODE_PRESERVE_HARDLINKS = 0x08
)

type msgCallbacks struct {
	sync.Mutex
	callbacks map[uint32]MsgCallback
}

type freeID struct {
	next  uint64
	maxid uint64
}

var logger = utils.GetLogger("juicefs")

type queryMap struct {
	*url.Values
}

func (qm *queryMap) duration(key, originalKey string, d time.Duration) time.Duration {
	val := qm.Get(key)
	if val == "" {
		oVal := qm.Get(originalKey)
		if oVal == "" {
			return d
		}
		val = oVal
	}

	qm.Del(key)
	if dur, err := time.ParseDuration(val); err == nil {
		return dur
	} else {
		logger.Warnf("Parse duration %s for key %s: %s", val, key, err)
		return d
	}
}

func (qm *queryMap) pop(key string) string {
	defer qm.Del(key)
	return qm.Get(key)
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
	logger.Errorf("error: %s\n%s", err, debug.Stack())
	return syscall.EIO
}

func accessMode(attr *Attr, uid uint32, gids []uint32) uint8 {
	if uid == 0 {
		return 0x7
	}
	mode := attr.Mode
	if uid == attr.Uid {
		return uint8(mode>>6) & 7
	}
	for _, gid := range gids {
		if gid == attr.Gid {
			return uint8(mode>>3) & 7
		}
	}
	return uint8(mode & 7)
}

func align4K(length uint64) int64 {
	if length == 0 {
		return 1 << 12
	}
	return int64((((length - 1) >> 12) + 1) << 12)
}

type plockRecord struct {
	Type  uint32
	Pid   uint32
	Start uint64
	End   uint64
}

type ownerKey struct {
	Sid   uint64
	Owner uint64
}

type PLockItem struct {
	ownerKey
	plockRecord
}

type FLockItem struct {
	ownerKey
	Type string
}

func parseOwnerKey(key string) (*ownerKey, error) {
	pair := strings.Split(key, "_")
	if len(pair) != 2 {
		return nil, fmt.Errorf("invalid owner key: %s", key)
	}
	sid, err := strconv.ParseUint(pair[0], 10, 64)
	if err != nil {
		return nil, err
	}
	owner, err := strconv.ParseUint(pair[1], 16, 64)
	if err != nil {
		return nil, err
	}
	return &ownerKey{sid, owner}, nil
}

func loadLocks(d []byte) []plockRecord {
	var ls []plockRecord
	rb := utils.FromBuffer(d)
	for rb.HasMore() {
		ls = append(ls, plockRecord{rb.Get32(), rb.Get32(), rb.Get64(), rb.Get64()})
	}
	return ls
}

func dumpLocks(ls []plockRecord) []byte {
	wb := utils.NewBuffer(uint32(len(ls)) * 24)
	for _, l := range ls {
		wb.Put32(l.Type)
		wb.Put32(l.Pid)
		wb.Put64(l.Start)
		wb.Put64(l.End)
	}
	return wb.Bytes()
}

func updateLocks(ls []plockRecord, nl plockRecord) []plockRecord {
	// ls is ordered by l.start without overlap
	size := len(ls)
	for i := 0; i < size && nl.Start <= nl.End; i++ {
		l := ls[i]
		if nl.Start < l.Start && nl.End >= l.Start {
			// split nl
			ls = append(ls, nl)
			ls[len(ls)-1].End = l.Start - 1
			nl.Start = l.Start
		}
		if nl.Start > l.Start && nl.Start <= l.End {
			// split l
			l.End = nl.Start - 1
			ls = append(ls, l)
			ls[i].Start = nl.Start
			l = ls[i]
		}
		if nl.Start == l.Start {
			ls[i].Type = nl.Type // update l
			ls[i].Pid = nl.Pid
			if l.End > nl.End {
				// split l
				ls[i].End = nl.End
				l.Start = nl.End + 1
				ls = append(ls, l)
			}
			nl.Start = ls[i].End + 1
		}
	}
	if nl.Start <= nl.End {
		ls = append(ls, nl)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].Start < ls[j].Start })
	for i := 0; i < len(ls); {
		if ls[i].Type == F_UNLCK || ls[i].Start > ls[i].End {
			// remove empty one
			copy(ls[i:], ls[i+1:])
			ls = ls[:len(ls)-1]
		} else {
			if i+1 < len(ls) && ls[i].Type == ls[i+1].Type && ls[i].Pid == ls[i+1].Pid && ls[i].End+1 == ls[i+1].Start {
				// combine continuous range
				ls[i].End = ls[i+1].End
				ls[i+1].Start = ls[i+1].End + 1
			}
			i++
		}
	}
	return ls
}

func (m *baseMeta) emptyDir(ctx Context, inode Ino, skipCheckTrash bool, count *uint64, concurrent chan int) syscall.Errno {
	if st := m.Access(ctx, inode, 3, nil); st != 0 {
		return st
	}
	for {
		var entries []*Entry
		if st := m.en.doReaddir(ctx, inode, 0, &entries, 10000); st != 0 && st != syscall.ENOENT {
			return st
		}
		if len(entries) == 0 {
			return 0
		}
		var wg sync.WaitGroup
		var status syscall.Errno
		// try directories first to increase parallel
		var dirs int
		for i, e := range entries {
			if e.Attr.Typ == TypeDirectory {
				entries[dirs], entries[i] = entries[i], entries[dirs]
				dirs++
			}
		}
		for i, e := range entries {
			if e.Attr.Typ == TypeDirectory {
				select {
				case concurrent <- 1:
					wg.Add(1)
					go func(child Ino, name string) {
						defer wg.Done()
						e := m.emptyEntry(ctx, inode, name, child, skipCheckTrash, count, concurrent)
						if e != 0 && e != syscall.ENOENT {
							status = e
						}
						<-concurrent
					}(e.Inode, string(e.Name))
				default:
					if st := m.emptyEntry(ctx, inode, string(e.Name), e.Inode, skipCheckTrash, count, concurrent); st != 0 && st != syscall.ENOENT {
						return st
					}
				}
			} else {
				if count != nil {
					atomic.AddUint64(count, 1)
				}
				if st := m.Unlink(ctx, inode, string(e.Name), skipCheckTrash); st != 0 && st != syscall.ENOENT {
					return st
				}
			}
			if ctx.Canceled() {
				return syscall.EINTR
			}
			entries[i] = nil // release memory
		}
		wg.Wait()
		if status != 0 || inode == TrashInode { // try only once for .trash
			return status
		}
	}
}

func (m *baseMeta) emptyEntry(ctx Context, parent Ino, name string, inode Ino, skipCheckTrash bool, count *uint64, concurrent chan int) syscall.Errno {
	st := m.emptyDir(ctx, inode, skipCheckTrash, count, concurrent)
	if st == 0 && !isTrash(inode) {
		st = m.Rmdir(ctx, parent, name, skipCheckTrash)
		if st == syscall.ENOTEMPTY {
			st = m.emptyEntry(ctx, parent, name, inode, skipCheckTrash, count, concurrent)
		} else if count != nil {
			atomic.AddUint64(count, 1)
		}
	}
	return st
}

func (m *baseMeta) Remove(ctx Context, parent Ino, name string, count *uint64) syscall.Errno {
	parent = m.checkRoot(parent)
	if st := m.Access(ctx, parent, 3, nil); st != 0 {
		return st
	}
	var inode Ino
	var attr Attr
	if st := m.Lookup(ctx, parent, name, &inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		if count != nil {
			atomic.AddUint64(count, 1)
		}
		return m.Unlink(ctx, parent, name)
	}
	concurrent := make(chan int, 50)
	return m.emptyEntry(ctx, parent, name, inode, false, count, concurrent)
}

func (m *baseMeta) GetSummary(ctx Context, inode Ino, summary *Summary, recursive bool) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		summary.Files++
		summary.Size += uint64(align4K(attr.Length))
		if attr.Typ == TypeFile {
			summary.Length += attr.Length
		}
		return 0
	}
	summary.Dirs++
	summary.Size += uint64(align4K(0))

	const concurrency = 50
	dirs := []Ino{inode}
	for len(dirs) > 0 {
		entriesList := make([][]*Entry, len(dirs))
		var eg errgroup.Group
		eg.SetLimit(concurrency)
		for i := range dirs {
			ino := dirs[i]
			entries := &entriesList[i]
			eg.Go(func() error {
				st := m.Readdir(ctx, ino, 1, entries)
				if st != 0 && st != syscall.ENOENT {
					return st
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err.(syscall.Errno)
		}
		dirs = dirs[:0]
		for _, entries := range entriesList {
			for _, e := range entries {
				if bytes.Equal(e.Name, []byte(".")) || bytes.Equal(e.Name, []byte("..")) {
					continue
				}
				if e.Attr.Typ == TypeDirectory {
					summary.Dirs++
					summary.Size += uint64(align4K(0))
					if recursive {
						dirs = append(dirs, e.Inode)
					}
				} else {
					summary.Files++
					summary.Size += uint64(align4K(e.Attr.Length))
					if e.Attr.Typ == TypeFile {
						summary.Length += e.Attr.Length
					}
				}
			}
		}
	}
	return 0
}

func (m *baseMeta) FastGetSummary(ctx Context, inode Ino, summary *Summary, recursive bool) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		summary.Files++
		summary.Size += uint64(align4K(attr.Length))
		if attr.Typ == TypeFile {
			summary.Length += attr.Length
		}
		return 0
	}
	summary.Dirs++
	summary.Size += uint64(align4K(0))

	const concurrency = 50
	dirs := []Ino{inode}
	for len(dirs) > 0 {
		entriesList := make([][]*Entry, len(dirs))
		dirStats := make([]dirStat, len(dirs))
		var eg errgroup.Group
		eg.SetLimit(concurrency)
		for i := range dirs {
			ino := dirs[i]
			entries := &entriesList[i]
			stat := &dirStats[i]
			eg.Go(func() error {
				s, err := m.GetDirStat(ctx, ino)
				if err != nil {
					return err
				}
				*stat = *s
				var attr Attr
				if st := m.GetAttr(ctx, ino, &attr); st != 0 && st != syscall.ENOENT {
					return st
				}
				if attr.Nlink == 2 {
					// leaf dir, no need to read entries
					return nil
				}
				if st := m.Readdir(ctx, ino, 0, entries); st != 0 && st != syscall.ENOENT {
					return st
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return errno(err)
		}
		dirs = dirs[:0]
		for i, entries := range entriesList {
			stat := dirStats[i]
			summary.Size += uint64(stat.space)
			summary.Length += uint64(stat.length)
			if entries == nil {
				// leaf dir
				summary.Files += uint64(stat.inodes)
				continue
			}
			for _, e := range entries {
				if bytes.Equal(e.Name, []byte(".")) || bytes.Equal(e.Name, []byte("..")) {
					continue
				}
				if e.Attr.Typ == TypeDirectory {
					summary.Dirs++
					if recursive {
						dirs = append(dirs, e.Inode)
					}
				} else {
					summary.Files++
				}
			}
		}
	}
	return 0
}
