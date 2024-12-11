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

package meta

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	etcd "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/pkg/transport"
)

type etcdTxn struct {
	ctx      context.Context
	kv       etcd.KV
	observed map[string]int64
	buffer   map[string][]byte
}

func (tx *etcdTxn) get(key []byte) []byte {
	k := string(key)
	if v, ok := tx.buffer[k]; ok {
		return v
	}
	resp, err := tx.kv.Get(tx.ctx, k, etcd.WithLimit(1))
	if err != nil {
		panic(fmt.Errorf("get %v: %s", k, err))
	}
	if resp.Count == 0 {
		tx.observed[k] = 0
		return nil
	}
	if resp.Count > 1 {
		panic(fmt.Errorf("expect 1 keys but got %d", resp.Count))
	}
	for _, pair := range resp.Kvs {
		if bytes.Equal(pair.Key, key) {
			tx.observed[k] = pair.ModRevision
			return pair.Value
		} else {
			panic(fmt.Errorf("expect key %v, but got %v", k, string(pair.Key)))
		}
	}
	panic("unreachable")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (tx *etcdTxn) gets(keys ...[]byte) [][]byte {
	if len(keys) > 128 {
		var rs = make([][]byte, 0, len(keys))
		for i := 0; i < len(keys); i += 128 {
			rs = append(rs, tx.gets(keys[i:min(i+128, len(keys))]...)...)
		}
		return rs
	}
	ops := make([]etcd.Op, len(keys))
	for i, key := range keys {
		ops[i] = etcd.OpGet(string(key))
	}
	r, err := tx.kv.Do(tx.ctx, etcd.OpTxn(nil, ops, nil))
	if err != nil {
		panic(fmt.Errorf("batch get with %d keys: %s", len(keys), err))
	}
	rs := make(map[string][]byte)
	for _, res := range r.Txn().Responses {
		for _, p := range res.GetResponseRange().Kvs {
			k := string(p.Key)
			tx.observed[k] = p.ModRevision
			rs[k] = p.Value
		}
	}
	values := make([][]byte, len(keys))
	for i, key := range keys {
		k := string(key)
		if v, ok := tx.buffer[k]; ok {
			values[i] = v
			continue
		}
		values[i] = rs[k]
		if len(values[i]) == 0 {
			tx.observed[k] = 0
		}
	}
	return values
}

func (tx *etcdTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	opts := []etcd.OpOption{etcd.WithRange(string(end))}
	if keysOnly {
		opts = append(opts, etcd.WithKeysOnly())
	}
	resp, err := tx.kv.Get(tx.ctx, string(begin), opts...)
	if err != nil {
		panic(fmt.Errorf("get range [%v-%v): %s", string(begin), string(end), err))
	}
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = kv.ModRevision
		if !handler(kv.Key, kv.Value) {
			break
		}
	}
}

func (tx *etcdTxn) exist(prefix []byte) bool {
	resp, err := tx.kv.Get(tx.ctx, string(prefix), etcd.WithPrefix(), etcd.WithCountOnly())
	if err != nil {
		panic(fmt.Errorf("get prefix %v with count only: %s", string(prefix), err))
	}
	return resp.Count > 0
}

func (tx *etcdTxn) set(key, value []byte) {
	tx.buffer[string(key)] = value
	if len(tx.buffer) >= 128 {
		err := tx.commmit()
		if err != nil {
			panic(err)
		}
		tx.observed = make(map[string]int64)
		tx.buffer = make(map[string][]byte)
	}
}

func (tx *etcdTxn) append(key []byte, value []byte) {
	new := append(tx.get(key), value...)
	tx.set(key, new)
}

func (tx *etcdTxn) incrBy(key []byte, value int64) int64 {
	buf := tx.get(key)
	new := parseCounter(buf)
	if value != 0 {
		new += value
		tx.set(key, packCounter(new))
	}
	return new
}

func (tx *etcdTxn) delete(key []byte) {
	tx.buffer[string(key)] = nil
}

func (tx *etcdTxn) commmit() error {
	start := time.Now()
	var conds []etcd.Cmp
	var ops []etcd.Op
	for k, v := range tx.observed {
		conds = append(conds, etcd.Compare(etcd.ModRevision(k), "=", v))
	}
	for k, v := range tx.buffer {
		var op etcd.Op
		if v == nil {
			op = etcd.OpDelete(string(k))
		} else {
			op = etcd.OpPut(string(k), string(v))
		}
		ops = append(ops, op)
	}
	resp, err := tx.kv.Txn(tx.ctx).If(conds...).Then(ops...).Commit()
	if time.Since(start) > time.Millisecond*10 {
		logger.Debugf("txn with %d conds and %d ops took %s", len(conds), len(ops), time.Since(start))
	}
	if err != nil {
		return err
	}
	if resp.Succeeded {
		return nil
	}
	return conflicted
}

type etcdClient struct {
	client *etcd.Client
	kv     etcd.KV
}

func (c *etcdClient) name() string {
	return "etcd"
}

func (c *etcdClient) shouldRetry(err error) bool {
	return errors.Is(err, conflicted)
}

func (c *etcdClient) config(key string) interface{} {
	return nil
}

func (c *etcdClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	tx := &etcdTxn{
		ctx,
		c.kv,
		make(map[string]int64),
		make(map[string][]byte),
	}
	defer func() {
		if r := recover(); r != nil {
			fe, ok := r.(error)
			if ok {
				err = fe
			} else {
				panic(r)
			}
		}
	}()
	err = f(&kvTxn{tx, retry})
	if err != nil {
		return err
	}
	if len(tx.buffer) == 0 {
		return nil // read only
	}
	return tx.commmit()
}

var conflicted = errors.New("conflicted transaction")

func (c *etcdClient) scan(prefix []byte, handler func(key []byte, value []byte)) error {
	var start = prefix
	var end = string(nextKey(prefix))
	resp, err := c.client.Get(context.Background(), "anything")
	if err != nil {
		return err
	}
	currentRev := resp.Header.Revision
	var following bool
	for {
		resp, err := c.client.Get(context.Background(),
			string(start),
			etcd.WithRange(end),
			etcd.WithLimit(1024),
			etcd.WithMaxModRev(currentRev),
			etcd.WithSerializable())
		if err != nil {
			return fmt.Errorf("get start %v: %s", string(start), err)
		}
		if following && len(resp.Kvs) > 0 {
			resp.Kvs = resp.Kvs[1:]
		}
		if len(resp.Kvs) == 0 {
			break
		}
		for _, kv := range resp.Kvs {
			handler(kv.Key, kv.Value)
		}
		start = resp.Kvs[len(resp.Kvs)-1].Key
		following = true
	}
	return nil
}

func (c *etcdClient) reset(prefix []byte) error {
	_, err := c.kv.Delete(context.Background(), string(prefix), etcd.WithPrefix())
	return err
}

func (c *etcdClient) close() error {
	return c.client.Close()
}

func (c *etcdClient) gc() {}

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

func newEtcdClient(addr string) (tkvClient, error) {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %s", addr, err)
	}
	passwd, _ := u.User.Password()
	hosts := strings.Split(u.Host, ",")
	for i, h := range hosts {
		h, _, err := net.SplitHostPort(h)
		if err != nil {
			hosts[i] = net.JoinHostPort(h, "2379")
		}
	}
	conf := etcd.Config{
		Endpoints:        hosts,
		Username:         u.User.Username(),
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
	maxCompactSlices = 100
	var prefix string = u.Path + "\xFD"
	return withPrefix(&etcdClient{c, etcd.NewKV(c)}, []byte(prefix)), nil
}

func init() {
	Register("etcd", newKVMeta)
	drivers["etcd"] = newEtcdClient
}
