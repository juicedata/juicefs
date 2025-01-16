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

package chunk

import (
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
)

type memItem struct {
	atime time.Time
	page  *Page
}

type memcache struct {
	sync.Mutex
	capacity    int64
	maxItems    int64
	used        int64
	pages       map[string]memItem
	eviction    string
	cacheExpire time.Duration

	metrics *cacheManagerMetrics
}

func newMemStore(config *Config, metrics *cacheManagerMetrics) *memcache {
	c := &memcache{
		capacity:    int64(config.CacheSize),
		maxItems:    config.CacheItems,
		pages:       make(map[string]memItem),
		eviction:    config.CacheEviction,
		cacheExpire: config.CacheExpire,
		metrics:     metrics,
	}
	runtime.SetFinalizer(c, func(c *memcache) {
		for _, p := range c.pages {
			p.page.Release()
		}
		c.pages = nil
	})
	if c.cacheExpire > 0 {
		go c.cleanupExpire()
	}
	return c
}

func (c *memcache) removeStage(key string) error {
	return nil
}

func (c *memcache) usedMemory() int64 {
	c.Lock()
	defer c.Unlock()
	return c.used
}

func (c *memcache) stats() (int64, int64) {
	c.Lock()
	defer c.Unlock()
	return int64(len(c.pages)), c.used
}

func (c *memcache) cache(key string, p *Page, force, dropCache bool) {
	if !c.enabled() {
		return
	}
	c.Lock()
	defer c.Unlock()
	if c.full() && c.eviction == "none" {
		logger.Debugf("Caching is full, drop %s (%d bytes)", key, len(p.Data))
		c.metrics.cacheDrops.Add(1)
		return
	}
	if _, ok := c.pages[key]; ok {
		return
	}
	size := int64(cap(p.Data))
	c.metrics.cacheWrites.Add(1)
	c.metrics.cacheWriteBytes.Add(float64(size))
	p.Acquire()
	c.pages[key] = memItem{time.Now(), p}
	c.used += size
	if c.full() && c.eviction != "none" {
		c.cleanup()
	}
}

func (c *memcache) delete(key string, p *Page) {
	size := int64(cap(p.Data))
	c.used -= size
	p.Release()
	delete(c.pages, key)
}

func (c *memcache) remove(key string, staging bool) {
	c.Lock()
	defer c.Unlock()
	if item, ok := c.pages[key]; ok {
		c.delete(key, item.page)
		logger.Debugf("remove %s from cache", key)
	}
}

func (c *memcache) load(key string) (ReadCloser, error) {
	c.Lock()
	defer c.Unlock()
	if item, ok := c.pages[key]; ok {
		c.pages[key] = memItem{time.Now(), item.page}
		return NewPageReader(item.page), nil
	}
	return nil, errors.New("not found")
}

func (c *memcache) exist(key string) bool {
	if !c.enabled() {
		return false
	}
	c.Lock()
	defer c.Unlock()
	if item, ok := c.pages[key]; ok {
		c.pages[key] = memItem{time.Now(), item.page}
		return true
	}
	return false
}

// locked
func (c *memcache) cleanup() {
	var cnt int
	var lastKey string
	var lastValue memItem
	var now = time.Now()
	// for each two random keys, then compare the access time, evict the older one
	for k, v := range c.pages {
		if cnt == 0 || lastValue.atime.After(v.atime) {
			lastKey = k
			lastValue = v
		}
		cnt++
		if cnt > 1 {
			logger.Debugf("remove %s from cache, age: %d", lastKey, now.Sub(lastValue.atime))
			c.metrics.cacheEvicts.Add(1)
			c.delete(lastKey, lastValue.page)
			cnt = 0
			if !c.full() {
				break
			}
		}
	}
}

func (c *memcache) enabled() bool {
	return c.capacity > 0
}

func (c *memcache) full() bool {
	return c.used > c.capacity || (c.maxItems != 0 && int64(len(c.pages)) > c.maxItems)
}

func (c *memcache) cleanupExpire() {
	var interval = time.Minute
	if c.cacheExpire < time.Minute {
		interval = c.cacheExpire
	}
	for {
		var freed int64
		var cnt, deleted int
		c.Lock()
		cutoff := time.Now().Add(-c.cacheExpire)
		for k, v := range c.pages {
			cnt++
			if cnt > 1e3 {
				break
			}
			if v.atime.Before(cutoff) {
				deleted++
				freed += int64(cap(v.page.Data))
				c.metrics.cacheEvicts.Add(1)
				c.delete(k, v.page)
			}
		}
		c.Unlock()
		if deleted > 0 {
			logger.Debugf("Expired cache blocks: %d blocks (%s), remaining: %d blocks (%s)", deleted, humanize.IBytes(uint64(freed)), len(c.pages), humanize.IBytes(uint64(c.used)))
		}
		time.Sleep(interval * time.Duration((cnt+1-deleted)/(cnt+1)))
	}
}

func (c *memcache) stage(key string, data []byte, keepCache bool) (string, error) {
	return "", errors.New("not supported")
}
func (c *memcache) uploaded(key string, size int)    {}
func (c *memcache) isEmpty() bool                    { return false }
func (c *memcache) getMetrics() *cacheManagerMetrics { return c.metrics }
