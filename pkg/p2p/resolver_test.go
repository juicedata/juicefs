package p2p

import (
	"os"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

// newTestMeta creates an in-memory meta engine for testing.
func newTestMeta(t *testing.T) meta.Meta {
	t.Helper()
	_ = os.Remove("/tmp/juicefs.memkv.setting.json")
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test-resolver",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %v", err)
	}
	return m
}

// createFile creates a file under parent with the given name and writes a single
// slice of the given size, returning the file's inode.
func createFile(t *testing.T, m meta.Meta, ctx meta.Context, parent meta.Ino, name string, size uint32) meta.Ino {
	t.Helper()
	var ino meta.Ino
	if st := m.Create(ctx, parent, name, 0644, 022, 0, &ino, nil); st != 0 {
		t.Fatalf("create %q: %s", name, st)
	}
	if size == 0 {
		return ino
	}
	var sliceID uint64
	if st := m.NewSlice(ctx, &sliceID); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	if st := m.Write(ctx, ino, 0, 0, meta.Slice{Id: sliceID, Size: size, Len: size}, time.Now()); st != 0 {
		t.Fatalf("write %q: %s", name, st)
	}
	return ino
}

func TestMetaResolver_ResolveFile(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20 // 4MB

	// Create a 5MB file: should produce 2 blocks (4MB + 1MB)
	createFile(t, m, ctx, meta.RootInode, "testfile", 5<<20)

	resolver := NewMetaResolver(m, blockSize, false)
	blocks, err := resolver.Resolve(ctx, []string{"/testfile"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	// First block: 4MB
	if blocks[0].Size != blockSize {
		t.Errorf("block 0 size: got %d, want %d", blocks[0].Size, blockSize)
	}
	if blocks[0].Index != 0 {
		t.Errorf("block 0 index: got %d, want 0", blocks[0].Index)
	}

	// Second block: 1MB
	expectedSecond := 1 << 20
	if blocks[1].Size != expectedSecond {
		t.Errorf("block 1 size: got %d, want %d", blocks[1].Size, expectedSecond)
	}
	if blocks[1].Index != 1 {
		t.Errorf("block 1 index: got %d, want 1", blocks[1].Index)
	}

	// Keys must not be empty
	for i, b := range blocks {
		if b.Key == "" {
			t.Errorf("block %d has empty key", i)
		}
		if b.SliceID == 0 {
			t.Errorf("block %d has zero slice ID", i)
		}
	}
}

func TestMetaResolver_ResolveDirectory(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20 // 4MB

	// Create a directory with two files
	var dirIno meta.Ino
	if st := m.Mkdir(ctx, meta.RootInode, "mydir", 0755, 0, 0, &dirIno, nil); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}

	// File 1: 3MB -> 1 block
	createFile(t, m, ctx, dirIno, "small.bin", 3<<20)
	// File 2: 5MB -> 2 blocks
	createFile(t, m, ctx, dirIno, "large.bin", 5<<20)

	resolver := NewMetaResolver(m, blockSize, false)
	blocks, err := resolver.Resolve(ctx, []string{"/mydir"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// 1 block (3MB) + 2 blocks (5MB) = 3 blocks
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
}

func TestMetaResolver_SkipsZeroSliceID(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20 // 4MB

	// Create a file
	var ino meta.Ino
	if st := m.Create(ctx, meta.RootInode, "sparse", 0644, 022, 0, &ino, nil); st != 0 {
		t.Fatalf("create: %s", st)
	}

	// Write a slice to chunk 1 (skip chunk 0 to leave it as a hole)
	var sliceID uint64
	if st := m.NewSlice(ctx, &sliceID); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	sliceSize := uint32(2 << 20) // 2MB
	if st := m.Write(ctx, ino, 1, 0, meta.Slice{Id: sliceID, Size: sliceSize, Len: sliceSize}, time.Now()); st != 0 {
		t.Fatalf("write: %s", st)
	}

	// We need the file to span at least 2 chunks. The file length after writing to chunk 1
	// at offset 0 should be chunkSize + sliceSize.
	// Let's verify by reading the attr.
	var attr meta.Attr
	if st := m.GetAttr(ctx, ino, &attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	t.Logf("sparse file length: %d (expecting >= %d)", attr.Length, uint64(chunkSize)+uint64(sliceSize))

	resolver := NewMetaResolver(m, blockSize, false)
	blocks, err := resolver.resolveFile(ctx, ino, attr.Length)
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}

	// Only chunk 1 has data; chunk 0 should produce no blocks (all zeros / hole).
	// The 2MB data in chunk 1 -> 1 block.
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block (hole skipped), got %d", len(blocks))
	}
	if blocks[0].SliceID != sliceID {
		t.Errorf("block slice ID: got %d, want %d", blocks[0].SliceID, sliceID)
	}
}

func TestMetaResolver_Deduplication(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20 // 4MB

	createFile(t, m, ctx, meta.RootInode, "file1", 3<<20)

	resolver := NewMetaResolver(m, blockSize, false)
	// Resolve the same path twice
	blocks, err := resolver.Resolve(ctx, []string{"/file1", "/file1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Should be deduplicated: only 1 block, not 2
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block (deduplicated), got %d", len(blocks))
	}
}

func TestMetaResolver_EmptyFile(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20

	createFile(t, m, ctx, meta.RootInode, "empty", 0)

	resolver := NewMetaResolver(m, blockSize, false)
	blocks, err := resolver.Resolve(ctx, []string{"/empty"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks for empty file, got %d", len(blocks))
	}
}

func TestMetaResolver_HashPrefix(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20

	createFile(t, m, ctx, meta.RootInode, "hashed", 3<<20)

	resolver := NewMetaResolver(m, blockSize, true)
	blocks, err := resolver.Resolve(ctx, []string{"/hashed"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	// Hash-prefixed keys start with "chunks/XX/" where XX is a hex prefix
	key := blocks[0].Key
	if len(key) == 0 {
		t.Fatal("block key is empty")
	}
	// Hash prefix format: chunks/<2-hex>/<id/1000000>/<id>_<idx>_<size>
	if key[:7] != "chunks/" {
		t.Errorf("expected key to start with 'chunks/', got %q", key)
	}
}

func TestMetaResolver_NonexistentPath(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()

	resolver := NewMetaResolver(m, 4<<20, false)
	_, err := resolver.Resolve(ctx, []string{"/does/not/exist"})
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestMetaResolver_NestedDirectories(t *testing.T) {
	m := newTestMeta(t)
	ctx := meta.Background()
	blockSize := 4 << 20

	// Create /a/b/file.bin (3MB)
	var aIno, bIno meta.Ino
	if st := m.Mkdir(ctx, meta.RootInode, "a", 0755, 0, 0, &aIno, nil); st != 0 {
		t.Fatalf("mkdir a: %s", st)
	}
	if st := m.Mkdir(ctx, aIno, "b", 0755, 0, 0, &bIno, nil); st != 0 {
		t.Fatalf("mkdir b: %s", st)
	}
	createFile(t, m, ctx, bIno, "file.bin", 3<<20)

	resolver := NewMetaResolver(m, blockSize, false)
	blocks, err := resolver.Resolve(ctx, []string{"/a"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block from nested dir, got %d", len(blocks))
	}
}
