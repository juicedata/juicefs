//go:build fdb
// +build fdb

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
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/apple/foundationdb/bindings/go/src/fdb"
)

func init() {
	Register("fdb", newKVMeta)
	drivers["fdb"] = newFdbClient
}

type fdbTxn struct {
	fdb.Transaction
	c *fdbClient
}

type fdbClient struct {
	client fdb.Database
	nextid uint64
}

func newFdbClient(addr string) (tkvClient, error) {
	err := fdb.APIVersion(630)
	if err != nil {
		return nil, fmt.Errorf("set API version: %s", err)
	}
	u, err := url.Parse("fdb://" + addr)
	if err != nil {
		return nil, err
	}
	db, err := fdb.OpenDatabase(u.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %s", err)
	}
	// TODO: database options
	return withPrefix(&fdbClient{db, rand.Uint64()}, append([]byte(u.Query().Get("prefix")), 0xFD)), nil
}

func (c *fdbClient) name() string {
	return "fdb"
}

func (c *fdbClient) config(key string) interface{} {
	return nil
}

func (c *fdbClient) simpleTxn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	return c.txn(ctx, f, retry)
}

func (c *fdbClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) error {
	_, err := c.client.Transact(func(t fdb.Transaction) (interface{}, error) {
		e := f(&kvTxn{&fdbTxn{t, c}, retry})
		return nil, e
	})
	return err
}

func (c *fdbClient) scan(prefix []byte, handler func(key, value []byte) bool) error {
	begin := fdb.Key(prefix)
	end := fdb.Key(nextKey(prefix))
	limit := 102400
	var done bool
	for {
		if _, err := c.client.ReadTransact(func(t fdb.ReadTransaction) (interface{}, error) {
			// TODO:  t.Options().SetPriorityBatch()
			snapshot := t.Snapshot()
			iter := snapshot.GetRange(
				fdb.KeyRange{Begin: begin, End: end},
				fdb.RangeOptions{Limit: limit, Mode: fdb.StreamingModeWantAll},
			).Iterator()
			var r fdb.KeyValue
			var count int
			for iter.Advance() {
				r = iter.MustGet()
				if !handler(r.Key, r.Value) {
					break
				}
				count++
			}
			if count < limit {
				done = true
			} else {
				begin = append(r.Key, 0)
			}
			return nil, nil
		}); err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (c *fdbClient) reset(prefix []byte) error {
	_, err := c.client.Transact(func(t fdb.Transaction) (interface{}, error) {
		t.ClearRange(fdb.KeyRange{
			Begin: fdb.Key(prefix),
			End:   fdb.Key(nextKey(prefix)),
		})
		return nil, nil
	})
	return err
}

func (c *fdbClient) close() error {
	return nil
}

func (c *fdbClient) shouldRetry(err error) bool {
	return false
}

func (c *fdbClient) gc() {}

func (c *fdbClient) getId() uint64 {
	return atomic.AddUint64(&c.nextid, 1)
}

func (c *fdbClient) rewind(id uint64, factor int) uint64 {
	shift := uint64(1e6)
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

func (tx *fdbTxn) id() uint64 {
	ver, err := tx.GetReadVersion().Get()
	if err != nil {
		logger.Debugf("get read version: %s", err)
		return 0
	}
	// add logical id to avoid conflict between concurrent transactions
	return uint64(ver)*1e3 + tx.c.getId()%1e3
}

func (tx *fdbTxn) get(key []byte) []byte {
	return tx.Get(fdb.Key(key)).MustGet()
}

func (tx *fdbTxn) gets(keys ...[]byte) [][]byte {
	fut := make([]fdb.FutureByteSlice, len(keys))
	for i, key := range keys {
		fut[i] = tx.Get(fdb.Key(key))
	}
	ret := make([][]byte, len(keys))
	for i, f := range fut {
		ret[i] = f.MustGet()
	}
	return ret
}

func (tx *fdbTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	it := tx.GetRange(fdb.KeyRange{Begin: fdb.Key(begin), End: fdb.Key(end)},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll}).Iterator()
	for it.Advance() {
		kv := it.MustGet()
		if !handler(kv.Key, kv.Value) {
			break
		}
	}
}

func (tx *fdbTxn) exist(prefix []byte) bool {
	return tx.GetRange(
		fdb.KeyRange{Begin: fdb.Key(prefix), End: fdb.Key(nextKey(prefix))},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
	).Iterator().Advance()
}

func (tx *fdbTxn) set(key, value []byte) {
	tx.Set(fdb.Key(key), value)
}

func (tx *fdbTxn) append(key []byte, value []byte) {
	tx.AppendIfFits(fdb.Key(key), fdb.Key(value))
}

func (tx *fdbTxn) incrBy(key []byte, value int64) int64 {
	tx.Add(fdb.Key(key), packCounter(value))
	// TODO: don't return new value if not needed
	return parseCounter(tx.Get(fdb.Key(key)).MustGet())
}

func (tx *fdbTxn) delete(key []byte) {
	tx.Clear(fdb.Key(key))
}
