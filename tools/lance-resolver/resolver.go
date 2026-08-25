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
	"strconv"
	"strings"

	lancepb "github.com/juicedata/juicefs/tools/lance-resolver/proto/lance"
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
)

type lanceVersionHint struct {
	Version uint64 `json:"version"`
}

// resolveLanceDataset resolves a Lance dataset path into individual file paths
// with optional byte ranges, formatted for "juicefs warmup -f".
func resolveLanceDataset(datasetPath, version string, manifestOnly bool, includeIndices bool, columns []string) ([]string, error) {
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
	//
	// Column-level byte ranges are not implemented yet, so data files are
	// always emitted in full; --columns therefore falls back to full-file
	// warmup rather than silently producing an incomplete list.
	_ = columns
	for _, frag := range manifest.Fragments {
		for _, dataFile := range frag.Files {
			fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
			paths = append(paths, fullPath)
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
				paths = append(paths, overlayPath)
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

func main() {
	fs := flag.NewFlagSet("lance-resolver", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestOnly := fs.Bool("manifest-only", false, "only output the manifest file path")
	includeIndices := fs.Bool("include-indices", false, "also include index files under _indices/")
	version := fs.String("version", "", "specific version to resolve (default: latest)")
	columnsFlag := fs.String("columns", "", "comma-separated column names for column-level warmup (not implemented yet; full data files are warmed instead)")
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
		fmt.Fprintln(os.Stderr, "warning: --columns is not implemented yet; full data files will be warmed instead")
	}

	paths, err := resolveLanceDataset(datasetPath, *version, *manifestOnly, *includeIndices, columns)
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
  --columns COL1,COL2     Column-level warmup (not implemented yet; full data files are warmed instead)
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
