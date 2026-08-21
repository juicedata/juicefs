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
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	lancepb "github.com/juicedata/juicefs/pkg/vfs/proto/lance"
	file2pb "github.com/juicedata/juicefs/pkg/vfs/proto/lance/file2"
	"google.golang.org/protobuf/proto"
)

const (
	lanceMagic            = "LANC"
	lanceManifestDir      = "_versions"
	lanceDataDir          = "data"
	lanceDeletionsDir     = "_deletions"
	lanceIndicesDir       = "_indices"
	lanceTransactionsDir  = "_transactions"
	lanceVersionHintFile  = "latest_version_hint.json"
	lanceManifestExt      = ".manifest"
	lanceFileFooterLen    = 40
	lanceCmoEntrySize     = 16 // position(8B) + length(8B) per column in CMO table
)

// LanceWarmupConfig configures Lance dataset warmup behavior.
type LanceWarmupConfig struct {
	Version           string   // specific version to warmup; empty = latest
	ManifestOnly      bool     // only warmup manifest files, skip data files
	IncludeIndices    bool     // also warmup index files under _indices/
	Columns           []string // column names for column-level warmup
	IncludeDataPages  bool     // when doing column warmup, also warmup data page buffers
}

// byteRange represents a [start, end) byte range within a file.
type byteRange struct {
	start uint64
	end   uint64
}

// lanceVersionHint is the JSON structure of latest_version_hint.json.
type lanceVersionHint struct {
	Version uint64 `json:"version"`
}

// resolveLancePaths resolves a Lance dataset path into individual file paths
// that can be fed into the normal warmup pipeline.
//
// The resolution process:
//  1. Find the manifest file path (latest or specified version)
//  2. Read and parse the manifest protobuf
//  3. Collect all data files, deletion files, overlay files, and transaction files
func (c *CacheFiller) resolveLancePaths(
	ctx meta.Context,
	datasetPath string,
	config *LanceWarmupConfig,
) ([]string, error) {
	var paths []string

	// 1. Find manifest file path
	manifestPath, err := c.findLanceManifestPath(ctx, datasetPath, config.Version)
	if err != nil {
		return nil, fmt.Errorf("find lance manifest: %w", err)
	}
	paths = append(paths, manifestPath)

	if config != nil && config.ManifestOnly {
		return paths, nil
	}

	// 2. Read and parse manifest
	manifest, err := c.readAndParseLanceManifest(ctx, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("parse lance manifest %s: %w", manifestPath, err)
	}

	// 3. Collect all fragment data files
	// When doing column-level warmup, skip adding data file paths
	// because they will be warmed up at byte-range level by warmupLanceColumns.
	colOnlyMode := config != nil && len(config.Columns) > 0
	for _, frag := range manifest.Fragments {
		// Data files: path is relative to data/ directory
		for _, dataFile := range frag.Files {
			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
			if !colOnlyMode {
				paths = append(paths, fullPath)
			}
		}

		// Deletion file: path is constructed as _deletions/{fragId}-{readVersion}-{id}.{suffix}
		if frag.DeletionFile != nil {
			delPath := relativeDeletionFilePath(frag.GetId(), frag.DeletionFile)
			if delPath != "" {
				paths = append(paths, path.Join(datasetPath, delPath))
			}
		}

		// Overlay files: each overlay has a DataFile with a path relative to data/
		for _, overlay := range frag.Overlays {
			if overlay.DataFile != nil && overlay.DataFile.Path != "" {
				overlayPath := path.Join(datasetPath, lanceDataDir, overlay.DataFile.Path)
				if !colOnlyMode {
					paths = append(paths, overlayPath)
				}
			}
		}
	}

	// 4. Optional: warmup index files
	if config != nil && config.IncludeIndices {
		// walkDir will handle recursive traversal
		indexPath := path.Join(datasetPath, lanceIndicesDir)
		paths = append(paths, indexPath)
	}

	// 5. Transaction file
	if manifest.TransactionFile != "" {
		paths = append(paths, path.Join(datasetPath, lanceTransactionsDir, manifest.TransactionFile))
	}

	// 6. Column-level warmup (V2 format only)
	if config != nil && len(config.Columns) > 0 {
		// Column warmup works at the byte-range level inside data files,
		// so it bypasses the normal path-based warmup pipeline.
		// We call it here directly and skip adding those data files to paths
		// to avoid double-warming (unless the user didn't also request full file warmup).
		if err := c.warmupLanceColumns(ctx, datasetPath, manifest, config, WarmupCache); err != nil {
			logger.Warnf("warmup lance columns: %s", err)
		}
	}

	return paths, nil
}

// findLanceManifestPath locates the manifest file for the given dataset path and version.
//
// Resolution order:
//  1. If version is specified, try V1 and V2 naming directly
//  2. If no version, read latest_version_hint.json for the version number
//  3. Fallback: list _versions/ directory and find the latest version
func (c *CacheFiller) findLanceManifestPath(
	ctx meta.Context,
	datasetPath string,
	version string,
) (string, error) {
	versionsDir := path.Join(datasetPath, lanceManifestDir)

	if version != "" {
		// Try V1 naming: _versions/{version}.manifest
		v1Path := path.Join(versionsDir, version+lanceManifestExt)
		if st := c.resolve(ctx, v1Path, new(Ino), new(Attr)); st == 0 {
			return v1Path, nil
		}
		// Try V2 naming: _versions/{u64::MAX - version:020}.manifest
		if v, err := strconv.ParseUint(version, 10, 64); err == nil {
			inverted := ^uint64(0) - v
			v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
			if st := c.resolve(ctx, v2Path, new(Ino), new(Attr)); st == 0 {
				return v2Path, nil
			}
		}
		return "", fmt.Errorf("manifest for version %s not found in %s", version, versionsDir)
	}

	// No version specified: try reading latest_version_hint.json
	hintPath := path.Join(versionsDir, lanceVersionHintFile)
	if content, err := c.readFileContent(ctx, hintPath); err == nil {
		var hint lanceVersionHint
		if json.Unmarshal(content, &hint) == nil && hint.Version > 0 {
			// Try V2 naming first (preferred by newer Lance versions)
			inverted := ^uint64(0) - hint.Version
			v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
			if st := c.resolve(ctx, v2Path, new(Ino), new(Attr)); st == 0 {
				return v2Path, nil
			}
			// Fallback to V1 naming
			v1Path := path.Join(versionsDir, fmt.Sprintf("%d%s", hint.Version, lanceManifestExt))
			if st := c.resolve(ctx, v1Path, new(Ino), new(Attr)); st == 0 {
				return v1Path, nil
			}
		}
	}

	// Fallback: list _versions/ directory and find the latest manifest
	return c.findLatestManifestByListing(ctx, versionsDir)
}

// findLatestManifestByListing lists the _versions/ directory and finds the latest manifest.
// V1 naming: {version}.manifest — highest version number is latest
// V2 naming: {u64::MAX - version:020}.manifest — smallest filename is latest
func (c *CacheFiller) findLatestManifestByListing(
	ctx meta.Context,
	versionsDir string,
) (string, error) {
	var inode Ino
	var attr = &Attr{}
	if st := c.resolve(ctx, versionsDir, &inode, attr); st != 0 {
		return "", fmt.Errorf("resolve versions dir %s: %s", versionsDir, st)
	}
	if attr.Typ != meta.TypeDirectory {
		return "", fmt.Errorf("%s is not a directory", versionsDir)
	}

	var entries []*meta.Entry
	if st := c.meta.Readdir(ctx, inode, 1, &entries); st != 0 {
		return "", fmt.Errorf("readdir %s: %s", versionsDir, st)
	}

	var latestVersion uint64
	var latestPath string
	found := false

	for _, e := range entries {
		name := string(e.Name)
		if !strings.HasSuffix(name, lanceManifestExt) {
			continue
		}
		// Skip detached manifests (d{version}.manifest)
		if strings.HasPrefix(name, "d") {
			continue
		}

		baseName := strings.TrimSuffix(name, lanceManifestExt)

		// Try V2 naming: 20-digit zero-padded
		if len(baseName) == 20 {
			inverted, err := strconv.ParseUint(baseName, 10, 64)
			if err == nil {
				version := ^uint64(0) - inverted
				if !found || version > latestVersion {
					latestVersion = version
					latestPath = path.Join(versionsDir, name)
					found = true
				}
				continue
			}
		}

		// Try V1 naming: plain version number
		if v, err := strconv.ParseUint(baseName, 10, 64); err == nil {
			if !found || v > latestVersion {
				latestVersion = v
				latestPath = path.Join(versionsDir, name)
				found = true
			}
		}
	}

	if !found {
		return "", fmt.Errorf("no manifest files found in %s", versionsDir)
	}
	return latestPath, nil
}

// readAndParseLanceManifest reads a manifest file via JuiceFS metadata and chunk store,
// then parses the protobuf content.
func (c *CacheFiller) readAndParseLanceManifest(
	ctx meta.Context,
	manifestPath string,
) (*lancepb.Manifest, error) {
	content, err := c.readFileContent(ctx, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest file: %w", err)
	}
	return parseLanceManifestBytes(content)
}

// parseLanceManifestBytes parses a Lance manifest binary file.
//
// File layout (from the end):
//
//	[protobuf_len: 4 bytes LE] [protobuf data] ... [manifest_pos: 8 bytes LE] [major: 2 bytes] [minor: 2 bytes] [MAGIC: 4 bytes]
//
// The last 16 bytes contain: manifest_pos (8B) + major_version (2B) + minor_version (2B) + MAGIC (4B).
// At manifest_pos: message_length (4B LE) + protobuf_message (message_length bytes).
func parseLanceManifestBytes(data []byte) (*lancepb.Manifest, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("manifest file too small: %d bytes", len(data))
	}

	// Verify magic number (last 4 bytes)
	magic := data[len(data)-4:]
	if !bytes.Equal(magic, []byte(lanceMagic)) {
		return nil, fmt.Errorf("invalid magic number: %x (expected %q)", magic, lanceMagic)
	}

	// Read manifest position (8 bytes LE, starting at len-16)
	manifestPos := binary.LittleEndian.Uint64(data[len(data)-16 : len(data)-8])
	if manifestPos >= uint64(len(data)) {
		return nil, fmt.Errorf("invalid manifest position %d (file size %d)", manifestPos, len(data))
	}

	msgStart := int(manifestPos)
	// Need at least 4 bytes for the length prefix
	if msgStart+4 > len(data)-16 {
		return nil, fmt.Errorf("manifest position %d leaves no room for message", msgStart)
	}

	// Read protobuf message length (4 bytes LE at manifest_pos)
	msgLen := binary.LittleEndian.Uint32(data[msgStart : msgStart+4])
	msgEnd := msgStart + 4 + int(msgLen)

	// Validate message bounds (message should not overlap with the 16-byte trailer)
	if msgEnd > len(data)-16 {
		return nil, fmt.Errorf("manifest message length mismatch: start=%d, len=%d, but only %d bytes available before trailer",
			msgStart, msgLen, len(data)-16-msgStart-4)
	}

	// Decode protobuf
	pbManifest := &lancepb.Manifest{}
	if err := proto.Unmarshal(data[msgStart+4:msgEnd], pbManifest); err != nil {
		return nil, fmt.Errorf("decode manifest protobuf: %w", err)
	}

	return pbManifest, nil
}

// readFileContent reads the full content of a file through JuiceFS metadata and chunk store.
// This is used to read small files like manifest files and version hint files.
func (c *CacheFiller) readFileContent(ctx meta.Context, p string) ([]byte, error) {
	var inode Ino
	var attr = &Attr{}
	if st := c.resolve(ctx, p, &inode, attr); st != 0 {
		return nil, fmt.Errorf("resolve %s: %s", p, st)
	}
	if attr.Typ != meta.TypeFile {
		return nil, fmt.Errorf("%s is not a regular file (type=%d)", p, attr.Typ)
	}

	// Read all chunks and assemble
	var buf bytes.Buffer
	chunkCnt := uint32((attr.Length + meta.ChunkSize - 1) / meta.ChunkSize)
	for i := uint32(0); i < chunkCnt; i++ {
		var slices []meta.Slice
		if st := c.meta.Read(ctx, inode, i, &slices); st != 0 {
			return nil, fmt.Errorf("read inode %d chunk %d: %s", inode, i, st)
		}
		for _, s := range slices {
			if s.Id == 0 {
				// Zero-filled slice (hole)
				buf.Write(make([]byte, s.Len))
				continue
			}
			// Read slice data from chunk store
			reader := c.store.NewReader(s.Id, int(s.Size))
			// Read the entire slice starting at offset 0
			page := chunk.NewOffPage(int(s.Size))
			n, err := reader.ReadAt(context.TODO(), page, 0)
			if err != nil && n == 0 {
				page.Release()
				return nil, fmt.Errorf("read slice %d: %w", s.Id, err)
			}
			// Use s.Len (logical length) instead of n (physical read length)
			// to avoid encryption padding issues
			actualLen := n
			if int(s.Len) < actualLen {
				actualLen = int(s.Len)
			}
			buf.Write(page.Data[:actualLen])
			page.Release()
		}
	}
	// Trim to exact file length — encryption may add padding bytes
	result := buf.Bytes()
	if uint64(len(result)) > attr.Length {
		result = result[:attr.Length]
	}
	return result, nil
}

// relativeDeletionFilePath constructs the relative path of a deletion file.
// Format: _deletions/{fragment_id}-{read_version}-{id}.{suffix}
// suffix: "arrow" for Array type, "bin" for Bitmap type
func relativeDeletionFilePath(fragmentID uint64, delFile *lancepb.DeletionFile) string {
	if delFile == nil {
		return ""
	}
	suffix := "bin" // default to bitmap
	if delFile.FileType == lancepb.DeletionFile_ARROW_ARRAY {
		suffix = "arrow"
	}
	return fmt.Sprintf("%s/%d-%d-%d.%s", lanceDeletionsDir, fragmentID, delFile.ReadVersion, delFile.Id, suffix)
}

// isLanceDataset checks if a path looks like a Lance dataset directory.
func isLanceDataset(p string) bool {
	return strings.HasSuffix(p, ".lance") || strings.HasSuffix(p, ".lance/")
}

// -----------------------------------------------------------------------------------
// Column-level metadata warmup (Lance V2 file format only)
// -----------------------------------------------------------------------------------

// lanceFileFooter represents the 40-byte footer of a Lance V2 data file.
//
// Layout (all little-endian):
//
//	u64 column_meta_start          — start of Column Metadatas region
//	u64 column_meta_offsets_start   — start of Column Metadata Offset (CMO) table
//	u64 global_buff_offsets_start  — start of Global Buffers Offset (GBO) table
//	u32 num_global_buffers
//	u32 num_columns
//	u16 major_version
//	u16 minor_version
//	"LANC" (4 bytes magic)
type lanceFileFooter struct {
	columnMetaStart         uint64
	columnMetaOffsetsStart  uint64
	globalBuffOffsetsStart  uint64
	numGlobalBuffers        uint32
	numColumns              uint32
	majorVersion            uint16
	minorVersion            uint16
}

// readLanceFileFooter reads the 40-byte footer from the end of a .lance data file.
func (c *CacheFiller) readLanceFileFooter(
	ctx meta.Context, inode Ino, fileSize int64,
) (*lanceFileFooter, error) {
	if fileSize < int64(lanceFileFooterLen) {
		return nil, fmt.Errorf("file too small for footer: %d bytes", fileSize)
	}
	data, err := c.readFileRange(ctx, inode, fileSize-int64(lanceFileFooterLen), int64(lanceFileFooterLen))
	if err != nil {
		return nil, fmt.Errorf("read footer: %w", err)
	}
	// Verify magic
	magic := data[lanceFileFooterLen-4:]
	if !bytes.Equal(magic, []byte(lanceMagic)) {
		return nil, fmt.Errorf("invalid lance file magic: %x", magic)
	}
	return &lanceFileFooter{
		columnMetaStart:        binary.LittleEndian.Uint64(data[0:8]),
		columnMetaOffsetsStart: binary.LittleEndian.Uint64(data[8:16]),
		globalBuffOffsetsStart: binary.LittleEndian.Uint64(data[16:24]),
		numGlobalBuffers:       binary.LittleEndian.Uint32(data[24:28]),
		numColumns:             binary.LittleEndian.Uint32(data[28:32]),
		majorVersion:           binary.LittleEndian.Uint16(data[32:34]),
		minorVersion:           binary.LittleEndian.Uint16(data[34:36]),
	}, nil
}

// readColumnMetadataOffset reads the CMO table entry for a given column index.
// Each CMO entry is 16 bytes: position(8B LE) + length(8B LE).
// Returns (position, length) of the ColumnMetadata protobuf in the file.
func (c *CacheFiller) readColumnMetadataOffset(
	ctx meta.Context, inode Ino, footer *lanceFileFooter, columnIndex uint32,
) (position uint64, length uint64, err error) {
	if columnIndex >= footer.numColumns {
		return 0, 0, fmt.Errorf("column index %d out of range (num_columns=%d)", columnIndex, footer.numColumns)
	}
	offset := int64(footer.columnMetaOffsetsStart) + int64(columnIndex)*int64(lanceCmoEntrySize)
	data, err := c.readFileRange(ctx, inode, offset, int64(lanceCmoEntrySize))
	if err != nil {
		return 0, 0, fmt.Errorf("read CMO entry for column %d: %w", columnIndex, err)
	}
	position = binary.LittleEndian.Uint64(data[0:8])
	length = binary.LittleEndian.Uint64(data[8:16])
	return position, length, nil
}

// readColumnMetadata reads and parses the ColumnMetadata protobuf for a given column.
func (c *CacheFiller) readColumnMetadata(
	ctx meta.Context, inode Ino, footer *lanceFileFooter, columnIndex uint32,
) (*file2pb.ColumnMetadata, error) {
	pos, length, err := c.readColumnMetadataOffset(ctx, inode, footer, columnIndex)
	if err != nil {
		return nil, err
	}
	data, err := c.readFileRange(ctx, inode, int64(pos), int64(length))
	if err != nil {
		return nil, fmt.Errorf("read column %d metadata bytes: %w", columnIndex, err)
	}
	cm := &file2pb.ColumnMetadata{}
	if err := proto.Unmarshal(data, cm); err != nil {
		return nil, fmt.Errorf("decode column %d metadata: %w", columnIndex, err)
	}
	return cm, nil
}

// columnByteRanges extracts all byte ranges from a ColumnMetadata that should be warmed up.
//
// If includeDataPages is true, includes each page's buffer_offsets/sizes.
// Always includes the column-level buffer_offsets/sizes.
func columnByteRanges(cm *file2pb.ColumnMetadata, includeDataPages bool) []byteRange {
	var ranges []byteRange

	if includeDataPages {
		// Collect page data buffer ranges
		for _, page := range cm.Pages {
			for i := range page.BufferOffsets {
				if i < len(page.BufferSizes) {
					start := page.BufferOffsets[i]
					size := page.BufferSizes[i]
					ranges = append(ranges, byteRange{start: start, end: start + size})
				}
			}
		}
	}

	// Collect column-level buffer ranges (e.g. dictionaries, statistics)
	for i := range cm.BufferOffsets {
		if i < len(cm.BufferSizes) {
			start := cm.BufferOffsets[i]
			size := cm.BufferSizes[i]
			ranges = append(ranges, byteRange{start: start, end: start + size})
		}
	}

	return ranges
}

// warmupByteRanges maps file-internal byte ranges to JuiceFS slices and warms them up.
//
// For each range [start, end):
//  1. Compute startChunk = start / ChunkSize, endChunk = (end-1) / ChunkSize
//  2. For each chunk, meta.Read(inode, chunkIdx, &slices)
//  3. For each slice, check if [sliceStart, sliceEnd) overlaps [rangeStart, rangeEnd)
//  4. If overlap, FillCache(slice.Id, slice.Size) to warm the containing block
func (c *CacheFiller) warmupByteRanges(
	ctx meta.Context, inode Ino, ranges []byteRange, action CacheAction,
) error {
	// Merge overlapping and adjacent ranges to reduce meta.Read calls
	ranges = mergeByteRanges(ranges)

	for _, r := range ranges {
		if r.end <= r.start {
			continue
		}
		startChunk := uint32(r.start / meta.ChunkSize)
		endChunk := uint32((r.end - 1) / meta.ChunkSize)

		for chunkIdx := startChunk; chunkIdx <= endChunk; chunkIdx++ {
			if ctx.Canceled() {
				return nil
			}
			var slices []meta.Slice
			if st := c.meta.Read(ctx, inode, chunkIdx, &slices); st != 0 {
				logger.Warnf("read inode %d chunk %d: %s", inode, chunkIdx, st)
				continue
			}
			for _, s := range slices {
				if s.Id == 0 {
					continue // hole
				}
				// Compute the file-level byte range of this slice
				sliceStart := uint64(chunkIdx)*uint64(meta.ChunkSize) + uint64(s.Off)
				sliceEnd := sliceStart + uint64(s.Len)

				// Check overlap with target range
				if sliceEnd <= r.start || sliceStart >= r.end {
					continue // no overlap
				}

				logger.Infof("warmupByteRanges: chunk=%d slice_id=%d slice_len=%d file=[%d,%d) target=[%d,%d)",
					chunkIdx, s.Id, s.Len, sliceStart, sliceEnd, r.start, r.end)

				switch action {
				case WarmupCache:
					if err := c.store.FillCache(s.Id, s.Size); err != nil {
						logger.Warnf("fill cache slice %d (chunk %d): %s", s.Id, chunkIdx, err)
					}
				case EvictCache:
					_ = c.store.EvictCache(s.Id, s.Size)
				case CheckCache:
					_ = c.store.CheckCache(s.Id, s.Size, nil)
				}
			}
		}
	}
	return nil
}

// mergeByteRanges merges overlapping and adjacent byte ranges to minimize meta.Read calls.
func mergeByteRanges(ranges []byteRange) []byteRange {
	if len(ranges) <= 1 {
		return ranges
	}
	// Sort by start
	sortByteRanges(ranges)
	merged := []byteRange{ranges[0]}
	for i := 1; i < len(ranges); i++ {
		last := &merged[len(merged)-1]
		if ranges[i].start <= last.end {
			// Overlap or adjacent — merge
			if ranges[i].end > last.end {
				last.end = ranges[i].end
			}
		} else {
			merged = append(merged, ranges[i])
		}
	}
	return merged
}

func sortByteRanges(ranges []byteRange) {
	// Simple insertion sort (ranges are typically few dozens)
	for i := 1; i < len(ranges); i++ {
		key := ranges[i]
		j := i - 1
		for j >= 0 && ranges[j].start > key.start {
			ranges[j+1] = ranges[j]
			j--
		}
		ranges[j+1] = key
	}
}

// buildFieldColumnMap builds a map from field name to column index.
//
// In Lance V2 format, physical columns are assigned in DFS pre-order:
//   - V2.0: every field (including struct intermediates) gets a column index
//   - V2.1: only leaf fields get column indices (struct intermediates don't)
//
// Since we only support V2 and the manifest's DataFile.column_indices tells us
// the actual mapping for each file, we use DataFile.fields and DataFile.column_indices
// to build the map when available. As a fallback, we use manifest.Fields with V2.0 rules.
func buildFieldColumnMap(manifest *lancepb.Manifest, dataFile *lancepb.DataFile) map[string]int {
	m := make(map[string]int)

	if dataFile != nil && len(dataFile.Fields) > 0 && len(dataFile.ColumnIndices) > 0 {
		// Use the file's own field → column index mapping
		// dataFile.Fields is a list of field IDs; dataFile.ColumnIndices is the
		// corresponding column indices in the file.
		// We need the manifest's schema to map field IDs to names.
		idToName := make(map[int32]string)
		for _, field := range manifest.Fields {
			idToName[field.Id] = field.Name
		}
		for i, fieldID := range dataFile.Fields {
			if i < len(dataFile.ColumnIndices) {
				if name, ok := idToName[fieldID]; ok {
					m[name] = int(dataFile.ColumnIndices[i])
				}
			}
		}
		return m
	}

	// Fallback: assign column indices in DFS order (V2.0 rules)
	colIdx := 0
	var walk func(field *lancepb.Field, prefix string)
	walk = func(field *lancepb.Field, prefix string) {
		name := field.Name
		if prefix != "" {
			name = prefix + "." + name
		}
		m[name] = colIdx
		colIdx++
	}
	for _, field := range manifest.Fields {
		walk(field, "")
	}
	return m
}

// warmupLanceColumns performs column-level metadata warmup for a Lance dataset.
//
// For each data file in the dataset:
//  1. Read the file footer (40 bytes from end)
//  2. For each specified column, read its ColumnMetadata via the CMO table
//  3. Extract byte ranges from ColumnMetadata (page buffers + column buffers)
//  4. Map those byte ranges to JuiceFS slices and FillCache
func (c *CacheFiller) warmupLanceColumns(
	ctx meta.Context,
	datasetPath string,
	manifest *lancepb.Manifest,
	config *LanceWarmupConfig,
	action CacheAction,
) error {
	for _, frag := range manifest.Fragments {
		for _, dataFile := range frag.Files {
			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)

			var inode Ino
			var attr = &Attr{}
			if st := c.resolve(ctx, fullPath, &inode, attr); st != 0 {
				logger.Warnf("resolve lance data file %s: %s", fullPath, st)
				continue
			}
			if attr.Typ != meta.TypeFile {
				continue
			}

			// Read V2 file footer
			footer, err := c.readLanceFileFooter(ctx, inode, int64(attr.Length))
			if err != nil {
				logger.Warnf("read footer %s: %s", fullPath, err)
				continue
			}

			// Build column name → column index map for this file
			colMap := buildFieldColumnMap(manifest, dataFile)

			// For each requested column, read & warmup its metadata
			for _, colName := range config.Columns {
				colIdx, ok := colMap[colName]
				if !ok {
					logger.Warnf("column %q not found in %s", colName, fullPath)
					continue
				}
				if colIdx >= int(footer.numColumns) {
					logger.Warnf("column %q index %d out of range (%d columns) in %s",
						colName, colIdx, footer.numColumns, fullPath)
					continue
				}

				// Read CMO entry to get ColumnMetadata protobuf location
				cmPos, cmLen, err := c.readColumnMetadataOffset(ctx, inode, footer, uint32(colIdx))
				if err != nil {
					logger.Warnf("read CMO for column %q in %s: %s", colName, fullPath, err)
					continue
				}

				// Read and parse ColumnMetadata
				cm, err := c.readColumnMetadata(ctx, inode, footer, uint32(colIdx))
				if err != nil {
					logger.Warnf("read column %q metadata in %s: %s", colName, fullPath, err)
					continue
				}

				// Build byte ranges to warmup:
				// 1. ColumnMetadata protobuf itself (from CMO table)
				// 2. Column-level buffers (dictionaries, statistics) — always
				// 3. Page data buffers — only when --lance-column-data
				ranges := []byteRange{{start: cmPos, end: cmPos + cmLen}}
				ranges = append(ranges, columnByteRanges(cm, config.IncludeDataPages)...)

				logger.Infof("warmupLanceColumns: file=%s col=%q colIdx=%d ranges=%d",
					dataFile.Path, colName, colIdx, len(ranges))

				if err := c.warmupByteRanges(ctx, inode, ranges, action); err != nil {
					logger.Warnf("warmup column %q in %s: %s", colName, fullPath, err)
				}
			}
		}
	}
	return nil
}

// readFileRange reads a specific byte range [offset, offset+length) from a file
// through JuiceFS metadata and chunk store.
func (c *CacheFiller) readFileRange(
	ctx meta.Context, inode Ino, offset, length int64,
) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	startChunk := uint32(offset / int64(meta.ChunkSize))
	endChunk := uint32((offset + length - 1) / int64(meta.ChunkSize))

	var buf bytes.Buffer
	for chunkIdx := startChunk; chunkIdx <= endChunk; chunkIdx++ {
		var slices []meta.Slice
		if st := c.meta.Read(ctx, inode, chunkIdx, &slices); st != 0 {
			return nil, fmt.Errorf("read chunk %d: %s", chunkIdx, st)
		}
		for _, s := range slices {
			if s.Id == 0 {
				continue // hole
			}
			// Compute the file-level byte range of this slice
			chunkStart := int64(chunkIdx) * int64(meta.ChunkSize)
			sliceFileStart := chunkStart + int64(s.Off)
			sliceFileEnd := sliceFileStart + int64(s.Len)

			// Intersect with [offset, offset+length)
			readStart := sliceFileStart
			if readStart < offset {
				readStart = offset
			}
			readEnd := sliceFileEnd
			if readEnd > offset+length {
				readEnd = offset + length
			}
			if readStart >= readEnd {
				continue
			}

			// Read from chunk store
			intraSliceOff := readStart - sliceFileStart
			readLen := readEnd - readStart
			page := chunk.NewOffPage(int(readLen))
			reader := c.store.NewReader(s.Id, int(s.Size))
			n, err := reader.ReadAt(ctx, page, int(intraSliceOff))
			if err != nil && n == 0 {
				page.Release()
				return nil, fmt.Errorf("read slice %d at offset %d: %w", s.Id, intraSliceOff, err)
			}
			buf.Write(page.Data[:n])
			page.Release()
		}
	}
	return buf.Bytes(), nil
}
