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

	"github.com/prometheus/client_golang/prometheus"
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

	cacheWrites     prometheus.Counter
	cacheEvicts     prometheus.Counter
	cacheWriteBytes prometheus.Counter
}

func newMemStore(config *Config, reg prometheus.Registerer) *memcache {
	c := &memcache{
		capacity: config.CacheSize << 20,
		pages:    make(map[string]memItem),

		cacheWrites: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "blockcache_writes",
			Help: "written cached block",
		}),
		cacheEvicts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "blockcache_evicts",
			Help: "evicted cache blocks",
		}),
		cacheWriteBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "blockcache_write_bytes",
			Help: "write bytes of cached block",
		}),
	}
	if reg != nil {
		reg.MustRegister(c.cacheWrites)
		reg.MustRegister(c.cacheWriteBytes)
		reg.MustRegister(c.cacheEvicts)
	}
	runtime.SetFinalizer(c, func(c *memcache) {
		for _, p := range c.pages {
			p.page.Release()
		}
		c.pages = nil
	})
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
	c.cacheWrites.Add(1)
	c.cacheWriteBytes.Add(float64(size))
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
			c.cacheEvicts.Add(1)
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
