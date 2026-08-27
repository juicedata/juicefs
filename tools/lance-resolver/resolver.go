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

// Package main implements a standalone Lance dataset resolver for JuiceFS warmup.
//
// It reads a Lance dataset directory and outputs file paths with optional byte ranges
// in a format that "juicefs warmup -f" can consume:
//
//  /path/to/file.lance
//  /path/to/file.lance [205600064-205823840;206605482-206633082]
//
// This is the "external resolver" approach: JuiceFS stays format-agnostic,
// and this tool handles all Lance-specific format parsing.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
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
	lanceMagic             = "LANC"
	lanceManifestDir       = "_versions"
	lanceDataDir           = "data"
	lanceDeletionsDir      = "_deletions"
	lanceIndicesDir        = "_indices"
	lanceTransactionsDir   = "_transactions"
	lanceVersionHintFile   = "latest_version_hint.json"
	lanceManifestExt       = ".manifest"
	lanceManifestFooterLen = 16
	lanceFileFooterLen     = 40
	lanceCMOEntrySize      = 16
)

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
	// Output manifest path - byte ranges will use bracketed notation (matching warmup -f format)
	paths := []string{manifestPath}

	if manifestOnly {
		return paths, nil
	}

	// 2. Read and parse manifest
	manifest, err := readAndParseLanceManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("parse lance manifest %s: %w", manifestPath, err)
	}

	// 3. Collect all fragment data files.
	colOnlyMode := len(columns) > 0
	for _, frag := range manifest.Fragments {
		for _, dataFile := range frag.Files {
			if colOnlyMode {
				if p, ok, err := columnWarmupPath(datasetPath, manifest, dataFile, columns, includeDataPages); err != nil {
					fmt.Fprintf(os.Stderr, "warning: resolve column ranges for %s: %v; warming full file instead\n", dataFile.Path, err)
				} else if ok {
					paths = append(paths, p)
					continue
				}
			}
			paths = append(paths, path.Join(datasetPath, lanceDataDir, dataFile.Path))
		}

		if frag.DeletionFile != nil {
			delPath := relativeDeletionFilePath(frag.GetId(), frag.DeletionFile)
			if delPath != "" {
				paths = append(paths, path.Join(datasetPath, delPath))
			}
		}

		for _, overlay := range frag.Overlays {
			if overlay.DataFile != nil && overlay.DataFile.Path != "" {
				if colOnlyMode {
					if p, ok, err := columnWarmupPath(datasetPath, manifest, overlay.DataFile, columns, includeDataPages); err != nil {
						fmt.Fprintf(os.Stderr, "warning: resolve column ranges for overlay %s: %v; warming full file instead\n", overlay.DataFile.Path, err)
					} else if ok {
						paths = append(paths, p)
						continue
					}
				}
				paths = append(paths, path.Join(datasetPath, lanceDataDir, overlay.DataFile.Path))
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

		if len(base) == 20 {
			// V2 naming: {u64::MAX - version:020}.manifest. The parsed value
			// is the inverted version; recover the real version and pick the
			// largest one.
			if v2, err := strconv.ParseUint(base, 10, 64); err == nil {
				v := ^uint64(0) - v2
				if latestPath == "" || v > latestVersion {
					latestVersion = v
					latestPath = path.Join(versionsDir, name)
				}
				continue
			}
		}

		// V1 naming: {version}.manifest
		if v, err := strconv.ParseUint(base, 10, 64); err == nil {
			if latestPath == "" || v > latestVersion {
				latestVersion = v
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
	return parseLanceManifestBytes(data)
}

// parseLanceManifestBytes parses a Lance manifest file.
//
// A Lance manifest is laid out as:
//
//  [optional bytes before manifest]
//  [manifest_len: u32 LE][manifest protobuf]
//  [manifest_pos: u64 LE][major: u16 LE][minor: u16 LE]["LANC"]
//
// The trailing 16-byte footer points back at the manifest via manifest_pos,
// which is used instead of assuming the protobuf starts at offset 0.
func parseLanceManifestBytes(data []byte) (*lancepb.Manifest, error) {
	if len(data) < lanceManifestFooterLen {
		return nil, fmt.Errorf("manifest too short: %d bytes", len(data))
	}

	footer := data[len(data)-lanceManifestFooterLen:]
	if string(footer[12:16]) != lanceMagic {
		return nil, fmt.Errorf("invalid manifest magic %q", footer[12:16])
	}

	manifestPos := binary.LittleEndian.Uint64(footer[0:8])
	if manifestPos > uint64(len(data)-lanceManifestFooterLen) {
		return nil, fmt.Errorf("manifest position %d out of range (file size %d)", manifestPos, len(data))
	}

	pos := int(manifestPos)
	if pos+4 > len(data)-lanceManifestFooterLen {
		return nil, fmt.Errorf("manifest position %d leaves no room for manifest length", manifestPos)
	}

	msgLen := binary.LittleEndian.Uint32(data[pos : pos+4])
	msgStart := pos + 4
	available := len(data) - lanceManifestFooterLen - msgStart
	if uint64(msgLen) > uint64(available) {
		return nil, fmt.Errorf("manifest message truncated: length=%d available=%d", msgLen, available)
	}

	manifest := &lancepb.Manifest{}
	if err := proto.Unmarshal(data[msgStart:msgStart+int(msgLen)], manifest); err != nil {
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

type byteRange struct {
	start uint64
	end   uint64
}

// buildFieldColumnMap maps manifest field names to physical column indices for
// a V2 data file. DataFile.Fields and DataFile.ColumnIndices are parallel
// arrays: Fields[i] is the field ID and ColumnIndices[i] is its column index.
func buildFieldColumnMap(manifest *lancepb.Manifest, dataFile *lancepb.DataFile) map[string]int {
	m := make(map[string]int)
	if dataFile == nil {
		return m
	}

	idToName := make(map[int32]string)
	for _, field := range manifest.Fields {
		idToName[field.Id] = field.Name
	}

	for i, fieldID := range dataFile.Fields {
		if i >= len(dataFile.ColumnIndices) {
			break
		}
		if name, ok := idToName[fieldID]; ok {
			m[name] = int(dataFile.ColumnIndices[i])
		}
	}
	return m
}

// columnByteRanges returns the byte ranges occupied by a column.
//
// Column-level metadata buffers and the ColumnMetadata protobuf itself are
// always included. If includeDataPages is true, page data buffers are added
// too.
func columnByteRanges(cm *file2pb.ColumnMetadata, includeDataPages bool) []byteRange {
	var ranges []byteRange
	appendRange := func(offset, size uint64) {
		// Skip empty buffers and entries whose offset+size would overflow uint64
		// (corrupt metadata), which would otherwise yield end < start.
		if size == 0 || offset > ^uint64(0)-size {
			return
		}
		ranges = append(ranges, byteRange{start: offset, end: offset + size})
	}
	if includeDataPages {
		for _, page := range cm.Pages {
			for i := range page.BufferOffsets {
				if i < len(page.BufferSizes) {
					appendRange(page.BufferOffsets[i], page.BufferSizes[i])
				}
			}
		}
	}
	for i := range cm.BufferOffsets {
		if i < len(cm.BufferSizes) {
			appendRange(cm.BufferOffsets[i], cm.BufferSizes[i])
		}
	}
	return ranges
}

// mergeByteRanges sorts and merges overlapping or adjacent byte ranges.
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

// columnWarmupPath resolves the byte ranges of the requested columns in a
// single V2 data file. It returns a warmup target like:
//
///path/to/file.lance [123-456;789-1024]
//
// ok is false when the file is not a V2 file or contains none of the requested
// columns, in which case callers should fall back to the full data file.
func columnWarmupPath(datasetPath string, manifest *lancepb.Manifest, dataFile *lancepb.DataFile, columns []string, includeDataPages bool) (string, bool, error) {
	if dataFile == nil || dataFile.FileMajorVersion < 2 {
		return "", false, nil
	}

	fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
	colMap := buildFieldColumnMap(manifest, dataFile)
	var colIndices []int
	for _, col := range columns {
		if idx, ok := colMap[col]; ok {
			colIndices = append(colIndices, idx)
		}
	}
	if len(colIndices) == 0 {
		return "", false, nil
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return "", false, fmt.Errorf("open %s: %w", fullPath, err)
	}
	defer f.Close()

	fileInfo, err := f.Stat()
	if err != nil {
		return "", false, fmt.Errorf("stat %s: %w", fullPath, err)
	}
	fileSize := fileInfo.Size()
	if fileSize < lanceFileFooterLen {
		return "", false, fmt.Errorf("file %s too small for Lance V2 footer", fullPath)
	}

	footer := make([]byte, lanceFileFooterLen)
	if _, err := f.ReadAt(footer, fileSize-lanceFileFooterLen); err != nil {
		return "", false, fmt.Errorf("read footer %s: %w", fullPath, err)
	}
	if magic := string(footer[36:40]); magic != lanceMagic {
		return "", false, fmt.Errorf("invalid Lance file magic %q in %s", magic, fullPath)
	}

	numColumns := binary.LittleEndian.Uint32(footer[28:32])
	columnMetaOffsetsStart := binary.LittleEndian.Uint64(footer[8:16])

	if columnMetaOffsetsStart > uint64(fileSize-lanceFileFooterLen) {
		return "", false, fmt.Errorf("column metadata offset table out of range in %s", fullPath)
	}

	var ranges []byteRange
	for _, colIdx := range colIndices {
		if uint32(colIdx) >= numColumns {
			fmt.Fprintf(os.Stderr, "warning: column index %d out of range in %s (num_columns=%d); warming full file instead\n", colIdx, dataFile.Path, numColumns)
			return "", false, nil
		}

		entryOffset := columnMetaOffsetsStart + uint64(colIdx)*lanceCMOEntrySize
		if entryOffset+lanceCMOEntrySize > uint64(fileSize-lanceFileFooterLen) {
			fmt.Fprintf(os.Stderr, "warning: column metadata offset entry out of range in %s; warming full file instead\n", dataFile.Path)
			return "", false, nil
		}

		entry := make([]byte, lanceCMOEntrySize)
		if _, err := f.ReadAt(entry, int64(entryOffset)); err != nil {
			return "", false, fmt.Errorf("read column metadata offset %d in %s: %w", colIdx, fullPath, err)
		}
		cmPos := binary.LittleEndian.Uint64(entry[0:8])
		cmLen := binary.LittleEndian.Uint64(entry[8:16])
		fileLimit := uint64(fileSize - lanceFileFooterLen)
		// Avoid uint64 overflow in cmPos+cmLen: validate with subtraction so a
		// corrupt CMO entry cannot wrap the sum and defeat this bounds check.
		if cmLen > fileLimit || cmPos > fileLimit-cmLen {
			fmt.Fprintf(os.Stderr, "warning: column metadata out of range in %s; warming full file instead\n", dataFile.Path)
			return "", false, nil
		}

		cmData := make([]byte, int(cmLen))
		if _, err := f.ReadAt(cmData, int64(cmPos)); err != nil {
			return "", false, fmt.Errorf("read column metadata for column %d in %s: %w", colIdx, fullPath, err)
		}
		cm := &file2pb.ColumnMetadata{}
		if err := proto.Unmarshal(cmData, cm); err != nil {
			return "", false, fmt.Errorf("unmarshal column metadata for column %d in %s: %w", colIdx, fullPath, err)
		}

		ranges = append(ranges, byteRange{start: cmPos, end: cmPos + cmLen})
		ranges = append(ranges, columnByteRanges(cm, includeDataPages)...)
	}

	ranges = mergeByteRanges(ranges)
	if len(ranges) == 0 {
		return "", false, nil
	}
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		parts = append(parts, fmt.Sprintf("%d-%d", r.start, r.end))
	}
	return fmt.Sprintf("%s [%s]", fullPath, strings.Join(parts, ";")), true, nil
}

func main() {
	fs := flag.NewFlagSet("lance-resolver", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestOnly := fs.Bool("manifest-only", false, "only output the manifest file path")
	includeIndices := fs.Bool("include-indices", false, "also include index files under _indices/")
	version := fs.String("version", "", "specific version to resolve (default: latest)")
	columnsFlag := fs.String("columns", "", "comma-separated column names for column-level warmup")
	includeDataPages := fs.Bool("include-data-pages", false, "also warm column page data buffers (use with --columns)")
	fs.Usage = printUsage

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Error: exactly one dataset path is required")
		printUsage()
		os.Exit(1)
	}

	datasetPath := fs.Arg(0)
	if !isLanceDataset(datasetPath) {
		fmt.Fprintf(os.Stderr, "Error: %s is not a Lance dataset (no _versions/ directory found)\n", datasetPath)
		os.Exit(1)
	}

	var columns []string
	if strings.TrimSpace(*columnsFlag) != "" {
		for _, c := range strings.Split(*columnsFlag, ",") {
			if c = strings.TrimSpace(c); c != "" {
				columns = append(columns, c)
			}
		}
	}

	paths, err := resolveLanceDataset(datasetPath, *version, *manifestOnly, *includeIndices, columns, *includeDataPages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	for _, p := range paths {
		fmt.Println(p)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: lance-resolver [options] <dataset-path>

Resolve a Lance dataset into file paths for JuiceFS warmup.

Options:
  --version VERSION       Specific version to resolve (default: latest)
  --manifest-only         Only output the manifest file path
  --include-indices       Also include index files under _indices/
  --columns COL1,COL2     Column-level warmup for V2 data files (metadata buffer ranges)
  --include-data-pages    Include column page data buffer ranges (use with --columns)
  -h, --help              Show this help message

Output format (for "juicefs warmup -f"):
  /path/to/file.lance
  /path/to/file.lance [205600064-205823840;206605482-206633082]

Examples:
  lance-resolver /mnt/jfs/dataset.lance > /tmp/list.txt
  juicefs warmup -f /tmp/list.txt
  lance-resolver --columns id,name /mnt/jfs/dataset.lance > /tmp/list.txt
  juicefs warmup -f /tmp/list.txt
`)
}
