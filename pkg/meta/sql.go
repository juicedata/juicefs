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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/sirupsen/logrus"
	"xorm.io/xorm"
	"xorm.io/xorm/log"
	"xorm.io/xorm/names"
)

const MaxFieldsCountOfTable = 13 // node table

type setting struct {
	Name  string `xorm:"pk"`
	Value string `xorm:"varchar(4096) notnull"`
}

type counter struct {
	Name  string `xorm:"pk"`
	Value int64  `xorm:"notnull"`
}

type edge struct {
	Id     int64  `xorm:"pk bigserial"`
	Parent Ino    `xorm:"unique(edge) notnull"`
	Name   []byte `xorm:"unique(edge) varbinary(255) notnull"`
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
	Name []byte `xorm:"varbinary(255)"`
}

type chunk struct {
	Id     int64  `xorm:"pk bigserial"`
	Inode  Ino    `xorm:"unique(chunk) notnull"`
	Indx   uint32 `xorm:"unique(chunk) notnull"`
	Slices []byte `xorm:"blob notnull"`
}

type chunkRef struct {
	Chunkid uint64 `xorm:"pk"`
	Size    uint32 `xorm:"notnull"`
	Refs    int    `xorm:"notnull"`
}

type delslices struct {
	Chunkid uint64 `xorm:"pk"`
	Deleted int64  `xorm:"notnull"` // timestamp
	Slices  []byte `xorm:"blob notnull"`
}

type symlink struct {
	Inode  Ino    `xorm:"pk"`
	Target []byte `xorm:"varbinary(4096) notnull"`
}

type xattr struct {
	Id    int64  `xorm:"pk bigserial"`
	Inode Ino    `xorm:"unique(name) notnull"`
	Name  string `xorm:"unique(name) notnull"`
	Value []byte `xorm:"blob notnull"`
}

type flock struct {
	Id    int64  `xorm:"pk bigserial"`
	Inode Ino    `xorm:"notnull unique(flock)"`
	Sid   uint64 `xorm:"notnull unique(flock)"`
	Owner int64  `xorm:"notnull unique(flock)"`
	Ltype byte   `xorm:"notnull"`
}

type plock struct {
	Id      int64  `xorm:"pk bigserial"`
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

type session2 struct {
	Sid    uint64 `xorm:"pk"`
	Expire int64  `xorm:"notnull"`
	Info   []byte `xorm:"blob"`
}

type sustained struct {
	Id    int64  `xorm:"pk bigserial"`
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
	if time.Since(start) > time.Millisecond*5 {
		logger.Warnf("The latency to database is too high: %s", time.Since(start))
	}

	engine.DB().SetMaxIdleConns(runtime.NumCPU() * 2)
	engine.DB().SetConnMaxIdleTime(time.Minute * 5)
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
	return m.txn(func(s *xorm.Session) error {
		_, err := s.Exec("delete from jfs_chunk_ref where chunkid=?", chunkid)
		return err
	})
}

func (m *dbMeta) Init(format Format, force bool) error {
	if err := m.db.Sync2(new(setting), new(counter)); err != nil {
		return fmt.Errorf("create table setting, counter: %s", err)
	}
	if err := m.db.Sync2(new(edge)); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return fmt.Errorf("create table edge: %s", err)
	}
	if err := m.db.Sync2(new(node), new(symlink), new(xattr)); err != nil {
		return fmt.Errorf("create table node, symlink, xattr: %s", err)
	}
	if err := m.db.Sync2(new(chunk), new(chunkRef), new(delslices)); err != nil {
		return fmt.Errorf("create table chunk, chunk_ref, delslices: %s", err)
	}
	if err := m.db.Sync2(new(session2), new(sustained), new(delfile)); err != nil {
		return fmt.Errorf("create table session2, sustaind, delfile: %s", err)
	}
	if err := m.db.Sync2(new(flock), new(plock)); err != nil {
		return fmt.Errorf("create table flock, plock: %s", err)
	}

	var s = setting{Name: "format"}
	var ok bool
	err := m.roTxn(func(ses *xorm.Session) (err error) {
		ok, err = ses.Get(&s)
		return err
	})
	if err != nil {
		return err
	}

	if ok {
		var old Format
		err = json.Unmarshal([]byte(s.Value), &old)
		if err != nil {
			return fmt.Errorf("json: %s", err)
		}
		if err = format.update(&old, force); err != nil {
			return err
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
			ok2, err := s.ForUpdate().Get(&node{Inode: TrashInode})
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
		&chunk{}, &chunkRef{}, &delslices{},
		&session{}, &session2{}, &sustained{}, &delfile{},
		&flock{}, &plock{})
}

func (m *dbMeta) doLoad() (data []byte, err error) {
	err = m.roTxn(func(ses *xorm.Session) error {
		if ok, err := ses.IsTableExist(&setting{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		s := setting{Name: "format"}
		ok, err := ses.Get(&s)
		if err == nil && ok {
			data = []byte(s.Value)
		}
		return err
	})
	return
}

func (m *dbMeta) doNewSession(sinfo []byte) error {
	err := m.db.Sync2(new(session2), new(delslices))
	if err != nil {
		return fmt.Errorf("update table session2, delslices: %s", err)
	}
	// add primary key
	if err = m.db.Sync2(new(edge), new(chunk), new(xattr), new(sustained)); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return fmt.Errorf("update table edge, chunk, xattr, sustained: %s", err)
	}
	// update the owner from uint64 to int64
	if err = m.db.Sync2(new(flock), new(plock)); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return fmt.Errorf("update table flock, plock: %s", err)
	}

	for {
		if err = m.txn(func(s *xorm.Session) error {
			return mustInsert(s, &session2{m.sid, m.expireTime(), sinfo})
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

func (m *dbMeta) getSession(row interface{}, detail bool) (*Session, error) {
	var s Session
	var info []byte
	switch row := row.(type) {
	case *session2:
		s.Sid = row.Sid
		s.Expire = time.Unix(row.Expire, 0)
		info = row.Info
	case *session:
		s.Sid = row.Sid
		s.Expire = time.Unix(row.Heartbeat, 0).Add(time.Minute * 5)
		info = row.Info
		if info == nil { // legacy client has no info
			info = []byte("{}")
		}
	default:
		return nil, fmt.Errorf("invalid type: %T", row)
	}
	if err := json.Unmarshal(info, &s); err != nil {
		return nil, fmt.Errorf("corrupted session info; json error: %s", err)
	}
	if detail {
		var (
			srows []sustained
			frows []flock
			prows []plock
		)
		err := m.roTxn(func(ses *xorm.Session) error {
			if err := ses.Find(&srows, &sustained{Sid: s.Sid}); err != nil {
				return fmt.Errorf("find sustained %d: %s", s.Sid, err)
			}
			s.Sustained = make([]Ino, 0, len(srows))
			for _, srow := range srows {
				s.Sustained = append(s.Sustained, srow.Inode)
			}

			if err := ses.Find(&frows, &flock{Sid: s.Sid}); err != nil {
				return fmt.Errorf("find flock %d: %s", s.Sid, err)
			}
			s.Flocks = make([]Flock, 0, len(frows))
			for _, frow := range frows {
				s.Flocks = append(s.Flocks, Flock{frow.Inode, uint64(frow.Owner), string(frow.Ltype)})
			}

			if err := ses.Find(&prows, &plock{Sid: s.Sid}); err != nil {
				return fmt.Errorf("find plock %d: %s", s.Sid, err)
			}
			s.Plocks = make([]Plock, 0, len(prows))
			for _, prow := range prows {
				s.Plocks = append(s.Plocks, Plock{prow.Inode, uint64(prow.Owner), prow.Records})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func (m *dbMeta) GetSession(sid uint64, detail bool) (s *Session, err error) {
	err = m.roTxn(func(ses *xorm.Session) error {
		row := session2{Sid: sid}
		if ok, err := ses.Get(&row); err != nil {
			return err
		} else if ok {
			s, err = m.getSession(&row, detail)
			return err
		}
		if ok, err := ses.IsTableExist(&session{}); err != nil {
			return err
		} else if ok {
			row := session{Sid: sid}
			if ok, err := ses.Get(&row); err != nil {
				return err
			} else if ok {
				s, err = m.getSession(&row, detail)
				return err
			}
		}
		return fmt.Errorf("session not found: %d", sid)
	})
	return
}

func (m *dbMeta) ListSessions() ([]*Session, error) {
	var sessions []*Session
	err := m.roTxn(func(ses *xorm.Session) error {
		var rows []session2
		err := ses.Find(&rows)
		if err != nil {
			return err
		}
		sessions = make([]*Session, 0, len(rows))
		for i := range rows {
			s, err := m.getSession(&rows[i], false)
			if err != nil {
				logger.Errorf("get session: %s", err)
				continue
			}
			sessions = append(sessions, s)
		}
		if ok, err := ses.IsTableExist(&session{}); err != nil {
			logger.Errorf("Check legacy session table: %s", err)
		} else if ok {
			var lrows []session
			if err = ses.Find(&lrows); err != nil {
				logger.Errorf("Scan legacy sessions: %s", err)
				return nil
			}
			for i := range lrows {
				s, err := m.getSession(&lrows[i], false)
				if err != nil {
					logger.Errorf("Get legacy session: %s", err)
					continue
				}
				sessions = append(sessions, s)
			}
		}
		return nil
	})
	return sessions, err
}

func (m *dbMeta) getCounter(name string) (v int64, err error) {
	err = m.roTxn(func(s *xorm.Session) error {
		c := counter{Name: name}
		_, err := s.Get(&c)
		if err == nil {
			v = c.Value
		}
		return err
	})
	return
}

func (m *dbMeta) incrCounter(name string, value int64) (int64, error) {
	var v int64
	err := m.txn(func(s *xorm.Session) error {
		var c = counter{Name: name}
		ok, err := s.ForUpdate().Get(&c)
		if err != nil {
			return err
		}
		v = c.Value + value
		if value > 0 {
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
	var changed bool
	err := m.txn(func(s *xorm.Session) error {
		changed = false
		c := counter{Name: name}
		ok, err := s.ForUpdate().Get(&c)
		if err != nil {
			return err
		}
		if c.Value > value-diff {
			return nil
		} else {
			changed = true
			c.Value = value
			if ok {
				_, err = s.Cols("value").Update(&c, &counter{Name: name})
			} else {
				err = mustInsert(s, &c)
			}
			return err
		}
	})

	return changed, err
}

func mustInsert(s *xorm.Session, beans ...interface{}) error {
	for start, end, size := 0, 0, len(beans); end < size; start = end {
		end = start + 200
		if end > size {
			end = size
		}
		if n, err := s.Insert(beans[start:end]...); err != nil {
			return err
		} else if d := end - start - int(n); d > 0 {
			return fmt.Errorf("%d records not inserted: %+v", d, beans[start:end])
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
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "too many connections") || strings.Contains(msg, "too many clients") {
		logger.Warnf("transaction failed: %s, will retry it. please increase the max number of connections in your database, or use a connection pool.", msg)
		return true
	}
	switch m.db.DriverName() {
	case "sqlite3":
		return errors.Is(err, errBusy) || strings.Contains(msg, "database is locked")
	case "mysql":
		// MySQL, MariaDB or TiDB
		return strings.Contains(msg, "try restarting transaction") || strings.Contains(msg, "try again later") ||
			strings.Contains(msg, "Duplicate entry")
	case "postgres":
		return strings.Contains(msg, "current transaction is aborted") || strings.Contains(msg, "deadlock detected") ||
			strings.Contains(msg, "duplicate key value") || strings.Contains(msg, "could not serialize access")
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
			return nil, f(s)
		})
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if m.shouldRetry(err) {
			txRestart.Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", err)
	return err
}

func (m *dbMeta) roTxn(f func(s *xorm.Session) error) error {
	start := time.Now()
	defer func() { txDist.Observe(time.Since(start).Seconds()) }()
	var err error
	s := m.db.NewSession()
	defer s.Close()
	for i := 0; i < 50; i++ {
		// TODO: read-only
		if err := s.Begin(); err != nil {
			logger.Debugf("Start transaction failed, try again (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		err = f(s)
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		_ = s.Rollback()
		if m.shouldRetry(err) {
			txRestart.Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", err)
	return err
}

func (m *dbMeta) parseAttr(n *node, attr *Attr) {
	if attr == nil || n == nil {
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
	return errno(m.roTxn(func(s *xorm.Session) error {
		s = s.Table(&edge{})
		if attr != nil {
			s = s.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
		}
		nn := namedNode{node: node{Parent: parent}, Name: []byte(name)}
		exist, err := s.Select("*").Get(&nn)
		if err != nil {
			return err
		}
		if !exist {
			return syscall.ENOENT
		}
		*inode = nn.Inode
		m.parseAttr(&nn.node, attr)
		return nil
	}))
}

func (m *dbMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return errno(m.roTxn(func(s *xorm.Session) error {
		var n = node{Inode: inode}
		ok, err := s.Get(&n)
		if ok {
			m.parseAttr(&n, attr)
		} else if err == nil {
			err = syscall.ENOENT
		}
		return err
	}))
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
		ok, err := s.ForUpdate().Get(&cur)
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
			err = mustInsert(s, &chunk{Inode: inode, Indx: indx, Slices: buf})
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
		ok, err := s.ForUpdate().Get(&n)
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
		var zeroChunks []chunk
		var left, right = n.Length, length
		if left > right {
			right, left = left, right
		}
		if right/ChunkSize-left/ChunkSize > 1 {
			err := s.Where("inode = ? AND indx > ? AND indx < ?", inode, left/ChunkSize, right/ChunkSize).Cols("indx").ForUpdate().Find(&zeroChunks)
			if err != nil {
				return err
			}
		}

		l := uint32(right - left)
		if right > (left/ChunkSize+1)*ChunkSize {
			l = ChunkSize - uint32(left%ChunkSize)
		}
		if err = m.appendSlice(s, inode, uint32(left/ChunkSize), marshalSlice(uint32(left%ChunkSize), 0, 0, 0, l)); err != nil {
			return err
		}
		buf := marshalSlice(0, 0, 0, 0, ChunkSize)
		for _, c := range zeroChunks {
			if err = m.appendSlice(s, inode, c.Indx, buf); err != nil {
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
		ok, err := s.ForUpdate().Get(&n)
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
		if newSpace > 0 && m.checkQuota(newSpace, 0) {
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

func (m *dbMeta) doReadlink(ctx Context, inode Ino) (target []byte, err error) {
	err = m.roTxn(func(s *xorm.Session) error {
		var l = symlink{Inode: inode}
		ok, err := s.Get(&l)
		if err == nil && ok {
			target = []byte(l.Target)
		}
		return err
	})
	return
}

func (m *dbMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
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
		ok, err := s.ForUpdate().Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.ForUpdate().Get(&e)
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
				ok, err = s.ForUpdate().Get(&foundNode)
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

		var updateParent bool
		now := time.Now().UnixNano() / 1e3
		if parent != TrashInode {
			if _type == TypeDirectory {
				pn.Nlink++
				updateParent = true
			}
			if updateParent || time.Duration(now-pn.Mtime)*1e3 >= minUpdateTime {
				pn.Mtime = now
				pn.Ctime = now
				updateParent = true
			}
		}
		n.Atime = now
		n.Mtime = now
		n.Ctime = now
		if pn.Mode&02000 != 0 || ctx.Value(CtxKey("behavior")) == "Hadoop" || runtime.GOOS == "darwin" {
			n.Gid = pn.Gid
			if _type == TypeDirectory && runtime.GOOS == "linux" {
				n.Mode |= pn.Mode & 02000
			}
		}

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: ino, Type: _type}, &n); err != nil {
			return err
		}
		if updateParent {
			if _, err := s.Cols("nlink", "mtime", "ctime").Update(&pn, &node{Inode: pn.Inode}); err != nil {
				return err
			}
		}
		if _type == TypeSymlink {
			if err = mustInsert(s, &symlink{Inode: ino, Target: []byte(path)}); err != nil {
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
		ok, err := s.ForUpdate().Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.ForUpdate().Get(&e)
		if err != nil {
			return err
		}
		if !ok && m.conf.CaseInsensi {
			if ee := m.resolveCase(ctx, parent, name); ee != nil {
				ok = true
				e.Name = ee.Name
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
		ok, err = s.ForUpdate().Get(&n)
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

		var updateParent bool
		if !isTrash(parent) && time.Duration(now-pn.Mtime)*1e3 >= minUpdateTime {
			pn.Mtime = now
			pn.Ctime = now
			updateParent = true
		}

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if updateParent {
			if _, err = s.Cols("mtime", "ctime").Update(&pn, &node{Inode: pn.Inode}); err != nil {
				return err
			}
		}
		if n.Nlink > 0 {
			if _, err := s.Cols("nlink", "ctime", "parent").Update(&n, &node{Inode: e.Inode}); err != nil {
				return err
			}
			if trash > 0 {
				if err = mustInsert(s, &edge{Parent: trash, Name: []byte(m.trashEntry(parent, e.Inode, string(e.Name))), Inode: e.Inode, Type: e.Type}); err != nil {
					return err
				}
			}
		} else {
			switch e.Type {
			case TypeFile:
				if opened {
					if err = mustInsert(s, sustained{Sid: m.sid, Inode: e.Inode}); err != nil {
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
		ok, err := s.ForUpdate().Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.ForUpdate().Get(&e)
		if err != nil {
			return err
		}
		if !ok && m.conf.CaseInsensi {
			if ee := m.resolveCase(ctx, parent, name); ee != nil {
				ok = true
				e.Inode = ee.Inode
				e.Name = ee.Name
				e.Type = ee.Attr.Typ
			}
		}
		if !ok {
			return syscall.ENOENT
		}
		if e.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var n = node{Inode: e.Inode}
		ok, err = s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		exist, err := s.ForUpdate().Exist(&edge{Parent: e.Inode})
		if err != nil {
			return err
		}
		if exist {
			return syscall.ENOTEMPTY
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
			if _, err = s.Cols("ctime", "parent").Update(&n, &node{Inode: n.Inode}); err != nil {
				return err
			}
			if err = mustInsert(s, &edge{Parent: trash, Name: []byte(m.trashEntry(parent, e.Inode, string(e.Name))), Inode: e.Inode, Type: e.Type}); err != nil {
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
		if !isTrash(parent) {
			_, err = s.Cols("nlink", "mtime", "ctime").Update(&pn, &node{Inode: pn.Inode})
		}
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
		var t node
		rows, err := s.ForUpdate().Where("inode IN (?,?)", parentSrc, parentDst).Rows(&t)
		if err != nil {
			return err
		}
		defer rows.Close()
		var spn, dpn node
		for rows.Next() {
			if err := rows.Scan(&t); err != nil {
				return err
			}
			if t.Inode == parentSrc {
				spn = t
			}
			if t.Inode == parentDst {
				dpn = t
			}
		}
		if spn.Inode == 0 || dpn.Inode == 0 {
			return syscall.ENOENT
		}
		if spn.Type != TypeDirectory || dpn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var se = edge{Parent: parentSrc, Name: []byte(nameSrc)}
		ok, err := s.ForUpdate().Get(&se)
		if err != nil {
			return err
		}
		if !ok && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentSrc, nameSrc); e != nil {
				ok = true
				se.Inode = e.Inode
				se.Type = e.Attr.Typ
				se.Name = e.Name
			}
		}
		if !ok {
			return syscall.ENOENT
		}
		if parentSrc == parentDst && string(se.Name) == nameDst {
			if inode != nil {
				*inode = se.Inode
			}
			return nil
		}
		var sn = node{Inode: se.Inode}
		ok, err = s.ForUpdate().Get(&sn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}

		var de = edge{Parent: parentDst, Name: []byte(nameDst)}
		ok, err = s.ForUpdate().Get(&de)
		if err != nil {
			return err
		}
		if !ok && m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parentDst, nameDst); e != nil {
				ok = true
				de.Inode = e.Inode
				de.Type = e.Attr.Typ
				de.Name = e.Name
			}
		}
		var supdate, dupdate bool
		now := time.Now().UnixNano() / 1e3
		opened = false
		dn = node{Inode: de.Inode}
		if ok {
			if flags == RenameNoReplace {
				return syscall.EEXIST
			}
			dino = de.Inode
			ok, err := s.ForUpdate().Get(&dn)
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
					supdate, dupdate = true, true
				}
			} else {
				if de.Type == TypeDirectory {
					exist, err := s.ForUpdate().Exist(&edge{Parent: de.Inode})
					if err != nil {
						return err
					}
					if exist {
						return syscall.ENOTEMPTY
					}
					dpn.Nlink--
					dupdate = true
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

		if se.Type == TypeDirectory && parentSrc != parentDst {
			spn.Nlink--
			dpn.Nlink++
			supdate, dupdate = true, true
		}
		if supdate || time.Duration(now-spn.Mtime)*1e3 >= minUpdateTime {
			spn.Mtime = now
			spn.Ctime = now
			supdate = true
		}
		if dupdate || time.Duration(now-dpn.Mtime)*1e3 >= minUpdateTime {
			dpn.Mtime = now
			dpn.Ctime = now
			dupdate = true
		}
		sn.Parent = parentDst
		sn.Ctime = now
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
					name := m.trashEntry(parentDst, dino, string(de.Name))
					if err = mustInsert(s, &edge{Parent: trash, Name: []byte(name), Inode: dino, Type: de.Type}); err != nil {
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
							if err = mustInsert(s, sustained{Sid: m.sid, Inode: dino}); err != nil {
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
			if err = mustInsert(s, &edge{Parent: parentDst, Name: de.Name, Inode: se.Inode, Type: se.Type}); err != nil {
				return err
			}
		}
		if parentDst != parentSrc && !isTrash(parentSrc) && supdate {
			if _, err := s.Cols("nlink", "mtime", "ctime").Update(&spn, &node{Inode: parentSrc}); err != nil {
				return err
			}
		}
		if _, err := s.Cols("ctime", "parent").Update(&sn, &node{Inode: sn.Inode}); err != nil {
			return err
		}
		if dupdate {
			if _, err := s.Cols("nlink", "mtime", "ctime").Update(&dpn, &node{Inode: parentDst}); err != nil {
				return err
			}
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
		ok, err := s.ForUpdate().Get(&pn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if pn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.ForUpdate().Get(&e)
		if err != nil {
			return err
		}
		if ok || !ok && m.conf.CaseInsensi && m.resolveCase(ctx, parent, name) != nil {
			return syscall.EEXIST
		}

		var n = node{Inode: inode}
		ok, err = s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type == TypeDirectory {
			return syscall.EPERM
		}

		var updateParent bool
		now := time.Now().UnixNano() / 1e3
		if time.Duration(now-pn.Mtime)*1e3 >= minUpdateTime {
			pn.Mtime = now
			pn.Ctime = now
			updateParent = true
		}
		n.Nlink++
		n.Ctime = now

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: inode, Type: n.Type}); err != nil {
			return err
		}
		if updateParent {
			if _, err := s.Cols("mtime", "ctime").Update(&pn, &node{Inode: parent}); err != nil {
				return err
			}
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

func (m *dbMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	return errno(m.roTxn(func(s *xorm.Session) error {
		s = s.Table(&edge{})
		if plus != 0 {
			s = s.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
		}
		if limit > 0 {
			s = s.Limit(limit, 0)
		}
		var nodes []namedNode
		if err := s.Find(&nodes, &edge{Parent: inode}); err != nil {
			return err
		}
		for _, n := range nodes {
			if len(n.Name) == 0 {
				logger.Errorf("Corrupt entry with empty name: inode %d parent %d", n.Inode, inode)
				continue
			}
			entry := &Entry{
				Inode: n.Inode,
				Name:  n.Name,
				Attr:  &Attr{},
			}
			if plus != 0 {
				m.parseAttr(&n.node, entry.Attr)
			} else {
				entry.Attr.Typ = n.Type
			}
			*entries = append(*entries, entry)
		}
		return nil
	}))
}

func (m *dbMeta) doCleanStaleSession(sid uint64) error {
	var fail bool
	// release locks
	err := m.txn(func(s *xorm.Session) error {
		if _, err := s.Delete(flock{Sid: sid}); err != nil {
			return err
		}
		if _, err := s.Delete(plock{Sid: sid}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		logger.Warnf("Delete flock/plock with sid %d: %d", sid, err)
		fail = true
	}

	var sus []sustained
	err = m.roTxn(func(ses *xorm.Session) error {
		sus = nil
		return ses.Find(&sus, &sustained{Sid: sid})
	})
	if err != nil {
		logger.Warnf("Scan sustained with sid %d: %s", sid, err)
		fail = true
	} else {
		for _, su := range sus {
			if err = m.doDeleteSustainedInode(sid, su.Inode); err != nil {
				logger.Warnf("Delete sustained inode %d of sid %d: %s", su.Inode, sid, err)
				fail = true
			}
		}
	}

	if fail {
		return fmt.Errorf("failed to clean up sid %d", sid)
	} else {
		return m.txn(func(s *xorm.Session) error {
			if n, err := s.Delete(&session2{Sid: sid}); err != nil {
				return err
			} else if n == 1 {
				return nil
			}
			ok, err := s.IsTableExist(&session{})
			if err == nil && ok {
				_, err = s.Delete(&session{Sid: sid})
			}
			return err
		})
	}
}

func (m *dbMeta) doFindStaleSessions(limit int) ([]uint64, error) {
	var sids []uint64
	_ = m.roTxn(func(ses *xorm.Session) error {
		var ss []session2
		err := ses.Where("Expire < ?", time.Now().Unix()).Limit(limit, 0).Find(&ss)
		if err != nil {
			return err
		}
		for _, s := range ss {
			sids = append(sids, s.Sid)
		}
		return nil
	})

	limit -= len(sids)
	if limit <= 0 {
		return sids, nil
	}

	err := m.roTxn(func(ses *xorm.Session) error {
		if ok, err := ses.IsTableExist(&session{}); err != nil {
			return err
		} else if ok {
			var ls []session
			err := ses.Where("Heartbeat < ?", time.Now().Add(time.Minute*-5).Unix()).Limit(limit, 0).Find(&ls)
			if err != nil {
				return err
			}
			for _, l := range ls {
				sids = append(sids, l.Sid)
			}
		}
		return nil
	})
	if err != nil {
		logger.Errorf("Check legacy session table: %s", err)
	}

	return sids, nil
}

func (m *dbMeta) doRefreshSession() {
	_ = m.txn(func(ses *xorm.Session) error {
		n, err := ses.Cols("Expire").Update(&session2{Expire: m.expireTime()}, &session2{Sid: m.sid})
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
		ok, err := s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err = mustInsert(s, &delfile{inode, n.Length, time.Now().Unix()}); err != nil {
			return err
		}
		_, err = s.Delete(&sustained{Sid: sid, Inode: inode})
		if err != nil {
			return err
		}
		newSpace = -align4K(n.Length)
		_, err = s.Delete(&node{Inode: inode})
		return err
	})
	if err == nil {
		m.updateStats(newSpace, -1)
		m.tryDeleteFileData(inode, n.Length)
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
	err := m.roTxn(func(s *xorm.Session) error {
		_, err := s.Where("inode=? and indx=?", inode, indx).Get(&c)
		return err
	})
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
		ok, err := s.ForUpdate().Get(&n)
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
		ok, err = s.ForUpdate().Where("Inode = ? and Indx = ?", inode, indx).Get(&ck)
		if err != nil {
			return err
		}
		buf := marshalSlice(off, slice.Chunkid, slice.Size, slice.Off, slice.Len)
		if ok {
			if err := m.appendSlice(s, inode, indx, buf); err != nil {
				return err
			}
		} else {
			if err = mustInsert(s, &chunk{Inode: inode, Indx: indx, Slices: buf}); err != nil {
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
		var ts []node
		err := s.ForUpdate().Where("inode IN (?,?)", fin, fout).Find(&ts)
		if err != nil {
			return err
		}
		var nin, nout node
		for _, t := range ts {
			if t.Inode == fin {
				nin = t
			}
			if t.Inode == fout {
				nout = t
			}
		}

		if nin.Inode == 0 || nout.Inode == 0 {
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

		var cs []chunk
		err = s.Where("inode = ? AND indx >= ? AND indx <= ?", fin, offIn/ChunkSize, (offIn+size)/ChunkSize).ForUpdate().Find(&cs)
		if err != nil {
			return err
		}
		chunks := make(map[uint32][]*slice)
		for _, c := range cs {
			chunks[c.Indx] = readSliceBuf(c.Slices)
		}

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
	files := make(map[Ino]uint64)
	err := m.roTxn(func(s *xorm.Session) error {
		var ds []delfile
		err := s.Where("expire < ?", ts).Limit(limit, 0).Find(&ds)
		if err != nil {
			return err
		}
		for _, d := range ds {
			files[d.Inode] = d.Length
		}
		return nil
	})
	return files, err
}

func (m *dbMeta) doCleanupSlices() {
	var cks []chunkRef
	_ = m.roTxn(func(s *xorm.Session) error {
		cks = nil
		return s.Where("refs <= 0").Find(&cks)
	})
	for _, ck := range cks {
		m.deleteSlice(ck.Chunkid, ck.Size)
	}
}

func (m *dbMeta) deleteChunk(inode Ino, indx uint32) error {
	var c chunk
	var ss []*slice
	err := m.txn(func(s *xorm.Session) error {
		ok, err := s.ForUpdate().Where("inode = ? AND indx = ?", inode, indx).Get(&c)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		ss = readSliceBuf(c.Slices)
		for _, sc := range ss {
			_, err = s.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? AND size=?", sc.chunkid, sc.size)
			if err != nil {
				return err
			}
		}
		c.Slices = nil
		n, err := s.Where("inode = ? AND indx = ?", inode, indx).Delete(&c)
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
		err := m.roTxn(func(s *xorm.Session) error {
			ok, err := s.Get(&ref)
			if err == nil && !ok {
				err = errors.New("not found")
			}
			return err
		})
		if err == nil && ref.Refs <= 0 {
			m.deleteSlice(s.chunkid, s.size)
		}
	}
	return nil
}

func (m *dbMeta) doDeleteFileData(inode Ino, length uint64) {
	var indexes []chunk
	_ = m.roTxn(func(s *xorm.Session) error {
		indexes = nil
		return s.Cols("indx").Find(&indexes, &chunk{Inode: inode})
	})
	for _, c := range indexes {
		err := m.deleteChunk(inode, c.Indx)
		if err != nil {
			logger.Warnf("deleteChunk inode %d index %d error: %s", inode, c.Indx, err)
			return
		}
	}
	_ = m.txn(func(s *xorm.Session) error {
		_, err := s.Delete(delfile{Inode: inode})
		return err
	})
}

func (m *dbMeta) doCleanupDelayedSlices(edge int64, limit int) (int, error) {
	var result []delslices
	_ = m.roTxn(func(s *xorm.Session) error {
		result = nil
		return s.Where("deleted < ?", edge).Limit(limit, 0).Find(&result)
	})

	var count int
	var ss []Slice
	for _, ds := range result {
		if err := m.txn(func(ses *xorm.Session) error {
			ds := delslices{Chunkid: ds.Chunkid}
			if ok, e := ses.ForUpdate().Get(&ds); e != nil {
				return e
			} else if !ok {
				return nil
			}
			ss = ss[:0]
			m.decodeDelayedSlices(ds.Slices, &ss)
			if len(ss) == 0 {
				return fmt.Errorf("invalid value for delayed slices %d: %v", ds.Chunkid, ds.Slices)
			}
			for _, s := range ss {
				if _, e := ses.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s.Chunkid, s.Size); e != nil {
					return e
				}
			}
			_, e := ses.Delete(&delslices{Chunkid: ds.Chunkid})
			return e
		}); err != nil {
			logger.Warnf("Cleanup delayed slices %d: %s", ds.Chunkid, err)
			continue
		}
		for _, s := range ss {
			var ref = chunkRef{Chunkid: s.Chunkid}
			err := m.roTxn(func(s *xorm.Session) error {
				ok, err := s.Get(&ref)
				if err == nil && !ok {
					err = errors.New("not found")
				}
				return err
			})
			if err == nil && ref.Refs <= 0 {
				m.deleteSlice(s.Chunkid, s.Size)
				count++
			}
		}
		if count >= limit {
			break
		}
	}
	return count, nil
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
	err := m.roTxn(func(s *xorm.Session) error {
		_, err := s.Where("inode=? and indx=?", inode, indx).Get(&c)
		return err
	})
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
	var buf []byte
	trash := m.toTrash(0)
	if trash {
		for _, s := range ss {
			buf = append(buf, m.encodeDelayedSlice(s.chunkid, s.size)...)
		}
	}
	err = m.txn(func(s *xorm.Session) error {
		var c2 = chunk{Inode: inode}
		_, err := s.ForUpdate().Where("indx=?", indx).Get(&c2)
		if err != nil {
			return err
		}
		if len(c2.Slices) < len(c.Slices) || !bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(c.Slices), len(c2.Slices))
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped*sliceBytes], marshalSlice(pos, chunkid, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := s.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if err = mustInsert(s, chunkRef{chunkid, size, 1}); err != nil {
			return err
		}
		if trash {
			if err = mustInsert(s, &delslices{chunkid, time.Now().Unix(), buf}); err != nil {
				return err
			}
		} else {
			for _, s_ := range ss {
				if _, err := s.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s_.chunkid, s_.size); err != nil {
					return err
				}
			}
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if err != nil {
		var c = chunkRef{Chunkid: chunkid}
		var ok bool
		e := m.roTxn(func(s *xorm.Session) error {
			var e error
			ok, e = s.Get(&c)
			return e
		})
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
		if !trash {
			for _, s := range ss {
				var ref = chunkRef{Chunkid: s.chunkid}
				var ok bool
				err := m.roTxn(func(s *xorm.Session) error {
					var e error
					ok, e = s.Get(&ref)
					return e
				})
				if err == nil && ok && ref.Refs <= 0 {
					m.deleteSlice(s.chunkid, s.size)
				}
			}
		}
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}

	if force {
		m.compactChunk(inode, indx, force)
	} else {
		go func() {
			// wait for the current compaction to finish
			time.Sleep(time.Millisecond * 10)
			m.compactChunk(inode, indx, force)
		}()
	}
}

func dup(b []byte) []byte {
	r := make([]byte, len(b))
	copy(r, b)
	return r
}

func (m *dbMeta) CompactAll(ctx Context, bar *utils.Bar) syscall.Errno {
	var cs []chunk
	err := m.roTxn(func(s *xorm.Session) error {
		cs = nil
		return s.Where("length(slices) >= ?", sliceBytes*2).Cols("inode", "indx").Find(&cs)
	})
	if err != nil {
		return errno(err)
	}

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
	err := m.roTxn(func(s *xorm.Session) error {
		var cs []chunk
		err := s.Find(&cs)
		if err != nil {
			return err
		}
		for _, c := range cs {
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
		return nil
	})
	if err != nil {
		return errno(err)
	}
	if m.fmt.TrashDays == 0 {
		return 0
	}

	err = m.roTxn(func(s *xorm.Session) error {
		if ok, err := s.IsTableExist(&delslices{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		var dss []delslices
		err := s.Find(&dss)
		if err != nil {
			return err
		}
		var ss []Slice
		for _, ds := range dss {
			ss = ss[:0]
			m.decodeDelayedSlices(ds.Slices, &ss)
			if showProgress != nil {
				for range ss {
					showProgress()
				}
			}
			for _, s := range ss {
				if s.Chunkid > 0 {
					slices[1] = append(slices[1], s)
				}
			}
		}
		return nil
	})
	return errno(err)
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	return errno(m.roTxn(func(s *xorm.Session) error {
		var x = xattr{Inode: inode, Name: name}
		ok, err := s.Get(&x)
		if err != nil {
			return err
		}
		if !ok {
			return ENOATTR
		}
		*vbuff = x.Value
		return nil
	}))
}

func (m *dbMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	defer timeit(time.Now())
	inode = m.checkRoot(inode)
	return errno(m.roTxn(func(s *xorm.Session) error {
		var xs []xattr
		err := s.Where("inode = ?", inode).Find(&xs, &xattr{Inode: inode})
		if err != nil {
			return err
		}
		*names = nil
		for _, x := range xs {
			*names = append(*names, []byte(x.Name)...)
			*names = append(*names, 0)
		}
		return nil
	}))
}

func (m *dbMeta) doSetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		var k = &xattr{Inode: inode, Name: name}
		var x = xattr{Inode: inode, Name: name, Value: value}
		ok, err := s.ForUpdate().Get(k)
		if err != nil {
			return err
		}
		k.Value = nil
		switch flags {
		case XattrCreate:
			if ok {
				return syscall.EEXIST
			}
			err = mustInsert(s, &x)
		case XattrReplace:
			if !ok {
				return ENOATTR
			}
			_, err = s.Update(&x, k)
		default:
			if ok {
				_, err = s.Update(&x, k)
			} else {
				err = mustInsert(s, &x)
			}
		}
		return err
	}))
}

func (m *dbMeta) doRemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
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

func (m *dbMeta) dumpEntry(inode Ino, typ uint8) (*DumpedEntry, error) {
	e := &DumpedEntry{}
	return e, m.roTxn(func(s *xorm.Session) error {
		n := &node{Inode: inode}
		ok, err := s.Get(n)
		if err != nil {
			return err
		}
		attr := &Attr{Typ: typ, Nlink: 1}
		if !ok {
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
		} else {
			m.parseAttr(n, attr)
		}
		e.Attr = dumpAttr(attr)
		e.Attr.Inode = inode

		var rows []xattr
		if err = s.Find(&rows, &xattr{Inode: inode}); err != nil {
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
				if ok, err = s.Get(c); err != nil {
					return err
				}
				if !ok {
					logger.Warnf("no found chunk target for inode %d indx %d", inode, indx)
					break
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
			ok, err = s.Get(l)
			if err != nil {
				return err
			}
			if !ok {
				logger.Warnf("no link target for inode %d", inode)
			}
			e.Symlink = string(l.Target)
		}
		return nil
	})
}

func (m *dbMeta) dumpEntryFast(inode Ino, typ uint8) *DumpedEntry {
	e := &DumpedEntry{}
	n, ok := m.snap.node[inode]
	if !ok {
		if inode != TrashInode {
			logger.Warnf("The entry of the inode was not found. inode: %v", inode)
		}
	}

	attr := &Attr{Typ: typ, Nlink: 1}
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
				break
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
			l = &symlink{}
		}
		e.Symlink = string(l.Target)
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
	if m.snap != nil {
		edges = m.snap.edges[inode]
	} else {
		err := m.roTxn(func(s *xorm.Session) error {
			edges = nil
			return s.Find(&edges, &edge{Parent: inode})
		})
		if err != nil {
			return err
		}
	}

	if showProgress != nil {
		showProgress(int64(len(edges)), 0)
	}
	if err := tree.writeJsonWithOutEntry(bw, depth); err != nil {
		return err
	}

	sort.Slice(edges, func(i, j int) bool { return bytes.Compare(edges[i].Name, edges[j].Name) == -1 })

	for idx, e := range edges {
		var entry *DumpedEntry
		if m.snap != nil {
			entry = m.dumpEntryFast(e.Inode, e.Type)
		} else {
			entry, err = m.dumpEntry(e.Inode, e.Type)
			if err != nil {
				return err
			}
		}

		if entry == nil {
			continue
		}

		entry.Name = string(e.Name)
		if e.Type == TypeDirectory {
			logger.Infof("dump dir %d %s -> %d", inode, e.Name, e.Inode)
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
	return m.roTxn(func(ses *xorm.Session) error {
		snap := &dbSnap{
			node:    make(map[Ino]*node),
			symlink: make(map[Ino]*symlink),
			xattr:   make(map[Ino][]*xattr),
			edges:   make(map[Ino][]*edge),
			chunk:   make(map[string]*chunk),
		}

		for _, s := range []interface{}{new(node), new(symlink), new(edge), new(xattr), new(chunk)} {
			if count, err := ses.Count(s); err == nil {
				bar.IncrTotal(count)
			} else {
				return err
			}
		}

		bufferSize := 10000
		if err := ses.Table(&node{}).BufferSize(bufferSize).Iterate(new(node), func(idx int, bean interface{}) error {
			n := bean.(*node)
			snap.node[n.Inode] = n
			bar.Increment()
			return nil
		}); err != nil {
			return err
		}

		if err := ses.Table(&symlink{}).BufferSize(bufferSize).Iterate(new(symlink), func(idx int, bean interface{}) error {
			s := bean.(*symlink)
			snap.symlink[s.Inode] = s
			bar.Increment()
			return nil
		}); err != nil {
			return err
		}
		if err := ses.Table(&edge{}).BufferSize(bufferSize).Iterate(new(edge), func(idx int, bean interface{}) error {
			e := bean.(*edge)
			snap.edges[e.Parent] = append(snap.edges[e.Parent], e)
			bar.Increment()
			return nil
		}); err != nil {
			return err
		}

		if err := ses.Table(&xattr{}).BufferSize(bufferSize).Iterate(new(xattr), func(idx int, bean interface{}) error {
			x := bean.(*xattr)
			snap.xattr[x.Inode] = append(snap.xattr[x.Inode], x)
			bar.Increment()
			return nil
		}); err != nil {
			return err
		}

		if err := ses.Table(&chunk{}).BufferSize(bufferSize).Iterate(new(chunk), func(idx int, bean interface{}) error {
			c := bean.(*chunk)
			snap.chunk[fmt.Sprintf("%d-%d", c.Inode, c.Indx)] = c
			bar.Increment()
			return nil
		}); err != nil {
			return err
		}
		m.snap = snap
		return nil
	})
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

	progress := utils.NewProgress(false, false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)
	if root == 1 {
		defer func() { m.snap = nil }()
		bar := progress.AddCountBar("Snapshot keys", 0)
		if err = m.makeSnap(bar); err != nil {
			return fmt.Errorf("Fetch all metadata from DB: %s", err)
		}
		bar.Done()
		tree = m.dumpEntryFast(root, TypeDirectory)
		trash = m.dumpEntryFast(TrashInode, TypeDirectory)
	} else {
		if tree, err = m.dumpEntry(root, TypeDirectory); err != nil {
			return err
		}
	}
	if tree == nil {
		return errors.New("The entry of the root inode was not found")
	}
	tree.Name = "FSTree"

	var drows []delfile
	var crows []counter
	var srows []sustained
	err = m.roTxn(func(s *xorm.Session) error {
		drows = nil
		if err := s.Find(&drows); err != nil {
			return err
		}
		crows = nil
		if err = s.Find(&crows); err != nil {
			return err
		}
		srows = nil
		return s.Find(&srows)
	})
	if err != nil {
		return err
	}
	dels := make([]*DumpedDelFile, 0, len(drows))
	for _, row := range drows {
		dels = append(dels, &DumpedDelFile{row.Inode, row.Length, row.Expire})
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

	ss := make(map[uint64][]Ino)
	for _, row := range srows {
		ss[row.Sid] = append(ss[row.Sid], row.Inode)
	}
	sessions := make([]*DumpedSustained, 0, len(ss))
	for k, v := range ss {
		sessions = append(sessions, &DumpedSustained{k, v})
	}

	dm := DumpedMeta{
		Setting:   m.fmt,
		Counters:  counters,
		Sustained: sessions,
		DelFiles:  dels,
	}
	if dm.Setting.SecretKey != "" {
		dm.Setting.SecretKey = "removed"
		logger.Warnf("Secret key is removed for the sake of safety")
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

	return bw.Flush()
}

func (m *dbMeta) loadEntry(e *DumpedEntry, cs *DumpedCounters, refs map[uint64]*chunkRef, beansCh chan interface{}, bar *utils.Bar) {
	inode := e.Attr.Inode
	logger.Debugf("Loading entry inode %d name %s", inode, unescape(e.Name))
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
	if n.Type == TypeFile {
		n.Length = attr.Length
		bar.IncrTotal(int64(len(e.Chunks)))
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]byte, 0, sliceBytes*len(c.Slices))
			for _, s := range c.Slices {
				slices = append(slices, marshalSlice(s.Pos, s.Chunkid, s.Size, s.Off, s.Len)...)
				if refs[s.Chunkid] == nil {
					refs[s.Chunkid] = &chunkRef{s.Chunkid, s.Size, 1}
				} else {
					refs[s.Chunkid].Refs++
				}
				if cs.NextChunk <= int64(s.Chunkid) {
					cs.NextChunk = int64(s.Chunkid) + 1
				}
			}
			beansCh <- &chunk{Inode: inode, Indx: c.Index, Slices: slices}
		}
	} else if n.Type == TypeDirectory {
		n.Length = 4 << 10
		if len(e.Entries) > 0 {
			bar.IncrTotal(int64(len(e.Entries)))
			for _, c := range e.Entries {
				beansCh <- &edge{
					Parent: inode,
					Name:   unescape(c.Name),
					Inode:  c.Attr.Inode,
					Type:   typeFromString(c.Attr.Type),
				}
			}
		}
	} else if n.Type == TypeSymlink {
		symL := unescape(e.Symlink)
		n.Length = uint64(len(symL))
		bar.IncrTotal(1)
		beansCh <- &symlink{inode, symL}
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
		bar.IncrTotal(int64(len(e.Xattrs)))
		for _, x := range e.Xattrs {
			beansCh <- &xattr{Inode: inode, Name: x.Name, Value: unescape(x.Value)}
		}
	}
	beansCh <- n
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
	if err = m.db.Sync2(new(chunk), new(chunkRef), new(delslices)); err != nil {
		return fmt.Errorf("create table chunk, chunk_ref, delslices: %s", err)
	}
	if err = m.db.Sync2(new(session2), new(sustained), new(delfile)); err != nil {
		return fmt.Errorf("create table session2, sustaind, delfile: %s", err)
	}
	if err = m.db.Sync2(new(flock), new(plock)); err != nil {
		return fmt.Errorf("create table flock, plock: %s", err)
	}

	logger.Infoln("Reading file ...")
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
	cbar := progress.AddCountBar("Collected entries", 1) // with root
	showProgress := func(totalIncr, currentIncr int64) {
		cbar.IncrTotal(totalIncr)
		cbar.IncrInt64(currentIncr)
	}
	dm.FSTree.Attr.Inode = 1
	entries := make(map[Ino]*DumpedEntry)
	if err = collectEntry(dm.FSTree, entries, showProgress); err != nil {
		return err
	}
	if dm.Trash != nil {
		cbar.IncrTotal(1)
		if err = collectEntry(dm.Trash, entries, showProgress); err != nil {
			return err
		}
	}
	cbar.Done()

	counters := &DumpedCounters{
		NextInode: 2,
		NextChunk: 1,
	}
	refs := make(map[uint64]*chunkRef)

	var batchSize int
	switch m.db.DriverName() {
	case "sqlite3":
		batchSize = 999 / MaxFieldsCountOfTable
	case "mysql":
		batchSize = 65535 / MaxFieldsCountOfTable
	case "postgres":
		batchSize = 1000
	}
	beansCh := make(chan interface{}, batchSize*2)
	lbar := progress.AddCountBar("Loaded records", int64(len(entries)))
	go func() {
		defer close(beansCh)
		for _, entry := range entries {
			m.loadEntry(entry, counters, refs, beansCh, lbar)
		}
		lbar.IncrTotal(8)
		beansCh <- &setting{"format", string(format)}
		beansCh <- &counter{"usedSpace", counters.UsedSpace}
		beansCh <- &counter{"totalInodes", counters.UsedInodes}
		beansCh <- &counter{"nextInode", counters.NextInode}
		beansCh <- &counter{"nextChunk", counters.NextChunk}
		beansCh <- &counter{"nextSession", counters.NextSession}
		beansCh <- &counter{"nextTrash", counters.NextTrash}
		beansCh <- &counter{"nextCleanupSlices", 0}
		if len(dm.DelFiles) > 0 {
			lbar.IncrTotal(int64(len(dm.DelFiles)))
			for _, d := range dm.DelFiles {
				beansCh <- &delfile{d.Inode, d.Length, d.Expire}
			}
		}
		if len(refs) > 0 {
			lbar.IncrTotal(int64(len(refs)))
			for _, v := range refs {
				beansCh <- v
			}
		}
	}()

	chunkBatch := make([]interface{}, 0, batchSize)
	edgeBatch := make([]interface{}, 0, batchSize)
	xattrBatch := make([]interface{}, 0, batchSize)
	nodeBatch := make([]interface{}, 0, batchSize)
	chunkRefBatch := make([]interface{}, 0, batchSize)

	insertBatch := func(beanSlice []interface{}) error {
		var n int64
		err := m.txn(func(s *xorm.Session) error {
			var err error
			if n, err = s.Insert(beanSlice); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		} else if d := len(beanSlice) - int(n); d > 0 {
			return fmt.Errorf("%d records not inserted: %+v", d, beanSlice)
		}
		lbar.IncrInt64(n)
		return nil
	}

	addToBatch := func(batch *[]interface{}, bean interface{}) error {
		*batch = append(*batch, bean)
		if len(*batch) >= batchSize {
			if err := insertBatch(*batch); err != nil {
				return err
			}
			*batch = (*batch)[:0]
		}
		return nil
	}

	for bean := range beansCh {
		switch bean.(type) {
		case *chunk:
			if err := addToBatch(&chunkBatch, bean); err != nil {
				return err
			}
		case *edge:
			if err := addToBatch(&edgeBatch, bean); err != nil {
				return err
			}
		case *xattr:
			if err := addToBatch(&xattrBatch, bean); err != nil {
				return err
			}
		case *node:
			if err := addToBatch(&nodeBatch, bean); err != nil {
				return err
			}
		case *chunkRef:
			if err := addToBatch(&chunkRefBatch, bean); err != nil {
				return err
			}
		default:
			if err := insertBatch([]interface{}{bean}); err != nil {
				return err
			}
		}
	}
	for _, one := range [][]interface{}{chunkBatch, edgeBatch, xattrBatch, nodeBatch, chunkRefBatch} {
		if len(one) > 0 {
			if err := insertBatch(one); err != nil {
				return err
			}
		}
	}

	progress.Done()
	logger.Infof("Dumped counters: %+v", *dm.Counters)
	logger.Infof("Loaded counters: %+v", *counters)
	return nil

}
