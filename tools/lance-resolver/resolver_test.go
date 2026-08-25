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

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	lancepb "github.com/juicedata/juicefs/tools/lance-resolver/proto/lance"
	"google.golang.org/protobuf/proto"
)

// buildManifestFile builds a real Lance manifest file:
//
//[prefix][manifest_len: u32 LE][manifest protobuf][manifest_pos: u64 LE][major: u16][minor: u16]["LANC"]
func buildManifestFile(t *testing.T, manifest *lancepb.Manifest, manifestPos uint64, prefix []byte, magic string) []byte {
	t.Helper()
	pbData, err := proto.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	section := make([]byte, 4+len(pbData))
	binary.LittleEndian.PutUint32(section[:4], uint32(len(pbData)))
	copy(section[4:], pbData)

	buf := make([]byte, 0, len(prefix)+len(section)+lanceManifestFooterLen)
	buf = append(buf, prefix...)
	buf = append(buf, section...)
	footerStart := len(buf)
	buf = append(buf, make([]byte, lanceManifestFooterLen)...)
	binary.LittleEndian.PutUint64(buf[footerStart:footerStart+8], manifestPos)
	copy(buf[footerStart+12:footerStart+16], magic)
	return buf
}

func writeManifestFile(t *testing.T, path string, manifest *lancepb.Manifest) {
	t.Helper()
	if err := os.WriteFile(path, buildManifestFile(t, manifest, 0, nil, lanceMagic), 0644); err != nil {
		t.Fatalf("write manifest %s: %v", path, err)
	}
}

func TestParseLanceManifestBytes(t *testing.T) {
	manifest := &lancepb.Manifest{
		Version: 1,
		Fields: []*lancepb.Field{
			{Id: 0, Name: "id"},
			{Id: 1, Name: "value"},
		},
		Fragments: []*lancepb.DataFragment{
			{
				Id: 1,
				Files: []*lancepb.DataFile{
					{Path: "file1.lance", FileMajorVersion: 2, ColumnIndices: []int32{0, 1}},
				},
			},
		},
	}

	parsed, err := parseLanceManifestBytes(buildManifestFile(t, manifest, 0, nil, lanceMagic))
	if err != nil {
		t.Fatalf("parseLanceManifestBytes: %v", err)
	}
	if parsed.GetVersion() != 1 {
		t.Errorf("version = %d, want 1", parsed.GetVersion())
	}
	if len(parsed.Fragments) != 1 {
		t.Errorf("fragments = %d, want 1", len(parsed.Fragments))
	}
	if len(parsed.Fields) != 2 {
		t.Errorf("fields = %d, want 2", len(parsed.Fields))
	}
}

func TestParseLanceManifestBytes_ManifestAtNonZeroOffset(t *testing.T) {
	manifest := &lancepb.Manifest{Version: 7, Fragments: []*lancepb.DataFragment{{Id: 1}}}

	// Simulate a manifest with data before the manifest section (e.g. a
	// transaction section), which is why the footer must be used to locate
	// manifest_pos instead of assuming offset 0.
	prefix := []byte{0x01, 0x02, 0x03, 0x04}
	parsed, err := parseLanceManifestBytes(buildManifestFile(t, manifest, uint64(len(prefix)), prefix, lanceMagic))
	if err != nil {
		t.Fatalf("parseLanceManifestBytes: %v", err)
	}
	if parsed.GetVersion() != 7 {
		t.Errorf("version = %d, want 7", parsed.GetVersion())
	}
	if len(parsed.Fragments) != 1 {
		t.Errorf("fragments = %d, want 1", len(parsed.Fragments))
	}
}

func TestParseLanceManifestBytes_Errors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte{1, 2, 3}},
		{"bad magic", buildManifestFile(t, &lancepb.Manifest{}, 0, nil, "NOPE")},
		{"manifest position out of range", buildManifestFile(t, &lancepb.Manifest{}, 100, nil, lanceMagic)},
		{
			"truncated",
			func() []byte {
				buf := make([]byte, 20)
				binary.LittleEndian.PutUint32(buf[0:4], 100) // manifest message length
				// Footer occupies buf[4:20]; manifest_pos=0 points at buf[0:4].
				binary.LittleEndian.PutUint64(buf[4:12], 0)
				copy(buf[16:20], lanceMagic)
				return buf
			}(),
		},
		{
			"invalid protobuf",
			func() []byte {
				payload := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
				data := make([]byte, 0, 4+len(payload)+lanceManifestFooterLen)
				var lenBuf [4]byte
				binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
				data = append(data, lenBuf[:]...)
				data = append(data, payload...)
				footerStart := len(data)
				data = append(data, make([]byte, lanceManifestFooterLen)...)
				binary.LittleEndian.PutUint64(data[footerStart:footerStart+8], 0)
				copy(data[footerStart+12:footerStart+16], lanceMagic)
				return data
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseLanceManifestBytes(tt.data); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestReadAndParseLanceManifest(t *testing.T) {
	manifest := &lancepb.Manifest{Version: 3, Fragments: []*lancepb.DataFragment{{Id: 42}}}
	file := filepath.Join(t.TempDir(), "test.manifest")
	writeManifestFile(t, file, manifest)

	parsed, err := readAndParseLanceManifest(file)
	if err != nil {
		t.Fatalf("readAndParseLanceManifest: %v", err)
	}
	if parsed.GetVersion() != 3 {
		t.Errorf("version = %d, want 3", parsed.GetVersion())
	}
	if len(parsed.Fragments) != 1 || parsed.Fragments[0].GetId() != 42 {
		t.Errorf("fragments = %v, want id 42", parsed.Fragments)
	}
}

func TestIsLanceDataset(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if !isLanceDataset(dsPath) {
		t.Error("should be detected as Lance dataset")
	}
	if isLanceDataset(dir) {
		t.Error("directory without _versions/ should not be detected")
	}
	if isLanceDataset("/nonexistent") {
		t.Error("nonexistent path should not be detected")
	}
}

func TestFindLatestManifestByListing_V1(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "1.manifest"), []byte("v1"), 0644)
	os.WriteFile(filepath.Join(dir, "5.manifest"), []byte("v5"), 0644)
	os.WriteFile(filepath.Join(dir, "3.manifest"), []byte("v3"), 0644)

	path, err := findLatestManifestByListing(dir)
	if err != nil {
		t.Fatalf("findLatestManifestByListing: %v", err)
	}
	if filepath.Base(path) != "5.manifest" {
		t.Errorf("got %s, want 5.manifest", filepath.Base(path))
	}
}

func TestFindLatestManifestByListing_V2(t *testing.T) {
	dir := t.TempDir()
	for v := uint64(1); v <= 5; v++ {
		name := fmt.Sprintf("%020d%s", ^uint64(0)-v, lanceManifestExt)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("v"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	path, err := findLatestManifestByListing(dir)
	if err != nil {
		t.Fatalf("findLatestManifestByListing: %v", err)
	}
	want := fmt.Sprintf("%020d%s", ^uint64(0)-uint64(5), lanceManifestExt)
	if filepath.Base(path) != want {
		t.Errorf("got %s, want %s", filepath.Base(path), want)
	}
}

func TestFindLatestManifestByListing_Empty(t *testing.T) {
	dir := t.TempDir()
	if _, err := findLatestManifestByListing(dir); err == nil {
		t.Fatal("expected error for empty directory")
	}
}

func TestRelativeDeletionFilePath(t *testing.T) {
	delFile := &lancepb.DeletionFile{
		FileType:    lancepb.DeletionFile_ARROW_ARRAY,
		ReadVersion: 5,
		Id:          10,
	}
	if got, want := relativeDeletionFilePath(1, delFile), "_deletions/1-5-10.arrow"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	delFile.FileType = lancepb.DeletionFile_BITMAP
	if got, want := relativeDeletionFilePath(1, delFile), "_deletions/1-5-10.bin"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	if relativeDeletionFilePath(1, nil) != "" {
		t.Error("nil deletion file should return empty string")
	}
}

func TestResolveLanceDataset(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")

	versionsDir := filepath.Join(dsPath, "_versions")
	dataDir := filepath.Join(dsPath, "data")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir versions: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "data_0.lance"), []byte("dummy data"), 0644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	manifest := &lancepb.Manifest{
		Version: 1,
		Fragments: []*lancepb.DataFragment{
			{
				Id: 1,
				Files: []*lancepb.DataFile{
					{Path: "data_0.lance"},
				},
			},
		},
	}
	writeManifestFile(t, filepath.Join(versionsDir, "1.manifest"), manifest)
	os.WriteFile(filepath.Join(versionsDir, "latest_version_hint.json"), []byte(`{"version":1}`), 0644)

	paths, err := resolveLanceDataset(dsPath, "", false, false, nil)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2: %v", len(paths), paths)
	}
}

func TestResolveLanceDataset_ColumnsFallback(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")

	versionsDir := filepath.Join(dsPath, "_versions")
	dataDir := filepath.Join(dsPath, "data")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir versions: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "data_0.lance"), []byte("dummy data"), 0644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	manifest := &lancepb.Manifest{
		Version: 1,
		Fragments: []*lancepb.DataFragment{
			{
				Id: 1,
				Files: []*lancepb.DataFile{
					{Path: "data_0.lance"},
				},
			},
		},
	}
	writeManifestFile(t, filepath.Join(versionsDir, "1.manifest"), manifest)
	os.WriteFile(filepath.Join(versionsDir, "latest_version_hint.json"), []byte(`{"version":1}`), 0644)

	// Until column-level ranges are implemented, --columns must not suppress
	// full data files.
	paths, err := resolveLanceDataset(dsPath, "", false, false, []string{"id"})
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2 (manifest + full data file): %v", len(paths), paths)
	}
	if filepath.Base(paths[1]) != "data_0.lance" {
		t.Errorf("paths[1] = %s, want data_0.lance", paths[1])
	}
}

func TestResolveLanceDataset_ManifestOnly(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeManifestFile(t, filepath.Join(versionsDir, "1.manifest"), &lancepb.Manifest{Version: 1})

	paths, err := resolveLanceDataset(dsPath, "1", true, false, nil)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
}

func TestResolveLanceDataset_NotADataset(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveLanceDataset(dir, "", false, false, nil); err == nil {
		t.Fatal("expected error for non-dataset path")
	}
}

func TestFindLanceManifestPath_SpecificVersion(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(versionsDir, "5.manifest"), []byte("test"), 0644)

	path, err := findLanceManifestPath(dsPath, "5")
	if err != nil {
		t.Fatalf("findLanceManifestPath: %v", err)
	}
	if filepath.Base(path) != "5.manifest" {
		t.Errorf("got %s, want 5.manifest", filepath.Base(path))
	}
}

func TestFindLanceManifestPath_NotFound(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := findLanceManifestPath(dsPath, "999"); err == nil {
		t.Fatal("expected error for non-existent version")
	}
}

func TestMain(m *testing.M) {
	fmt.Println("Running lance-resolver tests")
	os.Exit(m.Run())
}
