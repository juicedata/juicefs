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
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
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
	dir          string
	mode         os.FileMode
	capacity     int64
	freeRatio    float32
	scanInterval time.Duration
	pending      chan pendingFile
	pages        map[string]*Page
	m            *cacheManager

	used     int64
	keys     map[string]cacheItem
	scanned  bool
	full     bool
	checksum string // checksum level
	uploader func(key, path string, force bool) bool
}

func newCacheStore(m *cacheManager, dir string, cacheSize int64, pendingPages int, config *Config, uploader func(key, path string, force bool) bool) *cacheStore {
	if config.CacheMode == 0 {
		config.CacheMode = 0600 // only owner can read/write cache
	}
	if config.FreeSpace == 0.0 {
		config.FreeSpace = 0.1 // 10%
	}
	c := &cacheStore{
		m:            m,
		dir:          dir,
		mode:         config.CacheMode,
		capacity:     cacheSize,
		freeRatio:    config.FreeSpace,
		checksum:     config.CacheChecksum,
		scanInterval: config.CacheScanInterval,
		keys:         make(map[string]cacheItem),
		pending:      make(chan pendingFile, pendingPages),
		pages:        make(map[string]*Page),
		uploader:     uploader,
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
	cache.scanCached()
	if cache.scanInterval > 0 {
		for {
			time.Sleep(cache.scanInterval)
			cache.scanCached()
		}
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
			cache.m.cacheDrops.Add(1)
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

func (cache *cacheStore) flushPage(path string, data []byte) (err error) {
	start := time.Now()
	cache.m.cacheWrites.Add(1)
	cache.m.cacheWriteBytes.Add(float64(len(data)))
	defer func() {
		cache.m.cacheWriteHist.Observe(time.Since(start).Seconds())
	}()
	cache.createDir(filepath.Dir(path))
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE, cache.mode)
	if err != nil {
		logger.Warnf("Can't create cache file %s: %s", tmp, err)
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if _, err = f.Write(data); err != nil {
		logger.Warnf("Write to cache file %s failed: %s", tmp, err)
		_ = f.Close()
		return
	}
	if cache.checksum != CsNone {
		if _, err = f.Write(checksum(data)); err != nil {
			logger.Warnf("Write checksum to cache file %s failed: %s", tmp, err)
			_ = f.Close()
			return
		}
	}
	if err = f.Close(); err != nil {
		logger.Warnf("Close cache file %s failed: %s", tmp, err)
		return
	}
	if err = os.Rename(tmp, path); err != nil {
		logger.Warnf("Rename cache file %s -> %s failed: %s", tmp, path, err)
	}
	return
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
	delete(cache.pages, key)
	path := cache.cachePath(key)
	if it, ok := cache.keys[key]; ok {
		if it.size > 0 {
			cache.used -= int64(it.size + 4096)
		}
		delete(cache.keys, key)
	} else if cache.scanned {
		path = "" // not existed
	}
	cache.Unlock()
	if path != "" {
		_ = os.Remove(path)
		stagingPath := cache.stagePath(key)
		if fi, err := os.Stat(stagingPath); err == nil {
			size := fi.Size()
			if err = os.Remove(stagingPath); err == nil {
				cache.m.stageBlocks.Sub(1)
				cache.m.stageBlockBytes.Sub(float64(size))
			}
		}
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
	f, err := openCacheFile(cache.cachePath(key), parseObjOrigSize(key), cache.checksum)
	cache.Lock()
	if err == nil {
		if it, ok := cache.keys[key]; ok {
			// update atime
			cache.keys[key] = cacheItem{it.size, uint32(time.Now().Unix())}
		}
	} else if it, ok := cache.keys[key]; ok {
		if it.size > 0 {
			cache.used -= int64(it.size + 4096)
		}
		delete(cache.keys, key)
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
		if cache.capacity > 0 && cache.flushPage(path, w.page.Data) == nil {
			cache.add(w.key, int32(len(w.page.Data)), uint32(time.Now().Unix()))
		}
		cache.Lock()
		_, ok := cache.pages[w.key]
		delete(cache.pages, w.key)
		atomic.AddInt64(&cache.totalPages, -int64(cap(w.page.Data)))
		cache.Unlock()
		w.page.Release()
		if !ok {
			cache.remove(w.key)
		}
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
	err := cache.flushPage(stagingPath, data)
	if err == nil {
		cache.m.stageBlocks.Add(1)
		cache.m.stageBlockBytes.Add(float64(len(data)))
		if cache.capacity > 0 && keepCache {
			path := cache.cachePath(key)
			cache.createDir(filepath.Dir(path))
			if err := os.Link(stagingPath, path); err == nil {
				cache.add(key, -int32(len(data)), uint32(time.Now().Unix()))
			} else {
				logger.Warnf("link %s to %s failed: %s", stagingPath, path, err)
			}
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
			cache.m.cacheEvicts.Add(1)
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
		_ = os.Remove(cache.cachePath(key))
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
			if !cache.uploader(lastKey, cache.stagePath(lastKey), true) {
				logger.Warnf("Upload list is too full")
				cache.Lock()
				return
			}
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
		if cache.uploader(lastKey, cache.stagePath(lastKey), true) {
			logger.Debugf("upload %s, age: %d", lastKey, uint32(time.Now().Unix())-lastValue.atime)
		}
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

var pathReg, _ = regexp.Compile(`^chunks/\d+/\d+/\d+_\d+_\d+$`)

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
				key := path[len(stagingPrefix)+1:]
				if runtime.GOOS == "windows" {
					key = strings.ReplaceAll(key, "\\", "/")
				}
				if !pathReg.MatchString(key) {
					logger.Warnf("Ignore invalid file in staging: %s", path)
					return nil
				}
				if parseObjOrigSize(key) == 0 {
					logger.Warnf("Ignore file with zero size: %s", path)
					return nil
				}
				logger.Debugf("Found staging block: %s", path)
				cache.m.stageBlocks.Add(1)
				cache.m.stageBlockBytes.Add(float64(fi.Size()))
				cache.uploader(key, path, false)
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

	cacheDrops      prometheus.Counter
	cacheWrites     prometheus.Counter
	cacheEvicts     prometheus.Counter
	cacheWriteBytes prometheus.Counter
	cacheWriteHist  prometheus.Histogram
	stageBlocks     prometheus.Gauge
	stageBlockBytes prometheus.Gauge
	stageBlockDelay prometheus.Counter
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
	cache(key string, p *Page, force bool)
	remove(key string)
	load(key string) (ReadCloser, error)
	uploaded(key string, size int)
	stage(key string, data []byte, keepCache bool) (string, error)
	stagePath(key string) string
	stats() (int64, int64)
	usedMemory() int64
}

func newCacheManager(config *Config, reg prometheus.Registerer, uploader func(key, path string, force bool) bool) CacheManager {
	if config.CacheDir == "memory" || config.CacheSize == 0 {
		return newMemStore(config, reg)
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
		return newMemStore(config, reg)
	}
	sort.Strings(dirs)
	dirCacheSize := config.CacheSize << 20
	dirCacheSize /= int64(len(dirs))
	m := &cacheManager{
		stores: make([]*cacheStore, len(dirs)),

		cacheDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "blockcache_drops",
			Help: "dropped block",
		}),
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
		cacheWriteHist: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "blockcache_write_hist_seconds",
			Help:    "write cached block latency distribution",
			Buckets: prometheus.ExponentialBuckets(0.00001, 2, 20),
		}),
		stageBlocks: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "staging_blocks",
			Help: "Number of blocks in the staging path.",
		}),
		stageBlockBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "staging_block_bytes",
			Help: "Total bytes of blocks in the staging path.",
		}),
		stageBlockDelay: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "staging_block_delay_seconds",
			Help: "Total seconds of delay for staging blocks",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.cacheWrites)
		reg.MustRegister(m.cacheWriteBytes)
		reg.MustRegister(m.cacheDrops)
		reg.MustRegister(m.cacheEvicts)
		reg.MustRegister(m.cacheWriteHist)
		reg.MustRegister(m.stageBlocks)
		reg.MustRegister(m.stageBlockBytes)
		reg.MustRegister(m.stageBlockDelay)
	}

	// 20% of buffer could be used for pending pages
	pendingPages := config.BufferSize * 2 / 10 / config.BlockSize / len(dirs)
	for i, d := range dirs {
		m.stores[i] = newCacheStore(m, strings.TrimSpace(d)+string(filepath.Separator), dirCacheSize, pendingPages, config, uploader)
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
	// io.Reader
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

/* --- Checksum --- */
const (
	CsNone   = "none"
	CsFull   = "full"
	CsShrink = "shrink"
	CsExtend = "extend"

	csBlock = 32 << 10
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

type cacheFile struct {
	*os.File
	length  int // length of data
	csLevel string
}

// Calculate 32-bits checksum for every 32 KiB data, so 512 Bytes for 4 MiB in total
func checksum(data []byte) []byte {
	length := len(data)
	buf := utils.NewBuffer(uint32((length-1)/csBlock+1) * 4)
	for start, end := 0, 0; start < length; start = end {
		end = start + csBlock
		if end > length {
			end = length
		}
		sum := crc32.Checksum(data[start:end], crc32c)
		buf.Put32(sum)
	}
	return buf.Bytes()
}

func openCacheFile(name string, length int, level string) (*cacheFile, error) {
	fp, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := fp.Stat()
	if err != nil {
		_ = fp.Close()
		return nil, err
	}
	checksumLength := ((length-1)/csBlock + 1) * 4
	switch fi.Size() - int64(length) {
	case 0:
		return &cacheFile{fp, length, CsNone}, nil
	case int64(checksumLength):
		return &cacheFile{fp, length, level}, nil
	default:
		_ = fp.Close()
		return nil, fmt.Errorf("invalid file size %d, data length %d", fi.Size(), length)
	}
}

func (cf *cacheFile) ReadAt(b []byte, off int64) (n int, err error) {
	logger.Tracef("CacheFile length %d level %s, readat off %d buffer size %d", cf.length, cf.csLevel, off, len(b))
	defer func() {
		logger.Tracef("CacheFile readat returns n %d err %s", n, err)
	}()
	if cf.csLevel == CsNone || cf.csLevel == CsFull && (off != 0 || len(b) != cf.length) {
		return cf.File.ReadAt(b, off)
	}
	var rb = b     // read buffer
	var roff = off // read offset
	if cf.csLevel == CsExtend {
		roff = off / csBlock * csBlock
		rend := int(off) + len(b)
		if rend%csBlock != 0 {
			rend = (rend/csBlock + 1) * csBlock
			if rend > cf.length {
				rend = cf.length
			}
		}
		if size := rend - int(roff); size != len(b) {
			p := NewOffPage(size)
			rb = p.Data
			defer func() {
				if err == nil {
					n = copy(b, rb[off-roff:])
				} else {
					n = 0
				}
				p.Release()
			}()
		}
	}
	if n, err = cf.File.ReadAt(rb, roff); err != nil {
		return
	}

	ioff := int(roff) / csBlock // index offset
	if cf.csLevel == CsShrink {
		if roff%csBlock != 0 {
			if o := csBlock - int(roff)%csBlock; len(rb) <= o {
				return
			} else {
				rb = rb[o:]
				ioff += 1
			}
		}
		if end := int(roff) + n; end != cf.length && end%csBlock != 0 {
			if len(rb) <= end%csBlock {
				return
			}
			rb = rb[:len(rb)-end%csBlock]
		}
	}
	// now rb contains the data to check
	length := len(rb)
	buf := utils.NewBuffer(uint32((length-1)/csBlock+1) * 4)
	if _, err = cf.File.ReadAt(buf.Bytes(), int64(cf.length+ioff*4)); err != nil {
		logger.Warnf("Read checksum of data length %d checksum offset %d: %s", length, cf.length+ioff*4, err)
		return
	}
	for start, end := 0, 0; start < length; start = end {
		end = start + csBlock
		if end > length {
			end = length
		}
		sum := crc32.Checksum(rb[start:end], crc32c)
		expect := buf.Get32()
		logger.Debugf("Cache file read data start %d end %d checksum %d, expected %d", start, end, sum, expect)
		if sum != expect {
			err = fmt.Errorf("data checksum %d != expect %d", sum, expect)
			break
		}
	}
	return
}
