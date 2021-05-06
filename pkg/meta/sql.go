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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
	"xorm.io/xorm"
)

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

type xattr struct {
	Inode Ino    `xorm:"unique(name)"`
	Name  string `xorm:"unique(name)"`
	Value []byte `xorm:"VARBINARY"`
}

type session struct {
	Sid       uint64 `xorm:"pk"`
	Heartbeat time.Time
}

type sustained struct {
	Sid   uint64 `xorm:"unique(sustained)"`
	Inode Ino    `xorm:"unique(sustained)"`
}

type delfile struct {
	Inode  Ino    `xorm:"unique(delfile)"`
	Length uint64 `xorm:"unique(delfile)"`
	Expire time.Time
}

type sliceRef struct {
	Chunkid uint64
	Size    uint32
	Refs    int
}

type freeID struct {
	next  uint64
	maxid uint64
}
type dbMeta struct {
	sync.Mutex
	conf   *Config
	engine *xorm.Engine

	sid          uint64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID
}

func newSQLMeta(driver, dsn string, conf *Config) (*dbMeta, error) {
	engine, err := xorm.NewEngine(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to use data source %s: %s", driver, err)
	}
	if err = engine.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %s", err)
	}
	engine.SetLogLevel(0)

	if conf.Retries == 0 {
		conf.Retries = 30
	}
	m := &dbMeta{
		conf:         conf,
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
	_ = m.engine.Sync2(new(sliceRef))
	_ = m.engine.Sync2(new(xattr))
	_ = m.engine.Sync2(new(session))
	_ = m.engine.Sync2(new(sustained))
	_ = m.engine.Sync2(new(delfile))

	old, err := m.Load()
	if err != nil {
		return err
	}
	if old != nil {
		if force {
			old.SecretKey = "removed"
			logger.Warnf("Existing volume will be overwrited: %+v", old)
		} else {
			// only AccessKey and SecretKey can be safely updated.
			format.UUID = old.UUID
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
			if format != *old {
				old.SecretKey = ""
				format.SecretKey = ""
				return fmt.Errorf("cannot update format from %+v to %+v", old, format)
			}
		}
		data, err := json.MarshalIndent(format, "", "")
		if err != nil {
			logger.Fatalf("json: %s", err)
		}
		_, err = m.engine.Update(&setting{"format", string(data)}, &setting{Name: "format"})
		return err
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}

	return m.txn(func(s *xorm.Session) error {
		_, err = s.Insert(&setting{"format", string(data)})
		if err != nil {
			return err
		}

		now := time.Now()
		_, err = s.Insert(&node{
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
		_, err = s.Insert(
			counter{"nextInode", 2}, // 1 is root
			counter{"nextChunk", 1},
			counter{"nextSession", 1},
			counter{"usedSpace", 0},
			counter{"totalInodes", 0},
			counter{"nextCleanupSlices", 0},
		)
		return err
	})
}

func (m *dbMeta) Load() (*Format, error) {
	var s setting
	ok, err := m.engine.Where("name = ?", "format").Get(&s)
	if err != nil || !ok {
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
	v, err := m.incrCounter("nextSession", 1)
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	_, err = m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
		return s.Insert(&session{v, time.Now()})
	})
	if err != nil {
		return fmt.Errorf("insert new session: %s", err)
	}
	m.sid = v
	logger.Debugf("session is %d", m.sid)

	go m.refreshSession()
	go m.cleanupDeletedFiles()
	go m.cleanupSlices()
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

func (m *dbMeta) incrCounter(name string, batch uint64) (uint64, error) {
	var v uint64
	errno := m.txn(func(s *xorm.Session) error {
		var c counter
		_, err := s.Where("name = ?", name).Get(&c)
		if err != nil {
			return err
		}
		v = c.Value
		_, err = s.Cols("value").Update(&counter{Value: c.Value + batch}, &counter{Name: name})
		return err
	})
	if errno == 0 {
		return v, nil
	}
	return 0, errno
}

func (m *dbMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next < m.freeInodes.maxid {
		v := m.freeInodes.next
		m.freeInodes.next++
		return Ino(v), nil
	}
	v, err := m.incrCounter("nextInode", 100)
	if err == nil {
		m.freeInodes.next = v + 1
		m.freeInodes.maxid = v + 1000
	}
	return Ino(v), err
}

func (m *dbMeta) txn(f func(s *xorm.Session) error) syscall.Errno {
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	var err error
	for i := 0; i < 50; i++ {
		_, err = m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
			return nil, f(s)
		})
		// TODO: add other retryable errors here
		if errors.Is(err, sqlite3.ErrBusy) || err != nil && strings.Contains(err.Error(), "database is locked") {
			txRestart.Add(1)
			logger.Debug("conflicted transaction, restart it")
			time.Sleep(time.Millisecond * time.Duration(i) * time.Duration(i))
			continue
		}
		break
	}
	return errno(err)
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

func (m *dbMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	var c counter
	_, err := m.engine.Where("name='usedSpace'").Get(&c)
	if err != nil {
		logger.Warnf("get used space: %s", err)
	}
	*totalspace = 1 << 50
	for *totalspace < c.Value {
		*totalspace *= 2
	}
	*availspace = *totalspace - c.Value
	_, err = m.engine.Where("name='totalInodes'").Get(&c)
	if err != nil {
		logger.Warnf("get total inodes: %s", err)
	}
	*iused = c.Value
	*iavail = 10 << 20
	return 0
}

func (m *dbMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	var e edge
	ok, err := m.engine.Where("Parent = ? and Name = ?", parent, name).Get(&e)
	if err != nil {
		return errno(err)
	}
	if !ok {
		if m.conf.CaseInsensi {
			// TODO: in SQL
			if e := m.resolveCase(ctx, parent, name); e != nil {
				*inode = e.Inode
				*attr = *e.Attr
				return 0
			}
		}
		return syscall.ENOENT
	}
	if attr == nil {
		*inode = Ino(e.Inode)
		return 0
	}
	var n node
	ok, err = m.engine.Where("inode = ?", e.Inode).Get(&n)
	if err != nil {
		return errno(err)
	}
	if !ok {
		return syscall.ENOENT
	}
	*inode = Ino(e.Inode)
	m.parseAttr(&n, attr)
	return 0
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
		_, err = s.Update(&cur, &node{Inode: inode})
		if err == nil {
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
		if n.Type != TypeFile {
			return syscall.EPERM
		}
		if length == n.Length {
			m.parseAttr(&n, attr)
			return nil
		}

		if length < n.Length {
			var c chunk
			indx := uint32(length / ChunkSize)
			if uint32(n.Length/ChunkSize) == indx {
				_, err = s.Exec("update chunk set slices=slices || ? where inode=? AND indx=?",
					marshalSlice(uint32(length%ChunkSize), 0, 0, 0, uint32(n.Length-length)), inode, indx)
			} else {
				_, err = s.Exec("update chunk set slices=slices || ? where inode=? AND indx=?",
					marshalSlice(uint32(length%ChunkSize), 0, 0, 0, ChunkSize-uint32(length%ChunkSize)), inode, indx)
			}
			if err != nil {
				return err
			}
			var indexes []uint32
			rows, err := s.Where("inode = ? AND indx > ?", inode, indx).Cols("indx").Rows(&c)
			if err != nil {
				return err
			}
			for rows.Next() {
				err = rows.Scan(&c)
				if err != nil {
					rows.Close()
					return err
				}
				indexes = append(indexes, c.Indx)
			}
			rows.Close()
			for _, indx := range indexes {
				_, err = s.Exec("update chunk set slices=slices || ? where inode=? AND indx=?",
					marshalSlice(0, 0, 0, 0, ChunkSize), inode, indx)
				if err != nil {
					return err
				}
			}
		}
		added := align4K(length) - align4K(n.Length)
		n.Length = length
		_, err = s.Cols("length").Update(&n, &node{Inode: n.Inode})
		if err != nil {
			return err
		}
		_, err = s.Exec("update counter set value=value+? where name='usedSpace'", added)
		if err == nil {
			m.parseAttr(&n, attr)
		}
		return nil
	})
}

func (r *dbMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
	if mode&fallocCollapesRange != 0 && mode != fallocCollapesRange {
		return syscall.EINVAL
	}
	if mode&fallocInsertRange != 0 && mode != fallocInsertRange {
		return syscall.EINVAL
	}
	if mode == fallocInsertRange || mode == fallocCollapesRange {
		return syscall.ENOTSUP
	}
	if mode&fallocPunchHole != 0 && mode&fallocKeepSize == 0 {
		return syscall.EINVAL
	}
	if size == 0 {
		return syscall.EINVAL
	}
	return r.txn(func(s *xorm.Session) error {
		var n node
		ok, err := s.Where("Inode = ?", inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type == TypeFIFO {
			return syscall.EPIPE
		}
		if n.Type != TypeFile {
			return syscall.EPERM
		}
		length := n.Length
		if off+size > n.Length {
			if mode&fallocKeepSize == 0 {
				length = off + size
			}
		}

		old := n.Length
		added := align4K(length) - align4K(n.Length)
		n.Length = length
		if _, err := s.Update(&n, &node{Inode: inode}); err != nil {
			return err
		}
		if mode&(fallocZeroRange|fallocPunchHole) != 0 {
			if off+size > old {
				size = old - off
			}
			for size > 0 {
				indx := uint32(off / ChunkSize)
				coff := off % ChunkSize
				l := size
				if coff+size > ChunkSize {
					l = ChunkSize - coff
				}
				if _, err = s.Exec("update chunk set slices=slices || ? where inode=? AND indx=?", marshalSlice(uint32(coff), 0, 0, 0, uint32(l)), inode, indx); err != nil {
					return err
				}
				off += l
				size -= l
			}
		}
		_, err = s.Exec("update counter set value=value+? where name='usedSpace'", added)
		return err
	})
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

		if _, err = s.Insert(&edge{parent, name, ino, _type}); err != nil {
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
			if _, err := s.Exec("update counter set value=value+? where name='usedSpace'", align4K(0)); err != nil {
				return err
			}
		}
		_, err = s.Exec("update counter set value=value+1 where name='totalInodes'")
		if err == nil {
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
		if !ok && m.conf.CaseInsensi {
			if ee := m.resolveCase(ctx, parent, name); ee != nil {
				ok = true
				e.Inode = ee.Inode
				e.Type = ee.Attr.Typ
			}
		}
		if !ok {
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
		if _, err = s.Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		if _, err = s.Exec("update counter set value=value-1 where name='totalInodes'"); err != nil {
			return err
		}

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
					if _, err := s.Insert(sustained{m.sid, e.Inode}); err != nil {
						return err
					}
					if _, err := s.Update(&n, &node{Inode: e.Inode}); err != nil {
						return err
					}
				} else {
					if _, err := s.Insert(delfile{e.Inode, n.Length, time.Now()}); err != nil {
						return err
					}
					if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
						return err
					}
					if _, err := s.Exec("update counter set value=value-? where name='usedSpace'", align4K(n.Length)); err != nil {
						return err
					}
				}
			}
		}
		if err == nil && e.Type == TypeFile && n.Nlink == 0 {
			if opened {
				m.Lock()
				m.removedFiles[Ino(e.Inode)] = true
				m.Unlock()
			} else {
				go m.deleteFile(e.Inode, n.Length)
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
		if !ok && m.conf.CaseInsensi {
			if ee := m.resolveCase(ctx, parent, name); ee != nil {
				ok = true
				e.Inode = ee.Inode
				e.Type = ee.Attr.Typ
			}
		}
		if !ok {
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
		if _, err := s.Exec("update counter set value=value-1 where name='totalInodes'"); err != nil {
			return err
		}
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
		if !ok && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentSrc, nameSrc); e != nil {
				ok = true
				se.Inode = e.Inode
				se.Name = string(e.Name)
			}
		}
		if !ok {
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
		if !ok && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				ok = true
				de.Inode = e.Inode
				de.Type = e.Attr.Typ
				de.Name = string(e.Name)
			}
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
					return syscall.ENOENT
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
		m.parseAttr(&sn, attr)

		if n, err := s.Delete(&edge{Parent: parentSrc, Name: se.Name}); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("delete src failed")
		}
		if _, err := s.Update(&spn, &node{Inode: parentSrc}); err != nil {
			return err
		}
		if dino > 0 {
			if dn.Type != TypeDirectory && dn.Nlink > 0 {
				if _, err := s.Update(dn, &node{Inode: dn.Inode}); err != nil {
					return err
				}
			} else {
				if dn.Type == TypeDirectory {
					if _, err := s.Delete(&node{Inode: dn.Inode}); err != nil {
						return err
					}
					dn.Nlink--
				} else if dn.Type == TypeSymlink {
					if _, err := s.Delete(&symlink{Inode: dn.Inode}); err != nil {
						return err
					}
					if _, err := s.Delete(&node{Inode: dn.Inode}); err != nil {
						return err
					}
				} else if dn.Type == TypeFile {
					if opened {
						if _, err := s.Update(&dn, &node{Inode: dn.Inode}); err != nil {
							return err
						}
						if _, err := s.Insert(sustained{m.sid, dn.Inode}); err != nil {
							return err
						}
					} else {
						if _, err := s.Insert(delfile{dn.Inode, dn.Length, time.Now()}); err != nil {
							return err
						}
						if _, err := s.Delete(&node{Inode: dn.Inode}); err != nil {
							return err
						}
						if _, err := s.Exec("update counter set value=value-? where name='usedSpace'", align4K(dn.Length)); err != nil {
							return err
						}
					}
				}
				if _, err := s.Exec("update counter set value=value-1 where name='totalInodes'"); err != nil {
					return err
				}
				if _, err := s.Delete(xattr{Inode: dino}); err != nil {
					return err
				}
			}
			if _, err := s.Delete(&edge{Parent: parentDst, Name: de.Name}); err != nil {
				return err
			}
		}
		if n, err := s.Insert(&edge{parentDst, nameDst, sn.Inode, sn.Type}); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("insert edge(%d,%s) failed", parentDst, nameDst)
		}
		if parentDst != parentSrc {
			if _, err := s.Update(&dpn, &node{Inode: parentDst}); err != nil {
				return err
			}
		}
		if _, err := s.Update(&sn, &node{Inode: sn.Inode}); err != nil {
			return err
		}
		if err == nil && dino > 0 && dn.Type == TypeFile {
			if opened {
				m.Lock()
				m.removedFiles[dino] = true
				m.Unlock()
			} else {
				go m.deleteFile(dino, dn.Length)
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
		if _, err := s.Cols("nlink").Update(&n, node{Inode: inode}); err != nil {
			return err
		}
		if err == nil {
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
	rows, err := m.engine.Where("parent = ?", inode).Rows(e)
	if err != nil {
		return errno(err)
	}
	names := make(map[Ino][]byte)
	var inodes []string
	for rows.Next() {
		err = rows.Scan(e)
		if err != nil {
			_ = rows.Close()
			return errno(err)
		}
		names[e.Inode] = []byte(e.Name)
		inodes = append(inodes, strconv.FormatUint(uint64(e.Inode), 10))
	}
	_ = rows.Close()
	if len(inodes) == 0 {
		return 0
	}

	var n node
	nodes, err := m.engine.Where(fmt.Sprintf("inode IN (%s)", strings.Join(inodes, ","))).Rows(&n)
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

func (m *dbMeta) cleanStaleSession(sid uint64) {
	var s sustained
	rows, err := m.engine.Where("Sid = ?", sid).Rows(&s)
	if err != nil {
		logger.Warnf("scan stale session %d: %s", sid, err)
		return
	}

	var inodes []Ino
	for rows.Next() {
		if rows.Scan(&s) == nil {
			inodes = append(inodes, s.Inode)
		}
	}
	rows.Close()

	var done bool = true
	for _, inode := range inodes {
		if err := m.deleteInode(inode); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
			done = false
		} else {
			_ = m.txn(func(ses *xorm.Session) error {
				_, err = ses.Delete(&s)
				return err
			})
		}
	}

	if done {
		_ = m.txn(func(ses *xorm.Session) error {
			_, err = ses.Delete(&session{Sid: sid})
			logger.Infof("cleanup session %d: %s", sid, err)
			return err
		})
	}
}

func (r *dbMeta) cleanStaleSessions() {
	// TODO: once per minute
	var s session
	rows, err := r.engine.Where("Heartbeat > ?", time.Now().Add(time.Minute*-5)).Rows(&s)
	if err != nil {
		logger.Warnf("scan stale sessions: %s", err)
		return
	}
	var ids []uint64
	for rows.Next() {
		if rows.Scan(&s) == nil {
			ids = append(ids, s.Sid)
		}
	}
	rows.Close()
	for _, sid := range ids {
		r.cleanStaleSession(sid)
	}

	// rng = &redis.ZRangeBy{Max: strconv.Itoa(int(now.Add(time.Minute * -3).Unix())), Count: 100}
	// staleSessions, _ = r.rdb.ZRangeByScore(ctx, allSessions, rng).Result()
	// for _, sid := range staleSessions {
	// 	r.cleanStaleLocks(sid)
	// }
}

func (m *dbMeta) refreshSession() {
	for {
		time.Sleep(time.Minute)
		_ = m.txn(func(ses *xorm.Session) error {
			_, err := ses.Cols("Heartbeat").Update(&session{Heartbeat: time.Now()}, &session{Sid: m.sid})
			if err != nil {
				logger.Errorf("update session: %s", err)
			}
			return err
		})
		go m.cleanStaleSessions()
	}
}

func (r *dbMeta) deleteInode(inode Ino) error {
	var n node
	err := r.txn(func(s *xorm.Session) error {
		ok, err := s.Where("inode = ?", inode).Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if _, err := s.Insert(&delfile{inode, n.Length, time.Now()}); err != nil {
			return err
		}
		if _, err := s.Delete(&node{Inode: inode}); err != nil {
			return err
		}
		_, err = s.Exec("update counter set value=value-? where name='usedSpace'", align4K(n.Length))
		return err
	})
	if err == 0 {
		go r.deleteFile(inode, n.Length)
	}
	return err
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
				if err := m.deleteInode(inode); err == nil {
					_ = m.txn(func(ses *xorm.Session) error {
						_, err := ses.Delete(&sustained{m.sid, inode})
						return err
					})
				}
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
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeChunks.next < m.freeChunks.maxid {
		*chunkid = m.freeChunks.next
		m.freeChunks.next++
		return 0
	}
	v, err := m.incrCounter("nextChunk", 1000)
	if err == nil {
		*chunkid = v
		m.freeChunks.next = v + 1
		m.freeChunks.maxid = v + 1000
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
		buf := marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len)
		if ok {
			if _, err := s.Exec("update chunk set slices=slices || ? where inode=? AND indx=?", buf, inode, indx); err != nil {
				return err
			}
		} else {
			if _, err := s.Insert(&chunk{inode, indx, buf}); err != nil {
				return err
			}
		}
		if _, err := s.Insert(sliceRef{slice.Chunkid, slice.Size, 1}); err != nil {
			return err
		}
		if _, err := s.Update(&n, &node{Inode: inode}); err != nil {
			return err
		}
		if added > 0 {
			_, err = s.Exec("update counter set value=value+? where name='usedSpace'", added)
		}
		if (len(ck.Slices)/sliceBytes)%20 == 19 {
			go m.compactChunk(inode, indx)
		}
		return err
	})
}

func (m *dbMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	return m.txn(func(s *xorm.Session) error {
		var nin, nout node
		ok, err := s.Where("inode = ?", fin).Get(&nin)
		if err != nil {
			return err
		}
		ok2, err2 := s.Where("inode = ?", fout).Get(&nout)
		if err2 != nil {
			return err2
		}
		if !ok || !ok2 {
			return syscall.ENOENT
		}
		if nin.Type != TypeFile {
			return syscall.EINVAL
		}
		if offIn >= nin.Length {
			*copied = 0
			return nil
		}
		if offIn+size > nin.Length {
			size = nin.Length - offIn
		}
		if nout.Type != TypeFile {
			return syscall.EINVAL
		}

		newleng := offOut + size
		var added int64
		if newleng > nout.Length {
			added = align4K(newleng) - align4K(nout.Length)
			nout.Length = newleng
		}
		now := time.Now()
		nout.Mtime = now

		var c chunk
		rows, err := s.Where("inode = ? AND indx >= ? AND indx <= ?", fin, offIn/ChunkSize, (offIn+size)/ChunkSize).Rows(&c)
		if err != nil {
			return err
		}
		chunks := make(map[uint32][]*slice)
		for rows.Next() {
			err = rows.Scan(&c)
			if err != nil {
				rows.Close()
				return err
			}
			chunks[c.Indx] = readSliceBuf(c.Slices)
		}
		rows.Close()

		ses := s
		updateSlices := func(indx uint32, buf []byte, s *Slice) error {
			if r, err := ses.Exec("update chunk set slices=slices || ? where inode=? AND indx=?", buf, fout, indx); err != nil {
				return err
			} else if n, _ := r.RowsAffected(); n == 0 {
				n, err := ses.Insert(&chunk{fout, indx, buf})
				if err != nil {
					return err
				} else if n == 0 {
					return fmt.Errorf("insert slice failed")
				}
			}
			if s.Chunkid > 0 {
				if _, err := ses.Exec("update slice_ref set refs=refs+1 where chunkid = ? AND size = ?", s.Chunkid, s.Size); err != nil {
					return err
				}
			}
			return nil
		}
		coff := uint64(offIn/ChunkSize) * ChunkSize
		for coff < offIn+size {
			if coff%ChunkSize != 0 {
				panic("coff")
			}
			// Add a zero chunk for hole
			ss := append([]*slice{{len: ChunkSize}}, chunks[uint32(coff/ChunkSize)]...)
			cs := buildSlice(ss)
			for _, s := range cs {
				pos := coff
				coff += uint64(s.Len)
				if pos < offIn+size && pos+uint64(s.Len) > offIn {
					if pos < offIn {
						dec := offIn - pos
						s.Off += uint32(dec)
						pos += dec
						s.Len -= uint32(dec)
					}
					if pos+uint64(s.Len) > offIn+size {
						dec := pos + uint64(s.Len) - (offIn + size)
						s.Len -= uint32(dec)
					}
					doff := pos - offIn + offOut
					indx := uint32(doff / ChunkSize)
					dpos := uint32(doff % ChunkSize)
					if dpos+s.Len > ChunkSize {
						if err := updateSlices(indx, marshalSlice(dpos, s.Chunkid, s.Size, s.Off, ChunkSize-dpos), &s); err != nil {
							return err
						}
						skip := ChunkSize - dpos
						if err := updateSlices(indx+1, marshalSlice(0, s.Chunkid, s.Size, s.Off+skip, s.Len-skip), &s); err != nil {
							return err
						}
					} else {
						if err := updateSlices(indx, marshalSlice(dpos, s.Chunkid, s.Size, s.Off, s.Len), &s); err != nil {
							return err
						}
					}
				}
			}
		}
		if _, err := s.Update(&nout, &node{Inode: fout}); err != nil {
			return err
		}
		if added > 0 {
			_, err = s.Exec("update counter set value=value+? where name='usedSpace'", added)
		}
		if err == nil {
			*copied = size
		}
		return err
	})
}

func (m *dbMeta) cleanupDeletedFiles() {
	for {
		time.Sleep(time.Minute)
		var d delfile
		rows, err := m.engine.Where("expire < ?", time.Now().Add(-time.Hour)).Rows(&d)
		if err != nil {
			continue
		}
		var fs []delfile
		for rows.Next() {
			if rows.Scan(&d) == nil {
				fs = append(fs, d)
			}
		}
		rows.Close()
		for _, f := range fs {
			logger.Debugf("cleanup chunks of inode %d with %d bytes", f.Inode, f.Length)
			m.deleteFile(d.Inode, d.Length)
		}
	}
}

func (m *dbMeta) cleanupSlices() {
	for {
		time.Sleep(time.Hour)

		// once per hour
		var c counter
		_, err := m.engine.Where("name = 'nextCleanupSlices'").Get(&c)
		if err != nil {
			continue
		}
		now := time.Now().Unix()
		if c.Value+3600 > uint64(now) {
			continue
		}
		_ = m.txn(func(ses *xorm.Session) error {
			_, err := ses.Update(&counter{Value: uint64(now)}, counter{Name: "nextCleanupSlices"})
			return err
		})

		var ck sliceRef
		rows, err := m.engine.Where("refs <= 0").Rows(&ck)
		if err != nil {
			continue
		}
		var cks []sliceRef
		for rows.Next() {
			if rows.Scan(&ck) == nil {
				cks = append(cks, ck)
			}
		}
		rows.Close()
		for _, ck := range cks {
			if ck.Refs <= 0 {
				m.deleteSlice(ck.Chunkid, ck.Size)
			}
		}
	}
}

func (m *dbMeta) deleteSlice(chunkid uint64, size uint32) {
	m.deleting <- 1
	defer func() { <-m.deleting }()
	err := m.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
	} else {
		_ = m.txn(func(ses *xorm.Session) error {
			_, err = ses.Exec("delete from slice_ref where chunkid=?", chunkid)
			if err != nil {
				logger.Errorf("delete slice %d: %s", chunkid, err)
			}
			return err
		})
	}
}

func (m *dbMeta) deleteChunk(inode Ino, indx uint32) error {
	var c chunk
	var ss []*slice
	err := m.txn(func(ses *xorm.Session) error {
		ok, err := ses.Where("inode = ? AND indx = ?", inode, indx).Get(&c)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		ss = readSliceBuf(c.Slices)
		for _, s := range ss {
			_, err = ses.Exec("update slice_ref set refs=refs-1 where chunkid=? AND size=?", s.chunkid, s.size)
			if err != nil {
				return err
			}
		}
		_, err = ses.Delete(&chunk{inode, indx, nil})
		return err
	})
	if err != 0 {
		return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", inode, err)
	}
	for _, s := range ss {
		m.deleteSlice(s.chunkid, s.size)
	}
	return nil
}

func (m *dbMeta) deleteFile(inode Ino, length uint64) {
	var c chunk
	rows, err := m.engine.Where("inode = ?", inode).Rows(&c)
	if err != nil {
		return
	}
	var indexes []uint32
	for rows.Next() {
		if rows.Scan(&c) == nil {
			indexes = append(indexes, c.Indx)
		}
	}
	rows.Close()
	for _, indx := range indexes {
		err = m.deleteChunk(inode, indx)
		if err != nil {
			return
		}
	}
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

	var chunkid uint64
	st := m.NewChunk(Background, 0, 0, 0, &chunkid)
	if st != 0 {
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
		if len(c2.Slices) < len(c.Slices) || !bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped], marshalSlice(pos, chunkid, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := s.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if n, err := s.Insert(sliceRef{chunkid, size, 1}); err != nil || n == 0 {
			return err
		}
		ses := s
		for _, s := range ss {
			if _, err := ses.Exec("update slice_ref set refs=refs-1 where chunkid=? and size=?", s.chunkid, s.size); err != nil {
				return err
			}
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if errno != 0 {
		var c chunk
		ok, err := m.engine.Where("chunkid=? and size=?", chunkid, size).Get(&c)
		if err == nil {
			if ok {
				errno = 0
			} else {
				errno = syscall.EINVAL
			}
		}
	}

	if errno == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		m.deleteSlice(chunkid, size)
	} else if errno == 0 {
		for _, s := range ss {
			var ref sliceRef
			ok, err := m.engine.Where("chunkid = ?", s.chunkid).Get(&ref)
			if err == nil && ok && ref.Refs <= 0 {
				m.deleteSlice(s.chunkid, s.size)
			}
		}
		go func() {
			// wait for the current compaction to finish
			time.Sleep(time.Millisecond * 10)
			m.compactChunk(inode, indx)
		}()
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, errno)
	}
}

func (m *dbMeta) CompactAll(ctx Context) syscall.Errno {
	var c chunk
	rows, err := m.engine.Where("length(slices) >= ?", sliceBytes*2).Cols("inode", "indx").Rows(&c)
	if err != nil {
		return errno(err)
	}
	var cs []chunk
	for rows.Next() {
		if rows.Scan(&c) == nil {
			cs = append(cs, c)
		}
	}
	rows.Close()

	for _, c := range cs {
		logger.Debugf("compact chunk %d:%d (%d slices)", c.Inode, c.Indx, len(c.Slices)/sliceBytes)
		m.compactChunk(c.Inode, c.Indx)
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
	defer rows.Close()

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
	defer rows.Close()
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
		n, err := s.InsertOne(&x)
		if err != nil || n == 0 {
			_, err = s.Update(&x, &xattr{inode, name, nil})
		}
		return err
	})
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	return m.txn(func(s *xorm.Session) error {
		n, err := s.Delete(&xattr{Inode: inode, Name: name})
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
