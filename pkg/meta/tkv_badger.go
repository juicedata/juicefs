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
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/juicedata/juicefs/pkg/utils"
)

type badgerTxn struct {
	t *badger.Txn
	c *badgerClient
}

func (tx *badgerTxn) id() uint64 {
	// add logical id to avoid conflict between concurrent transactions
	return tx.t.ReadTs()*1e2 + tx.c.getId()%1e2
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

func (tx *badgerTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	var prefix bool
	options := badger.IteratorOptions{}
	if keysOnly {
		options.PrefetchValues = false
		options.PrefetchSize = 0
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
		var value []byte
		if !keysOnly {
			var err error
			value, err = item.ValueCopy(nil)
			if err != nil {
				panic(err)
			}
		}
		if !handler(item.KeyCopy(nil), value) {
			break
		}
	}
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
	if err := tx.t.Set(key, value); err != nil {
		panic(err)
	}
}

func (tx *badgerTxn) append(key []byte, value []byte) {
	list := append(tx.get(key), value...)
	tx.set(key, list)
}

func (tx *badgerTxn) incrBy(key []byte, value int64) int64 {
	buf := tx.get(key)
	newCounter := parseCounter(buf)
	if value != 0 {
		newCounter += value
		tx.set(key, packCounter(newCounter))
	}
	return newCounter
}

func (tx *badgerTxn) delete(key []byte) {
	if err := tx.t.Delete(key); err != nil {
		panic(err)
	}
}

type badgerClient struct {
	client *badger.DB
	ticker *time.Ticker
	done   chan struct{}
	nextid uint64
}

func (c *badgerClient) name() string {
	return "badger"
}

func (c *badgerClient) getId() uint64 {
	return atomic.AddUint64(&c.nextid, 1)
}

func (c *badgerClient) rewind(id uint64, factor int) uint64 {
	shift := uint64(1e5)
	if s := os.Getenv("JFS_TKV_REWIND"); s != "" {
		if parsed, err := strconv.ParseUint(s, 10, 64); err == nil && parsed > 0 {
			shift = parsed
		}
	}
	if factor > 1 {
		shift *= uint64(factor)
	}
	if id > shift {
		return id - shift
	}
	return 1
}

func (c *badgerClient) shouldRetry(err error) bool {
	return err == badger.ErrConflict
}

func (c *badgerClient) config(key string) interface{} {
	return nil
}

func (c *badgerClient) simpleTxn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	return c.txn(ctx, f, retry)
}

func (c *badgerClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	tx := &badgerTxn{c.client.NewTransaction(true), c}
	defer func() { tx.t.Discard() }()
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
	// tx.t may differ from the original
	return tx.t.Commit()
}

func (c *badgerClient) scan(prefix []byte, handler func(key []byte, value []byte) bool) error {
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
		if !handler(item.KeyCopy(nil), value) {
			break
		}
	}
	return nil
}

func (c *badgerClient) reset(prefix []byte) error {
	if prefix == nil {
		return c.client.DropAll()
	}
	return c.client.DropPrefix(prefix)
}

func (c *badgerClient) close() error {
	close(c.done)
	c.ticker.Stop()
	return c.client.Close()
}

func (c *badgerClient) gc() {}

func newBadgerClient(addr string) (tkvClient, error) {
	opt := badger.DefaultOptions(addr)
	opt.Logger = utils.GetLogger("badger")
	opt.MetricsEnabled = false
	client, err := badger.Open(opt)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Hour)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				for client.RunValueLogGC(0.7) == nil {
				}
			case <-done:
				return
			}
		}
	}()
	return &badgerClient{client, ticker, done, 0}, nil
}

func init() {
	Register("badger", newKVMeta)
	drivers["badger"] = newBadgerClient
}
