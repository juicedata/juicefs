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

// DBConfig is config for Redis client.
type DBConfig struct {
	Strict      bool // update ctime
	Retries     int
	CaseInsensi bool
}

type setting struct {
	Name  string
	Value string `xorm:"varchar(4096)"`
}

type counter struct {
	Name  string
	Value uint64
}

type edge struct {
	Parent uint64
	Name   string
	Inode  uint64
	Type   uint8
}

type node struct {
	Inode  uint64
	Type   uint8
	Flags  uint8
	Mode   uint16
	Uid    uint32
	Gid    uint32
	Atime  time.Time
	Mtime  time.Time
	Ctime  time.Time
	Nlink  uint32
	Length uint64
	Rdev   uint32
	Parent uint64
}

type chunk struct {
	Inode   uint64
	Indx    uint32
	Pos     uint32
	Chunkid uint64
	Size    uint32
	Off     uint32
	Len     uint32
}

type symlink struct {
	Inode  uint64
	Target string `xorm:"varchar(4096)"`
}

type session struct {
	sid       uint64
	heartbeta time.Time
}

type delchunk struct {
	inode  uint64
	start  uint64
	end    uint64
	maxid  uint64
	expire time.Time
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
	v, err := m.incr("nextsession")
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

func (m *dbMeta) incr(name string) (uint64, error) {
	r, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var c counter
		_, err := s.Where("name = ?", name).Get(&c)
		if err != nil {
			return nil, err
		}
		c.Value++
		_, err = s.Cols("value").Update(&c)
		if err != nil {
			return nil, err
		}
		return c.Value - 1, nil
	})
	return r.(uint64), err
}

func (m *dbMeta) nextInode() (Ino, error) {
	v, err := m.incr("nextinode")
	return Ino(v), err
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
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var cur node
		ok, err := s.Where("Inode = ?", inode).Get(&cur)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
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
			return nil, nil
		}
		cur.Ctime = now
		_, err = s.Where("inode = ?", inode).Update(&cur)
		return &cur, err
	})
	return errno(err)
}

func (m *dbMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	n, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var n node
		ok, err := s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		n.Length = length
		_, err = s.Cols("length").Update(&n)
		if err != nil {
			return nil, err
		}
		return &n, nil
	})
	if err == nil && attr != nil {
		m.parseAttr(n.(*node), attr)
	}
	return errno(err)
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
	n.Inode = uint64(ino)
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
	n.Parent = uint64(parent)
	if inode != nil {
		*inode = ino
	}

	_, err = m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return nil, err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return nil, syscall.EEXIST
		}

		now := time.Now()
		if _type == TypeDirectory {
			pn.Nlink++
		}
		pn.Mtime = now
		pn.Ctime = now
		n.Atime = now
		n.Mtime = now
		n.Ctime = now
		if ctx.Value(CtxKey("behavior")) == "Hadoop" {
			n.Gid = pn.Gid
		}

		e.Inode = uint64(ino)
		e.Parent = uint64(parent)
		e.Name = name
		e.Type = _type
		_, err = s.Insert(&e)
		if err != nil {
			return nil, err
		}
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return nil, err
		}
		if _, err := s.Insert(&n); err != nil {
			return nil, err
		}
		if _type == TypeSymlink {
			if _, err := s.Insert(&symlink{Inode: uint64(ino), Target: path}); err != nil {
				return nil, err
			}
		} else if _type == TypeFile {
			s.Exec("update counter set value=value+? where key='usedSpace'", align4K(0))
		}
		s.Exec("update counter set value=value+1 where key='totalInodes'")
		return nil, err
	})
	if err != nil {
		m.parseAttr(&n, attr)
	}
	return errno(err)
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
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return nil, err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parent, name) == nil) {
			return nil, syscall.ENOENT
		}
		if e.Type == TypeDirectory {
			return nil, syscall.EPERM
		}

		var n node
		ok, err = s.Where("Inode = ?", e.Inode).Get(&n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}

		now := time.Now()
		pn.Mtime = now
		pn.Ctime = time.Now()
		n.Ctime = now
		n.Nlink--
		var opened bool
		if e.Type == TypeFile && n.Nlink == 0 {
			m.Lock()
			opened = m.openFiles[Ino(e.Inode)] > 0
			m.Unlock()
		}

		if _, err := s.Delete(&edge{Parent: uint64(parent), Name: name}); err != nil {
			return nil, err
		}
		// s.Delete(&xattr{Inode: e.Inode})
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return nil, err
		}
		s.Exec("update counter set value=value-1 where key='totalInodes'")

		if n.Nlink > 0 {
			if _, err := s.Update(&n, &node{Inode: e.Inode}); err != nil {
				return nil, err
			}
		} else {
			switch e.Type {
			case TypeSymlink:
				if _, err := s.Delete(&symlink{Inode: e.Inode}); err != nil {
					return nil, err
				}
				if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
					return nil, err
				}
			case TypeFile:
				if opened {
					if _, err := s.Update(&n, &node{Inode: e.Inode}); err != nil {
						return nil, err
					}
					// pipe.SAdd(ctx, r.sustained(r.sid), strconv.Itoa(int(inode)))
				} else {
					// pipe.ZAdd(ctx, delfiles, &redis.Z{Score: float64(now.Unix()), Member: r.toDelete(inode, attr.Length)})
					if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
						return nil, err
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
		return nil, err
	})
	return errno(err)
}

func (m *dbMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}

	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return nil, err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parent, name) == nil) {
			return nil, syscall.ENOENT
		}
		if e.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		cnt, err := s.Where("parent = ?", e.Inode).Count(&edge{})
		if err != nil {
			return nil, err
		}
		if cnt != 0 {
			return nil, syscall.ENOTEMPTY
		}

		now := time.Now()
		pn.Nlink--
		pn.Mtime = now
		pn.Ctime = now
		if _, err := s.Delete(&edge{Parent: uint64(parent), Name: name}); err != nil {
			return nil, err
		}
		if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
			return nil, err
		}
		// s.Delete(&xattr{Inode: e.Inode})
		if _, err := s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return nil, err
		}
		s.Exec("update counter set value=value-1 where key='totalInodes'")
		return nil, err
	})
	return errno(err)
}

func (m *dbMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var spn node
		ok, err := s.Where("Inode = ?", parentSrc).Get(&spn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if spn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var se edge
		ok, err = s.Where("parent=? and name=?", parentSrc, nameSrc).Get(&se)
		if err != nil {
			return nil, err
		}
		if !ok && (!m.conf.CaseInsensi || m.resolveCase(ctx, parentSrc, nameSrc) == nil) {
			return nil, syscall.ENOENT
		}

		if parentSrc == parentDst && nameSrc == nameDst {
			if inode != nil {
				*inode = Ino(se.Inode)
			}
			return nil, nil
		}

		var sn node
		ok, err = s.Where("Inode = ?", se.Inode).Get(&sn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}

		var dpn node
		ok, err = s.Where("Inode = ?", parentDst).Get(&dpn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if dpn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var de edge
		ok, err = s.Where("parent=? and name=?", parentDst, nameDst).Get(&de)
		if err != nil {
			return nil, err
		}
		var opened bool
		var dino Ino
		var dn node
		if ok {
			dino = Ino(de.Inode)
			if ctx.Value(CtxKey("behavior")) == "Hadoop" {
				return nil, syscall.EEXIST
			}
			if de.Type == TypeDirectory {
				cnt, err := s.Where("parent = ?", de.Inode).Count(&edge{})
				if err != nil {
					return nil, err
				}
				if cnt != 0 {
					return nil, syscall.ENOTEMPTY
				}
			} else {
				ok, err := s.Where("Inode = ?", de.Inode).Get(&dn)
				if err != nil {
					return nil, errno(err)
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
		spn.Ctime = now
		dpn.Mtime = now
		dpn.Ctime = now
		sn.Parent = uint64(parentDst)
		sn.Ctime = now
		if sn.Type == TypeDirectory && parentSrc != parentDst {
			spn.Nlink--
			dpn.Nlink++
		}
		if attr != nil {
			m.parseAttr(&sn, attr)
		}

		s.Delete(&edge{Parent: uint64(parentSrc), Name: nameSrc})
		s.Update(&spn, &node{Inode: uint64(parentSrc)})
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
			s.Delete(&edge{Parent: uint64(parentDst), Name: nameDst})
		}
		s.Insert(&edge{uint64(parentDst), nameDst, sn.Inode, sn.Type})
		if parentDst != parentSrc {
			s.Update(&dpn, &node{Inode: uint64(parentDst)})
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
		return nil, err
	})
	return errno(err)
}

func (m *dbMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var pn node
		ok, err := s.Where("Inode = ?", parent).Get(&pn)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return nil, syscall.ENOTDIR
		}
		var e edge
		ok, err = s.Where("parent=? and name=?", parent, name).Get(&e)
		if err != nil {
			return nil, err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return nil, syscall.EEXIST
		}

		var n node
		ok, err = s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if n.Type == TypeDirectory {
			return nil, syscall.EPERM
		}

		now := time.Now()
		pn.Mtime = now
		pn.Ctime = now
		n.Nlink++
		n.Ctime = now

		if ok, err := s.Insert(&edge{Parent: uint64(parent), Name: name, Inode: uint64(inode), Type: n.Type}); err != nil || ok == 0 {
			return nil, err
		}
		if _, err := s.Update(&pn, &node{Inode: uint64(parent)}); err != nil {
			return nil, err
		}
		if _, err := s.Cols("ctime", "nlink").Update(&n, node{Inode: uint64(inode)}); err != nil {
			return nil, err
		}
		if err == nil && attr != nil {
			m.parseAttr(&n, attr)
		}
		return nil, err
	})
	return errno(err)
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
	names := make(map[uint64][]byte)
	var inodes []string
	for rows.Next() {
		err = rows.Scan(e)
		if err != nil {
			return errno(err)
		}
		names[e.Inode] = []byte(e.Name)
		inodes = append(inodes, strconv.FormatUint(e.Inode, 10))
	}
	rows.Close()
	if len(inodes) == 0 {
		return 0
	}

	var n node
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
	rows, err := m.engine.Where("inode=? and indx=?", inode, indx).Rows(&c)
	if err != nil {
		return errno(err)
	}
	defer rows.Close()
	var vals []*slice
	for rows.Next() {
		err := rows.Scan(&c)
		if err != nil {
			return errno(err)
		}
		vals = append(vals, &slice{c.Chunkid, c.Size, c.Off, c.Size, c.Pos, nil, nil})
	}
	*chunks = buildSlice(vals)
	if len(vals) >= 5 || len(*chunks) >= 5 {
		// go r.compactChunk(inode, indx)
	}
	return 0
}

func (m *dbMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	v, err := m.incr("nextchunk")
	if err == nil {
		*chunkid = v
	}
	return errno(err)
}

func (m *dbMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	_, err := m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		var n node
		ok, err := s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, syscall.ENOENT
		}
		if n.Type != TypeFile {
			return nil, syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		var added int64
		if newleng > n.Length {
			added = align4K(newleng) - align4K(n.Length)
			n.Length = newleng
		}
		now := time.Now()
		n.Mtime = now
		n.Ctime = now

		if _, err := s.Insert(&chunk{uint64(inode), indx, off, slice.Chunkid, slice.Size, slice.Off, slice.Len}); err != nil {
			return nil, err
		}
		if _, err := s.Update(&n, &node{Inode: uint64(inode)}); err != nil {
			return nil, err
		}
		// most of chunk are used by single inode, so use that as the default (1 == not exists)
		if added > 0 {
			s.Exec("update counter set value=value+1 where key='usedSpace'")
		}
		if n, _ := s.Count(&chunk{Inode: uint64(inode), Indx: indx}); n%20 == 19 {
			// 	go r.compactChunk(inode, indx)
		}
		return nil, nil
	})
	return errno(err)
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	return syscall.ENOSYS
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
