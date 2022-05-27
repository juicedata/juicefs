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
	"os"
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
	if off > int64(len(data)) {
		off = int64(len(data))
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
	batch := 1000
	var objs = make(chan Object, batch)
	var keyList []string
	var cursor uint64
	for {
		// FIXME: this will be really slow for many objects
		keys, c, err := t.rdb.Scan(context.TODO(), cursor, prefix+"*", int64(batch)).Result()
		if err != nil {
			logger.Warnf("redis scan error, coursor %d: %s", cursor, err)
			return nil, err
		}
		for _, key := range keys {
			if key > marker {
				keyList = append(keyList, key)
			}
		}
		if c == 0 {
			break
		}
		cursor = c
	}

	sort.Strings(keyList)

	go func() {
		defer close(objs)
		lKeyList := len(keyList)
		for start := 0; start < lKeyList; start += batch {
			end := start + batch
			if end > lKeyList {
				end = lKeyList
			}

			p := t.rdb.Pipeline()
			for _, key := range keyList[start:end] {
				p.StrLen(c, key)
			}
			cmds, err := p.Exec(ctx)
			if err != nil {
				objs <- nil
				return
			}

			now := time.Now()
			for idx, cmd := range cmds {
				if intCmd, ok := cmd.(*redis.IntCmd); ok {
					size, err := intCmd.Result()
					if err != nil {
						objs <- nil
						return
					}
					if size == 0 {
						exist, err := t.rdb.Exists(context.TODO(), keyList[start:end][idx]).Result()
						if err != nil {
							objs <- nil
							return
						}
						if exist == 0 {
							continue
						}
					}
					// FIXME: mtime
					objs <- &obj{keyList[start:end][idx], size, now, strings.HasSuffix(keyList[start:end][idx], "/")}
				}
			}
		}
	}()
	return objs, nil
}

func (t *redisStore) Head(key string) (Object, error) {
	data, err := t.rdb.Get(context.TODO(), key).Bytes()
	if err == redis.Nil {
		return nil, os.ErrNotExist
	}
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
