/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

type lockOwner struct {
	sid   uint64
	owner uint64
}

func marshalFlock(ls map[lockOwner]byte) []byte {
	b := utils.NewBuffer(uint32(len(ls)) * 17)
	for o, l := range ls {
		b.Put64(o.sid)
		b.Put64(o.owner)
		b.Put8(l)
	}
	return b.Bytes()
}

func unmarshalFlock(buf []byte) map[lockOwner]byte {
	b := utils.FromBuffer(buf)
	var ls = make(map[lockOwner]byte)
	for b.HasMore() {
		sid := b.Get64()
		owner := b.Get64()
		ltype := b.Get8()
		ls[lockOwner{sid, owner}] = ltype
	}
	return ls
}

func (m *kvMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	ikey := m.flockKey(inode)
	var err error
	lkey := lockOwner{m.sid, owner}
	for {
		err = m.txn(func(tx kvTxn) error {
			v := tx.get(ikey)
			ls := unmarshalFlock(v)
			switch ltype {
			case F_UNLCK:
				delete(ls, lkey)
			case F_RDLCK:
				for _, l := range ls {
					if l == 'W' {
						return syscall.EAGAIN
					}
				}
				ls[lkey] = 'R'
			case F_WRLCK:
				delete(ls, lkey)
				if len(ls) > 0 {
					return syscall.EAGAIN
				}
				ls[lkey] = 'W'
			default:
				return syscall.EINVAL
			}
			if len(ls) == 0 {
				tx.dels(ikey)
			} else {
				tx.set(ikey, marshalFlock(ls))
			}
			return nil
		})

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

func marshalPlock(ls map[lockOwner][]byte) []byte {
	var size uint32
	for _, l := range ls {
		size += 8 + 8 + 4 + uint32(len(l))
	}
	b := utils.NewBuffer(size)
	for k, records := range ls {
		b.Put64(k.sid)
		b.Put64(uint64(k.owner))
		b.Put32(uint32(len(records)))
		b.Put(records)
	}
	return b.Bytes()
}

func unmarshalPlock(buf []byte) map[lockOwner][]byte {
	b := utils.FromBuffer(buf)
	var ls = make(map[lockOwner][]byte)
	for b.HasMore() {
		sid := b.Get64()
		owner := b.Get64()
		records := b.Get(int(b.Get32()))
		ls[lockOwner{sid, owner}] = records
	}
	return ls
}

func (m *kvMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	if *ltype == F_UNLCK {
		*start = 0
		*end = 0
		*pid = 0
		return 0
	}
	v, err := m.get(m.plockKey(inode))
	if err != nil {
		return errno(err)
	}
	owners := unmarshalPlock(v)
	delete(owners, lockOwner{m.sid, owner})
	for o, records := range owners {
		ls := loadLocks(records)
		for _, l := range ls {
			// find conflicted locks
			if (*ltype == F_WRLCK || l.ltype == F_WRLCK) && *end >= l.start && *start <= l.end {
				*ltype = l.ltype
				*start = l.start
				*end = l.end
				if o.sid == m.sid {
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

func (m *kvMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	ikey := m.plockKey(inode)
	var err error
	lock := plockRecord{ltype, pid, start, end}
	lkey := lockOwner{m.sid, owner}
	for {
		err = m.txn(func(tx kvTxn) error {
			owners := unmarshalPlock(tx.get(ikey))
			if ltype == F_UNLCK {
				records := owners[lkey]
				ls := loadLocks(records)
				if len(ls) == 0 {
					return nil // change nothing
				}
				ls = updateLocks(ls, lock)
				if len(ls) == 0 {
					delete(owners, lkey)
				} else {
					owners[lkey] = dumpLocks(ls)
				}
			} else {
				ls := loadLocks(owners[lkey])
				delete(owners, lkey)
				for _, d := range owners {
					ls := loadLocks(d)
					for _, l := range ls {
						// find conflicted locks
						if (ltype == F_WRLCK || l.ltype == F_WRLCK) && end >= l.start && start <= l.end {
							return syscall.EAGAIN
						}
					}
				}
				ls = updateLocks(ls, lock)
				owners[lkey] = dumpLocks(ls)
			}
			if len(owners) == 0 {
				tx.dels(ikey)
			} else {
				tx.set(ikey, marshalPlock(owners))
			}
			return nil
		})

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
