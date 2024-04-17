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
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore stores data chunks into Redis.
type redisStore struct {
	DefaultObjectStorage
	rdb redis.UniversalClient
	uri string
}

func (r *redisStore) String() string {
	return r.uri + "/"
}

func (r *redisStore) Create() error {
	return nil
}

func (r *redisStore) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	data, err := r.rdb.Get(ctx, key).Bytes()
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
	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (r *redisStore) Put(key string, in io.Reader, getters ...AttrGetter) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, key, data, 0).Err()
}

func (r *redisStore) Delete(key string, getters ...AttrGetter) error {
	return r.rdb.Del(ctx, key).Err()
}

func (t *redisStore) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	var scanCli []redis.UniversalClient
	var m sync.Mutex
	if c, ok := t.rdb.(*redis.ClusterClient); ok {
		err := c.ForEachMaster(context.TODO(), func(ctx context.Context, client *redis.Client) error {
			m.Lock()
			defer m.Unlock()
			scanCli = append(scanCli, client)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		scanCli = append(scanCli, t.rdb)
	}
	batch := 1000
	var objs = make(chan Object, batch)
	var keyList []string
	var cursor uint64
	for _, mCli := range scanCli {
		for {
			// FIXME: this will be really slow for many objects
			keys, c, err := mCli.Scan(context.TODO(), cursor, prefix+"*", int64(batch)).Result()
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
				p.StrLen(ctx, key)
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
					objs <- &obj{keyList[start:end][idx], size, now, strings.HasSuffix(keyList[start:end][idx], "/"), ""}
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
		"",
	}, err
}

func newRedis(uri, user, passwd, token string) (ObjectStorage, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("url parse %s: %s", uri, err)
	}
	hosts := u.Host
	opt, err := redis.ParseURL(u.String())
	if err != nil {
		return nil, fmt.Errorf("redis parse %s: %s", uri, err)
	}
	if user != "" {
		opt.Username = user
	}
	if passwd != "" {
		opt.Password = passwd
	}
	if opt.MaxRetries == 0 {
		opt.MaxRetries = -1 // Redis use -1 to disable retries
	}
	var rdb redis.UniversalClient
	if strings.Contains(hosts, ",") && strings.Index(hosts, ",") < strings.Index(hosts, ":") {
		var fopt redis.FailoverOptions
		ps := strings.Split(hosts, ",")
		fopt.MasterName = ps[0]
		fopt.SentinelAddrs = ps[1:]
		_, port, _ := net.SplitHostPort(fopt.SentinelAddrs[len(fopt.SentinelAddrs)-1])
		if port == "" {
			port = "26379"
		}
		for i, addr := range fopt.SentinelAddrs {
			h, p, e := net.SplitHostPort(addr)
			if e != nil {
				fopt.SentinelAddrs[i] = net.JoinHostPort(addr, port)
			} else if p == "" {
				fopt.SentinelAddrs[i] = net.JoinHostPort(h, port)
			}
		}
		fopt.SentinelPassword = os.Getenv("SENTINEL_PASSWORD_FOR_OBJ")
		fopt.DB = opt.DB
		fopt.Username = opt.Username
		fopt.Password = opt.Password
		fopt.TLSConfig = opt.TLSConfig
		fopt.MaxRetries = opt.MaxRetries
		fopt.MinRetryBackoff = opt.MinRetryBackoff
		fopt.MaxRetryBackoff = opt.MaxRetryBackoff
		fopt.ReadTimeout = opt.ReadTimeout
		fopt.WriteTimeout = opt.WriteTimeout
		rdb = redis.NewFailoverClient(&fopt)
	} else {
		if !strings.Contains(hosts, ",") {
			c := redis.NewClient(opt)
			info, err := c.ClusterInfo(context.Background()).Result()
			if err != nil && strings.Contains(err.Error(), "cluster mode") || err == nil && strings.Contains(info, "cluster_state:") {
				logger.Infof("redis %s is in cluster mode", hosts)
			} else {
				rdb = c
			}
		}
		if rdb == nil {
			var copt redis.ClusterOptions
			copt.Addrs = strings.Split(hosts, ",")
			copt.MaxRedirects = 1
			copt.Username = opt.Username
			copt.Password = opt.Password
			copt.TLSConfig = opt.TLSConfig
			copt.MaxRetries = opt.MaxRetries
			copt.MinRetryBackoff = opt.MinRetryBackoff
			copt.MaxRetryBackoff = opt.MaxRetryBackoff
			copt.ReadTimeout = opt.ReadTimeout
			copt.WriteTimeout = opt.WriteTimeout
			rdb = redis.NewClusterClient(&copt)
		}
	}
	u.User = new(url.Userinfo)
	return &redisStore{DefaultObjectStorage{}, rdb, u.String()}, nil
}

func init() {
	Register("redis", newRedis)
}
