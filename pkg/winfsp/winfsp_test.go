//go:build windows
// +build windows

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

package winfsp

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/winfsp/cgofuse/fuse"
)

// newTestVFS builds an in-memory VFS (memkv meta + mem object store) so the test
// has no external dependencies and can run on the Windows CI runner as-is.
func newTestVFS(t *testing.T) *vfs.VFS {
	t.Helper()
	metaConf := meta.DefaultConf()
	metaConf.MountPoint = "z:"
	m := meta.NewClient("memkv://", metaConf)
	format := &meta.Format{
		Name:        "test",
		UUID:        uuid.New().String(),
		Storage:     "mem",
		BlockSize:   4096,
		Compression: "lz4",
		DirStats:    true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	conf := &vfs.Config{
		Meta:    metaConf,
		Format:  *format,
		Version: "test",
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			Compress:   format.Compression,
			MaxUpload:  2,
			BufferSize: 30 << 20,
			CacheSize:  10 << 20,
			CacheDir:   "memory",
		},
		FuseOpts: &vfs.FuseOptions{},
	}
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_", registry)
	store := chunk.NewCachedStore(blob, *conf.Chunk, registry)
	return vfs.NewVFS(conf, m, store, registerer, registry)
}

// TestReaddirBatch verifies that (j *juice).Readdir returns every entry of a
// directory that contains more than one meta batch (DirBatchNum) of children.
//
// It guards against the WinFsp-specific regression where a single vfs.Readdir
// call returned at most DirBatchNum entries, so directories were truncated to
// the first batch (e.g. 4096 files). The fix loops over batches until the whole
// directory has been filled; this test fails if that loop is removed.
func TestReaddirBatch(t *testing.T) {
	v := newTestVFS(t)
	ctx := vfs.NewLogContext(meta.Background())

	// Shrink the batch size so the multi-batch loop is exercised cheaply.
	const batchNum = 100
	old := meta.DirBatchNum
	meta.DirBatchNum = map[string]int{"kv": batchNum, "redis": batchNum, "db": batchNum}
	defer func() { meta.DirBatchNum = old }()

	// Create a directory spanning several batches plus a partial last batch.
	const total = batchNum*3 + 7
	de, st := v.Mkdir(ctx, 1, "bigdir", 0755, 0)
	if st != 0 {
		t.Fatalf("mkdir bigdir: %s", st)
	}
	parent := de.Inode
	for i := 0; i < total; i++ {
		if _, e := v.Mkdir(ctx, parent, fmt.Sprintf("d%05d", i), 0755, 0); e != 0 {
			t.Fatalf("mkdir d%05d: %s", i, e)
		}
	}

	j := &juice{vfs: v, conf: v.Conf, asRoot: true}
	j.Init()

	fh, errno := v.Opendir(ctx, parent, 0)
	if errno != 0 {
		t.Fatalf("opendir: %s", errno)
	}
	defer v.Releasedir(ctx, parent, fh)
	j.handlers[fh] = handleInfo{ino: parent}

	seen := make(map[string]bool, total)
	fill := func(name string, stat *fuse.Stat_t, ofst int64) bool {
		if name != "." && name != ".." {
			seen[name] = true
		}
		return true
	}

	if e := j.Readdir("/bigdir", fill, 0, fh); e != 0 {
		t.Fatalf("readdir returned error: %d", e)
	}

	if len(seen) != total {
		t.Fatalf("expected %d entries, got %d (regression: directory truncated to a single batch of %d?)",
			total, len(seen), batchNum)
	}
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("d%05d", i)
		if !seen[name] {
			t.Fatalf("missing entry %s", name)
		}
	}
}
