//go:build !noetcd
// +build !noetcd

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
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	etcd "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/pkg/transport"
)

type etcdClient struct {
	DefaultObjectStorage
	client *etcd.Client
	kv     etcd.KV
	addr   string
}

func (c *etcdClient) String() string {
	return fmt.Sprintf("etcd://%s/", c.addr)
}

func (c *etcdClient) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	resp, err := c.kv.Get(context.TODO(), key, etcd.WithLimit(1))
	if err != nil {
		return nil, err
	}
	for _, pair := range resp.Kvs {
		if string(pair.Key) == key {
			if off > int64(len(pair.Value)) {
				off = int64(len(pair.Value))
			}
			data := pair.Value[off:]
			if limit > 0 && limit < int64(len(data)) {
				data = data[:limit]
			}
			return io.NopCloser(bytes.NewBuffer(data)), nil
		}
	}
	return nil, os.ErrNotExist
}

func (c *etcdClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	d, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	_, err = c.kv.Put(context.TODO(), key, string(d))
	return err
}

func (c *etcdClient) Head(key string) (Object, error) {
	resp, err := c.kv.Get(context.TODO(), key, etcd.WithLimit(1))
	if err != nil {
		return nil, err
	}
	for _, p := range resp.Kvs {
		if string(p.Key) == key {
			return &obj{
				key,
				int64(len(p.Value)),
				time.Now(),
				strings.HasSuffix(key, "/"),
				"",
			}, nil
		}
	}
	return nil, os.ErrNotExist
}

func (c *etcdClient) Delete(key string, getters ...AttrGetter) error {
	_, err := c.kv.Delete(context.TODO(), key)
	return err
}

func genNextKey(key string) string {
	next := make([]byte, len(key))
	copy(next, key)
	p := len(next) - 1
	next[p]++
	for next[p] == 0 {
		p--
		next[p]++
	}
	return string(next)
}

func (c *etcdClient) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "" {
		return nil, false, "", notSupported
	}
	if start == "" {
		start = prefix
	}
	var opts = []etcd.OpOption{etcd.WithLimit(limit), etcd.WithSort(etcd.SortByKey, etcd.SortAscend)}
	if len(prefix) > 0 && prefix[0] != 0xFF {
		opts = append(opts, etcd.WithRange(genNextKey(prefix)))
	} else {
		opts = append(opts, etcd.WithFromKey())
	}
	resp, err := c.client.Get(context.Background(), start, opts...)
	if err != nil {
		return nil, false, "", fmt.Errorf("get start %v: %s", start, err)
	}
	var objs []Object
	for _, kv := range resp.Kvs {
		k := string(kv.Key)
		if !strings.HasPrefix(k, prefix) {
			break
		}
		objs = append(objs, &obj{
			k,
			int64(len(kv.Value)),
			time.Now(),
			strings.HasSuffix(k, "/"),
			"",
		})
	}
	var nextMarker string
	if resp.More && len(objs) > 0 {
		nextMarker = objs[len(objs)-1].Key()
	}
	return objs, resp.More, nextMarker, nil
}

func buildTlsConfig(u *url.URL) (*tls.Config, error) {
	var tsinfo transport.TLSInfo
	q := u.Query()
	tsinfo.CAFile = q.Get("cacert")
	tsinfo.CertFile = q.Get("cert")
	tsinfo.KeyFile = q.Get("key")
	tsinfo.ServerName = q.Get("server-name")
	tsinfo.InsecureSkipVerify = q.Get("insecure-skip-verify") != ""
	if tsinfo.CAFile != "" || tsinfo.CertFile != "" || tsinfo.KeyFile != "" || tsinfo.ServerName != "" {
		return tsinfo.ClientConfig()
	}
	return nil, nil
}

func newEtcd(addr, user, passwd, token string) (ObjectStorage, error) {
	if !strings.HasPrefix(addr, "etcd://") {
		addr = "etcd://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", addr, err)
	}
	hosts := strings.Split(u.Host, ",")
	for i, h := range hosts {
		h, _, err := net.SplitHostPort(h)
		if err != nil {
			hosts[i] = net.JoinHostPort(h, "2379")
		}
	}
	conf := etcd.Config{
		Endpoints:        hosts,
		Username:         user,
		Password:         passwd,
		AutoSyncInterval: time.Minute,
	}
	conf.TLS, err = buildTlsConfig(u)
	if err != nil {
		return nil, fmt.Errorf("build tls config from %s: %s", u.RawQuery, err)
	}
	c, err := etcd.New(conf)
	if err != nil {
		return nil, err
	}
	return &etcdClient{DefaultObjectStorage{}, c, c.KV, u.Host}, nil
}

func init() {
	Register("etcd", newEtcd)
}
