/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"encoding/binary"
	"testing"

	lancepb "github.com/juicedata/juicefs/pkg/vfs/proto/lance"
	file2pb "github.com/juicedata/juicefs/pkg/vfs/proto/lance/file2"
	"google.golang.org/protobuf/proto"
)

// buildManifestFile builds a fake Lance manifest binary file for testing.
// Layout:
//
//	[protobuf_len: 4B LE] [protobuf data] [manifest_pos: 8B LE] [major: 2B] [minor: 2B] [MAGIC: 4B]
func buildManifestFile(t *testing.T, manifest *lancepb.Manifest) []byte {
	t.Helper()
	pbData, err := proto.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	// 4 bytes length prefix + protobuf data
	msgBlock := make([]byte, 4+len(pbData))
	binary.LittleEndian.PutUint32(msgBlock[:4], uint32(len(pbData)))
	copy(msgBlock[4:], pbData)

	// trailer: manifest_pos(8) + major(2) + minor(2) + magic(4) = 16 bytes
	trailer := make([]byte, 16)
	manifestPos := uint64(0) // manifest starts right at the beginning
	binary.LittleEndian.PutUint64(trailer[0:8], manifestPos)
	// major version = 0, minor version = 2
	binary.LittleEndian.PutUint16(trailer[8:10], 0) // major
	binary.LittleEndian.PutUint16(trailer[10:12], 2) // minor
	copy(trailer[12:16], []byte(lanceMagic))

	return append(msgBlock, trailer...)
}

func TestParseLanceManifestBytes(t *testing.T) {
	// Build a minimal manifest
	manifest := &lancepb.Manifest{
		Version: 42,
		Fragments: []*lancepb.DataFragment{
			{
				Id: 1,
				Files: []*lancepb.DataFile{
					{Path: "0000-uuid.lance"},
					{Path: "0001-uuid.lance"},
				},
			},
			{
				Id: 2,
				Files: []*lancepb.DataFile{
					{Path: "0002-uuid.lance"},
				},
				DeletionFile: &lancepb.DeletionFile{
					ReadVersion: 40,
					Id:          99,
					FileType:    lancepb.DeletionFile_BITMAP,
				},
			},
		},
		TransactionFile: "txn-abc",
	}

	data := buildManifestFile(t, manifest)

	parsed, err := parseLanceManifestBytes(data)
	if err != nil {
		t.Fatalf("parseLanceManifestBytes: %v", err)
	}

	if parsed.Version != 42 {
		t.Errorf("version: got %d, want 42", parsed.Version)
	}
	if len(parsed.Fragments) != 2 {
		t.Fatalf("fragments: got %d, want 2", len(parsed.Fragments))
	}

	frag0 := parsed.Fragments[0]
	if frag0.Id != 1 {
		t.Errorf("frag0 id: got %d, want 1", frag0.Id)
	}
	if len(frag0.Files) != 2 {
		t.Fatalf("frag0 files: got %d, want 2", len(frag0.Files))
	}
	if frag0.Files[0].Path != "0000-uuid.lance" {
		t.Errorf("frag0 file0 path: got %q, want %q", frag0.Files[0].Path, "0000-uuid.lance")
	}

	frag1 := parsed.Fragments[1]
	if frag1.Id != 2 {
		t.Errorf("frag1 id: got %d, want 2", frag1.Id)
	}
	if frag1.DeletionFile == nil {
		t.Fatal("frag1 deletion file is nil")
	}
	if frag1.DeletionFile.ReadVersion != 40 {
		t.Errorf("frag1 del read_version: got %d, want 40", frag1.DeletionFile.ReadVersion)
	}

	if parsed.TransactionFile != "txn-abc" {
		t.Errorf("transaction_file: got %q, want %q", parsed.TransactionFile, "txn-abc")
	}
}

func TestParseLanceManifestBytes_TooSmall(t *testing.T) {
	_, err := parseLanceManifestBytes([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for too-small file")
	}
}

func TestParseLanceManifestBytes_BadMagic(t *testing.T) {
	// 16 bytes with wrong magic
	data := make([]byte, 16)
	copy(data[12:16], []byte("BADX"))
	_, err := parseLanceManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestRelativeDeletionFilePath(t *testing.T) {
	tests := []struct {
		name       string
		fragID     uint64
		delFile    *lancepb.DeletionFile
		want       string
	}{
		{
			name:   "bitmap",
			fragID: 5,
			delFile: &lancepb.DeletionFile{
				ReadVersion: 10,
				Id:          20,
				FileType:    lancepb.DeletionFile_BITMAP,
			},
			want: "_deletions/5-10-20.bin",
		},
		{
			name:   "arrow",
			fragID: 3,
			delFile: &lancepb.DeletionFile{
				ReadVersion: 7,
				Id:          100,
				FileType:    lancepb.DeletionFile_ARROW_ARRAY,
			},
			want: "_deletions/3-7-100.arrow",
		},
		{
			name:    "nil",
			fragID:  0,
			delFile: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relativeDeletionFilePath(tt.fragID, tt.delFile)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsLanceDataset(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/data/mydataset.lance", true},
		{"/data/mydataset.lance/", true},
		{"/data/mydataset.parquet", false},
		{"/data/mydataset", false},
		{"/data/.lance", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isLanceDataset(tt.path); got != tt.want {
				t.Errorf("isLanceDataset(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestMergeByteRanges(t *testing.T) {
	tests := []struct {
		name   string
		input  []byteRange
		want   []byteRange
	}{
		{
			name:  "empty",
			input: []byteRange{},
			want:  []byteRange{},
		},
		{
			name:  "single",
			input: []byteRange{{100, 200}},
			want:  []byteRange{{100, 200}},
		},
		{
			name:  "disjoint",
			input: []byteRange{{100, 200}, {300, 400}},
			want:  []byteRange{{100, 200}, {300, 400}},
		},
		{
			name:  "overlapping",
			input: []byteRange{{100, 250}, {200, 400}},
			want:  []byteRange{{100, 400}},
		},
		{
			name:  "adjacent",
			input: []byteRange{{100, 200}, {200, 300}},
			want:  []byteRange{{100, 300}},
		},
		{
			name:  "unsorted overlapping",
			input: []byteRange{{300, 400}, {100, 250}, {200, 300}},
			want:  []byteRange{{100, 400}},
		},
		{
			name:  "contained",
			input: []byteRange{{100, 400}, {200, 300}},
			want:  []byteRange{{100, 400}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeByteRanges(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d]: got {%d,%d}, want {%d,%d}",
						i, got[i].start, got[i].end, tt.want[i].start, tt.want[i].end)
				}
			}
		})
	}
}

func TestColumnByteRanges(t *testing.T) {
	cm := &file2pb.ColumnMetadata{
		Pages: []*file2pb.ColumnMetadata_Page{
			{
				BufferOffsets: []uint64{1000, 2000},
				BufferSizes:   []uint64{500, 300},
			},
			{
				BufferOffsets: []uint64{3000},
				BufferSizes:   []uint64{100},
			},
		},
		BufferOffsets: []uint64{5000},
		BufferSizes:   []uint64{50},
	}

	// Metadata only (no data pages)
	ranges := columnByteRanges(cm, false)
	if len(ranges) != 1 {
		t.Fatalf("metadata only: got %d ranges, want 1", len(ranges))
	}
	if ranges[0].start != 5000 || ranges[0].end != 5050 {
		t.Errorf("metadata range: got {%d,%d}, want {5000,5050}",
			ranges[0].start, ranges[0].end)
	}

	// With data pages
	ranges = columnByteRanges(cm, true)
	if len(ranges) != 4 {
		t.Fatalf("with data: got %d ranges, want 4", len(ranges))
	}
	// After merge: [{1000,1500}, {2000,2300}, {3000,3100}, {5000,5050}]
	merged := mergeByteRanges(ranges)
	if len(merged) != 4 {
		t.Fatalf("merged: got %d ranges, want 4", len(merged))
	}
}

func TestBuildFieldColumnMap(t *testing.T) {
	manifest := &lancepb.Manifest{
		Fields: []*lancepb.Field{
			{Id: 0, Name: "id"},
			{Id: 1, Name: "name"},
			{Id: 2, Name: "value"},
		},
	}

	// Without DataFile, fallback to V2.0 rules
	m := buildFieldColumnMap(manifest, nil)
	if m["id"] != 0 {
		t.Errorf("id: got %d, want 0", m["id"])
	}
	if m["name"] != 1 {
		t.Errorf("name: got %d, want 1", m["name"])
	}
	if m["value"] != 2 {
		t.Errorf("value: got %d, want 2", m["value"])
	}

	// With DataFile providing explicit field→column mapping
	dataFile := &lancepb.DataFile{
		Fields:        []int32{0, 1, 2},
		ColumnIndices: []int32{2, 1, 0}, // reversed
	}
	m = buildFieldColumnMap(manifest, dataFile)
	if m["id"] != 2 {
		t.Errorf("id with datafile: got %d, want 2", m["id"])
	}
	if m["name"] != 1 {
		t.Errorf("name with datafile: got %d, want 1", m["name"])
	}
	if m["value"] != 0 {
		t.Errorf("value with datafile: got %d, want 0", m["value"])
	}
}
