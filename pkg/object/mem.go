/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type mobj struct {
	data  []byte
	mtime time.Time
	mode  os.FileMode
	owner string
	group string
}

type memStore struct {
	sync.Mutex
	DefaultObjectStorage
	name    string
	objects map[string]*mobj
}

func (m *memStore) String() string {
	return fmt.Sprintf("mem://%s/", m.name)
}

func (m *memStore) Head(key string) (Object, error) {
	m.Lock()
	defer m.Unlock()
	// Minimum length is 1.
	if key == "" {
		return nil, errors.New("object key cannot be empty")
	}
	o, ok := m.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	f := &file{
		obj{
			key,
			int64(len(o.data)),
			o.mtime,
			strings.HasSuffix(key, "/"),
			"",
		},
		o.owner,
		o.group,
		o.mode,
		false,
	}
	return f, nil
}

func (m *memStore) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	m.Lock()
	defer m.Unlock()
	// Minimum length is 1.
	if key == "" {
		return nil, errors.New("object key cannot be empty")
	}
	d, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not exists")
	}
	if off > int64(len(d.data)) {
		off = int64(len(d.data))
	}
	data := d.data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (m *memStore) Put(key string, in io.Reader, getters ...AttrGetter) error {
	m.Lock()
	defer m.Unlock()
	// Minimum length is 1.
	if key == "" {
		return errors.New("object key cannot be empty")
	}
	_, ok := m.objects[key]
	if ok {
		logger.Debugf("overwrite %s", key)
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	m.objects[key] = &mobj{data: data, mtime: time.Now()}
	return nil
}

func (m *memStore) Copy(dst, src string) error {
	d, err := m.Get(src, 0, -1)
	if err != nil {
		return err
	}
	return m.Put(dst, d)
}

func (m *memStore) Delete(key string, getters ...AttrGetter) error {
	m.Lock()
	defer m.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memStore) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	m.Lock()
	defer m.Unlock()

	objs := make([]Object, 0)
	commonPrefixsMap := make(map[string]bool, 0)
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) && k > marker {
			o := m.objects[k]
			if delimiter != "" {
				remainString := strings.TrimPrefix(k, prefix)
				if pos := strings.Index(remainString, delimiter); pos != -1 {
					commonPrefix := remainString[0 : pos+1]
					if _, ok := commonPrefixsMap[commonPrefix]; ok {
						continue
					}
					f := &file{
						obj{
							prefix + commonPrefix,
							0,
							time.Unix(0, 0),
							strings.HasSuffix(commonPrefix, "/"),
							"",
						},
						o.owner,
						o.group,
						o.mode,
						false,
					}
					objs = append(objs, f)
					commonPrefixsMap[commonPrefix] = true
					continue
				}
			}

			f := &file{
				obj{
					k,
					int64(len(o.data)),
					o.mtime,
					strings.HasSuffix(k, "/"),
					"",
				},
				o.owner,
				o.group,
				o.mode,
				false,
			}
			objs = append(objs, f)
		}
	}
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].Key() < objs[j].Key()
	})
	if int64(len(objs)) > limit {
		objs = objs[:limit]
	}
	return generateListResult(objs, limit)
}

func newMem(endpoint, accesskey, secretkey, token string) (ObjectStorage, error) {
	store := &memStore{name: endpoint}
	store.objects = make(map[string]*mobj)
	return store, nil
}

func init() {
	Register("mem", newMem)
}
