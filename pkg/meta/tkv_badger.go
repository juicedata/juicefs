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

func (tx *badgerTxn) get(key []byte) ([]byte, error) {
	item, err := tx.t.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := item.ValueCopy(nil)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (tx *badgerTxn) gets(keys ...[]byte) ([][]byte, error) {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		var err error
		values[i], err = tx.get(key)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (tx *badgerTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) error {
	var prefix bool
	var options = badger.IteratorOptions{
		PrefetchValues: !keysOnly,
		PrefetchSize:   1024,
	}
	if bytes.Equal(nextKey(begin), end) {
		prefix = true
		options.Prefix = begin
	}
	it := tx.t.NewIterator(options)
	if prefix {
		it.Rewind()
	} else {
		it.Seek(begin)
	}
	defer it.Close()
	for ; it.Valid(); it.Next() {
		item := it.Item()
		if !prefix && bytes.Compare(item.Key(), end) >= 0 {
			break
		}
		value, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if !handler(item.KeyCopy(nil), value) {
			break
		}
	}
	return nil
}

func (tx *badgerTxn) exist(prefix []byte) (bool, error) {
	it := tx.t.NewIterator(badger.IteratorOptions{
		Prefix:       prefix,
		PrefetchSize: 1,
	})
	defer it.Close()
	it.Rewind()
	return it.Valid(), nil
}

func (tx *badgerTxn) set(key, value []byte) error {
	err := tx.t.Set(key, value)
	if err == badger.ErrTxnTooBig {
		logger.Warn("Current transaction is too big, commit it")
		if er := tx.t.Commit(); er != nil {
			return er
		}
		tx.t = tx.c.NewTransaction(true)
		err = tx.t.Set(key, value)
	}
	return err
}

func (tx *badgerTxn) append(key []byte, value []byte) ([]byte, error) {
	old, err := tx.get(key)
	if err != nil {
		return nil, err
	}
	list := append(old, value...)
	err = tx.set(key, list)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (tx *badgerTxn) incrBy(key []byte, value int64) (int64, error) {
	buf, err := tx.get(key)
	if err != nil {
		return -1, err
	}
	newCounter := parseCounter(buf)
	if value != 0 {
		newCounter += value
		tx.set(key, packCounter(newCounter))
	}
	return newCounter, nil
}

func (tx *badgerTxn) delete(key []byte) error {
	return tx.t.Delete(key)
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

func (c *badgerClient) txn(f func(*kvTxn) error, retry int) (err error) {
	t := c.client.NewTransaction(true)
	defer t.Discard()
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
	tx := &badgerTxn{t, c.client}
	err = f(&kvTxn{tx, retry})
	if err != nil {
		return err
	}
	// tx could be committed
	return tx.t.Commit()
}

func (c *badgerClient) scan(prefix []byte, handler func(key []byte, value []byte)) error {
	tx := c.client.NewTransaction(false)
	defer tx.Discard()
	it := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		PrefetchValues: true,
		PrefetchSize:   10240,
	})
	defer it.Close()
	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		value, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		handler(it.Item().Key(), value)
	}
	return nil
}

func (c *badgerClient) reset(prefix []byte) error {
	if prefix == nil {
		return c.client.DropAll()
	} else {
		return c.client.DropPrefix(prefix)
	}
}

func (c *badgerClient) close() error {
	c.ticker.Stop()
	return c.client.Close()
}

func (c *badgerClient) gc() {}

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
