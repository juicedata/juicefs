package p2p

import (
	"reflect"
	"strings"
	"testing"
)

func TestManifest_KeyIsDeterministic(t *testing.T) {
	k1 := ManifestKey([]string{"/models/llama"}, 4*1024*1024, false, "")
	k2 := ManifestKey([]string{"/models/llama"}, 4*1024*1024, false, "")
	if k1 != k2 {
		t.Errorf("ManifestKey not deterministic: %q != %q", k1, k2)
	}
}

func TestManifest_KeyIsOrderInsensitive(t *testing.T) {
	k1 := ManifestKey([]string{"/a", "/b"}, 4*1024*1024, false, "")
	k2 := ManifestKey([]string{"/b", "/a"}, 4*1024*1024, false, "")
	if k1 != k2 {
		t.Errorf("ManifestKey order-sensitive: %q != %q", k1, k2)
	}
}

func TestManifest_KeyDiffersOnPaths(t *testing.T) {
	k1 := ManifestKey([]string{"/a"}, 4*1024*1024, false, "")
	k2 := ManifestKey([]string{"/b"}, 4*1024*1024, false, "")
	if k1 == k2 {
		t.Errorf("ManifestKey should differ for different paths, both got %q", k1)
	}
}

func TestManifest_KeyDiffersOnBlockSize(t *testing.T) {
	k1 := ManifestKey([]string{"/a"}, 4*1024*1024, false, "")
	k2 := ManifestKey([]string{"/a"}, 8*1024*1024, false, "")
	if k1 == k2 {
		t.Errorf("ManifestKey should differ for different block sizes")
	}
}

func TestManifest_KeyDiffersOnHashPrefix(t *testing.T) {
	k1 := ManifestKey([]string{"/a"}, 4*1024*1024, false, "")
	k2 := ManifestKey([]string{"/a"}, 4*1024*1024, true, "")
	if k1 == k2 {
		t.Errorf("ManifestKey should differ for different hashPrefix")
	}
}

func TestManifest_KeyDefaultPrefixed(t *testing.T) {
	k := ManifestKey([]string{"/a"}, 4*1024*1024, false, "")
	const wantPrefix = "p2p_warmup/manifest-"
	const wantSuffix = ".json.gz"
	if !strings.HasPrefix(k, wantPrefix) || !strings.HasSuffix(k, wantSuffix) {
		t.Errorf("default key %q does not match %q...%q", k, wantPrefix, wantSuffix)
	}
}

func TestManifest_KeyWithNameOverride(t *testing.T) {
	k := ManifestKey([]string{"/a"}, 4*1024*1024, false, "llama-7b")
	const want = "p2p_warmup/llama-7b.json.gz"
	if k != want {
		t.Errorf("named key got %q, want %q", k, want)
	}
}

func TestManifest_KeyNameIgnoresContentInputs(t *testing.T) {
	// When name is set, paths/blocksize/hashPrefix do not affect the key.
	k1 := ManifestKey([]string{"/a"}, 4*1024*1024, false, "myrun")
	k2 := ManifestKey([]string{"/totally/different"}, 8*1024*1024, true, "myrun")
	if k1 != k2 {
		t.Errorf("named key should ignore content inputs: %q != %q", k1, k2)
	}
}

func TestManifest_ValidateAcceptsMatching(t *testing.T) {
	m := &Manifest{
		Paths:      []string{"/a", "/b"},
		BlockSize:  4 * 1024 * 1024,
		HashPrefix: false,
	}
	if err := m.Validate([]string{"/a", "/b"}, 4*1024*1024, false); err != nil {
		t.Errorf("Validate should accept matching content: %v", err)
	}
}

func TestManifest_ValidateAcceptsReorderedPaths(t *testing.T) {
	m := &Manifest{
		Paths:      []string{"/a", "/b"},
		BlockSize:  4 * 1024 * 1024,
		HashPrefix: false,
	}
	if err := m.Validate([]string{"/b", "/a"}, 4*1024*1024, false); err != nil {
		t.Errorf("Validate should accept reordered paths: %v", err)
	}
}

func TestManifest_ValidateRejectsBlockSizeMismatch(t *testing.T) {
	m := &Manifest{
		Paths:      []string{"/a"},
		BlockSize:  4 * 1024 * 1024,
		HashPrefix: false,
	}
	err := m.Validate([]string{"/a"}, 8*1024*1024, false)
	if err == nil {
		t.Fatal("Validate should reject blocksize mismatch")
	}
	if !strings.Contains(err.Error(), "blocksize") {
		t.Errorf("error should mention blocksize: %v", err)
	}
}

func TestManifest_ValidateRejectsHashPrefixMismatch(t *testing.T) {
	m := &Manifest{
		Paths:      []string{"/a"},
		BlockSize:  4 * 1024 * 1024,
		HashPrefix: false,
	}
	err := m.Validate([]string{"/a"}, 4*1024*1024, true)
	if err == nil {
		t.Fatal("Validate should reject hashprefix mismatch")
	}
	if !strings.Contains(err.Error(), "hashprefix") {
		t.Errorf("error should mention hashprefix: %v", err)
	}
}

func TestManifest_ValidateRejectsPathsMismatch(t *testing.T) {
	m := &Manifest{
		Paths:      []string{"/a"},
		BlockSize:  4 * 1024 * 1024,
		HashPrefix: false,
	}
	err := m.Validate([]string{"/b"}, 4*1024*1024, false)
	if err == nil {
		t.Fatal("Validate should reject paths mismatch")
	}
	if !strings.Contains(err.Error(), "paths") {
		t.Errorf("error should mention paths: %v", err)
	}
}

func TestManifest_RoundTrip(t *testing.T) {
	original := &Manifest{
		Version:     1,
		CreatedAt:   1714000000000,
		Paths:       []string{"/models/llama"},
		BlockSize:   4 * 1024 * 1024,
		HashPrefix:  false,
		TotalBlocks: 3,
		TotalBytes:  4198400,
		Blocks: []ManifestBlock{
			{Key: "chunks/0/5/5001_0_2048", Size: 2048},
			{Key: "chunks/0/5/5002_0_4194304", Size: 4194304},
			{Key: "chunks/0/5/5004_0_2097152", Size: 2097152},
		},
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := UnmarshalManifest(data)
	if err != nil {
		t.Fatalf("UnmarshalManifest: %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Errorf("round-trip mismatch:\n got: %#v\nwant: %#v", got, original)
	}
}

func TestManifest_MarshalIsGzipped(t *testing.T) {
	m := &Manifest{
		Version:     1,
		Paths:       []string{"/p"},
		BlockSize:   4 * 1024 * 1024,
		TotalBlocks: 1,
		Blocks:      []ManifestBlock{{Key: "k", Size: 10}},
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// gzip magic number: 0x1f, 0x8b
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Errorf("Marshal output is not gzipped: first bytes = %x", data[:min(2, len(data))])
	}
}

func TestBuildManifest_ConvertsBlocks(t *testing.T) {
	blocks := []*Block{
		{Key: "chunks/0/5/5001_0_2048", Size: 2048},
		{Key: "chunks/0/5/5002_0_4194304", Size: 4194304},
	}
	m := BuildManifest([]string{"/p"}, 4194304, false, blocks)
	if m.Version != 1 {
		t.Errorf("Version = %d, want 1", m.Version)
	}
	if m.TotalBlocks != 2 {
		t.Errorf("TotalBlocks = %d, want 2", m.TotalBlocks)
	}
	if m.TotalBytes != 4196352 {
		t.Errorf("TotalBytes = %d, want %d", m.TotalBytes, 4196352)
	}
	if len(m.Blocks) != 2 {
		t.Fatalf("Blocks len = %d, want 2", len(m.Blocks))
	}
	if m.Blocks[0].Key != "chunks/0/5/5001_0_2048" || m.Blocks[0].Size != 2048 {
		t.Errorf("Blocks[0] = %+v", m.Blocks[0])
	}
}

func TestBuildManifest_SortsBlocksByKey(t *testing.T) {
	// Input order: 5002 before 5001 — manifest should sort.
	blocks := []*Block{
		{Key: "chunks/0/5/5002_0_4194304", Size: 4194304},
		{Key: "chunks/0/5/5001_0_2048", Size: 2048},
	}
	m := BuildManifest([]string{"/p"}, 4194304, false, blocks)
	if m.Blocks[0].Key != "chunks/0/5/5001_0_2048" {
		t.Errorf("Blocks[0].Key = %q, want sorted result", m.Blocks[0].Key)
	}
	if m.Blocks[1].Key != "chunks/0/5/5002_0_4194304" {
		t.Errorf("Blocks[1].Key = %q, want sorted result", m.Blocks[1].Key)
	}
}

func TestManifestToBlocks_PreservesIndex(t *testing.T) {
	m := &Manifest{
		Blocks: []ManifestBlock{
			{Key: "a", Size: 1},
			{Key: "b", Size: 2},
			{Key: "c", Size: 3},
		},
	}
	blocks := ManifestToBlocks(m)
	if len(blocks) != 3 {
		t.Fatalf("len = %d, want 3", len(blocks))
	}
	for i, b := range blocks {
		if b.Index != i {
			t.Errorf("Blocks[%d].Index = %d, want %d", i, b.Index, i)
		}
	}
	if blocks[0].Key != "a" || blocks[0].Size != 1 {
		t.Errorf("Blocks[0] = %+v", blocks[0])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
