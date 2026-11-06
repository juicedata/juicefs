package p2p

import (
	"fmt"
	"strings"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
)

const chunkSize = meta.ChunkSize // 64MB = 1 << 26

// MetaResolver resolves filesystem paths to a flat list of blocks
// via the JuiceFS metadata engine.
type MetaResolver struct {
	meta       meta.Meta
	blockSize  int
	hashPrefix bool
}

// NewMetaResolver creates a MetaResolver.
func NewMetaResolver(m meta.Meta, blockSize int, hashPrefix bool) *MetaResolver {
	return &MetaResolver{
		meta:       m,
		blockSize:  blockSize,
		hashPrefix: hashPrefix,
	}
}

// Resolve resolves all paths to blocks, deduplicates by key, and returns a flat block list.
func (r *MetaResolver) Resolve(ctx meta.Context, paths []string) ([]*Block, error) {
	seen := make(map[string]struct{})
	var result []*Block

	for _, p := range paths {
		blocks, err := r.resolvePath(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", p, err)
		}
		for _, b := range blocks {
			if _, ok := seen[b.Key]; !ok {
				seen[b.Key] = struct{}{}
				result = append(result, b)
			}
		}
	}
	return result, nil
}

// resolvePath resolves a single path to blocks.
// It first tries meta.Resolve for engines that support it (e.g. Redis),
// and falls back to walking path components via Lookup.
func (r *MetaResolver) resolvePath(ctx meta.Context, path string) ([]*Block, error) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		// Root directory
		return r.resolveDir(ctx, meta.RootInode)
	}

	ino, attr, err := r.lookupPath(ctx, path)
	if err != nil {
		return nil, err
	}

	switch attr.Typ {
	case meta.TypeDirectory:
		return r.resolveDir(ctx, ino)
	case meta.TypeFile:
		return r.resolveFile(ctx, ino, attr.Length)
	default:
		// Skip non-file, non-directory entries (symlinks, devices, etc.)
		return nil, nil
	}
}

// lookupPath resolves a path by walking each component via Lookup.
// Falls back from Resolve (single-call, not always supported) to
// component-by-component Lookup.
func (r *MetaResolver) lookupPath(ctx meta.Context, path string) (meta.Ino, meta.Attr, error) {
	var ino meta.Ino
	var attr meta.Attr

	// Try Resolve first (efficient single-call, supported by Redis)
	if st := r.meta.Resolve(ctx, meta.RootInode, path, &ino, &attr, false); st == 0 {
		return ino, attr, nil
	}

	// Fall back to walking path components via Lookup
	parent := meta.RootInode
	parts := strings.Split(path, "/")
	for _, name := range parts {
		if name == "" {
			continue
		}
		if st := r.meta.Lookup(ctx, parent, name, &ino, &attr, false); st != 0 {
			return 0, meta.Attr{}, fmt.Errorf("lookup %q in inode %d: %s", name, parent, st)
		}
		parent = ino
	}
	return ino, attr, nil
}

// resolveFile resolves a single file inode to blocks by iterating over its chunks.
func (r *MetaResolver) resolveFile(ctx meta.Context, ino meta.Ino, size uint64) ([]*Block, error) {
	if size == 0 {
		return nil, nil
	}

	chunkCnt := (size + chunkSize - 1) / chunkSize
	var result []*Block

	for idx := uint32(0); idx < uint32(chunkCnt); idx++ {
		var slices []meta.Slice
		if st := r.meta.Read(ctx, ino, idx, &slices); st != 0 {
			return nil, fmt.Errorf("read inode %d chunk %d: %s", ino, idx, st)
		}

		for _, s := range slices {
			if s.Id == 0 {
				continue // hole in sparse file
			}
			keys := chunk.SliceBlockKeys(s.Id, int(s.Size), r.blockSize, r.hashPrefix)
			for i, key := range keys {
				bsize := r.blockSize
				if remaining := int(s.Size) - i*r.blockSize; remaining < bsize {
					bsize = remaining
				}
				result = append(result, &Block{
					Key:     key,
					SliceID: s.Id,
					Index:   i,
					Size:    bsize,
				})
			}
		}
	}
	return result, nil
}

// resolveDir resolves a directory inode by recursing into its children.
func (r *MetaResolver) resolveDir(ctx meta.Context, ino meta.Ino) ([]*Block, error) {
	var entries []*meta.Entry
	if st := r.meta.Readdir(ctx, ino, 1, &entries); st != 0 {
		return nil, fmt.Errorf("readdir inode %d: %s", ino, st)
	}

	var result []*Block
	for _, e := range entries {
		name := string(e.Name)
		if name == "." || name == ".." {
			continue
		}
		switch e.Attr.Typ {
		case meta.TypeDirectory:
			blocks, err := r.resolveDir(ctx, e.Inode)
			if err != nil {
				return nil, err
			}
			result = append(result, blocks...)
		case meta.TypeFile:
			blocks, err := r.resolveFile(ctx, e.Inode, e.Attr.Length)
			if err != nil {
				return nil, err
			}
			result = append(result, blocks...)
		}
	}
	return result, nil
}
