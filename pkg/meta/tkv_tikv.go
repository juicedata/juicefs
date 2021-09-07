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
	"fmt"
	"strings"

	tikverr "github.com/tikv/client-go/v2/error"
	"github.com/tikv/client-go/v2/tikv"
)

func init() {
	Register("tikv", newKVMeta)
}

func newTkvClient(driver, addr string) (tkvClient, error) {
	if driver == "memkv" {
		return newMockClient()
	}
	if driver != "tikv" {
		return nil, fmt.Errorf("invalid driver %s != expected %s", driver, "tikv")
	}
	pds := strings.Split(addr, ",")
	client, err := tikv.NewTxnClient(pds)
	return &tikvClient{client}, err
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
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = ret[string(key)]
	}
	return values
}

func (tx *tikvTxn) scanRange0(begin, end []byte, filter func(k, v []byte) bool) map[string][]byte {
	it, err := tx.Iter(begin, end)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	var ret = make(map[string][]byte)
	for it.Valid() {
		key := it.Key()
		value := it.Value()
		if filter == nil || filter(key, value) {
			ret[string(key)] = value
		}
		if err = it.Next(); err != nil {
			panic(err)
		}
	}
	return ret
}

func (tx *tikvTxn) scanRange(begin, end []byte) map[string][]byte {
	return tx.scanRange0(begin, end, nil)
}

func (tx *tikvTxn) nextKey(key []byte) []byte {
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

func (tx *tikvTxn) scanKeys(prefix []byte) [][]byte {
	it, err := tx.Iter(prefix, tx.nextKey(prefix))
	if err != nil {
		panic(err)
	}
	defer it.Close()
	var ret [][]byte
	for it.Valid() {
		ret = append(ret, it.Key())
		if err = it.Next(); err != nil {
			panic(err)
		}
	}
	return ret
}

func (tx *tikvTxn) scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte {
	return tx.scanRange0(prefix, tx.nextKey(prefix), filter)
}

func (tx *tikvTxn) exist(prefix []byte) bool {
	it, err := tx.Iter(prefix, tx.nextKey(prefix))
	if err != nil {
		panic(err)
	}
	defer it.Close()
	return it.Valid()
}

func (tx *tikvTxn) set(key, value []byte) {
	if err := tx.Set(key, value); err != nil {
		panic(err)
	}
}

func (tx *tikvTxn) append(key []byte, value []byte) []byte {
	new := append(tx.get(key), value...)
	tx.set(key, new)
	return new
}

func (tx *tikvTxn) incrBy(key []byte, value int64) int64 {
	var new int64
	buf := tx.get(key)
	if len(buf) > 0 {
		new = parseCounter(buf)
	}
	if value != 0 {
		new += value
		tx.set(key, packCounter(new))
	}
	return new
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
	return "tikv"
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
		tx.SetEnable1PC(true)
		tx.SetEnableAsyncCommit(true)
		err = tx.Commit(context.Background())
	}
	return err
}
