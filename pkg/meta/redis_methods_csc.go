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
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// doLookupWithCache implements a cached version of lookup
func (m *redisMeta) doLookupWithCache(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno {
	// Try to get from cache first
	cacheKey := fmt.Sprintf("%d:%s", parent, name)
	m.cacheMu.RLock()
	if cached, ok := m.entryCache.Get(cacheKey); ok {
		m.cacheMu.RUnlock()
		entry := cached
		*inode = entry.ino
		*attr = entry.attr
		return 0
	}
	m.cacheMu.RUnlock()

	// Not in cache, perform actual lookup
	var foundIno Ino
	var foundType uint8

	entryKey := m.entryKey(parent)

	// Get the entry
	buf, err := m.rdb.HGet(ctx, entryKey, name).Bytes()
	if err != nil {
		return errno(err)
	}

	foundType, foundIno = m.parseEntry(buf)
	if foundType == 0 {
		return syscall.ENOENT
	}
	// Get the inode attributes
	encodedAttr, err := m.rdb.Get(ctx, m.inodeKey(foundIno)).Bytes()

	if err == nil {
		m.parseAttr(encodedAttr, attr)

		// Add to cache
		m.cacheMu.Lock()
		m.entryCache.Add(cacheKey, struct {
			ino  Ino
			attr Attr
		}{foundIno, *attr})
		m.cacheMu.Unlock()
	} else if err == redis.Nil {
		*attr = Attr{Typ: foundType}
		err = nil
	}

	if err != nil {
		return errno(err)
	}

	*inode = foundIno
	return 0
}

// Lookup retrieves the inode ID and attributes for a directory entry with client-side caching support
func (m *redisMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno {
	if m.clientCache {
		return m.doLookupWithCache(ctx, parent, name, inode, attr, checkPerm)
	}
	return m.baseMeta.Lookup(ctx, parent, name, inode, attr, checkPerm)
}

// invalidateFileCache explicitly invalidates cached data for a file
func (m *redisMeta) invalidateFileCache(inode Ino) {
	if !m.clientCache {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	// Remove from inode cache
	m.inodeCache.Remove(inode)
	// Remove any related entry cache items
	for _, k := range m.entryCache.Keys() {
		keyStr := k
		// Check if this entry references our inode
		if strings.Contains(keyStr, fmt.Sprintf(":%d:", uint64(inode))) ||
			strings.HasSuffix(keyStr, fmt.Sprintf(":%d", uint64(inode))) {
			m.entryCache.Remove(keyStr)
		}
	}
	
	// Remove any related read cache items
	if m.readCache != nil {
		for _, k := range m.readCache.Keys() {
			if k.Inode == inode {
				m.readCache.Remove(k)
			}
		}
	}

	logger.Debugf("Explicitly invalidated cache for inode %d", inode)
}

// Write with cache invalidation
func (m *redisMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time) syscall.Errno {
	// Specifically invalidate read cache for this chunk before writing
	if m.clientCache && m.readCache != nil {
		m.invalidateReadCache(inode, indx)
	}
	
	result := m.baseMeta.Write(ctx, inode, indx, off, slice, mtime)
	if result == 0 && m.clientCache {
		m.invalidateFileCache(inode)
	}
	return result
}

// Truncate with cache invalidation
func (m *redisMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr, skipPermCheck bool) syscall.Errno {
	result := m.baseMeta.Truncate(ctx, inode, flags, length, attr, skipPermCheck)
	if result == 0 && m.clientCache {
		m.invalidateFileCache(inode)
	}
	return result
}

// Create with cache invalidation
func (m *redisMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	result := m.baseMeta.Create(ctx, parent, name, mode, cumask, flags, inode, attr)
	if result == 0 && m.clientCache {
		// Invalidate parent directory cache
		m.invalidateEntryCache(parent, name)
	}
	return result
}
