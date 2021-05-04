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
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"xorm.io/xorm"
)

// DBConfig is config for SQL client.
type DBConfig struct {
	Strict      bool // update ctime
	Retries     int
	CaseInsensi bool
}

type setting struct {
	Name  string `xorm:"pk"`
	Value string `xorm:"varchar(4096)"`
}

type counter struct {
	Name  string `xorm:"pk"`
	Value uint64
}

type edge struct {
	Parent Ino    `xorm:"unique(edge)"`
	Name   string `xorm:"unique(edge)"`
	Inode  Ino
	Type   uint8
}

type node struct {
	Inode  Ino `xorm:"pk"`
	Type   uint8
	Flags  uint8
	Mode   uint16
	Uid    uint32
	Gid    uint32
	Atime  time.Time
	Mtime  time.Time
	Ctime  time.Time `xorm:"updated"`
	Nlink  uint32
	Length uint64
	Rdev   uint32
	Parent Ino
}

type chunk struct {
	Inode  Ino    `xorm:"unique(chunk)"`
	Indx   uint32 `xorm:"unique(chunk)"`
	Slices []byte `xorm:"VARBINARY"`
}

type symlink struct {
	Inode  Ino    `xorm:"pk"`
	Target string `xorm:"varchar(4096)"`
}

type session struct {
	Sid       uint64 `xorm:"pk"`
	Heartbeat time.Time
}

type delchunk struct {
	Inode  Ino
	Start  uint64
	End    uint64
	Maxid  uint64
	Expire time.Time
}

type xattr struct {
	Inode Ino    `xorm:"unique(name)"`
	Name  string `xorm:"unique(name)"`
	Value []byte `xorm:"VARBINARY"`
}

type dbMeta struct {
	sync.Mutex
	conf   *DBConfig
	engine *xorm.Engine

	sid          int64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
}

func NewSQLMeta(driver, dsn string) (*dbMeta, error) {
	engine, err := xorm.NewEngine(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to use data source %s: %s", driver, err)
	}

	m := &dbMeta{
		conf:         &DBConfig{},
		engine:       engine,
		openFiles:    make(map[Ino]int),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		deleting:     make(chan int, 2),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}
	return m, nil
}

func (m *dbMeta) Init(format Format, force bool) error {
	_ = m.engine.Sync2(new(setting))
	_ = m.engine.Sync2(new(counter))
	_ = m.engine.Sync2(new(node))
	_ = m.engine.Sync2(new(edge))
	_ = m.engine.Sync2(new(symlink))
	_ = m.engine.Sync2(new(chunk))
	_ = m.engine.Sync2(new(xattr))

	f, err := m.Load()
	if err != nil {
		return err
	}
	if f != nil {
		// TODO: update
		return nil
	}
	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}
	_, err = m.engine.Insert(&setting{"format", string(data)})
	if err != nil {
		return err
	}

	now := time.Now()
	_, err = m.engine.Insert(&node{
		Inode:  1,
		Type:   TypeDirectory,
		Mode:   0777,
		Atime:  now,
		Mtime:  now,
		Ctime:  now,
		Nlink:  2,
		Length: 4 << 10,
		Parent: 1,
	})
	_, _ = m.engine.Insert(
		counter{"nextinode", 2},
		counter{"nextchunkid", 1},
		counter{"nextsession", 1},
		counter{"usedSpace", 0},
		counter{"totalInodes", 0})
	return err
}

func (m *dbMeta) Load() (*Format, error) {
	var s setting
	has, err := m.engine.Where("name = ?", "format").Get(&s)
	if err != nil || !has {
		return nil, err
	}

	var format Format
	err = json.Unmarshal([]byte(s.Value), &format)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &format, nil
}

func (m *dbMeta) NewSession() error {
	v, err := m.incrCounter("nextsession")
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	m.sid = int64(v)
	logger.Debugf("session is %d", m.sid)

	// go r.refreshSession()
	// go r.cleanupDeletedFiles()
	// go r.cleanupSlices()
	return nil
}

func (m *dbMeta) OnMsg(mtype uint32, cb MsgCallback) {
	m.msgCallbacks.Lock()
	defer m.msgCallbacks.Unlock()
	m.msgCallbacks.callbacks[mtype] = cb
}

func (m *dbMeta) newMsg(mid uint32, args ...interface{}) error {
	m.msgCallbacks.Lock()
	cb, ok := m.msgCallbacks.callbacks[mid]
	m.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (m *dbMeta) incrCounter(name string) (uint64, error) {
	r, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var c counter
		_, err := s.Where("name = ?", name).Get(&c)
		if err != nil {
			return nil, err
		}
		c.Value++
		_, err = s.Cols("value").Update(&c, &counter{Name: name})
		if err != nil {
			return nil, err
		}
		return c.Value - 1, nil
	})
	return r.(uint64), err
}

func (m *dbMeta) nextInode() (Ino, error) {
	v, err := m.incrCounter("nextinode")
	return Ino(v), err
}

func (m *dbMeta) txn(f func(s *xorm.Session) error) syscall.Errno {
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		return nil, f(s)
	})
	return errno(err)
}

func (m *dbMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	var c counter
	m.engine.Where("name='usedSpace'").Get(&c)
	*totalspace = 1 << 50
	for *totalspace < c.Value {
		*totalspace *= 2
	}
	*availspace = *totalspace - c.Value
	m.engine.Where("name='totalInodes'").Get(&c)
	*iused = c.Value
	*iavail = 10 << 20
	return 0
}

func (m *dbMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var e edge
	has, err := m.engine.Where("Parent = ? and Name = ?", parent, name).Get(&e)
	if err != nil {
		return errno(err)
	}
	if !has {
		return syscall.ENOENT
	}
	if attr == nil {
		*inode = Ino(e.Inode)
		return 0
	}
	var n node
	has, err = m.engine.Where("inode = ?", e.Inode).Get(&n)
	if err != nil {
		return errno(err)
	}
	if !has {
		return syscall.ENOENT
	}
	*inode = Ino(e.Inode)
	m.parseAttr(&n, attr)
	return 0
}

func (m *dbMeta) parseAttr(n *node, attr *Attr) {
	if attr == nil {
		return
	}
	attr.Typ = n.Type
	attr.Mode = n.Mode
	attr.Flags = n.Flags
	attr.Uid = n.Uid
	attr.Gid = n.Gid
	attr.Atime = n.Atime.Unix()
	attr.Atimensec = uint32(n.Atime.Nanosecond())
	attr.Mtime = n.Mtime.Unix()
	attr.Mtimensec = uint32(n.Mtime.Nanosecond())
	attr.Ctime = n.Ctime.Unix()
	attr.Ctimensec = uint32(n.Ctime.Nanosecond())
	attr.Nlink = n.Nlink
	attr.Length = n.Length
	attr.Rdev = uint32(n.Rdev)
	attr.Parent = Ino(n.Parent)
	attr.Full = true
}

func (m *dbMeta) Access(ctx Context, inode Ino, mmask uint8, attr *Attr) syscall.Errno {
	if ctx.Uid() == 0 {
		return 0
	}

	if attr == nil || !attr.Full {
		if attr == nil {
			attr = &Attr{}
		}
		err := m.GetAttr(ctx, inode, attr)
		if err != 0 {
			return err
		}
	}

	mode := accessMode(attr, ctx.Uid(), ctx.Gid())
	if mode&mmask != mmask {
		logger.Debugf("Access inode %d %o, mode %o, request mode %o", inode, attr.Mode, mode, mmask)
		return syscall.EACCES
	}
	return 0
}

func (m *dbMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	var n node
	ok, err := m.engine.Where("Inode = ?", inode).Get(&n)
	if err != nil && inode == 1 {
		err = nil
		n.Type = TypeDirectory
		n.Mode = 0777
		n.Nlink = 2
		n.Length = 4 << 10
	}
	if err != nil {
		return errno(err)
	}
	if !ok {
		return syscall.ENOENT
	}
	m.parseAttr(&n, attr)
	return 0
}

func (m *dbMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var cur node
		ok, err := s.Where("Inode = ?", inode).Get(&cur)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if (set&(SetAttrUID|SetAttrGID)) != 0 && (set&SetAttrMode) != 0 {
			attr.Mode |= (cur.Mode & 06000)
		}
		var changed bool
		if (cur.Mode&06000) != 0 && (set&(SetAttrUID|SetAttrGID)) != 0 {
			if cur.Mode&01777 != cur.Mode {
				cur.Mode &= 01777
				changed = true
			}
			attr.Mode &= 01777
		}
		if set&SetAttrUID != 0 && cur.Uid != attr.Uid {
			cur.Uid = attr.Uid
			changed = true
		}
		if set&SetAttrGID != 0 && cur.Gid != attr.Gid {
			cur.Gid = attr.Gid
			changed = true
		}
		if set&SetAttrMode != 0 {
			if ctx.Uid() != 0 && (attr.Mode&02000) != 0 {
				if ctx.Gid() != cur.Gid {
					attr.Mode &= 05777
				}
			}
			if attr.Mode != cur.Mode {
				cur.Mode = attr.Mode
				changed = true
			}
		}
		now := time.Now()
		if set&SetAttrAtime != 0 && (cur.Atime.Unix() != attr.Atime || uint32(cur.Atime.Nanosecond()) != attr.Atimensec) {
			cur.Atime = time.Unix(attr.Atime, int64(attr.Atimensec))
			changed = true
		}
		if set&SetAttrAtimeNow != 0 {
			cur.Atime = now
			changed = true
		}
		if set&SetAttrMtime != 0 && (cur.Mtime.Unix() != attr.Mtime || uint32(cur.Mtime.Nanosecond()) != attr.Mtimensec) {
			cur.Mtime = time.Unix(attr.Mtime, int64(attr.Mtimensec))
			changed = true
		}
		if set&SetAttrMtimeNow != 0 {
			cur.Mtime = now
			changed = true
		}
		m.parseAttr(&cur, attr)
		if !changed {
			return nil
		}
		_, err = s.Where("inode = ?", inode).Update(&cur)
		if err == nil && attr != nil {
			m.parseAttr(&cur, attr)
		}
		return err
	})
}

func (m *dbMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var n node
		ok, err := s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		n.Length = length
		_, err = s.Cols("length").Update(&n, &node{Inode: n.Inode})
		if err != nil {
			return err
		}
		if err == nil && attr != nil {
			m.parseAttr(&n, attr)
		}
		return nil
	})
}

func (m *dbMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *dbMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	var l symlink
	ok, err := m.engine.Where("inode = ?", inode).Get(&l)
	if err != nil {
		return errno(err)
	}
	if !ok {
		return syscall.ENOENT
	}
	*path = []byte(l.Target)
	m.symlinks.Store(inode, []byte(l.Target))
	return 0
}

func (m *dbMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *dbMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (m *dbMeta) resolveCase(ctx Context, parent Ino, name string) *Entry {
	// TODO in SQL
	var entries []*Entry
	_ = m.Readdir(ctx, parent, 0, &entries)
	for _, e := range entries {
		n := string(e.Name)
		if strings.EqualFold(name, n) {
			return e
		}
	}
	return nil
}

func (m *dbMeta) mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	ino, err := m.nextInode()
	if err != nil {
		return errno(err)
	}
	var n node
	n.Inode = ino
	n.Type = _type
	n.Mode = mode & ^cumask
	n.Uid = ctx.Uid()
	n.Gid = ctx.Gid()
	if _type == TypeDirectory {
		n.Nlink = 2
		n.Length = 4 << 10
	} else {
		n.Nlink = 1
		if _type == TypeSymlink {
			n.Length = uint64(len(path))
		} else {
			n.Length = 0
			n.Rdev = rdev
		}
	}
	n.Parent = parent
	if inode != nil {
		*inode = ino
	}

	return m.txn(func(s *xorm.Session) error {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		now := time.Now()
		if _type == TypeDirectory {
			pn.Nlink++
		}
		pn.Mtime = now
		n.Atime = now
		n.Mtime = now
		if ctx.Value(CtxKey("behavior")) == "Hadoop" {
			n.Gid = pn.Gid
		}

		e.Inode = ino
		e.Parent = parent
		e.Name = name
		e.Type = _type
		_, err = s.Insert(&e)
		if err != nil {
			return err
		}
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		if _, err := s.Insert(&n); err != nil {
			return err
		}
		if _type == TypeSymlink {
			if _, err := s.Insert(&symlink{Inode: ino, Target: path}); err != nil {
				return err
			}
		} else if _type == TypeFile {
			s.Exec("update counter set value=value+? where key='usedSpace'", align4K(0))
		}
		s.Exec("update counter set value=value+1 where key='totalInodes'")
		if err != nil {
			m.parseAttr(&n, attr)
		}
		return err
	})
}

func (m *dbMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	return m.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, inode, attr)
}

func (m *dbMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno {
	err := m.Mknod(ctx, parent, name, TypeFile, mode, cumask, 0, inode, attr)
	if err == 0 && inode != nil {
		m.Lock()
		m.openFiles[*inode] = 1
		m.Unlock()
	}
	return err
}

func (m *dbMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parent, name) == nil) {
			return syscall.ENOENT
		}
		if e.Type == TypeDirectory {
			return syscall.EPERM
		}

		var n node
		ok, err = s.Where("Inode = ?", e.Inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}

		now := time.Now()
		pn.Mtime = now
		pn.Ctime = time.Now()
		n.Nlink--
		var opened bool
		if e.Type == TypeFile && n.Nlink == 0 {
			m.Lock()
			opened = m.openFiles[Ino(e.Inode)] > 0
			m.Unlock()
		}

		if _, err := s.Delete(&edge{Parent: parent, Name: name}); err != nil {
			return err
		}
		if _, err := s.Delete(&xattr{Inode: e.Inode}); err != nil {
			return err
		}
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		s.Exec("update counter set value=value-1 where key='totalInodes'")

		if n.Nlink > 0 {
			if _, err := s.Update(&n, &node{Inode: e.Inode}); err != nil {
				return err
			}
		} else {
			switch e.Type {
			case TypeSymlink:
				if _, err := s.Delete(&symlink{Inode: e.Inode}); err != nil {
					return err
				}
				if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
					return err
				}
			case TypeFile:
				if opened {
					if _, err := s.Update(&n, &node{Inode: e.Inode}); err != nil {
						return err
					}
					// pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(inode)))
				} else {
					// pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(inode, attr.Length)})
					if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
						return err
					}
					s.Exec("update counter set value=value-? where key='usedSpace'", align4K(n.Length))
				}
			}
		}
		if err == nil && e.Type == TypeFile && n.Nlink == 0 {
			if opened {
				m.Lock()
				m.removedFiles[Ino(e.Inode)] = true
				m.Unlock()
			} else {
				// go m.deleteFile(inode, attr.Length, "")
			}
		}
		return err
	})
}

func (m *dbMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}

	return m.txn(func(s *xorm.Session) error {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parent, name) == nil) {
			return syscall.ENOENT
		}
		if e.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		cnt, err := s.Where("parent = ?", e.Inode).Count(&edge{})
		if err != nil {
			return err
		}
		if cnt != 0 {
			return syscall.ENOTEMPTY
		}

		now := time.Now()
		pn.Nlink--
		pn.Mtime = now
		if _, err := s.Delete(&edge{Parent: parent, Name: name}); err != nil {
			return err
		}
		if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
			return err
		}
		if _, err := s.Delete(&xattr{Inode: e.Inode}); err != nil {
			return err
		}
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		s.Exec("update counter set value=value-1 where key='totalInodes'")
		return err
	})
}

func (m *dbMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var spn node
		ok, err := s.Where("Inode = ?", parentSrc).Get(&spn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if spn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var se edge
		ok, err = s.Where("parent=? and name=?", parentSrc, nameSrc).Get(&se)
		if err != nil {
			return err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parentSrc, nameSrc) == nil) {
			return syscall.ENOENT
		}

		if parentSrc == parentDst && nameSrc == nameDst {
			if inode != nil {
				*inode = Ino(se.Inode)
			}
			return nil
		}

		var sn node
		ok, err = s.Where("Inode = ?", se.Inode).Get(&sn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}

		var dpn node
		ok, err = s.Where("Inode = ?", parentDst).Get(&dpn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if dpn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var de edge
		ok, err = s.Where("parent=? and name=?", parentDst, nameDst).Get(&de)
		if err != nil {
			return err
		}
		var opened bool
		var dino Ino
		var dn node
		if ok {
			dino = Ino(de.Inode)
			if ctx.Value(CtxKey("behavior")) == "Hadoop" {
				return syscall.EEXIST
			}
			if de.Type == TypeDirectory {
				cnt, err := s.Where("parent = ?", de.Inode).Count(&edge{})
				if err != nil {
					return err
				}
				if cnt != 0 {
					return syscall.ENOTEMPTY
				}
			} else {
				ok, err := s.Where("Inode = ?", de.Inode).Get(&dn)
				if err != nil {
					return errno(err)
				}
				if !ok {

				}
				dn.Nlink--
				if dn.Nlink > 0 {
					dn.Ctime = time.Now()
				} else if dn.Type == TypeFile {
					m.Lock()
					opened = m.openFiles[Ino(dn.Inode)] > 0
					m.Unlock()
				}
			}
		}

		now := time.Now()
		spn.Mtime = now
		dpn.Mtime = now
		sn.Parent = parentDst
		if sn.Type == TypeDirectory && parentSrc != parentDst {
			spn.Nlink--
			dpn.Nlink++
		}
		if attr != nil {
			m.parseAttr(&sn, attr)
		}

		s.Delete(&edge{Parent: parentSrc, Name: nameSrc})
		s.Update(&spn, &node{Inode: parentSrc})
		if dino > 0 {
			if dn.Type != TypeDirectory && dn.Nlink > 0 {
				s.Update(dn, &node{Inode: dn.Inode})
			} else {
				if dn.Type == TypeDirectory {
					s.Delete(&node{Inode: dn.Inode})
					dn.Nlink--
				} else if dn.Type == TypeSymlink {
					s.Delete(&symlink{Inode: dn.Inode})
					s.Delete(&node{Inode: dn.Inode})
				} else if dn.Type == TypeFile {
					if opened {
						s.Update(&dn, &node{Inode: dn.Inode})
						// pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(dino)))
					} else {
						// pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(dino, dattr.Length)})
						s.Delete(&node{Inode: dn.Inode})
						s.Exec("update counter set value=value-? where key='usedSpace'", align4K(dn.Length))
					}
				}
				s.Exec("update counter set value=value-1 where key='totalInodes'")
				// pipe.Del(ctx, r.xattrKey(dino))
			}
			s.Delete(&edge{Parent: parentDst, Name: nameDst})
		}
		s.Insert(&edge{parentDst, nameDst, sn.Inode, sn.Type})
		if parentDst != parentSrc {
			s.Update(&dpn, &node{Inode: parentDst})
		}
		s.Update(&sn, &node{Inode: sn.Inode})
		if err == nil && dino > 0 && dn.Type == TypeFile {
			if opened {
				m.Lock()
				m.removedFiles[dino] = true
				m.Unlock()
			} else {
				// go r.deleteFile(dino, dattr.Length, "")
			}
		}
		return err
	})
}

func (m *dbMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		var n node
		ok, err = s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type == TypeDirectory {
			return syscall.EPERM
		}

		now := time.Now()
		pn.Mtime = now
		n.Nlink++

		if ok, err := s.Insert(&edge{Parent: parent, Name: name, Inode: inode, Type: n.Type}); err != nil || ok == 0 {
			return err
		}
		if _, err := s.Update(&pn, &node{Inode: parent}); err != nil {
			return err
		}
		if _, err := s.Cols("ctime", "nlink").Update(&n, node{Inode: inode}); err != nil {
			return err
		}
		if err == nil && attr != nil {
			m.parseAttr(&n, attr)
		}
		return err
	})
}

func (m *dbMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	var attr Attr
	eno := m.GetAttr(ctx, inode, &attr)
	if eno != 0 {
		return eno
	}
	*entries = nil
	*entries = append(*entries, &Entry{
		Inode: inode,
		Name:  []byte("."),
		Attr:  &attr,
	})
	var pattr Attr
	eno = m.GetAttr(ctx, attr.Parent, &pattr)
	if eno != 0 {
		return eno
	}
	*entries = append(*entries, &Entry{
		Inode: attr.Parent,
		Name:  []byte(".."),
		Attr:  &pattr,
	})

	e := new(edge)
	rows, err := m.engine.Where("parent=? ", inode).Rows(e)
	if err != nil {
		return errno(err)
	}
	names := make(map[Ino][]byte)
	var inodes []string
	for rows.Next() {
		err = rows.Scan(e)
		if err != nil {
			return errno(err)
		}
		names[e.Inode] = []byte(e.Name)
		inodes = append(inodes, strconv.FormatUint(uint64(e.Inode), 10))
	}
	rows.Close()
	if len(inodes) == 0 {
		return 0
	}

	var n node
	// FIXME
	nodes, err := m.engine.Where("inode IN (?)", strings.Join(inodes, ",")).Rows(&n)
	if err != nil {
		return errno(err)
	}
	defer nodes.Close()
	for nodes.Next() {
		err = nodes.Scan(&n)
		if err != nil {
			return errno(err)
		}
		attr := new(Attr)
		m.parseAttr(&n, attr)
		*entries = append(*entries, &Entry{
			Inode: Ino(n.Inode),
			Name:  names[n.Inode],
			Attr:  attr,
		})
	}
	return 0
}

func (m *dbMeta) Open(ctx Context, inode Ino, flags uint8, attr *Attr) syscall.Errno {
	var err syscall.Errno
	if attr != nil {
		err = m.GetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.Lock()
		m.openFiles[inode] = m.openFiles[inode] + 1
		m.Unlock()
	}
	return 0
}

func (m *dbMeta) Close(ctx Context, inode Ino) syscall.Errno {
	m.Lock()
	defer m.Unlock()
	refs := m.openFiles[inode]
	if refs <= 1 {
		delete(m.openFiles, inode)
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
			go func() {
				// if err := m.deleteInode(inode); err == nil {
				// 	m.rdb.SRem(ctx, m.sustained(m.sid), strconv.Itoa(int(inode)))
				// }
			}()
		}
	} else {
		m.openFiles[inode] = refs - 1
	}
	return 0
}

func (m *dbMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	var c chunk
	_, err := m.engine.Where("inode=? and indx=?", inode, indx).Get(&c)
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(c.Slices)
	*chunks = buildSlice(ss)
	if len(c.Slices)/sliceBytes >= 5 || len(*chunks) >= 5 {
		go m.compactChunk(inode, indx)
	}
	return 0
}

func (m *dbMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	v, err := m.incrCounter("nextchunk")
	if err == nil {
		*chunkid = v
	}
	return errno(err)
}

func (m *dbMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var n node
		ok, err := s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type != TypeFile {
			return syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		var added int64
		if newleng > n.Length {
			added = align4K(newleng) - align4K(n.Length)
			n.Length = newleng
		}
		now := time.Now()
		n.Mtime = now

		var ck chunk
		ok, err = s.Where("Inode = ? and Indx = ?", inode, indx).Get(&ck)
		if err != nil {
			return err
		}
		// TODO: use concat
		ck.Slices = append(ck.Slices, marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len)...)
		if ok {
			if _, err := s.Where("Inode = ? and indx = ?", inode, indx).Update(&ck); err != nil {
				return err
			}
		} else {
			ck.Inode = inode
			ck.Indx = indx
			if _, err := s.Insert(&ck); err != nil {
				return err
			}
		}
		if _, err := s.Update(&n, &node{Inode: inode}); err != nil {
			return err
		}
		// most of chunk are used by single inode, so use that as the default (1 == not exists)
		if added > 0 {
			s.Exec("update counter set value=value+1 where key='usedSpace'")
		}
		if (len(ck.Slices)/sliceBytes)%20 == 19 {
			go m.compactChunk(inode, indx)
		}
		return nil
	})
}

func readSliceBuf(buf []byte) []*slice {
	var ss []*slice
	for i := 0; i < len(buf); i += sliceBytes {
		s := new(slice)
		s.read(buf[i:])
		ss = append(ss, s)
	}
	return ss
}

func (m *dbMeta) compactChunk(inode Ino, indx uint32) {
	// avoid too many or duplicated compaction
	m.Lock()
	k := uint64(inode) + (uint64(indx) << 32)
	if len(m.compacting) > 10 || m.compacting[k] {
		m.Unlock()
		return
	}
	m.compacting[k] = true
	m.Unlock()
	defer func() {
		m.Lock()
		delete(m.compacting, k)
		m.Unlock()
	}()

	var c chunk
	_, err := m.engine.Where("inode=? and indx=?", inode, indx).Get(&c)
	if err != nil {
		return
	}
	chunkid, err := m.incrCounter("nextchunk")
	if err != nil {
		return
	}

	var ss []*slice
	var chunks []Slice
	var skipped int
	var pos, size uint32
	for skipped < len(c.Slices) {
		// the slices will be formed as a tree after buildSlice(),
		// we should create new one (or remove the link in tree)
		ss = readSliceBuf(c.Slices[skipped:])
		// copy the first slice so it will not be updated by buildSlice
		first := *ss[0]
		chunks = buildSlice(ss)
		pos, size = 0, 0
		if chunks[0].Chunkid == 0 {
			pos = chunks[0].Len
			chunks = chunks[1:]
		}
		for _, s := range chunks {
			size += s.Len
		}
		if first.len < (1<<20) || first.len*5 < size {
			// it's too small
			break
		}
		isFirst := func(pos uint32, s Slice) bool {
			return pos == first.pos && s.Chunkid == first.chunkid && s.Off == first.off && s.Len == first.len
		}
		if !isFirst(pos, chunks[0]) {
			// it's not the first slice, compact it
			break
		}
		skipped += sliceBytes
	}
	if len(ss) < 2 {
		return
	}

	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped/sliceBytes, pos, len(ss), size)
	err = m.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		return
	}
	errno := m.txn(func(s *xorm.Session) error {
		var c2 chunk
		_, err := s.Where("inode=? and indx=?", inode, indx).Get(&c2)
		if err != nil {
			return err
		}
		if bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped], marshalSlice(pos, chunkid, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := s.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// pipe.HIncrBy(ctx, sliceRefs, m.sliceKey(chunkid, size), 1) // create the key to tracking it
		// for _, s := range ss {
		// 	rs = append(rs, pipe.Decr(ctx, m.sliceKey(s.chunkid, s.size)))
		// }
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if errno != 0 && errno != syscall.EINVAL {
		// if e := m.rdb.HGet(ctx, sliceRefs, m.sliceKey(chunkid, size)).Err(); e == redis.Nil {
		// 	errno = syscall.EINVAL // failed
		// } else if e == nil {
		// 	errno = 0 // successful
		// }
	}

	if errno == syscall.EINVAL {
		// m.rdb.HIncrBy(ctx, sliceRefs, m.sliceKey(chunkid, size), -2)
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		// m.deleteSlice(ctx, chunkid, size)
	} else if errno == 0 {
		// reset it to zero
		// m.rdb.HIncrBy(ctx, sliceRefs, m.sliceKey(chunkid, size), -1)
		// m.cleanupZeroRef(m.sliceKey(chunkid, size))
		// for i, s := range ss {
		// 	if rs[i].Err() == nil && rs[i].Val() < 0 {
		// 		// m.deleteSlice(ctx, s.chunkid, s.size)
		// 	}
		// }
		// if m.rdb.LLen(ctx, m.chunkKey(inode, indx)).Val() > 5 {
		// 	go func() {
		// 		// wait for the current compaction to finish
		// 		time.Sleep(time.Millisecond * 10)
		// 		m.compactChunk(inode, indx)
		// 	}()
		// }
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, errno)
	}
}

func (m *dbMeta) CompactAll(ctx Context) syscall.Errno {
	var c chunk
	rows, err := m.engine.Rows(&c)
	if err != nil {
		return errno(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		err = rows.Scan(&c)
		if err != nil {
			return errno(err)
		}
		if len(c.Slices)/sliceBytes >= 2 {
			logger.Debugf("compact chunk %d:%d (%d slices)", c.Inode, c.Indx, len(c.Slices)/sliceBytes)
			m.compactChunk(c.Inode, c.Indx)
		}
	}
	return 0
}

func (m *dbMeta) ListSlices(ctx Context, slices *[]Slice) syscall.Errno {
	// r.cleanupOldSliceRefs()
	var c chunk
	rows, err := m.engine.Rows(&c)
	if err != nil {
		return errno(err)
	}
	defer func() { _ = rows.Close() }()

	*slices = nil
	for rows.Next() {
		err = rows.Scan(&c)
		if err != nil {
			return errno(err)
		}
		ss := readSliceBuf(c.Slices)
		for _, s := range ss {
			if s.chunkid > 0 {
				*slices = append(*slices, Slice{Chunkid: s.chunkid, Size: s.size})
			}
		}
	}
	return 0
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	var x xattr
	ok, err := m.engine.Where("Inode = ? AND name = ?", inode, name).Get(&x)
	if err != nil {
		return errno(err)
	}
	if !ok {
		return ENOATTR
	}
	*vbuff = x.Value
	return 0
}

func (m *dbMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	var x xattr
	rows, err := m.engine.Where("inode = ?", inode).Rows(&x)
	if err != nil {
		return errno(err)
	}
	*names = nil
	for rows.Next() {
		err = rows.Scan(&x)
		if err != nil {
			return errno(err)
		}
		*names = append(*names, []byte(x.Name)...)
		*names = append(*names, 0)
	}
	return 0
}

func (m *dbMeta) SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	return m.txn(func(s *xorm.Session) error {
		var x = xattr{inode, name, value}
		n, err := m.engine.InsertOne(&x)
		if err != nil || n == 0 {
			_, err = m.engine.Update(&x, &xattr{inode, name, nil})
		}
		return err
	})
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	return m.txn(func(s *xorm.Session) error {
		n, err := m.engine.Delete(&xattr{Inode: inode, Name: name})
		if n == 0 {
			err = ENOATTR
		}
		return err
	})
}

func (m *dbMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	return syscall.ENOSYS
}
