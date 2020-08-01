// Copyright (C) 2018-present Juicedata Inc.

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
	"unsafe"
)

type obj struct {
	data  []byte
	mtime time.Time
	mode  os.FileMode
	owner string
	group string
}

type memStore struct {
	sync.Mutex
	defaultObjectStorage
	name    string
	objects map[string]*obj
}

func (m *memStore) String() string {
	return fmt.Sprintf("mem://%s", m.name)
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
	m.objects[key] = &obj{data: data, mtime: time.Now()}
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
			f := &File{Object{k, int64(len(obj.data)), obj.mtime, strings.HasSuffix(k, "/")}, obj.owner, obj.group, obj.mode}
			objs = append(objs, (*Object)(unsafe.Pointer(f)))
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
	store.objects = make(map[string]*obj)
	return store
}

func init() {
	register("mem", newMem)
}
