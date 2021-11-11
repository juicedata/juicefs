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

func (tx *prefixTxn) scanKeys(prefix []byte) [][]byte {
	keys := tx.kvTxn.scanKeys(tx.realKey(prefix))
	for i, k := range keys {
		keys[i] = tx.origKey(k)
	}
	return keys
}

func (tx *prefixTxn) scanValues(prefix []byte, filter func(k, v []byte) bool) map[string][]byte {
	r := tx.kvTxn.scanValues(tx.realKey(prefix), func(k, v []byte) bool {
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

func withPrefix(client tkvClient, prefix []byte) tkvClient {
	return &prefixClient{client, prefix}
}
