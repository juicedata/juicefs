/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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

package chunk

import (
	"container/heap"
	"fmt"
	"math"
	"time"
)

const (
	EvictionNone    = "none"
	Eviction2Random = "2-random"
	EvictionLRU     = "lru"
)

const notInLru = math.MinInt // to trigger panic when misused

type cacheItem struct {
	size  int32
	atime uint32
}

type KeyIndex interface {
	name() string
	add(key cacheKey, item cacheItem)
	// remove removes key, staging blocks will not be removed unless explicitly requested
	remove(key cacheKey, staging bool) *cacheItem
	get(key cacheKey) *cacheItem
	peekAtime(key cacheKey) uint32
	len() int
	reset() KeyIndex
	// randomIter iterates over all items randomly
	randomIter() func(yield func(key cacheKey, item cacheItem) bool)
	// evictionIter evicts items based on different evict policies, yielding each evicted item
	evictionIter() func(yield func(key cacheKey, item cacheItem) bool)
}

func NewKeyIndex(config *Config) (KeyIndex, error) {
	switch config.CacheEviction {
	case EvictionNone:
		return &noneEviction{keys: make(map[cacheKey]cacheItem)}, nil
	case Eviction2Random:
		return &randomEviction{
			noneEviction: noneEviction{keys: make(map[cacheKey]cacheItem)},
			cacheExpire:  config.CacheExpire,
		}, nil
	case EvictionLRU:
		return &lruEviction{
			keys:    make(map[cacheKey]*lruItem),
			lruHeap: atimeHeap{},
		}, nil
	default:
		return nil, fmt.Errorf("unknown cache eviction policy: %q", config.CacheEviction)
	}
}

// noneEviction is a policy that does nothing.
type noneEviction struct {
	keys map[cacheKey]cacheItem
}

func (p *noneEviction) name() string {
	return EvictionNone
}

func (p *noneEviction) add(key cacheKey, item cacheItem) {
	p.keys[key] = item
}

func (p *noneEviction) remove(key cacheKey, staging bool) *cacheItem {
	item, ok := p.keys[key]
	if !ok {
		return nil
	}
	if item.size < 0 && !staging {
		return nil
	}
	delete(p.keys, key)
	return &item
}

func (p *noneEviction) get(key cacheKey) *cacheItem {
	if iter, ok := p.keys[key]; ok {
		// update atime
		p.keys[key] = cacheItem{iter.size, uint32(time.Now().Unix())}
		return &iter
	}
	return nil
}

func (p *noneEviction) peekAtime(key cacheKey) uint32 {
	return p.keys[key].atime
}

func (p *noneEviction) len() int {
	return len(p.keys)
}

func (p *noneEviction) reset() KeyIndex {
	snap := &noneEviction{keys: p.keys}
	p.keys = make(map[cacheKey]cacheItem, len(p.keys))
	return snap
}

func (p *noneEviction) randomIter() func(yield func(key cacheKey, item cacheItem) bool) {
	return func(yield func(key cacheKey, item cacheItem) bool) {
		for k, v := range p.keys {
			if !yield(k, v) {
				return
			}
		}
	}
}

func (p *noneEviction) evictionIter() func(yield func(key cacheKey, item cacheItem) bool) {
	panic("not implemented for " + p.name())
}

// randomEviction evicts items randomly.
type randomEviction struct {
	noneEviction
	cacheExpire time.Duration
}

func (p *randomEviction) name() string {
	return Eviction2Random
}

func (p *randomEviction) reset() KeyIndex {
	snap := &randomEviction{
		noneEviction: noneEviction{keys: p.keys},
		cacheExpire:  p.cacheExpire,
	}
	p.keys = make(map[cacheKey]cacheItem, len(p.keys))
	return snap
}

func (p *randomEviction) evictionIter() func(yield func(key cacheKey, item cacheItem) bool) {
	return func(yield func(key cacheKey, item cacheItem) bool) {
		var cnt int
		var lastK cacheKey
		var lastValue cacheItem
		var now = uint32(time.Now().Unix())
		var cutoff = now - uint32(p.cacheExpire/time.Second)
		for k, value := range p.keys {
			if value.size < 0 {
				continue // staging
			}
			if p.cacheExpire > 0 && value.atime < cutoff {
				lastK = k
				lastValue = value
				cnt++
			} else if cnt == 0 || lastValue.atime > value.atime {
				lastK = k
				lastValue = value
			}
			cnt++
			if cnt > 1 {
				delete(p.keys, lastK)
				if !yield(lastK, lastValue) {
					return
				}
				cnt = 0
			}
		}
	}
}

type lruItem struct {
	cacheItem
	pos int // Item position in lru heap, needed for updates
}

// A min-heap based on atime for cache eviction
type atimeHeap []heapItem

type heapItem struct {
	*lruItem
	key *cacheKey // key to cacheItem
}

func (h atimeHeap) Len() int { return len(h) }

func (h atimeHeap) Less(i, j int) bool { // min-heap
	if h[i].atime != h[j].atime {
		return h[i].atime < h[j].atime
	}
	if h[i].size != h[j].size {
		return h[i].size > h[j].size // prefer deleting larger blocks
	}
	return h[i].key.id < h[j].key.id
}

func (h atimeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].pos = i
	h[j].pos = j
}

func (h *atimeHeap) Push(x any) {
	item := x.(heapItem)
	item.pos = len(*h)
	*h = append(*h, item)
}

func (h *atimeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	item.pos = notInLru
	*h = old[0 : n-1]
	return item
}

// lruEviction evicts items based on least recent use (atime).
type lruEviction struct {
	keys    map[cacheKey]*lruItem
	lruHeap atimeHeap
}

func (p *lruEviction) name() string {
	return EvictionLRU
}

func (p *lruEviction) add(key cacheKey, item cacheItem) {
	if iter, ok := p.keys[key]; !ok {
		iter = &lruItem{cacheItem: item, pos: notInLru}
		p.keys[key] = iter
		if iter.size > 0 { // don't add staging blocks to lru as they should not be evicted in `cleanupFull`
			heap.Push(&p.lruHeap, heapItem{iter, &key})
		}
	} else {
		iter.cacheItem = item
		if iter.pos == notInLru {
			heap.Push(&p.lruHeap, heapItem{iter, &key})
		} else {
			heap.Fix(&p.lruHeap, iter.pos)
		}
	}
}

func (p *lruEviction) remove(key cacheKey, staging bool) *cacheItem {
	item, ok := p.keys[key]
	if !ok {
		return nil
	}
	if item.size < 0 && !staging {
		return nil
	}
	delete(p.keys, key)
	if item.pos != notInLru {
		heap.Remove(&p.lruHeap, item.pos)
	}
	return &item.cacheItem
}

func (p *lruEviction) get(key cacheKey) *cacheItem {
	if iter, ok := p.keys[key]; ok {
		// update atime
		iter.atime = uint32(time.Now().Unix())
		if iter.pos != notInLru {
			heap.Fix(&p.lruHeap, iter.pos)
		}
		return &iter.cacheItem
	}
	return nil
}

func (p *lruEviction) peekAtime(key cacheKey) uint32 {
	if item, ok := p.keys[key]; ok {
		return item.cacheItem.atime
	}
	return 0
}

func (p *lruEviction) len() int {
	return len(p.keys)
}

func (p *lruEviction) reset() KeyIndex {
	snap := &lruEviction{
		keys:    p.keys,
		lruHeap: p.lruHeap,
	}
	p.keys = make(map[cacheKey]*lruItem, len(p.keys))
	p.lruHeap = make(atimeHeap, 0, len(p.lruHeap))
	return snap
}

func (p *lruEviction) randomIter() func(yield func(key cacheKey, item cacheItem) bool) {
	return func(yield func(key cacheKey, item cacheItem) bool) {
		for k, v := range p.keys {
			if !yield(k, v.cacheItem) {
				return
			}
		}
	}
}

func (p *lruEviction) evictionIter() func(yield func(key cacheKey, item cacheItem) bool) {
	return func(yield func(key cacheKey, item cacheItem) bool) {
		for p.lruHeap.Len() > 0 {
			item := heap.Pop(&p.lruHeap).(heapItem)
			if item.size < 0 {
				logger.Warnf("Got a staging block in LRU: %s", item.key) // should not happen
				continue
			}
			delete(p.keys, *item.key)
			if !yield(*item.key, item.lruItem.cacheItem) {
				return
			}
		}
	}
}

// nolint:unused
func (p *lruEviction) verifyHeap() bool {
	cacheKeys := 0
	for k, v := range p.keys {
		if v.size > 0 {
			cacheKeys += 1
		} else if v.pos != notInLru {
			logger.Warnf("Staging block %s has size %d but index %d in lruHeap", k, v.size, v.pos)
			return false
		}
	}
	if p.lruHeap.Len() != cacheKeys {
		logger.Warnf("atime heap length %d does not match keys length %d", p.lruHeap.Len(), len(p.keys))
		return false
	}
	for i, item := range p.lruHeap {
		if item.pos != i {
			logger.Warnf("atime heap item %d index %d does not match its position %d", i, item.pos, i)
			return false
		}
		if it, ok := p.keys[*item.key]; !ok {
			logger.Warnf("heap item %d key %s not found in keys map", i, item.key)
			return false
		} else if it.cacheItem != item.cacheItem {
			logger.Warnf("heap item %d key %s does not match cacheItem in keys map", i, item.key)
			return false
		}
	}
	// Also validate the min-heap property based on atime
	n := p.lruHeap.Len()
	for i := 0; i < n/2; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < n && p.lruHeap[i].atime > p.lruHeap[left].atime {
			logger.Warnf("heap property violated: parent atime %d > left child atime %d at index %d", p.lruHeap[i].atime, p.lruHeap[left].atime, i)
			return false
		}
		if right < n && p.lruHeap[i].atime > p.lruHeap[right].atime {
			logger.Warnf("heap property violated: parent atime %d > right child atime %d at index %d", p.lruHeap[i].atime, p.lruHeap[right].atime, i)
			return false
		}
	}
	return true
}
