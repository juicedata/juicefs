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
	"syscall"
	"time"

	"xorm.io/xorm"
)

type flock struct {
	Inode Ino    `xorm:"notnull unique(flock)"`
	Sid   uint64 `xorm:"notnull unique(flock)"`
	Owner uint64 `xorm:"notnull unique(flock)"`
	Ltype byte   `xorm:"notnull"`
}

func (m *dbMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	if ltype == syscall.F_UNLCK {
		return errno(m.txn(func(s *xorm.Session) error {
			_, err := s.Delete(&flock{Inode: inode, Owner: owner, Sid: m.sid})
			return err
		}))
	}
	var err syscall.Errno
	for {
		err = errno(m.txn(func(s *xorm.Session) error {
			var suffix string
			if m.engine.DriverName() != "sqlite3" {
				suffix = " for update"
			}
			rows, err := s.SQL("select * from jfs_flock where inode=?"+suffix, inode).Rows(&flock{Inode: inode})
			if err != nil {
				return err
			}
			type key struct {
				sid uint64
				o   uint64
			}
			var locks = make(map[key]flock)
			var l flock
			for rows.Next() {
				if rows.Scan(&l) == nil {
					locks[key{l.Sid, l.Owner}] = l
				}
			}
			rows.Close()

			if ltype == syscall.F_RDLCK {
				for _, l := range locks {
					if l.Ltype == 'W' {
						return syscall.EAGAIN
					}
				}
				_, err := s.Insert(flock{Inode: inode, Owner: owner, Ltype: 'R', Sid: m.sid})
				return err
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
				_, err = s.Insert(flock{Inode: inode, Owner: owner, Ltype: 'W', Sid: m.sid})
			}
			return err
		}))

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

func (m *dbMeta) cleanStaleLocks(sid uint64) {
	_, _ = m.engine.Delete(flock{Sid: sid})
}
