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
	"container/heap"
	"errors"
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

func (s *sharded) Limits() Limits {
	l := s.stores[0].Limits()
	l.IsSupportUploadPartCopy = false
	return l
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

func (s *sharded) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	return s.pick(key).Get(key, off, limit, getters...)
}

func (s *sharded) Put(key string, body io.Reader, getters ...AttrGetter) error {
	return s.pick(key).Put(key, body, getters...)
}

func (s *sharded) Copy(dst, src string) error {
	return notSupported
}

func (s *sharded) Delete(key string, getters ...AttrGetter) error {
	return s.pick(key).Delete(key, getters...)
}

func (s *sharded) SetStorageClass(sc string) error {
	var err = notSupported
	for _, o := range s.stores {
		if os, ok := o.(SupportStorageClass); ok {
			err = os.SetStorageClass(sc)
		}
	}
	return err
}

const maxResults = 10000

// ListAll on all the keys that starts at marker from object storage.
func ListAll(store ObjectStorage, prefix, marker string, followLink bool) (<-chan Object, error) {
	if ch, err := store.ListAll(prefix, marker, followLink); err == nil {
		return ch, nil
	} else if !errors.Is(err, notSupported) {
		return nil, err
	}

	startTime := time.Now()
	out := make(chan Object, maxResults)
	logger.Debugf("Listing objects from %s marker %q", store, marker)
	objs, hasMore, nextToken, err := store.List(prefix, marker, "", "", maxResults, followLink)
	if errors.Is(err, notSupported) {
		return ListAllWithDelimiter(store, prefix, marker, "", followLink)
	}
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
	go func() {
		defer close(out)
		lastkey := ""
		first := true
		for {
			for _, obj := range objs {
				key := obj.Key()
				if !first && key <= lastkey {
					logger.Errorf("The keys are out of order: marker %q, last %q current %q", marker, lastkey, key)
					out <- nil
					return
				}
				lastkey = key
				// logger.Debugf("found key: %s", key)
				out <- obj
				first = false
			}
			if !hasMore {
				break
			}

			marker = lastkey
			startTime = time.Now()
			logger.Debugf("Continue listing objects from %s marker %q", store, marker)
			var nextToken2 string
			objs, hasMore, nextToken2, err = store.List(prefix, marker, nextToken, "", maxResults, followLink)
			for err != nil {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				time.Sleep(time.Millisecond * 100)
				objs, hasMore, nextToken, err = store.List(prefix, marker, nextToken, "", maxResults, followLink)
			}
			nextToken = nextToken2
			logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
		}
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

func (s *sharded) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	heads := &nextObjects{make([]nextKey, 0)}
	for i := range s.stores {
		ch, err := ListAll(s.stores[i], prefix, marker, followLink)
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

func NewSharded(name, endpoint, ak, sk, token string, shards int) (ObjectStorage, error) {
	stores := make([]ObjectStorage, shards)
	var err error
	for i := range stores {
		ep := fmt.Sprintf(endpoint, i)
		if strings.HasSuffix(ep, "%!(EXTRA int=0)") {
			return nil, fmt.Errorf("can not generate different endpoint using %s", endpoint)
		}
		stores[i], err = CreateStorage(name, ep, ak, sk, token)
		if err != nil {
			return nil, err
		}
	}
	return &sharded{stores: stores}, nil
}
