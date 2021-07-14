// +build fdb

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
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/apple/foundationdb/bindings/go/src/fdb"
)

func init() {
	Register("fdb", newKVMeta)
}

type fdbTxn struct {
	t fdb.Transaction
}

func (tx *fdbTxn) get(key []byte) []byte {
	return tx.t.Get(fdb.Key(key)).MustGet()
}

func (tx *fdbTxn) gets(keys ...[]byte) [][]byte {
	var result = make([][]byte, len(keys))
	var fs = make([]fdb.FutureByteSlice, len(keys))
	for i, k := range keys {
		fs[i] = tx.t.Get(fdb.Key(k))
	}
	for i := range keys {
		result[i] = fs[i].MustGet()
	}
	return result
}

func (tx *fdbTxn) scanRange(begin, end []byte) map[string][]byte {
	it := tx.t.GetRange(
		fdb.KeyRange{Begin: fdb.Key(begin), End: fdb.Key(end)},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
	).Iterator()
	var ret = make(map[string][]byte)
	for it.Advance() {
		v := it.MustGet()
		ret[string(v.Key)] = v.Value
	}
	return ret
}

func (tx *fdbTxn) scanKeys(prefix []byte) [][]byte {
	it := tx.t.GetRange(
		fdb.KeyRange{Begin: fdb.Key(prefix), End: fdb.Key(nextKey(prefix))},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
	).Iterator()
	var keys [][]byte
	for it.Advance() {
		v := it.MustGet()
		keys = append(keys, v.Key)
	}
	return keys
}

func (tx *fdbTxn) scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte {
	it := tx.t.GetRange(
		fdb.KeyRange{Begin: fdb.Key(prefix), End: fdb.Key(nextKey(prefix))},
		fdb.RangeOptions{Mode: fdb.StreamingModeWantAll},
	).Iterator()
	var ret = make(map[string][]byte)
	for it.Advance() {
		v := it.MustGet()
		if filter == nil || filter(v.Key, v.Value) {
			ret[string(v.Key)] = v.Value
		}
	}
	return ret
}

func (tx *fdbTxn) exist(prefix []byte) bool {
	key := tx.t.GetKey(fdb.FirstGreaterOrEqual(fdb.Key(prefix))).MustGet()
	return key != nil && bytes.HasPrefix(key, prefix)
}

func (tx *fdbTxn) append(key []byte, value []byte) {
	tx.t.AppendIfFits(fdb.Key(key), value)
}

func (tx *fdbTxn) set(key, value []byte) {
	tx.t.Set(fdb.Key(key), value)
}

func (tx *fdbTxn) incrBy(key []byte, value int64) int64 {
	var old int64
	buf := tx.get(key)
	if len(buf) > 0 {
		if len(buf) != 8 {
			panic("invalid counter value")
		}
		old = int64(binary.LittleEndian.Uint64(buf))
	}
	if value != 0 {
		buf = make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(old+value))
		tx.set(key, buf)
	}
	return old
}

func (tx *fdbTxn) dels(keys ...[]byte) {
	for _, k := range keys {
		tx.t.Clear(fdb.Key(k))
	}
}

type fdbClient struct {
	db fdb.Database
}

func (c *fdbClient) name() string {
	return "fdb"
}

func (c *fdbClient) txn(f func(kvTxn) error) error {
	tx, err := c.db.CreateTransaction()
	if err != nil {
		return err
	}

	err = f(&fdbTxn{tx})
	if err != nil {
		tx.Reset()
		return err
	}
	return tx.Commit().Get()
}

func newTkvClient(driver, clusterFile string) (tkvClient, error) {
	if driver != "fdb" {
		return nil, fmt.Errorf("invalid driver %s != expected %s", driver, "fdb")
	}
	db, err := fdb.OpenDatabase(clusterFile)
	return &fdbClient{db}, err
}
