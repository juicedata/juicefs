//go:build !nobolt
// +build !nobolt

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
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"time"

	bbolt "go.etcd.io/bbolt"
)

var bucket = []byte("bucket")

type bboltTxn struct {
	b *bbolt.Bucket
}

func (tx *bboltTxn) get(key []byte) (v []byte) {
	v = bytes.Clone(tx.b.Get(key))
	return
}

func (tx *bboltTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, key := range keys {
		values[i] = tx.get(key)
	}
	return values
}

func (tx *bboltTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {

	c := tx.b.Cursor()
	for k, v := c.Seek(begin); ; k, v = c.Next() {
		if len(k) == 0 {
			break
		}
		if bytes.Compare(k, end) >= 0 || bytes.Compare(k, begin) < 0 {
			return
		}
		if !handler(bytes.Clone(k), bytes.Clone(v)) {
			break
		}
	}
}

func (tx *bboltTxn) exist(prefix []byte) (exist bool) {
	c := tx.b.Cursor()
	k, _ := c.Seek(prefix)
	exist = bytes.HasPrefix(k, prefix)
	return
}

func (tx *bboltTxn) set(key, value []byte) {
	err := tx.b.Put(key, value)
	if err != nil {
		panic(err)
	}
}

func (tx *bboltTxn) append(key []byte, value []byte) {
	list := append(tx.get(key), value...)
	tx.set(key, list)
}

func (tx *bboltTxn) incrBy(key []byte, value int64) int64 {
	buf := tx.get(key)
	newCounter := parseCounter(buf)
	if value != 0 {
		newCounter += value
		tx.set(key, packCounter(newCounter))
	}
	return newCounter
}

func (tx *bboltTxn) delete(key []byte) {
	err := tx.b.Delete(key)
	if err != nil {
		panic(err)
	}
}

type bboltClient struct {
	client     *bbolt.DB
	txInTx     *bbolt.Tx
	txInTxLock sync.RWMutex
}

func (c *bboltClient) name() string {
	return "bbolt"
}

func (c *bboltClient) shouldRetry(err error) bool {
	return err == bbolt.ErrInvalid
}

func getCallerFrame(skip int) (ret []runtime.Frame) {
	const skipOffset = 2 // skip getCallerFrame and Callers
	pc := make([]uintptr, 30)
	numFrames := runtime.Callers(skip+skipOffset, pc)
	if numFrames < 1 {
		return
	}
	frames := runtime.CallersFrames(pc)
	var frame runtime.Frame
	var more bool
	for {
		frame, more = frames.Next()
		ret = append(ret, frame)
		if !more {
			break
		}
	}
	return
}

func isNested() bool {
	//NOTE: do't move
	fs := getCallerFrame(1)
	firstFunPtr := fs[0].Func.Entry()
	for _, f := range fs[1:] {
		if firstFunPtr == f.Func.Entry() {
			return true
		}
	}
	return false
}

func (c *bboltClient) txn(f func(*kvTxn) error, retry int) (err error) {
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

	// return c.client.Update(func(tx *bbolt.Tx) error {
	// 	ttt := &bboltTxn{b: tx.Bucket(bucket)}
	// 	return f(&kvTxn{ttt, retry})
	// })

	c.txInTxLock.RLock()
	if c.txInTx != nil && isNested() {
		//avoid db lock when txn nest txn
		ttt := &bboltTxn{b: c.txInTx.Bucket(bucket)}
		err = f(&kvTxn{ttt, retry})
		if err != bbolt.ErrTxClosed {
			c.txInTxLock.RUnlock()
			return
		}
		c.txInTx = nil
	}
	c.txInTxLock.RUnlock()

	return c.client.Update(func(tx *bbolt.Tx) error {
		c.txInTxLock.Lock()
		c.txInTx = tx
		c.txInTxLock.Unlock()

		ttt := &bboltTxn{b: tx.Bucket(bucket)}
		err := f(&kvTxn{ttt, retry})
		// c.txInTx = nil

		c.txInTxLock.Lock()
		c.txInTx = nil
		c.txInTxLock.Unlock()
		return err
	})

}

func (c *bboltClient) scan(prefix []byte, handler func(key []byte, value []byte)) error {
	return c.client.View(func(tx *bbolt.Tx) error {
		// Assume bucket exists and has keys
		c := tx.Bucket(bucket).Cursor()

		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// fmt.Printf("bolt SCAN key=%s, value=%s\n", k, v)
			if len(k) == 0 {
				break
			}
			handler(bytes.Clone(k), bytes.Clone(v))
		}
		return nil
	})
}

func (c *bboltClient) reset(prefix []byte) error {

	return c.client.Update(func(tx *bbolt.Tx) error {
		c := tx.Bucket(bucket).Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			fmt.Printf("key=%v, value=%v\n", k, len(v))
			if len(k) == 0 {
				break
			}
			c.Delete()
		}
		return nil
	})
}

func (c *bboltClient) close() error {
	return c.client.Close()
}

func (c *bboltClient) gc() {}

func newbboltClient(addr string) (tkvClient, error) {
	client, err := bbolt.Open(addr, 0600, &bbolt.Options{
		Timeout:         1 * time.Second,
		NoSync:          true,
		InitialMmapSize: 1024,
		FreelistType:    bbolt.FreelistArrayType,
	})
	if err != nil {
		return nil, err
	}

	client.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})

	return &bboltClient{client: client}, err
}

func init() {
	Register("bbolt", newKVMeta)
	drivers["bbolt"] = newbboltClient
}
