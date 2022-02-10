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
	kvTxn
	prefix []byte
}

func (tx *prefixTxn) realKey(key []byte) []byte {
	return append(tx.prefix, key...)
}

func (tx *prefixTxn) origKey(key []byte) []byte {
	return key[len(tx.prefix):]
}

func (tx *prefixTxn) get(key []byte) []byte {
	return tx.kvTxn.get(tx.realKey(key))
}

func (tx *prefixTxn) gets(keys ...[]byte) [][]byte {
	for i, key := range keys {
		keys[i] = tx.realKey(key)
	}
	return tx.kvTxn.gets(keys...)
}

func (tx *prefixTxn) scanRange(begin_, end_ []byte) map[string][]byte {
	r := tx.kvTxn.scanRange(tx.realKey(begin_), tx.realKey(end_))
	m := make(map[string][]byte, len(r))
	for k, v := range r {
		m[k[len(tx.prefix):]] = v
	}
	return m
}
func (tx *prefixTxn) scan(prefix []byte, handler func(key, value []byte)) {
	tx.kvTxn.scan(tx.realKey(prefix), func(key, value []byte) {
		key = tx.origKey(key)
		handler(key, value)
	})
}
func (tx *prefixTxn) scanKeys(prefix []byte) [][]byte {
	keys := tx.kvTxn.scanKeys(tx.realKey(prefix))
	for i, k := range keys {
		keys[i] = tx.origKey(k)
	}
	return keys
}

func (tx *prefixTxn) scanValues(prefix []byte, limit int, filter func(k, v []byte) bool) map[string][]byte {
	r := tx.kvTxn.scanValues(tx.realKey(prefix), limit, func(k, v []byte) bool {
		if filter == nil {
			return true
		}
		return filter(tx.origKey(k), v)
	})
	m := make(map[string][]byte, len(r))
	for k, v := range r {
		m[k[len(tx.prefix):]] = v
	}
	return m
}

func (tx *prefixTxn) exist(prefix []byte) bool {
	return tx.kvTxn.exist(tx.realKey(prefix))
}

func (tx *prefixTxn) set(key, value []byte) {
	tx.kvTxn.set(tx.realKey(key), value)
}

func (tx *prefixTxn) append(key []byte, value []byte) []byte {
	return tx.kvTxn.append(tx.realKey(key), value)
}

func (tx *prefixTxn) incrBy(key []byte, value int64) int64 {
	return tx.kvTxn.incrBy(tx.realKey(key), value)
}

func (tx *prefixTxn) dels(keys ...[]byte) {
	for i, key := range keys {
		keys[i] = tx.realKey(key)
	}
	tx.kvTxn.dels(keys...)
}

type prefixClient struct {
	tkvClient
	prefix []byte
}

func (c *prefixClient) txn(f func(kvTxn) error) error {
	return c.tkvClient.txn(func(tx kvTxn) error {
		return f(&prefixTxn{tx, c.prefix})
	})
}

func (c *prefixClient) reset(prefix []byte) error {
	if prefix != nil {
		return fmt.Errorf("prefix must be nil")
	}
	return c.tkvClient.reset(c.prefix)
}

func withPrefix(client tkvClient, prefix []byte) tkvClient {
	return &prefixClient{client, prefix}
}
