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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/juicedata/juicefs/pkg/utils"
)

func (r *redisMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	ikey := r.flockKey(inode)
	lkey := r.ownerKey(owner)
	if ltype == F_UNLCK {
		_, err := r.rdb.HDel(ctx, ikey, lkey).Result()
		return errno(err)
	}
	var err syscall.Errno
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			owners, err := tx.HGetAll(ctx, ikey).Result()
			if err != nil {
				return err
			}
			if ltype == F_RDLCK {
				for _, v := range owners {
					if v == "W" {
						return syscall.EAGAIN
					}
				}
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					pipe.HSet(ctx, ikey, lkey, "R")
					return nil
				})
				return err
			}
			delete(owners, lkey)
			if len(owners) > 0 {
				return syscall.EAGAIN
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, ikey, lkey, "W")
				pipe.SAdd(ctx, r.lockedKey(r.sid), ikey)
				return nil
			})
			return err
		}, ikey)

		if !block || err != syscall.EAGAIN {
			break
		}
		if ltype == F_WRLCK {
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

func (r *redisMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	if *ltype == F_UNLCK {
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
		ls := loadLocks([]byte(d))
		for _, l := range ls {
			// find conflicted locks
			if (*ltype == F_WRLCK || l.ltype == F_WRLCK) && *end >= l.start && *start <= l.end {
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
	*ltype = F_UNLCK
	*start = 0
	*end = 0
	*pid = 0
	return 0
}

func (r *redisMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	ikey := r.plockKey(inode)
	lkey := r.ownerKey(owner)
	var err syscall.Errno
	lock := plockRecord{ltype, pid, start, end}
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			if ltype == F_UNLCK {
				d, err := tx.HGet(ctx, ikey, lkey).Result()
				if err != nil {
					return err
				}
				ls := loadLocks([]byte(d))
				if len(ls) == 0 {
					return nil
				}
				ls = updateLocks(ls, lock)
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					if len(ls) == 0 {
						pipe.HDel(ctx, ikey, lkey)
					} else {
						pipe.HSet(ctx, ikey, lkey, dumpLocks(ls))
					}
					return nil
				})
				return err
			}
			owners, err := tx.HGetAll(ctx, ikey).Result()
			if err != nil {
				return err
			}
			ls := loadLocks([]byte(owners[lkey]))
			delete(owners, lkey)
			for _, d := range owners {
				ls := loadLocks([]byte(d))
				for _, l := range ls {
					// find conflicted locks
					if (ltype == F_WRLCK || l.ltype == F_WRLCK) && end >= l.start && start <= l.end {
						return syscall.EAGAIN
					}
				}
			}
			ls = updateLocks(ls, lock)
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, ikey, lkey, dumpLocks(ls))
				pipe.SAdd(ctx, r.lockedKey(r.sid), ikey)
				return nil
			})
			return err
		}, ikey)

		if !block || err != syscall.EAGAIN {
			break
		}
		if ltype == F_WRLCK {
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
