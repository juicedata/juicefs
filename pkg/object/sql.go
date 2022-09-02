//go:build !nosqlite || !nomysql || !nopg
// +build !nosqlite !nomysql !nopg

/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

package object

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
	"xorm.io/xorm"
	"xorm.io/xorm/log"
	"xorm.io/xorm/names"
)

type sqlStore struct {
	DefaultObjectStorage
	db   *xorm.Engine
	addr string
}

type blob struct {
	Id       int64     `xorm:"pk bigserial"`
	Key      string    `xorm:"notnull unique"`
	Size     int64     `xorm:"notnull"`
	Modified time.Time `xorm:"notnull updated"`
	Data     []byte    `xorm:"mediumblob"`
}

func (s *sqlStore) String() string {
	return fmt.Sprintf("%s://%s/", s.db.DriverName(), s.addr)
}

func (s *sqlStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	var b = blob{Key: key}
	// TODO: range
	ok, err := s.db.Get(&b)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	if off > int64(len(b.Data)) {
		off = int64(len(b.Data))
	}
	data := b.Data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (s *sqlStore) Put(key string, in io.Reader) error {
	d, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	var n int64
	now := time.Now()
	b := blob{Key: key, Data: d, Size: int64(len(d)), Modified: now}
	if s.db.DriverName() == "postgres" {
		var r sql.Result
		r, err = s.db.Exec("INSERT INTO jfs_blob(key, size,modified, data) VALUES(?, ?, ?,? ) "+
			"ON CONFLICT (key) DO UPDATE SET size=?,data=?", key, b.Size, now, d, b.Size, d)
		if err == nil {
			n, err = r.RowsAffected()
		}
	} else {
		n, err = s.db.Insert(&b)
		if err != nil || n == 0 {
			n, err = s.db.Update(&b, &blob{Key: key})
		}
	}
	if err == nil && n == 0 {
		err = errors.New("not inserted or updated")
	}
	return err
}

func (s *sqlStore) Head(key string) (Object, error) {
	var b = blob{Key: key}
	ok, err := s.db.Cols("key", "modified", "size").Get(&b)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	return &obj{
		key,
		b.Size,
		b.Modified,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *sqlStore) Delete(key string) error {
	_, err := s.db.Delete(&blob{Key: key})
	return err
}

func (s *sqlStore) List(prefix, marker string, limit int64) ([]Object, error) {
	if marker == "" {
		marker = prefix
	}
	var bs []blob
	err := s.db.Where("`key` >= ?", marker).Limit(int(limit)).Cols("`key`", "size", "modified").OrderBy("`key`").Find(&bs)
	if err != nil {
		return nil, err
	}
	var objs []Object
	for _, b := range bs {
		if strings.HasPrefix(b.Key, prefix) {
			objs = append(objs, &obj{
				key:   b.Key,
				size:  b.Size,
				mtime: b.Modified,
				isDir: strings.HasSuffix(b.Key, "/"),
			})
		} else {
			break
		}
	}
	return objs, nil
}

func newSQLStore(driver, addr, user, password string) (ObjectStorage, error) {
	var err error
	uri := addr
	if user != "" {
		uri = user + ":" + password + "@" + addr
	}
	var searchPath string
	if driver == "postgres" {
		uri = "postgres://" + uri

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
	engine, err := xorm.NewEngine(driver, uri)
	if err != nil {
		return nil, fmt.Errorf("open %s: %s", uri, err)
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
	if searchPath != "" {
		engine.SetSchema(searchPath)
	}
	engine.SetTableMapper(names.NewPrefixMapper(engine.GetTableMapper(), "jfs_"))
	if err := engine.Sync2(new(blob)); err != nil {
		return nil, fmt.Errorf("create table blob: %s", err)
	}
	return &sqlStore{DefaultObjectStorage{}, engine, addr}, nil
}

func init() {
	Register("sqlite3", func(addr, user, pass, token string) (ObjectStorage, error) {
		p := strings.Index(addr, "://")
		if p > 0 {
			addr = addr[p+3:]
		}
		return newSQLStore("sqlite3", addr, user, pass)
	})
	Register("mysql", func(addr, user, pass, token string) (ObjectStorage, error) {
		p := strings.Index(addr, "://")
		if p > 0 {
			addr = addr[p+3:]
		}
		return newSQLStore("mysql", addr, user, pass)
	})
	Register("postgres", func(addr, user, pass, token string) (ObjectStorage, error) {
		return newSQLStore("postgres", addr, user, pass)
	})
}
