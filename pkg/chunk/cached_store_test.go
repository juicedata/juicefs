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
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func forgetSlice(store ChunkStore, sliceId uint64, size int) error {
	w := store.NewWriter(sliceId)
	buf := bytes.Repeat([]byte{0x41}, size)
	if _, err := w.WriteAt(buf, 0); err != nil {
		return err
	}
	return w.Finish(size)
}

func testStore(t *testing.T, store ChunkStore) {
	writer := store.NewWriter(1)
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
	CacheDir:          filepath.Join(os.TempDir(), "diskCache"),
	CacheMode:         0600,
	CacheSize:         10 << 20,
	CacheChecksum:     CsNone,
	CacheScanInterval: time.Second * 300,
	MaxUpload:         1,
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
	config.UploadDelay = time.Hour
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	cleanCache := func() {
		rSlice := sliceForRead(1, 1024, store.(*cachedStore))
		keys := rSlice.keys()
		for _, k := range keys {
			store.(*cachedStore).bcache.remove(k, true)
		}
	}
	readSlice := func(id uint64, length int) error {
		p := NewPage(make([]byte, length))
		r := store.NewReader(id, length)
		_, err := r.ReadAt(context.Background(), p, 0)
		return err
	}

	// write to cache
	w := store.NewWriter(1)
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
	w = store.NewWriter(2)
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
	if err := store.FillCache(10, 1024); err != nil {
		t.Fatalf("fill cache 10 1024: %s", err)
	}
	if err := store.FillCache(11, uint32(bsize)); err != nil {
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
	err := store.CheckCache(10, 1024, handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	// evict slice 11
	err = store.EvictCache(11, uint32(bsize))
	assert.Nil(t, err)

	// stat
	if cnt, used := bcache.stats(); cnt != 1 || used != 1024+4096 { // only chunk 10 cached
		t.Fatalf("cache cnt %d used %d, expect cnt 1 used 5120", cnt, used)
	}

	// check again
	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(bsize), missBytes)
}

func BenchmarkCachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	w := store.NewWriter(1)
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
	w := store.NewWriter(2)
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
