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

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
)

type cached struct {
	ObjectStorage
	hot ObjectStorage
}

func (c *cached) String() string {
	return fmt.Sprintf("cached://%s/%s", c.hot, c.ObjectStorage)
}

func (c *cached) Get(key string, off, limit int64) (io.ReadCloser, error) {
	r, err := c.hot.Get(key, off, limit)
	if err != nil {
		r, err = c.ObjectStorage.Get(key, off, limit)
		if err == nil && off == 0 && limit == -1 {
			// TODO: guess the size and allocate the memory first
			data, err := ioutil.ReadAll(r)
			r.Close()
			if err != nil {
				return nil, err
			}
			go func() {
				_ = c.hot.Put(key, bytes.NewReader(data))
			}()
			r = ioutil.NopCloser(bytes.NewBuffer(data))
		}
	}
	return r, err
}

func (c *cached) Put(key string, in io.Reader) error {
	if rr, ok := in.(*bytes.Buffer); ok && rr.Len() < 1<<20 {
		// build cache for small files
		_ = c.hot.Put(key, bytes.NewReader(rr.Bytes()))
	}
	return c.ObjectStorage.Put(key, in)
}

func (c *cached) Delete(key string) error {
	_ = c.hot.Delete(key)
	return c.ObjectStorage.Delete(key)
}

func NewCachedStore(cold, hot ObjectStorage) ObjectStorage {
	return &cached{cold, hot}
}
