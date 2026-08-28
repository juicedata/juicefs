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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charlievieth/fastwalk"
	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/utils"
)

var (
	stagingDir          = "rawstaging"
	cacheDir            = "raw"
	maxIODur            = time.Second * 30
	stagingBlocks       atomic.Int64
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
	id        uint64
}

type diskCache struct {
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
	pending       chan *pendingFile
	pages         map[string]*pendingFile
	pendingID     atomic.Uint64
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

	// newBlockCooldown reduces the initial access time for newly cached staged blocks.
	// This helps prevent a surge of writes from evicting active read blocks.
	stagedBlockCooldown time.Duration
}

func newDiskCache(m *cacheManagerMetrics, dir string, cacheSize, maxItems int64, pendingPages int, config *Config, uploader func(key, path string, force bool) bool) *diskCache {
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
	c := &diskCache{
		m:                   m,
		dir:                 dir,
		mode:                config.CacheMode,
		capacity:            cacheSize,
		maxItems:            maxItems,
		maxStageWrite:       config.MaxStageWrite,
		freeRatio:           config.FreeSpace,
		checksum:            config.CacheChecksum,
		hashPrefix:          config.HashPrefix,
		scanInterval:        config.CacheScanInterval,
		cacheExpire:         config.CacheExpire,
		keys:                keyIndex,
		pending:             make(chan *pendingFile, pendingPages),
		pages:               make(map[string]*pendingFile),
		uploader:            uploader,
		opTs:                make(map[time.Duration]func() error),
		stagedBlockCooldown: config.CacheExpire / 2,
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

func (cache *diskCache) setLimitByFreeRatio(usage DiskFreeRatio, freeRatio float32) {
	sizeLimit := int64(float64(1-freeRatio) * float64(usage.spaceCap))
	if sizeLimit < cache.capacity {
		limit := cache.capacity
		cache.capacity = sizeLimit
		logger.Infof("Adjusted cache capacity based on freeratio: from %d to %d bytes", limit, cache.capacity)
	}
	if usage.inodeCap <= 0 {
		return
	}
	inodeLimit := int64(float64(1-freeRatio) * float64(usage.inodeCap))
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

func (cache *diskCache) lockFilePath() string {
	return filepath.Join(cache.dir, ".lock")
}

func (cache *diskCache) createLockFile() {
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

func (cache *diskCache) checkLockFile() {
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

func (c *diskCache) available() bool {
	return c.state.state() != dcDown
}

func (c *diskCache) enabled() bool {
	return c.capacity > 0
}

func (c *diskCache) full() bool {
	return c.used > c.capacity || (c.maxItems != 0 && int64(c.keys.len()) > c.maxItems)
}

func (cache *diskCache) checkErr(f func() error) error {
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

func (c *diskCache) checkTimeout() {
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

func (c *diskCache) statFile(path string) error {
	return c.checkErr(func() error {
		_, err := os.Stat(path)
		return err
	})
}

func (cache *diskCache) removeFile(path string) error {
	return cache.checkErr(func() error {
		return os.Remove(path)
	})
}

func (cache *diskCache) renameFile(oldpath, newpath string) error {
	return cache.checkErr(func() error {
		return os.Rename(oldpath, newpath)
	})
}

func (cache *diskCache) writeFile(f *os.File, data []byte) error {
	return cache.checkErr(func() error {
		_, err := f.Write(data)
		return err
	})
}

func (cache *diskCache) closeFile(f *os.File) error {
	return cache.checkErr(func() error {
		return f.Close()
	})
}

func (cache *diskCache) usedMemory() int64 {
	return atomic.LoadInt64(&cache.totalPages)
}

func (cache *diskCache) stats() (int64, int64) {
	cache.Lock()
	defer cache.Unlock()
	return int64(len(cache.pages) + cache.keys.len()), cache.used + cache.usedMemory()
}

func (cache *diskCache) isFull(usage DiskFreeRatio, stage bool) bool {
	if stage {
		return usage.br < cache.freeRatio/2 || (usage.inodeCap > 0 && usage.fr < cache.freeRatio/2)
	}
	return usage.br < cache.freeRatio || (usage.inodeCap > 0 && usage.fr < cache.freeRatio)
}

func (cache *diskCache) checkFreeSpace() {
	for cache.available() {
		usage := cache.curFreeRatio()
		cache.stageFull = cache.isFull(usage, true)
		cache.rawFull = cache.isFull(usage, false)
		if cache.rawFull && cache.keys.name() != EvictionNone {
			logger.Tracef("Cleanup cache when check free space (%s): free ratio (%d%%), space usage (%d%%), inodes usage (%d%%)", cache.dir, int(cache.freeRatio*100), int(usage.br*100), int(usage.fr*100))
			cache.Lock()
			cache.cleanupFull()
			cache.Unlock()
			usage = cache.curFreeRatio()
			cache.rawFull = cache.isFull(usage, false)
		}
		if cache.rawFull {
			cache.uploadStaging()
		}
		time.Sleep(time.Second)
	}
	logger.Infof("stop checkFreeSpace at %s", cache.dir)
}

func (cache *diskCache) cleanupExpire() {
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

func (cache *diskCache) refreshCacheKeys() {
	if cache.scanInterval < 0 {
		return
	}
	cache.scanCached(true)
	if cache.scanInterval > 0 {
		for {
			time.Sleep(cache.scanInterval)
			cache.scanCached(false)
		}
	}
}

func (cache *diskCache) removeStage(key string) error {
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

func (cache *diskCache) cache(key string, p *Page, force, dropCache bool) {
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
	w := &pendingFile{key: key, page: p, dropCache: dropCache, id: cache.pendingID.Add(1)}
	p.Acquire()
	cache.pages[key] = w
	atomic.AddInt64(&cache.totalPages, int64(cap(p.Data)))
	select {
	case cache.pending <- w:
	default:
		if force {
			cache.Unlock()
			cache.pending <- w
			cache.Lock()
		} else {
			// does not have enough bandwidth to write it into disk, discard it
			logger.Debugf("Caching queue is full (%s), drop %s (%d bytes)", cache.dir, key, len(p.Data))
			cache.m.cacheDrops.Add(1)
			if cache.pages[key] == w {
				delete(cache.pages, key)
			}
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
func (cache *diskCache) curFreeRatio() DiskFreeRatio {
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

func (cache *diskCache) writePage(path string, data []byte, dropCache bool, tierID uint8) (err error) {
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

	var f *os.File
	err = cache.checkErr(func() error {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cache.mode)
		return err
	})
	if err != nil {
		logger.Warnf("Can't create cache file %s: %s", path, err)
		return err
	}

	defer func() {
		if err != nil {
			_ = cache.removeFile(path)
		}
	}()

	if err = cache.writeFile(f, data); err != nil {
		logger.Warnf("Write to cache file %s failed: %s", path, err)
		_ = f.Close()
		return
	}
	if cache.checksum != CsNone {
		if err = cache.writeFile(f, checksum(data)); err != nil {
			logger.Warnf("Write checksum to cache file %s failed: %s", path, err)
			_ = f.Close()
			return
		}
	}
	if tierID != 0 {
		// only staged file has a footer
		footer := stageFooter{Tier: tierID}
		var fData []byte
		fData, err = (&footer).marshal(cache.checksum != CsNone)
		if err != nil {
			logger.Warnf("Marshal stage footer for cache file %s failed: %s", path, err)
			_ = f.Close()
			return
		}
		if err = cache.writeFile(f, fData); err != nil {
			logger.Warnf("Write stage footer to cache file %s failed: %s", path, err)
			_ = f.Close()
			return
		}
	}
	if dropCache {
		dropOSCache(f)
	}
	if err = cache.closeFile(f); err != nil {
		logger.Warnf("Close cache file %s failed: %s", path, err)
		return
	}
	return
}

func (cache *diskCache) flushPage(path string, data []byte, dropCache bool, tierID uint8) (err error) {
	tmp := path + ".tmp"
	if err = cache.writePage(tmp, data, dropCache, tierID); err != nil {
		return
	}
	if err = cache.renameFile(tmp, path); err != nil {
		logger.Warnf("Rename cache file %s -> %s failed: %s", tmp, path, err)
		_ = cache.removeFile(tmp)
	}
	return
}

func (cache *diskCache) createDir(dir string) {
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

func (cache *diskCache) getCacheKey(key string) cacheKey {
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

func (cache *diskCache) getPathFromKey(k cacheKey) string {
	if cache.hashPrefix {
		return fmt.Sprintf("chunks/%02X/%v/%v_%v_%v", k.id%256, k.id/1000/1000, k.id, k.indx, k.size)
	} else {
		return fmt.Sprintf("chunks/%v/%v/%v_%v_%v", k.id/1000/1000, k.id/1000, k.id, k.indx, k.size)
	}
}

func (cache *diskCache) remove(key string, staging bool) {
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
	if path != "" {
		if err := cache.removeFile(path); err != nil && !os.IsNotExist(err) {
			logger.Warnf("remove %s failed: %s", path, err)
		}
	}
	cache.Unlock()

	if path != "" {
		if staging {
			if err := cache.removeStage(key); err != nil && !os.IsNotExist(err) {
				logger.Warnf("remove stage %s failed: %s", cache.stagePath(key), err)
			}
		}
	}
}

func (cache *diskCache) load(key string) (ReadCloser, error) {
	cache.Lock()
	defer cache.Unlock()
	if w, ok := cache.pages[key]; ok {
		return NewPageReader(w.page), nil
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

func (cache *diskCache) exist(key string) (bool, error) {
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

func (cache *diskCache) cachePath(key string) string {
	return filepath.Join(cache.dir, cacheDir, key)
}

func (cache *diskCache) stagePath(key string) string {
	return filepath.Join(cache.dir, stagingDir, key)
}

// flush cached block into disk
func (cache *diskCache) flush() {
	for {
		cache.flushPending(<-cache.pending)
	}
}

func (cache *diskCache) flushPending(w *pendingFile) {
	tmp := cache.pendingPath(w)
	prepared := cache.enabled() && cache.writePage(tmp, w.page.Data, w.dropCache, 0) == nil
	cache.finishPending(w, tmp, prepared)
}

func (cache *diskCache) pendingPath(w *pendingFile) string {
	return fmt.Sprintf("%s.%d.tmp", cache.cachePath(w.key), w.id)
}

func (cache *diskCache) finishPending(w *pendingFile, tmp string, prepared bool) {
	path := cache.cachePath(w.key)
	committed, cleanup := false, false
	cache.Lock()
	if cache.pages[w.key] == w {
		if prepared {
			if err := cache.renameFile(tmp, path); err != nil {
				logger.Warnf("Rename cache file %s -> %s failed: %s", tmp, path, err)
			} else {
				committed = true
				if cache.addLocked(w.key, int32(len(w.page.Data)), uint32(time.Now().Unix())) {
					cleanup = cache.full() && cache.keys.name() != EvictionNone
				}
			}
		}
		delete(cache.pages, w.key)
	}
	cache.Unlock()
	if prepared && !committed {
		if err := cache.removeFile(tmp); err != nil && !os.IsNotExist(err) {
			logger.Warnf("remove %s failed: %s", tmp, err)
		}
	}
	atomic.AddInt64(&cache.totalPages, -int64(cap(w.page.Data)))
	w.page.Release()
	if cleanup {
		cache.Lock()
		if cache.full() && cache.keys.name() != EvictionNone {
			logger.Debugf("Cleanup cache when add new data (%s): %d blocks (%s)", cache.dir, cache.keys.len(), humanize.IBytes(uint64(cache.used)))
			cache.cleanupFull()
		}
		cache.Unlock()
	}
}

func (cache *diskCache) add(key string, size int32, atime uint32) {
	cache.Lock()
	defer cache.Unlock()
	if !cache.addLocked(key, size, atime) {
		return
	}
	if cache.full() && cache.keys.name() != EvictionNone {
		logger.Debugf("Cleanup cache when add new data (%s): %d blocks (%s)", cache.dir, cache.keys.len(), humanize.IBytes(uint64(cache.used)))
		cache.cleanupFull()
	}
}

func (cache *diskCache) addLocked(key string, size int32, atime uint32) bool {
	if size == 0 {
		logger.Warnf("Cache add %s with size 0, atime %d", key, atime) // should not happen
		return false
	}
	k := cache.getCacheKey(key)
	iter := cache.keys.get(k)
	if iter == nil {
		iter = &cacheItem{size: size, atime: atime}
	} else {
		if iter.size > 0 {
			cache.used -= int64(iter.size + 4096)
		}
		iter.size = size
	}
	cache.keys.add(k, *iter) // add or update
	if size > 0 {
		cache.used += int64(size + 4096)
	}
	return true
}

func (cache *diskCache) stage(key string, data []byte, tierID uint8) (string, error) {
	stagingPath := cache.stagePath(key)
	if cache.stageFull {
		return stagingPath, errStageFull
	}
	if cache.maxStageWrite != 0 && stagingBlocks.Load() > int64(cache.maxStageWrite) {
		return stagingPath, errStageConcurrency
	}
	stagingBlocks.Add(1)
	defer stagingBlocks.Add(-1)
	err := cache.flushPage(stagingPath, data, false, tierID)
	if err == nil {
		cache.m.stageBlocks.Add(1)
		cache.m.stageBlockBytes.Add(float64(len(data)))
		cache.m.stageWriteBytes.Add(float64(len(data)))
		if cache.enabled() {
			path := cache.cachePath(key)
			cache.createDir(filepath.Dir(path))
			if err = os.Link(stagingPath, path); err == nil {
				cache.add(key, -int32(len(data)), uint32(time.Now().Add(-cache.stagedBlockCooldown).Unix()))
			} else {
				logger.Warnf("link %s to %s failed: %s", stagingPath, path, err)
			}
		}
	}
	return stagingPath, err
}

func (cache *diskCache) uploaded(key string, size int) {
	cache.add(key, int32(size), 0)
}

func (cache *diskCache) spaceToFree(usage DiskFreeRatio) int64 {
	if usage.br < cache.freeRatio {
		return int64(float64(usage.spaceCap) * float64(cache.freeRatio-usage.br))
	}
	return 0
}

func (cache *diskCache) inodesToFree(usage DiskFreeRatio) int64 {
	if usage.fr < cache.freeRatio {
		return int64(float64(usage.inodeCap) * float64(cache.freeRatio-usage.fr))
	}
	return 0
}

// locked
func (cache *diskCache) cleanupFull() {
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
	if toFree := cache.spaceToFree(usage); toFree > 0 {
		if toFree > cache.used {
			goal = 0
		} else if cache.used-toFree < goal {
			goal = (cache.used - toFree) * 95 / 100
		}
	}
	if toFree := cache.inodesToFree(usage); toFree > 0 {
		if toFree > int64(cache.keys.len()) {
			num = 0
		} else {
			num = (int64(cache.keys.len()) - toFree) * 99 / 100
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

func (cache *diskCache) uploadStaging() {
	if !cache.scanned || cache.uploader == nil {
		return
	}
	usage := cache.curFreeRatio()
	spaceToFree := cache.spaceToFree(usage)
	inodeToFree := cache.inodesToFree(usage)
	if spaceToFree <= 0 && inodeToFree <= 0 {
		return
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
			spaceToFree -= int64(-lastValue.size + 4096)
			inodeToFree--
			cnt = 0
		}

		if spaceToFree <= 0 && inodeToFree <= 0 {
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

func (cache *diskCache) scanCached(fast bool) {
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

	walkDir := func(path string, walkFn fs.WalkDirFunc) error {
		if fast {
			return fastwalk.Walk(nil, path, walkFn)
		} else {
			return filepath.WalkDir(path, walkFn)
		}
	}

	_ = walkDir(cachePrefix, func(path string, d fs.DirEntry, err error) error {
		// this func should be concurrent safe
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

func (cache *diskCache) scanStaging() {
	if cache.uploader == nil {
		return
	}

	var start = time.Now()
	var oneMinAgo = start.Add(-time.Minute)
	var count, usage uint64
	stagingPrefix := filepath.Join(cache.dir, stagingDir)
	logger.Debugf("Scan %s to find staging blocks", stagingPrefix)
	_ = fastwalk.Walk(nil, stagingPrefix, func(path string, d fs.DirEntry, err error) error {
		// this func should be concurrent safe
		if err != nil {
			return nil // ignore it
		}
		if d.IsDir() || strings.HasSuffix(path, ".tmp") {
			fi, err := d.Info()
			if err != nil {
				return nil
			}
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
			atomic.AddUint64(&count, 1)
			atomic.AddUint64(&usage, uint64(origSize))
		}
		return nil
	})
	if count > 0 {
		logger.Infof("Found %d staging blocks (%s) in %s with %s", count, humanize.IBytes(usage), cache.dir, time.Since(start))
	}
}
