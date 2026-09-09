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

package vfs

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type countingStorage struct {
	object.ObjectStorage
	gets atomic.Int32
}

func (s *countingStorage) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	s.gets.Add(1)
	return s.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

// createCachedTestReader writes data as one slice at sliceOff of a new file
// and returns a data reader backed by a memory block cache that keeps full
// blocks.
func createCachedTestReader(t *testing.T, blockSize int, sliceOff uint32, data []byte) (*dataReader, *countingStorage, Ino) {
	t.Helper()
	metaConf := meta.DefaultConf()
	metaConf.MountPoint = "/jfs"
	m := meta.NewClient("memkv://", metaConf)
	format := &meta.Format{
		Name:        "test-" + uuid.New().String(),
		UUID:        uuid.New().String(),
		Storage:     "mem",
		BlockSize:   blockSize >> 10,
		Compression: "none",
		DirStats:    true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %v", err)
	}
	conf := &Config{
		Meta:   metaConf,
		Format: *format,
		Chunk: &chunk.Config{
			BlockSize:      blockSize,
			MaxUpload:      2,
			MaxDownload:    200,
			BufferSize:     4 << 20,
			Readahead:      1 << 20,
			CacheSize:      10 << 20,
			CacheDir:       "memory",
			CacheFullBlock: true,
		},
		FuseOpts: &FuseOptions{},
	}
	base, _ := object.CreateStorage("mem", "", "", "", "")
	blob := &countingStorage{ObjectStorage: base}
	store := chunk.NewCachedStore(blob, *conf.Chunk, nil)

	ctx := meta.Background()
	var inode meta.Ino
	var attr meta.Attr
	if st := m.Create(ctx, meta.RootInode, "file", 0644, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("create file: %s", st)
	}
	var sliceID uint64
	if st := m.NewSlice(ctx, &sliceID); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	w := store.NewWriter(sliceID, 0)
	if _, err := w.WriteAt(data, 0); err != nil {
		t.Fatalf("write slice: %s", err)
	}
	if err := w.Finish(len(data)); err != nil {
		t.Fatalf("finish slice: %s", err)
	}
	if st := m.Write(ctx, inode, 0, sliceOff, meta.Slice{Id: sliceID, Size: uint32(len(data)), Len: uint32(len(data))}, time.Now()); st != 0 {
		t.Fatalf("write meta: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_RDONLY, &attr); st != 0 {
		t.Fatalf("open: %s", st)
	}
	return NewDataReader(conf, m, store).(*dataReader), blob, Ino(inode)
}

func hasSlices(fr FileReader) bool {
	f := fr.(*fileReader)
	f.Lock()
	defer f.Unlock()
	return f.slices != nil
}

func TestReadCached(t *testing.T) {
	const blockSize = 64 << 10
	// 6 blocks; reads stay away from block 0 and the last 32 KiB, where the
	// reader starts readahead on its own.
	data := make([]byte, 6*blockSize)
	for i := range data {
		data[i] = byte(i * 7)
	}
	dr, blob, inode := createCachedTestReader(t, blockSize, 0, data)
	ctx := meta.Background()
	check := func(fr FileReader, off uint64, size int) {
		t.Helper()
		buf := make([]byte, size)
		if n, st := fr.Read(ctx, off, buf); st != 0 || n != size {
			t.Fatalf("read at %d: (%d,%s)", off, n, st)
		}
		if !bytes.Equal(buf, data[off:off+uint64(size)]) {
			t.Fatalf("read at %d: data mismatch", off)
		}
	}

	// cold: the slice reader fetches block 1 and caches it
	fr := dr.Open(inode, uint64(len(data)))
	check(fr, blockSize, 4096)
	if !hasSlices(fr) {
		t.Fatalf("uncached read should go through a slice reader")
	}
	if got := blob.gets.Load(); got == 0 {
		t.Fatalf("uncached read should reach the object storage")
	}
	fr.Close(ctx)

	// warm: served from the cache on the calling goroutine, no slice reader
	fr = dr.Open(inode, uint64(len(data)))
	defer fr.Close(ctx)
	gets := blob.gets.Load()
	check(fr, blockSize+1024, 4096)
	if hasSlices(fr) {
		t.Fatalf("cached read should not create a slice reader")
	}
	if got := blob.gets.Load(); got != gets {
		t.Fatalf("cached read should not reach the object storage: %d -> %d", gets, got)
	}

	// partly cached (block 1 is, block 2 is not): falls back to the slice reader
	check(fr, 2*blockSize-2048, 4096)
	if !hasSlices(fr) {
		t.Fatalf("partly cached read should fall back to a slice reader")
	}
	if got := blob.gets.Load(); got == gets {
		t.Fatalf("partly cached read should fetch the missing block")
	}
}

func TestReadCachedHole(t *testing.T) {
	const blockSize = 64 << 10
	// blocks 0-1 are a hole, blocks 2-3 hold data
	data := make([]byte, 2*blockSize)
	for i := range data {
		data[i] = byte(i*7 + 1)
	}
	dr, blob, inode := createCachedTestReader(t, blockSize, 2*blockSize, data)
	ctx := meta.Background()
	fr := dr.Open(inode, 4*blockSize)
	defer fr.Close(ctx)

	// inside the hole: zeros, nothing to fetch, no slice reader
	buf := make([]byte, 4096)
	buf[0] = 1
	if n, st := fr.Read(ctx, blockSize, buf); st != 0 || n != len(buf) {
		t.Fatalf("read in hole: (%d,%s)", n, st)
	}
	if !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatalf("read in hole should return zeros")
	}
	if hasSlices(fr) || blob.gets.Load() != 0 {
		t.Fatalf("read in hole should not need a slice reader or the object storage")
	}

	// hole plus uncached data: regular path
	off := uint64(2*blockSize - 2048)
	if n, st := fr.Read(ctx, off, buf); st != 0 || n != len(buf) {
		t.Fatalf("read across hole: (%d,%s)", n, st)
	}
	if !bytes.Equal(buf[:2048], make([]byte, 2048)) || !bytes.Equal(buf[2048:], data[:2048]) {
		t.Fatalf("read across hole: data mismatch")
	}
	if !hasSlices(fr) {
		t.Fatalf("read of uncached data should go through a slice reader")
	}
}

func TestReadCachedFallbacks(t *testing.T) {
	const blockSize = 64 << 10
	data := make([]byte, 6*blockSize)
	dr, _, inode := createCachedTestReader(t, blockSize, 0, data)
	ctx := meta.Background()
	buf := make([]byte, 4096)

	fr := dr.Open(inode, uint64(len(data))).(*fileReader)
	if _, ok := fr.readCached(ctx, uint64(len(data))-2048, buf); ok {
		t.Fatalf("read past the end should not take the fast path")
	}
	if _, ok := fr.readCached(ctx, 0, buf); ok {
		t.Fatalf("read at offset 0 should not take the fast path")
	}
	fr.Close(ctx)
	if _, ok := fr.readCached(ctx, blockSize, buf); ok {
		t.Fatalf("read on a closed reader should not take the fast path")
	}

	// the inode does not exist: meta lookup fails
	fr = dr.Open(inode+1000, uint64(len(data))).(*fileReader)
	defer fr.Close(ctx)
	if _, ok := fr.readCached(ctx, blockSize, buf); ok {
		t.Fatalf("read of an unknown inode should not take the fast path")
	}

	// the chunk store cannot read from the cache alone
	store := &blockingChunkStore{reader: &blockingChunkReader{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}}
	bdr, binode := createCancellationTestReader(t, store)
	bfr := bdr.Open(binode, 4).(*fileReader)
	defer bfr.Close(ctx)
	if _, ok := bfr.readCached(ctx, 1, buf[:3]); ok {
		t.Fatalf("store without CachedReader should not take the fast path")
	}
}
