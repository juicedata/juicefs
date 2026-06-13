//go:build slatedb
// +build slatedb

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	slatedb "slatedb.io/slatedb-go/uniffi"
)

var errSlateDBReadOnly = errors.New("slatedb: write in read-only transaction")

func slatedbKeyRange(begin, end []byte) slatedb.KeyRange {
	r := slatedb.KeyRange{StartInclusive: true, EndInclusive: false}
	if begin != nil {
		r.Start = &begin
	}
	if end != nil {
		r.End = &end
	}
	return r
}

func slatedbIterate(it *slatedb.DbIterator, keysOnly bool, handler func(k, v []byte) bool) {
	defer it.Destroy()
	for {
		kv, err := it.Next()
		if err != nil {
			panic(err)
		}
		if kv == nil {
			return
		}
		var value []byte
		if !keysOnly {
			value = kv.Value
		}
		if !handler(kv.Key, value) {
			return
		}
	}
}

type slatedbTxn struct {
	t *slatedb.DbTransaction
	c *slatedbClient
}

func (tx *slatedbTxn) id() uint64 {
	// add logical id to avoid conflict between concurrent transactions
	return tx.t.Seqnum()*1e2 + tx.c.getId()%1e2
}

func (tx *slatedbTxn) get(key []byte) []byte {
	v, err := tx.t.Get(key)
	if err != nil {
		panic(err)
	}
	if v == nil {
		return nil
	}
	return *v
}

func (tx *slatedbTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *slatedbTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	it, err := tx.t.Scan(slatedbKeyRange(begin, end))
	if err != nil {
		panic(err)
	}
	slatedbIterate(it, keysOnly, handler)
}

func (tx *slatedbTxn) exist(prefix []byte) bool {
	it, err := tx.t.ScanPrefix(prefix)
	if err != nil {
		panic(err)
	}
	defer it.Destroy()
	kv, err := it.Next()
	if err != nil {
		panic(err)
	}
	return kv != nil
}

func (tx *slatedbTxn) set(key, value []byte) {
	if err := tx.t.Put(key, value); err != nil {
		panic(err)
	}
}

func (tx *slatedbTxn) append(key []byte, value []byte) {
	list := append(tx.get(key), value...)
	tx.set(key, list)
}

func (tx *slatedbTxn) incrBy(key []byte, value int64) int64 {
	buf := tx.get(key)
	newCounter := parseCounter(buf)
	if value != 0 {
		newCounter += value
		tx.set(key, packCounter(newCounter))
	}
	return newCounter
}

func (tx *slatedbTxn) delete(key []byte) {
	if err := tx.t.Delete(key); err != nil {
		panic(err)
	}
}

// slatedbSnapTxn is the read-only view used by simpleTxn.
type slatedbSnapTxn struct {
	s *slatedb.DbSnapshot
}

func (tx *slatedbSnapTxn) id() uint64 { return 0 }

func (tx *slatedbSnapTxn) get(key []byte) []byte {
	v, err := tx.s.Get(key)
	if err != nil {
		panic(err)
	}
	if v == nil {
		return nil
	}
	return *v
}

func (tx *slatedbSnapTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *slatedbSnapTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	it, err := tx.s.Scan(slatedbKeyRange(begin, end))
	if err != nil {
		panic(err)
	}
	slatedbIterate(it, keysOnly, handler)
}

func (tx *slatedbSnapTxn) exist(prefix []byte) bool {
	it, err := tx.s.ScanPrefix(prefix)
	if err != nil {
		panic(err)
	}
	defer it.Destroy()
	kv, err := it.Next()
	if err != nil {
		panic(err)
	}
	return kv != nil
}

func (tx *slatedbSnapTxn) set(key, value []byte) {
	panic(errSlateDBReadOnly)
}

func (tx *slatedbSnapTxn) append(key []byte, value []byte) {
	panic(errSlateDBReadOnly)
}

func (tx *slatedbSnapTxn) incrBy(key []byte, value int64) int64 {
	if value != 0 {
		panic(errSlateDBReadOnly)
	}
	return parseCounter(tx.get(key))
}

func (tx *slatedbSnapTxn) delete(key []byte) {
	panic(errSlateDBReadOnly)
}

type slatedbClient struct {
	store        *slatedb.ObjectStore
	db           *slatedb.Db
	awaitDurable bool
	nextid       uint64
}

func (c *slatedbClient) name() string {
	return "slatedb"
}

func (c *slatedbClient) getId() uint64 {
	return atomic.AddUint64(&c.nextid, 1)
}

func (c *slatedbClient) rewind(id uint64, factor int) uint64 {
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

func (c *slatedbClient) shouldRetry(err error) bool {
	return errors.Is(err, slatedb.ErrErrorTransaction)
}

func (c *slatedbClient) config(key string) interface{} {
	return nil
}

func (c *slatedbClient) simpleTxn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	snap, err := c.db.Snapshot()
	if err != nil {
		return err
	}
	defer snap.Destroy()
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
	return f(&kvTxn{&slatedbSnapTxn{snap}, retry})
}

func (c *slatedbClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) (err error) {
	tx, err := c.db.Begin(slatedb.IsolationLevelSerializableSnapshot)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		// a transaction is consumed by Commit even when it fails (e.g. conflict)
		if !committed {
			_ = tx.Rollback()
		}
		tx.Destroy()
	}()
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
	err = f(&kvTxn{&slatedbTxn{tx, c}, retry})
	if err != nil {
		return err
	}
	committed = true
	_, err = tx.CommitWithOptions(slatedb.WriteOptions{AwaitDurable: c.awaitDurable})
	return err
}

func (c *slatedbClient) scan(prefix []byte, handler func(key []byte, value []byte) bool) (err error) {
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
	it, err := c.db.ScanPrefix(prefix)
	if err != nil {
		return err
	}
	slatedbIterate(it, false, handler)
	return nil
}

func (c *slatedbClient) reset(prefix []byte) error {
	const batch = 10000
	for {
		var keys [][]byte
		if err := c.scan(prefix, func(k, v []byte) bool {
			keys = append(keys, k)
			return len(keys) < batch
		}); err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		if err := c.txn(context.Background(), func(tx *kvTxn) error {
			for _, key := range keys {
				tx.delete(key)
			}
			return nil
		}, 0); err != nil {
			return err
		}
		if len(keys) < batch {
			return nil
		}
	}
}

func (c *slatedbClient) close() error {
	err := c.db.Shutdown()
	c.db.Destroy()
	c.store.Destroy()
	return err
}

func (c *slatedbClient) gc() {}

// newSlateDBClient opens a SlateDB database from addr, which is the part of
// the meta URL after "slatedb://". Supported forms:
//
//	slatedb://memory                          in-memory store (for testing)
//	slatedb:///var/lib/jfs/meta               local directory (file:// store)
//	slatedb://s3://bucket/prefix              any object store URL understood
//	                                          by SlateDB (s3://, gs://, az://...);
//	                                          credentials are taken from the
//	                                          environment (AWS_*, etc.)
//
// Query parameters:
//
//	dbpath=<path>        path of the database inside the object store (default: root)
//	durability=remote    wait until commits are durable in the object store (default)
//	durability=memory    ack commits from memory; bounded loss window on crash
//	settings=<json>      SlateDB settings as a JSON object, e.g. {"flush_interval":"20ms"}
func newSlateDBClient(addr string) (tkvClient, error) {
	query := url.Values{}
	if p := strings.IndexByte(addr, '?'); p >= 0 {
		var err error
		query, err = url.ParseQuery(addr[p+1:])
		if err != nil {
			return nil, fmt.Errorf("parse query %q: %s", addr[p+1:], err)
		}
		addr = addr[:p]
	}
	awaitDurable := true
	switch strings.ToLower(query.Get("durability")) {
	case "", "remote":
	case "memory":
		awaitDurable = false
	default:
		return nil, fmt.Errorf("invalid durability %q, expect remote or memory", query.Get("durability"))
	}

	storeURL := addr
	if storeURL == "" || storeURL == "memory" {
		storeURL = "memory:///"
	} else if !strings.Contains(storeURL, "://") {
		p, err := filepath.Abs(storeURL)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(p, 0750); err != nil {
			return nil, fmt.Errorf("create dir %s: %s", p, err)
		}
		p = filepath.ToSlash(p)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p // windows drive letter
		}
		storeURL = "file://" + p
	}
	store, err := slatedb.ObjectStoreResolve(storeURL)
	if err != nil {
		return nil, fmt.Errorf("resolve object store %s: %s", storeURL, err)
	}
	builder := slatedb.NewDbBuilder(query.Get("dbpath"), store)
	defer builder.Destroy()
	if s := query.Get("settings"); s != "" {
		// apply fields one by one on top of the defaults; SettingsFromJsonString
		// would require a complete settings object
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &fields); err != nil {
			store.Destroy()
			return nil, fmt.Errorf("parse settings %q: %s", s, err)
		}
		settings := slatedb.SettingsDefault()
		defer settings.Destroy()
		for k, v := range fields {
			if err := settings.Set(k, string(v)); err != nil {
				store.Destroy()
				return nil, fmt.Errorf("apply setting %s=%s: %s", k, v, err)
			}
		}
		if err := builder.WithSettings(settings); err != nil {
			store.Destroy()
			return nil, err
		}
	}
	db, err := builder.Build()
	if err != nil {
		store.Destroy()
		return nil, fmt.Errorf("open slatedb at %s: %s", storeURL, err)
	}
	return &slatedbClient{store: store, db: db, awaitDurable: awaitDurable}, nil
}

func init() {
	Register("slatedb", newKVMeta)
	drivers["slatedb"] = newSlateDBClient
}
