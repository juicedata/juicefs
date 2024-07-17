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

import "fmt"

type prefixTxn struct {
	txn    *KvTxn
	prefix []byte
}

func (tx *prefixTxn) realKey(key []byte) []byte {
	k := make([]byte, len(tx.prefix)+len(key))
	copy(k, tx.prefix)
	copy(k[len(tx.prefix):], key)
	return k
}

func (tx *prefixTxn) origKey(key []byte) []byte {
	return key[len(tx.prefix):]
}

func (tx *prefixTxn) Get(key []byte) []byte {
	return tx.txn.Get(tx.realKey(key))
}

func (tx *prefixTxn) Gets(keys ...[]byte) [][]byte {
	realKeys := make([][]byte, len(keys))
	for i, key := range keys {
		realKeys[i] = tx.realKey(key)
	}
	return tx.txn.Gets(realKeys...)
}

func (tx *prefixTxn) Scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	tx.txn.Scan(tx.realKey(begin), tx.realKey(end), keysOnly, func(k, v []byte) bool {
		return handler(tx.origKey(k), v)
	})
}

func (tx *prefixTxn) Exist(prefix []byte) bool {
	return tx.txn.Exist(tx.realKey(prefix))
}

func (tx *prefixTxn) Set(key, value []byte) {
	tx.txn.Set(tx.realKey(key), value)
}

func (tx *prefixTxn) Append(key []byte, value []byte) {
	tx.txn.Append(tx.realKey(key), value)
}

func (tx *prefixTxn) IncrBy(key []byte, value int64) int64 {
	return tx.txn.IncrBy(tx.realKey(key), value)
}

func (tx *prefixTxn) Delete(key []byte) {
	tx.txn.Delete(tx.realKey(key))
}

type prefixClient struct {
	TkvClient
	prefix []byte
}

func (c *prefixClient) txn(f func(*KvTxn) error, retry int) error {
	return c.TkvClient.Txn(func(tx *KvTxn) error {
		return f(&KvTxn{&prefixTxn{tx, c.prefix}, retry})
	}, retry)
}

func (c *prefixClient) scan(prefix []byte, handler func(key, value []byte)) error {
	k := make([]byte, len(c.prefix)+len(prefix))
	copy(k, c.prefix)
	copy(k[len(c.prefix):], prefix)
	return c.TkvClient.Scan(k, func(key, value []byte) {
		handler(key[len(c.prefix):], value)
	})
}

func (c *prefixClient) Reset(prefix []byte) error {
	if prefix != nil {
		return fmt.Errorf("prefix must be nil, but got %v", prefix)
	}
	return c.TkvClient.Reset(c.prefix)
}

func withPrefix(client TkvClient, prefix []byte) TkvClient {
	return &prefixClient{client, prefix}
}
