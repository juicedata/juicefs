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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	. "github.com/bytedance/mockey"
	. "github.com/smartystreets/goconvey/convey"
)

func testConf() Config {
	conf := defaultConf
	conf.CacheDir = filepath.Join(conf.CacheDir, fmt.Sprintf("%d", time.Now().UnixNano()))
	return conf
}

func TestNewCacheStore(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	s := newDiskCache(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	if s == nil {
		t.Fatalf("Create new cache store failed")
	}
}

func TestScanCached(t *testing.T) {
	var err error
	cfg := defaultConf
	cfg.CacheEviction = EvictionNone
	cache := &diskCache{
		opTs: make(map[time.Duration]func() error),
	}
	cache.state = newDCState(dcUnchanged, cache)
	cache.keys, err = NewKeyIndex(&cfg)
	require.NoError(t, err)
	cache.dir = t.TempDir()
	rawDir := filepath.Join(cache.dir, cacheDir)
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %s", rawDir, err)
	}
	num := 10
	for i := 0; i < num; i++ {
		if f, err := os.Create(filepath.Join(rawDir, fmt.Sprintf("test%d_1024", i))); err == nil {
			_ = f.Close()
		}
	}
	cache.scanCached(true)
	require.Equal(t, num, cache.keys.len())
}

func BenchmarkLoadCached(b *testing.B) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	s := newDiskCache(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	p := NewPage(make([]byte, 1024))
	key := "/chunks/1_1024"
	s.cache(key, p, false, false)
	time.Sleep(time.Millisecond * 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e == nil {
			_ = f.Close()
		} else {
			b.FailNow()
		}
	}
}

func BenchmarkLoadUncached(b *testing.B) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	s := newDiskCache(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	key := "chunks/222_1024"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e == nil {
			_ = f.Close()
		}
	}
}

func TestCheckPath(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		// unix path style
		{path: "chunks/111/222/3333_3333_3333", expected: true},
		{path: "chunks/111/222/3333_3333_0", expected: true},
		{path: "chunks/0/0/0_0_0", expected: true},
		{path: "chunks/01/10/0_01_0", expected: true},
		{path: "achunks/111/222/3333_3333_3333", expected: false},
		{path: "chunksa/111/222/3333_3333_3333", expected: false},
		{path: "chunksa", expected: false},
		{path: "chunks/111", expected: false},
		{path: "chunks/111/2222", expected: false},
		{path: "chunks/111/2222/3", expected: false},
		{path: "chunks/111/2222/3333_3333", expected: false},
		{path: "chunks/111/2222/3333_3333_3333_4444", expected: false},
		{path: "chunks/111/2222/3333_3333_3333/4444", expected: false},
		{path: "chunks/111_/2222/3333_3333_3333", expected: false},
		{path: "chunks/111/22_22/3333_3333_3333", expected: false},
		{path: "chunks/111/22_22/3333_3333_3333", expected: false},
		{path: "chunks/dd/222/3333_3333_0", expected: true}, // hash prefix
		{path: "chunks/FF/222/3333_3333_0", expected: true}, // hash prefix
		{path: "chunks/5D/222/3333_3333_0", expected: true}, // hash prefix
		{path: "chunks/D1/222/3333_3333_0", expected: true}, // hash prefix
		{path: "chunks/5DD/222/3333_3333_0", expected: false},
		{path: "chunks/111D/222/3333_3333_0", expected: false},
	}
	for _, c := range cases {
		if res := pathReg.MatchString(c.path); res != c.expected {
			t.Fatalf("check path %s expected %v but got %v", c.path, c.expected, res)
		}
	}
}

func TestCleanupFullDoesNotBlockLoad(t *testing.T) {
	PatchConvey("test getDiskUsage", t, func() {
		conf := defaultConf
		conf.CacheEviction = EvictionNone
		s := newTestCacheStore(t.TempDir()+"/", &conf, nil)
		Mock(getDiskUsage).To(func(path string) (uint64, uint64, uint64, uint64) {
			time.Sleep(time.Second * 10)
			return 1, 1, 1, 1
		}).Build()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			s.Lock()
			wg.Done()
			s.cleanupFull()
			s.Unlock()
		}()

		wg.Wait()
		start := time.Now()
		_, _ = s.load("1_1_1")
		So(time.Since(start), ShouldBeLessThan, time.Second*3)
	})
}

func TestAtimeNotLost(t *testing.T) {
	for _, eviction := range []string{EvictionNone, Eviction2Random, EvictionLRU} {
		cfg := defaultConf
		cfg.CacheEviction = eviction
		cfg.FreeSpace = 0.03
		m := newCacheManager(&cfg, nil, nil)
		key := "0_0_10"

		p := NewPage([]byte("helloworld"))
		defer p.Release()
		m.cache(key, p, true, false)
		time.Sleep(3 * time.Second)

		_, exist := m.exist(key) // touch atime
		if !exist {
			t.Fatalf("CacheStore key %s not exist", key)
		}
		s := m.(*cacheManager).stores[0]
		atimeMem := s.keys.peekAtime(s.getCacheKey(key))
		if atimeMem == 0 {
			t.Fatalf("CacheStore key %s atime lost", key)
		}
		s.scanCached(false) // should use atime from memory
		atimeAfterScan := s.keys.peekAtime(s.getCacheKey(key))
		if atimeAfterScan != atimeMem {
			t.Fatalf("CacheStore key %s atime lost after scan, before: %d, after: %d", key, atimeMem, atimeAfterScan)
		}
	}
}
func TestSetlimitByFreeRatio(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	cache := newDiskCache(nil, conf.CacheDir, 1<<30, 1000, 1, &conf, nil)

	usage := DiskFreeRatio{
		spaceCap: 1 << 30,
		inodeCap: 1000,
	}
	freeRatio := float32(0.2)
	cache.setLimitByFreeRatio(usage, 0.2)

	expectedSizeLimit := int64((1 - freeRatio + 0.05) * float32(usage.spaceCap))
	if cache.capacity > expectedSizeLimit {
		t.Fatalf("Expected capacity <= %d, but got %d", expectedSizeLimit, cache.capacity)
	}
	expectedInodeLimit := int64((1 - freeRatio + 0.05) * float32(usage.inodeCap))
	if cache.maxItems > expectedInodeLimit && cache.maxItems != 0 {
		t.Fatalf("Expected maxItems <= %d, but got %d", expectedInodeLimit, cache.maxItems)
	}
}

func TestSetLimitByFreeRatioUnknownInodesKeepExplicitMaxItems(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	cache := newDiskCache(nil, conf.CacheDir, 1<<30, 1000, 1, &conf, nil)

	usage := DiskFreeRatio{
		spaceCap: 1 << 30,
		inodeCap: 0,
	}
	cache.setLimitByFreeRatio(usage, 0.2)
	require.Equal(t, int64(1000), cache.maxItems)
}

func TestUnknownInodeStatsShouldNotMarkCacheAsRawFull(t *testing.T) {
	PatchConvey("unknown inode stats should not trigger rawFull", t, func() {
		Mock(getDiskUsage).To(func(path string) (uint64, uint64, uint64, uint64) {
			return 1 << 30, 1 << 30, 0, 0
		}).Build()

		conf := defaultConf
		conf.CacheDir = t.TempDir()
		m := new(cacheManagerMetrics)
		m.initMetrics()
		s := newDiskCache(m, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)

		require.Never(t, func() bool {
			s.Lock()
			defer s.Unlock()
			return s.rawFull
		}, 1500*time.Millisecond, 100*time.Millisecond)
	})
}

func Test2RandomEviction(t *testing.T) {
	Convey("Test2RandomEviction-CacheFull", t, func() {
		dir := t.TempDir()
		defer os.RemoveAll(dir)
		conf := defaultConf
		conf.FreeSpace = 0.00001
		conf.CacheScanInterval = -1 // Disable periodic scan
		conf.CacheSize = 1 << 30
		conf.CacheItems = 10 // Max 10 items to easily trigger eviction

		m := new(cacheManagerMetrics)
		m.initMetrics()
		s := newDiskCache(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
		require.NotNil(t, s)
		if _, ok := s.keys.(*randomEviction); !ok {
			t.Fatalf("Expected randomEviction, but got %T", s.keys)
		}

		// Add items with distinct atimes
		for i := 1; i <= 20; i++ {
			key := fmt.Sprintf("%d_%d_1024", i, i)
			s.add(key, 1024, uint32(time.Now().Add(time.Duration(i)*time.Second).Unix())) // New items have larger atime
			require.LessOrEqual(t, int64(s.keys.len()), conf.CacheItems, "Cache should not exceed max items limit during addition")
			require.Greater(t, s.keys.len(), 0, "Cache should always have items after addition")
		}
	})
}

func TestLruEviction(t *testing.T) {
	Convey("TestLruEviction-CacheFull", t, func() {
		dir := t.TempDir()
		defer os.RemoveAll(dir)
		conf := defaultConf
		conf.CacheEviction = EvictionLRU
		conf.FreeSpace = 0.00001
		conf.CacheScanInterval = -1 // Disable periodic scan
		conf.CacheSize = 1 << 30
		conf.CacheItems = 10 // Max 10 items to easily trigger eviction

		m := new(cacheManagerMetrics)
		m.initMetrics()
		s := newDiskCache(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
		require.NotNil(t, s)
		le := s.keys.(*lruEviction)

		// Add items with distinct atimes
		for i := 1; i <= 20; i++ {
			key := fmt.Sprintf("%d_%d_1024", i, i)
			s.add(key, 1024, uint32(time.Now().Add(time.Duration(i)*time.Second).Unix())) // New items have larger atime
			require.True(t, le.verifyHeap())
			require.LessOrEqual(t, int64(s.keys.len()), conf.CacheItems, "Cache should not exceed max items limit during addition")
			require.Greater(t, s.keys.len(), 0, "Cache should always have items after addition")
		}

		cutIndex := 20 - conf.CacheItems
		expectedKeys := make(map[string]bool)
		// After eviction, the cache should only contain the newest items.
		for i := cutIndex + 1; i <= 20; i++ {
			key := fmt.Sprintf("%d_%d_1024", i, i)
			expectedKeys[key] = true
		}

		require.Equal(t, le.lruHeap.Len(), len(le.keys), "Heap length should match keys length after insertion")
		require.Equal(t, len(expectedKeys), len(le.keys), "Number of items in cache after eviction mismatch")
		require.Equal(t, len(expectedKeys), le.lruHeap.Len(), "Number of items in heap after eviction mismatch")

		// Verify the heap also contains the expected keys
		tempHeap := make(atimeHeap, le.lruHeap.Len())
		copy(tempHeap, le.lruHeap)
		for tempHeap.Len() > 0 {
			item := tempHeap.Pop().(heapItem)
			require.Contains(t, expectedKeys, item.key.String(), "Unexpected key found in heap: %s", item.key.String())
		}

		// Verify all evicted keys are no longer in the cache
		for i := int64(1); i <= cutIndex; i++ {
			key := fmt.Sprintf("%d_%d_1024", i, i)
			_, ok := le.keys[s.getCacheKey(key)]
			require.False(t, ok, "Evicted key %s still found in cache", key)
		}
	})

	Convey("TestLruEviction-WriteBack", t, func() {
		dir := t.TempDir()
		defer os.RemoveAll(dir)
		conf := defaultConf
		conf.CacheEviction = EvictionLRU
		conf.Writeback = true
		conf.FreeSpace = 0.00001
		conf.CacheScanInterval = -1 // Disable periodic scan
		conf.CacheSize = 1 << 30
		conf.CacheItems = 10 // Max 10 items to easily trigger eviction

		// TODO: delete me
		m := new(cacheManagerMetrics)
		m.initMetrics()
		s := newDiskCache(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
		require.NotNil(t, s)
		le := s.keys.(*lruEviction)

		// Add items with distinct atimes
		blockPlaceHolder := []byte("test data")
		for i := 1; i <= 20; i++ {
			key := fmt.Sprintf("%d_%d_9", i, i)
			_, err := s.stage(key, blockPlaceHolder, 0)
			require.True(t, le.verifyHeap())
			require.NoError(t, err, "Failed to stage data for key %s", key)
		}
		require.Equal(t, 20, len(le.keys), "Cache should contain 20 staged items even if full")
		require.Equal(t, 0, len(le.lruHeap), "Staged items should not be in the LRU heap")

		s.Lock()
		s.cleanupFull()
		s.Unlock()
		for i := 1; i <= 20; i++ {
			key := fmt.Sprintf("%d_%d_9", i, i)
			s.uploaded(key, len(blockPlaceHolder))
		}
		require.Equal(t, len(le.keys), le.lruHeap.Len(), "Heap length should match keys length after staged items are uploaded")

		s.maxItems = 1
		s.Lock()
		s.cleanupFull()
		s.Unlock()
		require.Equal(t, 0, len(le.keys), "Cache should be empty by cleanupFull after setting maxItems to 1")
		require.Equal(t, 0, len(le.lruHeap), "LRU heap should be empty by cleanupFull after setting maxItems to 1")
	})
}

func TestCooldownAtimeOnWriteFixedOnLoad(t *testing.T) {
	dir := t.TempDir()
	conf := defaultConf
	conf.CacheExpire = time.Hour
	conf.CacheEviction = EvictionNone
	conf.CacheScanInterval = -1
	m := new(cacheManagerMetrics)
	m.initMetrics()
	cache := newDiskCache(m, dir, 1<<30, 1000, 1, &conf, nil)
	cache.scanned = true
	key := "0_0_4"

	PatchConvey("mock time.Now to avoid drift", t, func() {
		fixedTime := time.Date(2025, 1, 28, 12, 0, 0, 0, time.UTC)
		Mock(time.Now).Return(fixedTime).Build()
		path, err := cache.stage(key, []byte("test"), 0)
		require.NoError(t, err)
		require.NotEmpty(t, path)
		expectedCooldownAtime := uint32(fixedTime.Add(-conf.CacheExpire / 2).Unix())
		require.Equal(t, expectedCooldownAtime, cache.keys.peekAtime(cache.getCacheKey(key)))
		rc, err := cache.load(key)
		require.NoError(t, err)
		require.NotNil(t, rc)
		defer rc.Close()
		require.Equal(t, uint32(fixedTime.Unix()), cache.keys.peekAtime(cache.getCacheKey(key)))
	})
}

func newTestCacheStore(dir string, conf *Config, uploader func(key, path string, force bool) bool) *diskCache {
	keyIndex, _ := NewKeyIndex(conf)
	c := &diskCache{
		dir:       dir,
		mode:      0600,
		capacity:  1 << 30,
		freeRatio: conf.FreeSpace,
		keys:      keyIndex,
		pending:   make(chan pendingFile, 10),
		pages:     make(map[string]*Page),
		uploader:  uploader,
		opTs:      make(map[time.Duration]func() error),
		scanned:   true,
	}
	c.state = newDCState(dcNormal, c)
	return c
}

func TestUploadStagingToFreeCalculation(t *testing.T) {
	PatchConvey("uploadStaging should only upload enough blocks to satisfy freeRatio", t, func() {
		dir := t.TempDir()
		conf := defaultConf
		conf.FreeSpace = 0.10
		conf.CacheEviction = EvictionNone

		var uploadedKeys []string
		uploader := func(key, path string, force bool) bool {
			uploadedKeys = append(uploadedKeys, key)
			return true
		}

		s := newTestCacheStore(dir+"/", &conf, uploader)
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("chunks/0/0/%d_%d_1000", i, i)
			k := s.getCacheKey(key)
			s.keys.add(k, cacheItem{size: -1000, atime: uint32(time.Now().Unix()) - uint32(i*60)})
		}

		Mock(getDiskUsage).To(func(path string) (uint64, uint64, uint64, uint64) {
			return 100000, 5000, 100000, 100000
		}).Build()

		s.uploadStaging()
		uploaded := len(uploadedKeys)
		require.LessOrEqual(t, uploaded, 5)
		require.Greater(t, uploaded, 0, "should upload at least some blocks when disk is tight")
	})
}

func TestUploadStagingInodeToFree(t *testing.T) {
	PatchConvey("uploadStaging respects inode pressure", t, func() {
		dir := t.TempDir()
		conf := defaultConf
		conf.FreeSpace = 0.10
		conf.CacheEviction = EvictionNone

		var uploadCount int
		uploader := func(key, path string, force bool) bool {
			uploadCount++
			return true
		}
		s := newTestCacheStore(dir+"/", &conf, uploader)

		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("chunks/0/0/%d_%d_1000", i, i)
			k := s.getCacheKey(key)
			s.keys.add(k, cacheItem{size: -1000, atime: uint32(time.Now().Unix()) - uint32(i*60)})
		}

		Mock(getDiskUsage).To(func(path string) (uint64, uint64, uint64, uint64) {
			return 100000, 20000, 1000, 50
		}).Build()
		s.uploadStaging()
		count := uploadCount
		require.Greater(t, count, 0, "should upload blocks when inodes are tight")
	})
}

func TestSpaceToFreeNoAction(t *testing.T) {
	PatchConvey("uploadStaging does nothing when disk has enough space", t, func() {
		dir := t.TempDir()
		conf := defaultConf
		conf.FreeSpace = 0.10
		conf.CacheEviction = EvictionNone

		var uploadCount int
		uploader := func(key, path string, force bool) bool {
			uploadCount++
			return true
		}

		s := newTestCacheStore(dir+"/", &conf, uploader)

		for i := 0; i < 5; i++ {
			key := fmt.Sprintf("chunks/0/0/%d_%d_1000", i, i)
			k := s.getCacheKey(key)
			s.keys.add(k, cacheItem{size: -1000, atime: uint32(time.Now().Unix())})
		}

		// Mock: 20% free space, 20% free inodes - both above freeRatio (10%)
		Mock(getDiskUsage).To(func(path string) (uint64, uint64, uint64, uint64) {
			return 100000, 20000, 100000, 20000
		}).Build()

		s.uploadStaging()
		require.Equal(t, 0, uploadCount, "should not upload when disk has enough free space and inodes")
	})
}
