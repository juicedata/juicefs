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

	"github.com/juicedata/juicefs/pkg/chunk"
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
		name    string
		input   string
		expect  []ByteRange
		wantErr bool
	}{
		{"single range", "0-100", []ByteRange{{0, 100}}, false},
		{"multiple ranges", "0-100;200-300", []ByteRange{{0, 100}, {200, 300}}, false},
		{"large values", "205600064-205823840;206605482-206633082", []ByteRange{{205600064, 205823840}, {206605482, 206633082}}, false},
		{"missing separator", "100", nil, true},
		{"negative start", "-1-100", nil, true},
		{"end < start", "100-50", nil, true},
		{"end equals start", "100-100", nil, true},
		{"one invalid part rejects all", "0-100;invalid;200-300", nil, true},
		{"overflow", "99999999999999999999-99999999999999999999", nil, true},
		{"empty", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRanges(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRanges(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.expect) {
				t.Fatalf("parseRanges(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestSplitTarget(t *testing.T) {
	tests := []struct {
		target   string
		wantPath string
		wantSpec string
		wantErr  bool
	}{
		{"/data/file.lance", "/data/file.lance", "", false},
		{"/data/my file.lance", "/data/my file.lance", "", false},
		{"/data/10-20", "/data/10-20", "", false},
		{"inode:42", "inode:42", "", false},
		{"/data/file.lance [0-100]", "/data/file.lance", "0-100", false},
		{"/data/my file.lance [0-100;200-300]", "/data/my file.lance", "0-100;200-300", false},
		{"inode:42 [0-100]", "inode:42", "0-100", false},
		// malformed range groups are rejected rather than read as part of the path
		{"/data/file [0-100 200-300]", "", "", true},
		{"/data/file [abc]", "", "", true},
		{"/data/file []", "", "", true},
		{"/data/file [0-100;]", "", "", true},
		{"/data/file [100-50]", "", "", true},
	}
	for _, tt := range tests {
		gotPath, gotSpec, gotRanges, err := SplitTarget(tt.target)
		if (err != nil) != tt.wantErr {
			t.Errorf("SplitTarget(%q) error = %v, wantErr %v", tt.target, err, tt.wantErr)
			continue
		}
		if gotPath != tt.wantPath || gotSpec != tt.wantSpec {
			t.Errorf("SplitTarget(%q) = (%q, %q), want (%q, %q)", tt.target, gotPath, gotSpec, tt.wantPath, tt.wantSpec)
		}
		if tt.wantErr {
			continue
		}
		if (gotSpec == "") != (gotRanges == nil) {
			t.Errorf("SplitTarget(%q) spec %q and ranges %v disagree", tt.target, gotSpec, gotRanges)
		}
		if got := JoinTarget(gotPath, gotSpec); got != tt.target {
			t.Errorf("JoinTarget(%q, %q) = %q, want %q", gotPath, gotSpec, got, tt.target)
		}
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
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile [0-65536]"}, 2, nil)

	// Range-based warmup: multiple ranges
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile [0-4096;65536-69632]"}, 2, nil)

	// Bad cases: path with ranges but invalid file
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/not_exists [0-100]"}, 2, nil)

	// Bad cases: range beyond file size should still work (slices beyond file size won't exist)
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile [1000000-2000000]"}, 2, nil)

	// Bad cases: malformed ranges are skipped instead of warming the whole file
	v.cacheFiller.Cache(meta.Background(), WarmupCache, []string{"/test/bigfile [100-50]", "/test/bigfile [abc]"}, 2, nil)
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
		s, parts := iter.next()
		if len(parts) > 0 {
			ids = append(ids, s.Id)
		}
	}
	return ids
}

// collectParts returns, for each slice that has work to do, its id and the
// object ranges the iterator asks the store to operate on.
func collectParts(iter *sliceIterator) map[uint64][]chunk.Range {
	got := make(map[uint64][]chunk.Range)
	for iter.hasNext() {
		s, parts := iter.next()
		if len(parts) > 0 {
			got[s.Id] = parts
		}
	}
	return got
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

	// meta.Read may hand back a list shared with the open-file cache, so
	// filtering must not modify it in place.
	cached := map[uint32][]meta.Slice{
		0: {{Id: 1, Off: 0, Len: 100}, {Id: 2, Off: 0, Len: 100}, {Id: 3, Off: 0, Len: 100}},
	}
	want := append([]meta.Slice(nil), cached[0]...)
	iter = newIter([]ByteRange{{Start: 150, End: 160}}, cached, 1)
	if got := collectSliceIDs(iter); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("shared list: got %v, want [2]", got)
	}
	if !reflect.DeepEqual(cached[0], want) {
		t.Fatalf("slices returned by Read were modified: got %v, want %v", cached[0], want)
	}
}

// A slice that was partially overwritten keeps its original Size while Len
// shrinks, so operating on Size would touch data that can no longer be read.
func TestSliceIteratorSkipsOverwrittenData(t *testing.T) {
	// 64MiB object, overwritten except for its last 1MiB.
	const size, visible = 64 << 20, 1 << 20
	iter := &sliceIterator{
		ctx: meta.Background(),
		mClient: &fakeSliceMeta{slicesByChunk: map[uint32][]meta.Slice{
			0: {
				{Id: 200, Size: size - visible, Off: 0, Len: size - visible},
				{Id: 100, Size: size, Off: size - visible, Len: visible},
			},
		}},
		ino:      1,
		chunkCnt: 1,
		stat:     &CacheResponse{Locations: make(map[string]uint64)},
	}
	got := collectParts(iter)
	want := map[uint64][]chunk.Range{
		200: {{Off: 0, Len: size - visible}},
		100: {{Off: size - visible, Len: visible}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %v, want %v", got, want)
	}
}

func TestSliceIteratorParts(t *testing.T) {
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

	// A range is translated from file coordinates to object coordinates by way
	// of the slice's offset inside its object.
	iter := newIter([]ByteRange{{Start: 1200, End: 1300}}, map[uint32][]meta.Slice{
		0: {{Id: 1, Size: 8192, Off: 4096, Len: 2000}},
	}, 1)
	if got, want := collectParts(iter), map[uint64][]chunk.Range{
		1: {{Off: 4096 + 1200, Len: 100}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("translated part = %v, want %v", got, want)
	}

	// A range is clipped to the visible portion of the slice.
	iter = newIter([]ByteRange{{Start: 500, End: 100000}}, map[uint32][]meta.Slice{
		0: {{Id: 1, Size: 8192, Off: 4096, Len: 2000}},
	}, 1)
	if got, want := collectParts(iter), map[uint64][]chunk.Range{
		1: {{Off: 4096 + 500, Len: 1500}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("clipped part = %v, want %v", got, want)
	}

	// Several ranges hitting the same slice produce several parts.
	iter = newIter([]ByteRange{{Start: 10, End: 20}, {Start: 900, End: 950}}, map[uint32][]meta.Slice{
		0: {{Id: 1, Size: 4096, Off: 0, Len: 1000}},
	}, 1)
	if got, want := collectParts(iter), map[uint64][]chunk.Range{
		1: {{Off: 10, Len: 10}, {Off: 900, Len: 50}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("multiple parts = %v, want %v", got, want)
	}

	// Ranges are relative to the file, so a slice in a later chunk is offset by
	// the chunk start.
	iter = newIter([]ByteRange{{Start: cs + 100, End: cs + 200}}, map[uint32][]meta.Slice{
		0: {{Id: 1, Size: 1000, Off: 0, Len: 1000}},
		1: {{Id: 2, Size: 1000, Off: 0, Len: 1000}},
	}, 2)
	if got, want := collectParts(iter), map[uint64][]chunk.Range{
		2: {{Off: 100, Len: 100}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("later chunk part = %v, want %v", got, want)
	}
}

func TestParseRangesMerge(t *testing.T) {
	tests := []struct {
		input  string
		expect []ByteRange
	}{
		{"200-300;0-100", []ByteRange{{0, 100}, {200, 300}}},
		{"0-100;50-200", []ByteRange{{0, 200}}},
		{"0-100;100-200", []ByteRange{{0, 200}}},
		{"0-100;10-20", []ByteRange{{0, 100}}},
		{"0-10;20-30;5-25", []ByteRange{{0, 30}}},
	}
	for _, tt := range tests {
		got, err := parseRanges(tt.input)
		if err != nil {
			t.Fatalf("parseRanges(%q): %s", tt.input, err)
		}
		if !reflect.DeepEqual(got, tt.expect) {
			t.Errorf("parseRanges(%q) = %v, want %v", tt.input, got, tt.expect)
		}
	}
}
