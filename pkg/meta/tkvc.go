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
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	// "github.com/apple/foundationdb/bindings/go/src/fdb"

	tikverr "github.com/tikv/client-go/v2/error"
	"github.com/tikv/client-go/v2/tikv"
)

var endian = binary.LittleEndian

type kvTxn interface {
	get(key []byte) []byte
	gets(keys ...[]byte) [][]byte
	scanRange(begin, end []byte) [][]byte
	scanKeys(prefix []byte) [][]byte
	scanValues(prefix []byte) map[string][]byte
	exist(prefix []byte) bool
	sets(args ...[]byte)
	append(key []byte, value []byte) []byte
	incrBy(key []byte, value int64) int64
	dels(keys ...[]byte)
}

type tkvClient interface {
	name() string
	get(key []byte) ([]byte, error)
	gets(keys ...[]byte) ([][]byte, error) // FIXME: not the same as kvTxn
	scanKeys(prefix []byte) ([][]byte, error)
	scanValues(prefix []byte) (map[string][]byte, error)
	sets(args ...[]byte) error
	incrBy(key []byte, value int64) (int64, error)
	dels(keys ...[]byte) (int, error)
	txn(f func(kvTxn) error) error
}

func newTkvClient(driver, addr string) (tkvClient, error) {
	if driver == "tikv" {
		client, err := tikv.NewTxnClient([]string{addr})
		return &tikvClient{client}, err
	} else if driver == "fdb" {
		// client, err := fdb.OpenDatabase([]string{addr})
		// return &fdbClient{client}, err
		return nil, errors.New("not supported")
	} else {
		return nil, errors.New("not supported")
	}

}

type tikvTxn struct {
	*tikv.KVTxn
}

func (tx *tikvTxn) get(key []byte) []byte {
	value, err := tx.Get(context.TODO(), key)
	if tikverr.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
		panic(err)
	}
	return value
}

func (tx *tikvTxn) gets(keys ...[]byte) [][]byte {
	ret, err := tx.BatchGet(context.TODO(), keys)
	if err != nil {
		panic(err)
	}
	values := make([][]byte, 0, len(ret))
	for _, key := range keys {
		if v, ok := ret[string(key)]; ok {
			values = append(values, v)
		}
	}
	return values
}

func (tx *tikvTxn) scanRange(begin, end []byte) [][]byte {
	it, err := tx.Iter(begin, end)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	var ret [][]byte
	for it.Valid() {
		ret = append(ret, it.Key())
		it.Next()
	}
	return ret
}

func (tx *tikvTxn) scanKeys(prefix []byte) [][]byte {
	next := make([]byte, len(prefix))
	copy(next, prefix)
	next[len(next)-1]++
	it, err := tx.Iter(prefix, next)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	var ret [][]byte
	for it.Valid() {
		ret = append(ret, it.Key())
		it.Next()
	}
	return ret
}

func (tx *tikvTxn) scanValues(prefix []byte) map[string][]byte {
	next := make([]byte, len(prefix))
	copy(next, prefix)
	next[len(next)-1]++
	it, err := tx.Iter(prefix, next)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	ret := make(map[string][]byte)
	for it.Valid() {
		ret[string(it.Key())] = it.Value()
		it.Next()
	}
	return ret
}

func (tx *tikvTxn) exist(prefix []byte) bool {
	next := make([]byte, len(prefix))
	copy(next, prefix)
	next[len(next)-1]++
	it, err := tx.Iter(prefix, next)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	return it.Valid()
}

func (tx *tikvTxn) sets(args ...[]byte) {
	for i := 0; i < len(args); i += 2 {
		key, val := args[i], args[i+1]
		if err := tx.Set(key, val); err != nil {
			panic(err)
		}
	}
}

func (tx *tikvTxn) append(key []byte, value []byte) []byte {
	old, err := tx.Get(context.TODO(), key)
	if err != nil && !tikverr.IsErrNotFound(err) {
		panic(err)
	}
	new := append(old, value...)
	if err = tx.Set(key, new); err != nil {
		panic(err)
	}
	return new
}

func (tx *tikvTxn) incrBy(key []byte, value int64) int64 {
	var old int64
	buf, err := tx.Get(context.TODO(), key)
	if tikverr.IsErrNotFound(err) {
	} else if err != nil {
		panic(err)
	} else {
		if len(buf) != 8 {
			panic("invalid counter value")
		}
		old = int64(endian.Uint64(buf)) // FIXME: uint <-> int
	}
	if value == 0 {
		return old
	}
	new := old + value
	if new < 0 {
		new = 0
	}
	buf = make([]byte, 8)
	endian.PutUint64(buf, uint64(new))
	if err = tx.Set(key, buf); err != nil {
		panic(err)
	}
	return old
}

func (tx *tikvTxn) dels(keys ...[]byte) {
	for _, key := range keys {
		if err := tx.Delete(key); err != nil {
			panic(err)
		}
	}
}

type tikvClient struct {
	client *tikv.KVStore
}

func (c *tikvClient) name() string {
	return "TiKV"
}

// TODO: add txn retry
func (c *tikvClient) get(key []byte) ([]byte, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return nil, err
	}
	value, err := tx.Get(context.TODO(), key)
	if tikverr.IsErrNotFound(err) {
		return nil, nil
	}
	return value, err
}

func (c *tikvClient) gets(keys ...[]byte) ([][]byte, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return nil, err
	}
	ret, err := tx.BatchGet(context.TODO(), keys)
	if err != nil {
		return nil, err
	}
	values := make([][]byte, 0, len(keys))
	for _, key := range keys {
		if v, ok := ret[string(key)]; ok {
			values = append(values, v)
		} else {
			values = append(values, nil)
		}
	}
	return values, nil
}

func (c *tikvClient) scanKeys(prefix []byte) ([][]byte, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return nil, err
	}
	next := make([]byte, len(prefix))
	copy(next, prefix)
	next[len(next)-1]++
	it, err := tx.Iter(prefix, next)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var ret [][]byte
	for it.Valid() {
		ret = append(ret, it.Key())
		it.Next()
	}
	return ret, nil
}

func (c *tikvClient) scanValues(prefix []byte) (map[string][]byte, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return nil, err
	}
	next := make([]byte, len(prefix))
	copy(next, prefix)
	next[len(next)-1]++
	it, err := tx.Iter(prefix, next)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	ret := make(map[string][]byte)
	for it.Valid() {
		ret[string(it.Key())] = it.Value()
		it.Next()
	}
	return ret, nil
}

func (c *tikvClient) sets(args ...[]byte) error {
	tx, err := c.client.Begin()
	if err != nil {
		return err
	}
	for i := 0; i < len(args); i += 2 {
		key, val := args[i], args[i+1]
		if err := tx.Set(key, val); err != nil {
			return err
		}
	}
	return tx.Commit(context.Background())
}

func (c *tikvClient) incrBy(key []byte, value int64) (int64, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return 0, err
	}
	var old int64
	buf, err := tx.Get(context.TODO(), key)
	if tikverr.IsErrNotFound(err) {
	} else if err != nil {
		return 0, err
	} else {
		if len(buf) != 8 {
			return 0, fmt.Errorf("invalid counter value: %v", buf)
		}
		old = int64(endian.Uint64(buf)) // FIXME: uint <-> int
	}
	if value == 0 {
		return old, nil
	}
	new := old + value
	if new < 0 {
		new = 0
	}
	buf = make([]byte, 8)
	endian.PutUint64(buf, uint64(new))
	if err = tx.Set(key, buf); err != nil {
		return 0, err
	}
	return old, tx.Commit(context.Background())
}

func (c *tikvClient) dels(keys ...[]byte) (int, error) {
	tx, err := c.client.Begin()
	if err != nil {
		return 0, err
	}
	var count int
	for _, key := range keys {
		_, err := tx.Get(context.TODO(), key)
		if tikverr.IsErrNotFound(err) {
			continue
		} else if err != nil {
			return 0, err
		}
		if err = tx.Delete(key); err != nil {
			return 0, err
		}
		count++
	}
	return count, tx.Commit(context.Background())
}

func (c *tikvClient) txn(f func(kvTxn) error) error {
	tx, err := c.client.Begin()
	if err != nil {
		return err
	}
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
	if err = f(&tikvTxn{tx}); err != nil {
		return err
	}
	if !tx.IsReadOnly() {
		err = tx.Commit(context.Background())
	}
	return err
}

/*
func (d Database) Transact(f func(Transaction) (interface{}, error)) (interface{}, error) {
	tr, e := d.CreateTransaction()
	// Any error here is non-retryable
	if e != nil {
		return nil, e
	}

	wrapped := func() (ret interface{}, e error) {
		defer panicToError(&e)

		ret, e = f(tr)

		if e == nil {
			e = tr.Commit().Get()
		}

		return
	}

	return retryable(wrapped, tr.OnError)
}
*/

/*
type fdbClient struct {
	client fdb.Database
}

func (c *fdbClient) name() string {
	return "FoundationDB"
}

func (c *fdbClient) get(key []byte) ([]byte, error) {
	return nil, nil
}

func (c *fdbClient) puts(args ...[]byte) error {
	return nil
}

func (c *fdbClient) incr() {

}

func (c *fdbClient) scan() {

}

func (c *fdbClient) txn(f func() error) error {
	tx, err := c.client.Begin()
	if err != nil {
		return err
	}
	err = f(tx)
	return tx.Commit(context.Background())
}
*/
