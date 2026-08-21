package vfs

// Integration-style tests that construct real Lance binary data in memory
// and verify the parsing pipeline end-to-end without needing a JuiceFS mount.

import (
	"bytes"
	"encoding/binary"
	"testing"

	lancepb "github.com/juicedata/juicefs/pkg/vfs/proto/lance"
	file2pb "github.com/juicedata/juicefs/pkg/vfs/proto/lance/file2"
	"google.golang.org/protobuf/proto"
)

// buildManifestFile builds a complete Lance manifest binary file in memory.
// Layout: [4B msg_len LE][protobuf data][8B manifest_pos LE][2B major][2B minor][4B MAGIC]
func buildManifestFileE2E(manifest *lancepb.Manifest) []byte {
	pbData, _ := proto.Marshal(manifest)
	manifestPos := uint64(0) // protobuf starts at offset 0

	var buf bytes.Buffer
	// 4-byte length prefix
	lenBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBytes, uint32(len(pbData)))
	buf.Write(lenBytes)
	// protobuf data
	buf.Write(pbData)
	// trailer (16 bytes)
	posBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(posBytes, manifestPos)
	buf.Write(posBytes)
	buf.Write([]byte{0, 0}) // major version
	buf.Write([]byte{2, 0}) // minor version
	buf.Write([]byte("LANC"))
	return buf.Bytes()
}

// buildV2DataFile builds a Lance V2 data file in memory with the given columns.
// Each column gets a ColumnMetadata protobuf and a CMO table entry.
func buildV2DataFile(columns []*file2pb.ColumnMetadata) []byte {
	var colMetadatas bytes.Buffer
	cmoTable := make([]byte, 0, len(columns)*16)

	for _, cm := range columns {
		pbData, _ := proto.Marshal(cm)
		pos := uint64(colMetadatas.Len())
		length := uint64(len(pbData))

		// CMO entry: position(8B) + length(8B)
		entry := make([]byte, 16)
		binary.LittleEndian.PutUint64(entry[0:8], pos)
		binary.LittleEndian.PutUint64(entry[8:16], length)
		cmoTable = append(cmoTable, entry...)

		colMetadatas.Write(pbData)
	}

	// Assemble file: [data pages placeholder][column metadatas][CMO table][footer]
	dataPagesPlaceholder := []byte{} // empty, data pages are referenced by offset

	colMetaStart := uint64(len(dataPagesPlaceholder))
	cmoStart := colMetaStart + uint64(colMetadatas.Len())
	gboStart := cmoStart + uint64(len(cmoTable))

	var buf bytes.Buffer
	buf.Write(dataPagesPlaceholder)
	buf.Write(colMetadatas.Bytes())
	buf.Write(cmoTable)

	// Footer (40 bytes)
	footer := make([]byte, 40)
	binary.LittleEndian.PutUint64(footer[0:8], colMetaStart)
	binary.LittleEndian.PutUint64(footer[8:16], cmoStart)
	binary.LittleEndian.PutUint64(footer[16:24], gboStart)
	binary.LittleEndian.PutUint32(footer[24:28], 0) // num_global_buffers
	binary.LittleEndian.PutUint32(footer[28:32], uint32(len(columns)))
	binary.LittleEndian.PutUint16(footer[32:34], 0) // major
	binary.LittleEndian.PutUint16(footer[34:36], 2) // minor
	copy(footer[36:40], []byte("LANC"))
	buf.Write(footer)

	return buf.Bytes()
}

// TestE2E_ManifestParseAndPathResolution
// Verifies: build manifest → parse → extract fragment data file paths
func TestE2E_ManifestParseAndPathResolution(t *testing.T) {
	manifest := &lancepb.Manifest{
		Version: 5,
		Fields: []*lancepb.Field{
			{Id: 0, Name: "id"},
			{Id: 1, Name: "label"},
		},
		Fragments: []*lancepb.DataFragment{
			{
				Id: 0,
				Files: []*lancepb.DataFile{
					{Path: "0000-abc.lance", Fields: []int32{0, 1}, ColumnIndices: []int32{0, 1}},
				},
			},
			{
				Id: 1,
				Files: []*lancepb.DataFile{
					{Path: "0001-def.lance", Fields: []int32{0, 1}, ColumnIndices: []int32{0, 1}},
				},
				DeletionFile: &lancepb.DeletionFile{
					ReadVersion: 3,
					Id:         0,
					FileType:   lancepb.DeletionFile_BITMAP,
				},
			},
		},
		TransactionFile: "txn-001.lance",
	}

	// Build binary manifest
	manifestBytes := buildManifestFileE2E(manifest)

	// Parse it back
	parsed, err := parseLanceManifestBytes(manifestBytes)
	if err != nil {
		t.Fatalf("parseLanceManifestBytes failed: %v", err)
	}

	// Verify version
	if parsed.Version != 5 {
		t.Errorf("version: got %d, want 5", parsed.Version)
	}

	// Verify fragments
	if len(parsed.Fragments) != 2 {
		t.Fatalf("fragments: got %d, want 2", len(parsed.Fragments))
	}

	// Verify data file paths
	if parsed.Fragments[0].Files[0].Path != "0000-abc.lance" {
		t.Errorf("frag0 file: got %q, want %q",
			parsed.Fragments[0].Files[0].Path, "0000-abc.lance")
	}
	if parsed.Fragments[1].Files[0].Path != "0001-def.lance" {
		t.Errorf("frag1 file: got %q, want %q",
			parsed.Fragments[1].Files[0].Path, "0001-def.lance")
	}

	// Verify deletion file path construction
	delPath := relativeDeletionFilePath(parsed.Fragments[1].GetId(),
		parsed.Fragments[1].DeletionFile)
	expected := "_deletions/1-3-0.bin"
	if delPath != expected {
		t.Errorf("deletion path: got %q, want %q", delPath, expected)
	}

	// Verify transaction file
	if parsed.TransactionFile != "txn-001.lance" {
		t.Errorf("transaction file: got %q, want %q",
			parsed.TransactionFile, "txn-001.lance")
	}

	t.Logf("✓ Manifest parse E2E: version=%d, fragments=%d, files=%d, deletion=%s, txn=%s",
		parsed.Version, len(parsed.Fragments), 2, delPath, parsed.TransactionFile)
}

// TestE2E_V2FooterAndColumnMetadata
// Verifies: build V2 data file → read footer → read CMO → parse ColumnMetadata → extract byte ranges
func TestE2E_V2FooterAndColumnMetadata(t *testing.T) {
	// Build two columns with realistic structure
	col0 := &file2pb.ColumnMetadata{
		Pages: []*file2pb.ColumnMetadata_Page{
			{
				BufferOffsets: []uint64{100, 500},
				BufferSizes:   []uint64{400, 200},
				Length:        1000,
			},
		},
		BufferOffsets: []uint64{700},
		BufferSizes:   []uint64{50},
	}
	col1 := &file2pb.ColumnMetadata{
		Pages: []*file2pb.ColumnMetadata_Page{
			{
				BufferOffsets: []uint64{1000, 2000},
				BufferSizes:   []uint64{500, 300},
				Length:        500,
			},
		},
		BufferOffsets: []uint64{2500},
		BufferSizes:   []uint64{100},
	}

	fileBytes := buildV2DataFile([]*file2pb.ColumnMetadata{col0, col1})
	fileLen := int64(len(fileBytes))

	// --- Simulate readLanceFileFooter ---
	// Read last 40 bytes
	footerBytes := fileBytes[fileLen-40:]
	magic := footerBytes[36:40]
	if !bytes.Equal(magic, []byte("LANC")) {
		t.Fatalf("bad magic: %x", magic)
	}

	footer := &lanceFileFooter{
		columnMetaStart:        binary.LittleEndian.Uint64(footerBytes[0:8]),
		columnMetaOffsetsStart: binary.LittleEndian.Uint64(footerBytes[8:16]),
		globalBuffOffsetsStart: binary.LittleEndian.Uint64(footerBytes[16:24]),
		numGlobalBuffers:       binary.LittleEndian.Uint32(footerBytes[24:28]),
		numColumns:             binary.LittleEndian.Uint32(footerBytes[28:32]),
		majorVersion:           binary.LittleEndian.Uint16(footerBytes[32:34]),
		minorVersion:           binary.LittleEndian.Uint16(footerBytes[34:36]),
	}

	if footer.numColumns != 2 {
		t.Fatalf("num_columns: got %d, want 2", footer.numColumns)
	}
	if footer.majorVersion != 0 || footer.minorVersion != 2 {
		t.Fatalf("version: got %d.%d, want 0.2", footer.majorVersion, footer.minorVersion)
	}

	// --- Simulate readColumnMetadataOffset + readColumnMetadata ---
	for colIdx := uint32(0); colIdx < footer.numColumns; colIdx++ {
		// Read CMO entry
		cmoOffset := int64(footer.columnMetaOffsetsStart) + int64(colIdx)*16
		cmoEntry := fileBytes[cmoOffset : cmoOffset+16]
		pos := binary.LittleEndian.Uint64(cmoEntry[0:8])
		length := binary.LittleEndian.Uint64(cmoEntry[8:16])

		// Read and parse ColumnMetadata
		cmBytes := fileBytes[pos : pos+length]
		cm := &file2pb.ColumnMetadata{}
		if err := proto.Unmarshal(cmBytes, cm); err != nil {
			t.Fatalf("unmarshal column %d: %v", colIdx, err)
		}

		// Extract byte ranges (metadata only)
		ranges := columnByteRanges(cm, false)
		t.Logf("✓ Column %d: %d pages, %d metadata ranges", colIdx, len(cm.Pages), len(ranges))

		// Extract byte ranges (with data pages)
		allRanges := columnByteRanges(cm, true)
		merged := mergeByteRanges(allRanges)
		t.Logf("  Column %d: %d total ranges → %d after merge", colIdx, len(allRanges), len(merged))
	}

	// --- Verify byte range extraction for col 0 ---
	// col0: page buffers [100,500) [500,700), column buffer [700,750)
	// merged: [100,750)
	col0Bytes := file2pb.ColumnMetadata{}
	col0Data, _ := proto.Marshal(col0)
	proto.Unmarshal(col0Data, &col0Bytes)
	ranges := columnByteRanges(&col0Bytes, true)
	merged := mergeByteRanges(ranges)
	if len(merged) != 1 {
		t.Errorf("col0 merged ranges: got %d, want 1", len(merged))
	}
	if merged[0].start != 100 || merged[0].end != 750 {
		t.Errorf("col0 merged range: got [%d,%d), want [100,750)",
			merged[0].start, merged[0].end)
	}

	t.Logf("✓ V2 footer + CMO + ColumnMetadata E2E: %d columns, col0 range [100,750)", footer.numColumns)
}

// TestE2E_FieldColumnMapResolution
// Verifies: manifest schema → build field-column map → resolve column names to indices
func TestE2E_FieldColumnMapResolution(t *testing.T) {
	manifest := &lancepb.Manifest{
		Fields: []*lancepb.Field{
			{Id: 0, Name: "user_id"},
			{Id: 1, Name: "event"},
			{Id: 2, Name: "timestamp"},
			{Id: 3, Name: "payload"},
		},
	}

	// Case 1: No DataFile → fallback DFS assignment
	m1 := buildFieldColumnMap(manifest, nil)
	if m1["user_id"] != 0 || m1["event"] != 1 || m1["timestamp"] != 2 || m1["payload"] != 3 {
		t.Errorf("fallback mapping: %+v", m1)
	}

	// Case 2: DataFile with explicit column indices (reversed order)
	dataFile := &lancepb.DataFile{
		Fields:        []int32{0, 1, 2, 3},
		ColumnIndices: []int32{3, 2, 1, 0}, // reversed
	}
	m2 := buildFieldColumnMap(manifest, dataFile)
	if m2["user_id"] != 3 || m2["event"] != 2 || m2["timestamp"] != 1 || m2["payload"] != 0 {
		t.Errorf("explicit mapping: %+v", m2)
	}

	// Case 3: DataFile with subset of fields
	dataFile2 := &lancepb.DataFile{
		Fields:        []int32{0, 2}, // only user_id and timestamp
		ColumnIndices: []int32{0, 1}, // columns 0 and 1 in this file
	}
	m3 := buildFieldColumnMap(manifest, dataFile2)
	if m3["user_id"] != 0 || m3["timestamp"] != 1 {
		t.Errorf("subset mapping: %+v", m3)
	}
	if _, exists := m3["event"]; exists {
		t.Errorf("event should not be in map")
	}

	t.Logf("✓ Field-column map E2E: fallback=%+v, explicit=%+v, subset=%+v", m1, m2, m3)
}

// TestE2E_FullPipeline
// Verifies: manifest → schema → field map → V2 file → footer → column metadata → byte ranges → merge
func TestE2E_FullPipeline(t *testing.T) {
	// 1. Build manifest with schema
	manifest := &lancepb.Manifest{
		Version: 1,
		Fields: []*lancepb.Field{
			{Id: 0, Name: "col_a"},
			{Id: 1, Name: "col_b"},
		},
		Fragments: []*lancepb.DataFragment{
			{
				Id: 0,
				Files: []*lancepb.DataFile{
					{
						Path:          "0000-test.lance",
						Fields:        []int32{0, 1},
						ColumnIndices: []int32{0, 1},
					},
				},
			},
		},
	}

	// 2. Build and parse manifest
	manifestBytes := buildManifestFileE2E(manifest)
	parsed, err := parseLanceManifestBytes(manifestBytes)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// 3. Build field-column map
	dataFile := parsed.Fragments[0].Files[0]
	colMap := buildFieldColumnMap(parsed, dataFile)
	if colMap["col_a"] != 0 || colMap["col_b"] != 1 {
		t.Fatalf("field map: %+v", colMap)
	}

	// 4. Build V2 data file with matching columns
	col0 := &file2pb.ColumnMetadata{
		Pages: []*file2pb.ColumnMetadata_Page{
			{
				BufferOffsets: []uint64{0, 1000},
				BufferSizes:   []uint64{1000, 500},
				Length:        100,
			},
		},
		BufferOffsets: []uint64{1500},
		BufferSizes:   []uint64{200},
	}
	col1 := &file2pb.ColumnMetadata{
		Pages: []*file2pb.ColumnMetadata_Page{
			{
				BufferOffsets: []uint64{2000},
				BufferSizes:   []uint64{800},
				Length:        100,
			},
		},
		BufferOffsets: []uint64{2800},
		BufferSizes:   []uint64{100},
	}
	fileBytes := buildV2DataFile([]*file2pb.ColumnMetadata{col0, col1})
	fileLen := int64(len(fileBytes))

	// 5. Read footer
	footerBytes := fileBytes[fileLen-40:]
	footer := &lanceFileFooter{
		columnMetaStart:        binary.LittleEndian.Uint64(footerBytes[0:8]),
		columnMetaOffsetsStart: binary.LittleEndian.Uint64(footerBytes[8:16]),
		globalBuffOffsetsStart: binary.LittleEndian.Uint64(footerBytes[16:24]),
		numGlobalBuffers:       binary.LittleEndian.Uint32(footerBytes[24:28]),
		numColumns:             binary.LittleEndian.Uint32(footerBytes[28:32]),
		majorVersion:           binary.LittleEndian.Uint16(footerBytes[32:34]),
		minorVersion:           binary.LittleEndian.Uint16(footerBytes[34:36]),
	}

	// 6. For col_a (index 0): read CMO → parse ColumnMetadata → extract ranges
	cmoOff := int64(footer.columnMetaOffsetsStart)
	cmoEntry := fileBytes[cmoOff : cmoOff+16]
	cmPos := binary.LittleEndian.Uint64(cmoEntry[0:8])
	cmLen := binary.LittleEndian.Uint64(cmoEntry[8:16])
	cmBytes := fileBytes[cmPos : cmPos+cmLen]
	cm := &file2pb.ColumnMetadata{}
	if err := proto.Unmarshal(cmBytes, cm); err != nil {
		t.Fatalf("unmarshal col_a metadata: %v", err)
	}

	// 7. Extract and merge byte ranges (metadata only)
	rangesMeta := columnByteRanges(cm, false)
	mergedMeta := mergeByteRanges(rangesMeta)
	// col0 column buffer: [1500, 1700) → 1 range
	if len(mergedMeta) != 1 || mergedMeta[0].start != 1500 || mergedMeta[0].end != 1700 {
		t.Errorf("col_a metadata ranges: %+v, want [{1500,1700}]", mergedMeta)
	}

	// 8. Extract and merge byte ranges (with data pages)
	rangesAll := columnByteRanges(cm, true)
	mergedAll := mergeByteRanges(rangesAll)
	// col0: pages [0,1000) [1000,1500), column buffer [1500,1700) → merged [0,1700)
	if len(mergedAll) != 1 || mergedAll[0].start != 0 || mergedAll[0].end != 1700 {
		t.Errorf("col_a all ranges: %+v, want [{0,1700}]", mergedAll)
	}

	t.Logf("✓ Full pipeline E2E:")
	t.Logf("  manifest: version=%d, %d fragments, %d fields", parsed.Version, len(parsed.Fragments), len(parsed.Fields))
	t.Logf("  field map: col_a→%d, col_b→%d", colMap["col_a"], colMap["col_b"])
	t.Logf("  V2 footer: %d columns, version %d.%d", footer.numColumns, footer.majorVersion, footer.minorVersion)
	t.Logf("  col_a metadata-only ranges: %v", mergedMeta)
	t.Logf("  col_a full ranges (with data pages): %v", mergedAll)
}
