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
)

const (
	EvictionNone    = "none"
	Eviction2Random = "2-random"
	EvictionLRU     = "lru"
)

const notInLru = -1

// A min-heap based on atime for cache eviction
type atimeHeap []heapItem

type heapItem struct {
	*cacheItem
	key *cacheKey // key to cacheItem
}

func (h atimeHeap) Len() int { return len(h) }

func (h atimeHeap) Less(i, j int) bool { // min-heap
	if h[i].atime == h[j].atime {
		return h[i].key.id < h[j].key.id
	}
	return h[i].atime < h[j].atime
}

func (h atimeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *atimeHeap) Push(x any) {
	item := x.(heapItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *atimeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	item.index = -1 // marker
	*h = old[0 : n-1]
	return item
}

func (cache *cacheStore) lruPush(k cacheKey) {
	if cache.eviction == EvictionLRU {
		heap.Push(&cache.lruHeap, heapItem{cache.keys[k], &k})
	}
}

func (cache *cacheStore) lruFix(index int) {
	if cache.eviction == EvictionLRU && index != notInLru { // `cleanupfull` may pop heap without deleting keys
		heap.Fix(&cache.lruHeap, index)
	}
}

func (cache *cacheStore) lruPop() heapItem {
	item := heap.Pop(&cache.lruHeap).(heapItem)
	item.index = notInLru
	return item
}

func (cache *cacheStore) lruRemove(index int) {
	if cache.eviction == EvictionLRU && index != notInLru {
		heap.Remove(&cache.lruHeap, index)
	}
}

// nolint:unused
func (cache *cacheStore) verifyHeap() bool {
	cacheKeys := 0
	for k, v := range cache.keys {
		if v.size > 0 {
			cacheKeys += 1
		} else if v.index != notInLru {
			logger.Warnf("Staging block %s has size %d but index %d in lruHeap", k, v.size, v.index)
			return false
		}
	}
	if cache.lruHeap.Len() != cacheKeys {
		logger.Warnf("atime heap length %d does not match keys length %d", cache.lruHeap.Len(), len(cache.keys))
		return false
	}
	for i, item := range cache.lruHeap {
		if item.index != i {
			logger.Warnf("atime heap item %d index %d does not match its position %d", i, item.index, i)
			return false
		}
		if it, ok := cache.keys[*item.key]; !ok {
			logger.Warnf("heap item %d key %s not found in keys map", i, item.key)
			return false
		} else if it != item.cacheItem {
			logger.Warnf("heap item %d key %s does not match cacheItem in keys map", i, item.key)
			return false
		}
	}
	// Also validate the min-heap property based on atime
	n := cache.lruHeap.Len()
	for i := 0; i < n/2; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < n && cache.lruHeap[i].atime > cache.lruHeap[left].atime {
			logger.Warnf("heap property violated: parent atime %d > left child atime %d at index %d", cache.lruHeap[i].atime, cache.lruHeap[left].atime, i)
			return false
		}
		if right < n && cache.lruHeap[i].atime > cache.lruHeap[right].atime {
			logger.Warnf("heap property violated: parent atime %d > right child atime %d at index %d", cache.lruHeap[i].atime, cache.lruHeap[right].atime, i)
			return false
		}
	}
	return true
}
