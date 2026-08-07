# JuiceFS Lance Manifest Warmup — Implementation Documentation

## 1. Feature Overview

This document describes the Lance dataset warmup support added to the JuiceFS warmup command, including:

1. **Dataset-level warmup**: Parse Lance manifest files, automatically discover and warm up all related data files
2. **Column-level metadata warmup** (V2 format): Precisely warm up metadata for specified columns instead of entire data files

---

## 2. JuiceFS Architecture Overview

### 2.1 Overall Architecture

JuiceFS uses a **metadata and data separation** architecture:

```
┌──────────────────────────────────────────────────┐
│              Application / FUSE                  │
├──────────────────────────────────────────────────┤
│              VFS Layer (pkg/vfs)                 │
│   ┌──────────┐  ┌──────────┐  ┌─────────────┐ │
│   │  Reader   │  │  Writer   │  │ CacheFiller  │ │
│   └──────────┘  └──────────┘  └─────────────┘ │
├──────────────────────────────────────────────────┤
│         Meta Engine (pkg/meta)                   │
│   ┌────────┐  ┌────────┐  ┌────────┐           │
│   │ Redis  │  │  SQL   │  │  TKV   │           │
│   └────────┘  └────────┘  └────────┘           │
├──────────────────────────────────────────────────┤
│      Chunk Store (pkg/chunk)                     │
│   ┌──────────────────────────────────────┐       │
│   │         cachedStore                   │       │
│   │  ┌───────────┐  ┌──────────────────┐ │       │
│   │  │  bcache    │  │  ObjectStorage   │ │       │
│   │  │ (local)    │  │  (S3/GCS/...)    │ │       │
│   │  └───────────┘  └──────────────────┘ │       │
│   └──────────────────────────────────────┘       │
└──────────────────────────────────────────────────┘
```

### 2.2 Data Model

**File → Chunk → Slice → Block**

| Concept | Description |
|---------|-------------|
| **File** | User-visible file with an inode |
| **Chunk** | File split by 64MiB (ChunkSize) |
| **Slice** | Data slice within a chunk, with `Id`/`Size`/`Off`/`Len` |
| **Block** | Storage unit of a slice in object storage, split by BlockSize (default 4MiB) |

Object storage key format: `chunks/{id/1000/1000}/{id/1000}/{id}_{indx}_{blockSize}`

### 2.3 Warmup Flow

```
CLI (cmd/warmup.go)
  → Writes FillCache message to .jfs.control file
VFS (pkg/vfs/internal.go)
  → handleMsg parses FillCache message
CacheFiller (pkg/vfs/fill.go)
  → Cache() / CacheWithConfig() iterates paths, resolve() resolves inode
  → walkDir() recursively walks directory
  → cacheFile() processes each file
sliceIterator
  → meta.Read(inode, chunkIndex) gets slices
  → Calls handler for each slice
cachedStore.FillCache(id, size)
  → Computes block keys → downloads from object storage → writes to bcache local cache
```

---

## 3. Lance Data Format Analysis

### 3.1 Lance Dataset Directory Structure

```
/path/to/dataset.lance/          ← Dataset root directory
├── _versions/                    ← Manifest version directory
│   ├── 0.manifest               ← V1 naming: {version}.manifest
│   ├── 1.manifest
│   ├── 99999999999999999999.manifest  ← V2 naming: {u64::MAX - version:020}.manifest
│   ├── latest_version_hint.json  ← Version hint: {"version": N}
│   └── d{version}.manifest      ← Detached version
├── data/                         ← Data file directory (DataFile.path relative to this)
│   ├── 0000-uuid.lance
│   ├── 0001-uuid.lance
│   └── ...
├── _deletions/                   ← Deletion file directory
├── _indices/                     ← Secondary index directory
├── _transactions/                ← Transaction file directory
└── _overlays/                    ← Overlay file directory
```

### 3.2 Manifest Naming Schemes

| Scheme | Format | Description |
|--------|--------|-------------|
| V1 | `_versions/{version}.manifest` | Simple incrementing |
| V2 | `_versions/{u64::MAX - version:020}.manifest` | Reversed + zero-padded 20 digits, latest version sorts first |
| Detached | `_versions/d{version}.manifest` | Temporary version |

Version hint file `latest_version_hint.json`: `{"version": 42}`, used for O(1) latest version lookup.

### 3.3 Manifest File Physical Format

Manifest is a **Protobuf binary file** with a fixed-format trailer:

```
┌─────────────────────────────────────────────┐
│          Manifest File Layout               │
├─────────────────────────────────────────────┤
│  [protobuf message length: 4 bytes LE]     │
├─────────────────────────────────────────────┤
│       pb::Manifest protobuf message          │
├─────────────────────────────────────────────┤
│  manifest_position: 8 bytes LE              │
├─────────────────────────────────────────────┤
│  MAJOR_VERSION: 2 bytes (i16, =0)           │
├─────────────────────────────────────────────┤
│  MINOR_VERSION: 2 bytes (i16, =2)          │
├─────────────────────────────────────────────┤
│  MAGIC: 4 bytes = "LANC"                    │
└─────────────────────────────────────────────┘
```

### 3.4 Protobuf Message Definitions

#### Manifest (`protos/table.proto`)

```protobuf
message Manifest {
  repeated lance.file.Field fields = 1;          // Schema fields
  uint64 version = 2;                            // Dataset version number
  repeated DataFragment fragments = 4;            // Fragment list
  optional string transaction_file = 12;          // Transaction file path
  // ... other fields
}
```

#### DataFragment

```protobuf
message DataFragment {
  uint64 id = 1;                        // Fragment ID
  repeated DataFile files = 2;          // Data file list
  optional DeletionFile deletion_file = 3;  // Deletion file
  repeated DataOverlayFile overlays = 5;     // Overlay files
}
```

#### DataFile

```protobuf
message DataFile {
  string path = 1;               // Path relative to data/
  repeated int32 fields = 2;     // Field ID list
  repeated int32 column_indices = 3; // Field-to-column index mapping
  // ...
}
```

### 3.5 Lance V2 Data File Format

V2 file layout (from `protos/file2.proto`):

```
┌──────────────────────────────────────┐
│  Data Pages                          │  ← Data buffers
│  (Data Buffer 0..N)                  │
├──────────────────────────────────────┤
│  Column Metadatas                     │  ← One ColumnMetadata protobuf per column
│  Column 0 Metadata                   │  ← Contains pages list, buffer_offsets/sizes
│  Column 1 Metadata                    │
│  ...                                  │
│  Column CN Metadata                   │
├──────────────────────────────────────┤
│  Column Metadata Offset (CMO) Table   │  ← 16 bytes per column: position(8B) + length(8B)
├──────────────────────────────────────┤
│  Global Buffers Offset Table          │
├──────────────────────────────────────┤
│  Footer (40 bytes)                    │
│  u64: column_meta_start               │  ← Column Metadatas region start offset
│  u64: column_meta_offsets_start       │  ← CMO Table start offset
│  u64: global_buff_offsets_start       │
│  u32: num_global_buffers               │
│  u32: num_columns                      │
│  u16: major_version                    │
│  u16: minor_version                    │
│  "LANC" (4 bytes magic)               │
└──────────────────────────────────────┘
```

#### ColumnMetadata (`protos/file2.proto`)

```protobuf
message ColumnMetadata {
  message Page {
    repeated uint64 buffer_offsets = 1;  // Page data buffer offsets in file
    repeated uint64 buffer_sizes = 2;     // Size of each buffer
    uint64 length = 3;                   // Logical row count
    Encoding encoding = 4;               // Page encoding type
  }
  Encoding encoding = 1;                 // Column-level encoding
  repeated Page pages = 2;               // All pages for this column
  repeated uint64 buffer_offsets = 3;   // Column-level buffer offsets
  repeated uint64 buffer_sizes = 4;      // Column-level buffer sizes
}
```

---

## 4. Implemented Features

### 4.1 Dataset-level Warmup

**Function:** Given a Lance dataset path, automatically parse the manifest, discover all data files, deletion files, overlay files, and transaction files, and warm them up uniformly.

**Implementation files:**

| File | Description |
|------|-------------|
| `pkg/vfs/proto/lance/table.proto` | Lance manifest protobuf definition |
| `pkg/vfs/proto/lance/file.proto` | Lance file format proto definition |
| `pkg/vfs/proto/lance/table.pb.go` | Generated Go code |
| `pkg/vfs/proto/lance/file.pb.go` | Generated Go code |
| `pkg/vfs/lance_warmup.go` | Core implementation |
| `pkg/vfs/lance_warmup_test.go` | Unit tests |
| `cmd/warmup.go` | CLI options and message encoding |
| `pkg/vfs/internal.go` | FillCache message protocol extension |
| `pkg/vfs/fill.go` | CacheWithConfig() method |

**Core methods:**

| Method | Description |
|--------|-------------|
| `resolveLancePaths()` | Resolve dataset path, return file path list to warm up |
| `findLanceManifestPath()` | Locate manifest file (read hint or list directory) |
| `readAndParseLanceManifest()` | Read manifest file and parse protobuf |
| `parseLanceManifestBytes()` | Parse manifest binary (LANC magic → manifest_pos → protobuf) |
| `readFileContent()` | Read entire file content through JuiceFS chunk store |
| `isLanceDataset()` | Check if path is a Lance dataset (.lance suffix) |

**Flow:**

```
CLI: juicefs warmup --lance /mnt/jfs/data/ds.lance
  → sendCommand encodes Lance config into FillCache message
VFS: handleMsg parses message
  → CacheWithConfig()
  → Detect .lance suffix → isLanceDataset()
  → resolveLancePaths():
      1. findLanceManifestPath() — read latest_version_hint.json or list directory
      2. readAndParseLanceManifest() — read file → LANC magic → protobuf parsing
      3. Iterate fragments → collect data/, _deletions/, _overlays/ file paths
  → Expanded paths go through normal warmup flow
```

### 4.2 Column-level Metadata Warmup (V2 Format)

**Function:** Precisely warm up metadata for specified columns in Lance V2 data files (ColumnMetadata + page buffers), rather than entire files.

**New files:**

| File | Description |
|------|-------------|
| `pkg/vfs/proto/lance/file2/file2.proto` | Lance V2 file format protobuf definition |
| `pkg/vfs/proto/lance/file2/file2.pb.go` | Generated Go code (ColumnMetadata, Page, etc.) |

**New methods (`pkg/vfs/lance_warmup.go`):**

| Method | Description |
|--------|-------------|
| `readLanceFileFooter()` | Read V2 file 40B footer (column_meta_start, num_columns, etc.) |
| `readColumnMetadataOffset()` | Read 16B CMO table entry for specified column (position + length) |
| `readColumnMetadata()` | Read and protobuf-parse ColumnMetadata for specified column |
| `columnByteRanges()` | Extract page buffer and column buffer byte ranges from ColumnMetadata |
| `mergeByteRanges()` | Merge overlapping/adjacent byte ranges to reduce meta.Read calls |
| `warmupByteRanges()` | **Core**: Map file-internal byte ranges to JuiceFS slices → FillCache |
| `buildFieldColumnMap()` | Column name → column index mapping (supports DataFile.fields + column_indices) |
| `warmupLanceColumns()` | Column warmup entry point: iterate data files → footer → column metadata → byte ranges → warmup |
| `readFileRange()` | Read file content by byte range (through chunk store) |

**Byte Range → JuiceFS Slice Mapping Algorithm:**

```
Input: Lance file inode, byte range [start, end)
Output: Warm up corresponding JuiceFS slices

1. startChunk = start / ChunkSize(64MiB)
   endChunk = (end-1) / ChunkSize

2. For each chunkIndex ∈ [startChunk, endChunk]:
   a. meta.Read(inode, chunkIndex, &slices)  // Get slices from metadata engine
   b. For each slice:
      - Compute file range [chunkStart + slice.Off, chunkStart + slice.Off + slice.Len)
      - Intersect with [start, end)
      - If overlap → FillCache(slice.Id, slice.Size)
```

**Column Name → Column Index Mapping:**

- Prefer exact mapping from manifest DataFile's `fields` and `column_indices`
- Fall back to DFS-order assignment from manifest schema (V2.0 rule)

**Flow:**

```
CLI: juicefs warmup --lance --lance-columns "col_a,col_b" /mnt/jfs/data/ds.lance
  → sendCommand encodes column names into FillCache message
VFS: handleMsg parses message
  → CacheWithConfig(lanceCfg with Columns)
  → resolveLancePaths():
      1. Find and parse manifest
      2. warmupLanceColumns():
           for each data file:
             a. readLanceFileFooter() — read 40B trailer
             b. buildFieldColumnMap() — column name → column index
             c. for each requested column:
                - readColumnMetadata(inode, footer, colIdx) — read CMO table + protobuf
                - columnByteRanges(cm) — extract page/col buffer byte ranges
                - mergeByteRanges() — merge overlapping ranges
                - warmupByteRanges():
                    for each range [start,end):
                      for chunk in [start/64M, end/64M]:
                        meta.Read(inode, chunk) → slices
                        slice overlaps range? → FillCache(slice.Id, slice.Size)
```

### 4.3 FillCache Message Protocol Extension

The FillCache message is extended with backward-compatible Lance configuration fields:

```
Original:  [pathsLen:4B] [paths] [threads:2B] [background:1B] [action:1B]
Extended:  [lance_flag:1B] [manifest_only:1B] [include_indices:1B]
           [version_len:2B] [version]
           [columns_len:2B] [columns_data] [include_data_pages:1B]
```

All extended fields are optional — checked via `r.HasMore()`, ensuring backward compatibility with older clients.

---

## 5. Usage

### 5.1 Dataset-level Warmup

```bash
# Warm up entire Lance dataset (auto-parse manifest, warm up all data files)
juicefs warmup --lance /mnt/jfs/data/mydataset.lance

# Warm up a specific dataset version
juicefs warmup --lance --lance-version 5 /mnt/jfs/data/mydataset.lance

# Only warm up manifest file (metadata only, no data files)
juicefs warmup --lance --lance-manifest-only /mnt/jfs/data/mydataset.lance

# Warm up dataset including index files
juicefs warmup --lance --lance-include-indices /mnt/jfs/data/mydataset.lance
```

### 5.2 Column-level Metadata Warmup

```bash
# Warm up column metadata for col_a and col_b (ColumnMetadata + column-level buffers)
juicefs warmup --lance --lance-columns "col_a,col_b" /mnt/jfs/data/mydataset.lance

# Warm up col_a metadata + data page buffers (page buffer offsets/sizes)
juicefs warmup --lance --lance-columns "col_a" --lance-column-data /mnt/jfs/data/mydataset.lance

# Combine: specific version + column-level warmup
juicefs warmup --lance --lance-version 3 --lance-columns "col_a,col_b" /mnt/jfs/data/mydataset.lance
```

### 5.3 CLI Options Summary

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--lance` | bool | false | Enable Lance dataset warmup mode |
| `--lance-version` | string | "" (latest) | Specify Lance dataset version |
| `--lance-manifest-only` | bool | false | Only warm up manifest files |
| `--lance-include-indices` | bool | false | Also warm up index files |
| `--lance-columns` | string | "" | Comma-separated column names for column-level warmup |
| `--lance-column-data` | bool | false | Also warm up column data page buffers |

### 5.4 Use Cases

**Case 1: Initial Lance dataset load**

```bash
# Warm up the entire dataset to accelerate first queries
juicefs warmup --lance /mnt/jfs/data/training_data.lance
```

**Case 2: Wide table, querying only a few columns**

```bash
# 500-column table, only querying 3 columns
juicefs warmup --lance --lance-columns "id,timestamp,feature_42" /mnt/jfs/data/wide_table.lance
```

**Case 3: Incremental warmup after version update**

```bash
# New version published, only warm up manifest and specified columns
juicefs warmup --lance --lance-version 10 --lance-columns "id,label" /mnt/jfs/data/ds.lance
```

**Case 4: Metadata-only acceleration (minimal cache space)**

```bash
# Only warm up manifest files, suitable for metadata-heavy but data-light access patterns
juicefs warmup --lance --lance-manifest-only /mnt/jfs/data/ds.lance
```

---

## 6. Implementation Details

### 6.1 Project Structure

```
juicefs/
├── cmd/warmup.go                      # CLI: --lance options, sendCommand encoding
├── pkg/vfs/
│   ├── fill.go                        # CacheWithConfig(): Lance detection branch
│   ├── internal.go                    # handleMsg: FillCache message decoding
│   ├── lance_warmup.go                # ★ Core implementation (dataset + column-level)
│   ├── lance_warmup_test.go           # Unit tests
│   ├── lance_warmup_e2e_test.go       # E2E integration tests
│   └── proto/lance/
│       ├── table.proto                # Lance manifest protobuf definition
│       ├── file.proto                 # Lance V1 file format proto
│       ├── table.pb.go               # Generated Go code
│       ├── file.pb.go                 # Generated Go code
│       └── file2/
│           ├── file2.proto            # Lance V2 file format proto (ColumnMetadata)
│           └── file2.pb.go            # Generated Go code
```

### 6.2 FillCache Message Protocol

```
┌────────────────────────────────────────────────────────────────┐
│ FillCache Message Body (backward-compatible extension)         │
├────────────────────────────────────────────────────────────────┤
│ pathsLen: 4 bytes LE                                           │
│ paths:    N bytes (pathsLen, newline-separated paths)          │
│ threads:  2 bytes LE                                           │
│ background: 1 byte                                             │
│ action:   1 byte (WarmupCache=0, EvictCache=1, CheckCache=2)  │
├──────── ─ ─ ─ ─ optional fields below (HasMore check) ─ ─ ─ ─┤
│ lance_flag:       1 byte (1=lance mode)                      │
│ manifest_only:    1 byte                                       │
│ include_indices:  1 byte                                       │
│ version_len:      2 bytes LE                                   │
│ version:          N bytes                                      │
│ columns_len:      2 bytes LE (comma-separated column names)    │
│ columns_data:     N bytes                                      │
│ include_data_pages: 1 byte                                      │
└────────────────────────────────────────────────────────────────┘
```

### 6.3 Tests

#### Unit Tests

13 tests implemented (9 unit + 4 E2E), covering the following scenarios:

| Test | Description |
|------|-------------|
| `TestParseLanceManifestBytes` | Parse complete manifest with fragments/files/deletions |
| `TestParseLanceManifestBytes_TooSmall` | Too-small file returns error |
| `TestParseLanceManifestBytes_BadMagic` | Bad magic returns error |
| `TestRelativeDeletionFilePath` | Deletion file path construction (bitmap/arrow/nil) |
| `TestIsLanceDataset` | .lance suffix detection |
| `TestMergeByteRanges` | Byte range merging (empty/single/disjoint/overlapping/adjacent/contained) |
| `TestColumnByteRanges` | Extract byte ranges from ColumnMetadata (metadata-only / with data pages) |
| `TestBuildFieldColumnMap` | Column name → column index mapping (fallback / exact mapping) |
| `TestE2E_ManifestParseAndPathResolution` | Build manifest binary → parse → verify fragments/files/deletions/txn |
| `TestE2E_V2FooterAndColumnMetadata` | Build V2 data file → read footer → read CMO → parse ColumnMetadata → extract byte ranges |
| `TestE2E_FieldColumnMapResolution` | Manifest schema → field mapping (fallback DFS / exact / subset fields) |
| `TestE2E_FullPipeline` | Full pipeline: manifest → schema → field map → V2 file → footer → column metadata → byte ranges → merge |

Run tests:

```bash
cd <juicefs-repo>
go test ./pkg/vfs/ -run "TestParseLance|TestRelativeDeletion|TestIsLance|TestMergeByte|TestColumnByte|TestBuildField|TestE2E" -v -count=1
```

---

## 7. Limitations & Notes

1. **V2 format only**: Column-level metadata warmup only supports Lance V2 file format (identified by `major_version` and `minor_version` in the footer). V1 (legacy) format Page Table is not supported.

2. **Slice granularity limitation**: JuiceFS warmup granularity is at the Slice level (object storage objects, each up to 64MiB). `FillCache` downloads all blocks of the entire slice. Therefore:
   - Small files (< 64MiB): the entire file is a single slice, column-level warmup has the same effect as full warmup
   - Large files (multiple slices): if a column's data only spans a subset of slices, column-level warmup can reduce download volume
   - The actual effectiveness of column-level warmup depends on the physical distribution of column data within the file

3. **Nested fields**: The current `buildFieldColumnMap` uses simple DFS-order column index assignment, which may be imprecise for complex nested fields (struct/list). Prefer using the explicit `fields` + `column_indices` mapping from the manifest's DataFile entries.

4. **Large file range reads**: `readFileRange` reads through the chunk store by range, but may require multiple `meta.Read` calls for very large files. Byte range merging optimization is implemented to reduce call count.

5. **Protobuf dependency**: Introduces `google.golang.org/protobuf` dependency, adding a small amount of binary size.

6. **Concurrency control**: Column-level warmup concurrency is controlled by the `--threads` parameter (default 50), sharing the concurrency pool with file-level warmup.

---

## 7.5 Column Warmup Bug Analysis and Fix Record

This section documents issues discovered during manual testing of column-level warmup, their root cause analysis, and fixes, for future maintenance reference.

### 7.5.1 Problem Description

During manual testing (section 8.5.3 column-level metadata warmup):

- After running `juicefs warmup --lance --lance-columns "id" /mnt/jfs/data/test_lance_large.lance`
- Cache size (~99MB) was nearly identical to full warmup (~99MB)
- JuiceFS logs showed `warmup column "id" in ...lance: 0 byte ranges`
- Column-level warmup did not actually warm up any byte ranges

### 7.5.2 Root Cause Analysis

A standalone Go program was used to parse the ColumnMetadata of V2.1 format files, revealing three issues:

#### Issue 1: `columnByteRanges(cm, false)` returned empty list

The logic of `columnByteRanges`:

```go
func columnByteRanges(cm *file2pb.ColumnMetadata, includeDataPages bool) []byteRange {
    var ranges []byteRange
    // 1. Column-level buffers (field 3/4) — always included
    for i := range cm.BufferOffsets { ... }
    // 2. Page data buffers (page.BufferOffsets/sizes) — only when includeDataPages=true
    if includeDataPages {
        for _, page := range cm.Pages { ... }
    }
    return ranges
}
```

When `--lance-column-data` was not specified (`includeDataPages=false`), only column-level buffers (`cm.BufferOffsets`/`cm.BufferSizes`) were collected. However, in V2.1 format, these fields were **empty**:

```
Column 0: 1 pages, 0 buffer_offsets, 0 buffer_sizes  ← column-level buffers empty
  Page 0: 2 buffer_offsets, 2 buffer_sizes, length=50000  ← page-level has data
```

In V2.1 format, all data buffer information is stored inside Pages. Column-level buffers are only used for auxiliary data like dictionaries/statistics. When a column has no such auxiliary data, column-level buffers are empty, resulting in 0 byte ranges.

#### Issue 2: Missing ColumnMetadata protobuf byte range

The CMO (Column Metadata Offset) table records each column's ColumnMetadata protobuf position and length:

```
CMO entry: [8B position LE][8B length LE]
```

However, `warmupLanceColumns` did not include this range when calling `columnByteRanges`. The ColumnMetadata protobuf itself is essential metadata that must be read to query column data (it contains page layout information), and should be warmed up.

#### Issue 3: Missing `colOnlyMode` in `resolveLancePaths`

When `--lance-columns` was specified, `resolveLancePaths` should only return metadata file paths (manifest, txn, hint, etc.) and **not** data file paths (since data files are warmed up by `warmupLanceColumns` via byte ranges). However, the original code did not implement the `colOnlyMode` logic, causing data files to also be added to the paths list, triggering full warmup.

### 7.5.3 Fixes

#### Fix 1: `warmupLanceColumns` adds CMO range

In `pkg/vfs/lance_warmup.go`'s `warmupLanceColumns` function, read the CMO entry to get the ColumnMetadata protobuf position and length, and add it as the first byte range in the warmup list:

```go
// Before
cm, err := c.readColumnMetadata(ctx, inode, footer, uint32(colIdx))
ranges := columnByteRanges(cm, config.IncludeDataPages)

// After
cmPos, cmLen, err := c.readColumnMetadataOffset(ctx, inode, footer, uint32(colIdx))
cm, err := c.readColumnMetadata(ctx, inode, footer, uint32(colIdx))
ranges := []byteRange{{start: cmPos, end: cmPos + cmLen}}  // CMO range
ranges = append(ranges, columnByteRanges(cm, config.IncludeDataPages)...)
```

This ensures the CMO range is always warmed up even if `columnByteRanges` returns empty, guaranteeing the ColumnMetadata protobuf itself is cached.

#### Fix 2: `resolveLancePaths` adds `colOnlyMode`

In `pkg/vfs/lance_warmup.go`'s `resolveLancePaths` function, set `colOnlyMode = true` when `config.Columns` is non-empty, skipping data file path addition:

```go
colOnlyMode := len(config.Columns) > 0
// ...
if !colOnlyMode {
    // Add data file paths
    paths = append(paths, dataFilePath)
}
```

This avoids simultaneously performing full path warmup and byte range warmup in column mode.

### 7.5.4 Warmup Range Design

After the fix, column-level warmup byte ranges consist of:

| Range Type | Source | Always Included | Description |
|-----------|--------|:--:|------|
| ColumnMetadata protobuf | CMO table `[pos, pos+length)` | ✅ | Column metadata itself (page layout, etc.) |
| Column-level buffers | `cm.BufferOffsets`/`cm.BufferSizes` | ✅ | Dictionaries, statistics |
| Page data buffers | `page.BufferOffsets`/`page.BufferSizes` | ❌ | Only when `--lance-column-data` |

Data flow:

```
Manifest → Schema → field_id → DataFile.column_indices → column index
                                                         ↓
V2 File → Footer → CMO table → [cmPos, cmPos+cmLen)  → Warmup range ①
                              → ColumnMetadata protobuf
                                ↓
                                ├── cm.BufferOffsets/Sizes    → Warmup range ②
                                └── pages[].BufferOffsets    → Warmup range ③ (optional)
                                                         ↓
                                           mergeByteRanges → dedup + merge
                                                         ↓
                                           warmupByteRanges → map to slices
                                                         ↓
                                           FillCache → download blocks
```

### 7.5.5 Verification Results

Using a 200MB Lance dataset (3 columns × 200K rows × 2 files, ~197MB per file / 4 chunks):

| Warmup Method | Cache Size | Cache Files | Notes |
|--------------|-----------|-------------|-------|
| Full warmup | 395M | 103 | All data |
| id column + data pages | **11M** | 7 | id column data at file tail, only hits 1 slice |
| large_blob column + data pages | 395M | 103 | large_blob occupies most of the file data |

**id column** (int64, 8B per row, 200K rows ≈ 1.6MB) has data concentrated in the last chunk 3 of each file, hitting only 1 slice (~5MB), resulting in only 11MB cache — **97% reduction** compared to 395MB full warmup.

**large_blob column** (binary, 2KB per row, 200K rows ≈ 390MB) occupies most of the file data, spanning all chunks, so cache is identical to full warmup.

### 7.5.6 Architecture Limitation: JuiceFS Slice vs Lance Column Data

#### Key Concepts

**Lance V2 file structure**: A single `.lance` data file contains **all column** data, arranged sequentially by column:

```
Single .lance file (197MB, 3 columns × 200K rows)
┌─────────────────────────────────────────────────────────────┐
│ large_blob column data   [0           ~ 205,200,000)    ~195MB   │
│ name column data         [201,326,592 ~ 205,600,000)     ~4.1MB   │
│ id column data           [205,600,064 ~ 205,823,840)     ~224KB   │
│ ColumnMetadata           [206,605,482 ~ 206,633,082)     ~28KB   │
│ CMO table + Footer       [206,633,082 ~ 206,633,122)      40B   │
└─────────────────────────────────────────────────────────────┘
```

**Not** one file per column — all columns' data is in the same file, physically arranged by column.

**JuiceFS storage model**: JuiceFS splits this 197MB file into multiple slices by 64MiB:

```
File: 197MB .lance file
┌──────────────────┬──────────────────┬──────────────────┬───────────┐
│    Slice 0        │    Slice 1        │    Slice 2        │  Slice 3   │
│    [0, 64MB)      │    [64MB, 128MB)  │    [128MB, 192MB) │ [192MB, ] │
│    64MB           │    64MB           │    64MB           │ 5MB       │
│                  │                  │                  │            │
│  large_blob data  │  large_blob data  │  large_blob data  │ name + id │
│                  │                  │                  │ + metadata │
└──────────────────┴──────────────────┴──────────────────┴───────────┘
```

#### How Column-level Warmup Works

1. Parse Lance manifest → get data file paths
2. Parse V2 footer → CMO table → per-column ColumnMetadata → page buffer_offsets/sizes
3. Compute target column byte ranges (e.g., id column at [205,600,064, 205,823,840))
4. Map to JuiceFS slices (id column data is in Slice 3)
5. Call `FillCache` to download the corresponding slice

#### Why Column-level Warmup Doesn't Always Reduce Download Volume

`FillCache(sliceID, sliceSize)` downloads **all blocks of the entire slice**. Thus:

- **id column** (224KB, at file tail) → only hits Slice 3 → downloads 5MB ✅ 97% reduction
- **large_blob column** (195MB, spans most of file) → hits Slices 0+1+2+3 → downloads 197MB ❌ no difference

Column-level warmup only reduces download volume when the target column's data is concentrated in a **small subset of slices**. This is an inherent characteristic of the JuiceFS object storage architecture — warmup granularity is at the slice/block level, not arbitrary byte ranges.

---

## 8. Manual Testing Guide

This chapter describes the complete workflow from building JuiceFS, creating a Lance dataset, mounting the filesystem, to verifying warmup functionality.

### 8.1 Environment Setup

#### Required Software

```bash
# Go (build JuiceFS)
go version  # Requires 1.21+

# Python + pylance (create Lance dataset)
python3 --version  # 3.8+
pip3 install pylance

# Redis (JuiceFS metadata engine, optional, SQLite can be used instead)
# If redis-server is unavailable, SQLite can be used as the metadata engine
```

#### 8.1.1 Building JuiceFS

```bash
cd juicefs

# Switch to lance branch
git checkout lance

# Build
go build -o juicefs .

# Verify
./juicefs --version
# Expected output: juicefs version 1.5.0-dev+unknown

# Check warmup command help
./juicefs warmup --help
# Confirm --lance, --lance-columns, --lance-column-data options are visible
```

#### 8.1.2 Running Unit Tests

```bash
cd juicefs

# Run all Lance-related tests
go test ./pkg/vfs/ \
  -run "TestParseLance|TestRelativeDeletion|TestIsLance|TestMergeByte|TestColumnByte|TestBuildField|TestE2E" \
  -v -count=1

# Expected: 13 tests all PASS
```

### 8.2 Creating a Lance Dataset

Use Python + pylance to create a test Lance dataset locally:

```python
#!/usr/bin/env python3
"""create_lance_dataset.py — Create a test Lance dataset"""

import lance
import pyarrow as pa
import os

# 1. Define schema and test data
schema = pa.schema([
    pa.field("id", pa.int64()),
    pa.field("name", pa.string()),
    pa.field("score", pa.float32()),
    pa.field("embedding", pa.list_(pa.float32(), 128)),
])

num_rows = 10000

table = pa.table({
    "id": pa.array(range(num_rows), type=pa.int64()),
    "name": pa.array([f"item_{i}" for i in range(num_rows)], type=pa.string()),
    "score": pa.array([float(i) / 100.0 for i in range(num_rows)], type=pa.float32()),
    "embedding": pa.array(
        [[float(i) / 1000.0] * 128 for i in range(num_rows)],
        type=pa.list_(pa.float32(), 128)
    ),
}, schema=schema)

# 2. Write Lance dataset
dataset_path = "/tmp/test_lance_data.lance"
if os.path.exists(dataset_path):
    import shutil
    shutil.rmtree(dataset_path)

# Write data with multiple fragments
lance.write_dataset(
    table,
    dataset_path,
    mode="overwrite",
    max_rows_per_file=2500,  # 4 fragments
)

print(f"Lance dataset created at: {dataset_path}")

# 3. Verify directory structure
for root, dirs, files in os.walk(dataset_path):
    level = root.replace(dataset_path, "").count(os.sep)
    indent = "  " * level
    print(f"{indent}{os.path.basename(root)}/")
    subindent = "  " * (level + 1)
    for file in sorted(files):
        filepath = os.path.join(root, file)
        size = os.path.getsize(filepath)
        print(f"{subindent}{file} ({size:,} bytes)")

# 4. Print manifest info
import lance
ds = lance.dataset(dataset_path)
print(f"\nDataset info:")
print(f"  Version: {ds.version}")
print(f"  Schema: {ds.schema}")
print(f"  Num fragments: {len(list(ds.get_fragments()))}")
for frag in ds.get_fragments():
    print(f"    Fragment {frag.fragment_id}: {len(frag.data_files())} files")
    for f in frag.data_files():
        print(f"      - {f.path}")
```

Run:

```bash
python3 create_lance_dataset.py
```

Expected output (directory structure similar to):

```
test_lance_data.lance/
  _versions/
    0.manifest (xxx bytes)
    latest_version_hint.json (xx bytes)
  data/
    0000-xxx.lance (xxx bytes)
    0001-xxx.lance (xxx bytes)
    0002-xxx.lance (xxx bytes)
    0003-xxx.lance (xxx bytes)

Dataset info:
  Version: 1
  Schema: id: int64, name: string, score: float, embedding: fixed_size_list<item: float>[128]
  Num fragments: 4
```

### 8.3 Mounting JuiceFS

JuiceFS requires `format` to format the volume first, then `mount` to mount it. The meta URL is a positional argument, not a `--meta` option.

#### 8.3.1 Using SQLite Metadata Engine (No Redis Required)

```bash
# Create mount point
mkdir -p /mnt/jfs

# 1. Format volume (only needed once)
#    SQLite as metadata engine, local disk as default object storage (--storage file)
./juicefs format sqlite3:///tmp/juicefs-meta.db mylance

# 2. Mount (meta URL as positional argument)
./juicefs mount sqlite3:///tmp/juicefs-meta.db /mnt/jfs -d
```

After successful mount, `/mnt/jfs` is a usable JuiceFS filesystem.

#### 8.3.2 Using Redis Metadata Engine (Optional)

```bash
# Start Redis first
redis-server --daemonize yes

# Format
./juicefs format redis://127.0.0.1:6379/1 mylance

# Mount
./juicefs mount redis://127.0.0.1:6379/1 /mnt/jfs -d
```

#### 8.3.3 Background Mounting (Recommended)

```bash
# -d is equivalent to --background, background mode
./juicefs format sqlite3:///tmp/juicefs-meta.db mylance
./juicefs mount sqlite3:///tmp/juicefs-meta.db /mnt/jfs -d

# Verify mount
df -h /mnt/jfs
# Expected: JuiceFS:mylance  1.0P  0  1.0P  0% /mnt/jfs

ls /mnt/jfs
# Expected: can list directory normally
```

### 8.4 Copy Lance Dataset to JuiceFS

```bash
# Create data directory
mkdir -p /mnt/jfs/data

# Copy Lance dataset to JuiceFS
cp -r /tmp/test_lance_data.lance /mnt/jfs/data/

# Verify files in JuiceFS
ls -la /mnt/jfs/data/test_lance_data.lance/
ls -la /mnt/jfs/data/test_lance_data.lance/_versions/
ls -la /mnt/jfs/data/test_lance_data.lance/data/
```

### 8.5 Testing Warmup

#### 8.5.1 Dataset-level Warmup

```bash
# Clear JuiceFS local cache (ensure no cache before warmup)
rm -rf ~/.juicefs/cache/

# Warm up entire Lance dataset
./juicefs warmup --lance /mnt/jfs/data/test_lance_data.lance

# Expected output:
# Lance mode: latest version, manifest-only=false, include-indices=false
# warmup cache file: 6
# (6 = manifest + 4 data files + version_hint or other metadata files)
```

**Cache location and verification:**

JuiceFS default cache directory is `~/.juicefs/cache/`, organized by volume UUID subdirectory:

```
~/.juicefs/cache/
└── <volume-uuid>/
    ├── raw/
    │   └── chunks/
    │       └── 0/
    │           └── 0/
    │               ├── 1_0_1326828   ← slice_id=1, block_index=0, size=1326828
    │               ├── 2_0_1328620
    │               ├── 3_0_1328748
    │               ├── 4_0_1329132
    │               ├── 5_0_533
    │               └── 6_0_1156
    └── .lock
```

File naming format: `{slice_id}_{block_index}_{size}`

**Three ways to verify cache is effective:**

```bash
# Method 1: Use --check to verify cache hit rate (recommended)
./juicefs warmup --lance --check /mnt/jfs/data/test_lance_data.lance
# Expected output: 6 files checked, 5.1 MiB of 5.1 MiB (100.0%) cached
# --check does not download data, only checks if blocks are already in cache

# Method 2: Check cache directory size
du -sh ~/.juicefs/cache/
# Before warmup: 0 or very small; after warmup: has data (e.g., 5.2M)

# Method 3: List cache files
find ~/.juicefs/cache/ -type f -name "*_*_*" | wc -l
# Expected: matches the file count from warmup output
```

#### 8.5.2 Manifest-only Warmup

```bash
# Only warm up manifest files
./juicefs warmup \
  --lance \
  --lance-manifest-only \
  /mnt/jfs/data/test_lance_data.lance

# Expected: only warms up manifest files under _versions/, not data/ directory
```

#### 8.5.3 Column-level Metadata Warmup

```bash
# Warm up id and name column metadata (ColumnMetadata protobuf + column-level buffers)
./juicefs warmup --lance --lance-columns "id,name" /mnt/jfs/data/test_lance_data.lance

# Warm up id column metadata + data page buffers (page buffer offsets/sizes)
./juicefs warmup --lance --lance-columns "id" --lance-column-data /mnt/jfs/data/test_lance_data.lance
```

**How column-level warmup works:**

Column-level warmup locates the specified column's ColumnMetadata protobuf by parsing the CMO (Column Metadata Offset) table in the V2 file footer, extracts byte ranges for page buffers and column buffers, then maps them to JuiceFS slices for warmup.

Warmup ranges include:
1. **ColumnMetadata protobuf itself** (position and length from CMO table)
2. **Column-level buffers** (dictionaries, statistics, etc.)
3. **Page data buffers** (only when `--lance-column-data`)

**Important notes:**

JuiceFS warmup minimum granularity is the slice (object storage object, each up to 64MiB). `FillCache` downloads all blocks of the entire slice. Therefore:
- Small files (< 64MiB): the entire file is one slice, column-level warmup same as full warmup
- Large files (multiple slices): if a column's data only spans a subset of slices, column-level warmup can reduce download volume

**Verifying column-level warmup (requires large dataset):**

```bash
# Create large dataset (each file ~200MB, spans 4 chunks)
python3 -c "
import lance, pyarrow as pa, os, numpy as np

num_rows = 200000
table = pa.table({
    'id': pa.array(range(num_rows), type=pa.int64()),
    'name': pa.array([f'item_{i:06d}' for i in range(num_rows)], type=pa.string()),
    'large_blob': pa.array([os.urandom(2048) for _ in range(num_rows)], type=pa.binary()),
})

lance.write_dataset(table, '/tmp/test_lance_huge.lance', mode='overwrite', max_rows_per_file=100000)
"

# Copy to JuiceFS
cp -r /tmp/test_lance_huge.lance /mnt/jfs/

# Full warmup
rm -rf ~/.juicefs/cache/
./juicefs warmup --lance /mnt/jfs/test_lance_huge.lance
du -sh ~/.juicefs/cache/
# Expected: ~395M (full data)

# id column warmup (with data pages, id column data at file tail, only 1 slice)
rm -rf ~/.juicefs/cache/
./juicefs warmup --lance --lance-columns "id" --lance-column-data /mnt/jfs/test_lance_huge.lance
du -sh ~/.juicefs/cache/
# Expected: ~11M (only the last chunk's slice)

# large_blob column warmup (data spans most of file, all chunks)
rm -rf ~/.juicefs/cache/
./juicefs warmup --lance --lance-columns "large_blob" --lance-column-data /mnt/jfs/test_lance_huge.lance
du -sh ~/.juicefs/cache/
# Expected: ~395M (large_blob occupies most of the data)
```

**Measured results (200MB dataset, 3 columns × 200K rows × 2 files):**

| Warmup Method | Cache Size | Cache Files | Notes |
|--------------|-----------|-------------|-------|
| Full warmup | 395M | 103 | All data |
| id column + data pages | 11M | 7 | id column data at file tail, only hits 1 slice |
| large_blob column + data pages | 395M | 103 | large_blob occupies most of the file data |

**Conclusion:** The actual effectiveness of column-level warmup depends on the physical distribution of column data within the file. When the target column's data is concentrated in a subset of slices (e.g., a small column at the file tail), it can significantly reduce download volume; when the column data occupies most of the file, the effect is minimal.

#### 8.5.4 Version-specific Warmup

```bash
# Warm up version 0
./juicefs warmup \
  --lance \
  --lance-version 0 \
  /mnt/jfs/data/test_lance_data.lance

# Expected output includes:
# Lance mode: version=0, manifest-only=false, include-indices=false
```

#### 8.5.5 Checking Cache Status

```bash
# Check which data blocks are cached (no actual warmup)
./juicefs warmup \
  --lance \
  --check \
  /mnt/jfs/data/test_lance_data.lance

# --check mode only checks cache hit rate, does not perform downloads

# Check local cache directory size
du -sh ~/.juicefs/cache/
```

#### 8.5.6 Evicting Cache

```bash
# Evict cached data blocks
./juicefs warmup \
  --lance \
  --evict \
  /mnt/jfs/data/test_lance_data.lance

# Verify cache has been cleared
du -sh ~/.juicefs/cache/
```

### 8.6 Verifying Warmup Effectiveness

#### Method 1: Read Latency Comparison

```bash
# Clear cache, read directly (cold read)
rm -rf ~/.juicefs/cache/
time cat /mnt/jfs/data/test_lance_data.lance/data/*.lance > /dev/null

# Warm up
./juicefs warmup \
  --lance \
  /mnt/jfs/data/test_lance_data.lance

# Read again (hot read)
time cat /mnt/jfs/data/test_lance_data.lance/data/*.lance > /dev/null

# Expected: hot read noticeably faster than cold read
```

#### Method 2: Verify via Python Lance Read

```python
#!/usr/bin/env python3
"""verify_lance_warmup.py — Verify Lance dataset readability after warmup"""

import lance
import time

ds = lance.dataset("/mnt/jfs/data/test_lance_data.lance")
print(f"Dataset: version={ds.version}, fragments={len(list(ds.get_fragments()))}")

# Cold/hot read comparison
start = time.time()
table = ds.to_table()
elapsed = time.time() - start
print(f"Full scan: {len(table)} rows in {elapsed:.3f}s")

# Column-level read
for col in ["id", "name", "score", "embedding"]:
    start = time.time()
    col_data = ds.to_table(columns=[col])
    elapsed = time.time() - start
    print(f"Column '{col}': {col_data.count_rows()} rows in {elapsed:.3f}s")
```

Run:

```bash
# Cold read
rm -rf ~/.juicefs/cache/
python3 verify_lance_warmup.py

# Warm up
./juicefs warmup --lance /mnt/jfs/data/test_lance_data.lance

# Hot read
python3 verify_lance_warmup.py
```

### 8.7 Post-test Cleanup

```bash
# Unmount JuiceFS
umount /mnt/jfs

# Clean up local storage
rm -rf ~/.juicefs/cache/
rm -f /tmp/juicefs-meta.db
rm -rf /tmp/juicefs-storage/
rm -rf /tmp/test_lance_data.lance
rm -f ./juicefs  # Optional: remove build artifact
```

### 8.8 Troubleshooting

| Problem | Possible Cause | Solution |
|---------|---------------|----------|
| `open control file: no such file` | JuiceFS not mounted | Confirm `mount` succeeded, `df -h /mnt/jfs` shows output |
| `find lance manifest: no manifest files found` | Incomplete directory structure | Confirm `.manifest` files exist under `_versions/` |
| `read footer: file too small` | Corrupted data file | Re-copy Lance dataset to JuiceFS |
| `column "xxx" not found` | Column name mismatch | Check column names with `python3 -c "import lance; print(lance.dataset(path).schema)"` |
| `warmup lance columns: ...` warning | V1 format data file | Ensure data files are V2 format (pylance 1.x writes V2 by default) |
| JuiceFS mount fails | Metadata engine connection issue | Confirm `format` and `mount` meta URL format is correct, SQLite uses `sqlite3:///path/to/db` |
| Permission denied | warmup requires root | Ensure current user has read/write permissions on the mount point |