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
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/go-redis/redis/v8"
	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	usedSpace    = "usedSpace"
	totalInodes  = "totalInodes"
	delfiles     = "delfiles"
	allSessions  = "sessions"
	sessionInfos = "sessionInfos"
	sliceRefs    = "sliceRef"
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

type msgCallbacks struct {
	sync.Mutex
	callbacks map[uint32]MsgCallback
}

type freeID struct {
	next  uint64
	maxid uint64
}

var logger = utils.GetLogger("juicefs")

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

func lookupSubdir(m Meta, subdir string) (Ino, error) {
	var root Ino = 1
	for subdir != "" {
		ps := strings.SplitN(subdir, "/", 2)
		if ps[0] != "" {
			var attr Attr
			r := m.Lookup(Background, root, ps[0], &root, &attr)
			if r != 0 {
				return 0, fmt.Errorf("lookup subdir %s: %s", ps[0], r)
			}
			if attr.Typ != TypeDirectory {
				return 0, fmt.Errorf("%s is not a redirectory", ps[0])
			}
		}
		if len(ps) == 1 {
			break
		}
		subdir = ps[1]
	}
	return root, nil
}

type plockRecord struct {
	ltype uint32
	pid   uint32
	start uint64
	end   uint64
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
		wb.Put32(l.ltype)
		wb.Put32(l.pid)
		wb.Put64(l.start)
		wb.Put64(l.end)
	}
	return wb.Bytes()
}

func updateLocks(ls []plockRecord, nl plockRecord) []plockRecord {
	// ls is ordered by l.start without overlap
	size := len(ls)
	for i := 0; i < size && nl.start <= nl.end; i++ {
		l := ls[i]
		if nl.start < l.start && nl.end >= l.start {
			// split nl
			ls = append(ls, nl)
			ls[len(ls)-1].end = l.start - 1
			nl.start = l.start
		}
		if nl.start > l.start && nl.start <= l.end {
			// split l
			l.end = nl.start - 1
			ls = append(ls, l)
			ls[i].start = nl.start
			l = ls[i]
		}
		if nl.start == l.start {
			ls[i].ltype = nl.ltype // update l
			ls[i].pid = nl.pid
			if l.end > nl.end {
				// split l
				ls[i].end = nl.end
				l.start = nl.end + 1
				ls = append(ls, l)
			}
			nl.start = ls[i].end + 1
		}
	}
	if nl.start <= nl.end {
		ls = append(ls, nl)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].start < ls[j].start })
	for i := 0; i < len(ls); {
		if ls[i].ltype == F_UNLCK || ls[i].start > ls[i].end {
			// remove empty one
			copy(ls[i:], ls[i+1:])
			ls = ls[:len(ls)-1]
		} else {
			if i+1 < len(ls) && ls[i].ltype == ls[i+1].ltype && ls[i].pid == ls[i+1].pid && ls[i].end+1 == ls[i+1].start {
				// combine continuous range
				ls[i].end = ls[i+1].end
				ls[i+1].start = ls[i+1].end + 1
			}
			i++
		}
	}
	return ls
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

func GetSummary(r Meta, ctx Context, inode Ino, summary *Summary, recursive bool) syscall.Errno {
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
				if recursive {
					if st := GetSummary(r, ctx, e.Inode, summary, recursive); st != 0 {
						return st
					}
				} else {
					summary.Dirs++
					summary.Size += 4096
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
