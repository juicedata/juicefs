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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	if d, err := os.ReadFile(settingPath); err == nil {
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

func (tx *memTxn) get(key []byte) ([]byte, error) {
	k := string(key)
	if v, ok := tx.buffer[k]; ok {
		return v, nil
	}
	tx.store.Lock()
	defer tx.store.Unlock()
	it := tx.store.get(k)
	if it != nil {
		tx.observed[k] = it.ver
		return it.value, nil
	} else {
		tx.observed[k] = 0
		return nil, nil
	}
}

func (tx *memTxn) gets(keys ...[]byte) ([][]byte, error) {
	values := make([][]byte, len(keys))
	var err error
	for i, key := range keys {
		values[i], err = tx.get(key)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (tx *memTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) error {
	tx.store.Lock()
	defer tx.store.Unlock()
	tx.store.items.AscendGreaterOrEqual(&kvItem{key: string(begin)}, func(i btree.Item) bool {
		it := i.(*kvItem)
		key := []byte(it.key)
		if bytes.Compare(key, end) >= 0 {
			return false
		}
		tx.observed[it.key] = it.ver
		return handler(key, it.value)
	})
	return nil
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

func (tx *memTxn) exist(prefix []byte) (bool, error) {
	var ret bool
	tx.store.Lock()
	defer tx.store.Unlock()
	tx.store.items.AscendGreaterOrEqual(&kvItem{key: string(prefix)}, func(i btree.Item) bool {
		it := i.(*kvItem)
		if strings.HasPrefix(it.key, string(prefix)) {
			tx.observed[it.key] = it.ver
			ret = true
		}
		return false
	})
	return ret, nil
}

func (tx *memTxn) set(key, value []byte) error {
	tx.buffer[string(key)] = value
	return nil
}

func (tx *memTxn) append(key []byte, value []byte) ([]byte, error) {
	old, err := tx.get(key)
	if err != nil {
		return nil, err
	}
	new := append(old, value...)
	err = tx.set(key, new)
	if err != nil {
		return nil, err
	}
	return new, nil
}

func (tx *memTxn) incrBy(key []byte, value int64) (int64, error) {
	buf, err := tx.get(key)
	if err != nil {
		return -1, err
	}
	new := parseCounter(buf)
	if value != 0 {
		new += value
		tx.set(key, packCounter(new))
	}
	return new, nil
}

func (tx *memTxn) delete(key []byte) error {
	tx.buffer[string(key)] = nil
	return nil
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

func (c *memKV) shouldRetry(err error) bool {
	return strings.Contains(err.Error(), "write conflict")
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

func (c *memKV) txn(f func(*kvTxn) error, retry int) error {
	tx := &memTxn{
		store:    c,
		observed: make(map[string]int),
		buffer:   make(map[string][]byte),
	}
	if err := f(&kvTxn{tx, retry}); err != nil {
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
		if err := os.WriteFile(settingPath, d, 0644); err != nil {
			return err
		}
	}
	for k, value := range tx.buffer {
		c.set(k, value)
	}
	return nil
}

func (c *memKV) scan(prefix []byte, handler func(key []byte, value []byte)) error {
	c.Lock()
	snap := c.items.Clone()
	c.Unlock()
	begin := string(prefix)
	end := string(nextKey(prefix))
	snap.AscendGreaterOrEqual(&kvItem{key: begin}, func(i btree.Item) bool {
		it := i.(*kvItem)
		if end != "" && it.key >= end {
			return false
		}
		handler([]byte(it.key), it.value)
		return true
	})
	return nil
}

func (c *memKV) reset(prefix []byte) error {
	if len(prefix) == 0 {
		c.Lock()
		c.items = btree.New(2)
		c.temp = &kvItem{}
		c.Unlock()
		return nil
	}
	return c.txn(func(kt *kvTxn) error {
		return c.scan(prefix, func(key, value []byte) {
			kt.delete(key)
		})
	}, 0)
}

func (c *memKV) close() error {
	return nil
}

func (c *memKV) gc() {}
