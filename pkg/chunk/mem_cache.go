/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package chunk

import (
	"errors"
	"sync"
	"time"
)

type memItem struct {
	atime time.Time
	page  *Page
}

type memcache struct {
	sync.Mutex
	capacity int64
	used     int64
	pages    map[string]memItem
}

func newMemStore(config *Config) *memcache {
	c := &memcache{
		capacity: config.CacheSize << 20,
		pages:    make(map[string]memItem),
	}
	return c
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

func (c *memcache) cache(key string, p *Page, force bool) {
	if c.capacity == 0 {
		return
	}
	c.Lock()
	defer c.Unlock()
	if _, ok := c.pages[key]; ok {
		return
	}
	size := int64(cap(p.Data))
	cacheWrites.Add(1)
	cacheWriteBytes.Add(float64(size))
	p.Acquire()
	c.pages[key] = memItem{time.Now(), p}
	c.used += size
	if c.used > c.capacity {
		c.cleanup()
	}
}

func (c *memcache) delete(key string, p *Page) {
	size := int64(cap(p.Data))
	c.used -= size
	p.Release()
	delete(c.pages, key)
}

func (c *memcache) remove(key string) {
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
			cacheEvicts.Add(1)
			c.delete(lastKey, lastValue.page)
			cnt = 0
			if c.used < c.capacity {
				break
			}
		}
	}
}

func (c *memcache) stage(key string, data []byte, keepCache bool) (string, error) {
	return "", errors.New("not supported")
}
func (c *memcache) uploaded(key string, size int) {}
func (c *memcache) stagePath(key string) string   { return "" }
