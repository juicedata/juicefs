/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"os"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
)

type cacheCall struct {
	action               CacheAction
	id                   uint64
	size, offset, length uint32
}

type recordingChunkStore struct {
	chunk.ChunkStore
	calls chan cacheCall
}

func (s *recordingChunkStore) FillCache(id uint64, size, offset, length uint32) (uint64, error) {
	s.calls <- cacheCall{WarmupCache, id, size, offset, length}
	return 4096, nil
}

func (s *recordingChunkStore) EvictCache(id uint64, size, offset, length uint32) (uint64, error) {
	s.calls <- cacheCall{EvictCache, id, size, offset, length}
	return 4096, nil
}

func (s *recordingChunkStore) CheckCache(id uint64, size, offset, length uint32, handler func(bool, string, int)) (uint64, error) {
	s.calls <- cacheCall{CheckCache, id, size, offset, length}
	return 4096, nil
}

func TestFill(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	entry, _ := v.Mkdir(ctx, 1, "test", 0777, 022)
	fe, fh, _ := v.Create(ctx, entry.Inode, "file", 0644, 0, uint32(os.O_WRONLY))
	_ = v.Write(ctx, fe.Inode, []byte("hello"), 0, fh)
	_ = v.Flush(ctx, fe.Inode, fh, 0)
	v.Release(ctx, fe.Inode, fh)
	_, _ = v.Symlink(ctx, "test/file", 1, "sym")
	_, _ = v.Symlink(ctx, "/tmp/testfile", 1, "sym2")
	_, _ = v.Symlink(ctx, "testfile", 1, "sym3")

	// normal cases
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/file", "/test", "/sym", "/"}, 2, nil)

	// remove chunk
	var slices []meta.Slice
	_ = v.Meta.Read(meta.Background(), fe.Inode, 0, &slices)
	for _, s := range slices {
		_ = v.Store.Remove(s.Id, int(s.Size))
	}
	// bad cases
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/file", "/sym2", "/sym3", "/.stats", "/not_exists"}, 2, nil)
}

func TestCacheFillerUsesVisibleSliceRange(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := meta.Background()
	var inode meta.Ino
	var attr meta.Attr
	if st := v.Meta.Create(ctx, meta.RootInode, "partial", 0644, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("create file: %s", st)
	}
	slice := meta.Slice{Id: 123, Size: 8 << 20, Off: 3 << 20, Len: 2 << 20}
	if st := v.Meta.Write(ctx, inode, 0, 0, slice, time.Now()); st != 0 {
		t.Fatalf("write slice: %s", st)
	}

	store := &recordingChunkStore{ChunkStore: v.Store, calls: make(chan cacheCall, 3)}
	v.cacheFiller.store = store
	for _, action := range []CacheAction{WarmupCache, EvictCache, CheckCache} {
		resp := &CacheResponse{Locations: make(map[string]uint64)}
		v.cacheFiller.Cache(ctx, action, []string{"/partial"}, 1, resp)
		call := <-store.calls
		if call.action != action || call.id != slice.Id || call.size != slice.Size || call.offset != slice.Off || call.length != slice.Len {
			t.Fatalf("unexpected cache call: %+v", call)
		}
		if resp.TotalBytes != 4096 {
			t.Fatalf("total bytes: %d, want 4096", resp.TotalBytes)
		}
	}
}
