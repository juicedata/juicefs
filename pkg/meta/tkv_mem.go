// +build !tikv,!fdb

/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package meta

import (
	"encoding/binary"
	"fmt"
	"sync"
)

func init() {
	Register("memkv", newKVMeta)
}

func newTkvClient(driver, addr string) (tkvClient, error) {
	if driver != "memkv" {
		return nil, fmt.Errorf("invalid driver %s != expected %s", driver, "memkv")
	}
	return &memKV{
		items: make(map[string]*kvItem),
	}, nil
}

type memTxn struct {
	store    *memKV
	observed map[string]int
	buffer   map[string][]byte
}

func (tx *memTxn) get(key []byte) []byte {
	if v, ok := tx.buffer[string(key)]; ok {
		return v
	}
	tx.store.Lock()
	defer tx.store.Unlock()
	v, ok := tx.store.items[string(key)]
	if ok {
		tx.observed[string(key)] = v.ver
		return v.value
	}
	return nil
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
	for k, v := range tx.store.items {
		if k >= begin && (end == "" || k < end) && len(v.value) > 0 {
			tx.observed[string(k)] = v.ver
			ret[k] = v.value
		}
	}
	return ret
}

func (tx *memTxn) nextKey(key []byte) []byte {
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
	for k := range tx.scanValues(prefix) {
		keys = append(keys, []byte(k))
	}
	return keys
}

func (tx *memTxn) scanValues(prefix []byte) map[string][]byte {
	return tx.scanRange(prefix, tx.nextKey(prefix))
}

func (tx *memTxn) exist(prefix []byte) bool {
	return len(tx.scanValues(prefix)) > 0
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
	var old int64
	buf := tx.get(key)
	if len(buf) > 0 {
		if len(buf) != 8 {
			panic("invalid counter value")
		}
		old = int64(binary.BigEndian.Uint64(buf))
	}
	if value != 0 {
		buf = make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(old+value))
		tx.set(key, buf)
	}
	return old
}

func (tx *memTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		tx.buffer[string(key)] = nil
	}
}

type kvItem struct {
	ver   int
	value []byte
}

type memKV struct {
	sync.Mutex
	items map[string]*kvItem
}

func (c *memKV) name() string {
	return "memkv"
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
		it := c.items[k]
		if it.ver > ver {
			return fmt.Errorf("write conflict: %s %d > %d", k, it.ver, ver)
		}
	}
	for k, value := range tx.buffer {
		it := c.items[k]
		if it == nil {
			it = &kvItem{}
			c.items[k] = it
		}
		it.ver++
		it.value = value
	}
	return nil
}
