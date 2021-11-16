/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package vfs

import (
	"log"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

func createTestVFS() *VFS {
	mp := "/jfs"
	metaConf := &meta.Config{
		Retries:    10,
		Strict:     true,
		MountPoint: mp,
	}
	m := meta.NewClient("memkv://", metaConf)
	format := meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "mem",
		BlockSize: 4096,
	}
	err := m.Init(format, true)
	if err != nil {
		log.Fatalf("setting: %s", err)
	}
	conf := &Config{
		Meta:       metaConf,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			Compress:   format.Compression,
			MaxUpload:  2,
			BufferSize: 30 << 20,
			CacheSize:  10,
			CacheDir:   "memory",
		},
	}

	blob, _ := object.CreateStorage("mem", "", "", "")
	store := chunk.NewCachedStore(blob, *conf.Chunk)
	return NewVFS(conf, m, store)
}

func TestFill(t *testing.T) {
	v := createTestVFS()
	ctx := NewLogContext(meta.Background)
	entry, _ := v.Mkdir(ctx, 1, "test", 0777, 022)
	fe, fh, _ := v.Create(ctx, entry.Inode, "file", 0644, 0, uint32(os.O_WRONLY))
	_ = v.Write(ctx, fe.Inode, []byte("hello"), 0, fh)
	_ = v.Flush(ctx, fe.Inode, fh, 0)
	_ = v.Release(ctx, fe.Inode, fh)
	_, _ = v.Symlink(ctx, "test/file", 1, "sym")
	_, _ = v.Symlink(ctx, "/tmp/testfile", 1, "sym2")
	_, _ = v.Symlink(ctx, "testfile", 1, "sym3")

	// normal cases
	v.fillCache([]string{"/test/file", "/test", "/sym", "/"}, 2)

	// remove chunk
	var slices []meta.Slice
	_ = v.Meta.Read(meta.Background, fe.Inode, 0, &slices)
	for _, s := range slices {
		_ = v.Store.Remove(s.Chunkid, int(s.Size))
	}
	// bad cases
	v.fillCache([]string{"/test/file", "/sym2", "/sym3", "/.stats", "/not_exists"}, 2)
}
