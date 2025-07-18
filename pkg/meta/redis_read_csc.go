//go:build !noredis
// +build !noredis

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package meta

import (
	"strings"
	"syscall"
)

// CacheKey for read operations - combines inode and index
type readCacheKey struct {
	Inode Ino
	Index uint32
}

// Read with client-side cache support
func (m *redisMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	if m.clientCache {
		// Use the cached method for reading with CSC support
		slices, err := m.doReadWithCache(ctx, inode, indx)
		if err != 0 {
			return err
		}
		// Convert []*slice to []Slice
		result := make([]Slice, len(slices))
		for i, s := range slices {
			result[i] = Slice{
				Id:   s.id,
				Size: s.size,
				Off:  s.off,
				Len:  s.len,
			}
		}
		*chunks = result
		return 0
	}
	// Use the base method if CSC is disabled
	return m.baseMeta.Read(ctx, inode, indx, chunks)
}

// doReadWithCache implements chunk reading with client-side cache support
func (m *redisMeta) doReadWithCache(ctx Context, inode Ino, indx uint32) ([]*slice, syscall.Errno) {
	// Handle any Redis specific errors or array responses
	result, err := m.rdb.LRange(ctx, m.chunkKey(inode, indx), 0, -1).Result()
	if err != nil {
		// Redis CSC can cause array responses when the cache is valid
		if strings.HasPrefix(err.Error(), "redis: can't parse reply=\"*") {
			// This is actually a cache hit - no data has changed
			// Need to check if we have it in our local cache
			key := readCacheKey{Inode: inode, Index: indx}
			m.cacheMu.RLock()
			if cached, ok := m.readCache.Get(key); ok {
				m.cacheMu.RUnlock()
				// Return the cached slices
				return cached, 0
			}
			m.cacheMu.RUnlock()
			
			// If not in cache, fall back to regular read
			// This is inefficient but should be rare
			return m.doRead(ctx, inode, indx)
		}
		return nil, errno(err)
	}

	// Parse the results
	slices := readSlices(result)
	
	// Cache the results
	key := readCacheKey{Inode: inode, Index: indx}
	m.cacheMu.Lock()
	m.readCache.Add(key, slices)
	m.cacheMu.Unlock()
	
	return slices, 0
}
