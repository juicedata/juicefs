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
	"syscall"
)

// doGetAttrWithCache implements attribute retrieval with client-side cache
func (m *redisMeta) doGetAttrWithCache(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	// Try to get from cache first
	m.cacheMu.RLock()
	if cached, ok := m.inodeCache.Get(inode); ok {
		m.cacheMu.RUnlock()
		cachedAttr := cached
		*attr = *cachedAttr
		return 0
	}
	m.cacheMu.RUnlock()

	// Not in cache, fetch from Redis
	a, err := m.rdb.Get(ctx, m.inodeKey(inode)).Bytes()
	if err != nil {
		return errno(err)
	}

	m.parseAttr(a, attr)

	// Add to cache
	cachedAttr := *attr
	m.cacheMu.Lock()
	m.inodeCache.Add(inode, &cachedAttr)
	m.cacheMu.Unlock()

	return 0
}

// GetAttr retrieves the attribute of an inode with client-side caching support
func (m *redisMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	if m.clientCache {
		return m.doGetAttrWithCache(ctx, inode, attr)
	}
	return m.baseMeta.GetAttr(ctx, inode, attr)
}
