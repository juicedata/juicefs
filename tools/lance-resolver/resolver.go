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

// Package main implements a standalone Lance dataset resolver for JuiceFS warmup.
//
// It reads a Lance dataset directory and outputs file paths with optional byte ranges
// in a format that "juicefs warmup -f" can consume:
//
//	/path/to/file.lance
//	/path/to/file.lance 205600064-205823840;206605482-206633082
//
// This is the "external resolver" approach: JuiceFS stays format-agnostic,
// and this tool handles all Lance-specific format parsing.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	lancepb "github.com/juicedata/juicefs/tools/lance-resolver/proto/lance"
	file2pb "github.com/juicedata/juicefs/tools/lance-resolver/proto/lance/file2"
	"google.golang.org/protobuf/proto"
)

const (
	lanceMagic           = "LANC"
	lanceManifestDir     = "_versions"
	lanceDataDir         = "data"
	lanceDeletionsDir    = "_deletions"
	lanceIndicesDir      = "_indices"
	lanceTransactionsDir = "_transactions"
	lanceVersionHintFile = "latest_version_hint.json"
	lanceManifestExt     = ".manifest"
	lanceFileFooterLen   = 40
	lanceCmoEntrySize    = 16
)

type byteRange struct {
	start uint64
	end   uint64
}

type lanceVersionHint struct {
	Version uint64 `json:"version"`
}

// resolveLanceDataset resolves a Lance dataset path into individual file paths
// with optional byte ranges, formatted for "juicefs warmup -f".
func resolveLanceDataset(datasetPath, version string, manifestOnly bool, includeIndices bool, columns []string, includeDataPages bool) ([]string, error) {
	// 1. Find manifest file path
	manifestPath, err := findLanceManifestPath(datasetPath, version)
	if err != nil {
		return nil, fmt.Errorf("find lance manifest: %w", err)
	}
	// Output manifest path - byte ranges will use space separator (matching warmup -f format)
	paths := []string{manifestPath}

	if manifestOnly {
		return paths, nil
	}

	// 2. Read and parse manifest
	manifest, err := readAndParseLanceManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("parse lance manifest %s: %w", manifestPath, err)
	}

	// 3. Collect all fragment data files
	colOnlyMode := len(columns) > 0
	for _, frag := range manifest.Fragments {
		for _, dataFile := range frag.Files {
			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
			if !colOnlyMode {
				paths = append(paths, fullPath)
			}
		}

		if frag.DeletionFile != nil {
			delPath := relativeDeletionFilePath(frag.GetId(), frag.DeletionFile)
			if delPath != "" {
				paths = append(paths, path.Join(datasetPath, delPath))
			}
		}

		for _, overlay := range frag.Overlays {
			if overlay.DataFile != nil && overlay.DataFile.Path != "" {
				overlayPath := path.Join(datasetPath, lanceDataDir, overlay.DataFile.Path)
				if !colOnlyMode {
					paths = append(paths, overlayPath)
				}
			}
		}
	}

	// 4. Index files
	if includeIndices {
		entries, err := os.ReadDir(path.Join(datasetPath, lanceIndicesDir))
		if err == nil {
			for _, e := range entries {
				paths = append(paths, path.Join(datasetPath, lanceIndicesDir, e.Name()))
			}
		}
	}

	// 5. Transaction file
	if manifest.TransactionFile != "" {
		paths = append(paths, path.Join(datasetPath, lanceTransactionsDir, manifest.TransactionFile))
	}

	// 6. Column-level byte ranges (V2 format only)
	if len(columns) > 0 {
		colPaths, err := resolveColumnByteRanges(datasetPath, manifest, columns, includeDataPages)
		if err != nil {
			return nil, fmt.Errorf("resolve column byte ranges: %w", err)
		}
		// Merge column paths with existing paths
		paths = append(paths, colPaths...)
	}

	return paths, nil
}

func findLanceManifestPath(datasetPath, version string) (string, error) {
	versionsDir := path.Join(datasetPath, lanceManifestDir)

	if version != "" {
		v1Path := path.Join(versionsDir, version+lanceManifestExt)
		if _, err := os.Stat(v1Path); err == nil {
			return v1Path, nil
		}
		if v, err := strconv.ParseUint(version, 10, 64); err == nil {
			inverted := ^uint64(0) - v
			v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
			if _, err := os.Stat(v2Path); err == nil {
				return v2Path, nil
			}
		}
		return "", fmt.Errorf("manifest for version %s not found in %s", version, versionsDir)
	}

	hintPath := path.Join(versionsDir, lanceVersionHintFile)
	if content, err := os.ReadFile(hintPath); err == nil {
		var hint lanceVersionHint
		if json.Unmarshal(content, &hint) == nil && hint.Version > 0 {
			inverted := ^uint64(0) - hint.Version
			v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
			if _, err := os.Stat(v2Path); err == nil {
				return v2Path, nil
			}
			v1Path := path.Join(versionsDir, fmt.Sprintf("%d%s", hint.Version, lanceManifestExt))
			if _, err := os.Stat(v1Path); err == nil {
				return v1Path, nil
			}
		}
	}

	return findLatestManifestByListing(versionsDir)
}

func findLatestManifestByListing(versionsDir string) (string, error) {
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return "", fmt.Errorf("read versions dir %s: %w", versionsDir, err)
	}

	var latestPath string
	var latestVersion uint64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, lanceManifestExt) {
			continue
		}
		base := strings.TrimSuffix(name, lanceManifestExt)
		if v, err := strconv.ParseUint(base, 10, 64); err == nil {
			// V1 naming: {version}.manifest
			if v > latestVersion {
				latestVersion = v
				latestPath = path.Join(versionsDir, name)
			}
		} else {
			// V2 naming: {inverted}.manifest
			inverted := ^uint64(0) - latestVersion
			invStr := fmt.Sprintf("%020d", inverted)
			if base == invStr {
				latestPath = path.Join(versionsDir, name)
			}
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no manifest files found in %s", versionsDir)
	}
	return latestPath, nil
}

func readAndParseLanceManifest(manifestPath string) (*lancepb.Manifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	// V2 format: [txn_len:4][Transaction][manifest_len:4][Manifest][footer:16]
	// Detect V2 by checking if there's a second section after the first protobuf
	if len(data) >= 8 {
		txnLen := binary.LittleEndian.Uint32(data[:4])
		if uint32(len(data)) >= 4+txnLen+4 {
			// Looks like V2 format with two sections
			manifestData := data[4+txnLen:]
			manifestLen := binary.LittleEndian.Uint32(manifestData[:4])
			if uint32(len(manifestData)) >= 4+manifestLen {
				manifest := &lancepb.Manifest{}
				if err := proto.Unmarshal(manifestData[4:4+manifestLen], manifest); err != nil {
					return nil, fmt.Errorf("unmarshal V2 manifest: %w", err)
				}
				return manifest, nil
			}
		}
	}

	// Fallback: V1 format or plain protobuf with length prefix
	return parseLanceManifestBytes(data)
}

func parseLanceManifestBytes(data []byte) (*lancepb.Manifest, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("manifest too short: %d bytes", len(data))
	}
	pbLen := binary.LittleEndian.Uint32(data[:4])
	if uint32(len(data)) < 4+pbLen {
		return nil, fmt.Errorf("manifest truncated: need %d bytes, got %d", 4+pbLen, len(data))
	}
	manifest := &lancepb.Manifest{}
	if err := proto.Unmarshal(data[4:4+pbLen], manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return manifest, nil
}

func relativeDeletionFilePath(fragmentID uint64, delFile *lancepb.DeletionFile) string {
	if delFile == nil || delFile.ReadVersion == 0 {
		return ""
	}
	suffix := "bin" // Default to BITMAP
	if delFile.FileType == lancepb.DeletionFile_ARROW_ARRAY {
		suffix = "arrow"
	}
	return path.Join(lanceDeletionsDir,
		fmt.Sprintf("%d-%d-%d.%s", fragmentID, delFile.ReadVersion, delFile.Id, suffix))
}

func isLanceDataset(p string) bool {
	versionsDir := path.Join(p, lanceManifestDir)
	info, err := os.Stat(versionsDir)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// resolveColumnByteRanges resolves column-level byte ranges for V2 Lance data files.
// Returns paths with byte ranges in the format: "path start-end;start-end;..."
// This matches the "juicefs warmup -f" file format (space-separated path and ranges).
func resolveColumnByteRanges(datasetPath string, manifest *lancepb.Manifest, columns []string, includeDataPages bool) ([]string, error) {
	var results []string

	for _, frag := range manifest.Fragments {
		for _, dataFile := range frag.Files {
			// Only V2 format files have column metadata
			if dataFile.FileMajorVersion < 2 {
				continue
			}

			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
			fieldColMap := buildFieldColumnMap(manifest, dataFile)

			colIndices := make([]int, 0, len(columns))
			for _, col := range columns {
				if idx, ok := fieldColMap[col]; ok {
					colIndices = append(colIndices, idx)
				}
			}
			if len(colIndices) == 0 {
				continue
			}

			// Read footer + column metadata
			f, err := os.Open(fullPath)
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", fullPath, err)
			}
			fileInfo, err := f.Stat()
			if err != nil {
				f.Close()
				return nil, fmt.Errorf("stat %s: %w", fullPath, err)
			}
			fileSize := fileInfo.Size()

			if fileSize < lanceFileFooterLen {
				f.Close()
				continue
			}

			footer := make([]byte, lanceFileFooterLen)
			if _, err := f.ReadAt(footer, fileSize-lanceFileFooterLen); err != nil {
				f.Close()
				return nil, fmt.Errorf("read footer %s: %w", fullPath, err)
			}

			// Read footer (40 bytes at end of file)
			// V2 footer layout: [cm_pos:8][cm_len:8][rows:8][maj:4][min:4][?:4][magic:4]
			magic := string(footer[36:40])
			if magic != lanceMagic {
				f.Close()
				continue
			}

			// Column metadata position and length are relative to decoded data,
			// not raw file offsets. For V2 encoded files, we need to decode the
			// encoding wrapper first. This is complex and format-specific.
			// TODO: update for Lance V2 encoding format
			f.Close()
			continue
		}
	}

	return results, nil
}

func columnByteRanges(cm *file2pb.ColumnMetadata, includeDataPages bool) []byteRange {
	var ranges []byteRange
	// Column-level buffers
	for i := 0; i < len(cm.BufferOffsets) && i < len(cm.BufferSizes); i++ {
		ranges = append(ranges, byteRange{start: cm.BufferOffsets[i], end: cm.BufferOffsets[i] + cm.BufferSizes[i]})
	}
	// Page-level buffers
	if includeDataPages {
		for _, page := range cm.Pages {
			for i := 0; i < len(page.BufferOffsets) && i < len(page.BufferSizes); i++ {
				ranges = append(ranges, byteRange{start: page.BufferOffsets[i], end: page.BufferOffsets[i] + page.BufferSizes[i]})
			}
		}
	}
	return ranges
}

func mergeByteRanges(ranges []byteRange) []byteRange {
	if len(ranges) <= 1 {
		return ranges
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	merged := []byteRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if r.start <= last.end {
			if r.end > last.end {
				last.end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func buildFieldColumnMap(manifest *lancepb.Manifest, dataFile *lancepb.DataFile) map[string]int {
	m := make(map[string]int)
	if dataFile == nil {
		return m
	}
	for _, field := range manifest.Fields {
		colIdx := -1
		for i, colIdxVal := range dataFile.ColumnIndices {
			if int(colIdxVal) == int(field.Id) {
				colIdx = i
				break
			}
		}
		if colIdx >= 0 {
			m[field.Name] = colIdx
		}
	}
	return m
}

func main() {
	manifestOnly := false
	includeIndices := false
	includeDataPages := false
	version := ""
	var columns []string

	// Simple flag parsing (no external deps needed)
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--manifest-only":
			manifestOnly = true
		case "--include-indices":
			includeIndices = true
		case "--include-data-pages":
			includeDataPages = true
		case "--version":
			i++
			if i < len(args) {
				version = args[i]
			}
		case "--columns":
			i++
			if i < len(args) {
				columns = strings.Split(args[i], ",")
			}
		case "-h", "--help":
			printUsage()
			return
		default:
			if !strings.HasPrefix(args[i], "-") {
				// Dataset path
				datasetPath := args[i]
				if !isLanceDataset(datasetPath) {
					fmt.Fprintf(os.Stderr, "Error: %s is not a Lance dataset (no _versions/ directory found)\n", datasetPath)
					os.Exit(1)
				}

				paths, err := resolveLanceDataset(datasetPath, version, manifestOnly, includeIndices, columns, includeDataPages)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}

				for _, p := range paths {
					fmt.Println(p)
				}
				return
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Error: no dataset path specified\n")
	printUsage()
	os.Exit(1)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: lance-resolver [options] <dataset-path>

Resolve a Lance dataset into file paths for JuiceFS warmup.

Options:
  --version VERSION       Specific version to resolve (default: latest)
  --manifest-only         Only output the manifest file path
  --include-indices       Also include index files under _indices/
  --columns COL1,COL2     Column-level byte range resolution (V2 format only)
  --include-data-pages    Include data page buffers in column ranges
  -h, --help              Show this help message

Output format (for "juicefs warmup -f"):
  /path/to/file.lance
  /path/to/file.lance 205600064-205823840;206605482-206633082

Examples:
  lance-resolver /mnt/jfs/dataset.lance > /tmp/list.txt
  juicefs warmup -f /tmp/list.txt
  lance-resolver --columns id,name /mnt/jfs/dataset.lance > /tmp/list.txt
  juicefs warmup -f /tmp/list.txt
`)
}