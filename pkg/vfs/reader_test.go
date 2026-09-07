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

// createCachedTestReader writes data as one slice of a new file and returns a
// data reader backed by a memory block cache that keeps full blocks.
func createCachedTestReader(t *testing.T, blockSize int, data []byte) (*dataReader, *countingStorage, Ino) {
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
	if st := m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceID, Size: uint32(len(data)), Len: uint32(len(data))}, time.Now()); st != 0 {
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
	dr, blob, inode := createCachedTestReader(t, blockSize, data)
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
