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
	"container/heap"
	"fmt"
	"hash/fnv"
	"io"
	"strings"
	"time"
)

type sharded struct {
	DefaultObjectStorage
	stores []ObjectStorage
}

func (s *sharded) String() string {
	return fmt.Sprintf("shard%d://%s", len(s.stores), s.stores[0])
}

func (s *sharded) Create() error {
	for _, o := range s.stores {
		if err := o.Create(); err != nil {
			return err
		}
	}
	return nil
}

func (s *sharded) pick(key string) ObjectStorage {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	i := h.Sum32() % uint32(len(s.stores))
	return s.stores[i]
}

func (s *sharded) Head(key string) (Object, error) {
	return s.pick(key).Head(key)
}

func (s *sharded) Get(key string, off, limit int64) (io.ReadCloser, error) {
	return s.pick(key).Get(key, off, limit)
}

func (s *sharded) Put(key string, body io.Reader) error {
	return s.pick(key).Put(key, body)
}

func (s *sharded) Delete(key string) error {
	return s.pick(key).Delete(key)
}

const maxResults = 10000

// ListAll on all the keys that starts at marker from object storage.
func ListAll(store ObjectStorage, prefix, marker string) (<-chan Object, error) {
	if ch, err := store.ListAll(prefix, marker); err == nil {
		return ch, nil
	}

	startTime := time.Now()
	out := make(chan Object, maxResults)
	logger.Debugf("Listing objects from %s marker %q", store, marker)
	objs, err := store.List("", marker, maxResults)
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
	go func() {
		lastkey := ""
		first := true
	END:
		for len(objs) > 0 {
			for _, obj := range objs {
				key := obj.Key()
				if !first && key <= lastkey {
					logger.Fatalf("The keys are out of order: marker %q, last %q current %q", marker, lastkey, key)
				}
				lastkey = key
				// logger.Debugf("found key: %s", key)
				out <- obj
				first = false
			}
			// Corner case: the func parameter `marker` is an empty string("") and exactly
			// one object which key is an empty string("") returned by the List() method.
			if lastkey == "" {
				break END
			}

			marker = lastkey
			startTime = time.Now()
			logger.Debugf("Continue listing objects from %s marker %q", store, marker)
			objs, err = store.List(prefix, marker, maxResults)
			for err != nil {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				time.Sleep(time.Millisecond * 100)
				objs, err = store.List(prefix, marker, maxResults)
			}
			logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
		}
		close(out)
	}()
	return out, nil
}

type nextKey struct {
	o  Object
	ch <-chan Object
}

type nextObjects struct {
	os []nextKey
}

func (s *nextObjects) Len() int           { return len(s.os) }
func (s *nextObjects) Less(i, j int) bool { return s.os[i].o.Key() < s.os[j].o.Key() }
func (s *nextObjects) Swap(i, j int)      { s.os[i], s.os[j] = s.os[j], s.os[i] }
func (s *nextObjects) Push(o interface{}) { s.os = append(s.os, o.(nextKey)) }
func (s *nextObjects) Pop() interface{} {
	o := s.os[len(s.os)-1]
	s.os = s.os[:len(s.os)-1]
	return o
}

func (s *sharded) ListAll(prefix, marker string) (<-chan Object, error) {
	heads := &nextObjects{make([]nextKey, 0)}
	for i := range s.stores {
		ch, err := ListAll(s.stores[i], prefix, marker)
		if err != nil {
			return nil, fmt.Errorf("list %s: %s", s.stores[i], err)
		}
		first := <-ch
		if first != nil {
			heads.Push(nextKey{first, ch})
		}
	}
	heap.Init(heads)

	out := make(chan Object, 1000)
	go func() {
		for heads.Len() > 0 {
			n := heap.Pop(heads).(nextKey)
			out <- n.o
			o := <-n.ch
			if o != nil {
				heap.Push(heads, nextKey{o, n.ch})
			}
		}
		close(out)
	}()
	return out, nil
}

func (s *sharded) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return s.pick(key).CreateMultipartUpload(key)
}

func (s *sharded) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return s.pick(key).UploadPart(key, uploadID, num, body)
}

func (s *sharded) AbortUpload(key string, uploadID string) {
	s.pick(key).AbortUpload(key, uploadID)
}

func (s *sharded) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return s.pick(key).CompleteUpload(key, uploadID, parts)
}

func NewSharded(name, endpoint, ak, sk string, shards int) (ObjectStorage, error) {
	stores := make([]ObjectStorage, shards)
	var err error
	for i := range stores {
		ep := fmt.Sprintf(endpoint, i)
		if strings.HasSuffix(ep, "%!(EXTRA int=0)") {
			return nil, fmt.Errorf("can not generate different endpoint using %s", endpoint)
		}
		stores[i], err = CreateStorage(name, ep, ak, sk)
		if err != nil {
			return nil, err
		}
	}
	return &sharded{stores: stores}, nil
}
