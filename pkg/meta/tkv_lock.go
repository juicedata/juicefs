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
		}, inode)

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
		b.Put64(k.owner)
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
			if (*ltype == F_WRLCK || l.Type == F_WRLCK) && *end >= l.Start && *start <= l.End {
				*ltype = l.Type
				*start = l.Start
				*end = l.End
				if o.sid == m.sid {
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
						if (ltype == F_WRLCK || l.Type == F_WRLCK) && end >= l.Start && start <= l.End {
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
		}, inode)

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
