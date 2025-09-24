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
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"xorm.io/xorm"
	"xorm.io/xorm/log"
	"xorm.io/xorm/names"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const MaxFieldsCountOfTable = 18 // node table

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
	Inode        Ino    `xorm:"pk"`
	Type         uint8  `xorm:"notnull"`
	Flags        uint8  `xorm:"notnull"`
	Mode         uint16 `xorm:"notnull"`
	Uid          uint32 `xorm:"notnull"`
	Gid          uint32 `xorm:"notnull"`
	Atime        int64  `xorm:"notnull"`
	Mtime        int64  `xorm:"notnull"`
	Ctime        int64  `xorm:"notnull"`
	Atimensec    int16  `xorm:"notnull default 0"`
	Mtimensec    int16  `xorm:"notnull default 0"`
	Ctimensec    int16  `xorm:"notnull default 0"`
	Nlink        uint32 `xorm:"notnull"`
	Length       uint64 `xorm:"notnull"`
	Rdev         uint32
	Parent       Ino
	AccessACLId  uint32 `xorm:"'access_acl_id'"`
	DefaultACLId uint32 `xorm:"'default_acl_id'"`
}

func (n *node) setAtime(ns int64) {
	n.Atime = ns / 1e3
	n.Atimensec = int16(ns % 1e3)
}

func (n *node) getMtime() int64 {
	return n.Mtime*1e3 + int64(n.Mtimensec)
}

func (n *node) setMtime(ns int64) {
	n.Mtime = ns / 1e3
	n.Mtimensec = int16(ns % 1e3)
}

func (n *node) setCtime(ns int64) {
	n.Ctime = ns / 1e3
	n.Ctimensec = int16(ns % 1e3)
}

func getACLIdColName(aclType uint8) string {
	switch aclType {
	case aclAPI.TypeAccess:
		return "access_acl_id"
	case aclAPI.TypeDefault:
		return "default_acl_id"
	}
	return ""
}

type acl struct {
	Id          uint32 `xorm:"pk autoincr"`
	Owner       uint16
	Group       uint16
	Mask        uint16
	Other       uint16
	NamedUsers  []byte
	NamedGroups []byte
}

func newSQLAcl(r *aclAPI.Rule) *acl {
	a := &acl{
		Owner: r.Owner,
		Group: r.Group,
		Mask:  r.Mask,
		Other: r.Other,
	}
	a.NamedUsers = r.NamedUsers.Encode()
	a.NamedGroups = r.NamedGroups.Encode()
	return a
}

func (a *acl) toRule() *aclAPI.Rule {
	r := &aclAPI.Rule{}
	r.Owner = a.Owner
	r.Group = a.Group
	r.Other = a.Other
	r.Mask = a.Mask
	r.NamedUsers.Decode(a.NamedUsers)
	r.NamedGroups.Decode(a.NamedGroups)
	return r
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

type userGroupQuota struct {
	Qtype      uint32 `xorm:"pk notnull"` // 1 for user, 2 for group
	Qkey       uint64 `xorm:"pk notnull"` // uid or gid
	MaxSpace   int64  `xorm:"notnull"`
	MaxInodes  int64  `xorm:"notnull"`
	UsedSpace  int64  `xorm:"notnull"`
	UsedInodes int64  `xorm:"notnull"`
}

type dbMeta struct {
	*baseMeta
	db    *xorm.Engine
	spool *sync.Pool
	snap  *dbSnap

	noReadOnlyTxn bool
	statement     map[string]string
	tablePrefix   string
}

var _ Meta = (*dbMeta)(nil)
var _ engine = (*dbMeta)(nil)

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

func extractCustomConfig[T string | int](value *url.Values, key string, defaultV T) (T, error) {
	if value == nil {
		return defaultV, nil
	}
	if v := value.Get(key); v != "" {
		value.Del(key)
		var result T
		switch any(defaultV).(type) {
		case int:
			parsedInt, err := strconv.Atoi(v)
			if err != nil {
				return defaultV, fmt.Errorf("failed to parse value as int: %v", err)
			}
			result = any(parsedInt).(T)
		case string:
			result = any(v).(T)
		default:
			return defaultV, fmt.Errorf("unsupported type: %T", defaultV)
		}
		return result, nil
	} else {
		return defaultV, nil
	}
}

var setTransactionIsolation func(dns string) (string, error)

type prefixMapper struct {
	mapper names.Mapper
	prefix string
}

func (m prefixMapper) Obj2Table(name string) string {
	if name == "sliceRef" {
		return m.prefix + "chunk_ref"
	}
	return m.prefix + m.mapper.Obj2Table(name)
}

func (m prefixMapper) Table2Obj(name string) string {
	if name == m.prefix+"chunk_ref" {
		return "sliceRef"
	}
	return m.mapper.Table2Obj(name[len(m.prefix):])
}
func (m *dbMeta) sqlConv(sql string) string {
	return m.statement[sql]
}

func (m *dbMeta) initStatement() {
	m.statement["SELECT length FROM node WHERE inode IN (SELECT inode FROM sustained)"] =
		fmt.Sprintf("SELECT length FROM %snode WHERE inode IN (SELECT inode FROM %ssustained)", m.tablePrefix, m.tablePrefix)
	m.statement["update counter set value=value + ? where name='totalInodes'"] =
		fmt.Sprintf("update %scounter set value=value + ? where name='totalInodes'", m.tablePrefix)
	m.statement["update counter set value= value + ? where name='usedSpace'"] =
		fmt.Sprintf("update %scounter set value= value + ? where name='usedSpace'", m.tablePrefix)
	m.statement["update chunk set slices=slices || ? where inode=? AND indx=?"] =
		fmt.Sprintf("update %schunk set slices=slices || ? where inode=? AND indx=?", m.tablePrefix)
	m.statement["update chunk set slices=concat(slices, ?) where inode=? AND indx=?"] =
		fmt.Sprintf("update %schunk set slices=concat(slices, ?) where inode=? AND indx=?", m.tablePrefix)
	m.statement["update chunk_ref set refs=refs+1 where chunkid = ? AND size = ?"] =
		fmt.Sprintf("update %schunk_ref set refs=refs+1 where chunkid = ? AND size = ?", m.tablePrefix)
	m.statement["update chunk_ref set refs=refs-1 where chunkid=? AND size=?"] =
		fmt.Sprintf("update %schunk_ref set refs=refs-1 where chunkid=? AND size=?", m.tablePrefix)
	m.statement["update dir_quota set used_space=used_space+?, used_inodes=used_inodes+? where inode=?"] =
		fmt.Sprintf("update %sdir_quota set used_space=used_space+?, used_inodes=used_inodes+? where inode=?", m.tablePrefix)
	m.statement["update user_group_quota set used_space=used_space+?, used_inodes=used_inodes+? where qtype=? and qkey=?"] =
		fmt.Sprintf("update %suser_group_quota set used_space=used_space+?, used_inodes=used_inodes+? where qtype=? and qkey=?", m.tablePrefix)

	m.statement[`
			 INSERT INTO chunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON CONFLICT (inode, indx)
			 DO UPDATE SET slices=chunk.slices || ?`] =
		fmt.Sprintf(`
			 INSERT INTO %schunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON CONFLICT (inode, indx)
			 DO UPDATE SET slices=%schunk.slices || ?`, m.tablePrefix, m.tablePrefix)
	m.statement[`
			 INSERT INTO chunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			 slices=concat(slices, ?)`] =
		fmt.Sprintf(`
			 INSERT INTO %schunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			 slices=concat(slices, ?)`, m.tablePrefix)
	m.statement[`
			 INSERT INTO chunk_ref (chunkid, size, refs)
			 VALUES (?, ?, ?)
			 ON CONFLICT (chunkid)
			 DO UPDATE SET size=?, refs=?`] =
		fmt.Sprintf(`
			 INSERT INTO %schunk_ref (chunkid, size, refs)
			 VALUES (?, ?, ?)
			 ON CONFLICT (chunkid)
			 DO UPDATE SET size=?, refs=?`, m.tablePrefix)
	m.statement[`
			 INSERT INTO chunk_ref (chunkid, size, refs)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			 size=?, refs=?`] =
		fmt.Sprintf(`
			 INSERT INTO %schunk_ref (chunkid, size, refs)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			 size=?, refs=?`, m.tablePrefix)
	m.statement["edge.inode=node.inode"] = fmt.Sprintf("%sedge.inode=%snode.inode", m.tablePrefix, m.tablePrefix)
	m.statement["edge.id"] = fmt.Sprintf("%sedge.id", m.tablePrefix)
	m.statement["edge.name"] = fmt.Sprintf("%sedge.name", m.tablePrefix)
	m.statement["edge.type"] = fmt.Sprintf("%sedge.type", m.tablePrefix)
	m.statement["edge.*"] = fmt.Sprintf("%sedge.*", m.tablePrefix)
	m.statement["node.*"] = fmt.Sprintf("%snode.*", m.tablePrefix)
	m.statement[`INSERT INTO chunk_ref (chunkid, size, refs) VALUES (?,?,?) ON CONFLICT DO NOTHING`] =
		fmt.Sprintf(`INSERT INTO %schunk_ref (chunkid, size, refs) VALUES (?,?,?) ON CONFLICT DO NOTHING`, m.tablePrefix)
	m.statement[`INSERT IGNORE INTO chunk_ref (chunkid, size, refs) VALUES (?,?,?)`] =
		fmt.Sprintf(`INSERT IGNORE INTO %schunk_ref (chunkid, size, refs) VALUES (?,?,?)`, m.tablePrefix)
}

func newSQLMeta(driver, addr string, conf *Config) (Meta, error) {
	var searchPath string

	baseUrl, queryStr, _ := strings.Cut(addr, "?")
	var query url.Values
	var err error
	query, err = url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	var vOpenConns, vIdleConns, vIdleTime, vLifeTime int
	if vOpenConns, err = extractCustomConfig(&query, "max_open_conns", 0); err != nil {
		return nil, err
	}
	if vIdleConns, err = extractCustomConfig(&query, "max_idle_conns", runtime.GOMAXPROCS(-1)*2); err != nil {
		return nil, err
	}
	if vIdleTime, err = extractCustomConfig(&query, "max_idle_time", 300); err != nil {
		return nil, err
	}
	if vLifeTime, err = extractCustomConfig(&query, "max_life_time", 0); err != nil {
		return nil, err
	}
	var tablePrefix string
	if tablePrefix, err = extractCustomConfig(&query, "table_prefix", ""); err != nil {
		return nil, err
	}
	if tablePrefix == "" {
		tablePrefix = "jfs_"
	} else {
		tablePrefix = "jfs_" + tablePrefix + "_"
	}

	if driver == "sqlite3" {
		if !query.Has("cache") {
			query.Add("cache", "shared")
		}
		if !query.Has("_journal") && !query.Has("_journal_mode") {
			query.Add("_journal", "WAL")
		}
		if !query.Has("_timeout") && !query.Has("_busy_timeout") {
			query.Add("_timeout", "5000")
		}
	}

	if encode := query.Encode(); encode != "" {
		addr = fmt.Sprintf("%s?%s", baseUrl, encode)
	} else {
		addr = baseUrl
	}

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
	if driver == "mysql" && setTransactionIsolation != nil {
		addr = recoveryMysqlPwd(addr)
		var err error
		if addr, err = setTransactionIsolation(addr); err != nil {
			return nil, err
		}
	}

	if driver == "sqlite3" {
		DirBatchNum["db"] = 4096 // SQLITE_MAX_VARIABLE_NUMBER limit
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
	if vOpenConns > 0 {
		engine.DB().SetMaxOpenConns(vOpenConns)
	}
	if vLifeTime > 0 {
		engine.DB().SetConnMaxLifetime(time.Second * time.Duration(vLifeTime))
	}
	engine.DB().SetMaxIdleConns(vIdleConns)
	engine.DB().SetConnMaxIdleTime(time.Second * time.Duration(vIdleTime))
	engine.SetTableMapper(prefixMapper{mapper: engine.GetTableMapper(), prefix: tablePrefix})
	m := &dbMeta{
		baseMeta:    newBaseMeta(addr, conf),
		db:          engine,
		statement:   make(map[string]string),
		tablePrefix: tablePrefix,
	}
	m.initStatement()
	m.spool = &sync.Pool{
		New: func() interface{} {
			s := engine.NewSession()
			runtime.SetFinalizer(s, func(s *xorm.Session) {
				_ = s.Close()
			})
			return s
		},
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
		_, err := s.Delete(&sliceRef{Id: id})
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

func (m *dbMeta) syncAllTables() error {
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
	if err := m.syncTable(new(flock), new(plock), new(dirQuota), new(userGroupQuota)); err != nil {
		return fmt.Errorf("create table flock, plock, dirQuota, userGroupQuota: %s", err)
	}
	if err := m.syncTable(new(dirStats)); err != nil {
		return fmt.Errorf("create table dirStats: %s", err)
	}
	if err := m.syncTable(new(detachedNode)); err != nil {
		return fmt.Errorf("create table detachedNode: %s", err)
	}
	if err := m.syncTable(new(acl)); err != nil {
		return fmt.Errorf("create table acl: %s", err)
	}
	return nil
}

func (m *dbMeta) doInit(format *Format, force bool) error {
	if err := m.syncAllTables(); err != nil {
		return err
	}
	var s = setting{Name: "format"}
	var ok bool
	err := m.simpleTxn(Background(), func(ses *xorm.Session) (err error) {
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
		if !old.UserGroupQuota && format.UserGroupQuota {
			// remove user group quota as they are outdated
			_, err = m.db.Where("TRUE").Delete(new(userGroupQuota))
			if err != nil {
				return errors.Wrap(err, "drop table userGroupQuota")
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
	n := &node{
		Type:   TypeDirectory,
		Nlink:  2,
		Length: 4 << 10,
		Parent: 1,
	}
	now := time.Now().UnixNano()
	n.setAtime(now)
	n.setMtime(now)
	n.setCtime(now)
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

func (m *dbMeta) cacheACLs(ctx Context) error {
	if !m.getFormat().EnableACL {
		return nil
	}
	return m.simpleTxn(ctx, func(s *xorm.Session) error {
		return s.Table(&acl{}).Iterate(new(acl), func(idx int, bean interface{}) error {
			a := bean.(*acl)
			m.aclCache.Put(a.Id, a.toRule())
			return nil
		})
	})
}

func (m *dbMeta) Reset() error {
	m.Lock()
	defer m.Unlock()
	return m.db.DropTables(&setting{}, &counter{},
		&node{}, &edge{}, &symlink{}, &xattr{},
		&chunk{}, &sliceRef{}, &delslices{},
		&session{}, &session2{}, &sustained{}, &delfile{},
		&flock{}, &plock{}, &dirStats{}, &dirQuota{}, &userGroupQuota{}, &detachedNode{}, &acl{})
}

func (m *dbMeta) doLoad() (data []byte, err error) {
	err = m.simpleTxn(Background(), func(ses *xorm.Session) error {
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

func (m *dbMeta) doNewSession(sinfo []byte, update bool) error {
	// add new table
	err := m.syncTable(new(session2), new(delslices), new(dirStats), new(detachedNode), new(dirQuota), new(userGroupQuota), new(acl))
	if err != nil {
		return fmt.Errorf("update table session2, delslices, dirstats, detachedNode, dirQuota, userGroupQuota, acl: %s", err)
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
		beans := session2{Sid: m.sid, Expire: m.expireTime(), Info: sinfo}
		if update {
			return m.txn(func(s *xorm.Session) error {
				_, err = s.Cols("expire", "info").Update(&beans, &session2{Sid: beans.Sid})
				return err
			})
		} else {
			if err = m.txn(func(s *xorm.Session) error {
				return mustInsert(s, &beans)
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
		err := m.roTxn(Background(), func(ses *xorm.Session) error {
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
	err = m.roTxn(Background(), func(ses *xorm.Session) error {
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
	err := m.roTxn(Background(), func(ses *xorm.Session) error {
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
	err = m.simpleTxn(Background(), func(s *xorm.Session) error {
		c := counter{Name: name}
		_, err := s.Get(&c)
		if err == nil {
			v = c.Value
		}
		return err
	})
	return
}

func (m *dbMeta) incrCounter(name string, value int64) (v int64, err error) {
	err = m.txn(func(s *xorm.Session) error {
		v, err = m.incrSessionCounter(s, name, value)
		return err
	})
	return
}

func (m *dbMeta) incrSessionCounter(s *xorm.Session, name string, value int64) (v int64, err error) {
	var c = counter{Name: name}
	ok, err := s.ForUpdate().Get(&c)
	if err != nil {
		return
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
	return
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
	if m.Name() == "mysql" && err == syscall.EBUSY {
		// Retry transaction when parent node update return 0 rows in MySQL
		return true
	}

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
		// error 1020 for MariaDB when conflict
		return strings.Contains(msg, "try restarting transaction") || strings.Contains(msg, "try again later") ||
			strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "error 1020 (hy000)") ||
			strings.Contains(msg, "invalid connection") || strings.Contains(msg, "bad connection") || errors.Is(err, io.EOF) // could not send data to client: No buffer space available
	case "postgres":
		if e, ok := err.(interface{ SafeToRetry() bool }); ok {
			return e.SafeToRetry()
		}
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

	defer m.txBatchLock(inodes...)()
	var (
		lastErr error
		method  string
	)
	for i := 0; i < 50; i++ {
		_, err := m.db.Transaction(func(s *xorm.Session) (interface{}, error) {
			return nil, f(s)
		})
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if err != nil && m.shouldRetry(err) {
			if method == "" {
				method = callerName(context.TODO()) // lazy evaluation
			}
			m.txRestart.WithLabelValues(method).Add(1)
			logger.Debugf("Transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Transaction succeeded after %d tries (%s), inodes: %v, method: %s, last error: %s", i+1, time.Since(start), inodes, method, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried 50 times, returning: %s", lastErr)
	return lastErr
}

func (m *dbMeta) roTxn(ctx context.Context, f func(s *xorm.Session) error) error {
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()
	s := m.db.NewSession()
	defer s.Close()
	var opt sql.TxOptions
	if !m.noReadOnlyTxn {
		opt.ReadOnly = true
		opt.Isolation = sql.LevelRepeatableRead
	}

	var maxRetry int
	val := ctx.Value(txMaxRetryKey{})
	if val == nil {
		maxRetry = 50
	} else {
		maxRetry = val.(int)
	}
	var (
		lastErr error
		method  string
	)
	for i := 0; i < maxRetry; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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
			if method == "" {
				method = callerName(ctx) // lazy evaluation
			}
			m.txRestart.WithLabelValues(method).Add(1)
			logger.Debugf("Read transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Read transaction succeeded after %d tries (%s), method: %s, last error: %s", i+1, time.Since(start), method, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried %d times, returning: %s", maxRetry, lastErr)
	return lastErr
}

func (m *dbMeta) simpleTxn(ctx context.Context, f func(s *xorm.Session) error) error {
	start := time.Now()
	defer func() { m.txDist.Observe(time.Since(start).Seconds()) }()
	s := m.spool.Get().(*xorm.Session)
	defer m.spool.Put(s)

	var (
		maxRetry = 50
		lastErr  error
		method   string
	)
	for i := 0; i < maxRetry; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		err := f(s)
		if eno, ok := err.(syscall.Errno); ok && eno == 0 {
			err = nil
		}
		if err != nil && m.shouldRetry(err) {
			if method == "" {
				method = callerName(ctx) // lazy evaluation
			}
			m.txRestart.WithLabelValues(method).Add(1)
			logger.Debugf("Read transaction failed, restart it (tried %d): %s", i+1, err)
			lastErr = err
			time.Sleep(time.Millisecond * time.Duration(i*i))
			continue
		} else if err == nil && i > 1 {
			logger.Warnf("Simple transaction succeeded after %d tries (%s), method: %s, last error: %s", i+1, time.Since(start), method, lastErr)
		}
		return err
	}
	logger.Warnf("Already tried %d times, returning: %s", maxRetry, lastErr)
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
	attr.AccessACL = n.AccessACLId
	attr.DefaultACL = n.DefaultACLId
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
	n.setAtime(attr.Atime*1e9 + int64(attr.Atimensec))
	n.setMtime(attr.Mtime*1e9 + int64(attr.Mtimensec))
	n.setCtime(attr.Ctime*1e9 + int64(attr.Ctimensec))
	n.Nlink = attr.Nlink
	n.Length = attr.Length
	n.Rdev = attr.Rdev
	n.Parent = attr.Parent
	n.AccessACLId = attr.AccessACL
	n.DefaultACLId = attr.DefaultACL
}

func (m *dbMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *dbMeta) doSyncVolumeStat(ctx Context) error {
	if m.conf.ReadOnly {
		return syscall.EROFS
	}
	var used, inode int64
	if err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		total, err := s.SumsInt(&dirStats{}, "used_space", "used_inodes")
		used += total[0]
		inode += total[1]
		return err
	}); err != nil {
		return err
	}
	if err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		queryResultMap, err := s.QueryString(m.sqlConv("SELECT length FROM node WHERE inode IN (SELECT inode FROM sustained)"))
		if err != nil {
			return err
		}
		for _, v := range queryResultMap {
			value, err := strconv.ParseInt(v["length"], 10, 64)
			if err != nil {
				logger.Warnf("parse sustained length: %s err: %s", v["length"], err)
				continue
			}
			used += align4K(uint64(value))
			inode += 1
		}
		return nil
	}); err != nil {
		return err
	}

	if err := m.scanTrashEntry(ctx, func(_ Ino, length uint64) {
		used += align4K(length)
		inode += 1
	}); err != nil {
		return err
	}
	logger.Debugf("Used space: %s, inodes: %d", humanize.IBytes(uint64(used)), inode)
	return m.txn(func(s *xorm.Session) error {
		if _, err := s.Cols("value").Update(&counter{Value: inode}, &counter{Name: totalInodes}); err != nil {
			return fmt.Errorf("update totalInodes: %s", err)
		}
		_, err := s.Cols("value").Update(&counter{Value: used}, &counter{Name: usedSpace})
		return err
	})
}

func (m *dbMeta) doFlushStats() {
	newSpace := atomic.LoadInt64(&m.newSpace)
	newInodes := atomic.LoadInt64(&m.newInodes)
	if newSpace != 0 || newInodes != 0 {
		err := m.txn(func(s *xorm.Session) error {
			if _, err := s.Exec(m.sqlConv("update counter set value=value + ? where name='totalInodes'"), newInodes); err != nil {
				return err
			}
			_, err := s.Exec(m.sqlConv("update counter set value= value + ? where name='usedSpace'"), newSpace)
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
}

func (m *dbMeta) doLookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	return errno(m.simpleTxn(ctx, func(s *xorm.Session) error {
		s = s.Table(&edge{})
		nn := namedNode{node: node{Parent: parent}, Name: []byte(name)}
		var exist bool
		var err error
		if attr != nil {
			s = s.Join("INNER", &node{}, m.sqlConv("edge.inode=node.inode"))
			exist, err = s.Select(m.sqlConv("node.*")).Get(&nn)
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
		m.of.Update(nn.Inode, attr)
		return nil
	}))
}

func (m *dbMeta) doGetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return errno(m.simpleTxn(ctx, func(s *xorm.Session) error {
		var n = node{Inode: inode}
		ok, err := s.Get(&n)
		if err != nil {
			return err
		} else if !ok {
			return syscall.ENOENT
		}
		m.parseAttr(&n, attr)
		return nil
	}))
}

func (m *dbMeta) doSetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr, oldAttr *Attr) syscall.Errno {
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
		if oldAttr != nil {
			*oldAttr = curAttr
		}
		if curAttr.Parent > TrashInode {
			return syscall.EPERM
		}
		now := time.Now()

		rule, err := m.getACL(s, curAttr.AccessACL)
		if err != nil {
			return err
		}

		rule = rule.Dup()
		dirtyAttr, st := m.mergeAttr(ctx, inode, set, &curAttr, attr, now, rule)
		if st != 0 {
			return st
		}
		if dirtyAttr == nil {
			return nil
		}

		dirtyAttr.AccessACL, err = m.insertACL(s, rule)
		if err != nil {
			return err
		}

		var dirtyNode node
		m.parseNode(dirtyAttr, &dirtyNode)
		dirtyNode.setCtime(now.UnixNano())
		_, err = s.Cols("flags", "mode", "uid", "gid", "atime", "mtime", "ctime",
			"atimensec", "mtimensec", "ctimensec", "access_acl_id", "default_acl_id").
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
		r, err = s.Exec(m.sqlConv("update chunk set slices=slices || ? where inode=? AND indx=?"), buf, inode, indx)
	} else {
		r, err = s.Exec(m.sqlConv("update chunk set slices=concat(slices, ?) where inode=? AND indx=?"), buf, inode, indx)
	}
	if err == nil {
		if n, _ := r.RowsAffected(); n == 0 {
			err = mustInsert(s, &chunk{Inode: inode, Indx: indx, Slices: buf})
		}
	}
	return err
}

func (m *dbMeta) upsertSlice(s *xorm.Session, inode Ino, indx uint32, buf []byte, insert *bool) error {
	var err error
	driver := m.Name()
	if driver == "sqlite3" || driver == "postgres" {
		_, err = s.Exec(m.sqlConv(`
			 INSERT INTO chunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON CONFLICT (inode, indx)
			 DO UPDATE SET slices=chunk.slices || ?`), inode, indx, buf, buf)
	} else {
		var r sql.Result
		r, err = s.Exec(m.sqlConv(`
			 INSERT INTO chunk (inode, indx, slices)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			 slices=concat(slices, ?)`), inode, indx, buf, buf)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		*insert = n == 1 // https://dev.mysql.com/doc/refman/5.7/en/insert-on-duplicate.html
	}
	return err
}

func (m *dbMeta) doTruncate(ctx Context, inode Ino, flags uint8, length uint64, delta *dirStat, attr *Attr, skipPermCheck bool) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		*delta = dirStat{}
		nodeAttr := node{Inode: inode}
		ok, err := s.ForUpdate().Get(&nodeAttr)
		if err != nil {
			return err
		}
		if !ok {
			return syscall.ENOENT
		}
		if nodeAttr.Type != TypeFile || nodeAttr.Flags&(FlagImmutable|FlagAppend) != 0 || (flags == 0 && nodeAttr.Parent > TrashInode) {
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
		delta.length = int64(length) - int64(nodeAttr.Length)
		delta.space = align4K(length) - align4K(nodeAttr.Length)
		if err := m.checkQuota(ctx, delta.space, 0, nodeAttr.Uid, nodeAttr.Gid, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
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
		nodeAttr.setMtime(now)
		nodeAttr.setCtime(now)
		if _, err = s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nodeAttr, &node{Inode: nodeAttr.Inode}); err != nil {
			return err
		}
		m.parseAttr(&nodeAttr, attr)
		return nil
	}, inode))
}

func (m *dbMeta) doFallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		*delta = dirStat{}
		nodeAttr := node{Inode: inode}
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
		var t Attr
		m.parseAttr(&nodeAttr, &t)
		if st := m.Access(ctx, inode, MODE_MASK_W, &t); st != 0 {
			return st
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
		delta.length = int64(length) - int64(old)
		delta.space = align4K(length) - align4K(old)
		if err := m.checkQuota(ctx, delta.space, 0, nodeAttr.Uid, nodeAttr.Gid, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
			return err
		}
		now := time.Now().UnixNano()
		nodeAttr.Length = length
		nodeAttr.setMtime(now)
		nodeAttr.setCtime(now)
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
		m.parseAttr(&nodeAttr, attr)
		return nil
	}, inode))
}

func (m *dbMeta) doReadlink(ctx Context, inode Ino, noatime bool) (atime int64, target []byte, err error) {
	if noatime {
		err = m.simpleTxn(ctx, func(s *xorm.Session) error {
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
		if nodeAttr.Type != TypeSymlink {
			return syscall.EINVAL
		}
		l := symlink{Inode: inode}
		ok, e = s.Get(&l)
		if e != nil {
			return e
		}
		if !ok {
			return syscall.EIO
		}
		m.parseAttr(&nodeAttr, attr)
		target = l.Target
		if !m.atimeNeedsUpdate(attr, now) {
			atime = attr.Atime*int64(time.Second) + int64(attr.Atimensec)
			return nil
		}
		nodeAttr.setAtime(now.UnixNano())
		atime = now.UnixNano()
		_, e = s.Cols("atime", "atimensec").Update(&nodeAttr, &node{Inode: inode})
		return e
	}, inode)
	return
}

func (m *dbMeta) doMknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, path string, inode *Ino, attr *Attr) syscall.Errno {
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
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if pattr.Parent > TrashInode {
			return syscall.ENOENT
		}
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
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
				*inode = foundIno
			}
			return syscall.EEXIST
		} else if parent == TrashInode {
			if next, err := m.incrSessionCounter(s, "nextTrash", 1); err != nil {
				return err
			} else {
				*inode = TrashInode + Ino(next)
			}
		}

		n := node{Inode: *inode}
		m.parseNode(attr, &n)
		mode &= 07777
		if pattr.DefaultACL != aclAPI.None && _type != TypeSymlink {
			// inherit default acl
			if _type == TypeDirectory {
				n.DefaultACLId = pattr.DefaultACL
			}

			// set access acl by parent's default acl
			rule, err := m.getACL(s, pattr.DefaultACL)
			if err != nil {
				return err
			}

			if rule.IsMinimal() {
				// simple acl as default
				n.Mode = mode & (0xFE00 | rule.GetMode())
			} else {
				cRule := rule.ChildAccessACL(mode)
				id, err := m.insertACL(s, cRule)
				if err != nil {
					return err
				}

				n.AccessACLId = id
				n.Mode = (mode & 0xFE00) | cRule.GetMode()
			}
		} else {
			n.Mode = mode & ^cumask
		}
		if (pn.Flags & FlagSkipTrash) != 0 {
			n.Flags |= FlagSkipTrash
		}

		var updateParent bool
		var nlinkAdjust int32
		now := time.Now().UnixNano()
		if parent != TrashInode {
			if _type == TypeDirectory {
				pn.Nlink++
				updateParent = true
				nlinkAdjust++
			}
			if updateParent || time.Duration(now-pn.getMtime()) >= m.conf.SkipDirMtime {
				pn.setMtime(now)
				pn.setCtime(now)
				updateParent = true
			}
		}
		n.setAtime(now)
		n.setMtime(now)
		n.setCtime(now)
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

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: *inode, Type: _type}, &n); err != nil {
			return err
		}
		if _type == TypeSymlink {
			if err = mustInsert(s, &symlink{Inode: *inode, Target: []byte(path)}); err != nil {
				return err
			}
		}
		if _type == TypeDirectory {
			if err = mustInsert(s, &dirStats{Inode: *inode}); err != nil {
				return err
			}
		}
		if updateParent {
			if _n, err := s.SetExpr("nlink", fmt.Sprintf("nlink + (%d)", nlinkAdjust)).Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode}); err != nil || _n == 0 {
				if err == nil {
					logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, pn.Inode)
					if m.Name() == "mysql" {
						err = syscall.EBUSY
					} else {
						err = syscall.ENOENT
					}
				}
				if err != nil {
					return err
				}
			}
		}
		m.parseAttr(&n, attr)
		return nil
	}))
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
			if (n.Flags & FlagSkipTrash) != 0 {
				trash = 0
			}
			if trash > 0 && n.Nlink > 1 {
				if o, e := s.Get(&edge{Parent: trash, Name: []byte(m.trashEntry(parent, e.Inode, string(e.Name))), Inode: e.Inode, Type: e.Type}); e == nil && o {
					trash = 0
				}
			}
			n.setCtime(now)
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
		if !parent.IsTrash() && time.Duration(now-pn.getMtime()) >= m.conf.SkipDirMtime {
			pn.setMtime(now)
			pn.setCtime(now)
			updateParent = true
		}

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
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
		if updateParent {
			var _n int64
			if _n, err = s.Cols("mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode}); err != nil || _n == 0 {
				if err == nil {
					logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, pn.Inode)
					if m.Name() == "mysql" {
						err = syscall.EBUSY
					} else {
						err = syscall.ENOENT
					}
				}
				if err != nil {
					return err
				}
			}
		}
		return err
	})
	if err == nil && trash == 0 {
		if n.Type == TypeFile && n.Nlink == 0 {
			m.fileDeleted(opened, parent.IsTrash(), n.Inode, n.Length)
		}
		m.updateStats(newSpace, newInode)
	}
	if err == nil && attr != nil {
		m.parseAttr(&n, attr)
	}
	return errno(err)
}

func (m *dbMeta) doRmdir(ctx Context, parent Ino, name string, pinode *Ino, attr *Attr, skipCheckTrash ...bool) syscall.Errno {
	var trash Ino
	if !(len(skipCheckTrash) == 1 && skipCheckTrash[0]) {
		if st := m.checkTrash(parent, &trash); st != 0 {
			return st
		}
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
		if ok && attr != nil {
			m.parseAttr(&n, attr)
		}
		exist, err := s.Exist(&edge{Parent: e.Inode})
		if err != nil {
			return err
		}
		if exist {
			return syscall.ENOTEMPTY
		}
		if (n.Flags & FlagSkipTrash) != 0 {
			trash = 0
		}
		now := time.Now().UnixNano()
		if ok {
			if ctx.Uid() != 0 && pn.Mode&01000 != 0 && ctx.Uid() != pn.Uid && ctx.Uid() != n.Uid {
				return syscall.EACCES
			}
			if trash > 0 {
				n.setCtime(now)
				n.Parent = trash
			}
		} else {
			logger.Warnf("no attribute for inode %d (%d, %s)", e.Inode, parent, name)
			trash = 0
		}
		pn.Nlink--
		pn.setMtime(now)
		pn.setCtime(now)

		if _, err := s.Delete(&edge{Parent: parent, Name: e.Name}); err != nil {
			return err
		}
		if _, err := s.Delete(&dirStats{Inode: e.Inode}); err != nil {
			logger.Warnf("remove dir usage of ino(%d): %s", e.Inode, err)
			return err
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
		if !parent.IsTrash() {
			_, err = s.SetExpr("nlink", "nlink - 1").Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: pn.Inode})
		}
		return err
	})
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

func (m *dbMeta) getNodes(s *xorm.Session, nodes ...*node) error {
	for i := range nodes {
		ok, err := s.Get(nodes[i])
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
	parentLocks := []Ino{parentDst}
	if !parentSrc.IsTrash() { // there should be no conflict if parentSrc is in trash, relax lock to accelerate `restore` subcommand
		parentLocks = append(parentLocks, parentSrc)
	}
	err := m.txn(func(s *xorm.Session) error {
		opened = false
		dino = 0
		newSpace, newInode = 0, 0
		var spn = node{Inode: parentSrc}
		var dpn = node{Inode: parentDst}
		err := m.getNodes(s, &spn, &dpn)
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
				if string(e.Name) != nameSrc || parentSrc != parentDst {
					ok = true
					se.Inode = e.Inode
					se.Type = e.Attr.Typ
					se.Name = e.Name
				}
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
		// TODO: check parentDst is a subdir of source node
		if se.Inode == parentDst || se.Inode == dpattr.Parent {
			return syscall.EPERM
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
				if string(e.Name) != nameSrc || parentSrc != parentDst {
					ok = true
					de.Inode = e.Inode
					de.Type = e.Attr.Typ
					de.Name = e.Name
				}
			}
		}
		var supdate, dupdate bool
		var srcnlink, dstnlink int32
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
			if (dn.Flags & FlagSkipTrash) != 0 {
				trash = 0
			}
			dn.setCtime(now)
			if exchange {
				if parentSrc != parentDst {
					if de.Type == TypeDirectory {
						dn.Parent = parentSrc
						dpn.Nlink--
						dstnlink--
						spn.Nlink++
						srcnlink++
						supdate, dupdate = true, true
					} else if dn.Parent > 0 {
						dn.Parent = parentSrc
					}
				}
			} else if de.Inode == se.Inode {
				return nil
			} else if se.Type == TypeDirectory && de.Type != TypeDirectory {
				return syscall.ENOTDIR
			} else if de.Type == TypeDirectory {
				if se.Type != TypeDirectory {
					return syscall.EISDIR
				}
				exist, err := s.Exist(&edge{Parent: de.Inode})
				if err != nil {
					return err
				}
				if exist {
					return syscall.ENOTEMPTY
				}
				dpn.Nlink--
				dstnlink--
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
				srcnlink--
				dpn.Nlink++
				dstnlink++
				supdate, dupdate = true, true
			} else if sn.Parent > 0 {
				sn.Parent = parentDst
			}
		}
		if supdate || time.Duration(now-spn.getMtime()) >= m.conf.SkipDirMtime {
			spn.setMtime(now)
			spn.setCtime(now)
			supdate = true
		}
		if dupdate || time.Duration(now-dpn.getMtime()) >= m.conf.SkipDirMtime {
			dpn.setMtime(now)
			dpn.setCtime(now)
			dupdate = true
		}
		sn.setCtime(now)
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

		if _, err := s.Cols("ctime", "ctimensec", "parent").Update(&sn, &node{Inode: sn.Inode}); err != nil {
			return err
		}

		if parentDst != parentSrc && !parentSrc.IsTrash() && supdate {
			if dupdate && dpn.Inode < spn.Inode {
				if _n, err := s.SetExpr("nlink", fmt.Sprintf("nlink + (%d)", dstnlink)).Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&dpn, &node{Inode: parentDst}); err != nil || _n == 0 {
					if err == nil {
						logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, dpn.Inode)
						if m.Name() == "mysql" {
							err = syscall.EBUSY
						} else {
							err = syscall.ENOENT
						}
					}
					if err != nil {
						return err
					}
				}
				dupdate = false
			}

			if _n, err := s.SetExpr("nlink", fmt.Sprintf("nlink + (%d)", srcnlink)).Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&spn, &node{Inode: parentSrc}); err != nil || _n == 0 {
				if err == nil {
					logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, spn.Inode)
					if m.Name() == "mysql" {
						err = syscall.EBUSY
					} else {
						err = syscall.ENOENT
					}
				}
				if err != nil {
					return err
				}
			}
		}

		if dupdate {
			if _n, err := s.SetExpr("nlink", fmt.Sprintf("nlink + (%d)", dstnlink)).Cols("nlink", "mtime", "ctime", "mtimensec", "ctimensec").Update(&dpn, &node{Inode: parentDst}); err != nil || _n == 0 {
				if err == nil {
					logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, dpn.Inode)
					if m.Name() == "mysql" {
						err = syscall.EBUSY
					} else {
						err = syscall.ENOENT
					}
				}
				if err != nil {
					return err
				}
			}
		}
		return err
	}, parentLocks...)
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
		if pn.Parent > TrashInode {
			return syscall.ENOENT
		}
		var pattr Attr
		m.parseAttr(&pn, &pattr)
		if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, &pattr); st != 0 {
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
		if time.Duration(now-pn.getMtime()) >= m.conf.SkipDirMtime {
			pn.setMtime(now)
			pn.setCtime(now)
			updateParent = true
		}
		n.Parent = 0
		n.Nlink++
		n.setCtime(now)

		if err = mustInsert(s, &edge{Parent: parent, Name: []byte(name), Inode: inode, Type: n.Type}); err != nil {
			return err
		}
		if _, err := s.Cols("nlink", "ctime", "ctimensec", "parent").Update(&n, node{Inode: inode}); err != nil {
			return err
		}
		if updateParent {
			if _n, err := s.Cols("mtime", "ctime", "mtimensec", "ctimensec").Update(&pn, &node{Inode: parent}); err != nil || _n == 0 {
				if err == nil {
					logger.Infof("Update parent node affected rows = %d should be 1 for inode = %d .", _n, pn.Inode)
					if m.Name() == "mysql" {
						err = syscall.EBUSY
					} else {
						err = syscall.ENOENT
					}
				}
				return err
			}
		}

		m.parseAttr(&n, attr)
		return err
	}, inode))
}

func (m *dbMeta) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	return errno(m.simpleTxn(ctx, func(s *xorm.Session) error {
		s = s.Table(&edge{})
		if plus != 0 {
			s = s.Join("INNER", &node{}, m.sqlConv("edge.inode=node.inode"))
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
				m.of.Update(entry.Inode, entry.Attr)
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
	err = m.simpleTxn(Background(), func(ses *xorm.Session) error {
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
	_ = m.simpleTxn(Background(), func(ses *xorm.Session) error {
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

	err := m.simpleTxn(Background(), func(ses *xorm.Session) error {
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
	}
	return err
}

func (m *dbMeta) doRead(ctx Context, inode Ino, indx uint32) ([]*slice, syscall.Errno) {
	var c = chunk{Inode: inode, Indx: indx}
	if err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		_, err := s.MustCols("indx").Get(&c)
		return err
	}); err != nil {
		return nil, errno(err)
	}
	return readSliceBuf(c.Slices), 0
}

func (m *dbMeta) doWrite(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time, numSlices *int, delta *dirStat, attr *Attr) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		*delta = dirStat{}
		nodeAttr := node{Inode: inode}
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
			delta.length = int64(newleng - nodeAttr.Length)
			delta.space = align4K(newleng) - align4K(nodeAttr.Length)
			nodeAttr.Length = newleng
		}
		if err := m.checkQuota(ctx, delta.space, 0, nodeAttr.Uid, nodeAttr.Gid, m.getParents(s, inode, nodeAttr.Parent)...); err != 0 {
			return err
		}
		nodeAttr.setMtime(mtime.UnixNano())
		nodeAttr.setCtime(time.Now().UnixNano())
		m.parseAttr(&nodeAttr, attr)

		buf := marshalSlice(off, slice.Id, slice.Size, slice.Off, slice.Len)
		var insert bool // no compaction check for the first slice
		if err = m.upsertSlice(s, inode, indx, buf, &insert); err != nil {
			return err
		}
		if err = mustInsert(s, sliceRef{slice.Id, slice.Size, 1}); err != nil {
			return err
		}
		_, err = s.Cols("length", "mtime", "ctime", "mtimensec", "ctimensec").Update(&nodeAttr, &node{Inode: inode})
		if err == nil && !insert {
			ck := chunk{Inode: inode, Indx: indx}
			_, _ = s.MustCols("indx").Get(&ck)
			*numSlices = len(ck.Slices) / sliceBytes
		}
		return err
	}, inode))
}

func (m *dbMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied, outLength *uint64) syscall.Errno {
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
			if copied != nil {
				*copied = 0
			}
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
		if err := m.checkQuota(ctx, newSpace, 0, nout.Uid, nout.Gid, m.getParents(s, fout, nout.Parent)...); err != 0 {
			return err
		}
		now := time.Now().UnixNano()
		nout.setMtime(now)
		nout.setCtime(now)
		if outLength != nil {
			*outLength = nout.Length
		}

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
				if _, err := ses.Exec(m.sqlConv("update chunk_ref set refs=refs+1 where chunkid = ? AND size = ?"), id, size); err != nil {
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
		if copied != nil {
			*copied = size
		}
		return nil
	}, fout)
	if err == nil {
		m.updateParentStat(ctx, fout, nout.Parent, newLength, newSpace)
		if newSpace > 0 {
			m.updateUserGroupQuota(ctx, nout.Uid, nout.Gid, newSpace, 0)
		}
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
	if err := m.simpleTxn(ctx, func(s *xorm.Session) error {
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
	if err = m.simpleTxn(ctx, func(s *xorm.Session) error {
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
			n, err := s.Cols("data_length", "used_space", "used_inodes").Update(&st, &dirStats{Inode: ino})
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
	err := m.simpleTxn(Background(), func(s *xorm.Session) error {
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

func (m *dbMeta) doCleanupSlices(ctx Context) {
	var cks []sliceRef
	_ = m.simpleTxn(ctx, func(s *xorm.Session) error {
		cks = nil
		return s.Where("refs <= 0").Find(&cks)
	})
	for _, ck := range cks {
		m.deleteSlice(ck.Id, ck.Size)
		if ctx.Canceled() {
			break
		}
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
			_, err = s.Exec(m.sqlConv("update chunk_ref set refs=refs-1 where chunkid=? AND size=?"), sc.id, sc.size)
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
		err := m.simpleTxn(Background(), func(s *xorm.Session) error {
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
	_ = m.simpleTxn(Background(), func(s *xorm.Session) error {
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

func (m *dbMeta) doCleanupDelayedSlices(ctx Context, edge int64) (int, error) {
	var count int
	var ss []Slice
	var result []delslices
	var batch int = 1e6
	for {
		_ = m.simpleTxn(ctx, func(s *xorm.Session) error {
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
					if _, e := ses.Exec(m.sqlConv("update chunk_ref set refs=refs-1 where chunkid=? AND size=?"), s.Id, s.Size); e != nil {
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
				err := m.simpleTxn(ctx, func(s *xorm.Session) error {
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
				if ctx.Canceled() {
					return count, nil
				}
			}
		}
		if len(result) < batch {
			break
		}
	}
	return count, nil
}

func (m *dbMeta) doCompactChunk(inode Ino, indx uint32, origin []byte, ss []*slice, skipped int, pos uint32, id uint64, size uint32, delayed []byte) syscall.Errno {
	st := errno(m.txn(func(s *xorm.Session) error {
		var c2 = chunk{Inode: inode, Indx: indx}
		_, err := s.ForUpdate().MustCols("indx").Get(&c2)
		if err != nil {
			return err
		}
		if len(c2.Slices) < len(origin) || !bytes.Equal(origin, c2.Slices[:len(origin)]) {
			logger.Infof("chunk %d:%d was changed %d -> %d", inode, indx, len(origin), len(c2.Slices))
			return syscall.EINVAL
		}

		c2.Slices = append(append(c2.Slices[:skipped*sliceBytes], marshalSlice(pos, id, size, 0, size)...), c2.Slices[len(origin):]...)
		if _, err := s.Cols("slices").Where("Inode = ? AND indx = ?", inode, indx).Update(c2); err != nil {
			return err
		}
		// create the key to tracking it
		if err = mustInsert(s, sliceRef{id, size, 1}); err != nil {
			return err
		}
		if delayed != nil {
			if len(delayed) > 0 {
				if err = mustInsert(s, &delslices{id, time.Now().Unix(), delayed}); err != nil {
					return err
				}
			}
		} else {
			for _, s_ := range ss {
				if s_.id == 0 {
					continue
				}
				if _, err := s.Exec(m.sqlConv("update chunk_ref set refs=refs-1 where chunkid=? AND size=?"), s_.id, s_.size); err != nil {
					return err
				}
			}
		}
		return nil
	}, inode))
	// there could be false-negative that the compaction is successful, double-check
	if st != 0 && st != syscall.EINVAL {
		var ok bool
		if err := m.simpleTxn(Background(), func(s *xorm.Session) error {
			var e error
			ok, e = s.Get(&sliceRef{Id: id})
			return e
		}); err == nil {
			if ok {
				st = 0
			} else {
				logger.Infof("compacted chunk %d was not used", id)
				st = syscall.EINVAL
			}
		}
	}

	if st == syscall.EINVAL {
		_ = m.txn(func(s *xorm.Session) error {
			return mustInsert(s, &sliceRef{id, size, 0})
		})
	} else if st == 0 && delayed == nil {
		for _, s := range ss {
			if s.id == 0 {
				continue
			}
			var ref = sliceRef{Id: s.id}
			var ok bool
			err := m.simpleTxn(Background(), func(s *xorm.Session) error {
				var e error
				ok, e = s.Get(&ref)
				return e
			})
			if err == nil && ok && ref.Refs <= 0 {
				m.deleteSlice(s.id, s.size)
			}
		}
	}
	return st
}

func dup(b []byte) []byte {
	r := make([]byte, len(b))
	copy(r, b)
	return r
}

func (m *dbMeta) scanAllChunks(ctx Context, ch chan<- cchunk, bar *utils.Bar) error {
	return m.roTxn(ctx, func(s *xorm.Session) error {
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

func (m *dbMeta) ListSlices(ctx Context, slices map[Ino][]Slice, scanPending, delete bool, showProgress func()) syscall.Errno {
	if delete {
		m.doCleanupSlices(ctx)
	}
	err := m.simpleTxn(ctx, func(s *xorm.Session) error {
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

	if scanPending {
		_ = m.simpleTxn(ctx, func(s *xorm.Session) error {
			var cks []sliceRef
			err := s.Where("refs <= 0").Find(&cks)
			if err != nil {
				return err
			}
			for _, ck := range cks {
				slices[0] = append(slices[0], Slice{Id: ck.Id, Size: ck.Size})
			}
			return nil
		})
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

	err := m.simpleTxn(ctx, func(tx *xorm.Session) error {
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
					if _, e := tx.Exec(m.sqlConv("update chunk_ref set refs=refs-1 where chunkid=? AND size=?"), s.Id, s.Size); e != nil {
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
				err := m.simpleTxn(ctx, func(tx *xorm.Session) error {
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
	err := m.simpleTxn(ctx, func(tx *xorm.Session) error {
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
	if err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		if ok, err := s.IsTableExist(&delfile{}); err != nil {
			return err
		} else if !ok {
			return nil
		}
		return s.Find(&dfs)
	}); err != nil {
		return err
	}

	for _, ds := range dfs {
		if _, err := scan(ds.Inode, ds.Length, ds.Expire); err != nil {
			return err
		}
	}

	return nil
}

func (m *dbMeta) doRepair(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	n := &node{
		Inode:  inode,
		Type:   attr.Typ,
		Mode:   attr.Mode,
		Uid:    attr.Uid,
		Gid:    attr.Gid,
		Length: attr.Length,
		Parent: attr.Parent,
		Nlink:  attr.Nlink,
	}
	n.setAtime(attr.Atime*1e9 + int64(attr.Atimensec))
	n.setMtime(attr.Mtime*1e9 + int64(attr.Mtimensec))
	n.setCtime(attr.Ctime*1e9 + int64(attr.Ctimensec))
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
				updateColumns := []string{
					"type", "mode",
					"uid", "gid",
					"length", "parent", "nlink",
					"atime", "mtime", "ctime",
					"atimensec", "mtimensec", "ctimensec",
				}
				_, err = s.Cols(updateColumns...).Update(n, &node{Inode: inode})
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
	return errno(m.simpleTxn(ctx, func(s *xorm.Session) error {
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
	return errno(m.roTxn(ctx, func(s *xorm.Session) error {
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

		var n = node{Inode: inode}
		ok, err := s.Get(&n)
		if err != nil {
			return err
		} else if !ok {
			return syscall.ENOENT
		}
		attr := &Attr{}
		m.parseAttr(&n, attr)
		setXAttrACL(names, attr.AccessACL, attr.DefaultACL)
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
		existing := k.Value
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
			_, err = s.Cols("value").Update(&x, k)
		default:
			if !ok {
				err = mustInsert(s, &x)
			} else if !bytes.Equal(existing, value) {
				_, err = s.Cols("value").Update(&x, k)
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

func (m *dbMeta) doGetQuota(ctx Context, qtype uint32, key uint64) (*Quota, error) {
	if qtype != DirQuotaType && qtype != UserQuotaType && qtype != GroupQuotaType {
		return nil, errors.Errorf("invalid quota type %d", qtype)
	}

	var quota *Quota
	err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		if qtype == DirQuotaType {
			q := &dirQuota{Inode: Ino(key)}
			ok, e := s.Get(q)
			if e == nil && ok {
				quota = &Quota{
					MaxSpace:   q.MaxSpace,
					MaxInodes:  q.MaxInodes,
					UsedSpace:  q.UsedSpace,
					UsedInodes: q.UsedInodes}
			}
			return e
		} else {
			q := &userGroupQuota{Qtype: qtype, Qkey: key}
			ok, e := s.Get(q)
			if e == nil && ok {
				quota = &Quota{
					MaxSpace:   q.MaxSpace,
					MaxInodes:  q.MaxInodes,
					UsedSpace:  q.UsedSpace,
					UsedInodes: q.UsedInodes}
			}
			return e
		}
	})
	return quota, err
}

func updateQuotaFields(quota *Quota, exist bool, maxSpace, maxInodes *int64, usedSpace, usedInodes *int64) []string {
	updateColumns := make([]string, 0, 4)
	if quota.MaxSpace >= 0 {
		*maxSpace = quota.MaxSpace
		updateColumns = append(updateColumns, "max_space")
	}
	if quota.MaxInodes >= 0 {
		*maxInodes = quota.MaxInodes
		updateColumns = append(updateColumns, "max_inodes")
	}
	if quota.UsedSpace >= 0 {
		*usedSpace = quota.UsedSpace
		updateColumns = append(updateColumns, "used_space")
	} else if !exist {
		*usedSpace = 0
		updateColumns = append(updateColumns, "used_space")
	}
	if quota.UsedInodes >= 0 {
		*usedInodes = quota.UsedInodes
		updateColumns = append(updateColumns, "used_inodes")
	} else if !exist {
		*usedInodes = 0
		updateColumns = append(updateColumns, "used_inodes")
	}

	return updateColumns
}

func (m *dbMeta) doSetQuota(ctx Context, qtype uint32, key uint64, quota *Quota) (bool, error) {
	var created bool
	err := m.txn(func(s *xorm.Session) error {
		if qtype == DirQuotaType {
			origin := &dirQuota{Inode: Ino(key)}
			exist, e := s.ForUpdate().Get(origin)
			if e != nil {
				return e
			}
			created = !exist
			updateColumns := updateQuotaFields(quota, exist, &origin.MaxSpace, &origin.MaxInodes, &origin.UsedSpace, &origin.UsedInodes)
			if exist {
				_, e = s.Cols(updateColumns...).Update(origin, &dirQuota{Inode: Ino(key)})
			} else {
				e = mustInsert(s, origin)
			}
			return e
		} else if qtype == UserQuotaType || qtype == GroupQuotaType {
			origin := &userGroupQuota{Qtype: qtype, Qkey: key}
			exist, e := s.ForUpdate().Get(origin)
			if e != nil {
				return e
			}
			created = !exist
			updateColumns := updateQuotaFields(quota, exist, &origin.MaxSpace, &origin.MaxInodes, &origin.UsedSpace, &origin.UsedInodes)
			if exist {
				_, e = s.Cols(updateColumns...).Update(origin, &userGroupQuota{Qtype: qtype, Qkey: key})
			} else {
				e = mustInsert(s, origin)
			}
			return e
		} else {
			return errors.Errorf("invalid quota type %d", qtype)
		}
	})

	return created, err
}

func (m *dbMeta) doDelQuota(ctx Context, qtype uint32, key uint64) error {
	if qtype != DirQuotaType && qtype != UserQuotaType && qtype != GroupQuotaType {
		return errors.Errorf("invalid quota type %d", qtype)
	}

	return m.txn(func(s *xorm.Session) error {
		if qtype == DirQuotaType {
			_, e := s.Delete(&dirQuota{Inode: Ino(key)})
			return e
		} else {
			_, e := s.Cols("max_space", "max_inodes").
				Update(&userGroupQuota{MaxSpace: -1, MaxInodes: -1},
					&userGroupQuota{Qtype: qtype, Qkey: key})
			return e
		}
	})
}

func (m *dbMeta) doLoadQuotas(ctx Context) (map[uint64]*Quota, map[uint64]*Quota, map[uint64]*Quota, error) {
	var dirQuotasList []dirQuota
	var userGroupQuotasList []userGroupQuota

	err := m.simpleTxn(ctx, func(s *xorm.Session) error {
		if e := s.Find(&dirQuotasList); e != nil {
			return e
		}
		if e := s.Find(&userGroupQuotasList); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	dirQuotas := make(map[uint64]*Quota)
	userQuotas := make(map[uint64]*Quota)
	groupQuotas := make(map[uint64]*Quota)

	// Load directory quotas
	for _, q := range dirQuotasList {
		quota := &Quota{
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		}
		dirQuotas[uint64(q.Inode)] = quota
	}

	// Load user and group quotas
	for _, q := range userGroupQuotasList {
		quota := &Quota{
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		}

		switch q.Qtype {
		case UserQuotaType:
			userQuotas[q.Qkey] = quota
		case GroupQuotaType:
			groupQuotas[q.Qkey] = quota
		}
	}

	return dirQuotas, userQuotas, groupQuotas, nil
}

func (m *dbMeta) doFlushQuotas(ctx Context, quotas []*iQuota) error {
	sort.Slice(quotas, func(i, j int) bool { return quotas[i].qkey < quotas[j].qkey })
	return m.txn(func(s *xorm.Session) error {
		for _, q := range quotas {
			if q.qtype == DirQuotaType {
				logger.Infof("doFlushquot ino:%d, %+v", q.qkey, q.quota)
				_, err := s.Exec(m.sqlConv("update dir_quota set used_space=used_space+?, used_inodes=used_inodes+? where inode=?"),
					q.quota.newSpace, q.quota.newInodes, q.qkey)
				if err != nil {
					return err
				}
			} else {
				_, err := s.Exec(m.sqlConv("update user_group_quota set used_space=used_space+?, used_inodes=used_inodes+? where qtype=? and qkey=?"),
					q.quota.newSpace, q.quota.newInodes, q.qtype, q.qkey)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (m *dbMeta) dumpEntry(s *xorm.Session, inode Ino, typ uint8, e *DumpedEntry, showProgress func(totalIncr, currentIncr int64)) error {
	n := &node{Inode: inode}
	ok, err := s.Get(n)
	if err != nil {
		return err
	}
	attr := &Attr{Typ: typ, Nlink: 1}
	if !ok {
		logger.Warnf("The entry of the inode was not found. inode: %d", inode)
		if attr.Typ == TypeDirectory {
			attr.Nlink = 2
		}
	} else {
		m.parseAttr(n, attr)
	}
	dumpAttr(attr, e.Attr)
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

	accessACl, err := m.getACL(s, attr.AccessACL)
	if err != nil {
		return err
	}
	e.AccessACL = dumpACL(accessACl)
	defaultACL, err := m.getACL(s, attr.DefaultACL)
	if err != nil {
		return err
	}
	e.DefaultACL = dumpACL(defaultACL)

	if attr.Typ == TypeFile {
		for indx := uint32(0); uint64(indx)*ChunkSize < attr.Length; indx++ {
			c := &chunk{Inode: inode, Indx: indx}
			if ok, err = s.MustCols("indx").Get(c); err != nil {
				return err
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
			return err
		}
		if !ok {
			logger.Warnf("no link target for inode %d", inode)
		}
		e.Symlink = string(l.Target)
	} else if attr.Typ == TypeDirectory {
		var edges []*edge
		err := s.Limit(1000, 0).Find(&edges, &edge{Parent: inode})
		if err != nil {
			return err
		}
		if showProgress != nil {
			showProgress(int64(len(edges)), 0)
		}
		if len(edges) < 1000 {
			e.Entries = make(map[string]*DumpedEntry, len(edges))
			for _, edge := range edges {
				name := string(edge.Name)
				ce := entryPool.Get()
				ce.Name = name
				ce.Attr.Inode = edge.Inode
				ce.Attr.Type = typeToString(edge.Type)
				e.Entries[name] = ce
			}
		}
	}
	return nil
}

func (m *dbMeta) dumpEntryFast(inode Ino, typ uint8) *DumpedEntry {
	e := &DumpedEntry{}
	n, ok := m.snap.node[inode]
	if !ok && inode != TrashInode {
		logger.Warnf("Corrupt inode: %d, missing attribute", inode)
	}

	attr := &Attr{Typ: typ, Nlink: 1}
	if !ok {
		logger.Warnf("The entry of the inode was not found. inode: %d", inode)
		if attr.Typ == TypeDirectory {
			attr.Nlink = 2
		}
	} else {
		m.parseAttr(n, attr)
	}
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

	if attr.AccessACL != aclAPI.None {
		e.AccessACL = dumpACL(m.aclCache.Get(attr.AccessACL))
	}
	if attr.DefaultACL != aclAPI.None {
		e.DefaultACL = dumpACL(m.aclCache.Get(attr.DefaultACL))
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

func (m *dbMeta) dumpDir(s *xorm.Session, inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth, threads int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	if tree.Entries == nil {
		// retry for large directory
		var edges []*edge
		err := s.Find(&edges, &edge{Parent: inode})
		if err != nil {
			return err
		}
		tree.Entries = make(map[string]*DumpedEntry, len(edges))
		for _, edge := range edges {
			name := string(edge.Name)
			ce := entryPool.Get()
			ce.Name = name
			ce.Attr.Inode = edge.Inode
			ce.Attr.Type = typeToString(edge.Type)
			tree.Entries[name] = ce
		}
		if showProgress != nil {
			showProgress(int64(len(edges))-1000, 0)
		}
	}
	var entries []*DumpedEntry
	for _, e := range tree.Entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	_ = tree.writeJsonWithOutEntry(bw, depth)

	ms := make([]sync.Mutex, threads)
	conds := make([]*sync.Cond, threads)
	ready := make([]bool, threads)
	var err error
	for c := 0; c < threads; c++ {
		conds[c] = sync.NewCond(&ms[c])
		if c < len(entries) {
			go func(c int) {
				for i := c; i < len(entries) && err == nil; i += threads {
					e := entries[i]
					er := m.roTxn(Background(), func(s *xorm.Session) error {
						return m.dumpEntry(s, e.Attr.Inode, 0, e, showProgress)
					})
					ms[c].Lock()
					ready[c] = true
					if er != nil {
						err = er
					}
					conds[c].Signal()
					for ready[c] && err == nil {
						conds[c].Wait()
					}
					ms[c].Unlock()
				}
			}(c)
		}
	}

	for i, e := range entries {
		c := i % threads
		ms[c].Lock()
		for !ready[c] && err == nil {
			conds[c].Wait()
		}
		ready[c] = false
		conds[c].Signal()
		ms[c].Unlock()
		if err != nil {
			return err
		}
		if e.Attr.Type == "directory" {
			err = m.dumpDir(s, e.Attr.Inode, e, bw, depth+2, threads, showProgress)
		} else {
			err = e.writeJSON(bw, depth+2)
		}
		if err != nil {
			return err
		}
		entries[i] = nil
		entryPool.Put(e)
		if i != len(entries)-1 {
			bwWrite(",")
		}
		if showProgress != nil {
			showProgress(0, 1)
		}
	}
	bwWrite(fmt.Sprintf("\n%s}\n%s}", strings.Repeat(jsonIndent, depth+1), strings.Repeat(jsonIndent, depth)))
	return nil
}

func (m *dbMeta) dumpDirFast(inode Ino, tree *DumpedEntry, bw *bufio.Writer, depth int, showProgress func(totalIncr, currentIncr int64)) error {
	bwWrite := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	edges := m.snap.edges[inode]
	_ = tree.writeJsonWithOutEntry(bw, depth)
	sort.Slice(edges, func(i, j int) bool { return bytes.Compare(edges[i].Name, edges[j].Name) == -1 })

	for i, e := range edges {
		entry := m.dumpEntryFast(e.Inode, e.Type)
		if entry == nil {
			logger.Warnf("ignore broken entry %s (inode: %d) in %s", string(e.Name), e.Inode, inode)
			continue
		}

		entry.Name = string(e.Name)
		if e.Type == TypeDirectory {
			_ = m.dumpDirFast(e.Inode, entry, bw, depth+2, showProgress)
		} else {
			_ = entry.writeJSON(bw, depth+2)
		}
		if i != len(edges)-1 {
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

	for _, s := range []interface{}{new(node), new(symlink), new(edge), new(xattr), new(chunk), new(acl)} {
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

	if err := ses.Table(&acl{}).Iterate(new(acl), func(idx int, bean interface{}) error {
		a := bean.(*acl)
		m.aclCache.Put(a.Id, a.toRule())
		bar.Increment()
		return nil
	}); err != nil {
		return err
	}

	m.snap = snap
	return nil
}

func (m *dbMeta) DumpMeta(w io.Writer, root Ino, threads int, keepSecret, fast, skipTrash bool) (err error) {
	defer func() {
		if p := recover(); p != nil {
			debug.PrintStack()
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
	return m.roTxn(Background(), func(s *xorm.Session) error {
		if root == RootInode && fast {
			defer func() { m.snap = nil }()
			bar := progress.AddCountBar("Snapshot keys", 0)
			if err = m.makeSnap(s, bar); err != nil {
				return fmt.Errorf("Fetch all metadata from DB: %s", err)
			}
			bar.Done()
			tree = m.dumpEntryFast(root, TypeDirectory)
			if !skipTrash {
				trash = m.dumpEntryFast(TrashInode, TypeDirectory)
			}
		} else {
			tree = &DumpedEntry{
				Name: "FSTree",
				Attr: &DumpedAttr{
					Inode: root,
					Type:  typeToString(TypeDirectory),
				},
			}
			if err = m.dumpEntry(s, root, TypeDirectory, tree, nil); err != nil {
				return err
			}
			if root == 1 && !skipTrash {
				trash = &DumpedEntry{
					Name: "Trash",
					Attr: &DumpedAttr{
						Inode: TrashInode,
						Type:  typeToString(TypeDirectory),
					},
				}
				if err = m.dumpEntry(s, TrashInode, TypeDirectory, trash, nil); err != nil {
					return err
				}
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
		// todo Add user/group quota
		dumpedQuotas := make(map[Ino]*DumpedQuota, len(qs))
		for _, q := range qs {
			dumpedQuotas[Ino(q.Inode)] = &DumpedQuota{q.MaxSpace, q.MaxInodes, 0, 0}
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
		useTotal := root == RootInode && !skipTrash
		bar := progress.AddCountBar("Dumped entries", 1) // with root
		if useTotal {
			totalBean := &counter{Name: "totalInodes"}
			if _, err := s.Get(totalBean); err != nil {
				return err
			}
			bar.SetTotal(totalBean.Value)
		}
		bar.Increment()
		if trash != nil {
			trash.Name = "Trash"
			bar.IncrTotal(1)
			bar.Increment()
		}
		showProgress := func(totalIncr, currentIncr int64) {
			if !useTotal {
				bar.IncrTotal(totalIncr)
			}
			bar.IncrInt64(currentIncr)
		}
		if m.snap != nil {
			_ = m.dumpDirFast(root, tree, bw, 1, showProgress)
		} else {
			showProgress(int64(len(tree.Entries)), 0)
			if err = m.dumpDir(s, root, tree, bw, 1, threads, showProgress); err != nil {
				logger.Errorf("dump dir %d failed: %s", root, err)
				return fmt.Errorf("dump dir %d failed", root) // don't retry
			}
		}
		if trash != nil {
			if _, err = bw.WriteString(","); err != nil {
				return err
			}
			if m.snap != nil {
				_ = m.dumpDirFast(TrashInode, trash, bw, 1, showProgress)
			} else {
				showProgress(int64(len(trash.Entries)), 0)
				if err = m.dumpDir(s, TrashInode, trash, bw, 1, threads, showProgress); err != nil {
					logger.Errorf("dump trash failed: %s", err)
					return fmt.Errorf("dump trash failed") // don't retry
				}
			}
		}
		if _, err = bw.WriteString("\n}\n"); err != nil {
			return err
		}
		progress.Done()
		return bw.Flush()
	})
}

func (m *dbMeta) loadEntry(e *DumpedEntry, chs []chan interface{}, aclMaxId *uint32) {
	inode := e.Attr.Inode
	attr := e.Attr
	n := &node{
		Inode:  inode,
		Flags:  attr.Flags,
		Type:   typeFromString(attr.Type),
		Mode:   attr.Mode,
		Uid:    attr.Uid,
		Gid:    attr.Gid,
		Nlink:  attr.Nlink,
		Rdev:   attr.Rdev,
		Parent: e.Parents[0],
	} // Length not set
	n.setAtime(attr.Atime*1e9 + int64(attr.Atimensec))
	n.setMtime(attr.Mtime*1e9 + int64(attr.Mtimensec))
	n.setCtime(attr.Ctime*1e9 + int64(attr.Ctimensec))

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

	n.AccessACLId = m.saveACL(loadACL(e.AccessACL), aclMaxId)
	n.DefaultACLId = m.saveACL(loadACL(e.DefaultACL), aclMaxId)
	chs[0] <- n
}

func (m *dbMeta) getTxnBatchNum() int {
	switch m.Name() {
	case "sqlite3":
		return 999 / MaxFieldsCountOfTable
	case "mysql":
		return 65535 / MaxFieldsCountOfTable
	case "postgres":
		return 1000
	default:
		return 1000
	}
}

func (m *dbMeta) checkAddr() error {
	tables, err := m.db.DBMetas()
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		addr := m.addr
		if !strings.Contains(addr, "://") {
			addr = fmt.Sprintf("%s://%s", m.Name(), addr)
		}
		return fmt.Errorf("database %s is not empty", addr)
	}
	return nil
}

func (m *dbMeta) LoadMeta(r io.Reader) error {
	if err := m.checkAddr(); err != nil {
		return err
	}
	if err := m.syncAllTables(); err != nil {
		return err
	}

	batch := m.getTxnBatchNum()
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

	var aclMaxId uint32 = 0
	dm, counters, parents, refs, err := loadEntries(r,
		func(e *DumpedEntry) { m.loadEntry(e, chs, &aclMaxId) },
		func(ck *chunkKey) { chs[3] <- &sliceRef{ck.id, ck.size, 1} })
	if err != nil {
		return err
	}
	m.loadDumpedQuotas(Background(), dm.Quotas)
	if err = m.loadDumpedACLs(Background()); err != nil {
		return err
	}

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

		m.parseAttr(&n, attr)
		if eno := m.Access(ctx, srcIno, MODE_MASK_R, attr); eno != 0 {
			return eno
		}

		if cmode&CLONE_MODE_PRESERVE_ATTR == 0 {
			n.Uid = ctx.Uid()
			n.Gid = ctx.Gid()
			n.Mode &= ^cumask
			ns := now.UnixNano()
			n.setAtime(ns)
			n.setMtime(ns)
			n.setCtime(ns)
		}
		// TODO: preserve hardlink
		if n.Type == TypeFile && n.Nlink > 1 {
			n.Nlink = 1
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
				pn.setMtime(now)
				pn.setCtime(now)
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
							if _, err := s.Exec(m.sqlConv("update chunk_ref set refs=refs+1 where chunkid = ? AND size = ?"), sli.id, sli.size); err != nil {
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
	err := m.roTxn(Background(), func(s *xorm.Session) error {
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
		n.setMtime(now)
		n.setCtime(now)
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
		curNode.setAtime(now.UnixNano())
		attr.Atime = curNode.Atime / 1e6
		attr.Atimensec = uint32(curNode.Atime%1e6*1000) + uint32(curNode.Atimensec)
		if _, err = s.Cols("atime", "atimensec").Update(&curNode, &node{Inode: inode}); err == nil {
			updated = true
		}
		return err
	}, inode)
	return updated, err
}

func (m *dbMeta) insertACL(s *xorm.Session, rule *aclAPI.Rule) (uint32, error) {
	if rule == nil {
		return aclAPI.None, nil
	}
	if err := m.tryLoadMissACLs(s); err != nil {
		logger.Warnf("Mknode: load miss acls error: %s", err)
	}
	var aclId uint32
	if aclId = m.aclCache.GetId(rule); aclId == aclAPI.None {
		// TODO conflicts from multiple clients are rare and result in only minor duplicates, thus not addressed for now.
		val := newSQLAcl(rule)
		if _, err := s.Insert(val); err != nil {
			return aclAPI.None, err
		}
		aclId = val.Id
		m.aclCache.Put(aclId, rule)
	}
	return aclId, nil
}

func (m *dbMeta) tryLoadMissACLs(s *xorm.Session) error {
	missIds := m.aclCache.GetMissIds()
	if len(missIds) > 0 {
		var acls []acl
		if err := s.In("id", missIds).Find(&acls); err != nil {
			return err
		}

		got := make(map[uint32]struct{}, len(acls))
		for _, data := range acls {
			got[data.Id] = struct{}{}
			m.aclCache.Put(data.Id, data.toRule())
		}
		if len(acls) < len(missIds) {
			for _, id := range missIds {
				if _, ok := got[id]; !ok {
					m.aclCache.Put(id, aclAPI.EmptyRule())
				}
			}
		}
	}
	return nil
}

func (m *dbMeta) getACL(s *xorm.Session, id uint32) (*aclAPI.Rule, error) {
	if id == aclAPI.None {
		return nil, nil
	}
	if cRule := m.aclCache.Get(id); cRule != nil {
		return cRule, nil
	}

	var aclVal = &acl{Id: id}
	if ok, err := s.Get(aclVal); err != nil {
		return nil, err
	} else if !ok {
		return nil, syscall.EIO
	}

	r := aclVal.toRule()
	m.aclCache.Put(id, r)
	return r, nil
}

func (m *dbMeta) doSetFacl(ctx Context, ino Ino, aclType uint8, rule *aclAPI.Rule) syscall.Errno {
	return errno(m.txn(func(s *xorm.Session) error {
		attr := &Attr{}
		n := &node{Inode: ino}
		if ok, err := s.ForUpdate().Get(n); err != nil {
			return err
		} else if !ok {
			return syscall.ENOENT
		}
		m.parseAttr(n, attr)

		if ctx.Uid() != 0 && ctx.Uid() != attr.Uid {
			return syscall.EPERM
		}

		if attr.Flags&FlagImmutable != 0 {
			return syscall.EPERM
		}

		oriACL, oriMode := getAttrACLId(attr, aclType), attr.Mode

		// https://github.com/torvalds/linux/blob/480e035fc4c714fb5536e64ab9db04fedc89e910/fs/fuse/acl.c#L143-L151
		// TODO: check linux capabilities
		if ctx.Uid() != 0 && !inGroup(ctx, attr.Gid) {
			// clear sgid
			attr.Mode &= 05777
		}

		if rule.IsEmpty() {
			// remove acl
			setAttrACLId(attr, aclType, aclAPI.None)
		} else if rule.IsMinimal() && aclType == aclAPI.TypeAccess {
			// remove acl
			setAttrACLId(attr, aclType, aclAPI.None)
			// set mode
			attr.Mode &= 07000
			attr.Mode |= ((rule.Owner & 7) << 6) | ((rule.Group & 7) << 3) | (rule.Other & 7)
		} else {
			// set acl
			rule.InheritPerms(attr.Mode)
			aclId, err := m.insertACL(s, rule)
			if err != nil {
				return err
			}
			setAttrACLId(attr, aclType, aclId)

			// set mode
			if aclType == aclAPI.TypeAccess {
				attr.Mode &= 07000
				attr.Mode |= ((rule.Owner & 7) << 6) | ((rule.Mask & 7) << 3) | (rule.Other & 7)
			}
		}

		// update attr
		var updateCols []string
		if oriACL != getAttrACLId(attr, aclType) {
			updateCols = append(updateCols, getACLIdColName(aclType))
		}
		if oriMode != attr.Mode {
			updateCols = append(updateCols, "mode")
		}
		if len(updateCols) > 0 {
			updateCols = append(updateCols, "ctime", "ctimensec")

			var dirtyNode node
			m.parseNode(attr, &dirtyNode)
			dirtyNode.setCtime(time.Now().UnixNano())
			_, err := s.Cols(updateCols...).Update(&dirtyNode, &node{Inode: ino})
			return err
		}

		return nil
	}, ino))
}

func (m *dbMeta) doGetFacl(ctx Context, ino Ino, aclType uint8, aclId uint32, rule *aclAPI.Rule) syscall.Errno {
	return errno(m.roTxn(ctx, func(s *xorm.Session) error {
		if aclId == aclAPI.None {
			attr := &Attr{}
			n := &node{Inode: ino}
			if ok, err := s.Get(n); err != nil {
				return err
			} else if !ok {
				return syscall.ENOENT
			}
			m.parseAttr(n, attr)
			m.of.Update(ino, attr)
			aclId = getAttrACLId(attr, aclType)
		}

		a, err := m.getACL(s, aclId)
		if err != nil {
			return err
		}
		if a == nil {
			return ENOATTR
		}
		*rule = *a
		return nil
	}))
}

func (m *dbMeta) loadDumpedACLs(ctx Context) error {
	id2Rule := m.aclCache.GetAll()
	if len(id2Rule) == 0 {
		return nil
	}

	acls := make([]*acl, 0, len(id2Rule))
	for id, rule := range id2Rule {
		aclV := newSQLAcl(rule)
		aclV.Id = id
		acls = append(acls, aclV)
	}

	return m.txn(func(s *xorm.Session) error {
		n, err := s.Insert(acls)
		if err != nil {
			return err
		}
		if int(n) != len(acls) {
			return fmt.Errorf("only %d acls inserted, expected %d", n, len(acls))
		}
		return nil
	})
}

type dbDirHandler struct {
	dirHandler
}

func (m *dbMeta) newDirHandler(inode Ino, plus bool, entries []*Entry) DirHandler {
	h := &dbDirHandler{
		dirHandler: dirHandler{
			inode:       inode,
			plus:        plus,
			initEntries: entries,
			fetcher:     m.getDirFetcher(),
			batchNum:    DirBatchNum["db"],
		},
	}
	h.batch, _ = h.fetch(Background(), 0)
	return h
}

func (m *dbMeta) getDirFetcher() dirFetcher {
	return func(ctx Context, inode Ino, cursor interface{}, offset, limit int, plus bool) (interface{}, []*Entry, error) {
		entries := make([]*Entry, 0, limit)
		err := m.roTxn(Background(), func(s *xorm.Session) error {
			var name []byte
			if cursor != nil {
				name = cursor.([]byte)
			} else {
				if offset > 0 {
					var edges []edge
					if err := s.Table(&edge{}).Where("parent = ?", inode).OrderBy("name").Limit(1, offset-1).Find(&edges); err != nil {
						return err
					}
					if len(edges) < 1 {
						return nil
					}
					name = edges[0].Name
				}
			}

			var ids []int64
			var err error
			// sorted by (parent, name) index
			if name == nil {
				err = s.Table(&edge{}).Cols("id").Where("parent = ?", inode).OrderBy("name").Limit(limit).Find(&ids)
			} else {
				err = s.Table(&edge{}).Cols("id").Where("parent = ? and name > ?", inode, name).OrderBy("name").Limit(limit).Find(&ids)
			}
			if err != nil {
				return err
			}

			s = s.Table(&edge{}).In(m.sqlConv("edge.id"), ids).OrderBy(m.sqlConv("edge.name")) // need to sorted by name, otherwise the cursor will be invalid
			if plus {
				s = s.Join("INNER", &node{}, m.sqlConv("edge.inode=node.inode")).Cols(m.sqlConv("edge.name"), m.sqlConv("node.*"))
			} else {
				s = s.Cols(m.sqlConv("edge.id"), m.sqlConv("edge.name"), m.sqlConv("edge.type"))
			}
			var nodes []namedNode
			if err := s.Find(&nodes); err != nil {
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
				if plus {
					m.parseAttr(&n.node, entry.Attr)
					m.of.Update(n.Inode, entry.Attr)
				} else {
					entry.Attr.Typ = n.Type
				}
				entries = append(entries, entry)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		if len(entries) == 0 {
			return nil, nil, nil
		}
		return entries[len(entries)-1].Name, entries, nil
	}
}
