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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Writeback = true
	p := filepath.Join(conf.CacheDir, stagingDir, "chunks/0/0/123_0_4")
	os.MkdirAll(filepath.Dir(p), 0744)
	f, _ := os.Create(p)
	f.WriteString("good")
	f.Close()
	store := NewCachedStore(mem, conf, nil)
	time.Sleep(time.Millisecond * 50) // wait for scan to finish
	in, err := mem.Get(ctx, "chunks/0/0/123_0_4", 0, -1)
	if err != nil {
		t.Fatalf("staging object should be upload")
	}
	data, _ := io.ReadAll(in)
	if string(data) != "good" {
		t.Fatalf("data %s != expect good", data)
	}
	testStore(t, store)
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
