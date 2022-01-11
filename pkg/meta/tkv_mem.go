//go:build !fdb
// +build !fdb

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/google/btree"
)

func init() {
	Register("memkv", newKVMeta)
	drivers["memkv"] = newMockClient
}

const settingPath = "/tmp/juicefs.memkv.setting.json"

func newMockClient(addr string) (tkvClient, error) {
	client := &memKV{items: btree.New(2), temp: &kvItem{}}
	if d, err := ioutil.ReadFile(settingPath); err == nil {
		var buffer map[string][]byte
		if err = json.Unmarshal(d, &buffer); err == nil {
			for k, v := range buffer {
				client.set(k, v) // not locked
			}
		}
	}
	return client, nil
}

type memTxn struct {
	store    *memKV
	observed map[string]int
	buffer   map[string][]byte
}

func (tx *memTxn) get(key []byte) []byte {
	k := string(key)
	if v, ok := tx.buffer[k]; ok {
		return v
	}
	tx.store.Lock()
	defer tx.store.Unlock()
	it := tx.store.get(k)
	if it != nil {
		tx.observed[k] = it.ver
		return it.value
	} else {
		tx.observed[k] = 0
		return nil
	}
}

func (tx *memTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *memTxn) scanRange(begin_, end_ []byte) map[string][]byte {
	tx.store.Lock()
	defer tx.store.Unlock()
	begin := string(begin_)
	end := string(end_)
	ret := make(map[string][]byte)
	tx.store.items.AscendGreaterOrEqual(&kvItem{key: begin}, func(i btree.Item) bool {
		it := i.(*kvItem)
		if end == "" || it.key < end {
			tx.observed[it.key] = it.ver
			ret[it.key] = it.value
			return true
		}
		return false
	})
	return ret
}

func (tx *memTxn) scan(prefix []byte, handler func(key []byte, value []byte)) {
	tx.store.Lock()
	defer tx.store.Unlock()
	begin := string(prefix)
	tx.store.items.AscendGreaterOrEqual(&kvItem{key: begin}, func(i btree.Item) bool {
		it := i.(*kvItem)
		tx.observed[it.key] = it.ver
		handler([]byte(it.key), it.value)
		return true
	})
}

func nextKey(key []byte) []byte {
	if len(key) == 0 {
		return nil
	}
	next := make([]byte, len(key))
	copy(next, key)
	p := len(next) - 1
	for {
		next[p]++
		if next[p] != 0 {
			break
		}
		p--
		if p < 0 {
			panic("can't scan keys for 0xFF")
		}
	}
	return next
}

func (tx *memTxn) scanKeys(prefix []byte) [][]byte {
	var keys [][]byte
	for k := range tx.scanValues(prefix, nil) {
		keys = append(keys, []byte(k))
	}
	return keys
}

func (tx *memTxn) scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte {
	res := tx.scanRange(prefix, nextKey(prefix))
	for k, v := range res {
		if filter != nil && !filter([]byte(k), v) {
			delete(res, k)
		}
	}
	return res
}

func (tx *memTxn) exist(prefix []byte) bool {
	return len(tx.scanKeys(prefix)) > 0
}

func (tx *memTxn) set(key, value []byte) {
	tx.buffer[string(key)] = value
}

func (tx *memTxn) append(key []byte, value []byte) []byte {
	new := append(tx.get(key), value...)
	tx.set(key, new)
	return new
}

func (tx *memTxn) incrBy(key []byte, value int64) int64 {
	var new int64
	buf := tx.get(key)
	if len(buf) > 0 {
		new = parseCounter(buf)
	}
	if value != 0 {
		new += value
		tx.set(key, packCounter(new))
	}
	return new
}

func (tx *memTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		tx.buffer[string(key)] = nil
	}
}

type kvItem struct {
	key   string
	ver   int
	value []byte
}

func (it *kvItem) Less(o btree.Item) bool {
	return it.key < o.(*kvItem).key
}

type memKV struct {
	sync.Mutex
	items *btree.BTree
	temp  *kvItem
}

func (c *memKV) name() string {
	return "memkv"
}

func (c *memKV) get(key string) *kvItem {
	c.temp.key = key
	it := c.items.Get(c.temp)
	if it != nil {
		return it.(*kvItem)
	}
	return nil
}

func (c *memKV) set(key string, value []byte) {
	c.temp.key = key
	if value == nil {
		c.items.Delete(c.temp)
		return
	}
	it := c.items.Get(c.temp)
	if it != nil {
		it.(*kvItem).ver++
		it.(*kvItem).value = value
	} else {
		c.items.ReplaceOrInsert(&kvItem{key: key, ver: 1, value: value})
	}
}

func (c *memKV) txn(f func(kvTxn) error) error {
	tx := &memTxn{
		store:    c,
		observed: make(map[string]int),
		buffer:   make(map[string][]byte),
	}
	if err := f(tx); err != nil {
		return err
	}

	if len(tx.buffer) == 0 {
		return nil
	}
	c.Lock()
	defer c.Unlock()
	for k, ver := range tx.observed {
		it := c.get(k)
		if it == nil && ver != 0 {
			return fmt.Errorf("write conflict: %s was version %d, now deleted", k, ver)
		} else if it != nil && it.ver > ver {
			return fmt.Errorf("write conflict: %s %d > %d", k, it.ver, ver)
		}
	}
	if _, ok := tx.buffer["setting"]; ok {
		d, _ := json.Marshal(tx.buffer)
		if err := ioutil.WriteFile(settingPath, d, 0644); err != nil {
			return err
		}
	}
	for k, value := range tx.buffer {
		c.set(k, value)
	}
	return nil
}

func (c *memKV) reset(prefix []byte) error {
	if prefix != nil {
		return fmt.Errorf("prefix must be nil")
	}
	c.Lock()
	c.items = btree.New(2)
	c.temp = &kvItem{}
	c.Unlock()
	return nil
}
