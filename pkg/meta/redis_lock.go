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
	"context"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

func (r *redisMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	ikey := r.flockKey(inode)
	lkey := r.ownerKey(owner)
	if ltype == F_UNLCK {
		return errno(r.txn(ctx, func(tx *redis.Tx) error {
			lkeys, err := tx.HKeys(ctx, ikey).Result()
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HDel(ctx, ikey, lkey)
				if len(lkeys) == 1 && lkeys[0] == lkey {
					pipe.SRem(ctx, r.lockedKey(r.sid), ikey)
				}
				return nil
			})
			return err
		}, ikey))
	}
	var err error
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			owners, err := tx.HGetAll(ctx, ikey).Result()
			if err != nil {
				return err
			}
			delete(owners, lkey)
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
	return errno(err)
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
			if (*ltype == F_WRLCK || l.Type == F_WRLCK) && *end >= l.Start && *start <= l.End {
				*ltype = l.Type
				*start = l.Start
				*end = l.End
				sid, _ := strconv.Atoi(strings.Split(k, "_")[0])
				if uint64(sid) == r.sid {
					*pid = l.Pid
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
	var err error
	lock := plockRecord{ltype, pid, start, end}
	for {
		err = r.txn(ctx, func(tx *redis.Tx) error {
			if ltype == F_UNLCK {
				d, err := tx.HGet(ctx, ikey, lkey).Result()
				if err != nil && err != redis.Nil {
					return err
				}
				ls := loadLocks([]byte(d))
				if len(ls) == 0 {
					return nil
				}
				ls = updateLocks(ls, lock)
				var lkeys []string
				if len(ls) == 0 {
					lkeys, err = tx.HKeys(ctx, ikey).Result()
					if err != nil {
						return err
					}
				}
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					if len(ls) == 0 {
						pipe.HDel(ctx, ikey, lkey)
						if len(lkeys) == 1 && lkeys[0] == lkey {
							pipe.SRem(ctx, r.lockedKey(r.sid), ikey)
						}
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
					if (ltype == F_WRLCK || l.Type == F_WRLCK) && end >= l.Start && start <= l.End {
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
	return errno(err)
}

func (r *redisMeta) ListLocks(ctx context.Context, inode Ino) ([]PLockItem, []FLockItem, error) {
	fKey := r.flockKey(inode)
	pKey := r.plockKey(inode)

	rawFLocks, err := r.rdb.HGetAll(ctx, fKey).Result()
	if err != nil {
		return nil, nil, err
	}
	flocks := make([]FLockItem, 0, len(rawFLocks))
	for k, v := range rawFLocks {
		owner, err := parseOwnerKey(k)
		if err != nil {
			return nil, nil, err
		}
		flocks = append(flocks, FLockItem{*owner, v})
	}

	rawPLocks, err := r.rdb.HGetAll(ctx, pKey).Result()
	if err != nil {
		return nil, nil, err
	}
	plocks := make([]PLockItem, 0)
	for k, d := range rawPLocks {
		owner, err := parseOwnerKey(k)
		if err != nil {
			return nil, nil, err
		}
		ls := loadLocks([]byte(d))
		for _, l := range ls {
			plocks = append(plocks, PLockItem{*owner, l})
		}
	}
	return plocks, flocks, nil
}
