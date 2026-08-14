/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davies/groupcache/consistenthash"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/murmur3"
)

var errNotCached = errors.New("not cached")

type cacheManager struct {
	sync.Mutex
	consistentMap *consistenthash.Map
	storeMap      map[string]*cacheStore
	stores        []*cacheStore
	metrics       *cacheManagerMetrics
}

func legacyKeyHash(s string) uint32 {
	hash := fnv.New32()
	_, _ = hash.Write([]byte(s))
	return hash.Sum32()
}

// hasMeta reports whether path contains any of the magic characters
// recognized by Match.
func hasMeta(path string) bool {
	magicChars := `*?[`
	if runtime.GOOS != "windows" {
		magicChars = `*?[\`
	}
	return strings.ContainsAny(path, magicChars)
}

var osPathSeparator = string([]byte{os.PathSeparator})

func expandDir(pattern string) []string {
	pattern = strings.TrimRight(pattern, "/")
	if runtime.GOOS == "windows" {
		pattern = strings.TrimRight(pattern, osPathSeparator)
	}
	if pattern == "" {
		return []string{"/"}
	}
	if !hasMeta(pattern) {
		return []string{pattern}
	}
	dir, f := filepath.Split(pattern)
	if hasMeta(f) {
		matched, err := filepath.Glob(pattern)
		if err != nil {
			logger.Errorf("glob %s: %s", pattern, err)
			return []string{pattern}
		}
		return matched
	}
	var rs []string
	for _, p := range expandDir(dir) {
		rs = append(rs, filepath.Join(p, f))
	}
	return rs
}

type CacheManager interface {
	cache(key string, p *Page, force, dropCache bool)
	remove(key string, staging bool)
	load(key string) (ReadCloser, error)
	exist(key string) (string, bool)
	uploaded(key string, size int)
	stage(key string, data []byte, tierID uint8) (string, error)
	removeStage(key string) error
	stats() (int64, int64)
	usedMemory() int64
	isEmpty() bool
	getMetrics() *cacheManagerMetrics
}

func newCacheManager(config *Config, reg prometheus.Registerer, uploader func(key, path string, force bool) bool) CacheManager {
	getEnvs()
	metrics := newCacheManagerMetrics(reg)
	if config.CacheDir == "memory" || !config.CacheEnabled() {
		return newMemStore(config, metrics)
	}
	var dirs []string
	for _, d := range utils.SplitDir(config.CacheDir) {
		dd := expandDir(d)
		if config.AutoCreate {
			dirs = append(dirs, dd...)
		} else {
			for _, d := range dd {
				if fi, err := os.Stat(d); err == nil && fi.IsDir() {
					dirs = append(dirs, d)
				}
			}
		}
	}
	if len(dirs) == 0 {
		config.CacheSize = 100 << 20
		logger.Warnf("No cache dir existed, use memory cache instead, cache size: 100 MiB")
		return newMemStore(config, metrics)
	}
	sort.Strings(dirs)
	dirCacheSize := int64(config.CacheSize) / int64(len(dirs))
	dirCacheItems := config.CacheItems / int64(len(dirs))
	m := &cacheManager{
		consistentMap: consistenthash.New(100, murmur3.Sum32),
		storeMap:      make(map[string]*cacheStore, len(dirs)),
		stores:        make([]*cacheStore, len(dirs)),
		metrics:       metrics,
	}

	// 20% of buffer could be used for pending pages
	pendingPages := int(config.BufferSize) * 2 / 10 / config.BlockSize / len(dirs)
	for i, d := range dirs {
		store := newCacheStore(metrics, strings.TrimSpace(d)+string(filepath.Separator), dirCacheSize, dirCacheItems, pendingPages, config, uploader)
		m.stores[i] = store
		m.storeMap[store.id] = store
		m.consistentMap.Add(store.id)
	}
	go m.cleanup()
	return m
}

func (m *cacheManager) getMetrics() *cacheManagerMetrics {
	return m.metrics
}

func (m *cacheManager) cleanup() {
	for !m.isEmpty() {
		var ids []string
		m.Lock()
		for id, s := range m.storeMap {
			if s == nil || !s.available() {
				ids = append(ids, id)
			}
		}
		m.Unlock()
		for _, id := range ids {
			m.removeStore(id)
		}
		time.Sleep(time.Second)
	}
}

func (m *cacheManager) isEmpty() bool {
	return m.length() == 0
}

func (m *cacheManager) length() int {
	m.Lock()
	defer m.Unlock()
	return len(m.storeMap)
}

func (m *cacheManager) removeStore(id string) {
	m.Lock()
	m.consistentMap.Remove(id)
	var dir string
	if s := m.storeMap[id]; s != nil {
		dir = s.dir
	}
	delete(m.storeMap, id)
	for i, c := range m.stores {
		if c != nil && c.id == id {
			m.stores[i] = nil
		}
	}
	m.Unlock()
	logger.Errorf("cache dir `%s`(%s) is unavailable, removed", dir, id)
}

func (m *cacheManager) getStore(key string) *cacheStore {
	for {
		m.Lock()
		id := m.consistentMap.Get(key)
		s := m.storeMap[id]
		m.Unlock()
		if s == nil || s.available() {
			return s
		}
		m.removeStore(id)
	}
}

func (m *cacheManager) removeStage(key string) error {
	if s := m.getStore(key); s == nil {
		return errCacheDown
	} else {
		return s.removeStage(key)
	}
}

// Deprecated: use getStore instead
func (m *cacheManager) getStoreLegacy(key string) *cacheStore {
	return m.stores[legacyKeyHash(key)%uint32(len(m.stores))]
}

func (m *cacheManager) usedMemory() int64 {
	var used int64
	for _, s := range m.stores {
		if s != nil {
			used += s.usedMemory()
		}
	}
	return used
}

func (m *cacheManager) stats() (int64, int64) {
	var cnt, used int64
	for _, s := range m.stores {
		if s != nil {
			c, u := s.stats()
			cnt += c
			used += u
		}
	}
	return cnt, used
}

func (m *cacheManager) cache(key string, p *Page, force, dropCache bool) {
	store := m.getStore(key)
	if store != nil {
		store.cache(key, p, force, dropCache)
	}
}

type ReadCloser interface {
	// io.Reader
	io.ReaderAt
	io.Closer
}

func (m *cacheManager) load(key string) (ReadCloser, error) {
	store := m.getStore(key)
	if store == nil {
		return nil, errors.New("no available cache dir")
	}
	r, err := store.load(key)
	if err == errNotCached {
		legacy := m.getStoreLegacy(key)
		if legacy != store && legacy != nil {
			r, err = legacy.load(key)
		}
	}
	return r, err
}

func (m *cacheManager) exist(key string) (string, bool) {
	store := m.getStore(key)
	if store == nil {
		return "", false
	}
	loc := store.dir
	existed, err := store.exist(key)
	if err == errNotCached {
		legacy := m.getStoreLegacy(key)
		if legacy != store && legacy != nil {
			existed, _ = legacy.exist(key)
			loc = legacy.dir
		}
	}
	return loc, existed
}

func (m *cacheManager) remove(key string, staging bool) {
	store := m.getStore(key)
	if store != nil {
		store.remove(key, staging)
	}
}

func (m *cacheManager) stage(key string, data []byte, tierID uint8) (string, error) {
	store := m.getStore(key)
	if store != nil {
		return store.stage(key, data, tierID)
	}
	return "", errors.New("no available cache dir")
}

func (m *cacheManager) uploaded(key string, size int) {
	store := m.getStore(key)
	if store != nil {
		store.uploaded(key, size)
	}
}
