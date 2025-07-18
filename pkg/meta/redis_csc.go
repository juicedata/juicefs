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
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// setupClientSideCaching configures Redis client-side caching
func (m *redisMeta) setupClientSideCaching(expiry time.Duration) error {
	ctx := Background()

	// Store the expiry setting - 0 means infinite (no expiry)
	m.clientCacheExpiry = expiry

	// For cluster clients, we need a separate connection for tracking
	if _, ok := m.rdb.(*redis.ClusterClient); ok {
		// For cluster mode, we should get the master node for our key
		_, err := m.rdb.(*redis.ClusterClient).MasterForKey(ctx, m.prefix)
		if err != nil {
			return err
		}
	}

	// Enable tracking - first disable tracking if already enabled
	_ = m.rdb.Do(ctx, "CLIENT", "TRACKING", "OFF").Err() // Ignore errors if not previously enabled

	// Always use BCAST mode for simplicity
	err := m.rdb.Do(ctx, "CLIENT", "TRACKING", "ON", "BCAST").Err()
	if err != nil {
		return err
	}

	// Subscribe to invalidation messages
	m.cacheSubscription = m.rdb.Subscribe(ctx, "__redis__:invalidate")

	// Start a goroutine to handle invalidation messages
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("Recovered from panic when starting cache invalidation: %v", r)
			}
		}()
		m.handleCacheInvalidation()
	}()

	return nil
}

// handleCacheInvalidation processes invalidation messages from Redis
func (m *redisMeta) handleCacheInvalidation() {
	ch := m.cacheSubscription.Channel()
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Recovered from panic in handleCacheInvalidation: %v", r)
		}
	}()

	for msg := range ch {
		key := msg.Payload
		if key == "" || !strings.HasPrefix(key, m.prefix) {
			continue
		}

		m.cacheMu.Lock()
		if strings.HasPrefix(key, m.prefix+"i") {
			// Invalidate inode cache
			inodeStr := key[len(m.prefix)+1:]
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err == nil {
				m.inodeCache.Remove(Ino(inode))
				logger.Debugf("Invalidated inode cache for %s", inodeStr)
			}
		} else if strings.HasPrefix(key, m.prefix+"d") {
			// Invalidate entry cache
			parentStr := key[len(m.prefix)+1:]
			parent, err := strconv.ParseUint(parentStr, 10, 64)
			if err == nil {
				// We need to invalidate all entries related to this directory
				for _, k := range m.entryCache.Keys() {
					if strings.HasPrefix(k, strconv.FormatUint(parent, 10)+":") {
						m.entryCache.Remove(k)
					}
				}
				logger.Debugf("Invalidated entry cache for %s", parentStr)
			}
		}
		m.cacheMu.Unlock()
	}
}

// setupCachedMethods prepares cached versions of methods
func (m *redisMeta) setupCachedMethods() {
	if m.clientCache {
		logger.Debugf("Redis client-side caching methods are ready")
	}
}

// safeGetAttr safely gets inode attributes for preloading, handling Redis parsing errors
func (m *redisMeta) safeGetAttr(ctx Context, inode Ino) (*Attr, bool) {
	attr := &Attr{}

	// Instead of using baseMeta.GetAttr which can trigger Redis parsing errors,
	// we'll directly access Redis with error handling
	a, err := m.rdb.Get(ctx, m.inodeKey(inode)).Bytes()
	if err != nil {
		// Check if it's a Redis parsing error which might be caused by CSC
		if strings.Contains(err.Error(), "can't parse reply") {
			logger.Debugf("Ignoring Redis parsing error for inode %d: %v", inode, err)
			return nil, false
		}

		// If it's another error, also skip
		if err != redis.Nil {
			logger.Debugf("Error getting inode %d: %v", inode, err)
		}
		return nil, false
	}

	// Parse the attribute bytes
	m.parseAttr(a, attr)
	return attr, true
}

// safeReaddir safely reads directory entries for preloading, handling Redis parsing errors
func (m *redisMeta) safeReaddir(ctx Context, inode Ino) ([]*Entry, bool) {
	var entries []*Entry

	// Get all entries from the directory hash
	entryKey := m.entryKey(inode)
	var vals map[string]string

	vals, err := m.rdb.HGetAll(ctx, entryKey).Result()
	if err != nil {
		// Check if it's a Redis parsing error
		if strings.Contains(err.Error(), "can't parse reply") {
			logger.Debugf("Ignoring Redis parsing error in readdir for inode %d: %v", inode, err)
			return nil, false
		}

		logger.Debugf("Error getting directory entries for inode %d: %v", inode, err)
		return nil, false
	}

	// Process each entry
	for name, val := range vals {
		if name == "" {
			continue
		}

		typ, ino := m.parseEntry([]byte(val))

		entry := &Entry{
			Inode: ino,
			Name:  []byte(name),
			Attr: &Attr{
				Typ: typ,
			},
		}

		entries = append(entries, entry)
	}

	return entries, true
}

// preloadInodeCache loads the first N inodes into cache
func (m *redisMeta) preloadInodeCache(maxCount int) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Recovered from panic in preloadInodeCache: %v", r)
		}
	}()

	if !m.clientCache || m.inodeCache == nil {
		return
	}

	ctx := Background()
	logger.Infof("Preloading up to %d inodes into client-side cache...", maxCount)
	start := time.Now()

	// Instead of scanning all Redis keys (which can cause parsing issues),
	// start from the root inode and load the most important inodes first
	rootInode := Ino(1)
	count := 0

	// First, let's load the root inode safely
	rootAttr, ok := m.safeGetAttr(ctx, rootInode)
	if !ok {
		logger.Warnf("Error preloading root inode")
	} else {
		// Cache manually
		cachedAttr := *rootAttr
		m.cacheMu.Lock()
		m.inodeCache.Add(rootInode, &cachedAttr)
		m.cacheMu.Unlock()
		count++

		// Load root directory entries to get the most important inodes
		entries, ok := m.safeReaddir(ctx, rootInode)
		if !ok {
			logger.Warnf("Error reading root directory")
		} else {
			// Load attributes for each entry in root dir
			for _, entry := range entries {
				if count >= maxCount {
					break
				}

				entryAttr, ok := m.safeGetAttr(ctx, entry.Inode)
				if ok {
					// Cache manually
					cachedEntryAttr := *entryAttr
					m.cacheMu.Lock()
					m.inodeCache.Add(entry.Inode, &cachedEntryAttr)
					m.cacheMu.Unlock()

					count++

					// If it's a directory, also load its contents recursively
					if entryAttr.Typ == TypeDirectory && count < maxCount {
						subEntries, ok := m.safeReaddir(ctx, entry.Inode)
						if ok {
							for _, subEntry := range subEntries {
								if count >= maxCount {
									break
								}

								subAttr, ok := m.safeGetAttr(ctx, subEntry.Inode)
								if ok {
									cachedSubAttr := *subAttr
									m.cacheMu.Lock()
									m.inodeCache.Add(subEntry.Inode, &cachedSubAttr)
									m.cacheMu.Unlock()

									count++
								}
							}
						}
					}
				}

				if count%100 == 0 && count > 0 {
					logger.Debugf("Preloaded %d inodes...", count)
				}
			}
		}
	}

	logger.Infof("Preloaded %d inodes in %v", count, time.Since(start))
} // shutdownClientSideCaching safely cleans up CSC resources
func (m *redisMeta) shutdownClientSideCaching() {
	if m.cacheSubscription != nil {
		err := m.cacheSubscription.Close()
		if err != nil {
			logger.Warnf("Error closing Redis cache subscription: %v", err)
		}
		m.cacheSubscription = nil
	}

	// Disable tracking
	if m.clientCache {
		ctx := Background()
		_ = m.rdb.Do(ctx, "CLIENT", "TRACKING", "OFF").Err()
	}
}
