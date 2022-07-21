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
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// redisStore stores data chunks into Redis.
type redisStore struct {
	DefaultObjectStorage
	rdb       redis.UniversalClient
	uri       string
	isCluster bool
	opt       *redis.Options
}

var c = context.TODO()

func (r *redisStore) String() string {
	return r.uri + "/"
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
	var scanCli []redis.UniversalClient
	if t.isCluster {
		/**
		ClusterNodes result sample:
		cluster nodes: a43f22ff3b2d07ed46db8964bcd68be22b1bf3b8 127.0.0.1:7001@17001 master - 0 1658404421000 2 connected 5461-10922
		411d0d6e1ac7b0276400362971073f69e68fa195 127.0.0.1:7000@17000 master - 0 1658404422183 1 connected 0-5460
		d5a92ff5fdcfaa5d8d15195a2742a1ceae0a5f79 127.0.0.1:7002@17002 myself,master - 0 1658404420000 3 connected 10923-16383 [29-<-411d0d6e1ac7b0276400362971073f69e68fa195]
		cbc49ef4b24df6ee5a2dbedfc22e7e6970a18305 127.0.0.1:7003@17003 slave a43f22ff3b2d07ed46db8964bcd68be22b1bf3b8 0 1658404421177 2 connected
		*/
		nodes := strings.Split(strings.TrimPrefix(t.rdb.ClusterNodes(context.Background()).String(), "cluster nodes: "), "\n")
		for _, node := range nodes {
			infos := strings.SplitN(node, " ", 4)
			if len(infos) >= 3 && strings.Contains(infos[2], "master") {
				opt := new(redis.Options)
				*opt = *t.opt
				opt.Addr = strings.Split(infos[1], "@")[0]
				scanCli = append(scanCli, redis.NewClient(opt))
			}
		}
		if len(scanCli) == 0 {
			return nil, fmt.Errorf("not found master node of redis cluster")
		}
		defer func() {
			for _, mCli := range scanCli {
				_ = mCli.Close()
			}
		}()
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
	var isCluster bool
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
			isCluster = true
		}
	}
	u.User = new(url.Userinfo)
	return &redisStore{DefaultObjectStorage{}, rdb, u.String(), isCluster, opt}, nil
}

func init() {
	Register("redis", newRedis)
}
