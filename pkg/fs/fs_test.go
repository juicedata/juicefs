/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

package fs

import (
	"testing"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
)

// nolint:errcheck
func TestFileSystem(t *testing.T) {
	m, err := meta.NewRedisMeta("redis://127.0.0.1:6379/10", &meta.RedisConfig{})
	if err != nil {
		t.Logf("redis is not available: %s", err)
		t.Skip()
	}
	format := meta.Format{
		Name:      "test",
		BlockSize: 4096,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: &meta.Config{},
		Chunk: &chunk.Config{
			BlockSize: 4096,
		},
	}
	store := chunk.NewDiskStore("/tmp")
	fs, _ := NewFileSystem(&conf, m, store)
	ctx := meta.Background
	if _, err := fs.Create(ctx, "/hello", 0644); err != 0 {
		t.Fatalf("create /hello: %s", err)
	}
	defer fs.Delete(ctx, "/hello")
	if _, err := fs.Stat(ctx, "/hello"); err != 0 {
		t.Fatalf("stat /hello: %s", err)
	}
	if err := fs.Delete(ctx, "/hello"); err != 0 {
		t.Fatalf("delete /hello: %s", err)
	}
}
