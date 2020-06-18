// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"sort"
	"strings"
	"sync"
	"time"
)

type Obj struct {
	Data    []byte
	Updated int
}

type memStore struct {
	sync.Mutex
	defaultObjectStorage
	objects map[string]*Obj
}

func (m *memStore) String() string {
	return "memstore"
}

func (m *memStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	m.Lock()
	defer m.Unlock()
	d, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not exists")
	}
	data := d.Data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (m *memStore) Put(key string, in io.Reader) error {
	m.Lock()
	defer m.Unlock()
	_, ok := m.objects[key]
	if ok {
		return errors.New("can't overwrite")
	}
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	m.objects[key] = &Obj{data, int(time.Now().Unix())}
	return nil
}

func (m *memStore) Copy(dst, src string) error {
	d, err := m.Get(src, 0, -1)
	if err != nil {
		return err
	}
	return m.Put(dst, d)
}

func (m *memStore) Exists(key string) error {
	m.Lock()
	defer m.Unlock()
	_, ok := m.objects[key]
	if !ok {
		return errors.New("not exists")
	}
	return nil
}

func (m *memStore) Delete(key string) error {
	m.Lock()
	defer m.Unlock()
	_, ok := m.objects[key]
	if !ok {
		return errors.New("not exists")
	}
	delete(m.objects, key)
	return nil
}

type sortObject []*Object

func (s sortObject) Len() int           { return len(s) }
func (s sortObject) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s sortObject) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (m *memStore) List(prefix, marker string, limit int64) ([]*Object, error) {
	m.Lock()
	defer m.Unlock()

	objs := make([]*Object, 0)
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) && k > marker {
			obj := m.objects[k]
			objs = append(objs, &Object{k, int64(len(obj.Data)), obj.Updated, obj.Updated})
		}
	}
	sort.Sort(sortObject(objs))
	if int64(len(objs)) > limit {
		objs = objs[:limit]
	}
	return objs, nil
}

func newMem(endpoint, accesskey, secretkey string) ObjectStorage {
	store := &memStore{}
	store.objects = make(map[string]*Obj)
	return store
}

func init() {
	register("mem", newMem)
}
