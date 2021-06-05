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

package object

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/go-redis/redis/v8"
)

// redisStore stores data chunks into Redis.
type redisStore struct {
	DefaultObjectStorage
	rdb *redis.Client
}

var c = context.TODO()

func (r *redisStore) String() string {
	return fmt.Sprintf("redis://%s/", r.rdb.Options().Addr)
}

func (r *redisStore) Create() error {
	return nil
}

func (r *redisStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	data, err := r.rdb.Get(c, key).Bytes()
	if err != nil {
		return nil, err
	}
	data = data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (r *redisStore) Put(key string, in io.Reader) error {
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	return r.rdb.Set(c, key, data, 0).Err()
}

func (r *redisStore) Delete(key string) error {
	return r.rdb.Del(c, key).Err()
}

func newRedis(url, user, passwd string) (ObjectStorage, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", url, err)
	}
	if user != "" {
		opt.Username = user
	}
	if passwd != "" {
		opt.Password = passwd
	}
	rdb := redis.NewClient(opt)
	return &redisStore{DefaultObjectStorage{}, rdb}, nil
}

func init() {
	Register("redis", newRedis)
}
