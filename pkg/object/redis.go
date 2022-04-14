//go:build !noredis
// +build !noredis

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

package object

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"sort"
	"strings"
	"time"

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

func (t *redisStore) ListAll(prefix, marker string) (<-chan Object, error) {
	var objs = make(chan Object, 1000)
	defer close(objs)
	var keyList []string
	var cursor uint64
	for {
		// FIXME: this will be really slow for many objects
		keys, c, err := t.rdb.Scan(context.TODO(), cursor, "*", 1000).Result()
		if err != nil {
			logger.Warnf("redis scan error, coursor %d: %s", cursor, err)
			return nil, err
		}
		for _, key := range keys {
			if key > marker && strings.HasPrefix(key, prefix) {
				keyList = append(keyList, key)
			}
		}
		if c == 0 {
			break
		}
		cursor = c
	}
	sort.Strings(keyList)
	now := time.Now()
	for _, key := range keyList {
		data, err := t.rdb.Get(c, key).Bytes()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		// FIXME: mtime
		objs <- &obj{key, int64(len(data)), now, strings.HasSuffix(key, "/")}
	}
	return objs, nil
}

func (t *redisStore) Head(key string) (Object, error) {
	data, err := t.rdb.Get(context.TODO(), key).Bytes()
	return &obj{
		key,
		int64(len(data)),
		time.Now(),
		strings.HasSuffix(key, "/"),
	}, err
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
