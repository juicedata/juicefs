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
	badger "github.com/dgraph-io/badger/v3"
	"sync"
)

func init() {
	Register("badger", newKVMeta)
	drivers["badger"] = newBadgerClient
}

func newBadgerClient(addr string) (tkvClient, error) {
	client, err := badger.Open(badger.DefaultOptions(addr))
	if err != nil {
		return nil, err
	}
	return &badgerClient{client, new(sync.RWMutex)}, err
}

type badgerTxn struct {
	t *badger.Txn
}

func (tx *badgerTxn) scan(prefix []byte, handler func(key []byte, value []byte)) {
	it := tx.t.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()
	for it.Seek(prefix); it.Valid(); it.Next() {
		item := it.Item()
		value, _ := item.ValueCopy(nil)
		handler(it.Item().Key(), value)
	}
}

func (tx *badgerTxn) get(key []byte) []byte {
	item, err := tx.t.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil
	}
	var value []byte
	err = item.Value(func(val []byte) error {
		value = append([]byte{}, val...)
		return nil
	})
	if err != nil {
		panic(err)
	}
	return value
}

func (tx *badgerTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	ret := make(map[string][]byte)
	for _, key := range keys {
		item, err := tx.t.Get(key)
		if err == badger.ErrKeyNotFound {
			ret[string(key)] = []byte{}
		}
		if err != nil {
			panic(err)
		}
		err = item.Value(func(val []byte) error {
			ret[string(key)] = append([]byte{}, val...)
			return nil
		})
		if err != nil {
			panic(err)
		}
	}

	for i, key := range keys {
		values[i] = ret[string(key)]
	}
	return values
}

func (tx *badgerTxn) Iter(startKey []byte, upperBound []byte) (*badger.Iterator, error) {
	opts := badger.DefaultIteratorOptions
	it := tx.t.NewIterator(opts)
	it.Seek(startKey)
	return it, nil
}

func (tx *badgerTxn) scanRange0(begin, end []byte, filter func(k, v []byte) bool) map[string][]byte {
	opts := badger.DefaultIteratorOptions
	it := tx.t.NewIterator(opts)
	defer it.Close()
	var ret = make(map[string][]byte)
	for it.Seek(begin); it.Valid(); it.Next() {
		item := it.Item()
		if bytes.Compare(item.Key(), end) >= 0 {
			break
		}
		key := item.Key()
		var value []byte
		err := item.Value(func(val []byte) error {
			value = append([]byte{}, val...)
			return nil
		})
		if err != nil {
			panic(err)
		}
		if filter == nil || filter(key, value) {
			ret[string(key)] = value
		}
	}
	return ret
}

func (tx *badgerTxn) scanRange(begin, end []byte) map[string][]byte {
	return tx.scanRange0(begin, end, nil)
}

func (tx *badgerTxn) nextKey(key []byte) []byte {
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

func (tx *badgerTxn) scanKeys(prefix []byte) [][]byte {
	endKey := tx.nextKey(prefix)
	opts := badger.DefaultIteratorOptions
	it := tx.t.NewIterator(opts)

	defer it.Close()
	var ret [][]byte
	for it.Seek(prefix); it.Valid(); it.Next() {
		item := it.Item()
		if bytes.Compare(item.Key(), endKey) >= 0 {
			break
		}
		ret = append(ret, item.Key())
	}
	return ret
}

func (tx *badgerTxn) scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte {
	return tx.scanRange0(prefix, tx.nextKey(prefix), filter)
}

func (tx *badgerTxn) exist(prefix []byte) bool {
	nextKey := tx.nextKey(prefix)
	it, err := tx.Iter(prefix, nextKey)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	if it.Valid() {
		item := it.Item()
		if bytes.Compare(item.Key(), nextKey) >= 0 {
			return false
		}
	}
	return it.Valid()
}

func (tx *badgerTxn) set(key, value []byte) {
	err := tx.t.Set(key, value)
	if err == badger.ErrTxnTooBig {
		logger.Infof("Current txn too big:%v.\n", tx)
		if er := tx.t.Commit(); er != nil {
			panic(er)
		}
		//tx.t = tx.c.client.NewTransaction(true)
		if er := tx.t.Set(key, value); er != nil {
			panic(err)
		}

	}
	if err != nil && err != badger.ErrTxnTooBig {
		panic(err)
	}
}

func (tx *badgerTxn) append(key []byte, value []byte) []byte {
	list := append(tx.get(key), value...)
	tx.set(key, list)
	return list
}

func (tx *badgerTxn) incrBy(key []byte, value int64) int64 {
	var newCounter int64
	buf := tx.get(key)
	if len(buf) > 0 {
		newCounter = parseCounter(buf)
	}
	if value != 0 {
		newCounter += value
		tx.set(key, packCounter(newCounter))
	}
	return newCounter
}

func (tx *badgerTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		if err := tx.t.Delete(key); err != nil {
			panic(err)
		}
	}
}

type badgerClient struct {
	client *badger.DB
	*sync.RWMutex
}

func (c *badgerClient) name() string {
	return "badger"
}

func (c *badgerClient) txn(f func(kvTxn) error) error {
	c.Lock()
	defer c.Unlock()
	tx := c.client.NewTransaction(true)
	defer tx.Discard()
	err := f(&badgerTxn{tx})
	defer func(e *error) {
		if r := recover(); r != nil {
			fe, ok := r.(error)
			if ok {
				*e = fe
			} else {
				panic(r)
			}
		}
	}(&err)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (c *badgerClient) reset(prefix []byte) error {
	tx := c.client.NewTransaction(true)
	defer tx.Discard()
	it := tx.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()
	for it.Rewind(); it.Valid(); it.Next() {
		if err := tx.Delete(it.Item().Key()); err != nil {
			panic(err)
		}
	}
	return nil
}
