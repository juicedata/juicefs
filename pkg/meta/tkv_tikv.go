//go:build !notikv
// +build !notikv

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
	"net/url"
	"os"
	"strings"
	"time"

	plog "github.com/pingcap/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/tikv/client-go/v2/config"
	tikverr "github.com/tikv/client-go/v2/error"
	"github.com/tikv/client-go/v2/oracle"
	"github.com/tikv/client-go/v2/tikv"
	"github.com/tikv/client-go/v2/txnkv"
	"github.com/tikv/client-go/v2/txnkv/txnutil"
	"go.uber.org/zap"
)

func init() {
	Register("tikv", newKVMeta)
	// drivers["tikv"] = newTikvClient
	RegisterKvDriver("tikv", newTikvClient)
}

func newTikvClient(addr string) (TkvClient, error) {
	var plvl string // TiKV (PingCap) uses uber-zap logging, make it less verbose
	switch logger.Level {
	case logrus.TraceLevel:
		plvl = "debug"
	case logrus.DebugLevel:
		plvl = "info"
	case logrus.InfoLevel, logrus.WarnLevel:
		plvl = "warn"
	case logrus.ErrorLevel:
		plvl = "error"
	default:
		plvl = "dpanic"
	}
	l, prop, _ := plog.InitLogger(&plog.Config{Level: plvl}, zap.Fields(zap.String("component", "tikv"), zap.Int("pid", os.Getpid())))
	plog.ReplaceGlobals(l, prop)

	tUrl, err := url.Parse("tikv://" + addr)
	if err != nil {
		return nil, err
	}
	query := tUrl.Query()
	config.UpdateGlobal(func(conf *config.Config) {
		conf.Security = config.NewSecurity(
			query.Get("ca"),
			query.Get("cert"),
			query.Get("key"),
			strings.Split(query.Get("verify-cn"), ","))
	})
	interval := time.Hour * 3
	if dur, err := time.ParseDuration(query.Get("gc-interval")); err == nil {
		if dur != 0 && dur < time.Hour {
			logger.Warnf("TiKV gc-interval (%s) is too short, and is reset to 1h", dur)
			dur = time.Hour
		}
		interval = dur
	}
	logger.Infof("TiKV gc interval is set to %s", interval)

	client, err := txnkv.NewClient(strings.Split(tUrl.Host, ","))
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimLeft(tUrl.Path, "/")
	return withPrefix(&tikvClient{client.KVStore, interval}, append([]byte(prefix), 0xFD)), nil
}

type tikvTxn struct {
	ti *tikv.KVTxn
}

func (tx *tikvTxn) Get(key []byte) []byte {
	value, err := tx.ti.Get(context.TODO(), key)
	if tikverr.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
		panic(err)
	}
	return value
}

func (tx *tikvTxn) Gets(keys ...[]byte) [][]byte {
	ret, err := tx.ti.BatchGet(context.TODO(), keys)
	if err != nil {
		panic(err)
	}
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = ret[string(key)]
	}
	return values
}

func (tx *tikvTxn) Scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	it, err := tx.ti.Iter(begin, end)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	for it.Valid() && handler(it.Key(), it.Value()) {
		if err = it.Next(); err != nil {
			panic(err)
		}
	}
}

func (tx *tikvTxn) Exist(prefix []byte) bool {
	it, err := tx.ti.Iter(prefix, nextKey(prefix))
	if err != nil {
		panic(err)
	}
	defer it.Close()
	return it.Valid()
}

func (tx *tikvTxn) Set(key, value []byte) {
	if err := tx.ti.Set(key, value); err != nil {
		panic(err)
	}
}

func (tx *tikvTxn) Append(key []byte, value []byte) {
	new := append(tx.Get(key), value...)
	tx.Set(key, new)
}

func (tx *tikvTxn) IncrBy(key []byte, value int64) int64 {
	buf := tx.Get(key)
	new := parseCounter(buf)
	if value != 0 {
		new += value
		tx.Set(key, packCounter(new))
	}
	return new
}

func (tx *tikvTxn) Delete(key []byte) {
	if err := tx.ti.Delete(key); err != nil {
		panic(err)
	}
}

type tikvClient struct {
	client     *tikv.KVStore
	gcInterval time.Duration
}

func (c *tikvClient) Name() string {
	return "tikv"
}

func (c *tikvClient) ShouldRetry(err error) bool {
	return strings.Contains(err.Error(), "write conflict") || strings.Contains(err.Error(), "TxnLockNotFound")
}

func (c *tikvClient) Txn(f func(*KvTxn) error, retry int) (err error) {
	tx, err := c.client.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			fe, ok := r.(error)
			if ok {
				err = fe
			} else {
				err = errors.Errorf("tikv client txn func error: %v", r)
			}
		}
	}()
	if err = f(&KvTxn{&tikvTxn{tx}, retry}); err != nil {
		return err
	}
	if !tx.IsReadOnly() {
		tx.SetEnable1PC(true)
		tx.SetEnableAsyncCommit(true)
		err = tx.Commit(context.Background())
	}
	return err
}

func (c *tikvClient) Scan(prefix []byte, handler func(key, value []byte)) error {
	ts, err := c.client.CurrentTimestamp("global")
	if err != nil {
		return err
	}
	snap := c.client.GetSnapshot(ts)
	snap.SetScanBatchSize(10240)
	snap.SetNotFillCache(true)
	snap.SetPriority(txnutil.PriorityLow)
	it, err := snap.Iter(prefix, nextKey(prefix))
	if err != nil {
		return err
	}
	defer it.Close()
	for it.Valid() {
		handler(it.Key(), it.Value())
		if err = it.Next(); err != nil {
			return err
		}
	}
	return nil
}

func (c *tikvClient) Reset(prefix []byte) error {
	_, err := c.client.DeleteRange(context.Background(), prefix, nextKey(prefix), 1)
	return err
}

func (c *tikvClient) Close() error {
	return c.client.Close()
}

func (c *tikvClient) Gc() {
	if c.gcInterval == 0 {
		return
	}
	safePoint, err := c.client.GC(context.Background(), oracle.GoTimeToTS(time.Now().Add(-c.gcInterval)))
	if err == nil {
		logger.Debugf("TiKV GC returns new safe point: %d (%s)", safePoint, oracle.GetTimeFromTS(safePoint))
	} else {
		logger.Warnf("TiKV GC: %s", err)
	}
}
