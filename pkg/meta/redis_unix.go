// +build !windows

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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/juicedata/juicefs/pkg/utils"
)

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
