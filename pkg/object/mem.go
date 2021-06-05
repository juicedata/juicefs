/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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

package object

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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
		return nil, errors.New("not exists")
	}
	f := &file{
		obj{
			key,
			int64(len(o.data)),
			o.mtime,
			strings.HasSuffix(key, "/"),
		},
		o.owner,
		o.group,
		o.mode,
	}
	return f, nil
}

func (m *memStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
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
	data := d.data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (m *memStore) Put(key string, in io.Reader) error {
	m.Lock()
	defer m.Unlock()
	// Minimum length is 1.
	if key == "" {
		return errors.New("object key cannot be empty")
	}
	_, ok := m.objects[key]
	if ok {
		return errors.New("can't overwrite")
	}
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	m.objects[key] = &mobj{data: data, mtime: time.Now()}
	return nil
}

func (m *memStore) Chmod(key string, mode os.FileMode) error {
	m.Lock()
	defer m.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return errors.New("not found")
	}
	obj.mode = mode
	return nil
}

func (m *memStore) Chown(key string, owner, group string) error {
	m.Lock()
	defer m.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return errors.New("not found")
	}
	obj.owner = owner
	obj.group = group
	return nil
}

func (m *memStore) Chtimes(key string, mtime time.Time) error {
	m.Lock()
	defer m.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return errors.New("not found")
	}
	obj.mtime = mtime
	return nil
}

func (m *memStore) Copy(dst, src string) error {
	d, err := m.Get(src, 0, -1)
	if err != nil {
		return err
	}
	return m.Put(dst, d)
}

func (m *memStore) Delete(key string) error {
	m.Lock()
	defer m.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memStore) List(prefix, marker string, limit int64) ([]Object, error) {
	m.Lock()
	defer m.Unlock()

	objs := make([]Object, 0)
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) && k > marker {
			o := m.objects[k]
			f := &file{
				obj{
					k,
					int64(len(o.data)),
					o.mtime,
					strings.HasSuffix(k, "/"),
				},
				o.owner,
				o.group,
				o.mode,
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
	return objs, nil
}

func newMem(endpoint, accesskey, secretkey string) (ObjectStorage, error) {
	store := &memStore{name: endpoint}
	store.objects = make(map[string]*mobj)
	return store, nil
}

func init() {
	Register("mem", newMem)
}
