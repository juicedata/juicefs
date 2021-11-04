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
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

var (
	stagingDir = "rawstaging"
	cacheDir   = "raw"
)

type cacheItem struct {
	size  int32
	atime uint32
}

type pendingFile struct {
	key  string
	page *Page
}

type cacheStore struct {
	totalPages int64
	sync.Mutex
	dir       string
	mode      os.FileMode
	capacity  int64
	freeRatio float32
	pending   chan pendingFile
	pages     map[string]*Page

	used     int64
	keys     map[string]cacheItem
	scanned  bool
	full     bool
	uploader func(key, path string)
}

func newCacheStore(dir string, cacheSize int64, pendingPages int, config *Config, uploader func(key, path string)) *cacheStore {
	if config.CacheMode == 0 {
		config.CacheMode = 0600 // only owner can read/write cache
	}
	if config.FreeSpace == 0.0 {
		config.FreeSpace = 0.1 // 10%
	}
	c := &cacheStore{
		dir:       dir,
		mode:      config.CacheMode,
		capacity:  cacheSize,
		freeRatio: config.FreeSpace,
		keys:      make(map[string]cacheItem),
		pending:   make(chan pendingFile, pendingPages),
		pages:     make(map[string]*Page),
		uploader:  uploader,
	}
	c.createDir(c.dir)
	br, fr := c.curFreeRatio()
	if br < c.freeRatio || fr < c.freeRatio {
		logger.Warnf("not enough space (%d%%) or inodes (%d%%) for caching in %s: free ratio should be >= %d%%", int(br*100), int(fr*100), c.dir, int(c.freeRatio*100))
	}
	logger.Infof("Disk cache (%s): capacity (%d MB), free ratio (%d%%), max pending pages (%d)", c.dir, c.capacity>>20, int(c.freeRatio*100), pendingPages)
	go c.flush()
	go c.checkFreeSpace()
	go c.refreshCacheKeys()
	go c.scanStaging()
	return c
}

func (c *cacheStore) usedMemory() int64 {
	return atomic.LoadInt64(&c.totalPages)
}

func (cache *cacheStore) stats() (int64, int64) {
	cache.Lock()
	defer cache.Unlock()
	return int64(len(cache.pages) + len(cache.keys)), cache.used + cache.usedMemory()
}

func (cache *cacheStore) checkFreeSpace() {
	for {
		br, fr := cache.curFreeRatio()
		cache.full = br < cache.freeRatio/2 || fr < cache.freeRatio/2
		if br < cache.freeRatio || fr < cache.freeRatio {
			logger.Tracef("Cleanup cache when check free space (%s): free ratio (%d%%), space usage (%d%%), inodes usage (%d%%)", cache.dir, int(cache.freeRatio*100), int(br*100), int(fr*100))
			cache.Lock()
			cache.cleanup()
			cache.Unlock()

			br, fr = cache.curFreeRatio()
			if br < cache.freeRatio || fr < cache.freeRatio {
				cache.uploadStaging()
			}
		}
		time.Sleep(time.Second)
	}
}

func (cache *cacheStore) refreshCacheKeys() {
	for {
		cache.scanCached()
		time.Sleep(time.Minute * 5)
	}
}

func (cache *cacheStore) cache(key string, p *Page, force bool) {
	if cache.capacity == 0 {
		return
	}
	cache.Lock()
	defer cache.Unlock()
	if _, ok := cache.pages[key]; ok {
		return
	}
	p.Acquire()
	cache.pages[key] = p
	atomic.AddInt64(&cache.totalPages, int64(cap(p.Data)))
	select {
	case cache.pending <- pendingFile{key, p}:
	default:
		if force {
			cache.Unlock()
			cache.pending <- pendingFile{key, p}
			cache.Lock()
		} else {
			// does not have enough bandwidth to write it into disk, discard it
			logger.Debugf("Caching queue is full (%s), drop %s (%d bytes)", cache.dir, key, len(p.Data))
			cacheDrops.Add(1)
			delete(cache.pages, key)
			atomic.AddInt64(&cache.totalPages, -int64(cap(p.Data)))
			p.Release()
		}
	}
}

func (cache *cacheStore) curFreeRatio() (float32, float32) {
	total, free, files, ffree := getDiskUsage(cache.dir)
	return float32(free) / float32(total), float32(ffree) / float32(files)
}

func (cache *cacheStore) flushPage(path string, data []byte, sync bool) error {
	start := time.Now()
	cacheWrites.Add(1)
	cacheWriteBytes.Add(float64(len(data)))
	defer func() {
		cacheWriteHist.Observe(time.Since(start).Seconds())
	}()
	cache.createDir(filepath.Dir(path))
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE, cache.mode)
	if err != nil {
		logger.Infof("Can't create cache file %s: %s", tmp, err)
		return err
	}
	_, err = f.Write(data)
	if err != nil {
		logger.Warnf("Write to cache file %s failed: %s", tmp, err)
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if sync {
		err = f.Sync()
		if err != nil {
			logger.Warnf("sync stagging file %s failed: %s", tmp, err)
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	err = f.Close()
	if err != nil {
		logger.Warnf("Close cache file %s failed: %s", tmp, err)
		_ = os.Remove(tmp)
		return err
	}
	err = os.Rename(tmp, path)
	if err != nil {
		logger.Infof("Rename cache file %s -> %s failed: %s", tmp, path, err)
		_ = os.Remove(tmp)
	}
	return err
}

func (cache *cacheStore) createDir(dir string) {
	// who can read the cache, should be able to access the directories and add new file.
	readmode := cache.mode & 0444
	mode := cache.mode | (readmode >> 2) | (readmode >> 1)
	if st, err := os.Stat(dir); os.IsNotExist(err) {
		if filepath.Dir(dir) != dir {
			cache.createDir(filepath.Dir(dir))
		}
		_ = os.Mkdir(dir, mode)
		// umask may remove some permisssions
		_ = os.Chmod(dir, mode)
	} else if strings.HasPrefix(dir, cache.dir) && err == nil && st.Mode() != mode {
		changeMode(dir, st, mode)
	}
}

func (cache *cacheStore) remove(key string) {
	cache.Lock()
	path := cache.cachePath(key)
	if cache.keys[key].atime > 0 {
		cache.used -= int64(cache.keys[key].size + 4096)
		delete(cache.keys, key)
	} else if cache.scanned {
		path = "" // not existed
	}
	cache.Unlock()
	if path != "" {
		_ = os.Remove(path)
		_ = os.Remove(cache.stagePath(key))
	}
}

func (cache *cacheStore) load(key string) (ReadCloser, error) {
	cache.Lock()
	defer cache.Unlock()
	if p, ok := cache.pages[key]; ok {
		return NewPageReader(p), nil
	}
	if cache.scanned && cache.keys[key].atime == 0 {
		return nil, errors.New("not cached")
	}
	cache.Unlock()
	f, err := os.Open(cache.cachePath(key))
	cache.Lock()
	if err == nil {
		if it, ok := cache.keys[key]; ok {
			// update atime
			cache.keys[key] = cacheItem{it.size, uint32(time.Now().Unix())}
		}
	}
	return f, err
}

func (cache *cacheStore) cachePath(key string) string {
	return filepath.Join(cache.dir, cacheDir, key)
}

func (cache *cacheStore) stagePath(key string) string {
	return filepath.Join(cache.dir, stagingDir, key)
}

// flush cached block into disk
func (cache *cacheStore) flush() {
	for {
		w := <-cache.pending
		path := cache.cachePath(w.key)
		if cache.capacity > 0 && cache.flushPage(path, w.page.Data, false) == nil {
			cache.add(w.key, int32(len(w.page.Data)), uint32(time.Now().Unix()))
		}
		cache.Lock()
		delete(cache.pages, w.key)
		atomic.AddInt64(&cache.totalPages, -int64(cap(w.page.Data)))
		cache.Unlock()
		w.page.Release()
	}
}

func (cache *cacheStore) add(key string, size int32, atime uint32) {
	cache.Lock()
	defer cache.Unlock()
	it, ok := cache.keys[key]
	if ok && it.size > 0 {
		cache.used -= int64(it.size + 4096)
	}
	if atime == 0 {
		// update size of staging block
		cache.keys[key] = cacheItem{size, it.atime}
	} else {
		cache.keys[key] = cacheItem{size, atime}
	}
	if size > 0 {
		cache.used += int64(size + 4096)
	}

	if cache.used > cache.capacity {
		logger.Debugf("Cleanup cache when add new data (%s): %d blocks (%d MB)", cache.dir, len(cache.keys), cache.used>>20)
		cache.cleanup()
	}
}

func (cache *cacheStore) stage(key string, data []byte, keepCache bool) (string, error) {
	stagingPath := cache.stagePath(key)
	if cache.full {
		return stagingPath, errors.New("Space not enough on device")
	}
	err := cache.flushPage(stagingPath, data, false)
	if err == nil && cache.capacity > 0 && keepCache {
		path := cache.cachePath(key)
		cache.createDir(filepath.Dir(path))
		if err := os.Link(stagingPath, path); err == nil {
			cache.add(key, -int32(len(data)), uint32(time.Now().Unix()))
		} else {
			logger.Warnf("link %s to %s failed: %s", stagingPath, path, err)
		}
	}
	return stagingPath, err
}

func (cache *cacheStore) uploaded(key string, size int) {
	cache.add(key, int32(size), 0)
}

// locked
func (cache *cacheStore) cleanup() {
	if !cache.scanned {
		return
	}
	goal := cache.capacity * 95 / 100
	num := len(cache.keys) * 99 / 100
	// make sure we have enough free space after cleanup
	br, fr := cache.curFreeRatio()
	if br < cache.freeRatio {
		total, _, _, _ := getDiskUsage(cache.dir)
		toFree := int64(float32(total) * (cache.freeRatio - br))
		if toFree > cache.used {
			goal = 0
		} else if cache.used-toFree < goal {
			goal = cache.used - toFree
		}
	}
	if fr < cache.freeRatio {
		_, _, files, _ := getDiskUsage(cache.dir)
		toFree := int(float32(files) * (cache.freeRatio - fr))
		if toFree > len(cache.keys) {
			num = 0
		} else {
			num = len(cache.keys) - toFree
		}
	}

	var todel []string
	var freed int64
	var cnt int
	var lastKey string
	var lastValue cacheItem
	var now = uint32(time.Now().Unix())
	// for each two random keys, then compare the access time, evict the older one
	for key, value := range cache.keys {
		if value.size < 0 {
			continue // staging
		}
		if cnt == 0 || lastValue.atime > value.atime {
			lastKey = key
			lastValue = value
		}
		cnt++
		if cnt > 1 {
			delete(cache.keys, lastKey)
			freed += int64(lastValue.size + 4096)
			cache.used -= int64(lastValue.size + 4096)
			todel = append(todel, lastKey)
			logger.Debugf("remove %s from cache, age: %d", lastKey, now-lastValue.atime)
			cacheEvicts.Add(1)
			cnt = 0
			if len(cache.keys) < num && cache.used < goal {
				break
			}
		}
	}
	if len(todel) > 0 {
		logger.Debugf("cleanup cache (%s): %d blocks (%d MB), freed %d blocks (%d MB)", cache.dir, len(cache.keys), cache.used>>20, len(todel), freed>>20)
	}
	cache.Unlock()
	for _, key := range todel {
		os.Remove(cache.cachePath(key))
	}
	cache.Lock()
}

func (cache *cacheStore) uploadStaging() {
	cache.Lock()
	defer cache.Unlock()
	if !cache.scanned || cache.uploader == nil {
		return
	}

	var toFree int64
	br, fr := cache.curFreeRatio()
	if br < cache.freeRatio || fr < cache.freeRatio {
		total, _, _, _ := getDiskUsage(cache.dir)
		toFree = int64(float64(total)*float64(cache.freeRatio) - math.Min(float64(br), float64(fr)))
	}
	var cnt int
	var lastKey string
	var lastValue cacheItem
	// for each two random keys, then compare the access time, upload the older one
	for key, value := range cache.keys {
		if value.size > 0 {
			continue // read cache
		}

		// pick the bigger one if they were accessed within the same minute
		if cnt == 0 || lastValue.atime/60 > value.atime/60 ||
			lastValue.atime/60 == value.atime/60 && lastValue.size > value.size { // both size are < 0
			lastKey = key
			lastValue = value
		}
		cnt++
		if cnt > 1 {
			cache.Unlock()
			cache.uploader(lastKey, cache.stagePath(lastKey))
			logger.Debugf("upload %s, age: %d", lastKey, uint32(time.Now().Unix())-lastValue.atime)
			cache.Lock()
			// the size in keys should be updated
			toFree -= int64(-lastValue.size + 4096)
			cnt = 0
		}

		if toFree < 0 {
			break
		}
	}
	if cnt > 0 {
		cache.Unlock()
		cache.uploader(lastKey, cache.stagePath(lastKey))
		logger.Debugf("upload %s, age: %d", lastKey, uint32(time.Now().Unix())-lastValue.atime)
		cache.Lock()
	}
}

func (cache *cacheStore) scanCached() {
	cache.Lock()
	cache.used = 0
	cache.keys = make(map[string]cacheItem)
	cache.scanned = false
	cache.Unlock()

	var start = time.Now()
	var oneMinAgo = start.Add(-time.Minute)

	cachePrefix := filepath.Join(cache.dir, cacheDir)
	logger.Debugf("Scan %s to find cached blocks", cachePrefix)
	_ = filepath.Walk(cachePrefix, func(path string, fi os.FileInfo, err error) error {
		if fi != nil {
			if fi.IsDir() || strings.HasSuffix(path, ".tmp") {
				if fi.ModTime().Before(oneMinAgo) {
					// try to remove empty directory
					if os.Remove(path) == nil {
						logger.Debugf("Remove empty directory: %s", path)
					}
				}
			} else {
				key := path[len(cachePrefix)+1:]
				if runtime.GOOS == "windows" {
					key = strings.ReplaceAll(key, "\\", "/")
				}
				atime := uint32(getAtime(fi).Unix())
				if getNlink(fi) > 1 {
					cache.add(key, -int32(fi.Size()), atime)
				} else {
					cache.add(key, int32(fi.Size()), atime)
				}
			}
		}
		return nil
	})

	cache.Lock()
	cache.scanned = true
	logger.Debugf("Found %d cached blocks (%d bytes) in %s with %s", len(cache.keys), cache.used, cache.dir, time.Since(start))
	cache.Unlock()
}

func (cache *cacheStore) scanStaging() {
	if cache.uploader == nil {
		return
	}

	var start = time.Now()
	var oneMinAgo = start.Add(-time.Minute)
	var count int
	stagingPrefix := filepath.Join(cache.dir, stagingDir)
	logger.Debugf("Scan %s to find staging blocks", stagingPrefix)
	_ = filepath.Walk(stagingPrefix, func(path string, fi os.FileInfo, err error) error {
		if fi != nil {
			if fi.IsDir() || strings.HasSuffix(path, ".tmp") {
				if fi.ModTime().Before(oneMinAgo) {
					// try to remove empty directory
					if os.Remove(path) == nil {
						logger.Debugf("Remove empty directory: %s", path)
					}
				}
			} else {
				logger.Debugf("Found staging block: %s", path)
				key := path[len(stagingPrefix)+1:]
				if runtime.GOOS == "windows" {
					key = strings.ReplaceAll(key, "\\", "/")
				}
				cache.uploader(key, path)
				count++
			}
		}
		return nil
	})
	if count > 0 {
		logger.Infof("Found %d staging blocks (%d bytes) in %s with %s", count, cache.used, cache.dir, time.Since(start))
	}
}

type cacheManager struct {
	stores []*cacheStore
}

func keyHash(s string) uint32 {
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

func expandDir(pattern string) []string {
	for strings.HasSuffix(pattern, "/") {
		pattern = pattern[:len(pattern)-1]
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
	cache(key string, p *Page, force bool)
	remove(key string)
	load(key string) (ReadCloser, error)
	uploaded(key string, size int)
	stage(key string, data []byte, keepCache bool) (string, error)
	stagePath(key string) string
	stats() (int64, int64)
	usedMemory() int64
}

func newCacheManager(config *Config, uploader func(key, path string)) CacheManager {
	if config.CacheDir == "memory" || config.CacheSize == 0 {
		return newMemStore(config)
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
		logger.Warnf("No cache dir existed")
		return newMemStore(config)
	}
	sort.Strings(dirs)
	dirCacheSize := config.CacheSize << 20
	dirCacheSize /= int64(len(dirs))
	m := &cacheManager{
		stores: make([]*cacheStore, len(dirs)),
	}
	// 20% of buffer could be used for pending pages
	pendingPages := config.BufferSize * 2 / 10 / config.BlockSize / len(dirs)
	for i, d := range dirs {
		m.stores[i] = newCacheStore(strings.TrimSpace(d)+string(filepath.Separator), dirCacheSize, pendingPages, config, uploader)
	}
	return m
}

func (m *cacheManager) getStore(key string) *cacheStore {
	return m.stores[keyHash(key)%uint32(len(m.stores))]
}

func (m *cacheManager) usedMemory() int64 {
	var used int64
	for _, s := range m.stores {
		used += s.usedMemory()
	}
	return used
}

func (m *cacheManager) stats() (int64, int64) {
	var cnt, used int64
	for _, s := range m.stores {
		c, u := s.stats()
		cnt += c
		used += u
	}
	return cnt, used
}

func (m *cacheManager) cache(key string, p *Page, force bool) {
	m.getStore(key).cache(key, p, force)
}

type ReadCloser interface {
	io.Reader
	io.ReaderAt
	io.Closer
}

func (m *cacheManager) load(key string) (ReadCloser, error) {
	return m.getStore(key).load(key)
}

func (m *cacheManager) remove(key string) {
	m.getStore(key).remove(key)
}

func (m *cacheManager) stage(key string, data []byte, keepCache bool) (string, error) {
	return m.getStore(key).stage(key, data, keepCache)
}

func (m *cacheManager) stagePath(key string) string {
	return m.getStore(key).stagePath(key)
}

func (m *cacheManager) uploaded(key string, size int) {
	m.getStore(key).uploaded(key, size)
}
