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
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	etcd "go.etcd.io/etcd/client/v3"
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
	resp, err := tx.kv.Get(tx.ctx, k, etcd.WithLimit(1),
		etcd.WithSerializable())
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

func (tx *etcdTxn) gets(keys ...[]byte) [][]byte {
	// TODO: batch
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *etcdTxn) scanRange(begin_, end_ []byte) map[string][]byte {
	return tx.scanRange0(begin_, end_, nil)
}

func (tx *etcdTxn) scanRange0(begin_, end_ []byte, filter func(k, v []byte) bool) map[string][]byte {
	resp, err := tx.kv.Get(tx.ctx,
		string(begin_),
		etcd.WithRange(string(end_)),
		etcd.WithSerializable())
	if err != nil {
		panic(fmt.Errorf("get range [%v-%v): %s", string(begin_), string(end_), err))
	}
	ret := make(map[string][]byte)
	for _, kv := range resp.Kvs {
		if filter == nil || filter(kv.Key, kv.Value) {
			k := string(kv.Key)
			tx.observed[k] = kv.ModRevision
			ret[k] = kv.Value
		}
	}
	return ret
}

func (tx *etcdTxn) scan(prefix []byte, handler func(key []byte, value []byte)) {
	resp, err := tx.kv.Get(tx.ctx,
		string(prefix),
		etcd.WithPrefix(),
		etcd.WithSort(etcd.SortByKey, etcd.SortAscend),
		etcd.WithSerializable())
	if err != nil {
		panic(fmt.Errorf("get prefix %v: %s", string(prefix), err))
	}
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = kv.ModRevision
		handler(kv.Key, kv.Value)
	}
}

func (tx *etcdTxn) scanKeys(prefix []byte) [][]byte {
	resp, err := tx.kv.Get(tx.ctx,
		string(prefix),
		etcd.WithPrefix(),
		etcd.WithKeysOnly(),
		etcd.WithSort(etcd.SortByKey, etcd.SortAscend),
		etcd.WithSerializable())
	if err != nil {
		panic(fmt.Errorf("get prefix %v with keys only: %s", string(prefix), err))
	}
	var keys [][]byte
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = kv.ModRevision
		keys = append(keys, kv.Key)
	}
	return keys
}

func (tx *etcdTxn) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte {
	if limit == 0 {
		return nil
	}
	// TODO: use Limit option if filter is nil
	res := tx.scanRange0(prefix, nextKey(prefix), filter)
	if n := len(res) - limit; limit > 0 && n > 0 {
		for k := range res {
			delete(res, k)
			if n--; n == 0 {
				break
			}
		}
	}
	return res
}

func (tx *etcdTxn) exist(prefix []byte) bool {
	resp, err := tx.kv.Get(tx.ctx, string(prefix), etcd.WithPrefix(),
		etcd.WithCountOnly(), etcd.WithSerializable())
	if err != nil {
		panic(fmt.Errorf("get prefix %v with count only: %s", string(prefix), err))
	}
	return resp.Count > 0
}

func (tx *etcdTxn) set(key, value []byte) {
	tx.buffer[string(key)] = value
}

func (tx *etcdTxn) append(key []byte, value []byte) []byte {
	new := append(tx.get(key), value...)
	tx.set(key, new)
	return new
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

func (tx *etcdTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		tx.buffer[string(key)] = nil
	}
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

func (c *etcdClient) txn(f func(kvTxn) error) (err error) {
	ctx := context.Background()
	tx := &etcdTxn{
		ctx,
		c.kv,
		make(map[string]int64),
		make(map[string][]byte),
	}
	start := time.Now()
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
	err = f(tx)
	if err != nil {
		return err
	}
	if len(tx.buffer) == 0 {
		return nil // read only
	}
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
	resp, err := c.kv.Txn(ctx).If(conds...).Then(ops...).Commit()
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

var conflicted = errors.New("conflicted transaction")

func (c *etcdClient) reset(prefix []byte) error {
	_, err := c.kv.Delete(context.Background(), string(prefix), etcd.WithPrefix())
	return err
}

func (c *etcdClient) close() error {
	return c.client.Close()
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
	conf := etcd.Config{
		Endpoints: strings.Split(u.Host, ","),
		Username:  u.User.Username(),
		Password:  passwd,
	}
	c, err := etcd.New(conf)
	if err != nil {
		return nil, err
	}
	var prefix string = u.Path + "\xFD"
	return withPrefix(&etcdClient{c, etcd.NewKV(c)}, []byte(prefix)), nil
}

func init() {
	Register("etcd", newKVMeta)
	drivers["etcd"] = newEtcdClient
}
