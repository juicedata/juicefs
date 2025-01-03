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

import (
	"context"
	"fmt"
)

type prefixTxn struct {
	*kvTxn
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

func (tx *prefixTxn) get(key []byte) []byte {
	return tx.kvTxn.get(tx.realKey(key))
}

func (tx *prefixTxn) gets(keys ...[]byte) [][]byte {
	realKeys := make([][]byte, len(keys))
	for i, key := range keys {
		realKeys[i] = tx.realKey(key)
	}
	return tx.kvTxn.gets(realKeys...)
}

func (tx *prefixTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	tx.kvTxn.scan(tx.realKey(begin), tx.realKey(end), keysOnly, func(k, v []byte) bool {
		return handler(tx.origKey(k), v)
	})
}

func (tx *prefixTxn) exist(prefix []byte) bool {
	return tx.kvTxn.exist(tx.realKey(prefix))
}

func (tx *prefixTxn) set(key, value []byte) {
	tx.kvTxn.set(tx.realKey(key), value)
}

func (tx *prefixTxn) append(key []byte, value []byte) {
	tx.kvTxn.append(tx.realKey(key), value)
}

func (tx *prefixTxn) incrBy(key []byte, value int64) int64 {
	return tx.kvTxn.incrBy(tx.realKey(key), value)
}

func (tx *prefixTxn) delete(key []byte) {
	tx.kvTxn.delete(tx.realKey(key))
}

type prefixClient struct {
	tkvClient
	prefix []byte
}

func (c *prefixClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) error {
	return c.tkvClient.txn(ctx, func(tx *kvTxn) error {
		return f(&kvTxn{&prefixTxn{tx, c.prefix}, retry})
	}, retry)
}

func (c *prefixClient) scan(prefix []byte, handler func(key, value []byte)) error {
	k := make([]byte, len(c.prefix)+len(prefix))
	copy(k, c.prefix)
	copy(k[len(c.prefix):], prefix)
	return c.tkvClient.scan(k, func(key, value []byte) {
		handler(key[len(c.prefix):], value)
	})
}

func (c *prefixClient) reset(prefix []byte) error {
	if prefix != nil {
		return fmt.Errorf("prefix must be nil, but got %v", prefix)
	}
	return c.tkvClient.reset(c.prefix)
}

func withPrefix(client tkvClient, prefix []byte) tkvClient {
	return &prefixClient{client, prefix}
}
