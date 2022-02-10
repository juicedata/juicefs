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
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/sirupsen/logrus"
	"xorm.io/xorm"
	"xorm.io/xorm/log"
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
	Owner int64  `xorm:"notnull unique(flock)"`
	Ltype byte   `xorm:"notnull"`
}

type plock struct {
	Inode   Ino    `xorm:"notnull unique(plock)"`
	Sid     uint64 `xorm:"notnull unique(plock)"`
	Owner   int64  `xorm:"notnull unique(plock)"`
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

type dbMeta struct {
	baseMeta
	db   *xorm.Engine
	snap *dbSnap
}
type dbSnap struct {
	node    map[Ino]*node
	symlink map[Ino]*symlink
	xattr   map[Ino][]*xattr
	edges   map[Ino][]*edge
	chunk   map[string]*chunk
}

func newSQLMeta(driver, addr string, conf *Config) (Meta, error) {
	if driver == "postgres" {
		addr = driver + "://" + addr
	}
	engine, err := xorm.NewEngine(driver, addr)
	if err != nil {
		return nil, fmt.Errorf("unable to use data source %s: %s", driver, err)
	}
	switch logger.Level { // make xorm less verbose
	case logrus.TraceLevel:
		engine.SetLogLevel(log.LOG_DEBUG)
	case logrus.DebugLevel:
		engine.SetLogLevel(log.LOG_INFO)
	case logrus.InfoLevel, logrus.WarnLevel:
		engine.SetLogLevel(log.LOG_WARNING)
	case logrus.ErrorLevel:
		engine.SetLogLevel(log.LOG_ERR)
	default:
		engine.SetLogLevel(log.LOG_OFF)
	}

	start := time.Now()
	if err = engine.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %s", err)
	}
	if time.Since(start) > time.Millisecond {
		logger.Warnf("The latency to database is too high: %s", time.Since(start))
	}

	engine.SetTableMapper(names.NewPrefixMapper(engine.GetTableMapper(), "jfs_"))
	m := &dbMeta{
		baseMeta: newBaseMeta(conf),
		db:       engine,
	}
	m.en = m
	m.root, err = lookupSubdir(m, conf.Subdir)

	return m, err
}

func (m *dbMeta) Shutdown() error {
	return m.db.Close()
}

func (m *dbMeta) Name() string {
	return m.db.DriverName()
}

func (m *dbMeta) doDeleteSlice(chunkid uint64, size uint32) error {
	return m.txn(func(ses *xorm.Session) error {
		_, err := ses.Exec("delete from jfs_chunk_ref where chunkid=?", chunkid)
		return err
	})
}

func (m *dbMeta) updateCollate() {
	if r, err := m.db.Query("show create table jfs_edge"); err != nil {
		logger.Fatalf("show table jfs_edge: %s", err.Error())
	} else {
		createTable := string(r[0]["Create Table"])
		// the default collate is case-insensitive
		if !strings.Contains(createTable, "SET utf8mb4 COLLATE utf8mb4_bin") {
			_, err := m.db.Exec("alter table jfs_edge modify name varchar (255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL")
			if err != nil && strings.Contains(err.Error(), "Error 1071: Specified key was too long; max key length is 767 bytes") {
				// MySQL 5.6 supports key length up to 767 bytes, so reduce the length of name to 190 chars
				_, err = m.db.Exec("alter table jfs_edge modify name varchar (190) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL")
			}
			if err != nil {
				logger.Fatalf("update collate: %s", err)
			}
		}
	}
}

func (m *dbMeta) Init(format Format, force bool) error {
	if err := m.db.Sync2(new(setting), new(counter)); err != nil {
		logger.Fatalf("create table setting, counter: %s", err)
	}
	if err := m.db.Sync2(new(edge)); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		logger.Fatalf("create table edge: %s", err)
	}
	if err := m.db.Sync2(new(node), new(symlink), new(xattr)); err != nil {
		logger.Fatalf("create table node, symlink, xattr: %s", err)
	}
	if err := m.db.Sync2(new(chunk), new(chunkRef)); err != nil {
		logger.Fatalf("create table chunk, chunk_ref: %s", err)
	}
	if err := m.db.Sync2(new(session), new(sustained), new(delfile)); err != nil {
		logger.Fatalf("create table session, sustaind, delfile: %s", err)
	}
	if err := m.db.Sync2(new(flock), new(plock)); err != nil {
		logger.Fatalf("create table flock, plock: %s", err)
	}
	if m.db.DriverName() == "mysql" {
		m.updateCollate()
	}

	var s = setting{Name: "format"}
	ok, err := m.db.Get(&s)
	if err != nil {
		return err
	}

	if ok {
		var old Format
		err = json.Unmarshal([]byte(s.Value), &old)
		if err != nil {
			return fmt.Errorf("json: %s", err)
		}
		if force {
			old.SecretKey = "removed"
			logger.Warnf("Existing volume will be overwrited: %+v", old)
		} else {
			format.UUID = old.UUID
			// these can be safely updated.
			old.Bucket = format.Bucket
			old.AccessKey = format.AccessKey
			old.SecretKey = format.SecretKey
			old.Capacity = format.Capacity
			old.Inodes = format.Inodes
			old.TrashDays = format.TrashDays
			if format != old {
				old.SecretKey = ""
				format.SecretKey = ""
				return fmt.Errorf("cannot update format from %+v to %+v", old, format)
			}
		}
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}

	m.fmt = format
	now := time.Now()
	n := &node{
		Type:   TypeDirectory,
		Atime:  now.UnixNano() / 1000,
		Mtime:  now.UnixNano() / 1000,
		Ctime:  now.UnixNano() / 1000,
		Nlink:  2,
		Length: 4 << 10,
		Parent: 1,
	}
	return m.txn(func(s *xorm.Session) error {
		if format.TrashDays > 0 {
			ok2, err := s.Get(&node{Inode: TrashInode})
			if err != nil {
				return err
			}
			if !ok2 {
				n.Inode = TrashInode
				n.Mode = 0555
				if err = mustInsert(s, n); err != nil {
					return err
				}
			}
		}
		if ok {
			_, err = s.Update(&setting{"format", string(data)}, &setting{Name: "format"})
			return err
		}
		var set = &setting{"format", string(data)}
		n.Inode = 1
		n.Mode = 0777
		var cs = []counter{
			{"nextInode", 2}, // 1 is root
			{"nextChunk", 1},
			{"nextSession", 0},
			{"usedSpace", 0},
			{"totalInodes", 0},
			{"nextCleanupSlices", 0},
		}
		return mustInsert(s, set, n, &cs)
	})
}

func (m *dbMeta) Reset() error {
	return m.db.DropTables(&setting{}, &counter{},
		&node{}, &edge{}, &symlink{}, &xattr{},
		&chunk{}, &chunkRef{},
		&session{}, &sustained{}, &delfile{},
		&flock{}, &plock{})
}

func (m *dbMeta) doLoad() ([]byte, error) {
	s := setting{Name: "format"}
	_, err := m.db.Get(&s)
	return []byte(s.Value), err
}

func (m *dbMeta) doNewSession(sinfo []byte) error {
	// old client has no info field
	err := m.db.Sync2(new(session))
	if err != nil {
		return fmt.Errorf("update table session: %s", err)
	}
	// update the owner from uint64 to int64
	if err = m.db.Sync2(new(flock), new(plock)); err != nil {
		return fmt.Errorf("update table flock, plock: %s", err)
	}
	if m.db.DriverName() == "mysql" {
		m.updateCollate()
	}

	for {
		if err = m.txn(func(s *xorm.Session) error {
			return mustInsert(s, &session{m.sid, time.Now().Unix(), sinfo})
		}); err == nil {
			break
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			logger.Warnf("session id %d is already used", m.sid)
			if v, e := m.incrCounter("nextSession", 1); e == nil {
				m.sid = uint64(v)
				continue
			} else {
				return fmt.Errorf("get session ID: %s", e)
			}
		} else {
			return fmt.Errorf("insert new session %d: %s", m.sid, err)
		}
	}

	go m.flushStats()
	return nil
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
		if err := m.db.Find(&srows, &sustained{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find sustained %d: %s", s.Sid, err)
		}
		s.Sustained = make([]Ino, 0, len(srows))
		for _, srow := range srows {
			s.Sustained = append(s.Sustained, srow.Inode)
		}

		if err := m.db.Find(&frows, &flock{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find flock %d: %s", s.Sid, err)
		}
		s.Flocks = make([]Flock, 0, len(frows))
		for _, frow := range frows {
			s.Flocks = append(s.Flocks, Flock{frow.Inode, uint64(frow.Owner), string(frow.Ltype)})
		}

		if err := m.db.Find(&prows, &plock{Sid: s.Sid}); err != nil {
			return nil, fmt.Errorf("find plock %d: %s", s.Sid, err)
		}
		s.Plocks = make([]Plock, 0, len(prows))
		for _, prow := range prows {
			s.Plocks = append(s.Plocks, Plock{prow.Inode, uint64(prow.Owner), prow.Records})
		}
	}
	return &s, nil
}

func (m *dbMeta) GetSession(sid uint64) (*Session, error) {
	row := session{Sid: sid}
	ok, err := m.db.Get(&row)
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
	err := m.db.Find(&rows)
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(rows))
	for i := range rows {
		s, err := m.getSession(&rows[i], false)
		if err != nil {
			logger.Errorf("get session: %s", err)
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (m *dbMeta) incrCounter(name string, batch int64) (int64, error) {
	var v int64
	err := m.txn(func(s *xorm.Session) error {
		var c = counter{Name: name}
		ok, err := s.Get(&c)
		if err != nil {
			return err
		}
		v = c.Value + batch
		if batch > 0 {
			c.Value = v
			if ok {
				_, err = s.Cols("value").Update(&c, &counter{Name: name})
			} else {
				err = mustInsert(s, &c)
			}
		}
		return err
	})
	return v, err
}

func (m *dbMeta) setIfSmall(name string, value, diff int64) (bool, error) {
	c := counter{Name: name}
	ok, err := m.db.Get(&c)
	if err != nil {
		return false, err
	}
	if c.Value > value-diff {
		return false, nil
	} else {
		if ok {
			err = m.txn(func(s *xorm.Session) error {
				_, err := s.Update(&counter{Value: value}, &counter{Name: name})
				return err
			})
		} else {
			err = m.txn(func(s *xorm.Session) error {
				_, err := s.InsertOne(&counter{Name: name, Value: value})
				return err
			})
		}
		return true, err
	}
}

func mustInsert(s *xorm.Session, beans ...interface{}) error {
	var start, end int
	batchSize := 200
	for i := 0; i < len(beans)/batchSize; i++ {
		end = start + batchSize
		inserted, err := s.Insert(beans[start:end]...)
		if err == nil && int(inserted) < end-start {
			return fmt.Errorf("%d records not inserted: %+v", end-start-int(inserted), beans[start:end])
		}
		start = end
	}
	if len(beans)%batchSize != 0 {
		inserted, err := s.Insert(beans[end:]...)
		if err == nil && int(inserted) < len(beans)-end {
			return fmt.Errorf("%d records not inserted: %+v", len(beans)-end-int(inserted), beans[end:])
		}
	}
	return nil
}

var errBusy error

func (m *dbMeta) shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(syscall.Errno); ok {
		return false
	}
	// TODO: add other retryable errors here
	msg := err.Error()
	switch m.db.DriverName() {
	case "sqlite3":
		return errors.Is(err, errBusy) || strings.Contains(msg, "database is locked")
	case "mysql":
		// MySQL, MariaDB or TiDB
		return strings.Contains(msg, "try restarting transaction") || strings.Contains(msg, "try again later")
	case "postgres":
		return strings.Contains(msg, "current transaction is aborted") || strings.Contains(msg, "deadlock detected")
	default:
		return false
	}
}

func (m *dbMeta) txn(f func(s *xorm.Session) error) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	var err error
	for i := 0; i < 50; i++ {
		_, err = m.db.Transaction(func(s *xorm.Session) (interface{}, error) {
			s.ForUpdate()
			return nil, f(s)
		})
		if m.shouldRetry(err) {
			txRestart.Add(1)
			logger.Debugf("conflicted transaction, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", err)
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

func (m *dbMeta) flushStats() {
	var inttype = "BIGINT"
	if m.db.DriverName() == "mysql" {
		inttype = "SIGNED"
	}
	for {
		newSpace := atomic.SwapInt64(&m.newSpace, 0)
		newInodes := atomic.SwapInt64(&m.newInodes, 0)
		if newSpace != 0 || newInodes != 0 {
			err := m.txn(func(s *xorm.Session) error {
				_, err := s.Exec("UPDATE jfs_counter SET value=value+ CAST((CASE name WHEN 'usedSpace' THEN ? ELSE ? END) AS "+inttype+") WHERE name='usedSpace' OR name='totalInodes' ", newSpace, newInodes)
				return err
			})
			if err != nil && !strings.Contains(err.Error(), "attempt to write a readonly database") {
				logger.Warnf("update stats: %s", err)
				m.updateStats(newSpace, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *dbMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	dbSession := m.db.Table(&edge{})
	if attr != nil {
		dbSession = dbSession.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
	}
	nn := namedNode{node: node{Parent: parent}, Name: name}
	exist, err := dbSession.Select("*").Get(&nn)
	if err != nil {
		return errno(err)
	}
	if !exist {
		return syscall.ENOENT
	}
	*inode = nn.Inode
	m.parseAttr(&nn.node, attr)
	return 0
}

func (m *dbMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	var n = node{Inode: inode}
	ok, err := m.db.Get(&n)
	if ok {
		m.parseAttr(&n, attr)
	} else if err == nil {
		err = syscall.ENOENT
	}
	return errno(err)
}

func clearSUGIDSQL(ctx Context, cur *node, set *Attr) {
	switch runtime.GOOS {
	case "darwin":
		if ctx.Uid() != 0 {
			// clear SUID and SGID
			cur.Mode &= 01777
			set.Mode &= 01777
		}
	case "linux":
		// same as ext
		if cur.Type != TypeDirectory {
			if ctx.Uid() != 0 || (cur.Mode>>3)&1 != 0 {
				// clear SUID and SGID
				cur.Mode &= 01777
				set.Mode &= 01777
			} else {
				// keep SGID if the file is non-group-executable
				cur.Mode &= 03777
				set.Mode &= 03777
			}
		}
	}
}

func (m *dbMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFE) }()
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
			clearSUGIDSQL(ctx, &cur, attr)
			changed = true
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
	driver := m.db.DriverName()
	if driver == "sqlite3" || driver == "postgres" {
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
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
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
		newSpace = align4K(length) - align4K(n.Length)
		if newSpace > 0 && m.checkQuota(newSpace, 0) {
			return syscall.ENOSPC
		}
		var c chunk
		var zeroChunks []uint32
		var left, right = n.Length, length
		if left > right {
			right, left = left, right
		}
		if right/ChunkSize-left/ChunkSize > 1 {
			rows, err := s.Where("inode = ? AND indx > ? AND indx < ?", inode, left/ChunkSize, right/ChunkSize).Cols("indx").Rows(&c)
			if err != nil {
				return err
			}
			for rows.Next() {
				if err = rows.Scan(&c); err != nil {
					_ = rows.Close()
					return err
				}
				zeroChunks = append(zeroChunks, c.Indx)
			}
			_ = rows.Close()
		}

		l := uint32(right - left)
		if right > (left/ChunkSize+1)*ChunkSize {
			l = ChunkSize - uint32(left%ChunkSize)
		}
		if err = m.appendSlice(s, inode, uint32(left/ChunkSize), marshalSlice(uint32(left%ChunkSize), 0, 0, 0, l)); err != nil {
			return err
		}
		buf := marshalSlice(0, 0, 0, 0, ChunkSize)
		for _, indx := range zeroChunks {
			if err = m.appendSlice(s, inode, indx, buf); err != nil {
				return err
			}
		}
		if right > (left/ChunkSize+1)*ChunkSize && right%ChunkSize > 0 {
			if err = m.appendSlice(s, inode, uint32(right/ChunkSize), marshalSlice(0, 0, 0, 0, uint32(right%ChunkSize))); err != nil {
				return err
			}
		}
		n.Length = length
		now := time.Now().UnixNano() / 1e3
		n.Mtime = now
		n.Ctime = now
		if _, err = s.Cols("length", "mtime", "ctime").Update(&n, &node{Inode: n.Inode}); err != nil {
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
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, 0xFFFFFFFF) }()
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

func (m *dbMeta) doReadlink(ctx Context, inode Ino) ([]byte, error) {
	var l = symlink{Inode: inode}
	_, err := m.db.Get(&l)
	return []byte(l.Target), err
}

func (m *dbMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	if m.checkQuota(4<<10, 1) {
		return syscall.ENOSPC
	}
	parent = m.checkRoot(parent)
	var ino Ino
	var err error
	if parent == TrashInode {
		var next int64
		next, err = m.incrCounter("nextTrash", 1)
		ino = TrashInode + Ino(next)
	} else {
		ino, err = m.nextInode()
	}
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
		var foundIno Ino
		var foundType uint8
		if ok {
			foundType, foundIno = e.Type, e.Inode
		} else if m.conf.CaseInsensi {
			if entry := m.resolveCase(ctx, parent, name); entry != nil {
				foundType, foundIno = entry.Attr.Typ, entry.Inode
			}
		}
		if foundIno != 0 {
			if _type == TypeFile || _type == TypeDirectory {
				foundNode := node{Inode: foundIno}
				ok, err = s.Get(&foundNode)
				if err != nil {
					return err
				} else if ok {
					m.parseAttr(&foundNode, attr)
				} else if attr != nil {
					*attr = Attr{Typ: foundType, Parent: parent} // corrupt entry
				}
				if inode != nil {
					*inode = foundIno
				}
			}
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
		if pn.Mode&02000 != 0 || ctx.Value(CtxKey("behavior")) == "Hadoop" || runtime.GOOS == "darwin" {
			n.Gid = pn.Gid
			if _type == TypeDirectory && runtime.GOOS == "linux" {
				n.Mode |= pn.Mode & 02000
			}
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

func (m *dbMeta) doUnlink(ctx Context, parent Ino, name string) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parent, &trash); st != 0 {
		return st
	}
	var newSpace, newInode int64
	var n node
	var opened bool
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
				e.Name = string(ee.Name)
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

		n = node{Inode: e.Inode}
		ok, err = s.Get(&n)
		if err != nil {
			return err
		}
		now := time.Now().UnixNano() / 1e3
		opened = false
		if ok {
			if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
			n.Ctime = now
			if trash == 0 {
				n.Nlink--
				if n.Type == TypeFile && n.Nlink == 0 {
					opened = m.of.IsOpen(e.Inode)
				}
			} else if n.Nlink == 1 {
				n.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, name)
			trash = 0
		}
		defer func() { m.of.InvalidateChunk(e.Inode, 0xFFFFFFFE) }()

		pn.Mtime = now
		pn.Ctime = now

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if _, err = s.Cols("mtime", "ctime").Update(&pn, &node{Inode: pn.Inode}); err != nil {
			return err
		}
		if n.Nlink > 0 {
			if _, err := s.Cols("nlink", "ctime").Update(&n, &node{Inode: e.Inode}); err != nil {
				return err
			}
			if trash > 0 {
				if err = mustInsert(s, &edge{trash, fmt.Sprintf("%d-%d-%s", parent, e.Inode, e.Name), e.Inode, e.Type}); err != nil {
					return err
				}
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
			if _, err := s.Delete(&xattr{Inode: e.Inode}); err != nil {
				return err
			}
		}
		return err
	})
	if err == nil && trash == 0 {
		if n.Type == TypeFile && n.Nlink == 0 {
			m.fileDeleted(opened, n.Inode, n.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *dbMeta) doRmdir(ctx Context, parent Ino, name string) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parent, &trash); st != 0 {
		return st
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
				e.Name = string(ee.Name)
				e.Type = ee.Attr.Typ
			}
		}
		if !ok {
			return syscall.ENOENT
		}
		if e.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		exist, err := s.Exist(&edge{Parent: e.Inode})
		if err != nil {
			return err
		}
		if exist {
			return syscall.ENOTEMPTY
		}
		var n = node{Inode: e.Inode}
		ok, err = s.Get(&n)
		if err != nil {
			return err
		}

		now := time.Now().UnixNano() / 1e3
		if ok {
			if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
			if trash > 0 {
				n.Ctime = now
				n.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, name)
			trash = 0
		}
		pn.Nlink--
		pn.Mtime = now
		pn.Ctime = now

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if trash > 0 {
			if _, err = s.Cols("nlink", "ctime").Update(&n, &node{Inode: n.Inode}); err != nil {
				return err
			}
			if err = mustInsert(s, &edge{trash, fmt.Sprintf("%d-%d-%s", parent, e.Inode, e.Name), e.Inode, e.Type}); err != nil {
				return err
			}
		} else {
			if _, err := s.Delete(&node{Inode: e.Inode}); err != nil {
				return err
			}
			if _, err := s.Delete(&xattr{Inode: e.Inode}); err != nil {
				return err
			}
		}
		_, err = s.Cols("nlink", "mtime", "ctime").Update(&pn, &node{Inode: pn.Inode})
		return err
	})
	if err == nil && trash == 0 {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *dbMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parentDst, &trash); st != 0 {
		return st
	}
	exchange := flags == RenameExchange
	var opened bool
	var dino Ino
	var dn node
	var newSpace, newInode int64
	err := m.txn(func(s *xorm.Session) error {
		var se = edge{Parent: parentSrc, Name: nameSrc}
		ok, err := s.Get(&se)
		if err != nil {
			return err
		}
		if !ok && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentSrc, nameSrc); e != nil {
				ok = true
				se.Inode = e.Inode
				se.Type = e.Attr.Typ
				se.Name = string(e.Name)
			}
		}
		if !ok {
			return syscall.ENOENT
		}
		if parentSrc == parentDst && se.Name == nameDst {
			if inode != nil {
				*inode = se.Inode
			}
			return nil
		}
		var spn = node{Inode: parentSrc}
		ok, err = s.Get(&spn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if spn.Type != TypeDirectory {
			return syscall.ENOTDIR
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
		var sn = node{Inode: se.Inode}
		ok, err = s.Get(&sn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
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
		now := time.Now().UnixNano() / 1e3
		opened = false
		dn = node{Inode: de.Inode}
		if ok {
			if flags == RenameNoReplace {
				return syscall.EEXIST
			}
			dino = de.Inode
			ok, err := s.Get(&dn)
			if err != nil {
				return err
			}
			if !ok { // corrupt entry
				logger.Warnf("no attribute for inode %d (%d, %s)", dino, parentDst, de.Name)
				trash = 0
			}
			dn.Ctime = now
			if exchange {
				dn.Parent = parentSrc
				if de.Type == TypeDirectory && parentSrc != parentDst {
					dpn.Nlink--
					spn.Nlink++
				}
			} else {
				if de.Type == TypeDirectory {
					exist, err := s.Exist(&edge{Parent: de.Inode})
					if err != nil {
						return err
					}
					if exist {
						return syscall.ENOTEMPTY
					}
					dpn.Nlink--
					if trash > 0 {
						dn.Parent = trash
					}
				} else {
					if trash == 0 {
						dn.Nlink--
						if de.Type == TypeFile && dn.Nlink == 0 {
							opened = m.of.IsOpen(dn.Inode)
						}
						defer func() { m.of.InvalidateChunk(dino, 0xFFFFFFFE) }()
					} else if dn.Nlink == 1 {
						dn.Parent = trash
					}
				}
			}
			if ctx.Uid() != 0 && dpn.Mode&01000 != 0 && ctx.Uid() != dpn.Uid && ctx.Uid() != dn.Uid {
				return syscall.EACCES
			}
		} else {
			if exchange {
				return syscall.ENOENT
			}
			dino = 0
		}
		if ctx.Uid() != 0 && spn.Mode&01000 != 0 && ctx.Uid() != spn.Uid && ctx.Uid() != sn.Uid {
			return syscall.EACCES
		}

		spn.Mtime = now
		spn.Ctime = now
		dpn.Mtime = now
		dpn.Ctime = now
		sn.Parent = parentDst
		sn.Ctime = now
		if se.Type == TypeDirectory && parentSrc != parentDst {
			spn.Nlink--
			dpn.Nlink++
		}
		if inode != nil {
			*inode = sn.Inode
		}
		m.parseAttr(&sn, attr)

		if exchange {
			if _, err := s.Cols("inode", "type").Update(&de, &edge{Parent: parentSrc, Name: se.Name}); err != nil {
				return err
			}
			if _, err := s.Cols("inode", "type").Update(&se, &edge{Parent: parentDst, Name: de.Name}); err != nil {
				return err
			}
			if _, err := s.Cols("ctime", "parent").Update(dn, &node{Inode: dino}); err != nil {
				return err
			}
		} else {
			if n, err := s.Delete(&edge{Parent: parentSrc, Name: se.Name}); err != nil {
				return err
			} else if n != 1 {
				return fmt.Errorf("delete src failed")
			}
			if dino > 0 {
				if trash > 0 {
					if _, err := s.Cols("ctime", "parent").Update(dn, &node{Inode: dino}); err != nil {
						return err
					}
					name := fmt.Sprintf("%d-%d-%s", parentDst, dino, de.Name)
					if err = mustInsert(s, &edge{trash, name, dino, de.Type}); err != nil {
						return err
					}
				} else if de.Type != TypeDirectory && dn.Nlink > 0 {
					if _, err := s.Cols("ctime", "nlink").Update(dn, &node{Inode: dino}); err != nil {
						return err
					}
				} else {
					if de.Type == TypeFile {
						if opened {
							if _, err := s.Cols("nlink", "ctime").Update(&dn, &node{Inode: dino}); err != nil {
								return err
							}
							if err = mustInsert(s, sustained{m.sid, dino}); err != nil {
								return err
							}
						} else {
							if err = mustInsert(s, delfile{dino, dn.Length, time.Now().Unix()}); err != nil {
								return err
							}
							if _, err := s.Delete(&node{Inode: dino}); err != nil {
								return err
							}
							newSpace, newInode = -align4K(dn.Length), -1
						}
					} else {
						if de.Type == TypeSymlink {
							if _, err := s.Delete(&symlink{Inode: dino}); err != nil {
								return err
							}
						}
						if _, err := s.Delete(&node{Inode: dino}); err != nil {
							return err
						}
						newSpace, newInode = -align4K(0), -1
					}
					if _, err := s.Delete(&xattr{Inode: dino}); err != nil {
						return err
					}
				}
				if _, err := s.Delete(&edge{Parent: parentDst, Name: de.Name}); err != nil {
					return err
				}
			}
			if err = mustInsert(s, &edge{parentDst, de.Name, se.Inode, se.Type}); err != nil {
				return err
			}
		}
		if parentDst != parentSrc && !isTrash(parentSrc) {
			if _, err := s.Cols("nlink", "mtime", "ctime").Update(&spn, &node{Inode: parentSrc}); err != nil {
				return err
			}
		}
		if _, err := s.Cols("ctime", "parent").Update(&sn, &node{Inode: sn.Inode}); err != nil {
			return err
		}
		if _, err := s.Cols("nlink", "mtime", "ctime").Update(&dpn, &node{Inode: parentDst}); err != nil {
			return err
		}
		return err
	})
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dn.Type == TypeFile && dn.Nlink == 0 {
			m.fileDeleted(opened, dino, dn.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	return errno(err)
}

func (m *dbMeta) doLink(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
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

func (m *dbMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	dbSession := m.db.Table(&edge{})
	if plus != 0 {
		dbSession = dbSession.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
	}
	var nodes []namedNode
	if err := dbSession.Find(&nodes, &edge{Parent: inode}); err != nil {
		return errno(err)
	}
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

func (m *dbMeta) doCleanStaleSession(sid uint64) {
	// release locks
	_, _ = m.db.Delete(flock{Sid: sid})
	_, _ = m.db.Delete(plock{Sid: sid})

	var s = sustained{Sid: sid}
	rows, err := m.db.Rows(&s)
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
	_ = rows.Close()

	done := true
	for _, inode := range inodes {
		if err := m.doDeleteSustainedInode(sid, inode); err != nil {
			logger.Errorf("Failed to delete inode %d: %s", inode, err)
			done = false
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

func (m *dbMeta) doFindStaleSessions(ts int64, limit int) ([]uint64, error) {
	var s session
	rows, err := m.db.Where("Heartbeat < ?", ts).Limit(limit, 0).Rows(&s)
	if err != nil {
		return nil, err
	}
	var sids []uint64
	for rows.Next() {
		if rows.Scan(&s) == nil {
			sids = append(sids, s.Sid)
		}
	}
	_ = rows.Close()
	return sids, nil
}

func (m *dbMeta) doRefreshSession() {
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
}

func (m *dbMeta) doDeleteSustainedInode(sid uint64, inode Ino) error {
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
		_, err = s.Delete(&sustained{sid, inode})
		if err != nil {
			return err
		}
		newSpace = -align4K(n.Length)
		_, err = s.Delete(&node{Inode: inode})
		return err
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		go m.doDeleteFileData(inode, n.Length)
	}
	return err
}

func (m *dbMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	f := m.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if cs, ok := m.of.ReadChunk(inode, indx); ok {
		*chunks = cs
		return 0
	}
	defer timeit(time.Now())
	var c chunk
	_, err := m.db.Where("inode=? and indx=?", inode, indx).Get(&c)
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(c.Slices)
	if ss == nil {
		return syscall.EIO
	}
	*chunks = buildSlice(ss)
	m.of.CacheChunk(inode, indx, *chunks)
	if !m.conf.ReadOnly && (len(c.Slices)/sliceBytes >= 5 || len(*chunks) >= 5) {
		go m.compactChunk(inode, indx, false)
	}
	return 0
}

func (m *dbMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	defer timeit(time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var newSpace int64
	var needCompact bool
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
		if err == nil {
			needCompact = (len(ck.Slices)/sliceBytes)%100 == 99
		}
		return err
	})
	if err == nil {
		if needCompact {
			go m.compactChunk(inode, indx, false)
		}
		m.updateStats(newSpace, 0)
	}
	return errno(err)
}

func (m *dbMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	defer timeit(time.Now())
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	var newSpace int64
	defer func() { m.of.InvalidateChunk(fout, 0xFFFFFFFF) }()
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
				_ = rows.Close()
				return err
			}
			chunks[c.Indx] = readSliceBuf(c.Slices)
		}
		_ = rows.Close()

		ses := s
		updateSlices := func(indx uint32, buf []byte, chunkid uint64, size uint32) error {
			if err := m.appendSlice(ses, fout, indx, buf); err != nil {
				return err
			}
			if chunkid > 0 {
				if _, err := ses.Exec("update jfs_chunk_ref set refs=refs+1 where chunkid = ? AND size = ?", chunkid, size); err != nil {
					return err
				}
			}
			return nil
		}
		coff := offIn / ChunkSize * ChunkSize
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
						if err := updateSlices(indx, marshalSlice(dpos, s.Chunkid, s.Size, s.Off, ChunkSize-dpos), s.Chunkid, s.Size); err != nil {
							return err
						}
						skip := ChunkSize - dpos
						if err := updateSlices(indx+1, marshalSlice(0, s.Chunkid, s.Size, s.Off+skip, s.Len-skip), s.Chunkid, s.Size); err != nil {
							return err
						}
					} else {
						if err := updateSlices(indx, marshalSlice(dpos, s.Chunkid, s.Size, s.Off, s.Len), s.Chunkid, s.Size); err != nil {
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

func (m *dbMeta) doFindDeletedFiles(ts int64, limit int) (map[Ino]uint64, error) {
	var d delfile
	rows, err := m.db.Where("expire < ?", ts).Limit(limit, 0).Rows(&d)
	if err != nil {
		return nil, err
	}
	files := make(map[Ino]uint64)
	for rows.Next() {
		if rows.Scan(&d) == nil {
			files[d.Inode] = d.Length
		}
	}
	_ = rows.Close()
	return files, nil
}

func (m *dbMeta) doCleanupSlices() {
	var ck chunkRef
	rows, err := m.db.Where("refs <= 0").Rows(&ck)
	if err != nil {
		return
	}
	var cks []chunkRef
	for rows.Next() {
		if rows.Scan(&ck) == nil {
			cks = append(cks, ck)
		}
	}
	_ = rows.Close()
	for _, ck := range cks {
		m.deleteSlice(ck.Chunkid, ck.Size)
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
		c.Slices = nil
		n, err := ses.Where("inode = ? AND indx = ?", inode, indx).Delete(&c)
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
		ok, err := m.db.Get(&ref)
		if err == nil && ok && ref.Refs <= 0 {
			m.deleteSlice(s.chunkid, s.size)
		}
	}
	return nil
}

func (m *dbMeta) doDeleteFileData(inode Ino, length uint64) {
	var c = chunk{Inode: inode}
	rows, err := m.db.Rows(&c)
	if err != nil {
		return
	}
	var indexes []uint32
	for rows.Next() {
		if rows.Scan(&c) == nil {
			indexes = append(indexes, c.Indx)
		}
	}
	_ = rows.Close()
	for _, indx := range indexes {
		err = m.deleteChunk(inode, indx)
		if err != nil {
			logger.Warnf("deleteChunk inode %d index %d error: %s", inode, indx, err)
			return
		}
	}
	_, _ = m.db.Delete(delfile{Inode: inode})
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
	_, err := m.db.Where("inode=? and indx=?", inode, indx).Get(&c)
	if err != nil {
		return
	}

	ss := readSliceBuf(c.Slices)
	skipped := skipSome(ss)
	ss = ss[skipped:]
	pos, size, chunks := compactChunk(ss)
	if len(ss) < 2 || size == 0 {
		return
	}

	var chunkid uint64
	st := m.NewChunk(Background, &chunkid)
	if st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = m.newMsg(CompactChunk, chunks, chunkid)
	if err != nil {
		if !strings.Contains(err.Error(), "not exist") && !strings.Contains(err.Error(), "not found") {
			logger.Warnf("compact %d %d with %d slices: %s", inode, indx, len(ss), err)
		}
		return
	}
	err = m.txn(func(ses *xorm.Session) error {
		var c2 = chunk{Inode: inode}
		_, err := ses.Where("indx=?", indx).Get(&c2)
		if err != nil {
			return err
		}
		if len(c2.Slices) < len(c.Slices) || !bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(c.Slices), len(c2.Slices))
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped*sliceBytes], marshalSlice(pos, chunkid, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := ses.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if err = mustInsert(ses, chunkRef{chunkid, size, 1}); err != nil {
			return err
		}
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
		ok, e := m.db.Get(&c)
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
		m.of.InvalidateChunk(inode, indx)
		for _, s := range ss {
			var ref = chunkRef{Chunkid: s.chunkid}
			ok, err := m.db.Get(&ref)
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

func (m *dbMeta) CompactAll(ctx Context, bar *utils.Bar) syscall.Errno {
	var c chunk
	rows, err := m.db.Where("length(slices) >= ?", sliceBytes*2).Cols("inode", "indx").Rows(&c)
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
	_ = rows.Close()

	bar.IncrTotal(int64(len(cs)))
	for _, c := range cs {
		logger.Debugf("compact chunk %d:%d (%d slices)", c.Inode, c.Indx, len(c.Slices)/sliceBytes)
		m.compactChunk(c.Inode, c.Indx, true)
		bar.Increment()
	}
	return 0
}

func (m *dbMeta) ListSlices(ctx Context, slices map[Ino][]Slice, delete bool, showProgress func()) syscall.Errno {
	if delete {
		m.doCleanupSlices()
	}
	var c chunk
	rows, err := m.db.Rows(&c)
	if err != nil {
		return errno(err)
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&c)
		if err != nil {
			return errno(err)
		}
		ss := readSliceBuf(c.Slices)
		for _, s := range ss {
			if s.chunkid > 0 {
				slices[c.Inode] = append(slices[c.Inode], Slice{Chunkid: s.chunkid, Size: s.size})
				if showProgress != nil {
					showProgress()
				}
			}
		}
	}
	return 0
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	var x = xattr{Inode: inode, Name: name}
	ok, err := m.db.Get(&x)
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
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	var x = xattr{Inode: inode}
	rows, err := m.db.Where("inode = ?", inode).Rows(&x)
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

func (m *dbMeta) SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	return errno(m.txn(func(s *xorm.Session) error {
		var x = xattr{inode, name, value}
		var err error
		var n int64
		switch flags {
		case XattrCreate:
			n, err = s.Insert(&x)
			if err != nil || n == 0 {
				err = syscall.EEXIST
			}
		case XattrReplace:
			n, err = s.Update(&x, &xattr{inode, name, nil})
			if err == nil && n == 0 {
				err = ENOATTR
			}
		default:
			n, err = s.Insert(&x)
			if err != nil || n == 0 {
				if m.db.DriverName() == "postgres" {
					// cleanup failed session
					_ = s.Rollback()
				}
				_, err = s.Update(&x, &xattr{inode, name, nil})
			}
		}
		return err
	}))
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	if name == "" {
		return syscall.EINVAL
	}
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	return errno(m.txn(func(s *xorm.Session) error {
		n, err := s.Delete(&xattr{Inode: inode, Name: name})
		if err != nil {
			return err
		} else if n == 0 {
			return ENOATTR
		} else {
			return nil
		}
	}))
}

func (m *dbMeta) dumpEntry(inode Ino) (*DumpedEntry, error) {
	e := &DumpedEntry{}
	return e, m.txn(func(s *xorm.Session) error {
		n := &node{Inode: inode}
		ok, err := m.db.Get(n)
		if err != nil {
			return err
		}
		if !ok {
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
			return nil
		}
		attr := &Attr{}
		m.parseAttr(n, attr)
		e.Attr = dumpAttr(attr)
		e.Attr.Inode = inode

		var rows []xattr
		if err = m.db.Find(&rows, &xattr{Inode: inode}); err != nil {
			return err
		}
		if len(rows) > 0 {
			xattrs := make([]*DumpedXattr, 0, len(rows))
			for _, x := range rows {
				xattrs = append(xattrs, &DumpedXattr{x.Name, string(x.Value)})
			}
			sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
			e.Xattrs = xattrs
		}

		if attr.Typ == TypeFile {
			for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
				c := &chunk{Inode: inode, Indx: indx}
				if ok, err = m.db.Get(c); err != nil {
					return err
				}
				if !ok {
					logger.Warnf("no found chunk target for inode %d indx %d", inode, indx)
					return nil
				}
				ss := readSliceBuf(c.Slices)
				slices := make([]*DumpedSlice, 0, len(ss))
				for _, s := range ss {
					slices = append(slices, &DumpedSlice{Chunkid: s.chunkid, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
				}
				e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
			}
		} else if attr.Typ == TypeSymlink {
			l := &symlink{Inode: inode}
			ok, err = m.db.Get(l)
			if err != nil {
				return err
			}
			if !ok {
				logger.Warnf("no link target for inode %d", inode)
				return nil
			}
			e.Symlink = l.Target
		}

		return nil
	})
}
func (m *dbMeta) dumpEntryFast(inode Ino) *DumpedEntry {
	e := &DumpedEntry{}
	n, ok := m.snap.node[inode]
	if !ok {
		if inode != TrashInode {
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
		}
		return nil
	}
	attr := &Attr{}
	m.parseAttr(n, attr)
	e.Attr = dumpAttr(attr)
	e.Attr.Inode = inode

	rows, ok := m.snap.xattr[inode]
	if ok && len(rows) > 0 {
		xattrs := make([]*DumpedXattr, 0, len(rows))
		for _, x := range rows {
			xattrs = append(xattrs, &DumpedXattr{x.Name, string(x.Value)})
		}
		sort.Slice(xattrs, func(i, j int) bool { return xattrs[i].Name < xattrs[j].Name })
		e.Xattrs = xattrs
	}

	if attr.Typ == TypeFile {
		for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
			c, ok := m.snap.chunk[fmt.Sprintf("%d-%d", inode, indx)]
			if !ok {
				logger.Warnf("no found chunk target for inode %d indx %d", inode, indx)
				return nil
			}
			ss := readSliceBuf(c.Slices)
			slices := make([]*DumpedSlice, 0, len(ss))
			for _, s := range ss {
				slices = append(slices, &DumpedSlice{Chunkid: s.chunkid, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
			}
			e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
		}
	} else if attr.Typ == TypeSymlink {
		l, ok := m.snap.symlink[inode]
		if !ok {
			logger.Warnf("no link target for inode %d", inode)
			return nil
		}
		e.Symlink = l.Target
	}
	return e
}

func (m *dbMeta) dumpDir(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	var edges []*edge
	var err error
	var ok bool
	if m.snap != nil {
		edges, ok = m.snap.edges[inode]
		if !ok {
			logger.Warnf("no edge target for inode %d", inode)
		}
	} else {
		if err := m.db.Find(&edges, &edge{Parent: inode}); err != nil {
			return err
		}
	}

	if showProgress != nil {
		showProgress(int64(len(edges)), 0)
	}
	if err := tree.writeJsonWithOutEntry(bw, depth); err != nil {
		return err
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].Name < edges[j].Name })

	for idx, e := range edges {
		var entry *DumpedEntry
		if m.snap != nil {
			entry = m.dumpEntryFast(e.Inode)
		} else {
			entry, err = m.dumpEntry(e.Inode)
			if err != nil {
				return err
			}
		}

		if entry == nil {
			continue
		}

		entry.Name = e.Name
		if e.Type == TypeDirectory {
			err = m.dumpDir(e.Inode, entry, bw, depth+2, showProgress)
		} else {
			err = entry.writeJSON(bw, depth+2)
		}
		if err != nil {
			return err
		}
		if idx != len(edges)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

func (m *dbMeta) makeSnap(bar *utils.Bar) error {
	m.snap = &dbSnap{
		node:    make(map[Ino]*node),
		symlink: make(map[Ino]*symlink),
		xattr:   make(map[Ino][]*xattr),
		edges:   make(map[Ino][]*edge),
		chunk:   make(map[string]*chunk),
	}

	for _, s := range []interface{}{new(node), new(symlink), new(edge), new(xattr), new(chunk)} {
		if count, err := m.db.Count(s); err == nil {
			bar.IncrTotal(count)
		} else {
			return err
		}
	}

	bufferSize := 10000
	if err := m.db.BufferSize(bufferSize).Iterate(new(node), func(idx int, bean interface{}) error {
		n := bean.(*node)
		m.snap.node[n.Inode] = n
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := m.db.BufferSize(bufferSize).Iterate(new(symlink), func(idx int, bean interface{}) error {
		s := bean.(*symlink)
		m.snap.symlink[s.Inode] = s
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}
	if err := m.db.BufferSize(bufferSize).Iterate(new(edge), func(idx int, bean interface{}) error {
		e := bean.(*edge)
		m.snap.edges[e.Parent] = append(m.snap.edges[e.Parent], e)
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := m.db.BufferSize(bufferSize).Iterate(new(xattr), func(idx int, bean interface{}) error {
		x := bean.(*xattr)
		m.snap.xattr[x.Inode] = append(m.snap.xattr[x.Inode], x)
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := m.db.BufferSize(bufferSize).Iterate(new(chunk), func(idx int, bean interface{}) error {
		c := bean.(*chunk)
		m.snap.chunk[fmt.Sprintf("%d-%d", c.Inode, c.Indx)] = c
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (m *dbMeta) DumpMeta(w io.Writer, root Ino) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("DumpMeta error: %v", p)
			}
		}
	}()
	var drows []delfile
	if err := m.db.Find(&drows); err != nil {
		return err
	}
	dels := make([]*DumpedDelFile, 0, len(drows))
	for _, row := range drows {
		dels = append(dels, &DumpedDelFile{row.Inode, row.Length, row.Expire})
	}

	progress := utils.NewProgress(false, false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)
	if root == 1 {
		bar := progress.AddCountBar("Snapshot keys", 0)
		if err = m.makeSnap(bar); err != nil {
			return fmt.Errorf("Fetch all metadata from DB: %s", err)
		}
		bar.Done()
		tree = m.dumpEntryFast(root)
		trash = m.dumpEntryFast(TrashInode)
	} else {
		if tree, err = m.dumpEntry(root); err != nil {
			return err
		}
	}
	if tree == nil {
		return errors.New("The entry of the root inode was not found")
	}
	tree.Name = "FSTree"
	format, err := m.Load()
	if err != nil {
		return err
	}

	var crows []counter
	if err = m.db.Find(&crows); err != nil {
		return err
	}
	counters := &DumpedCounters{}
	for _, row := range crows {
		switch row.Name {
		case "usedSpace":
			counters.UsedSpace = row.Value
		case "totalInodes":
			counters.UsedInodes = row.Value
		case "nextInode":
			counters.NextInode = row.Value
		case "nextChunk":
			counters.NextChunk = row.Value
		case "nextSession":
			counters.NextSession = row.Value
		case "nextTrash":
			counters.NextTrash = row.Value
		}
	}

	var srows []sustained
	if err = m.db.Find(&srows); err != nil {
		return err
	}
	ss := make(map[uint64][]Ino)
	for _, row := range srows {
		ss[row.Sid] = append(ss[row.Sid], row.Inode)
	}
	sessions := make([]*DumpedSustained, 0, len(ss))
	for k, v := range ss {
		sessions = append(sessions, &DumpedSustained{k, v})
	}

	dm := DumpedMeta{
		Setting:   format,
		Counters:  counters,
		Sustained: sessions,
		DelFiles:  dels,
	}

	bw, err := dm.writeJsonWithOutTree(w)
	if err != nil {
		return err
	}

	bar := progress.AddCountBar("Dumped entries", 1) // with root
	bar.Increment()
	if trash != nil {
		trash.Name = "Trash"
		bar.IncrTotal(1)
		bar.Increment()
	}
	showProgress := func(totalIncr, currentIncr int64) {
		bar.IncrTotal(totalIncr)
		bar.IncrInt64(currentIncr)
	}
	if err = m.dumpDir(root, tree, bw, 1, showProgress); err != nil {
		return err
	}
	if trash != nil {
		if _, err = bw.WriteString(","); err != nil {
			return err
		}
		if err = m.dumpDir(TrashInode, trash, bw, 1, showProgress); err != nil {
			return err
		}
	}
	if _, err = bw.WriteString("\n}\n"); err != nil {
		return err
	}
	progress.Done()
	m.snap = nil

	return bw.Flush()
}

func (m *dbMeta) loadEntry(e *DumpedEntry, cs *DumpedCounters, refs map[uint64]*chunkRef) error {
	inode := e.Attr.Inode
	logger.Debugf("Loading entry inode %d name %s", inode, e.Name)
	attr := e.Attr
	n := &node{
		Inode:  inode,
		Type:   typeFromString(attr.Type),
		Mode:   attr.Mode,
		Uid:    attr.Uid,
		Gid:    attr.Gid,
		Atime:  attr.Atime*1e6 + int64(attr.Atimensec)/1e3,
		Mtime:  attr.Mtime*1e6 + int64(attr.Atimensec)/1e3,
		Ctime:  attr.Ctime*1e6 + int64(attr.Atimensec)/1e3,
		Nlink:  attr.Nlink,
		Rdev:   attr.Rdev,
		Parent: e.Parent,
	} // Length not set
	var beans []interface{}
	if n.Type == TypeFile {
		n.Length = attr.Length
		chunks := make([]*chunk, 0, len(e.Chunks))
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]byte, 0, sliceBytes*len(c.Slices))
			for _, s := range c.Slices {
				slices = append(slices, marshalSlice(s.Pos, s.Chunkid, s.Size, s.Off, s.Len)...)
				m.Lock()
				if refs[s.Chunkid] == nil {
					refs[s.Chunkid] = &chunkRef{s.Chunkid, s.Size, 1}
				} else {
					refs[s.Chunkid].Refs++
				}
				m.Unlock()
				if cs.NextChunk <= int64(s.Chunkid) {
					cs.NextChunk = int64(s.Chunkid) + 1
				}
			}
			chunks = append(chunks, &chunk{inode, c.Index, slices})
		}
		if len(chunks) > 0 {
			beans = append(beans, chunks)
		}
	} else if n.Type == TypeDirectory {
		n.Length = 4 << 10
		if len(e.Entries) > 0 {
			edges := make([]*edge, 0, len(e.Entries))
			for _, c := range e.Entries {
				edges = append(edges, &edge{
					Parent: inode,
					Name:   c.Name,
					Inode:  c.Attr.Inode,
					Type:   typeFromString(c.Attr.Type),
				})
			}
			beans = append(beans, edges)
		}
	} else if n.Type == TypeSymlink {
		n.Length = uint64(len(e.Symlink))
		beans = append(beans, &symlink{inode, e.Symlink})
	}
	if inode > 1 && inode != TrashInode {
		cs.UsedSpace += align4K(n.Length)
		cs.UsedInodes += 1
	}
	if inode < TrashInode {
		if cs.NextInode <= int64(inode) {
			cs.NextInode = int64(inode) + 1
		}
	} else {
		if cs.NextTrash < int64(inode)-TrashInode {
			cs.NextTrash = int64(inode) - TrashInode
		}
	}

	if len(e.Xattrs) > 0 {
		xattrs := make([]*xattr, 0, len(e.Xattrs))
		for _, x := range e.Xattrs {
			xattrs = append(xattrs, &xattr{inode, x.Name, []byte(x.Value)})
		}
		beans = append(beans, xattrs)
	}
	beans = append(beans, n)
	s := m.db.NewSession()
	defer s.Close()
	return mustInsert(s, beans...)
}

func (m *dbMeta) LoadMeta(r io.Reader) error {
	tables, err := m.db.DBMetas()
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		return fmt.Errorf("Database %s is not empty", m.Name())
	}
	if err = m.db.Sync2(new(setting), new(counter)); err != nil {
		return fmt.Errorf("create table setting, counter: %s", err)
	}
	if err = m.db.Sync2(new(node), new(edge), new(symlink), new(xattr)); err != nil {
		return fmt.Errorf("create table node, edge, symlink, xattr: %s", err)
	}
	if err = m.db.Sync2(new(chunk), new(chunkRef)); err != nil {
		return fmt.Errorf("create table chunk, chunk_ref: %s", err)
	}
	if err = m.db.Sync2(new(session), new(sustained), new(delfile)); err != nil {
		return fmt.Errorf("create table session, sustaind, delfile: %s", err)
	}
	if err = m.db.Sync2(new(flock), new(plock)); err != nil {
		return fmt.Errorf("create table flock, plock: %s", err)
	}

	dec := json.NewDecoder(r)
	dm := &DumpedMeta{}
	if err = dec.Decode(dm); err != nil {
		return err
	}
	format, err := json.MarshalIndent(dm.Setting, "", "")
	if err != nil {
		return err
	}

	progress := utils.NewProgress(false, false)
	bar := progress.AddCountBar("Collected entries", 1) // with root
	showProgress := func(totalIncr, currentIncr int64) {
		bar.IncrTotal(totalIncr)
		bar.IncrInt64(currentIncr)
	}
	dm.FSTree.Attr.Inode = 1
	entries := make(map[Ino]*DumpedEntry)
	if err = collectEntry(dm.FSTree, entries, showProgress); err != nil {
		return err
	}
	if dm.Trash != nil {
		bar.IncrTotal(1)
		if err = collectEntry(dm.Trash, entries, showProgress); err != nil {
			return err
		}
	}
	bar.Done()

	counters := &DumpedCounters{
		NextInode: 2,
		NextChunk: 1,
	}
	refs := make(map[uint64]*chunkRef)
	bar = progress.AddCountBar("Loaded entries", int64(len(entries)))
	maxNum := 100
	pool := make(chan struct{}, maxNum)
	errCh := make(chan error, 100)
	done := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for _, entry := range entries {
		select {
		case err = <-errCh:
			return err
		default:
		}
		pool <- struct{}{}
		wg.Add(1)
		go func(entry *DumpedEntry) {
			defer func() {
				bar.Increment()
				wg.Done()
				<-pool
			}()
			if err = m.loadEntry(entry, counters, refs); err != nil {
				errCh <- err
			}
		}(entry)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err = <-errCh:
		return err
	case <-done:
	}
	progress.Done()
	logger.Infof("Dumped counters: %+v", *dm.Counters)
	logger.Infof("Loaded counters: %+v", *counters)

	beans := make([]interface{}, 0, 4) // setting, counter, delfile, chunkRef
	beans = append(beans, &setting{"format", string(format)})
	cs := make([]*counter, 0, 7)
	cs = append(cs, &counter{"usedSpace", counters.UsedSpace})
	cs = append(cs, &counter{"totalInodes", counters.UsedInodes})
	cs = append(cs, &counter{"nextInode", counters.NextInode})
	cs = append(cs, &counter{"nextChunk", counters.NextChunk})
	cs = append(cs, &counter{"nextSession", counters.NextSession})
	cs = append(cs, &counter{"nextTrash", counters.NextTrash})
	cs = append(cs, &counter{"nextCleanupSlices", 0})
	beans = append(beans, cs)
	if len(dm.DelFiles) > 0 {
		dels := make([]*delfile, 0, len(dm.DelFiles))
		for _, d := range dm.DelFiles {
			dels = append(dels, &delfile{d.Inode, d.Length, d.Expire})
		}
		beans = append(beans, dels)
	}
	if len(refs) > 0 {
		cks := make([]*chunkRef, 0, len(refs))
		for _, v := range refs {
			cks = append(cks, v)
		}
		beans = append(beans, cks)
	}
	s := m.db.NewSession()
	defer s.Close()
	return mustInsert(s, beans...)
}
