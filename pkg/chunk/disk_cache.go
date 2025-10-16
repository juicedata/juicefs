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
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/davies/groupcache/consistenthash"
	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/murmur3"
)

var (
	stagingDir          = "rawstaging"
	cacheDir            = "raw"
	maxIODur            = time.Second * 30
	stagingBlocks       atomic.Int64
	errNotCached        = errors.New("not cached")
	errStageFull        = errors.New("space not enough on device")
	errStageConcurrency = errors.New("concurrent staging limit reached")
)

type cacheKey struct {
	id   uint64
	indx uint32
	size uint32
}

func (k cacheKey) String() string { return fmt.Sprintf("%d_%d_%d", k.id, k.indx, k.size) }

type pendingFile struct {
	key       string
	page      *Page
	dropCache bool
}

type cacheStore struct {
	id         string
	totalPages int64
	sync.Mutex
	dir           string
	mode          os.FileMode
	maxStageWrite int
	capacity      int64
	maxItems      int64
	freeRatio     float32
	hashPrefix    bool
	scanInterval  time.Duration
	cacheExpire   time.Duration
	pending       chan pendingFile
	pages         map[string]*Page
	m             *cacheManagerMetrics

	used      int64
	keys      KeyIndex
	scanned   bool
	stageFull bool
	rawFull   bool
	checksum  string // checksum level
	uploader  func(key, path string, force bool) bool

	opTs map[time.Duration]func() error
	opMu sync.Mutex

	state     dcState
	stateLock sync.Mutex
}

func newCacheStore(m *cacheManagerMetrics, dir string, cacheSize, maxItems int64, pendingPages int, config *Config, uploader func(key, path string, force bool) bool) *cacheStore {
	if config.CacheMode == 0 {
		config.CacheMode = 0600 // only owner can read/write cache
	}
	if config.FreeSpace == 0.0 {
		config.FreeSpace = 0.1 // 10%
	}
	keyIndex, err := NewKeyIndex(config)
	if err != nil {
		logger.Warnf("%s, fallback to %s", err, Eviction2Random)
		config.CacheEviction = Eviction2Random
		keyIndex, _ = NewKeyIndex(config)
	}
	c := &cacheStore{
		m:             m,
		dir:           dir,
		mode:          config.CacheMode,
		capacity:      cacheSize,
		maxItems:      maxItems,
		maxStageWrite: config.MaxStageWrite,
		freeRatio:     config.FreeSpace,
		checksum:      config.CacheChecksum,
		hashPrefix:    config.HashPrefix,
		scanInterval:  config.CacheScanInterval,
		cacheExpire:   config.CacheExpire,
		keys:          keyIndex,
		pending:       make(chan pendingFile, pendingPages),
		pages:         make(map[string]*Page),
		uploader:      uploader,
		opTs:          make(map[time.Duration]func() error),
	}
	c.stateLock = sync.Mutex{}
	if config.Writeback {
		c.state = newDCState(dcUnchanged, c)
	} else {
		c.state = newDCState(dcNormal, c)
	}

	c.createDir(c.dir)
	usage := c.curFreeRatio()
	if usage.br < c.freeRatio || usage.fr < c.freeRatio {
		logger.Warnf("not enough space (%d%%) or inodes (%d%%) for caching in %s: free ratio should be >= %d%%", int(usage.br*100), int(usage.fr*100), c.dir, int(c.freeRatio*100))
	}
	logger.Infof("Disk cache (%s): used ratio - [space %s%%, inode %s%%]",
		c.dir, humanize.FtoaWithDigits(float64((1-usage.br)*100), 1), humanize.FtoaWithDigits(float64((1-usage.fr)*100), 1))

	c.setLimitByFreeRatio(usage, c.freeRatio)

	c.createLockFile()
	go c.checkLockFile()
	go c.flush()
	go c.checkFreeSpace()
	if c.cacheExpire > 0 {
		go c.cleanupExpire()
	}
	go c.refreshCacheKeys()
	go c.scanStaging()
	go c.checkTimeout()
	return c
}

func (cache *cacheStore) setLimitByFreeRatio(usage DiskFreeRatio, freeRatio float32) {
	sizeLimit := int64(float64(1-freeRatio) * float64(usage.spaceCap))
	inodeLimit := int64(float64(1-freeRatio) * float64(usage.inodeCap))
	if sizeLimit < cache.capacity {
		limit := cache.capacity
		cache.capacity = sizeLimit
		logger.Infof("Adjusted cache capacity based on freeratio: from %d to %d bytes", limit, cache.capacity)
	}
	if inodeLimit < cache.maxItems || cache.maxItems == 0 {
		limit := cache.maxItems
		cache.maxItems = inodeLimit

		maxItems := "unlimited"
		if cache.maxItems != 0 {
			maxItems = strconv.FormatInt(cache.maxItems, 10)
		}
		logger.Infof("Adjusted max items based on freeratio: from %d to %s items", limit, maxItems)
	}
}

func (cache *cacheStore) lockFilePath() string {
	return filepath.Join(cache.dir, ".lock")
}

func (cache *cacheStore) createLockFile() {
	lockfile := cache.lockFilePath()
	err := cache.checkErr(func() error {
		f, err := os.OpenFile(lockfile, os.O_CREATE|os.O_RDWR, 0666)
		if err != nil {
			return fmt.Errorf("open lock file %s: %w", lockfile, err)
		}
		defer f.Close()
		rawId, err := io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("read lock file %s: %w", lockfile, err)
		}
		if len(rawId) > 0 {
			cache.id = string(rawId)
		} else {
			cache.id = uuid.New().String()
			_, err = f.Write([]byte(cache.id))
			if err != nil {
				return fmt.Errorf("write lock file %s: %w", lockfile, err)
			}
		}
		return nil
	})
	if err != nil {
		logger.Warnf("create lock file %s: %s", lockfile, err)
	}
}

func (cache *cacheStore) checkLockFile() {
	lockfile := cache.lockFilePath()
	for cache.available() {
		time.Sleep(time.Second * 10)
		if err := cache.statFile(lockfile); err != nil && os.IsNotExist(err) {
			logger.Infof("lockfile %s is lost, cache device maybe broken", lockfile)
			if inRootVolume(cache.dir) && cache.freeRatio < 0.2 {
				logger.Infof("cache directory %s is in root volume, keep 20%% space free", cache.dir)
				cache.freeRatio = 0.2
			}
		}
	}
}

func (c *cacheStore) available() bool {
	return c.state.state() != dcDown
}

func (c *cacheStore) enabled() bool {
	return c.capacity > 0
}

func (c *cacheStore) full() bool {
	return c.used > c.capacity || (c.maxItems != 0 && int64(c.keys.len()) > c.maxItems)
}

func (cache *cacheStore) checkErr(f func() error) error {
	if !cache.available() {
		return errCacheDown
	}
	cache.state.beforeCacheOp()
	defer cache.state.afterCacheOp()
	if err := cache.state.checkCacheOp(); err != nil {
		return err
	}

	start := utils.Clock()
	cache.opMu.Lock()
	cache.opTs[start] = f
	cache.opMu.Unlock()
	err := f()
	cache.opMu.Lock()
	delete(cache.opTs, start)
	cache.opMu.Unlock()

	if err != nil {
		if errors.Is(err, syscall.EIO) || errors.Is(err, utils.ErrFuncTimeout) {
			logger.Errorf("cache store is unavailable: %s", err)
			cache.state.onIOErr()
		}
	} else {
		cache.state.onIOSucc()
	}
	return err
}

func getFunctionName(f interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

func (c *cacheStore) checkTimeout() {
	for c.available() {
		now := utils.Clock()
		cutOff := now - maxIODur
		c.opMu.Lock()
		for ts := range c.opTs {
			if ts < cutOff {
				logger.Warnf("IO operation %s on %s is timeout after %s, ", getFunctionName(c.opTs[ts]), c.dir, now-ts)
				c.state.onIOErr()
				delete(c.opTs, ts)
			}
		}
		c.opMu.Unlock()
		time.Sleep(time.Second)
	}
}

func (c *cacheStore) statFile(path string) error {
	return c.checkErr(func() error {
		_, err := os.Stat(path)
		return err
	})
}

func (cache *cacheStore) removeFile(path string) error {
	return cache.checkErr(func() error {
		return os.Remove(path)
	})
}

func (cache *cacheStore) renameFile(oldpath, newpath string) error {
	return cache.checkErr(func() error {
		return os.Rename(oldpath, newpath)
	})
}

func (cache *cacheStore) writeFile(f *os.File, data []byte) error {
	return cache.checkErr(func() error {
		_, err := f.Write(data)
		return err
	})
}

func (cache *cacheStore) closeFile(f *os.File) error {
	return cache.checkErr(func() error {
		return f.Close()
	})
}

func (cache *cacheStore) usedMemory() int64 {
	return atomic.LoadInt64(&cache.totalPages)
}

func (cache *cacheStore) stats() (int64, int64) {
	cache.Lock()
	defer cache.Unlock()
	return int64(len(cache.pages) + cache.keys.len()), cache.used + cache.usedMemory()
}

func (cache *cacheStore) checkFreeSpace() {
	for cache.available() {
		usage := cache.curFreeRatio()
		cache.stageFull = usage.br < cache.freeRatio/2 || usage.fr < cache.freeRatio/2
		cache.rawFull = usage.br < cache.freeRatio || usage.fr < cache.freeRatio
		if cache.rawFull && cache.keys.name() != EvictionNone {
			logger.Tracef("Cleanup cache when check free space (%s): free ratio (%d%%), space usage (%d%%), inodes usage (%d%%)", cache.dir, int(cache.freeRatio*100), int(usage.br*100), int(usage.fr*100))
			cache.Lock()
			cache.cleanupFull()
			cache.Unlock()
			usage = cache.curFreeRatio()
			cache.rawFull = usage.br < cache.freeRatio || usage.fr < cache.freeRatio
		}
		if cache.rawFull {
			cache.uploadStaging()
		}
		time.Sleep(time.Second)
	}
	logger.Infof("stop checkFreeSpace at %s", cache.dir)
}

func (cache *cacheStore) cleanupExpire() {
	var todel []cacheKey
	var interval = time.Minute
	if cache.cacheExpire < time.Minute {
		interval = cache.cacheExpire
	}
	for {
		var freed int64
		var cnt, deleted int
		var cutoff = uint32(time.Now().Unix()) - uint32(cache.cacheExpire/time.Second)
		cache.Lock()
		for k, v := range cache.keys.randomIter() {
			cnt++
			if cnt > 1e3 {
				break
			}
			if v.size < 0 {
				continue // staging
			}
			if v.atime < cutoff {
				if cache.keys.remove(k, false) != nil {
					deleted++
					freed += int64(v.size + 4096)
					cache.used -= int64(v.size + 4096)
					todel = append(todel, k)
					cache.m.cacheEvicts.Add(1)
				}
			}
		}
		if len(todel) > 0 {
			logger.Debugf("cleanup expired cache (%s): %d blocks (%s), expired %d blocks (%s)", cache.dir, cache.keys.len(), humanize.IBytes(uint64(cache.used)), len(todel), humanize.IBytes(uint64(freed)))
		}
		cache.Unlock()
		for _, k := range todel {
			if !cache.available() {
				break
			}
			_ = cache.removeFile(cache.cachePath(cache.getPathFromKey(k)))
		}
		todel = todel[:0]
		time.Sleep(interval / 1000 * time.Duration((cnt+1-deleted)*1000/(cnt+1)))
	}
}

func (cache *cacheStore) refreshCacheKeys() {
	if cache.scanInterval < 0 {
		return
	}
	cache.scanCached()
	if cache.scanInterval > 0 {
		for {
			time.Sleep(cache.scanInterval)
			cache.scanCached()
		}
	}
}

func (cache *cacheStore) removeStage(key string) error {
	var err error
	if err = cache.removeFile(cache.stagePath(key)); err == nil {
		cache.m.stageBlocks.Sub(1)
		cache.m.stageBlockBytes.Sub(float64(parseObjOrigSize(key)))
	}
	// ignore ENOENT error
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

func (cache *cacheStore) cache(key string, p *Page, force, dropCache bool) {
	if !cache.enabled() {
		return
	}
	if cache.rawFull && cache.keys.name() == EvictionNone {
		logger.Debugf("Caching directory is full (%s), drop %s (%d bytes)", cache.dir, key, len(p.Data))
		cache.m.cacheDrops.Add(1)
		return
	}
	cache.Lock()
	defer cache.Unlock()
	if _, ok := cache.pages[key]; ok {
		return
	}
	k := cache.getCacheKey(key)
	if cache.keys.get(k) != nil {
		return
	}
	p.Acquire()
	cache.pages[key] = p
	atomic.AddInt64(&cache.totalPages, int64(cap(p.Data)))
	select {
	case cache.pending <- pendingFile{key, p, dropCache}:
	default:
		if force {
			cache.Unlock()
			cache.pending <- pendingFile{key, p, dropCache}
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

type DiskFreeRatio struct {
	br       float32
	fr       float32
	spaceCap uint64
	inodeCap uint64
}

// caller should not hold cache lock
func (cache *cacheStore) curFreeRatio() DiskFreeRatio {
	var total, free, files, ffree uint64
	_ = cache.checkErr(func() error {
		total, free, files, ffree = getDiskUsage(cache.dir)
		return nil
	})
	usage := DiskFreeRatio{
		spaceCap: total,
		inodeCap: files,
	}
	if total != 0 {
		usage.br = float32(free) / float32(total)
	}
	if files != 0 {
		usage.fr = float32(ffree) / float32(files)
	}
	return usage
}

func (cache *cacheStore) flushPage(path string, data []byte, dropCache bool) (err error) {
	if !cache.available() {
		return errCacheDown
	}

	start := time.Now()
	cache.m.cacheWrites.Add(1)
	cache.m.cacheWriteBytes.Add(float64(len(data)))
	defer func() {
		cache.m.cacheWriteHist.Observe(time.Since(start).Seconds())
	}()
	cache.createDir(filepath.Dir(path))
	tmp := path + ".tmp"

	var f *os.File
	err = cache.checkErr(func() error {
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE, cache.mode)
		return err
	})
	if err != nil {
		logger.Warnf("Can't create cache file %s: %s", tmp, err)
		return err
	}

	defer func() {
		if err != nil {
			_ = cache.removeFile(tmp)
		}
	}()

	if err = cache.writeFile(f, data); err != nil {
		logger.Warnf("Write to cache file %s failed: %s", tmp, err)
		_ = f.Close()
		return
	}
	if cache.checksum != CsNone {
		if err = cache.writeFile(f, checksum(data)); err != nil {
			logger.Warnf("Write checksum to cache file %s failed: %s", tmp, err)
			_ = f.Close()
			return
		}
	}
	if dropCache {
		dropOSCache(f)
	}
	if err = cache.closeFile(f); err != nil {
		logger.Warnf("Close cache file %s failed: %s", tmp, err)
		return
	}
	if err = cache.renameFile(tmp, path); err != nil {
		logger.Warnf("Rename cache file %s -> %s failed: %s", tmp, path, err)
	}
	return
}

func (cache *cacheStore) createDir(dir string) {
	// who can read the cache, should be able to access the directories and add new file.
	_ = cache.checkErr(func() error {
		readmode := cache.mode & 0444
		mode := cache.mode | (readmode >> 2) | (readmode >> 1)
		var st os.FileInfo
		var err error
		dir = filepath.Clean(dir) // `CacheManager` appends "/" to dir, remove it so that following `filepath.Dir` returns the parent dir
		if st, err = os.Stat(dir); os.IsNotExist(err) {
			if filepath.Dir(dir) != dir {
				cache.createDir(filepath.Dir(dir))
			}
			_ = os.Mkdir(dir, mode)
			// umask may remove some permissions
			return os.Chmod(dir, mode)
		} else if strings.HasPrefix(dir, cache.dir) && err == nil && st.Mode().Perm() != mode.Perm() { // check permission only
			changeMode(dir, st, mode)
		}
		return err
	})
}

func (cache *cacheStore) getCacheKey(key string) cacheKey {
	p := strings.LastIndexByte(key, '/')
	p++
	var k cacheKey
	l := len(key)
	for p < l {
		if key[p] == '_' {
			p++
			break
		}
		k.id *= 10
		k.id += uint64(key[p] - '0')
		p++
	}
	for p < l {
		if key[p] == '_' {
			p++
			break
		}
		k.indx *= 10
		k.indx += uint32(key[p] - '0')
		p++
	}
	for p < l {
		k.size *= 10
		k.size += uint32(key[p] - '0')
		p++
	}
	return k
}

func (cache *cacheStore) getPathFromKey(k cacheKey) string {
	if cache.hashPrefix {
		return fmt.Sprintf("chunks/%02X/%v/%v_%v_%v", k.id%256, k.id/1000/1000, k.id, k.indx, k.size)
	} else {
		return fmt.Sprintf("chunks/%v/%v/%v_%v_%v", k.id/1000/1000, k.id/1000, k.id, k.indx, k.size)
	}
}

func (cache *cacheStore) remove(key string, staging bool) {
	cache.Lock()
	delete(cache.pages, key)
	path := cache.cachePath(key)
	k := cache.getCacheKey(key)
	if it := cache.keys.remove(k, staging); it != nil {
		if it.size > 0 {
			cache.used -= int64(it.size + 4096)
		}
	} else if cache.scanned || !staging {
		path = "" // not existed or staging block
	}
	cache.Unlock()

	if path != "" {
		if err := cache.removeFile(path); err != nil && !os.IsNotExist(err) {
			logger.Warnf("remove %s failed: %s", path, err)
		}
		if staging {
			if err := cache.removeStage(key); err != nil && !os.IsNotExist(err) {
				logger.Warnf("remove stage %s failed: %s", cache.stagePath(key), err)
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
	k := cache.getCacheKey(key)
	if cache.scanned && cache.keys.get(k) == nil {
		return nil, errNotCached
	}
	cache.Unlock()

	var f *cacheFile
	var err error
	err = cache.checkErr(func() error {
		f, err = openCacheFile(cache.cachePath(key), parseObjOrigSize(key), cache.checksum)
		if err != nil && !os.IsNotExist(err) {
			logger.Warnf("Open cache file %s failed: %s", cache.cachePath(key), err)
		}
		return err
	})

	cache.Lock()
	if err != nil {
		if it := cache.keys.remove(k, false); it != nil {
			cache.used -= int64(it.size + 4096)
		}
	}
	return f, err
}

func (cache *cacheStore) exist(key string) (bool, error) {
	cache.Lock()
	defer cache.Unlock()
	if _, ok := cache.pages[key]; ok {
		return true, nil
	}
	k := cache.getCacheKey(key)
	if cache.scanned && cache.keys.get(k) == nil {
		return false, errNotCached
	}
	cache.Unlock()
	var err error
	err = cache.checkErr(func() error {
		_, err = os.Stat(cache.cachePath(key))
		if err != nil && !os.IsNotExist(err) {
			logger.Warnf("Stat %s failed: %s", cache.cachePath(key), err)
		}
		return err
	})

	cache.Lock()
	if err == nil {
		return true, nil
	} else if it := cache.keys.remove(k, false); it != nil {
		cache.used -= int64(it.size + 4096)
	}
	return false, err
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
		if cache.enabled() && cache.flushPage(path, w.page.Data, w.dropCache) == nil {
			cache.add(w.key, int32(len(w.page.Data)), uint32(time.Now().Unix()))
		}
		cache.Lock()
		_, ok := cache.pages[w.key]
		delete(cache.pages, w.key)
		atomic.AddInt64(&cache.totalPages, -int64(cap(w.page.Data)))
		cache.Unlock()
		w.page.Release()
		if !ok {
			cache.remove(w.key, false)
		}
	}
}

func (cache *cacheStore) add(key string, size int32, atime uint32) {
	if size == 0 {
		logger.Warnf("Cache add %s with size 0, atime %d", key, atime) // should not happen
		return
	}
	k := cache.getCacheKey(key)
	cache.Lock()
	defer cache.Unlock()
	iter := cache.keys.get(k)
	if iter == nil {
		iter = &cacheItem{size: size, atime: atime}
	} else {
		if iter.size > 0 {
			cache.used -= int64(iter.size + 4096)
		}
		iter.size = size
		if atime > iter.atime {
			iter.atime = atime
		}
	}
	cache.keys.add(k, *iter) // add or update
	if size > 0 {
		cache.used += int64(size + 4096)
	}
	if cache.full() && cache.keys.name() != EvictionNone {
		logger.Debugf("Cleanup cache when add new data (%s): %d blocks (%s)", cache.dir, cache.keys.len(), humanize.IBytes(uint64(cache.used)))
		cache.cleanupFull()
	}
}

func (cache *cacheStore) stage(key string, data []byte) (string, error) {
	stagingPath := cache.stagePath(key)
	if cache.stageFull {
		return stagingPath, errStageFull
	}
	if cache.maxStageWrite != 0 && stagingBlocks.Load() > int64(cache.maxStageWrite) {
		return stagingPath, errStageConcurrency
	}
	stagingBlocks.Add(1)
	defer stagingBlocks.Add(-1)
	err := cache.flushPage(stagingPath, data, false)
	if err == nil {
		cache.m.stageBlocks.Add(1)
		cache.m.stageBlockBytes.Add(float64(len(data)))
		cache.m.stageWriteBytes.Add(float64(len(data)))
		if cache.enabled() {
			path := cache.cachePath(key)
			cache.createDir(filepath.Dir(path))
			if err = os.Link(stagingPath, path); err == nil {
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
func (cache *cacheStore) cleanupFull() {
	if !cache.available() {
		return
	}

	goal := cache.capacity * 95 / 100
	num := int64(cache.keys.len()) * 99 / 100
	if cache.maxItems != 0 && num > cache.maxItems*99/100 {
		num = cache.maxItems * 99 / 100
	}
	cache.Unlock()
	// make sure we have enough free space after cleanup
	usage := cache.curFreeRatio()
	cache.Lock()
	if usage.br < cache.freeRatio {
		toFree := int64(float32(usage.spaceCap) * (cache.freeRatio - usage.br))
		if toFree > cache.used {
			goal = 0
		} else if cache.used-toFree < goal {
			goal = (cache.used - toFree) * 95 / 100
		}
	}
	if usage.fr < cache.freeRatio {
		toFree := int(float32(usage.inodeCap) * (cache.freeRatio - usage.fr))
		if toFree > cache.keys.len() {
			num = 0
		} else {
			num = int64(cache.keys.len()-toFree) * 99 / 100
		}
	}
	if int64(cache.keys.len()) <= num && cache.used <= goal {
		return // some other thread has done the cleanup
	}

	var todel []cacheKey
	var freed int64
	var now = uint32(time.Now().Unix())

	for k, item := range cache.keys.evictionIter() {
		freed += int64(item.size + 4096)
		cache.used -= int64(item.size + 4096)
		todel = append(todel, k)

		logger.Debugf("remove %s from cache, age: %ds", k, now-item.atime)
		cache.m.cacheEvicts.Add(1)

		if int64(cache.keys.len()) <= num && cache.used <= goal {
			break
		}
	}
	if len(todel) > 0 {
		logger.Debugf("cleanup cache (%s) using %s eviction: %d blocks (%s), freed %d blocks (%s)", cache.dir, cache.keys.name(), cache.keys.len(), humanize.IBytes(uint64(cache.used)), len(todel), humanize.IBytes(uint64(freed)))
	}
	cache.Unlock()
	for _, k := range todel {
		if !cache.available() {
			break
		}
		_ = cache.removeFile(cache.cachePath(cache.getPathFromKey(k)))
	}
	cache.Lock()
}

func (cache *cacheStore) uploadStaging() {
	if !cache.scanned || cache.uploader == nil {
		return
	}
	var toFree int64
	usage := cache.curFreeRatio()
	if usage.br < cache.freeRatio || usage.fr < cache.freeRatio {
		toFree = int64(float64(usage.spaceCap)*float64(cache.freeRatio) - math.Min(float64(usage.br), float64(usage.fr)))
	}
	cache.Lock()
	defer cache.Unlock()
	var cnt int
	var lastK cacheKey
	var lastValue cacheItem
	// for each two random keys, then compare the access time, upload the older one
	for k, value := range cache.keys.randomIter() {
		if value.size > 0 {
			continue // read cache
		}

		// pick the bigger one if they were accessed within the same minute
		if cnt == 0 || lastValue.atime/60 > value.atime/60 ||
			lastValue.atime/60 == value.atime/60 && lastValue.size > value.size { // both size are < 0
			lastK = k
			lastValue = value
		}
		cnt++
		if cnt > 1 {
			cache.Unlock()
			key := cache.getPathFromKey(lastK)
			if !cache.uploader(key, cache.stagePath(key), true) {
				logger.Warnf("Upload list is too full")
				cache.Lock()
				return
			}
			logger.Debugf("upload %s, age: %d", key, uint32(time.Now().Unix())-lastValue.atime)
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
		key := cache.getPathFromKey(lastK)
		if cache.uploader(key, cache.stagePath(key), true) {
			logger.Debugf("upload %s, age: %d", key, uint32(time.Now().Unix())-lastValue.atime)
		}
		cache.Lock()
	}
}

func (cache *cacheStore) scanCached() {
	cache.Lock()
	cache.used = 0
	// atime in memory is more accurate than on disk, inherit it for the next round
	lastSnap := cache.keys.reset()
	cache.scanned = false
	cache.Unlock()

	var start = time.Now()
	var oneMinAgo = start.Add(-time.Minute)

	cachePrefix := filepath.Join(cache.dir, cacheDir)
	logger.Debugf("Scan %s to find cached blocks", cachePrefix)
	_ = filepath.WalkDir(cachePrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, _ := d.Info()
		if fi != nil {
			if fi.IsDir() || strings.HasSuffix(path, ".tmp") {
				if fi.ModTime().Before(oneMinAgo) {
					// try to remove empty directory
					if cache.removeFile(path) == nil {
						logger.Debugf("Remove empty directory: %s", path)
					}
				}
			} else {
				key := path[len(cachePrefix)+1:]
				if runtime.GOOS == "windows" {
					key = strings.ReplaceAll(key, "\\", "/")
				}
				atime := uint32(getAtime(fi).Unix())
				if lastAtime := lastSnap.peekAtime(cache.getCacheKey(key)); lastAtime > atime {
					atime = lastAtime
				}
				size := parseObjOrigSize(key) // track logical size
				if size == 0 {
					logger.Warnf("Ignore file with unknown size: %s", path)
					return nil
				}
				if getNlink(fi) > 1 {
					cache.add(key, -int32(size), atime)
				} else {
					cache.add(key, int32(size), atime)
				}
			}
		}
		return nil
	})

	cache.Lock()
	cache.scanned = true
	logger.Debugf("Found %s cached blocks (%s) in %s with %s", humanize.Comma(int64(cache.keys.len())), humanize.IBytes(uint64(cache.used)), cache.dir, time.Since(start))
	cache.Unlock()
}

var pathReg, _ = regexp.Compile(`^chunks/((\d+)|([0-9a-fA-F]{2}))/\d+/\d+_\d+_\d+$`)

func (cache *cacheStore) scanStaging() {
	if cache.uploader == nil {
		return
	}

	var start = time.Now()
	var oneMinAgo = start.Add(-time.Minute)
	var count, usage uint64
	stagingPrefix := filepath.Join(cache.dir, stagingDir)
	logger.Debugf("Scan %s to find staging blocks", stagingPrefix)
	_ = filepath.WalkDir(stagingPrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore it
		}
		fi, _ := d.Info()
		if fi != nil {
			if fi.IsDir() || strings.HasSuffix(path, ".tmp") {
				if fi.ModTime().Before(oneMinAgo) {
					// try to remove empty directory
					if cache.removeFile(path) == nil {
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
				origSize := parseObjOrigSize(key)
				if origSize == 0 {
					logger.Warnf("Ignore file with zero size: %s", path)
					return nil
				}
				logger.Debugf("Found staging block: %s", path)
				cache.m.stageBlocks.Add(1)
				cache.m.stageBlockBytes.Add(float64(origSize))
				cache.uploader(key, path, false)
				count++
				usage += uint64(origSize)
			}
		}
		return nil
	})
	if count > 0 {
		logger.Infof("Found %d staging blocks (%s) in %s with %s", count, humanize.IBytes(usage), cache.dir, time.Since(start))
	}
}

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
	stage(key string, data []byte) (string, error)
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
	existed, err := m.getStore(key).exist(key)
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

func (m *cacheManager) stage(key string, data []byte) (string, error) {
	store := m.getStore(key)
	if store != nil {
		return store.stage(key, data)
	}
	return "", errors.New("no available cache dir")
}

func (m *cacheManager) uploaded(key string, size int) {
	store := m.getStore(key)
	if store != nil {
		store.uploaded(key, size)
	}
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
