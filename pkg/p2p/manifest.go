package p2p

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// ManifestVersion is the current schema version. Bump on incompatible changes.
const ManifestVersion = 1

// Manifest is the leader-resolved block list for a warmup run. The leader
// uploads it once; followers download it instead of re-scanning meta.
// Blocks are stored sorted by Key so every peer derives the same Index for
// the same block — required for partition scheduling agreement.
type Manifest struct {
	Version     int             `json:"version"`
	CreatedAt   int64           `json:"created_at"` // unix millis
	Paths       []string        `json:"paths"`
	BlockSize   int             `json:"block_size"`
	HashPrefix  bool            `json:"hash_prefix"`
	TotalBlocks int             `json:"total_blocks"`
	TotalBytes  int64           `json:"total_bytes"`
	Blocks      []ManifestBlock `json:"blocks"`
}

// ManifestBlock is the per-block info needed to fetch: the storage key and
// expected size (size powers FetchBlock's cache-hit fast-path).
type ManifestBlock struct {
	Key  string `json:"k"`
	Size int    `json:"s"`
}

// ManifestKeyPrefix groups all manifests under one prefix so operators can
// target them with a single lifecycle rule or list query.
const ManifestKeyPrefix = "p2p_warmup/"

// ManifestKey picks the object-storage key for a manifest. Two modes:
//
//   - name == "": content-addressable. The key is derived from paths +
//     block_size + hash_prefix (path order doesn't matter), so peers with
//     the same warmup args agree on the key automatically.
//   - name != "": uses the given basename. Lets operators pick a readable
//     identifier, but avoiding collisions across volumes / path-sets is
//     then their responsibility.
//
// TODO: automatic manifest cleanup is not implemented. Uploaded manifests
// accumulate under ManifestKeyPrefix until an operator removes them
// manually or via a bucket lifecycle rule.
func ManifestKey(paths []string, blockSize int, hashPrefix bool, name string) string {
	if name != "" {
		return ManifestKeyPrefix + name + ".json.gz"
	}
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)

	h := sha256.New()
	for _, p := range sorted {
		h.Write([]byte(p))
		h.Write([]byte{0}) // null separator
	}
	fmt.Fprintf(h, "blocksize=%d\n", blockSize)
	fmt.Fprintf(h, "hashprefix=%v\n", hashPrefix)
	digest := hex.EncodeToString(h.Sum(nil))
	return ManifestKeyPrefix + "manifest-" + digest + ".json.gz"
}

// BuildManifest constructs a Manifest from a block list, sorting by Key and
// summing totals. Paths are stored verbatim; ManifestKey's hash is
// order-insensitive, so caller order doesn't affect the storage key.
func BuildManifest(paths []string, blockSize int, hashPrefix bool, blocks []*Block) *Manifest {
	mblocks := make([]ManifestBlock, len(blocks))
	var totalBytes int64
	for i, b := range blocks {
		mblocks[i] = ManifestBlock{Key: b.Key, Size: b.Size}
		totalBytes += int64(b.Size)
	}
	sort.Slice(mblocks, func(i, j int) bool { return mblocks[i].Key < mblocks[j].Key })

	return &Manifest{
		Version:     ManifestVersion,
		CreatedAt:   time.Now().UnixMilli(),
		Paths:       paths,
		BlockSize:   blockSize,
		HashPrefix:  hashPrefix,
		TotalBlocks: len(blocks),
		TotalBytes:  totalBytes,
		Blocks:      mblocks,
	}
}

// Validate confirms the manifest matches the current warmup args (paths,
// block size, hash prefix). Path comparison is order-insensitive. A mismatch
// usually means another warmup is reusing the same --manifest-name — without
// this check, followers would silently fetch the wrong block set.
func (m *Manifest) Validate(paths []string, blockSize int, hashPrefix bool) error {
	if m.BlockSize != blockSize {
		return fmt.Errorf("blocksize mismatch: manifest=%d, expected=%d", m.BlockSize, blockSize)
	}
	if m.HashPrefix != hashPrefix {
		return fmt.Errorf("hashprefix mismatch: manifest=%v, expected=%v", m.HashPrefix, hashPrefix)
	}
	got := append([]string(nil), m.Paths...)
	want := append([]string(nil), paths...)
	sort.Strings(got)
	sort.Strings(want)
	if !pathsEqual(got, want) {
		return fmt.Errorf("paths mismatch: manifest=%v, expected=%v", m.Paths, paths)
	}
	return nil
}

func pathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ManifestToBlocks converts a parsed manifest into scheduler-ready *Blocks.
// Index is the manifest position — same across peers, so partition
// scheduling agrees.
func ManifestToBlocks(m *Manifest) []*Block {
	blocks := make([]*Block, len(m.Blocks))
	for i, mb := range m.Blocks {
		blocks[i] = &Block{
			Key:   mb.Key,
			Index: i,
			Size:  mb.Size,
		}
	}
	return blocks
}

// Marshal encodes the manifest as JSON and gzip-compresses the result.
func (m *Manifest) Marshal() ([]byte, error) {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(jsonBytes); err != nil {
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// UnmarshalManifest decodes a gzipped JSON manifest produced by Marshal.
func UnmarshalManifest(data []byte) (*Manifest, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()
	jsonBytes, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		return nil, fmt.Errorf("unmarshal json: %w", err)
	}
	return &m, nil
}
