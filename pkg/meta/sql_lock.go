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
	"fmt"
	"syscall"
	"time"

	"xorm.io/xorm"
)

func (m *dbMeta) Flock(ctx Context, inode Ino, owner_ uint64, ltype uint32, block bool) syscall.Errno {
	owner := int64(owner_)
	if ltype == F_UNLCK {
		return errno(m.txn(func(s *xorm.Session) error {
			_, err := s.Delete(&flock{Inode: inode, Owner: owner, Sid: m.sid})
			return err
		}))
	}
	var err syscall.Errno
	for {
		err = errno(m.txn(func(s *xorm.Session) error {
			if exists, err := s.Get(&node{Inode: inode}); err != nil || !exists {
				if err == nil && !exists {
					err = syscall.ENOENT
				}
				return err
			}
			rows, err := s.Rows(&flock{Inode: inode})
			if err != nil {
				return err
			}
			type key struct {
				sid uint64
				o   int64
			}
			var locks = make(map[key]flock)
			var l flock
			for rows.Next() {
				if rows.Scan(&l) == nil {
					locks[key{l.Sid, l.Owner}] = l
				}
			}
			rows.Close()

			if ltype == F_RDLCK {
				for _, l := range locks {
					if l.Ltype == 'W' {
						return syscall.EAGAIN
					}
				}
				return mustInsert(s, flock{Inode: inode, Owner: owner, Ltype: 'R', Sid: m.sid})
			}
			me := key{m.sid, owner}
			_, ok := locks[me]
			delete(locks, me)
			if len(locks) > 0 {
				return syscall.EAGAIN
			}
			if ok {
				_, err = s.Cols("Ltype").Update(&flock{Ltype: 'W'}, &flock{Inode: inode, Owner: owner, Sid: m.sid})
			} else {
				err = mustInsert(s, flock{Inode: inode, Owner: owner, Ltype: 'W', Sid: m.sid})
			}
			return err
		}))

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

func (m *dbMeta) Getlk(ctx Context, inode Ino, owner_ uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	if *ltype == F_UNLCK {
		*start = 0
		*end = 0
		*pid = 0
		return 0
	}

	owner := int64(owner_)
	rows, err := m.engine.Rows(&plock{Inode: inode})
	if err != nil {
		return errno(err)
	}
	type key struct {
		sid uint64
		o   int64
	}
	var locks = make(map[key][]byte)
	var l plock
	for rows.Next() {
		if rows.Scan(&l) == nil && !(l.Sid == m.sid && l.Owner == owner) {
			locks[key{l.Sid, l.Owner}] = dup(l.Records)
		}
	}
	rows.Close()

	for k, d := range locks {
		ls := loadLocks([]byte(d))
		for _, l := range ls {
			// find conflicted locks
			if (*ltype == F_WRLCK || l.ltype == F_WRLCK) && *end >= l.start && *start <= l.end {
				*ltype = l.ltype
				*start = l.start
				*end = l.end
				if k.sid == m.sid {
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

func (m *dbMeta) Setlk(ctx Context, inode Ino, owner_ uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	var err syscall.Errno
	lock := plockRecord{ltype, pid, start, end}
	owner := int64(owner_)
	for {
		err = errno(m.txn(func(s *xorm.Session) error {
			if exists, err := s.Get(&node{Inode: inode}); err != nil || !exists {
				if err == nil && !exists {
					err = syscall.ENOENT
				}
				return err
			}
			if ltype == F_UNLCK {
				var l = plock{Inode: inode, Owner: owner, Sid: m.sid}
				ok, err := m.engine.Get(&l)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				ls := loadLocks([]byte(l.Records))
				if len(ls) == 0 {
					return nil
				}
				ls = updateLocks(ls, lock)
				if len(ls) == 0 {
					_, err = s.Delete(&plock{Inode: inode, Owner: owner, Sid: m.sid})
				} else {
					_, err = s.Cols("records").Update(plock{Records: dumpLocks(ls)}, l)
				}
				return err
			}
			rows, err := s.Rows(&plock{Inode: inode})
			if err != nil {
				return err
			}
			type key struct {
				sid   uint64
				owner int64
			}
			var locks = make(map[key][]byte)
			var l plock
			for rows.Next() {
				if rows.Scan(&l) == nil {
					locks[key{l.Sid, l.Owner}] = dup(l.Records)
				}
			}
			rows.Close()
			lkey := key{m.sid, owner}
			for k, d := range locks {
				if k == lkey {
					continue
				}
				ls := loadLocks([]byte(d))
				for _, l := range ls {
					// find conflicted locks
					if (ltype == F_WRLCK || l.ltype == F_WRLCK) && end >= l.start && start <= l.end {
						return syscall.EAGAIN
					}
				}
			}
			ls := updateLocks(loadLocks([]byte(locks[lkey])), lock)
			var n int64
			if len(locks[lkey]) > 0 {
				n, err = s.Cols("records").Update(plock{Records: dumpLocks(ls)},
					&plock{Inode: inode, Sid: m.sid, Owner: owner})
			} else {
				n, err = s.InsertOne(&plock{Inode: inode, Sid: m.sid, Owner: owner, Records: dumpLocks(ls)})
			}
			if err == nil && n == 0 {
				err = fmt.Errorf("insert/update failed")
			}
			return err
		}))

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
