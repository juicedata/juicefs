//go:build !nosqlite || !nomysql || !nopg
// +build !nosqlite !nomysql !nopg

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
	"bytes"
	"context"
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
		}, inode))
	}
	var err syscall.Errno
	for {
		err = errno(m.txn(func(s *xorm.Session) error {
			if exists, err := s.ForUpdate().Get(&node{Inode: inode}); err != nil || !exists {
				if err == nil && !exists {
					err = syscall.ENOENT
				}
				return err
			}
			var fs []flock
			err := s.ForUpdate().Find(&fs, &flock{Inode: inode})
			if err != nil {
				return err
			}
			type key struct {
				sid uint64
				o   int64
			}
			var locks = make(map[key]flock)
			for _, l := range fs {
				locks[key{l.Sid, l.Owner}] = l
			}

			me := key{m.sid, owner}
			flk, ok := locks[me]
			delete(locks, me)
			var typec byte = 'W'
			if ltype == F_RDLCK {
				for _, l := range locks {
					if l.Ltype == 'W' {
						return syscall.EAGAIN
					}
				}
				typec = 'R'
			} else if len(locks) > 0 {
				return syscall.EAGAIN
			}
			var n int64
			if ok {
				if flk.Ltype != typec {
					n, err = s.Cols("Ltype").Update(&flock{Ltype: typec}, &flock{Inode: inode, Owner: owner, Sid: m.sid})
				} else {
					n = 1
				}
			} else {
				n, err = s.InsertOne(&flock{Inode: inode, Owner: owner, Ltype: typec, Sid: m.sid})
			}
			if err == nil && n == 0 {
				err = fmt.Errorf("insert/update failed")
			}
			return err
		}, inode))

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
	rows, err := m.db.Rows(&plock{Inode: inode})
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
		l.Records = nil
		if rows.Scan(&l) == nil && !(l.Sid == m.sid && l.Owner == owner) {
			locks[key{l.Sid, l.Owner}] = dup(l.Records)
		}
	}
	_ = rows.Close()

	for k, d := range locks {
		ls := loadLocks(d)
		for _, l := range ls {
			// find conflicted locks
			if (*ltype == F_WRLCK || l.Type == F_WRLCK) && *end >= l.Start && *start <= l.End {
				*ltype = l.Type
				*start = l.Start
				*end = l.End
				if k.sid == m.sid {
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

func (m *dbMeta) Setlk(ctx Context, inode Ino, owner_ uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	var err syscall.Errno
	lock := plockRecord{ltype, pid, start, end}
	owner := int64(owner_)
	for {
		err = errno(m.txn(func(s *xorm.Session) error {
			if exists, err := s.ForUpdate().Get(&node{Inode: inode}); err != nil || !exists {
				if err == nil && !exists {
					err = syscall.ENOENT
				}
				return err
			}
			if ltype == F_UNLCK {
				var l = plock{Inode: inode, Owner: owner, Sid: m.sid}
				ok, err := s.ForUpdate().Get(&l)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				ls := loadLocks(l.Records)
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
			var ps []plock
			err := s.ForUpdate().Find(&ps, &plock{Inode: inode})
			if err != nil {
				return err
			}
			type key struct {
				sid   uint64
				owner int64
			}
			var locks = make(map[key][]byte)
			for _, l := range ps {
				locks[key{l.Sid, l.Owner}] = l.Records
			}
			lkey := key{m.sid, owner}
			for k, d := range locks {
				if k == lkey {
					continue
				}
				ls := loadLocks(d)
				for _, l := range ls {
					// find conflicted locks
					if (ltype == F_WRLCK || l.Type == F_WRLCK) && end >= l.Start && start <= l.End {
						return syscall.EAGAIN
					}
				}
			}
			ls := updateLocks(loadLocks(locks[lkey]), lock)
			var n int64
			records := dumpLocks(ls)
			if len(locks[lkey]) > 0 {
				if !bytes.Equal(locks[lkey], records) {
					n, err = s.Cols("records").Update(plock{Records: records},
						&plock{Inode: inode, Sid: m.sid, Owner: owner})
				} else {
					n = 1
				}
			} else {
				n, err = s.InsertOne(&plock{Inode: inode, Sid: m.sid, Owner: owner, Records: records})
			}
			if err == nil && n == 0 {
				err = fmt.Errorf("insert/update failed")
			}
			return err
		}, inode))

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

func (r *dbMeta) ListLocks(ctx context.Context, inode Ino) ([]PLockItem, []FLockItem, error) {
	var fs []flock
	if err := r.db.Find(&fs, &flock{Inode: inode}); err != nil {
		return nil, nil, err
	}

	flocks := make([]FLockItem, 0, len(fs))
	for _, f := range fs {
		flocks = append(flocks, FLockItem{ownerKey{f.Sid, uint64(f.Owner)}, string(f.Ltype)})
	}

	var ps []plock
	if err := r.db.Find(&ps, &plock{Inode: inode}); err != nil {
		return nil, nil, err
	}

	plocks := make([]PLockItem, 0)
	for _, p := range ps {
		ls := loadLocks(p.Records)
		for _, l := range ls {
			plocks = append(plocks, PLockItem{ownerKey{p.Sid, uint64(p.Owner)}, l})
		}
	}
	return plocks, flocks, nil
}
