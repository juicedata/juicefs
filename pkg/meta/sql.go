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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

type setting struct {
	Name  string `xorm:"pk"`
	Value string `xorm:"varchar(4096) notnull"`
}

type counter struct {
	Name  string `xorm:"pk"`
	Value int64  `xorm:"notnull"`
}

type edge struct {
	Parent Ino    `xorm:"unique(edge) notnull"`
	Name   string `xorm:"unique(edge) notnull"`
	Inode  Ino    `xorm:"notnull"`
	Type   uint8  `xorm:"notnull"`
}

type node struct {
	Inode  Ino    `xorm:"pk"`
	Type   uint8  `xorm:"notnull"`
	Flags  uint8  `xorm:"notnull"`
	Mode   uint16 `xorm:"notnull"`
	Uid    uint32 `xorm:"notnull"`
	Gid    uint32 `xorm:"notnull"`
	Atime  int64  `xorm:"notnull"`
	Mtime  int64  `xorm:"notnull"`
	Ctime  int64  `xorm:"notnull"`
	Nlink  uint32 `xorm:"notnull"`
	Length uint64 `xorm:"notnull"`
	Rdev   uint32
	Parent Ino
}

type namedNode struct {
	node `xorm:"extends"`
	Name string
}

type chunk struct {
	Inode  Ino    `xorm:"unique(chunk) notnull"`
	Indx   uint32 `xorm:"unique(chunk) notnull"`
	Slices []byte `xorm:"blob notnull"`
}
type chunkRef struct {
	Chunkid uint64 `xorm:"pk"`
	Size    uint32 `xorm:"notnull"`
	Refs    int    `xorm:"notnull"`
}
type symlink struct {
	Inode  Ino    `xorm:"pk"`
	Target string `xorm:"varchar(4096) notnull"`
}

type xattr struct {
	Inode Ino    `xorm:"unique(name) notnull"`
	Name  string `xorm:"unique(name) notnull"`
	Value []byte `xorm:"blob notnull"`
}

type flock struct {
	Inode Ino    `xorm:"notnull unique(flock)"`
	Sid   uint64 `xorm:"notnull unique(flock)"`
	Owner uint64 `xorm:"notnull unique(flock)"`
	Ltype byte   `xorm:"notnull"`
}

type plock struct {
	Inode   Ino    `xorm:"notnull unique(plock)"`
	Sid     uint64 `xorm:"notnull unique(plock)"`
	Owner   uint64 `xorm:"notnull unique(plock)"`
	Records []byte `xorm:"blob notnull"`
}

type session struct {
	Sid       uint64 `xorm:"pk"`
	Heartbeat int64  `xorm:"notnull"`
	Info      []byte `xorm:"blob"`
}

type sustained struct {
	Sid   uint64 `xorm:"unique(sustained) notnull"`
	Inode Ino    `xorm:"unique(sustained) notnull"`
}

type delfile struct {
	Inode  Ino    `xorm:"pk notnull"`
	Length uint64 `xorm:"notnull"`
	Expire int64  `xorm:"notnull"`
}

type freeID struct {
	next  uint64
	maxid uint64
}
type dbMeta struct {
	sync.Mutex
	conf   *Config
	fmt    Format
	engine *xorm.Engine

	sid          uint64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	newSpace     int64
	newInodes    int64
	usedSpace    int64
	usedInodes   int64

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID
}

func newSQLMeta(driver, dsn string, conf *Config) (*dbMeta, error) {
	engine, err := xorm.NewEngine(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to use data source %s: %s", driver, err)
	}
	start := time.Now()
	if err = engine.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %s", err)
	}
	if time.Since(start) > time.Millisecond {
		logger.Warnf("The latency to database is too high: %s", time.Since(start))
	}

	engine.SetTableMapper(names.NewPrefixMapper(engine.GetTableMapper(), "jfs_"))
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
	if err := m.engine.Sync2(new(setting), new(counter)); err != nil {
		logger.Fatalf("create table setting, counter: %s", err)
	}
	if err := m.engine.Sync2(new(node), new(edge), new(symlink), new(xattr)); err != nil {
		logger.Fatalf("create table node, edge, symlink, xattr: %s", err)
	}
	if err := m.engine.Sync2(new(chunk), new(chunkRef)); err != nil {
		logger.Fatalf("create table chunk, chunk_ref: %s", err)
	}
	if err := m.engine.Sync2(new(session), new(sustained), new(delfile)); err != nil {
		logger.Fatalf("create table session, sustaind, delfile: %s", err)
	}
	if err := m.engine.Sync2(new(flock), new(plock)); err != nil {
		logger.Fatalf("create table flock, plock: %s", err)
	}

	old, err := m.Load()
	if err != nil {
		return err
	}
	if old != nil {
		if force {
			old.SecretKey = "removed"
			logger.Warnf("Existing volume will be overwrited: %+v", old)
		} else {
			format.UUID = old.UUID
			// these can be safely updated.
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
			old.Capacity = format.Capacity
			old.Inodes = format.Inodes
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

	m.fmt = format
	return m.txn(func(s *xorm.Session) error {
		var set = &setting{"format", string(data)}
		now := time.Now()
		var root = &node{
			Inode:  1,
			Type:   TypeDirectory,
			Mode:   0777,
			Atime:  now.UnixNano() / 1000,
			Mtime:  now.UnixNano() / 1000,
			Ctime:  now.UnixNano() / 1000,
			Nlink:  2,
			Length: 4 << 10,
			Parent: 1,
		}
		var cs = []counter{
			{"nextInode", 2}, // 1 is root
			{"nextChunk", 1},
			{"nextSession", 1},
			{"usedSpace", 0},
			{"totalInodes", 0},
			{"nextCleanupSlices", 0},
		}
		return mustInsert(s, set, root, &cs)
	})
}

func (m *dbMeta) Load() (*Format, error) {
	var s = setting{Name: "format"}
	ok, err := m.engine.Get(&s)
	if err != nil || !ok {
		return nil, err
	}

	err = json.Unmarshal([]byte(s.Value), &m.fmt)
	if err != nil {
		return nil, fmt.Errorf("json: %s", err)
	}
	return &m.fmt, nil
}

func (m *dbMeta) NewSession() error {
	if err := m.engine.Sync2(new(session)); err != nil { // old client has no info field
		return err
	}
	v, err := m.incrCounter("nextSession", 1)
	if err != nil {
		return fmt.Errorf("create session: %s", err)
	}
	info, err := newSessionInfo()
	if err != nil {
		return fmt.Errorf("new session info: %s", err)
	}
	info.MountPoint = m.conf.MountPoint
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}
	err = m.txn(func(s *xorm.Session) error {
		return mustInsert(s, &session{v, time.Now().Unix(), data})
	})
	if err != nil {
		return fmt.Errorf("insert new session: %s", err)
	}
	m.sid = v
	logger.Debugf("session is %d", m.sid)

	go m.refreshUsage()
	go m.refreshSession()
	go m.cleanupDeletedFiles()
	go m.cleanupSlices()
	go m.flushStats()
	return nil
}

func (m *dbMeta) refreshUsage() {
	for {
		var c = counter{Name: "usedSpace"}
		_, err := m.engine.Get(&c)
		if err == nil {
			atomic.StoreInt64(&m.usedSpace, c.Value)
		}
		c = counter{Name: "totalInodes"}
		_, err = m.engine.Get(&c)
		if err == nil {
			atomic.StoreInt64(&m.usedInodes, c.Value)
		}
		time.Sleep(time.Second * 10)
	}
}

func (r *dbMeta) checkQuota(size, inodes int64) bool {
	if size > 0 && r.fmt.Capacity > 0 && atomic.LoadInt64(&r.usedSpace)+atomic.LoadInt64(&r.newSpace)+size > int64(r.fmt.Capacity) {
		return true
	}
	return inodes > 0 && r.fmt.Inodes > 0 && atomic.LoadInt64(&r.usedInodes)+atomic.LoadInt64(&r.newInodes)+inodes > int64(r.fmt.Inodes)
}

func (m *dbMeta) getSession(row *session, detail bool) (*Session, error) {
	var s Session
	if row.Info == nil { // legacy client has no info
		row.Info = []byte("{}")
	}
	if err := json.Unmarshal(row.Info, &s); err != nil {
		return nil, fmt.Errorf("corrupted session info; json error: %s", err)
	}
	s.Sid = row.Sid
	s.Heartbeat = time.Unix(row.Heartbeat, 0)
	if detail {
		var (
			srows []sustained
			frows []flock
			prows []plock
		)
		if err := m.engine.Find(&srows, &sustained{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find sustained %d: %s", s.Sid, err)
		}
		s.Sustained = make([]Ino, 0, len(srows))
		for _, srow := range srows {
			s.Sustained = append(s.Sustained, srow.Inode)
		}

		if err := m.engine.Find(&frows, &flock{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find flock %d: %s", s.Sid, err)
		}
		s.Flocks = make([]Flock, 0, len(frows))
		for _, frow := range frows {
			s.Flocks = append(s.Flocks, Flock{frow.Inode, frow.Owner, string(frow.Ltype)})
		}

		if err := m.engine.Find(&prows, &plock{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find plock %d: %s", s.Sid, err)
		}
		s.Plocks = make([]Plock, 0, len(prows))
		for _, prow := range prows {
			s.Plocks = append(s.Plocks, Plock{prow.Inode, prow.Owner, prow.Records})
		}
	}
	return &s, nil
}

func (m *dbMeta) GetSession(sid uint64) (*Session, error) {
	row := session{Sid: sid}
	ok, err := m.engine.Get(&row)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("session not found: %d", sid)
	}
	return m.getSession(&row, true)
}

func (m *dbMeta) ListSessions() ([]*Session, error) {
	var rows []session
	err := m.engine.Find(&rows)
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(rows))
	for _, row := range rows {
		s, err := m.getSession(&row, false)
		if err != nil {
			logger.Errorf("get session: %s", err)
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
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

func (m *dbMeta) incrCounter(name string, batch int64) (uint64, error) {
	var v int64
	err := m.txn(func(s *xorm.Session) error {
		var c = counter{Name: name}
		_, err := s.Get(&c)
		if err != nil {
			return err
		}
		v = c.Value
		_, err = s.Cols("value").Update(&counter{Value: c.Value + batch}, &counter{Name: name})
		return err
	})
	return uint64(v), err
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
		m.freeInodes.maxid = v + 100
	}
	return Ino(v), err
}

func mustInsert(s *xorm.Session, beans ...interface{}) error {
	inserted, err := s.Insert(beans...)
	if err == nil && int(inserted) < len(beans) {
		err = fmt.Errorf("%d records not inserted: %+v", len(beans)-int(inserted), beans)
	}
	return err
}

func (m *dbMeta) shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	// TODO: add other retryable errors here
	msg := err.Error()
	switch m.engine.DriverName() {
	case "sqlite3":
		return errors.Is(err, sqlite3.ErrBusy) || strings.Contains(msg, "database is locked")
	case "mysql":
		// MySQL, MariaDB or TiDB
		return strings.Contains(msg, "try restarting transaction") || strings.Contains(msg, "try again later")
	default:
		return false
	}
}

func (m *dbMeta) txn(f func(s *xorm.Session) error) error {
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	var err error
	for i := 0; i < 50; i++ {
		_, err = m.engine.Transaction(func(s *xorm.Session) (interface{}, error) {
			s.ForUpdate()
			return nil, f(s)
		})
		if m.shouldRetry(err) {
			txRestart.Add(1)
			logger.Debugf("conflicted transaction, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		break
	}
	return err
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
	attr.Atime = n.Atime / 1e6
	attr.Atimensec = uint32(n.Atime % 1e6 * 1000)
	attr.Mtime = n.Mtime / 1e6
	attr.Mtimensec = uint32(n.Mtime % 1e6 * 1000)
	attr.Ctime = n.Ctime / 1e6
	attr.Ctimensec = uint32(n.Ctime % 1e6 * 1000)
	attr.Nlink = n.Nlink
	attr.Length = n.Length
	attr.Rdev = n.Rdev
	attr.Parent = n.Parent
	attr.Full = true
}

func (m *dbMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *dbMeta) flushStats() {
	for {
		newSpace := atomic.SwapInt64(&m.newSpace, 0)
		newInodes := atomic.SwapInt64(&m.newInodes, 0)
		if newSpace != 0 || newInodes != 0 {
			err := m.txn(func(s *xorm.Session) error {
				_, err := s.Exec("UPDATE jfs_counter SET value=value+ (CASE name WHEN 'usedSpace' THEN ? ELSE ? END) WHERE name='usedSpace' OR name='totalInodes' ", newSpace, newInodes)
				return err
			})
			if err != nil {
				logger.Warnf("update stats: %s", err)
				m.updateStats(newSpace, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *dbMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	usedSpace := atomic.LoadInt64(&m.newSpace)
	inodes := atomic.LoadInt64(&m.newInodes)
	var c = counter{Name: "usedSpace"}
	_, err := m.engine.Get(&c)
	if err != nil {
		logger.Warnf("get used space: %s", err)
	} else {
		usedSpace += c.Value
	}
	if usedSpace < 0 {
		usedSpace = 0
	}
	usedSpace = ((usedSpace >> 16) + 1) << 16 // aligned to 64K
	if m.fmt.Capacity > 0 {
		*totalspace = m.fmt.Capacity
		if *totalspace < uint64(usedSpace) {
			*totalspace = uint64(usedSpace)
		}
	} else {
		*totalspace = 1 << 50
		for *totalspace*8 < uint64(usedSpace)*10 {
			*totalspace *= 2
		}
	}

	*availspace = *totalspace - uint64(usedSpace)
	c = counter{Name: "totalInodes"}
	_, err = m.engine.Get(&c)
	if err != nil {
		logger.Warnf("get total inodes: %s", err)
	} else {
		inodes += c.Value
	}
	if inodes < 0 {
		inodes = 0
	}
	*iused = uint64(inodes)
	if m.fmt.Inodes > 0 {
		if *iused > m.fmt.Inodes {
			*iavail = 0
		} else {
			*iavail = m.fmt.Inodes - *iused
		}
	} else {
		*iavail = 10 << 20
	}
	return 0
}

func (m *dbMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	dbSession := m.engine.Table(&edge{})
	if attr != nil {
		dbSession = dbSession.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
	}
	nn := namedNode{node: node{Parent: parent}, Name: name}
	exist, err := dbSession.Select("*").Get(&nn)
	if err != nil {
		return errno(err)
	}
	if !exist {
		if m.conf.CaseInsensi {
			// TODO: in SQL
			if e := m.resolveCase(ctx, parent, name); e != nil {
				*inode = e.Inode
				if attr != nil {
					return m.GetAttr(ctx, *inode, attr)
				}
				return 0
			}
		}
		return syscall.ENOENT
	}
	*inode = nn.Inode
	if attr != nil {
		m.parseAttr(&nn.node, attr)
	}
	return 0
}

func (r *dbMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	return syscall.ENOTSUP
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
	var n = node{Inode: inode}
	ok, err := m.engine.Get(&n)
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
	return errno(m.txn(func(s *xorm.Session) error {
		var cur = node{Inode: inode}
		ok, err := s.Get(&cur)
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
		now := time.Now().UnixNano() / 1e3
		if set&SetAttrAtime != 0 {
			cur.Atime = attr.Atime*1e6 + int64(attr.Atimensec)/1e3
			changed = true
		}
		if set&SetAttrAtimeNow != 0 {
			cur.Atime = now
			changed = true
		}
		if set&SetAttrMtime != 0 {
			cur.Mtime = attr.Mtime*1e6 + int64(attr.Mtimensec)/1e3
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
		cur.Ctime = now
		_, err = s.Cols("mode", "uid", "gid", "atime", "mtime", "ctime").Update(&cur, &node{Inode: inode})
		if err == nil {
			m.parseAttr(&cur, attr)
		}
		return err
	}))
}

func (m *dbMeta) appendSlice(s *xorm.Session, inode Ino, indx uint32, buf []byte) error {
	var r sql.Result
	var err error
	if m.engine.DriverName() == "sqlite3" {
		r, err = s.Exec("update jfs_chunk set slices=slices || ? where inode=? AND indx=?", buf, inode, indx)
	} else {
		r, err = s.Exec("update jfs_chunk set slices=concat(slices, ?) where inode=? AND indx=?", buf, inode, indx)
	}
	if err == nil {
		if n, _ := r.RowsAffected(); n == 0 {
			err = mustInsert(s, &chunk{inode, indx, buf})
		}
	}
	return err
}

func (m *dbMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		var n = node{Inode: inode}
		ok, err := s.Get(&n)
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
			var buf []byte
			if uint32(n.Length/ChunkSize) == indx {
				buf = marshalSlice(uint32(length%ChunkSize), 0, 0, 0, uint32(n.Length-length))
			} else {
				buf = marshalSlice(uint32(length%ChunkSize), 0, 0, 0, ChunkSize-uint32(length%ChunkSize))
			}
			err = m.appendSlice(s, inode, indx, buf)
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
				err = m.appendSlice(s, inode, indx, marshalSlice(0, 0, 0, 0, ChunkSize))
				if err != nil {
					return err
				}
			}
		}
		newSpace = align4K(length) - align4K(n.Length)
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		now := time.Now().UnixNano() / 1e3
		n.Length = length
		n.Mtime = now
		n.Ctime = now
		_, err = s.Cols("length", "mtime", "ctime").Update(&n, &node{Inode: n.Inode})
		if err != nil {
			return err
		}
		m.parseAttr(&n, attr)
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *dbMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
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
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		var n = node{Inode: inode}
		ok, err := s.Get(&n)
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
		newSpace = align4K(length) - align4K(n.Length)
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		now := time.Now().UnixNano() / 1e3
		n.Length = length
		n.Mtime = now
		n.Ctime = now
		if _, err := s.Cols("length", "mtime", "ctime").Update(&n, &node{Inode: inode}); err != nil {
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
				err = m.appendSlice(s, inode, indx, marshalSlice(uint32(coff), 0, 0, 0, uint32(l)))
				if err != nil {
					return err
				}
				off += l
				size -= l
			}
		}
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *dbMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	var l = symlink{Inode: inode}
	ok, err := m.engine.Get(&l)
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
	if m.checkQuota(4<<10, 1) {
		return syscall.ENOSPC
	}
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

	err = m.txn(func(s *xorm.Session) error {
		var pn = node{Inode: parent}
		ok, err := s.Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: name}
		ok, err = s.Get(&e)
		if err != nil {
			return err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		now := time.Now().UnixNano() / 1e3
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

		if err = mustInsert(s, &edge{parent, name, ino, _type}, &n); err != nil {
			return err
		}
		if _, err := s.Cols("nlink", "mtime", "ctime").Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		if _type == TypeSymlink {
			if err = mustInsert(s, &symlink{Inode: ino, Target: path}); err != nil {
				return err
			}
		}
		m.parseAttr(&n, attr)
		return nil
	})
	if err == nil {
		m.updateStats(align4K(0), 1)
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
	var newSpace, newInode int64
	err := m.txn(func(s *xorm.Session) error {
		var pn = node{Inode: parent}
		ok, err := s.Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: name}
		ok, err = s.Get(&e)
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

		var n = node{Inode: e.Inode}
		ok, err = s.Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
			return syscall.EACCES
		}

		now := time.Now().UnixNano() / 1e3
		pn.Mtime = now
		pn.Ctime = now
		n.Nlink--
		n.Ctime = now
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
		if _, err = s.Cols("mtime", "ctime").Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}

		if n.Nlink > 0 {
			if _, err := s.Cols("nlink", "ctime").Update(&n, &node{Inode: e.Inode}); err != nil {
				return err
			}
		} else {
			switch e.Type {
			case TypeFile:
				if opened {
					if err = mustInsert(s, sustained{m.sid, e.Inode}); err != nil {
						return err
					}
					if _, err := s.Cols("nlink", "ctime").Update(&n, &node{Inode: e.Inode}); err != nil {
						return err
					}
				} else {
					if err = mustInsert(s, delfile{e.Inode, n.Length, time.Now().Unix()}); err != nil {
						return err
					}
					if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
						return err
					}
					newSpace, newInode = -align4K(n.Length), -1
				}
			case TypeSymlink:
				if _, err := s.Delete(&symlink{Inode: e.Inode}); err != nil {
					return err
				}
				fallthrough
			default:
				if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
					return err
				}
				newSpace, newInode = -align4K(0), -1
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
	if err == nil {
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *dbMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	if name == "." {
		return syscall.EINVAL
	}
	if name == ".." {
		return syscall.ENOTEMPTY
	}

	err := m.txn(func(s *xorm.Session) error {
		var pn = node{Inode: parent}
		ok, err := s.Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: name}
		ok, err = s.Get(&e)
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
		if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid {
			var n = node{Inode: e.Inode}
			ok, err = s.Get(&n)
			if err != nil {
				return err
			}
			if !ok {
				return syscall.ENOENT
			}
			if ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
		}
		exist, err := s.Exist(&edge{Parent: e.Inode})
		if err != nil {
			return err
		}
		if exist {
			return syscall.ENOTEMPTY
		}

		now := time.Now().UnixNano() / 1e3
		pn.Nlink--
		pn.Mtime = now
		pn.Ctime = now
		if _, err := s.Delete(&edge{Parent: parent, Name: name}); err != nil {
			return err
		}
		if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
			return err
		}
		if _, err := s.Delete(&xattr{Inode: e.Inode}); err != nil {
			return err
		}
		_, err = s.Cols("nlink", "mtime", "ctime").Update(&pn, &node{Inode: pn.Inode})
		return err
	})
	if err == nil {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *dbMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	var newSpace, newInode int64
	err := m.txn(func(s *xorm.Session) error {
		var spn = node{Inode: parentSrc}
		ok, err := s.Get(&spn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if spn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var se = edge{Parent: parentSrc, Name: nameSrc}
		ok, err = s.Get(&se)
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

		var sn = node{Inode: se.Inode}
		ok, err = s.Get(&sn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}

		var dpn = node{Inode: parentDst}
		ok, err = s.Get(&dpn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if dpn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var de = edge{Parent: parentDst, Name: nameDst}
		ok, err = s.Get(&de)
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
		var dn = node{Inode: de.Inode}
		if ok {
			dino = Ino(de.Inode)
			if ctx.Value(CtxKey("behavior")) == "Hadoop" {
				return syscall.EEXIST
			}
			ok, err := s.Get(&dn)
			if err != nil {
				return err
			}
			if !ok {
				return syscall.ENOENT
			}
			if de.Type == TypeDirectory {
				exist, err := s.Exist(&edge{Parent: de.Inode})
				if err != nil {
					return err
				}
				if exist {
					return syscall.ENOTEMPTY
				}
			} else {
				dn.Nlink--
				if dn.Nlink > 0 {
					dn.Ctime = time.Now().UnixNano() / 1e3
				} else if dn.Type == TypeFile {
					m.Lock()
					opened = m.openFiles[Ino(dn.Inode)] > 0
					m.Unlock()
				}
			}
			if ctx.Uid() != 0 && dpn.Mode&01000 != 0 && ctx.Uid() != dpn.Uid && ctx.Uid() != dn.Uid {
				return syscall.EACCES
			}
		}
		if ctx.Uid() != 0 && spn.Mode&01000 != 0 && ctx.Uid() != spn.Uid && ctx.Uid() != sn.Uid {
			return syscall.EACCES
		}

		now := time.Now().UnixNano() / 1e3
		spn.Mtime = now
		spn.Ctime = now
		dpn.Mtime = now
		dpn.Ctime = now
		sn.Parent = parentDst
		sn.Ctime = now
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
		if _, err := s.Cols("nlink", "mtime", "ctime").Update(&spn, &node{Inode: parentSrc}); err != nil {
			return err
		}
		if dino > 0 {
			if dn.Type != TypeDirectory && dn.Nlink > 0 {
				if _, err := s.Update(dn, &node{Inode: dn.Inode}); err != nil {
					return err
				}
			} else {
				if dn.Type == TypeFile {
					if opened {
						if _, err := s.Cols("nlink", "ctime").Update(&dn, &node{Inode: dn.Inode}); err != nil {
							return err
						}
						if err = mustInsert(s, sustained{m.sid, dn.Inode}); err != nil {
							return err
						}
					} else {
						if err = mustInsert(s, delfile{dn.Inode, dn.Length, time.Now().Unix()}); err != nil {
							return err
						}
						if _, err := s.Delete(&node{Inode: dn.Inode}); err != nil {
							return err
						}
						newSpace, newInode = -align4K(dn.Length), -1
					}
				} else {
					if dn.Type == TypeDirectory {
						dn.Nlink--
					} else if dn.Type == TypeSymlink {
						if _, err := s.Delete(&symlink{Inode: dn.Inode}); err != nil {
							return err
						}
					}
					if _, err := s.Delete(&node{Inode: dn.Inode}); err != nil {
						return err
					}
					newSpace, newInode = -align4K(0), -1
				}
				if _, err := s.Delete(xattr{Inode: dino}); err != nil {
					return err
				}
			}
			if _, err := s.Delete(&edge{Parent: parentDst, Name: de.Name}); err != nil {
				return err
			}
		}
		if err = mustInsert(s, &edge{parentDst, nameDst, sn.Inode, sn.Type}); err != nil {
			return err
		}
		if parentDst != parentSrc {
			if _, err := s.Cols("nlink", "mtime", "ctime").Update(&dpn, &node{Inode: parentDst}); err != nil {
				return err
			}
		}
		if _, err := s.Cols("ctime").Update(&sn, &node{Inode: sn.Inode}); err != nil {
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
	if err == nil {
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *dbMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		var pn = node{Inode: parent}
		ok, err := s.Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: name}
		ok, err = s.Get(&e)
		if err != nil {
			return err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		var n = node{Inode: inode}
		ok, err = s.Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type == TypeDirectory {
			return syscall.EPERM
		}

		now := time.Now().UnixNano() / 1e3
		pn.Mtime = now
		pn.Ctime = now
		n.Nlink++
		n.Ctime = now

		if err = mustInsert(s, &edge{Parent: parent, Name: name, Inode: inode, Type: n.Type}); err != nil {
			return err
		}
		if _, err := s.Cols("mtime", "ctime").Update(&pn, &node{Inode: parent}); err != nil {
			return err
		}
		if _, err := s.Cols("nlink", "ctime").Update(&n, node{Inode: inode}); err != nil {
			return err
		}
		if err == nil {
			m.parseAttr(&n, attr)
		}
		return err
	}))
}

func (m *dbMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	var attr Attr
	eno := m.GetAttr(ctx, inode, &attr)
	if eno != 0 {
		return eno
	}
	var pattr Attr
	eno = m.GetAttr(ctx, attr.Parent, &pattr)
	if eno != 0 {
		return eno
	}
	dbSession := m.engine.Table(&edge{})
	if plus != 0 {
		dbSession = dbSession.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
	}
	var nodes []namedNode
	if err := dbSession.Find(&nodes, &edge{Parent: inode}); err != nil {
		return errno(err)
	}

	*entries = make([]*Entry, 0, 2+len(nodes))
	*entries = append(*entries, &Entry{
		Inode: inode,
		Name:  []byte("."),
		Attr:  &attr,
	})
	*entries = append(*entries, &Entry{
		Inode: attr.Parent,
		Name:  []byte(".."),
		Attr:  &pattr,
	})
	for _, n := range nodes {
		entry := &Entry{
			Inode: n.Inode,
			Name:  []byte(n.Name),
			Attr:  &Attr{},
		}
		if plus != 0 {
			m.parseAttr(&n.node, entry.Attr)
		} else {
			entry.Attr.Typ = n.Type
		}
		*entries = append(*entries, entry)
	}
	return 0
}

func (m *dbMeta) cleanStaleSession(sid uint64) {
	// release locks
	_, _ = m.engine.Delete(flock{Sid: sid})
	_, _ = m.engine.Delete(plock{Sid: sid})

	var s = sustained{Sid: sid}
	rows, err := m.engine.Rows(&s)
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

	done := true
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

func (m *dbMeta) cleanStaleSessions() {
	// TODO: once per minute
	var s session
	rows, err := m.engine.Where("Heartbeat < ?", time.Now().Add(time.Minute*-5).Unix()).Rows(&s)
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
		m.cleanStaleSession(sid)
	}
}

func (m *dbMeta) refreshSession() {
	for {
		time.Sleep(time.Minute)
		_ = m.txn(func(ses *xorm.Session) error {
			n, err := ses.Cols("Heartbeat").Update(&session{Heartbeat: time.Now().Unix()}, &session{Sid: m.sid})
			if err == nil && n == 0 {
				err = fmt.Errorf("no session found matching sid: %d", m.sid)
			}
			if err != nil {
				logger.Errorf("update session: %s", err)
			}
			return err
		})
		go m.cleanStaleSessions()
	}
}

func (m *dbMeta) deleteInode(inode Ino) error {
	var n = node{Inode: inode}
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		ok, err := s.Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err = mustInsert(s, &delfile{inode, n.Length, time.Now().Unix()}); err != nil {
			return err
		}
		newSpace = -align4K(n.Length)
		_, err = s.Delete(&node{Inode: inode})
		return err
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		go m.deleteFile(inode, n.Length)
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
		go m.compactChunk(inode, indx, false)
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
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		var n = node{Inode: inode}
		ok, err := s.Get(&n)
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
		if newleng > n.Length {
			newSpace = align4K(newleng) - align4K(n.Length)
			n.Length = newleng
		}
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		now := time.Now().UnixNano() / 1e3
		n.Mtime = now
		n.Ctime = now

		var ck chunk
		ok, err = s.Where("Inode = ? and Indx = ?", inode, indx).Get(&ck)
		if err != nil {
			return err
		}
		buf := marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len)
		if ok {
			if err := m.appendSlice(s, inode, indx, buf); err != nil {
				return err
			}
		} else {
			if err = mustInsert(s, &chunk{inode, indx, buf}); err != nil {
				return err
			}
		}
		if err = mustInsert(s, chunkRef{slice.Chunkid, slice.Size, 1}); err != nil {
			return err
		}
		_, err = s.Cols("length", "mtime", "ctime").Update(&n, &node{Inode: inode})
		if (len(ck.Slices)/sliceBytes)%20 == 19 {
			go m.compactChunk(inode, indx, false)
		}
		return err
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *dbMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		var nin, nout = node{Inode: fin}, node{Inode: fout}
		ok, err := s.Get(&nin)
		if err != nil {
			return err
		}
		ok2, err2 := s.Get(&nout)
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
		if newleng > nout.Length {
			newSpace = align4K(newleng) - align4K(nout.Length)
			nout.Length = newleng
		}
		if m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		now := time.Now().UnixNano() / 1e3
		nout.Mtime = now
		nout.Ctime = now

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
			if err := m.appendSlice(ses, fout, indx, buf); err != nil {
				return err
			}
			if s.Chunkid > 0 {
				if _, err := ses.Exec("update jfs_chunk_ref set refs=refs+1 where chunkid = ? AND size = ?", s.Chunkid, s.Size); err != nil {
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
		if _, err := s.Cols("length", "mtime", "ctime").Update(&nout, &node{Inode: fout}); err != nil {
			return err
		}
		*copied = size
		return nil
	})
	if err == nil {
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *dbMeta) cleanupDeletedFiles() {
	for {
		time.Sleep(time.Minute)
		var d delfile
		rows, err := m.engine.Where("expire < ?", time.Now().Add(-time.Hour).Unix()).Rows(&d)
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
		var c = counter{Name: "nextCleanupSlices"}
		_, err := m.engine.Get(&c)
		if err != nil {
			continue
		}
		now := time.Now().Unix()
		if c.Value+3600 > now {
			continue
		}
		_ = m.txn(func(ses *xorm.Session) error {
			_, err := ses.Update(&counter{Value: now}, counter{Name: "nextCleanupSlices"})
			return err
		})

		var ck chunkRef
		rows, err := m.engine.Where("refs <= 0").Rows(&ck)
		if err != nil {
			continue
		}
		var cks []chunkRef
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
		err = m.txn(func(ses *xorm.Session) error {
			_, err = ses.Exec("delete from jfs_chunk_ref where chunkid=?", chunkid)
			return err
		})
		if err != nil {
			logger.Errorf("delete slice %d: %s", chunkid, err)
		}
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
			_, err = ses.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? AND size=?", s.chunkid, s.size)
			if err != nil {
				return err
			}
		}
		n, err := ses.Delete(&c)
		if err == nil && n == 0 {
			err = fmt.Errorf("chunk %d:%d changed, try restarting transaction", inode, indx)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("delete slice from chunk %s fail: %s, retry later", inode, err)
	}
	for _, s := range ss {
		var ref = chunkRef{Chunkid: s.chunkid}
		ok, err := m.engine.Get(&ref)
		if err == nil && ok && ref.Refs <= 0 {
			m.deleteSlice(s.chunkid, s.size)
		}
	}
	return nil
}

func (m *dbMeta) deleteFile(inode Ino, length uint64) {
	var c = chunk{Inode: inode}
	rows, err := m.engine.Rows(&c)
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
	_, _ = m.engine.Delete(delfile{Inode: inode})
}

func (m *dbMeta) compactChunk(inode Ino, indx uint32, force bool) {
	if !force {
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
	}

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
	err = m.txn(func(s *xorm.Session) error {
		var c2 = chunk{Inode: inode}
		_, err := s.Where("indx=?", indx).Get(&c2)
		if err != nil {
			return err
		}
		if len(c2.Slices) < len(c.Slices) || !bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(c.Slices), len(c2.Slices))
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped], marshalSlice(pos, chunkid, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := s.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if err = mustInsert(s, chunkRef{chunkid, size, 1}); err != nil {
			return err
		}
		ses := s
		for _, s := range ss {
			if _, err := ses.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s.chunkid, s.size); err != nil {
				return err
			}
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if err != nil {
		var c = chunkRef{Chunkid: chunkid}
		ok, e := m.engine.Get(&c)
		if e == nil {
			if ok {
				err = nil
			} else {
				logger.Infof("compacted chunk %d was not used", chunkid)
				err = syscall.EINVAL
			}
		}
	}

	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, chunkid, size)
		m.deleteSlice(chunkid, size)
	} else if err == nil {
		for _, s := range ss {
			var ref = chunkRef{Chunkid: s.chunkid}
			ok, err := m.engine.Get(&ref)
			if err == nil && ok && ref.Refs <= 0 {
				m.deleteSlice(s.chunkid, s.size)
			}
		}
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}
	go func() {
		// wait for the current compaction to finish
		time.Sleep(time.Millisecond * 10)
		m.compactChunk(inode, indx, force)
	}()
}

func dup(b []byte) []byte {
	r := make([]byte, len(b))
	copy(r, b)
	return r
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
			c.Slices = dup(c.Slices)
			cs = append(cs, c)
		}
	}
	rows.Close()

	for _, c := range cs {
		logger.Debugf("compact chunk %d:%d (%d slices)", c.Inode, c.Indx, len(c.Slices)/sliceBytes)
		m.compactChunk(c.Inode, c.Indx, true)
	}
	return 0
}

func (m *dbMeta) ListSlices(ctx Context, slices *[]Slice, delete bool) syscall.Errno {
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
	var x = xattr{Inode: inode, Name: name}
	ok, err := m.engine.Get(&x)
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
	var x = xattr{Inode: inode}
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
	return errno(m.txn(func(s *xorm.Session) error {
		var x = xattr{inode, name, value}
		n, err := s.Insert(&x)
		if err != nil || n == 0 {
			_, err = s.Update(&x, &xattr{inode, name, nil})
		}
		return err
	}))
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	return errno(m.txn(func(s *xorm.Session) error {
		n, err := s.Delete(&xattr{Inode: inode, Name: name})
		if n == 0 {
			err = ENOATTR
		}
		return err
	}))
}
