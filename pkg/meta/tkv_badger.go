//go:build !nobadger
// +build !nobadger

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
	"time"

	badger "github.com/dgraph-io/badger/v3"
	"github.com/juicedata/juicefs/pkg/utils"
)

type badgerTxn struct {
	t *badger.Txn
	c *badger.DB
}

func (tx *badgerTxn) scan(prefix []byte, handler func(key []byte, value []byte)) {
	it := tx.t.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		PrefetchValues: true,
		PrefetchSize:   1024,
	})
	defer it.Close()
	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		value, err := item.ValueCopy(nil)
		if err != nil {
			panic(err)
		}
		handler(it.Item().Key(), value)
	}
}

func (tx *badgerTxn) get(key []byte) []byte {
	item, err := tx.t.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil
	}
	if err != nil {
		panic(err)
	}
	value, err := item.ValueCopy(nil)
	if err != nil {
		panic(err)
	}
	return value
}

func (tx *badgerTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *badgerTxn) scanRange(begin, end []byte) map[string][]byte {
	it := tx.t.NewIterator(badger.IteratorOptions{
		PrefetchValues: true,
		PrefetchSize:   1024,
	})
	defer it.Close()
	var ret = make(map[string][]byte)
	for it.Seek(begin); it.Valid(); it.Next() {
		item := it.Item()
		key := item.Key()
		if bytes.Compare(key, end) >= 0 {
			break
		}
		var value []byte
		value, err := item.ValueCopy(nil)
		if err != nil {
			panic(err)
		}
		ret[string(key)] = value
	}
	return ret
}

func (tx *badgerTxn) scanKeys(prefix []byte) [][]byte {
	it := tx.t.NewIterator(badger.IteratorOptions{
		PrefetchValues: false,
		PrefetchSize:   1024,
		Prefix:         prefix,
	})
	defer it.Close()
	var ret [][]byte
	for it.Rewind(); it.Valid(); it.Next() {
		ret = append(ret, it.Item().KeyCopy(nil))
	}
	return ret
}

func (tx *badgerTxn) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte {
	if limit == 0 {
		return nil
	}

	it := tx.t.NewIterator(badger.IteratorOptions{
		PrefetchValues: true,
		PrefetchSize:   1024,
		Prefix:         prefix,
	})
	defer it.Close()
	var ret = make(map[string][]byte)
	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		value, err := item.ValueCopy(nil)
		if err != nil {
			panic(err)
		}
		if filter == nil || filter(item.Key(), value) {
			ret[string(item.Key())] = value
			if limit > 0 {
				if limit--; limit == 0 {
					break
				}
			}
		}
	}
	return ret
}

func (tx *badgerTxn) exist(prefix []byte) bool {
	it := tx.t.NewIterator(badger.IteratorOptions{
		Prefix:       prefix,
		PrefetchSize: 1,
	})
	defer it.Close()
	it.Rewind()
	return it.Valid()
}

func (tx *badgerTxn) set(key, value []byte) {
	err := tx.t.Set(key, value)
	if err == badger.ErrTxnTooBig {
		logger.Warn("Current transaction is too big, commit it")
		if er := tx.t.Commit(); er != nil {
			panic(er)
		}
		tx.t = tx.c.NewTransaction(true)
		err = tx.t.Set(key, value)
	}
	if err != nil {
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
	ticker *time.Ticker
}

func (c *badgerClient) name() string {
	return "badger"
}

func (c *badgerClient) shouldRetry(err error) bool {
	return err == badger.ErrConflict
}

func (c *badgerClient) txn(f func(kvTxn) error) (err error) {
	tx := c.client.NewTransaction(true)
	defer tx.Discard()
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
	err = f(&badgerTxn{tx, c.client})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (c *badgerClient) reset(prefix []byte) error {
	for {
		tx := c.client.NewTransaction(true)
		defer tx.Discard()
		it := tx.NewIterator(badger.IteratorOptions{
			Prefix:       prefix,
			PrefetchSize: 1024,
		})
		it.Rewind()
		if !it.Valid() {
			it.Close()
			return nil
		}
		for ; it.Valid(); it.Next() {
			if err := tx.Delete(it.Item().Key()); err == badger.ErrTxnTooBig {
				break
			} else if err != nil {
				it.Close()
				return err
			}
		}
		it.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}

func (c *badgerClient) close() error {
	c.ticker.Stop()
	return c.client.Close()
}

func newBadgerClient(addr string) (tkvClient, error) {
	opt := badger.DefaultOptions(addr)
	opt.Logger = utils.GetLogger("badger")
	client, err := badger.Open(opt)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Hour)
	go func() {
		for range ticker.C {
			for client.RunValueLogGC(0.7) == nil {
			}
		}
	}()
	return &badgerClient{client, ticker}, err
}

func init() {
	Register("badger", newKVMeta)
	drivers["badger"] = newBadgerClient
}
