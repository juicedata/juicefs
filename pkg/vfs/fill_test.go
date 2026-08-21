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
	"reflect"
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
)

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

func TestParseRanges(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []ByteRange
	}{
		{"empty", "", nil},
		{"single range", "0-100", []ByteRange{{0, 100}}},
		{"multiple ranges", "0-100;200-300", []ByteRange{{0, 100}, {200, 300}}},
		{"with spaces", " 0-100 ; 200-300 ", []ByteRange{{0, 100}, {200, 300}}},
		{"large values", "205600064-205823840;206605482-206633082", []ByteRange{{205600064, 205823840}, {206605482, 206633082}}},
		{"invalid - missing separator", "100", nil},
		{"invalid - negative start", "-1-100", nil},
		{"invalid - end <= start", "100-50", nil},
		{"invalid - end equals start", "100-100", nil},
		{"mixed valid/invalid", "0-100;invalid;200-300", []ByteRange{{0, 100}, {200, 300}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRanges(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("parseRanges(%q) = %v, want %v", tt.input, got, tt.expect)
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Fatalf("parseRanges(%q)[%d] = %v, want %v", tt.input, i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestOverlapsRange(t *testing.T) {
	iter := &sliceIterator{
		ranges: []ByteRange{
			{100, 200},
			{300, 400},
		},
	}

	tests := []struct {
		name     string
		start    uint64
		end      uint64
		expected bool
	}{
		{"before first range", 0, 50, false},
		{"edge before first range", 50, 100, false},
		{"overlaps first range start", 50, 150, true},
		{"inside first range", 120, 180, true},
		{"overlaps first range end", 150, 250, true},
		{"between ranges", 250, 280, false},
		{"overlaps second range", 350, 450, true},
		{"after all ranges", 450, 500, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := iter.overlapsRange(tt.start, tt.end); got != tt.expected {
				t.Errorf("overlapsRange(%d, %d) = %v, want %v", tt.start, tt.end, got, tt.expected)
			}
		})
	}

	// nil ranges should always return true
	noRangesIter := &sliceIterator{ranges: nil}
	if !noRangesIter.overlapsRange(0, 100) {
		t.Error("overlapsRange with nil ranges should return true")
	}
}

func TestFillWithRanges(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())

	entry, _ := v.Mkdir(ctx, 1, "test", 0777, 022)
	fe, fh, _ := v.Create(ctx, entry.Inode, "bigfile", 0644, 0, uint32(os.O_WRONLY))
	// Write a large file to trigger multiple chunks
	data := make([]byte, 128*1024) // 128KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	_ = v.Write(ctx, fe.Inode, data, 0, fh)
	_ = v.Flush(ctx, fe.Inode, fh, 0)
	v.Release(ctx, fe.Inode, fh)

	// Full file warmup (no ranges) should work as before
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile"}, 2, nil)

	// Range-based warmup: only warmup first 64KB
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile\t0-65536"}, 2, nil)

	// Range-based warmup: multiple ranges
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile\t0-4096;65536-69632"}, 2, nil)

	// Bad cases: path with ranges but invalid file
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/not_exists\t0-100"}, 2, nil)

	// Bad cases: range beyond file size should still work (slices beyond file size won't exist)
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile\t1000000-2000000"}, 2, nil)
}

// fakeSliceMeta returns canned slice lists per chunk so the slice iterator can
// be exercised without a real metadata service.
type fakeSliceMeta struct {
	meta.Meta
	slicesByChunk map[uint32][]meta.Slice
}

func (f *fakeSliceMeta) Read(_ meta.Context, _ Ino, indx uint32, slices *[]meta.Slice) syscall.Errno {
	*slices = f.slicesByChunk[indx]
	return 0
}

func collectSliceIDs(iter *sliceIterator) []uint64 {
	var ids []uint64
	for iter.hasNext() {
		s := iter.next()
		if s.Id != 0 {
			ids = append(ids, s.Id)
		}
	}
	return ids
}

func TestSliceIteratorRanges(t *testing.T) {
	const cs = meta.ChunkSize
	newIter := func(ranges []ByteRange, slicesByChunk map[uint32][]meta.Slice, chunkCnt uint32) *sliceIterator {
		return &sliceIterator{
			ctx:      meta.Background(),
			mClient:  &fakeSliceMeta{slicesByChunk: slicesByChunk},
			ino:      1,
			chunkCnt: chunkCnt,
			stat:     &CacheResponse{Locations: make(map[string]uint64)},
			ranges:   ranges,
		}
	}

	// Slice positions within a chunk are accumulated from Len, not Slice.Off.
	iter := newIter([]ByteRange{{Start: 1500, End: 1600}}, map[uint32][]meta.Slice{
		0: {{Id: 11, Off: 777, Len: 1000}, {Id: 12, Off: 888, Len: 1000}},
	}, 1)
	if got := collectSliceIDs(iter); !reflect.DeepEqual(got, []uint64{12}) {
		t.Fatalf("slice offset: got %v, want [12]", got)
	}

	// A chunk whose slices don't overlap must not stop iteration before a later
	// overlapping chunk is reached.
	iter = newIter([]ByteRange{{Start: cs + 500, End: cs + 600}, {Start: 2*cs + 10, End: 2*cs + 20}}, map[uint32][]meta.Slice{
		0: {{Id: 1, Off: 0, Len: 100}},
		1: {{Id: 2, Off: 0, Len: 100}},
		2: {{Id: 3, Off: 0, Len: 100}},
	}, 3)
	if got := collectSliceIDs(iter); !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("later chunk: got %v, want [3]", got)
	}

	// Without ranges the iterator visits every slice.
	iter = newIter(nil, map[uint32][]meta.Slice{
		0: {{Id: 1, Off: 0, Len: 100}, {Id: 2, Off: 0, Len: 100}},
		1: {{Id: 3, Off: 0, Len: 100}},
	}, 2)
	if got := collectSliceIDs(iter); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("no ranges: got %v, want [1 2 3]", got)
	}
}
