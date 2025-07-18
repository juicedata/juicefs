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
	"fmt"
)

// Helper method to invalidate cache entries
func (m *redisMeta) invalidateInodeCache(inode Ino) {
	if !m.clientCache {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	m.inodeCache.Remove(inode)
	logger.Debugf("Manually invalidated inode cache for %d", inode)
}

// Helper method to invalidate directory entry cache
func (m *redisMeta) invalidateEntryCache(parent Ino, name string) {
	if !m.clientCache {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	cacheKey := fmt.Sprintf("%d:%s", parent, name)
	m.entryCache.Remove(cacheKey)
	logger.Debugf("Manually invalidated entry cache for %d:%s", parent, name)
}

// Helper method to invalidate read cache for a specific inode and chunk index
func (m *redisMeta) invalidateReadCache(inode Ino, indx uint32) {
	if !m.clientCache || m.readCache == nil {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	key := readCacheKey{Inode: inode, Index: indx}
	m.readCache.Remove(key)
	logger.Debugf("Manually invalidated read cache for inode %d index %d", inode, indx)
}

// Helper method to invalidate all read cache entries for an inode
func (m *redisMeta) invalidateAllReadCache(inode Ino) {
	if !m.clientCache || m.readCache == nil {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	// We need to scan through the cache and remove all entries for this inode
	// This is not very efficient, but it's a simple solution
	for _, key := range m.readCache.Keys() {
		if key.Inode == inode {
			m.readCache.Remove(key)
		}
	}

	logger.Debugf("Manually invalidated all read cache entries for inode %d", inode)
}
