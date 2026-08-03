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
)

// LanceWarmupConfig configures Lance dataset warmup behavior.
type LanceWarmupConfig struct {
	Version        string // specific version to warmup; empty = latest
	ManifestOnly   bool   // only warmup manifest files, skip data files
	IncludeIndices bool   // also warmup index files under _indices/
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
	for _, frag := range manifest.Fragments {
		// Data files: path is relative to data/ directory
		for _, dataFile := range frag.Files {
			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
			paths = append(paths, fullPath)
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
				paths = append(paths, overlayPath)
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
			buf.Write(page.Data[:n])
			page.Release()
		}
	}

	return buf.Bytes(), nil
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
