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

// shutdownClientSideCaching safely cleans up CSC resources
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
