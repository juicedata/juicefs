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

	pbData, err := proto.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	buf := make([]byte, 4+len(pbData))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(pbData)))
	copy(buf[4:], pbData)

	parsed, err := parseLanceManifestBytes(buf)
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

func TestParseLanceManifestBytes_Errors(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		error string
	}{
		{"too short", []byte{1, 2, 3}, "too short"},
		{"truncated", []byte{0, 0, 0, 10}, "truncated"},
		{"invalid protobuf", []byte{2, 0, 0, 0, 0xFF, 0xFF}, "unmarshal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLanceManifestBytes(tt.data)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIsLanceDataset(t *testing.T) {
	// Create temp Lance dataset structure
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

func TestBuildFieldColumnMap(t *testing.T) {
	manifest := &lancepb.Manifest{
		Fields: []*lancepb.Field{
			{Id: 0, Name: "id"},
			{Id: 1, Name: "name"},
			{Id: 2, Name: "value"},
		},
	}
	dataFile := &lancepb.DataFile{
		ColumnIndices: []int32{0, 2, 1},
	}

	m := buildFieldColumnMap(manifest, dataFile)
	if m["id"] != 0 {
		t.Errorf("id = %d, want 0", m["id"])
	}
	if m["name"] != 2 {
		t.Errorf("name = %d, want 2", m["name"])
	}
	if m["value"] != 1 {
		t.Errorf("value = %d, want 1", m["value"])
	}
	if _, ok := m["nonexistent"]; ok {
		t.Error("nonexistent field should not be in map")
	}
}

func TestFindLatestManifestByListing(t *testing.T) {
	dir := t.TempDir()

	// Create V1 manifests
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

func TestFindLatestManifestByListing_Empty(t *testing.T) {
	dir := t.TempDir()
	_, err := findLatestManifestByListing(dir)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}
}

func TestRelativeDeletionFilePath(t *testing.T) {
	delFile := &lancepb.DeletionFile{
		FileType:    lancepb.DeletionFile_ARROW_ARRAY,
		ReadVersion: 5,
		Id:          10,
	}
	path := relativeDeletionFilePath(1, delFile)
	expected := "_deletions/1-5-10.arrow"
	if path != expected {
		t.Errorf("got %s, want %s", path, expected)
	}

	// Bitmap type
	delFile.FileType = lancepb.DeletionFile_BITMAP
	path = relativeDeletionFilePath(1, delFile)
	expected = "_deletions/1-5-10.bin"
	if path != expected {
		t.Errorf("got %s, want %s", path, expected)
	}

	// Nil file
	if relativeDeletionFilePath(1, nil) != "" {
		t.Error("nil deletion file should return empty string")
	}
}

func TestResolveLanceDataset(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")

	// Create directory structure
	versionsDir := filepath.Join(dsPath, "_versions")
	dataDir := filepath.Join(dsPath, "data")
	os.MkdirAll(versionsDir, 0755)
	os.MkdirAll(dataDir, 0755)

	// Create a data file
	dataFilePath := filepath.Join(dataDir, "data_0.lance")
	os.WriteFile(dataFilePath, []byte("dummy data"), 0644)

	// Build manifest
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

	// Write manifest
	pbData, _ := proto.Marshal(manifest)
	buf := make([]byte, 4+len(pbData))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(pbData)))
	copy(buf[4:], pbData)
	manifestPath := filepath.Join(versionsDir, "1.manifest")
	os.WriteFile(manifestPath, buf, 0644)

	// Write latest_version_hint.json
	os.WriteFile(filepath.Join(versionsDir, "latest_version_hint.json"), []byte(`{"version":1}`), 0644)

	paths, err := resolveLanceDataset(dsPath, "", false, false, nil, false)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}

	// Should contain manifest and data file
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2: %v", len(paths), paths)
	}
}

func TestResolveLanceDataset_ManifestOnly(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	os.MkdirAll(versionsDir, 0755)

	manifest := &lancepb.Manifest{Version: 1}
	pbData, _ := proto.Marshal(manifest)
	buf := make([]byte, 4+len(pbData))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(pbData)))
	copy(buf[4:], pbData)
	os.WriteFile(filepath.Join(versionsDir, "1.manifest"), buf, 0644)

	paths, err := resolveLanceDataset(dsPath, "1", true, false, nil, false)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
}

func TestResolveLanceDataset_NotADataset(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveLanceDataset(dir, "", false, false, nil, false)
	if err == nil {
		t.Fatal("expected error for non-dataset path")
	}
}

func TestFindLanceManifestPath_SpecificVersion(t *testing.T) {
	dir := t.TempDir()
	dsPath := filepath.Join(dir, "test.lance")
	versionsDir := filepath.Join(dsPath, "_versions")
	os.MkdirAll(versionsDir, 0755)

	// Create V1 manifest
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
	os.MkdirAll(versionsDir, 0755)

	_, err := findLanceManifestPath(dsPath, "999")
	if err == nil {
		t.Fatal("expected error for non-existent version")
	}
}

func TestMain(m *testing.M) {
	fmt.Println("Running lance-resolver tests")
	os.Exit(m.Run())
}
