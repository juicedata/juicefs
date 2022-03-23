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
	"time"

	etcd "go.etcd.io/etcd/clientv3"
)

type etcdTxn struct {
	kv       etcd.KV
	observed map[string]int
	buffer   map[string][]byte
}

func (tx *etcdTxn) get(key []byte) []byte {
	k := string(key)
	if v, ok := tx.buffer[k]; ok {
		return v
	}
	resp, err := tx.kv.Get(context.Background(), string(key), etcd.WithLimit(1), etcd.WithSerializable())
	if err != nil {
		panic(err)
	}
	if resp.Count == 0 {
		tx.observed[k] = 0
		return nil
	}
	for _, pair := range resp.Kvs {
		if bytes.Equal(pair.Key, key) {
			tx.observed[k] = int(pair.ModRevision)
			return pair.Value
		}
	}
	panic("not found")
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
	resp, err := tx.kv.Get(context.Background(), string(begin_), etcd.WithRange(string(end_)))
	if err != nil {
		panic(err)
	}
	ret := make(map[string][]byte)
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = int(kv.ModRevision)
		ret[string(kv.Key)] = kv.Value
	}
	return ret
}

func (tx *etcdTxn) scan(prefix []byte, handler func(key []byte, value []byte)) {
	resp, err := tx.kv.Get(context.Background(), string(prefix), etcd.WithPrefix(),
		etcd.WithSort(etcd.SortByKey, etcd.SortAscend))
	if err != nil {
		panic(err)
	}
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = int(kv.ModRevision)
		handler(kv.Key, kv.Value)
	}
}

func (tx *etcdTxn) scanKeys(prefix_ []byte) [][]byte {
	resp, err := tx.kv.Get(context.Background(), string(prefix_), etcd.WithPrefix(), etcd.WithKeysOnly(),
		etcd.WithSort(etcd.SortByKey, etcd.SortAscend))
	if err != nil {
		panic(err)
	}
	var keys [][]byte
	for _, kv := range resp.Kvs {
		tx.observed[string(kv.Key)] = int(kv.ModRevision)
		keys = append(keys, kv.Key)
	}
	return keys
}

func (tx *etcdTxn) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte {
	if limit == 0 {
		return nil
	}

	res := tx.scanRange(prefix, nextKey(prefix))
	for k, v := range res {
		if filter != nil && !filter([]byte(k), v) {
			delete(res, k)
		}
	}
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
	resp, err := tx.kv.Get(context.Background(), string(prefix), etcd.WithPrefix(), etcd.WithCountOnly())
	if err != nil {
		panic(err)
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
	return false
}

func (c *etcdClient) txn(f func(kvTxn) error) (err error) {
	tx := &etcdTxn{
		c.kv,
		make(map[string]int),
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
	resp, err := c.kv.Txn(context.Background()).If(conds...).Then(ops...).Commit()
	logger.Infof("txt with %d cond %d op took %s", len(conds), len(ops), time.Since(start))
	if err != nil {
		return err
	}
	if resp.Succeeded {
		return nil
	}
	// Try again
	return
}

func (c *etcdClient) reset(prefix []byte) error {
	_, err := c.kv.Delete(context.Background(), string(prefix), etcd.WithPrefix())
	return err
}

func (c *etcdClient) close() error {
	return c.client.Close()
}

func newEtcdClient(addr string) (tkvClient, error) {
	c, err := etcd.NewFromURL(addr)
	if err != nil {
		return nil, err
	}
	return &etcdClient{c, etcd.NewKV(c)}, err
}

func init() {
	Register("etcd", newKVMeta)
	drivers["etcd"] = newEtcdClient
}
