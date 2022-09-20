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
	"fmt"
	"net/url"

	"github.com/apple/foundationdb/bindings/go/src/fdb"
)

func init() {
	Register("fdb", newKVMeta)
	drivers["fdb"] = newFdbClient
}

type fdbTxn struct {
	fdb.Transaction
}

type fdbClient struct {
	client fdb.Database
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
	return withPrefix(&fdbClient{db}, append([]byte(u.Query().Get("prefix")), 0xFD)), nil
}

func (c *fdbClient) name() string {
	return "fdb"
}

func (c *fdbClient) txn(f func(kvTxn) error) error {
	_, err := c.client.Transact(func(t fdb.Transaction) (interface{}, error) {
		e := f(&fdbTxn{t})
		return nil, e
	})
	return err
}

func (c *fdbClient) scan(prefix []byte, handler func(key, value []byte)) error {
	_, err := c.client.ReadTransact(func(t fdb.ReadTransaction) (interface{}, error) {
		snapshot := t.Snapshot()
		iter := snapshot.GetRange(
			fdb.KeyRange{Begin: fdb.Key(prefix), End: fdb.Key(nextKey(prefix))},
			fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
		).Iterator()
		for iter.Advance() {
			r := iter.MustGet()
			handler(r.Key, r.Value)
		}
		return nil, nil
	})
	return err
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
	// c = &fdbClient{}
	return nil
}

func (c *fdbClient) shouldRetry(err error) bool {
	return false
}

func (tx *fdbTxn) get(key []byte) []byte {
	return tx.Get(fdb.Key(key)).MustGet()
}

func (tx *fdbTxn) gets(keys ...[]byte) [][]byte {
	ret := make([][]byte, len(keys))
	for i, key := range keys {
		val := tx.Get(fdb.Key(key)).MustGet()
		ret[i] = val
	}
	return ret
}

func (tx *fdbTxn) range0(begin, end []byte) *fdb.RangeIterator {
	return tx.GetRange(
		fdb.KeyRange{Begin: fdb.Key(begin), End: fdb.Key(end)},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
	).Iterator()
}

func (tx *fdbTxn) scanRange(begin, end []byte) map[string][]byte {
	ret := make(map[string][]byte)
	iter := tx.range0(begin, end)
	for iter.Advance() {
		r := iter.MustGet()
		ret[string(r.Key)] = r.Value
	}
	return ret
}

func (tx *fdbTxn) scanKeys(prefix []byte) [][]byte {
	ret := make([][]byte, 0)
	iter := tx.range0(prefix, nextKey(prefix))
	for iter.Advance() {
		ret = append(ret, iter.MustGet().Key)
	}
	return ret
}

func (tx *fdbTxn) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte {
	ret := make(map[string][]byte)
	iter := tx.range0(prefix, nextKey(prefix))
	for iter.Advance() {
		r := iter.MustGet()
		if filter == nil || filter(r.Key, r.Value) {
			ret[string(r.Key)] = r.Value
			if limit > 0 {
				if limit--; limit == 0 {
					break
				}
			}
		}
	}
	return ret
}

func (tx *fdbTxn) exist(prefix []byte) bool {
	iter := tx.range0(prefix, nextKey(prefix))
	return iter.Advance()
}

func (tx *fdbTxn) set(key, value []byte) {
	tx.Set(fdb.Key(key), value)
}

func (tx *fdbTxn) append(key []byte, value []byte) []byte {
	tx.AppendIfFits(fdb.Key(key), fdb.Key(value))
	return tx.Get(fdb.Key(key)).MustGet()
}

func (tx *fdbTxn) incrBy(key []byte, value int64) int64 {
	tx.Add(fdb.Key(key), packCounter(value))
	return parseCounter(tx.Get(fdb.Key(key)).MustGet())
}

func (tx *fdbTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		tx.Clear(fdb.Key(key))
	}
}
