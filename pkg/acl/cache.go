/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package acl

import (
	"sync"
)

const None = 0

// Cache all rules
// - cache all rules when meta init.
// - on getfacl failure, read and cache rule from meta.
// - on setfacl success, read and cache all missed rules from meta. (considered as a low-frequency operation)
// - concurrent mounts may result in duplicate rules.
type Cache interface {
	Put(id uint32, r *Rule)
	Get(id uint32) *Rule
	GetId(r *Rule) uint32
	Size() int
	GetMissIds(maxId uint32) []uint32
}

func NewCache() Cache {
	return &cache{
		lock:     sync.RWMutex{},
		maxId:    None,
		id2Rule:  make(map[uint32]*Rule),
		cksum2Id: make(map[uint32][]uint32),
	}
}

type cache struct {
	lock     sync.RWMutex
	maxId    uint32
	id2Rule  map[uint32]*Rule
	cksum2Id map[uint32][]uint32
}

func (c *cache) GetMissIds(maxId uint32) []uint32 {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if c.maxId == maxId && uint32(len(c.id2Rule)) == maxId {
		return nil
	}

	if c.maxId > maxId {
		maxId = c.maxId
	}

	n := maxId + 1
	mark := make([]bool, n)
	for i := uint32(1); i < n; i++ {
		if _, ok := c.id2Rule[i]; ok {
			mark[i] = true
		}
	}

	var ret []uint32
	for i := uint32(1); i < n; i++ {
		if !mark[i] {
			ret = append(ret, i)
		}
	}
	return ret
}

func (c *cache) Size() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.id2Rule)
}

func (c *cache) Get(id uint32) *Rule {
	c.lock.RLock()
	defer c.lock.RUnlock()
	if r, ok := c.id2Rule[id]; ok {
		return r
	}
	return nil
}

func (c *cache) Put(id uint32, r *Rule) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if _, ok := c.id2Rule[id]; ok {
		return
	}

	if id > c.maxId {
		c.maxId = id
	}
	c.id2Rule[id] = r

	// empty slot
	if r == nil {
		return
	}

	cksum := r.Checksum()
	if _, ok := c.cksum2Id[cksum]; ok {
		c.cksum2Id[cksum] = append(c.cksum2Id[cksum], id)
	} else {
		c.cksum2Id[r.Checksum()] = []uint32{id}
	}
}

func (c *cache) GetId(r *Rule) uint32 {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if r == nil {
		return None
	}

	if ids, ok := c.cksum2Id[r.Checksum()]; ok {
		for _, id := range ids {
			if r.IsEqual(c.id2Rule[id]) {
				return id
			}
		}
	}
	return None
}
