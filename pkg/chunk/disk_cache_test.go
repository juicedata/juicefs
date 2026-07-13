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
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	. "github.com/bytedance/mockey"
	. "github.com/smartystreets/goconvey/convey"
)

// Copy from https://github.com/prometheus/client_golang/blob/v1.14.0/prometheus/testutil/testutil.go
func toFloat64(c prometheus.Collector) float64 {
	var (
		m      prometheus.Metric
		mCount int
		mChan  = make(chan prometheus.Metric)
		done   = make(chan struct{})
	)

	go func() {
		for m = range mChan {
			mCount++
		}
		close(done)
	}()

	c.Collect(mChan)
	close(mChan)
	<-done

	if mCount != 1 {
		panic(fmt.Errorf("collected %d metrics instead of exactly 1", mCount))
	}

	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		panic(fmt.Errorf("error happened while collecting metrics: %w", err))
	}
	if pb.Gauge != nil {
		return pb.Gauge.GetValue()
	}
	if pb.Counter != nil {
		return pb.Counter.GetValue()
	}
	if pb.Untyped != nil {
		return pb.Untyped.GetValue()
	}
	panic(fmt.Errorf("collected a non-gauge/counter/untyped metric: %s", pb))
}

func testConf() Config {
	conf := defaultConf
	conf.CacheDir = filepath.Join(conf.CacheDir, fmt.Sprintf("%d", time.Now().UnixNano()))
	return conf
}

func TestNewCacheStore(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	s := newCacheStore(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	if s == nil {
		t.Fatalf("Create new cache store failed")
	}
}

func TestMetrics(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	m := newCacheManager(&conf, nil, nil)
	metrics := m.(*cacheManager).metrics
	s := m.(*cacheManager).stores[0]
	content := []byte("helloworld")
	p := NewPage(content)
	s.cache("test", p, true, false)
	// Waiting for the cache to be flushed
	time.Sleep(time.Millisecond * 100)
	if toFloat64(metrics.cacheWrites) != 1.0 {
		t.Fatalf("expect the cacheWrites is 1")
	}

	if toFloat64(metrics.cacheWriteBytes) != float64(len(content)) {
		t.Fatalf("expect the cacheWriteBytes is %d", len(content))
	}

	if toFloat64(metrics.stageBlocks) != 0.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != 0.0 {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}
	key := fmt.Sprintf("chunks/0/5/5000_2_%d", len(content))
	stagingPath, err := m.stage(key, content, 0)
	if err != nil {
		t.Fatalf("stage failed: %s", err)
	}
	if toFloat64(metrics.stageBlocks) != 1.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != float64(len(content)) {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}
	err = m.removeStage(key)
	if err != nil {
		t.Fatalf("faild to remove stage")
	}

	if toFloat64(metrics.stageBlocks) != 0.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != 0.0 {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}

	if _, err := os.Stat(stagingPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("expect the stageingPath %s not exists", stagingPath)
	}
}

func TestScanCached(t *testing.T) {
	var err error
	cfg := defaultConf
	cfg.CacheEviction = EvictionNone
	cache := &cacheStore{
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

func TestChecksum(t *testing.T) {
	conf := testConf()
	conf.FreeSpace = 0.01
	conf.CacheEviction = EvictionNone
	defer os.RemoveAll(conf.CacheDir)
	m := new(cacheManagerMetrics)
	m.initMetrics()
	s := newCacheStore(m, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	k1 := "0_0_10" // no checksum
	k2 := "1_0_10"
	k3 := "2_1_102400"
	k4 := "3_5_102400" // corrupt data
	k5 := "4_8_1048576"

	p := NewPage([]byte("helloworld"))
	defer p.Release()
	s.cache(k1, p, true, false)

	s.checksum = CsFull
	s.cache(k2, p, true, false)

	buf := make([]byte, 102400)
	utils.RandRead(buf)
	s.cache(k3, NewPage(buf), true, false)

	fpath := s.cachePath(k4)
	dir := filepath.Dir(fpath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir parent dir %s: %s", dir, err)
	}
	f, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE, s.mode)
	if err != nil {
		t.Fatalf("Create cache file %s: %s", fpath, err)
	}
	if _, err = f.Write(buf); err != nil {
		_ = f.Close()
		t.Fatalf("Write cache file %s: %s", fpath, err)
	}
	corrupt := make([]byte, 102400)
	copy(corrupt, buf)
	for i := 98304; i < 102400; i++ { // reset 96K ~ 100K
		corrupt[i] = 0
	}
	if _, err = f.Write(checksum(corrupt)); err != nil {
		_ = f.Close()
		t.Fatalf("Write checksum to cache file %s: %s", fpath, err)
	}
	_ = f.Close()
	s.add(k4, 102400, uint32(time.Now().Unix()))

	buf = make([]byte, 1048576)
	utils.RandRead(buf)
	s.cache(k5, NewPage(buf), true, false)
	time.Sleep(time.Second * 5) // wait for cache file flushed

	check := func(key string, off int64, size int) error {
		rc, err := s.load(key)
		if err != nil {
			t.Logf("CacheStore files in %s:", s.dir)
			filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					t.Logf("error accessing %s: %v", path, err)
					return nil
				}
				t.Logf("cache file: %s", path)
				return nil
			})
			t.Fatalf("CacheStore load key %s: %s", key, err)
		}
		defer rc.Close()
		buf := make([]byte, size)
		_, err = rc.ReadAt(buf, off)
		return err
	}
	cases := []struct {
		key    string
		off    int64
		size   int
		expect bool
	}{
		{k1, 0, 10, true},
		{k1, 3, 5, true},
		{k2, 0, 10, true},
		{k2, 3, 5, true},
		{k3, 0, 102400, true},
		{k3, 8192, 92160, true}, // 8K ~ 98K
		{k4, 0, 102400, true},
		{k4, 8192, 92160, true}, // only CsExtend can detect the error
		{k5, 0, 1048576, true},
		{k5, 131072, 131072, true},
		{k5, 102400, 512000, true},
	}
	for _, l := range []string{CsNone, CsFull, CsShrink, CsExtend} {
		s.checksum = l
		if l != CsNone {
			cases[6].expect = false
		}
		if l == CsExtend {
			cases[7].expect = false
		}
		for _, c := range cases {
			if err = check(c.key, c.off, c.size); (err == nil) != c.expect {
				t.Fatalf("CacheStore check level %s case %+v: %s", l, c, err)
			}
		}
	}
}

func TestExpand(t *testing.T) {
	rs := expandDir("/not/exists/jfsCache")
	if len(rs) != 1 || rs[0] != "/not/exists/jfsCache" {
		t.Errorf("expand: %v", rs)
		t.FailNow()
	}

	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "aaa1"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa2"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3", "jfscache"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3", "jfscache", "jfs"), 0755)

	rs = expandDir(filepath.Join(dir, "aaa*", "jfscache", "jfs"))
	if len(rs) != 3 || rs[0] != filepath.Join(dir, "aaa1", "jfscache", "jfs") {
		t.Errorf("expand: %v", rs)
		t.FailNow()
	}
}

func BenchmarkLoadCached(b *testing.B) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	s := newCacheStore(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
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
	s := newCacheStore(nil, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
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

func shutdownStore(s *cacheStore) {
	s.stateLock.Lock()
	defer s.stateLock.Unlock()
	s.state.stop()
	s.state = newDCState(dcDown, s)
}

func TestCacheManager(t *testing.T) {
	conf := defaultConf
	dir0, dir1, dir2 := t.TempDir(), t.TempDir(), t.TempDir()
	conf.CacheDir = dir0 + ":" + dir1 + ":" + dir2
	conf.AutoCreate = true
	manager := newCacheManager(&conf, nil, nil)
	require.True(t, !manager.isEmpty())

	m, ok := manager.(*cacheManager)
	require.True(t, ok)
	require.Equal(t, 3, m.length())

	// case: key rehash after store removal
	k1 := "k1"
	p1 := NewPage([]byte{1, 2, 3})
	defer p1.Release()
	m.cache(k1, p1, true, false)

	s1 := m.getStore(k1)
	require.NotNil(t, s1)

	m.Lock()
	shutdownStore(s1)
	m.Unlock()
	time.Sleep(3 * time.Second)

	rc, _ := m.load(k1)
	require.Nil(t, rc)
	_, exist := m.exist(k1)
	require.False(t, exist)

	s2 := m.getStore(k1)
	require.NotNil(t, s2)

	// case: remove all store
	m.Lock()
	for _, s := range m.storeMap {
		shutdownStore(s)
	}
	m.Unlock()
	time.Sleep(3 * time.Second)
	require.True(t, m.isEmpty())
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
	cache := newCacheStore(nil, conf.CacheDir, 1<<30, 1000, 1, &conf, nil)

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
	cache := newCacheStore(nil, conf.CacheDir, 1<<30, 1000, 1, &conf, nil)

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
		s := newCacheStore(m, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)

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
		s := newCacheStore(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
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
		s := newCacheStore(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
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
		s := newCacheStore(m, filepath.Join(dir, "diskCache"), int64(conf.CacheSize), conf.CacheItems, 1, &conf, nil)
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
	cache := newCacheStore(m, dir, 1<<30, 1000, 1, &conf, nil)
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

func newTestCacheStore(dir string, conf *Config, uploader func(key, path string, force bool) bool) *cacheStore {
	keyIndex, _ := NewKeyIndex(conf)
	c := &cacheStore{
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

func TestOpenCacheFileReads(t *testing.T) {
	cases := []struct {
		name        string
		dataSize    int
		hasChecksum bool
		hasFooter   bool
		tier        uint8
		openLevel   string
		wantCsLevel string
		wantTier    uint8
	}{
		{name: "data only", dataSize: 64 << 10, openLevel: CsFull, wantCsLevel: CsNone},
		{name: "checksum only", dataSize: 64 << 10, hasChecksum: true, openLevel: CsFull, wantCsLevel: CsFull},
		{name: "checksum and tier", dataSize: 64 << 10, hasChecksum: true, hasFooter: true, tier: 2, openLevel: CsFull, wantCsLevel: CsFull, wantTier: 2},
		{name: "tier only", dataSize: 1 << 10, hasFooter: true, tier: 3, openLevel: CsNone, wantCsLevel: CsNone, wantTier: 3},
		// Regression: 80KiB data => checksumLength = 12 bytes, close to the
		// trailer size; a tier-only file must not be mistaken for checksum-only.
		{name: "tier without checksum no collision", dataSize: 80 << 10, hasFooter: true, tier: 2, openLevel: CsExtend, wantCsLevel: CsNone, wantTier: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.dataSize)
			utils.RandRead(data)

			path := filepath.Join(t.TempDir(), "cache")
			f, err := os.Create(path)
			require.NoError(t, err)
			_, err = f.Write(data)
			require.NoError(t, err)
			if c.hasChecksum {
				_, err = f.Write(checksum(data))
				require.NoError(t, err)
			}
			if c.hasFooter {
				_, err = f.Write(marshalFooter(t, c.tier, c.hasChecksum))
				require.NoError(t, err)
			}
			require.NoError(t, f.Close())

			cf, err := openCacheFile(path, len(data), c.openLevel)
			require.NoError(t, err)
			defer cf.Close()
			require.Equal(t, c.wantCsLevel, cf.csLevel)

			got := make([]byte, len(data))
			n, err := cf.ReadAt(got, 0)
			require.NoError(t, err)
			require.Equal(t, len(data), n)
			require.Equal(t, data, got)

			var ft stageFooter
			if c.hasFooter {
				require.NotZero(t, cf.footerOff)
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, c.wantTier, ft.Tier)
			} else {
				// A file without a footer defaults to tier 0.
				require.Zero(t, cf.footerOff)
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, uint8(0), ft.Tier)
			}
		})
	}
}

func marshalFooter(t *testing.T, tier uint8, hasChecksum bool) []byte {
	t.Helper()
	f := stageFooter{Tier: tier}
	b, err := f.marshal(hasChecksum)
	require.NoError(t, err)
	return b
}

// decodeStageFooter parses a marshaled footer ([magic][len][msgpack]) and
// returns the decoded metadata, verifying the framing along the way.
func decodeStageFooter(t *testing.T, b []byte) stageFooter {
	t.Helper()
	require.GreaterOrEqual(t, len(b), 4)
	require.Equal(t, stageFooterMagic, binary.BigEndian.Uint16(b[:2]))
	size := int(binary.BigEndian.Uint16(b[2:4]))
	require.Equal(t, size, len(b)-4)
	var m stageFooter
	require.NoError(t, msgpack.Unmarshal(b[4:], &m))
	return m
}

func TestEncodeStageFooterLengthParity(t *testing.T) {
	// The encoded length must be a multiple of 4 iff a checksum is present, and
	// the stored tier must round-trip regardless of the padding.
	for tier := uint8(0); tier <= maxTierID; tier++ {
		for _, hasChecksum := range []bool{true, false} {
			b := marshalFooter(t, tier, hasChecksum)
			require.Equal(t, hasChecksum, len(b)%4 == 0,
				"tier=%d hasChecksum=%v len=%d", tier, hasChecksum, len(b))

			require.Equal(t, tier, decodeStageFooter(t, b).Tier)
		}
	}
}

func TestOpenCacheFileRejectsInvalidSize(t *testing.T) {
	cases := []struct {
		name      string
		dataSize  int
		truncate  int    // bytes dropped from the end of the data
		trailer   []byte // extra bytes appended after the data
		openLevel string
	}{
		{
			// 40KiB data => checksumLength = 8 bytes; an "extra" that is a
			// multiple of 4 but smaller than the checksum length is impossible.
			name:      "extra smaller than checksum",
			dataSize:  40 << 10,
			trailer:   []byte{0, 0, 0, 0},
			openLevel: CsFull,
		},
		{
			name:      "truncated data",
			dataSize:  1 << 10,
			truncate:  1,
			openLevel: CsNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.dataSize)
			utils.RandRead(data)

			content := make([]byte, 0, len(data))
			content = append(content, data[:len(data)-c.truncate]...)
			content = append(content, c.trailer...)
			path := filepath.Join(t.TempDir(), "cache")
			require.NoError(t, os.WriteFile(path, content, 0600))

			_, err := openCacheFile(path, len(data), c.openLevel)
			require.Error(t, err)
		})
	}
}

func TestOpenCacheFileParsesStageFooter(t *testing.T) {
	data := make([]byte, 1024)
	utils.RandRead(data)

	badMagic := marshalFooter(t, 2, false)
	badMagic[0] ^= 0xff // corrupt the magic without changing the length

	cases := []struct {
		name     string
		footer   []byte
		wantErr  bool
		wantTier uint8
	}{
		{name: "truncated header", footer: []byte{0x46}, wantErr: true},                                // shorter than the 4-byte header
		{name: "bad magic", footer: badMagic, wantErr: true},                                           // valid length, wrong magic
		{name: "trailing data", footer: append(marshalFooter(t, 2, false), 0, 0, 0, 0), wantErr: true}, // extra bytes after the footer
		{name: "invalid tier clamped to zero", footer: marshalFooter(t, 9, false), wantTier: 0},        // out-of-range tier -> default
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache")
			f, err := os.Create(path)
			require.NoError(t, err)
			_, err = f.Write(data)
			require.NoError(t, err)
			_, err = f.Write(c.footer)
			require.NoError(t, err)
			require.NoError(t, f.Close())

			// openCacheFile only validates sizes; the footer content is
			// validated when it is unmarshaled.
			cf, err := openCacheFile(path, len(data), CsNone)
			require.NoError(t, err)
			defer cf.Close()

			var ft stageFooter
			err = ft.unmarshal(cf)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantTier, ft.Tier)
		})
	}
}

// writeStageFile writes a stage file laid out as [data][checksum?][footer] and
// returns its path.
func writeStageFile(tb testing.TB, dir, name string, data []byte, tier uint8, hasChecksum bool) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(tb, err)
	_, err = f.Write(data)
	require.NoError(tb, err)
	if hasChecksum {
		_, err = f.Write(checksum(data))
		require.NoError(tb, err)
	}
	sf := stageFooter{Tier: tier}
	fb, err := sf.marshal(hasChecksum)
	require.NoError(tb, err)
	_, err = f.Write(fb)
	require.NoError(tb, err)
	require.NoError(tb, f.Close())
	return path
}

func TestStageFooterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 64*1024)
	utils.RandRead(data)
	for tier := uint8(0); tier <= maxTierID; tier++ {
		for _, hasChecksum := range []bool{true, false} {
			name := fmt.Sprintf("tier%d_cs%v", tier, hasChecksum)
			t.Run(name, func(t *testing.T) {
				path := writeStageFile(t, dir, name, data, tier, hasChecksum)
				level := CsNone
				if hasChecksum {
					level = CsFull
				}

				cf, err := openCacheFile(path, len(data), level)
				require.NoError(t, err)
				defer cf.Close()

				got := make([]byte, len(data))
				n, err := cf.ReadAt(got, 0)
				require.NoError(t, err)
				require.Equal(t, len(data), n)
				require.Equal(t, data, got)

				var ft stageFooter
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, tier, ft.Tier)
			})
		}
	}
}
