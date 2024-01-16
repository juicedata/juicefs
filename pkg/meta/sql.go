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
	"fmt"
	"io"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"xorm.io/xorm"
	"xorm.io/xorm/log"
	"xorm.io/xorm/names"
)

const MaxFieldsCountOfTable = 16 // node table

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
	Inode  Ino    `xorm:"index notnull"`
	Type   uint8  `xorm:"notnull"`
}

type node struct {
	Inode     Ino    `xorm:"pk"`
	Type      uint8  `xorm:"notnull"`
	Flags     uint8  `xorm:"notnull"`
	Mode      uint16 `xorm:"notnull"`
	Uid       uint32 `xorm:"notnull"`
	Gid       uint32 `xorm:"notnull"`
	Atime     int64  `xorm:"notnull"`
	Mtime     int64  `xorm:"notnull"`
	Ctime     int64  `xorm:"notnull"`
	Atimensec int16  `xorm:"notnull default 0"`
	Mtimensec int16  `xorm:"notnull default 0"`
	Ctimensec int16  `xorm:"notnull default 0"`
	Nlink     uint32 `xorm:"notnull"`
	Length    uint64 `xorm:"notnull"`
	Rdev      uint32
	Parent    Ino
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

type sliceRef struct {
	Id   uint64 `xorm:"pk chunkid"`
	Size uint32 `xorm:"notnull"`
	Refs int    `xorm:"index notnull"`
}

func (c *sliceRef) TableName() string {
	return "jfs_chunk_ref"
}

type delslices struct {
	Id      uint64 `xorm:"pk chunkid"`
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

type dirStats struct {
	Inode      Ino   `xorm:"pk notnull"`
	DataLength int64 `xorm:"notnull"`
	UsedSpace  int64 `xorm:"notnull"`
	UsedInodes int64 `xorm:"notnull"`
}

type detachedNode struct {
	Inode Ino   `xorm:"pk notnull"`
	Added int64 `xorm:"notnull"`
}

type dirQuota struct {
	Inode      Ino   `xorm:"pk"`
	MaxSpace   int64 `xorm:"notnull"`
	MaxInodes  int64 `xorm:"notnull"`
	UsedSpace  int64 `xorm:"notnull"`
	UsedInodes int64 `xorm:"notnull"`
}

type dbMeta struct {
	*baseMeta
	db   *xorm.Engine
	snap *dbSnap

	noReadOnlyTxn bool
}

type dbSnap struct {
	node    map[Ino]*node
	symlink map[Ino]*symlink
	xattr   map[Ino][]*xattr
	edges   map[Ino][]*edge
	chunk   map[string]*chunk
}

func recoveryMysqlPwd(addr string) string {
	colonIndex := strings.Index(addr, ":")
	atIndex := strings.LastIndex(addr, "@")
	if colonIndex != -1 && colonIndex < atIndex {
		pwd := addr[colonIndex+1 : atIndex]
		if parse, err := url.Parse("mysql://root:" + pwd + "@127.0.0.1"); err == nil {
			if originPwd, ok := parse.User.Password(); ok {
				addr = fmt.Sprintf("%s:%s%s", addr[:colonIndex], originPwd, addr[atIndex:])
			}
		}
	}
	return addr
}

func newSQLMeta(driver, addr string, conf *Config) (Meta, error) {
	var searchPath string
	if driver == "postgres" {
		addr = driver + "://" + addr
		driver = "pgx"

		parse, err := url.Parse(addr)
		if err != nil {
			return nil, fmt.Errorf("parse url %s failed: %s", addr, err)
		}
		searchPath = parse.Query().Get("search_path")
		if searchPath != "" {
			if len(strings.Split(searchPath, ",")) > 1 {
				return nil, fmt.Errorf("currently, only one schema is supported in search_path")
			}
		}
	}

	// escaping is not necessary for mysql password https://github.com/go-sql-driver/mysql#password
	if driver == "mysql" {
		addr = recoveryMysqlPwd(addr)
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
	if searchPath != "" {
		engine.SetSchema(searchPath)
	}
	engine.DB().SetMaxIdleConns(runtime.GOMAXPROCS(-1) * 2)
	engine.DB().SetConnMaxIdleTime(time.Minute * 5)
	engine.SetTableMapper(names.NewPrefixMapper(engine.GetTableMapper(), "jfs_"))
	m := &dbMeta{
		baseMeta: newBaseMeta(addr, conf),
		db:       engine,
	}
	m.en = m
	return m, nil
}

func (m *dbMeta) Shutdown() error {
	return m.db.Close()
}

func (m *dbMeta) Name() string {
	name := m.db.DriverName()
	if name == "pgx" {
		name = "postgres"
	}
	return name
}

func (m *dbMeta) doDeleteSlice(id uint64, size uint32) error {
	return m.txn(func(s *xorm.Session) error {
		_, err := s.Exec("delete from jfs_chunk_ref where chunkid=?", id)
		return err
	})
}

func (m *dbMeta) syncTable(beans ...interface{}) error {
	err := m.db.Sync2(beans...)
	if err != nil && strings.Contains(err.Error(), "Duplicate key") {
		err = nil
	}
	return err
}

func (m *dbMeta) doInit(format *Format, force bool) error {
	if err := m.syncTable(new(setting), new(counter)); err != nil {
		return fmt.Errorf("create table setting, counter: %s", err)
	}
	if err := m.syncTable(new(edge)); err != nil {
		return fmt.Errorf("create table edge: %s", err)
	}
	if err := m.syncTable(new(node), new(symlink), new(xattr)); err != nil {
		return fmt.Errorf("create table node, symlink, xattr: %s", err)
	}
	if err := m.syncTable(new(chunk), new(sliceRef), new(delslices)); err != nil {
		return fmt.Errorf("create table chunk, chunk_ref, delslices: %s", err)
	}
	if err := m.syncTable(new(session2), new(sustained), new(delfile)); err != nil {
		return fmt.Errorf("create table session2, sustaind, delfile: %s", err)
	}
	if err := m.syncTable(new(flock), new(plock), new(dirQuota)); err != nil {
		return fmt.Errorf("create table flock, plock, dirQuota: %s", err)
	}
	if err := m.syncTable(new(dirStats)); err != nil {
		return fmt.Errorf("create table dirStats: %s", err)
	}
	if err := m.syncTable(new(detachedNode)); err != nil {
		return fmt.Errorf("create table detachedNode: %s", err)
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
		if !old.DirStats && format.DirStats {
			// remove dir stats as they are outdated
			_, err = m.db.Where("TRUE").Delete(new(dirStats))
			if err != nil {
				return errors.Wrap(err, "drop table dirStats")
			}
		}
		if err = format.update(&old, force); err != nil {
			return errors.Wrap(err, "update format")
		}
	}

	data, err := json.MarshalIndent(format, "", "")
	if err != nil {
		return fmt.Errorf("json: %s", err)
	}

	m.fmt = format
	now := time.Now()
	n := &node{
		Type:      TypeDirectory,
		Atime:     now.UnixNano() / 1e3,
		Mtime:     now.UnixNano() / 1e3,
		Ctime:     now.UnixNano() / 1e3,
		Atimensec: int16(now.UnixNano() % 1e3),
		Mtimensec: int16(now.UnixNano() % 1e3),
		Ctimensec: int16(now.UnixNano() % 1e3),
		Nlink:     2,
		Length:    4 << 10,
		Parent:    1,
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
		} else {
			var set = &setting{"format", string(data)}
			if n, err := s.Insert(set); err != nil {
				return err
			} else if n == 0 {
				return fmt.Errorf("format is not inserted")
			}
		}

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
		return mustInsert(s, n, &cs)
	})
}

func (m *dbMeta) Reset() error {
	return m.db.DropTables(&setting{}, &counter{},
		&node{}, &edge{}, &symlink{}, &xattr{},
		&chunk{}, &sliceRef{}, &delslices{},
		&session{}, &session2{}, &sustained{}, &delfile{},
		&flock{}, &plock{}, &dirStats{}, &dirQuota{}, &detachedNode{})
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
	// add new table
	err := m.syncTable(new(session2), new(delslices), new(dirStats), new(detachedNode), new(dirQuota))
	if err != nil {
		return fmt.Errorf("update table session2, delslices, dirstats, detachedNode, dirQuota: %s", err)
	}
	// add node table
	if err = m.syncTable(new(node)); err != nil {
		return fmt.Errorf("update table node: %s", err)
	}
	// add primary key
	if err = m.syncTable(new(edge), new(chunk), new(xattr), new(sustained)); err != nil {
		return fmt.Errorf("update table edge, chunk, xattr, sustained: %s", err)
	}
	// update the owner from uint64 to int64
	if err = m.syncTable(new(flock), new(plock)); err != nil {
		return fmt.Errorf("update table flock, plock: %s", err)
	}

	for {
		if err = m.txn(func(s *xorm.Session) error {
			return mustInsert(s, &session2{m.sid, m.expireTime(), sinfo})
		}); err == nil {
			break
		}
		if isDuplicateEntryErr(err) {
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
				s.Plocks = append(s.Plocks, Plock{prow.Inode, uint64(prow.Owner), loadLocks(prow.Records)})
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
		if ok, err := ses.IsTableExist(&session2{}); err != nil {
			return err
		} else if ok {
			row := session2{Sid: sid}
			if ok, err = ses.Get(&row); err != nil {
				return err
			} else if ok {
				s, err = m.getSession(&row, detail)
				return err
			}
		}
		if ok, err := ses.IsTableExist(&session{}); err != nil {
			return err
		} else if ok {
			row := session{Sid: sid}
			if ok, err = ses.Get(&row); err != nil {
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
		if ok, err := ses.IsTableExist(&session2{}); err != nil {
			return err
		} else if ok {
			var rows []session2
			if err = ses.Find(&rows); err != nil {
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
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "too many connections") || strings.Contains(msg, "too many clients") {
		logger.Warnf("transaction failed: %s, will retry it. please increase the max number of connections in your database, or use a connection pool.", msg)
		return true
	}
	switch m.Name() {
	case "sqlite3":
		return errors.Is(err, errBusy) || strings.Contains(msg, "database is locked")
	case "mysql":
		// MySQL, MariaDB or TiDB
		return strings.Contains(msg, "try restarting transaction") || strings.Contains(msg, "try again later") ||
			strings.Contains(msg, "duplicate entry")
	case "postgres":
		return strings.Contains(msg, "current transaction is aborted") || strings.Contains(msg, "deadlock detected") ||
			strings.Contains(msg, "duplicate key value") || strings.Contains(msg, "could not serialize access") ||
			strings.Contains(msg, "bad connection") || errors.Is(err, io.EOF) // could not send data to client: No buffer space available
	default:
		return false
	}
}

func (m *dbMeta) txn(f func(s *xorm.Session) error, inodes ...Ino) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()

	if m.Name() == "sqlite3" {
		// sqlite only allow one writer at a time
		inodes = []Ino{1}
	}

	if len(inodes) > 0 {
		m.txLock(uint(inodes[0]))
		defer m.txUnlock(uint(inodes[0]))
	}
	var lastErr error
	for i := 0; i < 50; i++ {
		_, err := m.db.Transaction(func(s *xorm.Session) (interface{}, error) {
			return nil, f(s)
		})
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if err != nil && m.shouldRetry(err) {
			m.txRestart.Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Transaction succeeded after %d tries (%s), inodes: %v, last error: %s", i+1, time.Since(start), inodes, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
}

func (m *dbMeta) roTxn(f func(s *xorm.Session) error) error {
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()
	s := m.db.NewSession()
	defer s.Close()
	var opt sql.TxOptions
	if !m.noReadOnlyTxn {
		opt.ReadOnly = true
		opt.Isolation = sql.LevelRepeatableRead
	}

	var lastErr error
	for i := 0; i < 50; i++ {
		err := s.BeginTx(&opt)
		if err != nil && opt.ReadOnly && (strings.Contains(err.Error(), "READ") || strings.Contains(err.Error(), "driver does not support read-only transactions")) {
			logger.Warnf("the database does not support read-only transaction")
			m.noReadOnlyTxn = true
			opt = sql.TxOptions{} // use default level
			err = s.BeginTx(&opt)
		}
		if err != nil {
			logger.Debugf("Start transaction failed, try again (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		}
		err = f(s)
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		_ = s.Rollback()
		if err != nil && m.shouldRetry(err) {
			m.txRestart.Add(1)
			logger.Debugf("Read transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Read transaction succeeded after %d tries (%s), last error: %s", i+1, time.Since(start), lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
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
	attr.Atimensec = uint32(n.Atime%1e6*1000) + uint32(n.Atimensec)
	attr.Mtime = n.Mtime / 1e6
	attr.Mtimensec = uint32(n.Mtime%1e6*1000) + uint32(n.Mtimensec)
	attr.Ctime = n.Ctime / 1e6
	attr.Ctimensec = uint32(n.Ctime%1e6*1000) + uint32(n.Ctimensec)
	attr.Nlink = n.Nlink
	attr.Length = n.Length
	attr.Rdev = n.Rdev
	attr.Parent = n.Parent
	attr.Full = true
}

func (m *dbMeta) parseNode(attr *Attr, n *node) {
	if attr == nil || n == nil {
		return
	}
	n.Type = attr.Typ
	n.Mode = attr.Mode
	n.Flags = attr.Flags
	n.Uid = attr.Uid
	n.Gid = attr.Gid
	n.Atime = attr.Atime*1e6 + int64(attr.Atimensec)/1000
	n.Mtime = attr.Mtime*1e6 + int64(attr.Mtimensec)/1000
	n.Ctime = attr.Ctime*1e6 + int64(attr.Ctimensec)/1000
	n.Atimensec = int16(attr.Atimensec % 1000)
	n.Mtimensec = int16(attr.Mtimensec % 1000)
	n.Ctimensec = int16(attr.Ctimensec % 1000)
	n.Nlink = attr.Nlink
	n.Length = attr.Length
	n.Rdev = attr.Rdev
	n.Parent = attr.Parent
}

func (m *dbMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *dbMeta) flushStats() {
	var inttype = "BIGINT"
	if m.Name() == "mysql" {
		inttype = "SIGNED"
	}
	for {
		newSpace := atomic.LoadInt64(&m.newSpace)
		newInodes := atomic.LoadInt64(&m.newInodes)
		if newSpace != 0 || newInodes != 0 {
			err := m.txn(func(s *xorm.Session) error {
				_, err := s.Exec(fmt.Sprintf("UPDATE jfs_counter SET value=value+ CAST((CASE name WHEN 'usedSpace' THEN %d ELSE %d END) AS %s) WHERE name='usedSpace' OR name='totalInodes' ", newSpace, newInodes, inttype))
				return err
			})
			if err != nil && !strings.Contains(err.Error(), "attempt to write a readonly database") {
				logger.Warnf("update stats: %s", err)
			}
			if err == nil {
				atomic.AddInt64(&m.newSpace, -newSpace)
				atomic.AddInt64(&m.usedSpace, newSpace)
				atomic.AddInt64(&m.newInodes, -newInodes)
				atomic.AddInt64(&m.usedInodes, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *dbMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	return errno(m.roTxn(func(s *xorm.Session) error {
		s = s.Table(&edge{})
		nn := namedNode{node: node{Parent: parent}, Name: []byte(name)}
		var exist bool
		var err error
		if attr != nil {
			s = s.Join("INNER", &node{}, "jfs_edge.inode=jfs_node.inode")
			exist, err = s.Select("jfs_node.*").Get(&nn)
		} else {
			exist, err = s.Select("*").Get(&nn)
		}
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

func (m *dbMeta) doSetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		var cur = node{Inode: inode}
		ok, err := s.ForUpdate().Get(&cur)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		var curAttr Attr
		m.parseAttr(&cur, &curAttr)
		if curAttr.Parent > TrashInode {
			return syscall.EPERM
		}
		now := time.Now()
		dirtyAttr, st := m.mergeAttr(ctx, inode, set, &curAttr, attr, now)
		if st != 0 {
			return st
		}
		if dirtyAttr == nil {
			return nil
		}
		var dirtyNode node
		m.parseNode(dirtyAttr, &dirtyNode)
		dirtyNode.Ctime = now.UnixNano() / 1e3
		dirtyNode.Ctimensec = int16(now.Nanosecond() % 1000)
		_, err = s.Cols("flags", "mode", "uid", "gid", "atime", "mtime", "ctime", "atimensec", "mtimensec", "ctimensec").
			Update(&dirtyNode, &node{Inode: inode})
		if err == nil {
			m.parseAttr(&dirtyNode, attr)
		}
		return err
	}, inode))
}

func (m *dbMeta) appendSlice(s *xorm.Session, inode Ino, indx uint32, buf []byte) error {
	var r sql.Result
	var err error
	driver := m.Name()
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

func (m *dbMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr, skipPermCheck bool) syscall.Errno {
	defer m.timeit("Truncate", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, invalidateAllChunks) }()
	var newLength, newSpace int64
	var nodeAttr node
	if attr == nil {
		attr = &Attr{}
	}
	err := m.txn(func(s *xorm.Session) error {
		newLength, newSpace = 0, 0
		nodeAttr = node{Inode: inode}
		ok, err := s.ForUpdate().Get(&nodeAttr)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if nodeAttr.Type != TypeFile || nodeAttr.Flags&(FlagImmutable|FlagAppend) != 0 || nodeAttr.Parent > TrashInode {
			return syscall.EPERM
		}
		m.parseAttr(&nodeAttr, attr)
		if !skipPermCheck {
			if st := m.Access(ctx, inode, MODE_MASK_W, attr); st != 0 {
				return st
			}
		}
		if length == nodeAttr.Length {
			return nil
		}
		newLength = int64(length) - int64(nodeAttr.Length)
		newSpace = align4K(length) - align4K(nodeAttr.Length)
		if err := m.checkQuota(ctx, newSpace, 0, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
			return err
		}
		var zeroChunks []chunk
		var left, right = nodeAttr.Length, length
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
		nodeAttr.Length = length
		now := time.Now().UnixNano()
		nodeAttr.Mtime = now / 1e3
		nodeAttr.Ctime = now / 1e3
		nodeAttr.Mtimensec = int16(now % 1e3)
		nodeAttr.Ctimensec = int16(now % 1e3)
		if _, err = s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nodeAttr, &node{Inode: nodeAttr.Inode}); err != nil {
			return err
		}
		m.parseAttr(&nodeAttr, attr)
		return nil
	}, inode)
	if err == nil {
		m.updateParentStat(ctx, inode, nodeAttr.Parent, newLength, newSpace)
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
	defer m.timeit("Fallocate", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, invalidateAllChunks) }()
	var newLength, newSpace int64
	var nodeAttr node
	err := m.txn(func(s *xorm.Session) error {
		newLength, newSpace = 0, 0
		nodeAttr = node{Inode: inode}
		ok, err := s.ForUpdate().Get(&nodeAttr)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if nodeAttr.Type == TypeFIFO {
			return syscall.EPIPE
		}
		if nodeAttr.Type != TypeFile || (nodeAttr.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		if (nodeAttr.Flags&FlagAppend) != 0 && (mode&^fallocKeepSize) != 0 {
			return syscall.EPERM
		}
		length := nodeAttr.Length
		if off+size > nodeAttr.Length {
			if mode&fallocKeepSize == 0 {
				length = off + size
			}
		}

		old := nodeAttr.Length
		newLength = int64(length) - int64(old)
		newSpace = align4K(length) - align4K(old)
		if err := m.checkQuota(ctx, newSpace, 0, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
			return err
		}
		now := time.Now().UnixNano()
		nodeAttr.Length = length
		nodeAttr.Mtime = now / 1e3
		nodeAttr.Ctime = now / 1e3
		nodeAttr.Mtimensec = int16(now % 1e3)
		nodeAttr.Ctimensec = int16(now % 1e3)
		if _, err := s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nodeAttr, &node{Inode: inode}); err != nil {
			return err
		}
		if mode&(fallocZeroRange|fallocPunchHole) != 0 && off < old {
			off, size := off, size
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
	}, inode)
	if err == nil {
		m.updateParentStat(ctx, inode, nodeAttr.Parent, newLength, newSpace)
	}
	return errno(err)
}

func (m *dbMeta) doReadlink(ctx Context, inode Ino, noatime bool) (atime int64, target []byte, err error) {
	if noatime {
		err = m.roTxn(func(s *xorm.Session) error {
			var l = symlink{Inode: inode}
			ok, err := s.Get(&l)
			if err == nil && ok {
				target = l.Target
			}
			return err
		})
		return
	}

	attr := &Attr{}
	now := time.Now()
	err = m.txn(func(s *xorm.Session) error {
		nodeAttr := node{Inode: inode}
		ok, e := s.ForUpdate().Get(&nodeAttr)
		if e != nil {
			return e
		}
		if !ok {
			return syscall.ENOENT
		}
		l := symlink{Inode: inode}
		ok, e = s.Get(&l)
		if e != nil {
			return e
		}
		if !ok {
			return syscall.ENOENT
		}
		m.parseAttr(&nodeAttr, attr)
		target = l.Target
		if !m.atimeNeedsUpdate(attr, now) {
			return nil
		}
		nodeAttr.Atime = now.Unix()*1e6 + int64(now.Nanosecond())/1e3
		nodeAttr.Atimensec = int16(now.Nanosecond() % 1e3)
		attr.Atime = now.Unix()
		_, e = s.Cols("atime", "atimensec").Update(&nodeAttr, &node{Inode: inode})
		return e
	}, inode)
	atime = attr.Atime
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
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if pattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parent, MODE_MASK_W, &pattr); st != 0 {
			return st
		}
		if (pn.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
		}
		var e = edge{Parent: parent, Name: []byte(name)}
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

		var updateParent bool
		now := time.Now().UnixNano()
		if parent != TrashInode {
			if _type == TypeDirectory {
				pn.Nlink++
				updateParent = true
			}
			if updateParent || time.Duration(now-pn.Mtime*1e3-int64(pn.Mtimensec)) >= minUpdateTime {
				pn.Mtime = now / 1e3
				pn.Ctime = now / 1e3
				updateParent = true
			}
		}
		n.Atime = now / 1e3
		n.Mtime = now / 1e3
		n.Ctime = now / 1e3
		n.Atimensec = int16(now % 1e3)
		n.Mtimensec = int16(now % 1e3)
		n.Ctimensec = int16(now % 1e3)
		if ctx.Value(CtxKey("behavior")) == "Hadoop" || runtime.GOOS == "darwin" {
			n.Gid = pn.Gid
		} else if runtime.GOOS == "linux" && pn.Mode&02000 != 0 {
			n.Gid = pn.Gid
			if _type == TypeDirectory {
				n.Mode |= 02000
			} else if n.Mode&02010 == 02010 && ctx.Uid() != 0 {
				var found bool
				for _, gid := range ctx.Gids() {
					if gid == pn.Gid {
						found = true
					}
				}
				if !found {
					n.Mode &= ^uint16(02000)
				}
			}
		}

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: ino, Type: _type}, &n); err != nil {
			return err
		}
		if updateParent {
			if _, err := s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode}); err != nil {
				return err
			}
		}
		if _type == TypeSymlink {
			if err = mustInsert(s, &symlink{Inode: ino, Target: []byte(path)}); err != nil {
				return err
			}
		}
		if _type == TypeDirectory {
			if err = mustInsert(s, &dirStats{Inode: ino}); err != nil {
				return err
			}
		}
		m.parseAttr(&n, attr)
		return nil
	}, parent))
}

func (m *dbMeta) doUnlink(ctx Context, parent Ino, name string, attr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
	}
	var n node
	var opened bool
	var newSpace, newInode int64
	err := m.txn(func(s *xorm.Session) error {
		opened = false
		newSpace, newInode = 0, 0
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
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if (pn.Flags&FlagAppend) != 0 || (pn.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.Get(&e)
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
		now := time.Now().UnixNano()
		if ok {
			if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
			if (n.Flags&FlagAppend) != 0 || (n.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			if trash > 0 && n.Nlink > 1 {
				if o, e := s.Get(&edge{Parent: trash, Name: []byte(m.trashEntry(parent, e.Inode, string(e.Name))), Inode: e.Inode, Type: e.Type}); e == nil && o {
					trash = 0
				}
			}
			n.Ctime = now / 1e3
			n.Ctimensec = int16(now % 1e3)
			if trash == 0 {
				n.Nlink--
				if n.Type == TypeFile && n.Nlink == 0 && m.sid > 0 {
					opened = m.of.IsOpen(e.Inode)
				}
			} else if n.Parent > 0 {
				n.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, name)
			trash = 0
		}
		defer func() { m.of.InvalidateChunk(e.Inode, invalidateAttrOnly) }()

		var updateParent bool
		if !isTrash(parent) && time.Duration(now-pn.Mtime*1e3-int64(pn.Mtimensec)) >= minUpdateTime {
			pn.Mtime = now / 1e3
			pn.Ctime = now / 1e3
			pn.Mtimensec = int16(now % 1e3)
			pn.Ctimensec = int16(now % 1e3)
			updateParent = true
		}

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if updateParent {
			if _, err = s.Cols("mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode}); err != nil {
				return err
			}
		}
		if n.Nlink > 0 {
			if _, err := s.Cols("nlink", "ctime", "ctimensec", "parent").Update(&n, &node{Inode: e.Inode}); err != nil {
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
					if _, err := s.Cols("nlink", "ctime", "ctimensec").Update(&n, &node{Inode: e.Inode}); err != nil {
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
	}, parent)
	if err == nil && trash == 0 {
		if n.Type == TypeFile && n.Nlink == 0 {
			m.fileDeleted(opened, isTrash(parent), n.Inode, n.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	if err == nil && attr != nil {
		m.parseAttr(&n, attr)
	}
	return errno(err)
}

func (m *dbMeta) doRmdir(ctx Context, parent Ino, name string, pinode *Ino, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
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
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
			return st
		}
		if pn.Flags&FlagImmutable != 0 || pn.Flags&FlagAppend != 0 {
			return syscall.EPERM
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.Get(&e)
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
		if pinode != nil {
			*pinode = e.Inode
		}
		var n = node{Inode: e.Inode}
		ok, err = s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		exist, err := s.Exist(&edge{Parent: e.Inode})
		if err != nil {
			return err
		}
		if exist {
			return syscall.ENOTEMPTY
		}
		now := time.Now().UnixNano()
		if ok {
			if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
			if trash > 0 {
				n.Ctime = now / 1e3
				n.Ctimensec = int16(now % 1e3)
				n.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, name)
			trash = 0
		}
		pn.Nlink--
		pn.Mtime = now / 1e3
		pn.Ctime = now / 1e3
		pn.Mtimensec = int16(now % 1e3)
		pn.Ctimensec = int16(now % 1e3)

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if _, err := s.Delete(&dirStats{Inode: e.Inode}); err != nil {
			logger.Warnf("remove dir usage of ino(%d): %s", e.Inode, err)
		}
		if _, err = s.Delete(&dirQuota{Inode: e.Inode}); err != nil {
			return err
		}

		if trash > 0 {
			if _, err = s.Cols("ctime", "ctimensec", "parent").Update(&n, &node{Inode: n.Inode}); err != nil {
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
			_, err = s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode})
		}
		return err
	}, parent)
	if err == nil && trash == 0 {
		m.updateStats(-align4K(0), -1)
	}
	return errno(err)
}

func (m *dbMeta) getNodesForUpdate(s *xorm.Session, nodes ...*node) error {
	// sort them to avoid deadlock
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Inode < nodes[j].Inode })
	for i := range nodes {
		ok, err := s.ForUpdate().Get(nodes[i])
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
	}
	return nil
}

func (m *dbMeta) doRename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode, tInode *Ino, attr, tAttr *Attr) syscall.Errno {
	var trash Ino
	if st := m.checkTrash(parentDst, &trash); st != 0 {
		return st
	}
	exchange := flags == RenameExchange
	var opened bool
	var dino Ino
	var dn node
	var newSpace, newInode int64
	lockParent := parentSrc
	if isTrash(lockParent) {
		lockParent = parentDst
	}
	err := m.txn(func(s *xorm.Session) error {
		opened = false
		dino = 0
		newSpace, newInode = 0, 0
		var spn = node{Inode: parentSrc}
		var dpn = node{Inode: parentDst}
		err := m.getNodesForUpdate(s, &spn, &dpn)
		if err != nil {
			return err
		}
		if spn.Type != TypeDirectory || dpn.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		if (spn.Flags&FlagAppend) != 0 || (spn.Flags&FlagImmutable) != 0 || (dpn.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}
		var spattr, dpattr Attr
		m.parseAttr(&spn, &spattr)
		m.parseAttr(&dpn, &dpattr)
		if flags&RenameRestore == 0 && dpattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parentSrc, MODE_MASK_W|MODE_MASK_X, &spattr); st != 0 {
			return st
		}
		if st := m.Access(ctx, parentDst, MODE_MASK_W|MODE_MASK_X, &dpattr); st != 0 {
			return st
		}
		var se = edge{Parent: parentSrc, Name: []byte(nameSrc)}
		ok, err := s.Get(&se)
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
		ok, err = s.Get(&sn)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		var sattr Attr
		m.parseAttr(&sn, &sattr)
		if parentSrc != parentDst && spattr.Mode&0o1000 != 0 && ctx.Uid() != 0 &&
			ctx.Uid() != sattr.Uid && (ctx.Uid() != spattr.Uid || sattr.Typ == TypeDirectory) {
			return syscall.EACCES
		}
		if (sn.Flags&FlagAppend) != 0 || (sn.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}

		if st := m.Access(ctx, parentDst, MODE_MASK_W|MODE_MASK_X, &dpattr); st != 0 {
			return st
		}
		var de = edge{Parent: parentDst, Name: []byte(nameDst)}
		ok, err = s.Get(&de)
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
		now := time.Now().UnixNano()
		dn = node{Inode: de.Inode}
		if ok {
			if flags&RenameNoReplace != 0 {
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
			if (dn.Flags&FlagAppend) != 0 || (dn.Flags&FlagImmutable) != 0 {
				return syscall.EPERM
			}
			dn.Ctime = now / 1e3
			dn.Ctimensec = int16(now % 1e3)
			if exchange {
				if parentSrc != parentDst {
					if de.Type == TypeDirectory {
						dn.Parent = parentSrc
						dpn.Nlink--
						spn.Nlink++
						supdate, dupdate = true, true
					} else if dn.Parent > 0 {
						dn.Parent = parentSrc
					}
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
						defer func() { m.of.InvalidateChunk(dino, invalidateAttrOnly) }()
					} else if dn.Parent > 0 {
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
		}
		if ctx.Uid() != 0 && spn.Mode&01000 != 0 && ctx.Uid() != spn.Uid && ctx.Uid() != sn.Uid {
			return syscall.EACCES
		}

		if parentSrc != parentDst {
			if se.Type == TypeDirectory {
				sn.Parent = parentDst
				spn.Nlink--
				dpn.Nlink++
				supdate, dupdate = true, true
			} else if sn.Parent > 0 {
				sn.Parent = parentDst
			}
		}
		if supdate || time.Duration(now-spn.Mtime*1e3-int64(spn.Mtimensec)) >= minUpdateTime {
			spn.Mtime = now / 1e3
			spn.Ctime = now / 1e3
			spn.Mtimensec = int16(now % 1e3)
			spn.Ctimensec = int16(now % 1e3)
			supdate = true
		}
		if dupdate || time.Duration(now-dpn.Mtime*1e3-int64(dpn.Mtimensec)) >= minUpdateTime {
			dpn.Mtime = now / 1e3
			dpn.Ctime = now / 1e3
			dpn.Mtimensec = int16(now % 1e3)
			dpn.Ctimensec = int16(now % 1e3)
			dupdate = true
		}
		sn.Ctime = now / 1e3
		sn.Ctimensec = int16(now % 1e3)
		if inode != nil {
			*inode = sn.Inode
		}
		m.parseAttr(&sn, attr)
		if dino > 0 {
			*tInode = dino
			m.parseAttr(&dn, tAttr)
		}

		if exchange {
			if _, err := s.Cols("inode", "type").Update(&de, &edge{Parent: parentSrc, Name: se.Name}); err != nil {
				return err
			}
			if _, err := s.Cols("inode", "type").Update(&se, &edge{Parent: parentDst, Name: de.Name}); err != nil {
				return err
			}
			if _, err := s.Cols("ctime", "ctimensec", "parent").Update(dn, &node{Inode: dino}); err != nil {
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
					if _, err := s.Cols("ctime", "ctimensec", "parent").Update(dn, &node{Inode: dino}); err != nil {
						return err
					}
					name := m.trashEntry(parentDst, dino, string(de.Name))
					if err = mustInsert(s, &edge{Parent: trash, Name: []byte(name), Inode: dino, Type: de.Type}); err != nil {
						return err
					}
				} else if de.Type != TypeDirectory && dn.Nlink > 0 {
					if _, err := s.Cols("ctime", "ctimensec", "nlink", "parent").Update(dn, &node{Inode: dino}); err != nil {
						return err
					}
				} else {
					if de.Type == TypeFile {
						if opened {
							if _, err := s.Cols("nlink", "ctime", "ctimensec").Update(&dn, &node{Inode: dino}); err != nil {
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
				if de.Type == TypeDirectory {
					if _, err = s.Delete(&dirQuota{Inode: dino}); err != nil {
						return err
					}
				}
			}
			if err = mustInsert(s, &edge{Parent: parentDst, Name: de.Name, Inode: se.Inode, Type: se.Type}); err != nil {
				return err
			}
		}
		if parentDst != parentSrc && !isTrash(parentSrc) && supdate {
			if _, err := s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&spn, &node{Inode: parentSrc}); err != nil {
				return err
			}
		}
		if _, err := s.Cols("ctime", "ctimensec", "parent").Update(&sn, &node{Inode: sn.Inode}); err != nil {
			return err
		}
		if dupdate {
			if _, err := s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&dpn, &node{Inode: parentDst}); err != nil {
				return err
			}
		}
		return err
	}, lockParent)
	if err == nil && !exchange && trash == 0 {
		if dino > 0 && dn.Type == TypeFile && dn.Nlink == 0 {
			m.fileDeleted(opened, false, dino, dn.Length)
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
		if pn.Parent > TrashInode {
			return syscall.ENOENT
		}
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if st := m.Access(ctx, parent, MODE_MASK_W, &pattr); st != 0 {
			return st
		}
		if pn.Flags&FlagImmutable != 0 {
			return syscall.EPERM
		}
		var e = edge{Parent: parent, Name: []byte(name)}
		ok, err = s.Get(&e)
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
		if (n.Flags&FlagAppend) != 0 || (n.Flags&FlagImmutable) != 0 {
			return syscall.EPERM
		}

		var updateParent bool
		now := time.Now().UnixNano()
		if time.Duration(now-pn.Mtime*1e3-int64(pn.Mtimensec)) >= minUpdateTime {
			pn.Mtime = now / 1e3
			pn.Ctime = now / 1e3
			updateParent = true
		}
		n.Parent = 0
		n.Nlink++
		n.Ctime = now / 1e3

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: inode, Type: n.Type}); err != nil {
			return err
		}
		if updateParent {
			if _, err := s.Cols("mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: parent}); err != nil {
				return err
			}
		}
		if _, err := s.Cols("nlink", "ctime", "ctimensec", "parent").Update(&n, node{Inode: inode}); err != nil {
			return err
		}
		if err == nil {
			m.parseAttr(&n, attr)
		}
		return err
	}, parent))
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
		logger.Warnf("Delete flock/plock with sid %d: %s", sid, err)
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

func (m *dbMeta) doRefreshSession() error {
	return m.txn(func(ses *xorm.Session) error {
		n, err := ses.Cols("Expire").Update(&session2{Expire: m.expireTime()}, &session2{Sid: m.sid})
		if err == nil && n == 0 {
			logger.Warnf("Session %d was stale and cleaned up, but now it comes back again", m.sid)
			err = mustInsert(ses, &session2{m.sid, m.expireTime(), m.newSessionInfo()})
		}
		return err
	})
}

func (m *dbMeta) doDeleteSustainedInode(sid uint64, inode Ino) error {
	var n = node{Inode: inode}
	var newSpace int64
	err := m.txn(func(s *xorm.Session) error {
		newSpace = 0
		n = node{Inode: inode}
		ok, err := s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		newSpace = -align4K(n.Length)
		if err = mustInsert(s, &delfile{inode, n.Length, time.Now().Unix()}); err != nil {
			return err
		}
		_, err = s.Delete(&sustained{Sid: sid, Inode: inode})
		if err != nil {
			return err
		}
		_, err = s.Delete(&node{Inode: inode})
		return err
	}, inode)
	if err == nil && newSpace < 0 {
		m.updateStats(newSpace, -1)
		m.tryDeleteFileData(inode, n.Length, false)
		m.updateDirQuota(Background, n.Parent, newSpace, -1)
	}
	return err
}

func (m *dbMeta) Read(ctx Context, inode Ino, indx uint32, slices *[]Slice) (rerr syscall.Errno) {
	defer func() {
		if rerr == 0 {
			m.touchAtime(ctx, inode, nil)
		}
	}()
	f := m.of.find(inode)
	if f != nil {
		f.RLock()
		defer f.RUnlock()
	}
	if ss, ok := m.of.ReadChunk(inode, indx); ok {
		*slices = ss
		return 0
	}
	defer m.timeit("Read", time.Now())
	var c = chunk{Inode: inode, Indx: indx}
	err := m.roTxn(func(s *xorm.Session) error {
		_, err := s.MustCols("indx").Get(&c)
		return err
	})
	if err != nil {
		return errno(err)
	}
	ss := readSliceBuf(c.Slices)
	if ss == nil {
		return syscall.EIO
	}
	*slices = buildSlice(ss)
	m.of.CacheChunk(inode, indx, *slices)
	if !m.conf.ReadOnly && (len(c.Slices)/sliceBytes >= 5 || len(*slices) >= 5) {
		go m.compactChunk(inode, indx, false)
	}
	return 0
}

func (m *dbMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time) syscall.Errno {
	defer m.timeit("Write", time.Now())
	f := m.of.find(inode)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	defer func() { m.of.InvalidateChunk(inode, indx) }()
	var newLength, newSpace int64
	var needCompact bool
	var nodeAttr node
	err := m.txn(func(s *xorm.Session) error {
		newLength, newSpace = 0, 0
		nodeAttr = node{Inode: inode}
		ok, err := s.ForUpdate().Get(&nodeAttr)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if nodeAttr.Type != TypeFile {
			return syscall.EPERM
		}
		newleng := uint64(indx)*ChunkSize + uint64(off) + uint64(slice.Len)
		if newleng > nodeAttr.Length {
			newLength = int64(newleng - nodeAttr.Length)
			newSpace = align4K(newleng) - align4K(nodeAttr.Length)
			nodeAttr.Length = newleng
		}
		if err := m.checkQuota(ctx, newSpace, 0, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
			return err
		}
		nodeAttr.Mtime = mtime.UnixNano() / 1e3
		nodeAttr.Mtimensec = int16(mtime.Nanosecond())
		ctime := time.Now()
		nodeAttr.Ctime = ctime.UnixNano() / 1e3
		nodeAttr.Ctimensec = int16(ctime.Nanosecond())

		var ck = chunk{Inode: inode, Indx: indx}
		ok, err = s.ForUpdate().MustCols("indx").Get(&ck)
		if err != nil {
			return err
		}
		buf := marshalSlice(off, slice.Id, slice.Size, slice.Off, slice.Len)
		if ok {
			if err := m.appendSlice(s, inode, indx, buf); err != nil {
				return err
			}
		} else {
			if err = mustInsert(s, &chunk{Inode: inode, Indx: indx, Slices: buf}); err != nil {
				return err
			}
		}
		if err = mustInsert(s, sliceRef{slice.Id, slice.Size, 1}); err != nil {
			return err
		}
		_, err = s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nodeAttr, &node{Inode: inode})
		if err == nil {
			ns := len(ck.Slices) / sliceBytes // number of slices
			needCompact = ns%100 == 99 || ns > 350
		}
		return err
	}, inode)
	if err == nil {
		if needCompact {
			go m.compactChunk(inode, indx, false)
		}
		m.updateParentStat(ctx, inode, nodeAttr.Parent, newLength, newSpace)
	}
	return errno(err)
}

func (m *dbMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied *uint64) syscall.Errno {
	defer m.timeit("CopyFileRange", time.Now())
	f := m.of.find(fout)
	if f != nil {
		f.Lock()
		defer f.Unlock()
	}
	var newLength, newSpace int64
	var nin, nout node
	defer func() { m.of.InvalidateChunk(fout, invalidateAllChunks) }()
	err := m.txn(func(s *xorm.Session) error {
		newLength, newSpace = 0, 0
		nin = node{Inode: fin}
		nout = node{Inode: fout}
		err := m.getNodesForUpdate(s, &nin, &nout)
		if err != nil {
			return err
		}
		if nin.Type != TypeFile {
			return syscall.EINVAL
		}
		if offIn >= nin.Length {
			*copied = 0
			return nil
		}
		size := size
		if offIn+size > nin.Length {
			size = nin.Length - offIn
		}
		if nout.Type != TypeFile {
			return syscall.EINVAL
		}
		if (nout.Flags&FlagImmutable) != 0 || (nout.Flags&FlagAppend) != 0 {
			return syscall.EPERM
		}

		newleng := offOut + size
		if newleng > nout.Length {
			newLength = int64(newleng - nout.Length)
			newSpace = align4K(newleng) - align4K(nout.Length)
			nout.Length = newleng
		}
		if err := m.checkQuota(ctx, newSpace, 0, m.getParents(s, fout, nout.Parent)...); err != 0 {
			return err
		}
		now := time.Now().UnixNano()
		nout.Mtime = now / 1e3
		nout.Ctime = now / 1e3
		nout.Mtimensec = int16(now % 1e3)
		nout.Ctimensec = int16(now % 1e3)

		var cs []chunk
		err = s.Where("inode = ? AND indx >= ? AND indx <= ?", fin, offIn/ChunkSize, (offIn+size)/ChunkSize).ForUpdate().Find(&cs)
		if err != nil {
			return err
		}
		chunks := make(map[uint32][]*slice)
		for _, c := range cs {
			chunks[c.Indx] = readSliceBuf(c.Slices)
			if chunks[c.Indx] == nil {
				return syscall.EIO
			}
		}

		ses := s
		updateSlices := func(indx uint32, buf []byte, id uint64, size uint32) error {
			if err := m.appendSlice(ses, fout, indx, buf); err != nil {
				return err
			}
			if id > 0 {
				if _, err := ses.Exec("update jfs_chunk_ref set refs=refs+1 where chunkid = ? AND size = ?", id, size); err != nil {
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
						if err := updateSlices(indx, marshalSlice(dpos, s.Id, s.Size, s.Off, ChunkSize-dpos), s.Id, s.Size); err != nil {
							return err
						}
						skip := ChunkSize - dpos
						if err := updateSlices(indx+1, marshalSlice(0, s.Id, s.Size, s.Off+skip, s.Len-skip), s.Id, s.Size); err != nil {
							return err
						}
					} else {
						if err := updateSlices(indx, marshalSlice(dpos, s.Id, s.Size, s.Off, s.Len), s.Id, s.Size); err != nil {
							return err
						}
					}
				}
			}
		}
		if _, err := s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nout, &node{Inode: fout}); err != nil {
			return err
		}
		*copied = size
		return nil
	}, fout)
	if err == nil {
		m.updateParentStat(ctx, fout, nout.Parent, newLength, newSpace)
	}
	return errno(err)
}

func (m *dbMeta) getParents(s *xorm.Session, inode, parent Ino) []Ino {
	if parent > 0 {
		return []Ino{parent}
	}
	var rows []edge
	if err := s.Find(&rows, &edge{Inode: inode}); err != nil {
		logger.Warnf("Scan edge key of inode %d: %s", inode, err)
		return nil
	}
	ps := make(map[Ino]struct{})
	for _, row := range rows {
		ps[row.Parent] = struct{}{}
	}
	pss := make([]Ino, 0, len(ps))
	for p := range ps {
		pss = append(pss, p)
	}
	return pss
}

func (m *dbMeta) doGetParents(ctx Context, inode Ino) map[Ino]int {
	var rows []edge
	if err := m.roTxn(func(s *xorm.Session) error {
		rows = nil
		return s.Find(&rows, &edge{Inode: inode})
	}); err != nil {
		logger.Warnf("Scan edge key of inode %d: %s", inode, err)
		return nil
	}
	ps := make(map[Ino]int)
	for _, row := range rows {
		ps[row.Parent]++
	}
	return ps
}

func (m *dbMeta) doUpdateDirStat(ctx Context, batch map[Ino]dirStat) error {
	table := m.db.GetTableMapper().Obj2Table("dirStats")
	fileLengthColumn := m.db.GetColumnMapper().Obj2Table("DataLength")
	usedSpaceColumn := m.db.GetColumnMapper().Obj2Table("UsedSpace")
	usedInodeColumn := m.db.GetColumnMapper().Obj2Table("UsedInodes")
	sql := fmt.Sprintf(
		"update `%s` set `%s` = `%s` + ?, `%s` = `%s` + ?, `%s` = `%s` + ? where `inode` = ?",
		table,
		fileLengthColumn, fileLengthColumn,
		usedSpaceColumn, usedSpaceColumn,
		usedInodeColumn, usedInodeColumn,
	)

	nonexist := make(map[Ino]bool, 0)

	for _, group := range m.groupBatch(batch, 1000) {
		err := m.txn(func(s *xorm.Session) error {
			for _, ino := range group {
				stat := batch[ino]
				ret, err := s.Exec(sql, stat.length, stat.space, stat.inodes, ino)
				if err != nil {
					return err
				}
				affected, err := ret.RowsAffected()
				if err != nil {
					return err
				}
				if affected == 0 {
					nonexist[ino] = true
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if len(nonexist) > 0 {
		m.parallelSyncDirStat(ctx, nonexist).Wait()
	}
	return nil
}

func (m *dbMeta) doSyncDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
	if m.conf.ReadOnly {
		return nil, syscall.EROFS
	}
	stat, st := m.calcDirStat(ctx, ino)
	if st != 0 {
		return nil, st
	}
	err := m.txn(func(s *xorm.Session) error {
		exist, err := s.Exist(&node{Inode: ino})
		if err != nil {
			return err
		}
		if !exist {
			return syscall.ENOENT
		}
		record := &dirStats{ino, stat.length, stat.space, stat.inodes}
		_, err = s.Insert(record)
		if err != nil && isDuplicateEntryErr(err) {
			_, err = s.Cols("data_length", "used_space", "used_inodes").Update(record, &dirStats{Inode: ino})
		}
		return err
	})
	return stat, errno(err)
}

func (m *dbMeta) doGetDirStat(ctx Context, ino Ino, trySync bool) (*dirStat, syscall.Errno) {
	st := dirStats{Inode: ino}
	var exist bool
	var err error
	if err = m.roTxn(func(s *xorm.Session) error {
		exist, err = s.Get(&st)
		return err
	}); err != nil {
		return nil, errno(err)
	}
	if !exist {
		if trySync {
			return m.doSyncDirStat(ctx, ino)
		}
		return nil, 0
	}

	if trySync && (st.UsedSpace < 0 || st.UsedInodes < 0) {
		logger.Warnf(
			"dir usage of inode %d is invalid: space %d, inodes %d, try to fix",
			ino, st.UsedSpace, st.UsedInodes,
		)
		stat, eno := m.calcDirStat(ctx, ino)
		if eno != 0 {
			return nil, eno
		}
		st.DataLength, st.UsedSpace, st.UsedInodes = stat.length, stat.space, stat.inodes
		e := m.txn(func(s *xorm.Session) error {
			n, err := s.AllCols().Update(&st)
			if err == nil && n != 1 {
				err = errors.Errorf("update dir usage of inode %d: %d rows affected", ino, n)
			}
			return err
		})
		if e != nil {
			logger.Warn(e)
		}
	}
	return &dirStat{st.DataLength, st.UsedSpace, st.UsedInodes}, 0
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
	var cks []sliceRef
	_ = m.roTxn(func(s *xorm.Session) error {
		cks = nil
		return s.Where("refs <= 0").Find(&cks)
	})
	for _, ck := range cks {
		m.deleteSlice(ck.Id, ck.Size)
	}
}

func (m *dbMeta) deleteChunk(inode Ino, indx uint32) error {
	var ss []*slice
	err := m.txn(func(s *xorm.Session) error {
		ss = ss[:0]
		var c = chunk{Inode: inode, Indx: indx}
		ok, err := s.ForUpdate().MustCols("indx").Get(&c)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		ss = readSliceBuf(c.Slices)
		if ss == nil {
			logger.Errorf("Corrupt value for inode %d chunk index %d, use `gc` to clean up leaked slices", inode, indx)
		}
		for _, sc := range ss {
			if sc.id == 0 {
				continue
			}
			_, err = s.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? AND size=?", sc.id, sc.size)
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
		if s.id == 0 {
			continue
		}
		var ref = sliceRef{Id: s.id}
		err := m.roTxn(func(s *xorm.Session) error {
			ok, err := s.Get(&ref)
			if err == nil && !ok {
				err = errors.New("not found")
			}
			return err
		})
		if err == nil && ref.Refs <= 0 {
			m.deleteSlice(s.id, s.size)
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

func (m *dbMeta) doCleanupDelayedSlices(edge int64) (int, error) {
	start := time.Now()
	var count int
	var ss []Slice
	var result []delslices
	var batch int = 1e6
	for {
		_ = m.roTxn(func(s *xorm.Session) error {
			result = result[:0]
			return s.Where("deleted < ?", edge).Limit(batch, 0).Find(&result)
		})

		for _, ds := range result {
			if err := m.txn(func(ses *xorm.Session) error {
				ss = ss[:0]
				ds := delslices{Id: ds.Id}
				if ok, e := ses.ForUpdate().Get(&ds); e != nil {
					return e
				} else if !ok {
					return nil
				}
				m.decodeDelayedSlices(ds.Slices, &ss)
				if len(ss) == 0 {
					return fmt.Errorf("invalid value for delayed slices %d: %v", ds.Id, ds.Slices)
				}
				for _, s := range ss {
					if _, e := ses.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s.Id, s.Size); e != nil {
						return e
					}
				}
				_, e := ses.Delete(&delslices{Id: ds.Id})
				return e
			}); err != nil {
				logger.Warnf("Cleanup delayed slices %d: %s", ds.Id, err)
				continue
			}
			for _, s := range ss {
				var ref = sliceRef{Id: s.Id}
				err := m.roTxn(func(s *xorm.Session) error {
					ok, err := s.Get(&ref)
					if err == nil && !ok {
						err = errors.New("not found")
					}
					return err
				})
				if err == nil && ref.Refs <= 0 {
					m.deleteSlice(s.Id, s.Size)
					count++
				}
			}
		}
		if len(result) < batch || time.Since(start) > 50*time.Minute {
			break
		}
	}
	return count, nil
}

func (m *dbMeta) compactChunk(inode Ino, indx uint32, force bool) {
	// avoid too many or duplicated compaction
	k := uint64(inode) + (uint64(indx) << 32)
	m.Lock()
	if force {
		for m.compacting[k] {
			m.Unlock()
			time.Sleep(time.Millisecond * 10)
			m.Lock()
		}
	} else if len(m.compacting) > 10 || m.compacting[k] {
		m.Unlock()
		return
	} else {
		m.compacting[k] = true
		defer func() {
			m.Lock()
			delete(m.compacting, k)
			m.Unlock()
		}()
	}
	m.Unlock()

	var c = chunk{Inode: inode, Indx: indx}
	err := m.roTxn(func(s *xorm.Session) error {
		_, err := s.MustCols("indx").Get(&c)
		return err
	})
	if err != nil {
		return
	}
	if len(c.Slices) > sliceBytes*maxCompactSlices {
		c.Slices = c.Slices[:sliceBytes*maxCompactSlices]
	}

	ss := readSliceBuf(c.Slices)
	if ss == nil {
		logger.Errorf("Corrupt value for inode %d chunk indx %d", inode, indx)
		return
	}
	skipped := skipSome(ss)
	var first, last *slice
	if skipped > 0 {
		first, last = ss[0], ss[skipped-1]
	}
	ss = ss[skipped:]
	pos, size, slices := compactChunk(ss)
	if len(ss) < 2 || size == 0 {
		return
	}
	if first != nil && last != nil && pos+size > first.pos && last.pos+last.len > pos {
		panic(fmt.Sprintf("invalid compaction: skipped slices [%+v, %+v], pos %d, size %d", *first, *last, pos, size))
	}

	var id uint64
	st := m.NewSlice(Background, &id)
	if st != 0 {
		return
	}
	logger.Debugf("compact %d:%d: skipped %d slices (%d bytes) %d slices (%d bytes)", inode, indx, skipped, pos, len(ss), size)
	err = m.newMsg(CompactChunk, slices, id)
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
			if s.id > 0 {
				buf = append(buf, m.encodeDelayedSlice(s.id, s.size)...)
			}
		}
	}
	err = m.txn(func(s *xorm.Session) error {
		var c2 = chunk{Inode: inode, Indx: indx}
		_, err := s.ForUpdate().MustCols("indx").Get(&c2)
		if err != nil {
			return err
		}
		if len(c2.Slices) < len(c.Slices) || !bytes.Equal(c.Slices, c2.Slices[:len(c.Slices)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(c.Slices), len(c2.Slices))
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped*sliceBytes], marshalSlice(pos, id, size, 0, size)...), c2.Slices[len(c.Slices):]...)
		if _, err := s.Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if err = mustInsert(s, sliceRef{id, size, 1}); err != nil {
			return err
		}
		if trash {
			if len(buf) > 0 {
				if err = mustInsert(s, &delslices{id, time.Now().Unix(), buf}); err != nil {
					return err
				}
			}
		} else {
			for _, s_ := range ss {
				if s_.id == 0 {
					continue
				}
				if _, err := s.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s_.id, s_.size); err != nil {
					return err
				}
			}
		}
		return nil
	})
	// there could be false-negative that the compaction is successful, double-check
	if err != nil {
		var c = sliceRef{Id: id}
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
				logger.Infof("compacted chunk %d was not used", id)
				err = syscall.EINVAL
			}
		}
	}

	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EINVAL {
		logger.Infof("compaction for %d:%d is wasted, delete slice %d (%d bytes)", inode, indx, id, size)
		m.deleteSlice(id, size)
	} else if err == nil {
		m.of.InvalidateChunk(inode, indx)
		if !trash {
			for _, s := range ss {
				if s.id == 0 {
					continue
				}
				var ref = sliceRef{Id: s.id}
				var ok bool
				err := m.roTxn(func(s *xorm.Session) error {
					var e error
					ok, e = s.Get(&ref)
					return e
				})
				if err == nil && ok && ref.Refs <= 0 {
					m.deleteSlice(s.id, s.size)
				}
			}
		}
	} else {
		logger.Warnf("compact %d %d: %s", inode, indx, err)
	}

	if force {
		m.compactChunk(inode, indx, force)
	}
}

func dup(b []byte) []byte {
	r := make([]byte, len(b))
	copy(r, b)
	return r
}

func (m *dbMeta) scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error {
	return m.roTxn(func(s *xorm.Session) error {
		return s.Table(&chunk{}).Iterate(new(chunk), func(idx int, bean interface{}) error {
			c := bean.(*chunk)
			if len(c.Slices) > sliceBytes {
				bar.IncrTotal(1)
				ch <- cchunk{c.Inode, c.Indx, len(c.Slices) / sliceBytes}
			}
			return nil
		})
	})
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
			if ss == nil {
				logger.Errorf("Corrupt value for inode %d chunk index %d", c.Inode, c.Indx)
				continue
			}
			for _, s := range ss {
				if s.id > 0 {
					slices[c.Inode] = append(slices[c.Inode], Slice{Id: s.id, Size: s.size})
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
	if m.getFormat().TrashDays == 0 {
		return 0
	}

	return errno(m.scanTrashSlices(ctx, func(ss []Slice, _ int64) (bool, error) {
		slices[1] = append(slices[1], ss...)
		if showProgress != nil {
			for range ss {
				showProgress()
			}
		}
		return false, nil
	}))
}

func (m *dbMeta) scanTrashSlices(ctx Context, scan trashSliceScan) error {
	if scan == nil {
		return nil
	}
	var dss []delslices

	err := m.roTxn(func(tx *xorm.Session) error {
		if ok, err := tx.IsTableExist(&delslices{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		return tx.Find(&dss)
	})
	if err != nil {
		return err
	}
	var ss []Slice
	for _, ds := range dss {
		var clean bool
		err = m.txn(func(tx *xorm.Session) error {
			ss = ss[:0]
			del := delslices{Id: ds.Id}
			found, err := tx.Get(&del)
			if err != nil {
				return errors.Wrapf(err, "get delslices %d", ds.Id)
			}
			if !found {
				return nil
			}
			m.decodeDelayedSlices(del.Slices, &ss)
			clean, err = scan(ss, del.Deleted)
			if err != nil {
				return err
			}
			if clean {
				for _, s := range ss {
					if _, e := tx.Exec("update jfs_chunk_ref set refs=refs-1 where chunkid=? and size=?", s.Id, s.Size); e != nil {
						return e
					}
				}
				_, err = tx.Delete(del)
			}
			return err
		})
		if err != nil {
			return err
		}
		if clean {
			for _, s := range ss {
				var ref = sliceRef{Id: s.Id}
				err := m.roTxn(func(tx *xorm.Session) error {
					ok, err := tx.Get(&ref)
					if err == nil && !ok {
						err = errors.New("not found")
					}
					return err
				})
				if err == nil && ref.Refs <= 0 {
					m.deleteSlice(s.Id, s.Size)
				}
			}
		}
	}
	return nil
}

func (m *dbMeta) scanPendingSlices(ctx Context, scan pendingSliceScan) error {
	if scan == nil {
		return nil
	}
	var refs []sliceRef
	err := m.roTxn(func(tx *xorm.Session) error {
		if ok, err := tx.IsTableExist(&sliceRef{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		return tx.Where("refs <= 0").Find(&refs)
	})
	if err != nil {
		return errors.Wrap(err, "scan slice refs")
	}
	for _, ref := range refs {
		clean, err := scan(ref.Id, ref.Size)
		if err != nil {
			return errors.Wrap(err, "scan slice")
		}
		if clean {
			// TODO: m.deleteSlice(ref.Id, ref.Size)
			// avoid lint warning
			_ = clean
		}
	}
	return nil
}

func (m *dbMeta) scanPendingFiles(ctx Context, scan pendingFileScan) error {
	if scan == nil {
		return nil
	}

	var dfs []delfile
	err := m.roTxn(func(s *xorm.Session) error {
		if ok, err := s.IsTableExist(&delfile{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		return s.Find(&dfs)
	})
	if err != nil {
		return err
	}
	for _, ds := range dfs {
		clean, err := scan(ds.Inode, ds.Length, ds.Expire)
		if err != nil {
			return err
		}
		if clean {
			m.doDeleteFileData(ds.Inode, ds.Length)
		}
	}
	return nil
}

func (m *dbMeta) doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	n := &node{
		Inode:     inode,
		Type:      attr.Typ,
		Mode:      attr.Mode,
		Uid:       attr.Uid,
		Gid:       attr.Gid,
		Atime:     attr.Atime*1e6 + int64(attr.Atimensec)/1e3,
		Mtime:     attr.Mtime*1e6 + int64(attr.Mtimensec)/1e3,
		Ctime:     attr.Ctime*1e6 + int64(attr.Ctimensec)/1e3,
		Atimensec: int16(attr.Atimensec % 1e3),
		Mtimensec: int16(attr.Mtimensec % 1e3),
		Ctimensec: int16(attr.Ctimensec % 1e3),
		Length:    attr.Length,
		Parent:    attr.Parent,
		Nlink:     attr.Nlink,
	}
	return errno(m.txn(func(s *xorm.Session) error {
		n.Nlink = 2
		var rows []edge
		if err := s.Find(&rows, &edge{Parent: inode}); err != nil {
			return err
		}
		for _, row := range rows {
			if row.Type == TypeDirectory {
				n.Nlink++
			}
		}
		ok, err := s.ForUpdate().Get(&node{Inode: inode})
		if err == nil {
			if ok {
				_, err = s.Update(n, &node{Inode: inode})
			} else {
				err = mustInsert(s, n)
			}
		}
		return err
	}, inode))
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	defer m.timeit("GetXattr", time.Now())
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
	defer m.timeit("ListXattr", time.Now())
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

func (m *dbMeta) doGetQuota(ctx Context, inode Ino) (*Quota, error) {
	var quota *Quota
	return quota, m.roTxn(func(s *xorm.Session) error {
		q := dirQuota{Inode: inode}
		ok, e := s.Get(&q)
		if e == nil && ok {
			quota = &Quota{
				MaxSpace:   q.MaxSpace,
				MaxInodes:  q.MaxInodes,
				UsedSpace:  q.UsedSpace,
				UsedInodes: q.UsedInodes}
		}
		return e
	})
}

func (m *dbMeta) doSetQuota(ctx Context, inode Ino, quota *Quota) (bool, error) {
	var created bool
	err := m.txn(func(s *xorm.Session) error {
		origin := dirQuota{Inode: inode}
		exist, e := s.ForUpdate().Get(&origin)
		if e != nil {
			return e
		}
		if exist {
			created = false
		} else if quota.MaxSpace < 0 && quota.MaxInodes < 0 {
			return errors.Errorf("limitation not set or deleted")
		} else {
			created = true
		}
		updateColumns := make([]string, 0, 4)
		if quota.MaxSpace >= 0 {
			origin.MaxSpace = quota.MaxSpace
			updateColumns = append(updateColumns, "max_space")
		}
		if quota.MaxInodes >= 0 {
			origin.MaxInodes = quota.MaxInodes
			updateColumns = append(updateColumns, "max_inodes")
		}
		if quota.UsedSpace >= 0 {
			origin.UsedSpace = quota.UsedSpace
			updateColumns = append(updateColumns, "used_space")
		}
		if quota.UsedInodes >= 0 {
			origin.UsedInodes = quota.UsedInodes
			updateColumns = append(updateColumns, "used_inodes")
		}
		if exist {
			_, e = s.Cols(updateColumns...).Update(&origin, &dirQuota{Inode: inode})
		} else {
			e = mustInsert(s, &origin)
		}
		return e
	})
	return created, err
}

func (m *dbMeta) doDelQuota(ctx Context, inode Ino) error {
	return m.txn(func(s *xorm.Session) error {
		_, e := s.Delete(&dirQuota{Inode: inode})
		return e
	})
}

func (m *dbMeta) doLoadQuotas(ctx Context) (map[Ino]*Quota, error) {
	var rows []dirQuota
	err := m.roTxn(func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}

	quotas := make(map[Ino]*Quota, len(rows))
	for _, row := range rows {
		quotas[row.Inode] = &Quota{
			MaxSpace:   row.MaxSpace,
			MaxInodes:  row.MaxInodes,
			UsedSpace:  row.UsedSpace,
			UsedInodes: row.UsedInodes,
		}
	}
	return quotas, nil
}

func (m *dbMeta) doFlushQuotas(ctx Context, quotas map[Ino]*Quota) error {
	return m.txn(func(s *xorm.Session) error {
		for ino, q := range quotas {
			_, err := s.Exec("update jfs_dir_quota set used_space=used_space+?, used_inodes=used_inodes+? where inode=?",
				q.newSpace, q.newInodes, ino)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *dbMeta) dumpEntry(s *xorm.Session, inode Ino, typ uint8) (*DumpedEntry, error) {
	e := &DumpedEntry{}
	n := &node{Inode: inode}
	ok, err := s.Get(n)
	if err != nil {
		return nil, err
	}
	attr := &Attr{Typ: typ, Nlink: 1}
	if !ok {
		logger.Warnf("The entry of the inode was not found. inode: %d", inode)
	} else {
		m.parseAttr(n, attr)
	}
	e.Attr = &DumpedAttr{}
	dumpAttr(attr, e.Attr)
	e.Attr.Inode = inode

	var rows []xattr
	if err = s.Find(&rows, &xattr{Inode: inode}); err != nil {
		return nil, err
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
			if ok, err = s.MustCols("indx").Get(c); err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			ss := readSliceBuf(c.Slices)
			if ss == nil {
				logger.Errorf("Corrupt value for inode %d chunk index %d", inode, indx)
			}
			slices := make([]*DumpedSlice, 0, len(ss))
			for _, s := range ss {
				slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
			}
			e.Chunks = append(e.Chunks, &DumpedChunk{indx, slices})
		}
	} else if attr.Typ == TypeSymlink {
		l := &symlink{Inode: inode}
		ok, err = s.Get(l)
		if err != nil {
			return nil, err
		}
		if !ok {
			logger.Warnf("no link target for inode %d", inode)
		}
		e.Symlink = string(l.Target)
	}
	return e, nil
}

func (m *dbMeta) dumpEntryFast(s *xorm.Session, inode Ino, typ uint8) *DumpedEntry {
	e := &DumpedEntry{}
	n, ok := m.snap.node[inode]
	if !ok && inode != TrashInode {
		logger.Errorf("Corrupt inode: %d, missing attribute", inode)
	}

	attr := &Attr{Typ: typ, Nlink: 1}
	m.parseAttr(n, attr)
	e.Attr = &DumpedAttr{}
	dumpAttr(attr, e.Attr)
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
				continue
			}
			ss := readSliceBuf(c.Slices)
			if ss == nil {
				logger.Errorf("Corrupt value for inode %d chunk index %d", inode, indx)
			}
			slices := make([]*DumpedSlice, 0, len(ss))
			for _, s := range ss {
				slices = append(slices, &DumpedSlice{Id: s.id, Pos: s.pos, Size: s.size, Off: s.off, Len: s.len})
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

func (m *dbMeta) dumpDir(s *xorm.Session, inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
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
		err = s.Find(&edges, &edge{Parent: inode})
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
			entry = m.dumpEntryFast(s, e.Inode, e.Type)
		} else {
			entry, err = m.dumpEntry(s, e.Inode, e.Type)
			if err != nil {
				return err
			}
		}

		if entry == nil {
			logger.Warnf("ignore broken entry %s (inode: %d) in %s", string(e.Name), e.Inode, inode)
			continue
		}

		entry.Name = string(e.Name)
		if e.Type == TypeDirectory {
			err = m.dumpDir(s, e.Inode, entry, bw, depth+2, showProgress)
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

func (m *dbMeta) makeSnap(ses *xorm.Session, bar *utils.Bar) error {
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

	if err := ses.Table(&node{}).Iterate(new(node), func(idx int, bean interface{}) error {
		n := bean.(*node)
		snap.node[n.Inode] = n
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := ses.Table(&symlink{}).Iterate(new(symlink), func(idx int, bean interface{}) error {
		s := bean.(*symlink)
		snap.symlink[s.Inode] = s
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}
	if err := ses.Table(&edge{}).Iterate(new(edge), func(idx int, bean interface{}) error {
		e := bean.(*edge)
		snap.edges[e.Parent] = append(snap.edges[e.Parent], e)
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := ses.Table(&xattr{}).Iterate(new(xattr), func(idx int, bean interface{}) error {
		x := bean.(*xattr)
		snap.xattr[x.Inode] = append(snap.xattr[x.Inode], x)
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	if err := ses.Table(&chunk{}).Iterate(new(chunk), func(idx int, bean interface{}) error {
		c := bean.(*chunk)
		snap.chunk[fmt.Sprintf("%d-%d", c.Inode, c.Indx)] = c
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}
	m.snap = snap
	return nil
}

func (m *dbMeta) DumpMeta(w io.Writer, root Ino, keepSecret bool) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("DumpMeta error: %v", p)
			}
		}
	}()

	progress := utils.NewProgress(false)
	var tree, trash *DumpedEntry
	root = m.checkRoot(root)
	return m.roTxn(func(s *xorm.Session) error {
		if root == RootInode {
			defer func() { m.snap = nil }()
			bar := progress.AddCountBar("Snapshot keys", 0)
			if err = m.makeSnap(s, bar); err != nil {
				return fmt.Errorf("Fetch all metadata from DB: %s", err)
			}
			bar.Done()
			tree = m.dumpEntryFast(s, root, TypeDirectory)
			trash = m.dumpEntryFast(s, TrashInode, TypeDirectory)
		} else {
			if tree, err = m.dumpEntry(s, root, TypeDirectory); err != nil {
				return err
			}
		}
		if tree == nil {
			return errors.New("The entry of the root inode was not found")
		}
		tree.Name = "FSTree"

		var drows []delfile
		// the statement remembers the table of last Iterator
		if err := s.Table(&delfile{}).Find(&drows); err != nil {
			return err
		}
		dels := make([]*DumpedDelFile, 0, len(drows))
		for _, row := range drows {
			dels = append(dels, &DumpedDelFile{row.Inode, row.Length, row.Expire})
		}
		var crows []counter
		if err = s.Find(&crows); err != nil {
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
		if err := s.Find(&srows); err != nil {
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

		var qs []dirQuota
		if err := s.Find(&qs); err != nil {
			return err
		}
		dumpedQuotas := make(map[Ino]*DumpedQuota, len(qs))
		for _, q := range qs {
			dumpedQuotas[q.Inode] = &DumpedQuota{q.MaxSpace, q.MaxInodes, 0, 0}
		}

		dm := DumpedMeta{
			Setting:   *m.getFormat(),
			Counters:  counters,
			Sustained: sessions,
			DelFiles:  dels,
			Quotas:    dumpedQuotas,
		}
		if !keepSecret && dm.Setting.SecretKey != "" {
			dm.Setting.SecretKey = "removed"
			logger.Warnf("Secret key is removed for the sake of safety")
		}
		if !keepSecret && dm.Setting.SessionToken != "" {
			dm.Setting.SessionToken = "removed"
			logger.Warnf("Session token is removed for the sake of safety")
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
		if err = m.dumpDir(s, root, tree, bw, 1, showProgress); err != nil {
			logger.Errorf("dump dir %d failed: %s", root, err)
			return fmt.Errorf("dump dir %d failed", root) // don't retry
		}
		if trash != nil {
			if _, err = bw.WriteString(","); err != nil {
				return err
			}
			if err = m.dumpDir(s, TrashInode, trash, bw, 1, showProgress); err != nil {
				logger.Errorf("dump trash failed: %s", err)
				return fmt.Errorf("dump trash failed") // don't retry
			}
		}
		if _, err = bw.WriteString("\n}\n"); err != nil {
			return err
		}
		progress.Done()
		return bw.Flush()
	})
}

func (m *dbMeta) loadEntry(e *DumpedEntry, chs []chan interface{}) {
	inode := e.Attr.Inode
	attr := e.Attr
	n := &node{
		Inode:     inode,
		Flags:     attr.Flags,
		Type:      typeFromString(attr.Type),
		Mode:      attr.Mode,
		Uid:       attr.Uid,
		Gid:       attr.Gid,
		Atime:     attr.Atime*1e6 + int64(attr.Atimensec)/1e3,
		Mtime:     attr.Mtime*1e6 + int64(attr.Mtimensec)/1e3,
		Ctime:     attr.Ctime*1e6 + int64(attr.Ctimensec)/1e3,
		Atimensec: int16(attr.Atimensec % 1e3),
		Mtimensec: int16(attr.Mtimensec % 1e3),
		Ctimensec: int16(attr.Ctimensec % 1e3),
		Nlink:     attr.Nlink,
		Rdev:      attr.Rdev,
		Parent:    e.Parents[0],
	} // Length not set

	// chs: node, edge, chunk, chunkRef, xattr, others
	if n.Type == TypeFile {
		n.Length = attr.Length
		for _, c := range e.Chunks {
			if len(c.Slices) == 0 {
				continue
			}
			slices := make([]byte, 0, sliceBytes*len(c.Slices))
			for _, s := range c.Slices {
				slices = append(slices, marshalSlice(s.Pos, s.Id, s.Size, s.Off, s.Len)...)
			}
			chs[2] <- &chunk{Inode: inode, Indx: c.Index, Slices: slices}
		}
	} else if n.Type == TypeDirectory {
		n.Length = 4 << 10
		stat := &dirStats{Inode: inode}
		for name, c := range e.Entries {
			length := uint64(0)
			if typeFromString(c.Attr.Type) == TypeFile {
				length = c.Attr.Length
			}
			stat.DataLength += int64(length)
			stat.UsedSpace += align4K(length)
			stat.UsedInodes++

			chs[1] <- &edge{
				Parent: inode,
				Name:   unescape(name),
				Inode:  c.Attr.Inode,
				Type:   typeFromString(c.Attr.Type),
			}
		}
		chs[5] <- stat
	} else if n.Type == TypeSymlink {
		symL := unescape(e.Symlink)
		n.Length = uint64(len(symL))
		chs[5] <- &symlink{inode, symL}
	}
	for _, x := range e.Xattrs {
		chs[4] <- &xattr{Inode: inode, Name: x.Name, Value: unescape(x.Value)}
	}
	chs[0] <- n
}

func (m *dbMeta) LoadMeta(r io.Reader) error {
	tables, err := m.db.DBMetas()
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		addr := m.addr
		if !strings.Contains(addr, "://") {
			addr = fmt.Sprintf("%s://%s", m.Name(), addr)
		}
		return fmt.Errorf("Database %s is not empty", addr)
	}
	if err = m.syncTable(new(setting), new(counter)); err != nil {
		return fmt.Errorf("create table setting, counter: %s", err)
	}
	if err = m.syncTable(new(node), new(edge), new(symlink), new(xattr)); err != nil {
		return fmt.Errorf("create table node, edge, symlink, xattr: %s", err)
	}
	if err = m.syncTable(new(chunk), new(sliceRef), new(delslices)); err != nil {
		return fmt.Errorf("create table chunk, chunk_ref, delslices: %s", err)
	}
	if err = m.syncTable(new(session2), new(sustained), new(delfile)); err != nil {
		return fmt.Errorf("create table session2, sustaind, delfile: %s", err)
	}
	if err = m.syncTable(new(flock), new(plock), new(dirQuota)); err != nil {
		return fmt.Errorf("create table flock, plock, dirQuota: %s", err)
	}
	if err := m.syncTable(new(dirStats)); err != nil {
		return fmt.Errorf("create table dirStats: %s", err)
	}
	if err := m.syncTable(new(detachedNode)); err != nil {
		return fmt.Errorf("create table detachedNode: %s", err)
	}
	var batch int
	switch m.Name() {
	case "sqlite3":
		batch = 999 / MaxFieldsCountOfTable
	case "mysql":
		batch = 65535 / MaxFieldsCountOfTable
	case "postgres":
		batch = 1000
	}
	chs := make([]chan interface{}, 6) // node, edge, chunk, chunkRef, xattr, others
	insert := func(index int, beans []interface{}) error {
		return m.txn(func(s *xorm.Session) error {
			var n int64
			var err error
			if index == len(chs)-1 { // multiple tables
				n, err = s.Insert(beans...)
			} else { // one table only
				n, err = s.Insert(beans)
			}
			if err == nil && int(n) != len(beans) {
				err = fmt.Errorf("only %d records inserted", n)
			}
			return err
		})
	}
	var wg sync.WaitGroup
	for i := range chs {
		chs[i] = make(chan interface{}, batch*2)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			buffer := make([]interface{}, 0, batch)
			for bean := range chs[i] {
				buffer = append(buffer, bean)
				if len(buffer) >= batch {
					if err := insert(i, buffer); err != nil {
						logger.Fatalf("Write %d beans in channel %d: %s", len(buffer), i, err)
					}
					buffer = buffer[:0]
				}
			}
			if len(buffer) > 0 {
				if err := insert(i, buffer); err != nil {
					logger.Fatalf("Write %d beans in channel %d: %s", len(buffer), i, err)
				}
			}
		}(i)
	}

	dm, counters, parents, refs, err := loadEntries(r,
		func(e *DumpedEntry) { m.loadEntry(e, chs) },
		func(ck *chunkKey) { chs[3] <- &sliceRef{ck.id, ck.size, 1} })
	if err != nil {
		return err
	}
	m.loadDumpedQuotas(Background, dm.Quotas)
	format, _ := json.MarshalIndent(dm.Setting, "", "")
	chs[5] <- &setting{"format", string(format)}
	chs[5] <- &counter{usedSpace, counters.UsedSpace}
	chs[5] <- &counter{totalInodes, counters.UsedInodes}
	chs[5] <- &counter{"nextInode", counters.NextInode}
	chs[5] <- &counter{"nextChunk", counters.NextChunk}
	chs[5] <- &counter{"nextSession", counters.NextSession}
	chs[5] <- &counter{"nextTrash", counters.NextTrash}
	for _, d := range dm.DelFiles {
		chs[5] <- &delfile{d.Inode, d.Length, d.Expire}
	}
	for _, c := range chs {
		close(c)
	}
	wg.Wait()

	// update chunkRefs
	if err = m.txn(func(s *xorm.Session) error {
		for k, v := range refs {
			if v > 1 {
				if _, e := s.Cols("refs").Update(&sliceRef{Refs: int(v)}, &sliceRef{Id: k.id}); e != nil {
					return e
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// update nlinks and parents for hardlinks
	return m.txn(func(s *xorm.Session) error {
		for i, ps := range parents {
			if len(ps) > 1 {
				_, err := s.Cols("nlink", "parent").Update(&node{Nlink: uint32(len(ps))}, &node{Inode: i})
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type checkDupError func(error) bool

var dupErrorCheckers []checkDupError

func isDuplicateEntryErr(err error) bool {
	for _, check := range dupErrorCheckers {
		if check(err) {
			return true
		}
	}
	return false
}

func (m *dbMeta) doCloneEntry(ctx Context, srcIno Ino, parent Ino, name string, ino Ino, attr *Attr, cmode uint8, cumask uint16, top bool) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		n := node{Inode: srcIno}
		ok, err := s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		n.Inode = ino
		n.Parent = parent
		now := time.Now()
		if cmode&CLONE_MODE_PRESERVE_ATTR == 0 {
			n.Uid = ctx.Uid()
			n.Gid = ctx.Gid()
			n.Mode &= ^cumask
			n.Atime = now.UnixNano() / 1e3
			n.Mtime = now.UnixNano() / 1e3
			n.Ctime = now.UnixNano() / 1e3
			n.Atimensec = int16(now.UnixNano() % 1e3)
			n.Mtimensec = int16(now.UnixNano() % 1e3)
			n.Ctimensec = int16(now.UnixNano() % 1e3)
		}
		// TODO: preserve hardlink
		if n.Type == TypeFile && n.Nlink > 1 {
			n.Nlink = 1
		}
		m.parseAttr(&n, attr)
		if eno := m.Access(ctx, srcIno, MODE_MASK_R, attr); eno != 0 {
			return eno
		}

		if top {
			var pattr Attr
			var pn = node{Inode: parent}
			if exist, err := s.Get(&pn); err != nil {
				return err
			} else if !exist {
				return syscall.ENOENT
			}
			m.parseAttr(&pn, &pattr)
			if pattr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			if (pattr.Flags & FlagImmutable) != 0 {
				return syscall.EPERM
			}
			if eno := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); eno != 0 {
				return eno
			}
			if n.Type != TypeDirectory {
				now := time.Now().UnixNano()
				pn.Mtime = now / 1e3
				pn.Ctime = now / 1e3
				pn.Mtimensec = int16(now % 1e3)
				pn.Ctimensec = int16(now % 1e3)
				if _, err = s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: parent}); err != nil {
					return err
				}
			}
		}
		if top && n.Type == TypeDirectory {
			err = mustInsert(s, &n, &detachedNode{Inode: ino, Added: time.Now().Unix()})
		} else {
			err = mustInsert(s, &n, &edge{Parent: parent, Name: []byte(name), Inode: ino, Type: n.Type})
			if isDuplicateEntryErr(err) {
				return syscall.EEXIST
			}
		}
		if err != nil {
			return err
		}
		var xs []xattr
		if err = s.Where("inode = ?", srcIno).Find(&xs, &xattr{Inode: srcIno}); err != nil {
			return err
		}
		if len(xs) > 0 {
			for i := range xs {
				xs[i].Id = 0
				xs[i].Inode = ino
			}
			if err := mustInsert(s, &xs); err != nil {
				return err
			}
		}
		switch n.Type {
		case TypeDirectory:
			var st = dirStats{Inode: srcIno}
			if exist, err := s.Get(&st); err != nil {
				return err
			} else if exist {
				st.Inode = ino
				if err := mustInsert(s, &st); err != nil {
					return err
				}
			}
		case TypeFile:
			// copy chunks
			if n.Length != 0 {
				var cs []chunk
				if err = s.Where("inode = ?", srcIno).ForUpdate().Find(&cs); err != nil {
					return err
				}
				for i := range cs {
					cs[i].Id = 0
					cs[i].Inode = ino
				}
				if len(cs) != 0 {
					if err := mustInsert(s, cs); err != nil {
						return err
					}
				}
				// TODO: batch?
				for _, c := range cs {
					for _, sli := range readSliceBuf(c.Slices) {
						if sli.id > 0 {
							if _, err := s.Exec("update jfs_chunk_ref set refs=refs+1 where chunkid = ? AND size = ?", sli.id, sli.size); err != nil {
								return err
							}
						}
					}
				}
			}
		case TypeSymlink:
			sym := symlink{Inode: srcIno}
			if exists, err := s.Get(&sym); err != nil {
				return err
			} else if !exists {
				return syscall.ENOENT
			}
			sym.Inode = ino
			return mustInsert(s, &sym)
		}
		return nil
	}, srcIno))
}

func (m *dbMeta) doFindDetachedNodes(t time.Time) []Ino {
	var inodes []Ino
	err := m.roTxn(func(s *xorm.Session) error {
		var nodes []detachedNode
		err := s.Where("added < ?", t.Unix()).Find(&nodes)
		for _, n := range nodes {
			inodes = append(inodes, n.Inode)
		}
		return err
	})
	if err != nil {
		logger.Errorf("Scan detached nodes error: %s", err)
	}
	return inodes
}

func (m *dbMeta) doCleanupDetachedNode(ctx Context, ino Ino) syscall.Errno {
	exist, err := m.db.Exist(&node{Inode: ino})
	if err != nil || !exist {
		return errno(err)
	}
	rmConcurrent := make(chan int, 10)
	if eno := m.emptyDir(ctx, ino, true, nil, rmConcurrent); eno != 0 {
		return eno
	}
	m.updateStats(-align4K(0), -1)
	return errno(m.txn(func(s *xorm.Session) error {
		if _, err := s.Delete(&node{Inode: ino}); err != nil {
			return err
		}
		if _, err := s.Delete(&dirStats{Inode: ino}); err != nil {
			return err
		}
		if _, err = s.Delete(&xattr{Inode: ino}); err != nil {
			return err
		}
		_, err = s.Delete(&detachedNode{Inode: ino})
		return err
	}, ino))
}

func (m *dbMeta) doAttachDirNode(ctx Context, parent Ino, inode Ino, name string) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		// must lock parent node first to avoid deadlock
		var n = node{Inode: parent}
		ok, err := s.ForUpdate().Get(&n)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if n.Type != TypeDirectory {
			return syscall.ENOTDIR
		}
		if n.Parent > TrashInode {
			return syscall.ENOENT
		}
		if (n.Flags & FlagImmutable) != 0 {
			return syscall.EPERM
		}
		n.Nlink++
		now := time.Now().UnixNano()
		n.Mtime = now / 1e3
		n.Ctime = now / 1e3
		n.Mtimensec = int16(now % 1e3)
		n.Ctimensec = int16(now % 1e3)
		if _, err = s.Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&n, &node{Inode: parent}); err != nil {
			return err
		}
		if err := mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: inode, Type: TypeDirectory}); err != nil {
			if isDuplicateEntryErr(err) {
				return syscall.EEXIST
			}
			return err
		}
		_, err = s.Delete(&detachedNode{Inode: inode})
		return err
	}, parent))
}

func (m *dbMeta) doTouchAtime(ctx Context, inode Ino, attr *Attr, now time.Time) (bool, error) {
	var updated bool
	err := m.txn(func(s *xorm.Session) error {
		curNode := node{Inode: inode}
		ok, err := s.ForUpdate().Get(&curNode)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		m.parseAttr(&curNode, attr)
		if !m.atimeNeedsUpdate(attr, now) {
			return nil
		}
		curNode.Atime = now.Unix()*1e6 + int64(now.Nanosecond())/1e3
		curNode.Atimensec = int16(now.Nanosecond() % 1e3)
		attr.Atime = curNode.Atime / 1e6
		attr.Atimensec = uint32(curNode.Atime%1e6*1000) + uint32(curNode.Atimensec)
		if _, err = s.Cols("atime", "atimensec").Update(&curNode, &node{Inode: inode}); err == nil {
			updated = true
		}
		return err
	}, inode)
	return updated, err
}
