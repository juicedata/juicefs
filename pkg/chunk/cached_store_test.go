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

//nolint:errcheck
package chunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davies/groupcache/consistenthash"
	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/murmur3"
)

func forgetSlice(store ChunkStore, sliceId uint64, size int) error {
	w := store.NewWriter(sliceId, 0)
	buf := bytes.Repeat([]byte{0x41}, size)
	if _, err := w.WriteAt(buf, 0); err != nil {
		return err
	}
	return w.Finish(size)
}

func testStore(t *testing.T, store ChunkStore) {
	writer := store.NewWriter(1, 0)
	data := []byte("hello world")
	if n, err := writer.WriteAt(data, 0); n != 11 || err != nil {
		t.Fatalf("write fail: %d %s", n, err)
	}
	offset := defaultConf.BlockSize - 3
	if n, err := writer.WriteAt(data, int64(offset)); err != nil || n != 11 {
		t.Fatalf("write fail: %d %s", n, err)
	}
	if err := writer.FlushTo(defaultConf.BlockSize + 3); err != nil {
		t.Fatalf("flush fail: %s", err)
	}
	size := offset + len(data)
	if err := writer.Finish(size); err != nil {
		t.Fatalf("finish fail: %s", err)
	}
	defer store.Remove(1, size)

	reader := store.NewReader(1, size)
	p := NewPage(make([]byte, 5))
	if n, err := reader.ReadAt(context.Background(), p, 6); n != 5 || err != nil {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
	p = NewPage(make([]byte, 5))
	if n, err := reader.ReadAt(context.Background(), p, 0); n != 5 || err != nil {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "hello" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
	p = NewPage(make([]byte, 20))
	if n, err := reader.ReadAt(context.Background(), p, offset); n != 11 || err != nil && err != io.EOF {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "hello world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}

	bsize := defaultConf.BlockSize / 2
	errs := make(chan error, 3)
	for i := 2; i < 5; i++ {
		go func(sliceId uint64) {
			if err := forgetSlice(store, sliceId, bsize); err != nil {
				errs <- err
				return
			}
			time.Sleep(time.Millisecond * 100) // waiting for flush
			errs <- store.Remove(sliceId, bsize)
		}(uint64(i))
	}
	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("test concurrent write failed: %s", err)
		}
	}
}

var defaultConf = Config{
	BlockSize:         1 << 20,
	CacheDir:          filepath.Join(os.TempDir(), fmt.Sprintf("diskCache-%d", os.Getpid())),
	CacheMode:         0600,
	CacheSize:         10 << 20,
	CacheChecksum:     CsNone,
	CacheScanInterval: time.Second * 300,
	MaxUpload:         1,
	MaxDownload:       200,
	MaxRetries:        10,
	PutTimeout:        time.Second,
	GetTimeout:        time.Second * 2,
	AutoCreate:        true,
	BufferSize:        10 << 20,
}

var ctx = context.Background()

func TestStoreDefault(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	_ = os.RemoveAll(defaultConf.CacheDir)
	store := NewCachedStore(mem, defaultConf, nil)
	testStore(t, store)
	if used := store.UsedMemory(); used != 0 {
		t.Fatalf("used memory %d != expect 0", used)
	}
	if cnt, used := store.(*cachedStore).bcache.stats(); cnt != 0 || used != 0 {
		t.Fatalf("cache cnt %d used %d, expect both 0", cnt, used)
	}
}

func TestStoreMemCache(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.CacheDir = "memory"
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
	if used := store.UsedMemory(); used != 0 {
		t.Fatalf("used memory %d != expect 0", used)
	}
	if cnt, used := store.(*cachedStore).bcache.stats(); cnt != 0 || used != 0 {
		t.Fatalf("cache cnt %d used %d, expect both 0", cnt, used)
	}
}
func TestStoreCompressed(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Compress = "lz4"
	conf.AutoCreate = false
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreLimited(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.UploadLimit = 1e6
	conf.DownloadLimit = 1e6
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreFull(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.FreeSpace = 0.9999
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreSmallBuffer(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.BufferSize = 1 << 20
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

type blockingStageCache struct {
	CacheManager
	started chan struct{}
	release chan struct{}
	removed chan string
}

func (c *blockingStageCache) cache(string, *Page, bool, bool) {}

func (c *blockingStageCache) stage(string, []byte, uint8) (string, error) {
	close(c.started)
	<-c.release
	return "late-stage", nil
}

func (c *blockingStageCache) removeStage(key string) error {
	c.removed <- key
	return nil
}

func TestWritebackStageTimeoutRemovesLateStage(t *testing.T) {
	mem, err := object.CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	cache := &blockingStageCache{
		started: make(chan struct{}),
		release: make(chan struct{}),
		removed: make(chan string, 1),
	}
	conf := defaultConf
	conf.Compress = "lz4"
	conf.PutTimeout = 20 * time.Millisecond
	conf.Writeback = true
	conf.WritebackThresholdSize = conf.BlockSize + 1
	store := &cachedStore{
		storage:       mem,
		conf:          conf,
		bcache:        cache,
		currentUpload: make(chan struct{}, 1),
		compressor:    compress.NewCompressor(conf.Compress),
	}
	store.initMetrics()

	writer := store.NewWriter(123, 0)
	data := []byte("late")
	_, err = writer.WriteAt(data, 0)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- writer.Finish(len(data)) }()

	select {
	case <-cache.started:
	case <-time.After(time.Second):
		t.Fatal("stage did not start")
	}
	timer := time.AfterFunc(5*conf.PutTimeout, func() { close(cache.release) })
	defer timer.Stop()

	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("direct upload did not finish after stage timeout")
	}
	select {
	case key := <-cache.removed:
		require.Equal(t, "chunks/0/0/123_0_4", key)
	case <-time.After(time.Second):
		t.Fatal("late stage was not removed")
	}
}

func TestStoreAsync(t *testing.T) {
	for _, writeback := range []bool{false, true} {
		t.Run(fmt.Sprintf("writeback=%t", writeback), func(t *testing.T) {
			mem, err := object.CreateStorage("mem", "", "", "", "")
			require.NoError(t, err)
			conf := defaultConf
			conf.CacheDir = t.TempDir()
			conf.Writeback = writeback
			key := "chunks/0/0/123_0_4"
			p := filepath.Join(conf.CacheDir, stagingDir, key)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0744))
			require.NoError(t, os.WriteFile(p, []byte("good"), 0600))
			store := NewCachedStore(mem, conf, nil).(*cachedStore)
			if !writeback {
				cache := store.bcache.(*cacheManager)
				for _, force := range []bool{false, true} {
					require.False(t, cache.stores[0].uploader(key, p, force))
				}
				require.Eventually(t, func() bool {
					return toFloat64(cache.metrics.stageBlocks) == 1 && toFloat64(cache.metrics.stageBlockBytes) == 4
				}, time.Second, time.Millisecond*10)
				store.pendingMutex.Lock()
				pending := len(store.pendingKeys)
				store.pendingMutex.Unlock()
				require.Zero(t, pending)
				require.Empty(t, store.pendingCh)
				data, err := os.ReadFile(p)
				require.NoError(t, err)
				require.Equal(t, "good", string(data))
				_, err = mem.Head(ctx, key)
				require.Error(t, err)
			} else {
				require.Eventually(t, func() bool {
					_, err := mem.Head(ctx, key)
					return err == nil
				}, time.Second, time.Millisecond*10)
				in, err := mem.Get(ctx, key, 0, -1)
				require.NoError(t, err)
				defer in.Close()
				data, err := io.ReadAll(in)
				require.NoError(t, err)
				require.Equal(t, "good", string(data))
				require.Eventually(t, func() bool {
					_, err := os.Stat(p)
					return os.IsNotExist(err)
				}, time.Second, time.Millisecond*10)
			}
			testStore(t, store)
		})
	}
}

func TestForceUpload(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	_ = os.RemoveAll(config.CacheDir)
	config.Writeback = true
	config.WritebackThresholdSize = config.BlockSize + 1
	config.UploadDelay = time.Hour
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	cleanCache := func() {
		rSlice := sliceForRead(1, 1024, store.(*cachedStore))
		for _, i := range rSlice.blockIndexes(nil) {
			store.(*cachedStore).bcache.remove(rSlice.key(i), true)
		}
	}
	readSlice := func(id uint64, length int) error {
		p := NewPage(make([]byte, length))
		r := store.NewReader(id, length)
		_, err := r.ReadAt(context.Background(), p, 0)
		return err
	}

	// write to cache
	w := store.NewWriter(1, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	cleanCache()
	if readSlice(1, 1024) == nil {
		t.Fatalf("read slice 1 should fail")
	}

	// write to os
	w = store.NewWriter(2, 0)
	w.SetWriteback(false)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	cleanCache()
	if readSlice(2, 1024) != nil {
		t.Fatalf("check slice 2 should success")
	}
}

func TestStoreDelayed(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Writeback = true
	conf.UploadDelay = time.Millisecond * 200
	store := NewCachedStore(mem, conf, nil)
	time.Sleep(time.Second) // waiting for cache scanned
	testStore(t, store)
	if err := forgetSlice(store, 10, 1024); err != nil {
		t.Fatalf("forge slice 10 1024: %s", err)
	}
	defer store.Remove(10, 1024)
	time.Sleep(time.Second) // waiting for upload
	if _, err := mem.Head(ctx, "chunks/0/0/10_0_1024"); err != nil {
		t.Fatalf("head object 10_0_1024: %s", err)
	}
}

func TestStoreMultiBuckets(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.HashPrefix = true
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestFillCache(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.CacheSize = 10 << 20
	conf.FreeSpace = 0.01
	_ = os.RemoveAll(conf.CacheDir)
	store := NewCachedStore(mem, conf, nil)
	if err := forgetSlice(store, 10, 1024); err != nil {
		t.Fatalf("forge slice 10 1024: %s", err)
	}
	defer store.Remove(10, 1024)
	bsize := conf.BlockSize
	if err := forgetSlice(store, 11, bsize); err != nil {
		t.Fatalf("forge slice 11 %d: %s", bsize, err)
	}
	defer store.Remove(11, bsize)

	time.Sleep(time.Millisecond * 100) // waiting for flush
	bcache := store.(*cachedStore).bcache
	if cnt, used := bcache.stats(); cnt != 1 || used != 1024+4096 { // only chunk 10 cached
		t.Fatalf("cache cnt %d used %d, expect cnt 1 used 5120", cnt, used)
	}
	if err := store.FillCache(10, 1024, nil); err != nil {
		t.Fatalf("fill cache 10 1024: %s", err)
	}
	if err := store.FillCache(11, uint32(bsize), nil); err != nil {
		t.Fatalf("fill cache 11 %d: %s", bsize, err)
	}
	time.Sleep(time.Second)
	expect := int64(1024 + 4096 + bsize + 4096)
	if cnt, used := bcache.stats(); cnt != 2 || used != expect {
		t.Fatalf("cache cnt %d used %d, expect cnt 2 used %d", cnt, used, expect)
	}

	var missBytes uint64
	handler := func(exists bool, loc string, size int) {
		if !exists {
			missBytes += uint64(size)
		}
	}
	// check
	err := store.CheckCache(10, 1024, nil, handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), nil, handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	// evict slice 11
	err = store.EvictCache(11, uint32(bsize), nil)
	assert.Nil(t, err)

	// stat
	if cnt, used := bcache.stats(); cnt != 1 || used != 1024+4096 { // only chunk 10 cached
		t.Fatalf("cache cnt %d used %d, expect cnt 1 used 5120", cnt, used)
	}

	// check again
	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), nil, handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(bsize), missBytes)
}

type fillCacheGET struct {
	key        string
	off, limit int64
}

type fillCacheBlockedStore struct {
	object.ObjectStorage
	entered  chan fillCacheGET
	release  <-chan struct{}
	calls    atomic.Int64
	bytes    atomic.Int64
	firstErr error
}

func (s *fillCacheBlockedStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	seq := s.calls.Add(1)
	select {
	case s.entered <- fillCacheGET{key, off, limit}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if seq == 1 && s.firstErr != nil {
		return nil, s.firstErr
	}
	r, err := s.ObjectStorage.Get(ctx, key, off, limit, getters...)
	if err != nil {
		return nil, err
	}
	return &fillCacheCountingReader{ReadCloser: r, bytes: &s.bytes}, nil
}

type fillCacheCountingReader struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (r *fillCacheCountingReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytes.Add(int64(n))
	return n, err
}

type fillCacheBlockingCache struct {
	CacheManager
	entered chan [2]bool
	release <-chan struct{}
}

func (c *fillCacheBlockingCache) cache(key string, p *Page, force, dropCache bool) {
	c.entered <- [2]bool{force, dropCache}
	<-c.release
	c.CacheManager.cache(key, p, force, dropCache)
}

func TestFillCacheConcurrentRead(t *testing.T) {
	cases := []struct {
		name           string
		warmup         bool
		firstErr       error
		cancelRead     bool
		disk           bool
		blockCache     bool
		compress       string
		prefetch       bool
		cacheFullBlock bool
	}{
		{name: "read-read"},
		{name: "read-warmup", warmup: true},
		{name: "read-warmup-error", warmup: true, firstErr: errors.New("injected GET failure")},
		{name: "read-warmup-canceled", warmup: true, cancelRead: true},
		{name: "read-warmup-disk", warmup: true, disk: true},
		{name: "read-warmup-blocked-cache", warmup: true, blockCache: true},
		{name: "read-warmup-compressed", warmup: true, compress: "zstd"},
		{name: "prefetch-warmup", warmup: true, prefetch: true},
		{name: "read-warmup-cache-full-block", warmup: true, cacheFullBlock: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const blockSize = 4096
			data := bytes.Repeat([]byte{0x5a}, blockSize)
			mem, err := object.CreateStorage("mem", "", "", "", "")
			require.NoError(t, err)
			release := make(chan struct{})
			backend := &fillCacheBlockedStore{ObjectStorage: mem, entered: make(chan fillCacheGET, 2), release: release, firstErr: tc.firstErr}
			conf := defaultConf
			conf.BlockSize = blockSize
			conf.CacheDir = "memory"
			conf.CacheEviction = Eviction2Random
			conf.CacheFullBlock = tc.cacheFullBlock
			conf.Compress = tc.compress
			conf.Prefetch = 0
			conf.GetTimeout = 10 * time.Second
			store := NewCachedStore(backend, conf, nil).(*cachedStore)
			if tc.disk {
				// Exercise disk admission and flushing without background space checks.
				cache := newTestCacheStore(t.TempDir()+string(filepath.Separator), &conf, nil)
				cache.id = "test"
				cache.m = store.bcache.getMetrics()
				cache.capacity = int64(conf.CacheSize)
				cache.checksum = conf.CacheChecksum
				mgr := &cacheManager{
					consistentMap: consistenthash.New(100, murmur3.Sum32),
					storeMap:      map[string]*diskCache{cache.id: cache},
					stores:        []*diskCache{cache},
					metrics:       cache.m,
				}
				mgr.consistentMap.Add(cache.id)
				store.bcache = mgr
				go cache.flush()
			}
			key := sliceForRead(10, blockSize, store).key(0)
			encoded := make([]byte, store.compressor.CompressBound(blockSize))
			n, err := store.compressor.Compress(encoded, data)
			require.NoError(t, err)
			require.NoError(t, mem.Put(context.Background(), key, bytes.NewReader(encoded[:n])))
			cnt, _ := store.bcache.stats()
			require.Zero(t, cnt)

			ctx, cancel := context.WithCancel(context.Background())
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			cacheRelease := make(chan struct{})
			var cacheOnce sync.Once
			unblockCache := func() { cacheOnce.Do(func() { close(cacheRelease) }) }
			blockedCache := &fillCacheBlockingCache{CacheManager: store.bcache, entered: make(chan [2]bool, 1), release: cacheRelease}
			if tc.blockCache {
				store.bcache = blockedCache
			}
			var workers sync.WaitGroup
			t.Cleanup(func() {
				unblock()
				unblockCache()
				cancel()
				workers.Wait()
				_ = store.EvictCache(10, blockSize, nil)
			})
			type result struct {
				warmup bool
				err    error
			}
			results := make(chan result, 2)
			pages := make(chan *Page, 2)
			read := func(ctx context.Context) error {
				page := NewPage(make([]byte, blockSize))
				pages <- page
				defer page.Release()
				n, err := store.NewReader(10, blockSize).ReadAt(ctx, page, 0)
				if err != nil {
					return err
				}
				if n != blockSize || !bytes.Equal(page.Data, data) {
					return fmt.Errorf("unexpected read: %d bytes, data matches: %t", n, bytes.Equal(page.Data, data))
				}
				return nil
			}
			start := func(fn func() error, warmup bool) {
				workers.Add(1)
				go func() {
					defer workers.Done()
					results <- result{warmup, fn()}
				}()
			}
			checkGET := func(req fillCacheGET) {
				require.Equal(t, fillCacheGET{key, 0, -1}, req)
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			if tc.prefetch {
				start(func() error { store.fetcher.op(key); return nil }, false)
			} else {
				start(func() error { return read(ctx) }, false)
			}
			select {
			case req := <-backend.entered:
				checkGET(req)
			case <-deadline.C:
				t.Fatal("first read did not enter GET")
			}
			var firstPage *Page
			if !tc.prefetch {
				firstPage = <-pages
			}
			if tc.warmup {
				start(func() error { return store.FillCache(10, blockSize, nil) }, true)
			} else {
				start(func() error { return read(context.Background()) }, false)
			}

			// Wait for a positive event while the first GET is still blocked.
			joined, duplicate := false, false
			for !joined && !duplicate {
				select {
				case req := <-backend.entered:
					checkGET(req)
					duplicate = true
				case <-deadline.C:
					t.Fatal("second operation neither joined the read nor entered GET")
				default:
					store.group.Lock()
					if req := store.group.rs[key]; req != nil {
						joined = req.dups == 1
					}
					store.group.Unlock()
					runtime.Gosched()
				}
			}
			firstErr := tc.firstErr
			if tc.cancelRead {
				firstErr = context.Canceled
				cancel()
			}
			checkResult := func(res result) {
				if !res.warmup && firstErr != nil {
					require.ErrorContains(t, res.err, firstErr.Error())
				} else {
					require.NoError(t, res.err)
				}
			}
			waitResult := func() result {
				select {
				case res := <-results:
					checkResult(res)
					return res
				case <-deadline.C:
					t.Fatal("operation did not finish")
					return result{}
				}
			}
			remaining := 2
			if tc.cancelRead {
				res := waitResult()
				require.False(t, res.warmup)
				remaining--
			}
			unblock()
			if tc.blockCache {
				select {
				case flags := <-blockedCache.entered:
					require.Equal(t, [2]bool{true, !store.conf.OSCache}, flags)
				case <-deadline.C:
					t.Fatal("warmup did not enter cache admission")
				}
				res := waitResult()
				require.False(t, res.warmup, "foreground read must finish while cache admission is blocked")
				require.Equal(t, int32(1), atomic.LoadInt32(&firstPage.refs), "warmup must retain the shared page")
				remaining--
				unblockCache()
			}
			for i := 0; i < remaining; i++ {
				waitResult()
			}
			workers.Wait()
			require.True(t, joined, "second operation must reuse the in-flight full-block download")
			var cachedPage *Page
			if tc.warmup {
				_, exists := store.bcache.exist(key)
				require.True(t, exists, "warmup must cache the block")
				require.NoError(t, read(context.Background()))
				if tc.disk {
					path := store.bcache.(*cacheManager).getStore(key).cachePath(key)
					require.Eventually(t, func() bool {
						cached, err := os.ReadFile(path)
						return err == nil && bytes.Equal(cached, data) && atomic.LoadInt32(&firstPage.refs) == 0
					}, 5*time.Second, time.Millisecond, "shared data must be flushed to disk and its page released")
				} else {
					r, err := store.bcache.load(key)
					require.NoError(t, err)
					cachedPage = r.(*pageReader).p
					require.NoError(t, r.Close())
					require.Equal(t, int32(1), atomic.LoadInt32(&cachedPage.refs), "only the cache should retain the page")
				}
			} else {
				cnt, _ = store.bcache.stats()
				require.Zero(t, cnt)
			}
			expectedGETs := int64(1)
			if firstErr != nil {
				expectedGETs++
			}
			cnt, _ = store.bcache.stats()
			t.Logf("joined=%t duplicate=%t GETs=%d bytes=%d cache_blocks=%d", joined, duplicate, backend.calls.Load(), backend.bytes.Load(), cnt)
			assert.Equal(t, expectedGETs, backend.calls.Load())
			assert.Equal(t, int64(n), backend.bytes.Load(), "successful data should be downloaded once")
			require.NoError(t, store.EvictCache(10, blockSize, nil))
			if cachedPage != nil {
				require.Zero(t, atomic.LoadInt32(&cachedPage.refs))
			}
			for len(pages) > 0 {
				p := <-pages
				require.Zero(t, atomic.LoadInt32(&p.refs))
			}
			if firstPage != nil {
				require.Eventually(t, func() bool { return atomic.LoadInt32(&firstPage.refs) == 0 }, 5*time.Second, time.Millisecond)
			}
		})
	}
}

func BenchmarkCachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	w := store.NewWriter(1, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	time.Sleep(time.Millisecond * 100)
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(1, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}

func BenchmarkUncachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	config.CacheSize = 0
	store := NewCachedStore(blob, config, nil)
	w := store.NewWriter(2, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(2, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}

type dStore struct {
	object.ObjectStorage
	cnt int32
}

func (s *dStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	atomic.AddInt32(&s.cnt, 1)
	return nil, errors.New("not found")
}

func TestStoreRetry(t *testing.T) {
	s := &dStore{}
	cs := NewCachedStore(s, defaultConf, nil)
	p := NewPage(nil)
	defer p.Release()
	cs.(*cachedStore).load(context.TODO(), "non", p, false, false) // wont retry
	require.Equal(t, int32(1), s.cnt)
}

func TestBlockIndexes(t *testing.T) {
	conf := defaultConf
	conf.BlockSize = 1 << 20 // 1MiB
	store := &cachedStore{conf: conf}
	const size = 4 << 20 // 4 blocks

	cases := []struct {
		name   string
		length int
		parts  []Range
		expect []int
	}{
		{"nil parts select every block", size, nil, []int{0, 1, 2, 3}},
		{"one part inside a block", size, []Range{{Off: 10, Len: 10}}, []int{0}},
		{"part spanning two blocks", size, []Range{{Off: (1 << 20) - 1, Len: 2}}, []int{0, 1}},
		{"last block only", size, []Range{{Off: 3 << 20, Len: 5}}, []int{3}},
		// two parts in the same block must not process it twice
		{"same block once", size, []Range{{Off: 0, Len: 10}, {Off: 1000, Len: 10}}, []int{0}},
		{"adjacent parts sharing a block", size, []Range{{Off: 0, Len: 1 << 20}, {Off: 1 << 20, Len: 10}}, []int{0, 1}},
		{"part beyond the object is clipped", size, []Range{{Off: 3 << 20, Len: 100 << 20}}, []int{3}},
		{"part starting past the end", size, []Range{{Off: 8 << 20, Len: 10}}, nil},
		{"empty part", size, []Range{{Off: 0, Len: 0}}, nil},
		{"zero length object", 0, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := sliceForRead(1, c.length, store)
			if got := r.blockIndexes(c.parts); !reflect.DeepEqual(got, c.expect) {
				t.Fatalf("blockIndexes(%v) = %v, want %v", c.parts, got, c.expect)
			}
		})
	}
}

// lateBody blocks on the first Read, so io.ReadFull stays pending past get-timeout.
type lateBody struct {
	data    []byte
	off     int
	blocked bool
}

func (b *lateBody) Read(p []byte) (int, error) {
	if !b.blocked {
		b.blocked = true
		time.Sleep(50 * time.Millisecond)
	}
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *lateBody) Close() error { return nil }

// lateStore completes a GET just after the caller gave up: it ignores cancellation and
// returns success a hair after get-timeout, with a body that has no bytes ready yet.
type lateStore struct {
	object.ObjectStorage
	data  []byte
	delay time.Duration
	seq   atomic.Int64
}

func (s *lateStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	time.Sleep(s.delay + time.Duration(s.seq.Add(1)%400)*time.Microsecond)
	return &lateBody{data: s.data}, nil
}

// Regression: a fetch abandoned by WithTimeout used to assign load's named return err,
// so a late `err = nil` could turn the timeout into a success and hand out a pooled page
// this read never filled - the block that previously occupied it. The page is pre-filled
// to stand in for that residue: a successful ReadAt must hold the requested block.
func TestReadAtNeverReportsSuccessWithUnfilledBuffer(t *testing.T) {
	const (
		bs          = 64 << 10
		iterations  = 400
		concurrency = 16
	)
	want := bytes.Repeat([]byte{'b'}, bs)
	residue := bytes.Repeat([]byte{'a'}, bs) // another block's bytes, left in a pooled page

	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.BlockSize = bs
	conf.CacheDir = t.TempDir()
	conf.GetTimeout = 5 * time.Millisecond
	conf.MaxRetries = 1
	slow := &lateStore{ObjectStorage: mem, data: want, delay: conf.GetTimeout}
	store := NewCachedStore(slow, conf, nil)

	var bad, ok, failed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < concurrency; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				sliceID := uint64(g*iterations + i + 1) // distinct key => one load per read
				page := NewOffPage(bs)
				copy(page.Data, residue)
				n, err := store.NewReader(sliceID, bs).ReadAt(context.Background(), page, 0)
				switch {
				case err != nil:
					failed.Add(1)
				case !bytes.Equal(page.Data[:n], want[:n]):
					bad.Add(1)
				default:
					ok.Add(1)
				}
				page.Release()
			}
		}(g)
	}
	wg.Wait()

	t.Logf("reads: ok=%d failed=%d CORRUPT=%d", ok.Load(), failed.Load(), bad.Load())
	require.Zero(t, bad.Load(), "ReadAt reported success but delivered another block's bytes")
}
