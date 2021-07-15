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
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
)

// nolint:errcheck
func TestFileSystem(t *testing.T) {
	m := meta.NewClient("redis://127.0.0.1:6379/10", &meta.Config{})
	format := meta.Format{
		Name:      "test",
		BlockSize: 4096,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: &meta.Config{},
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize << 10,
			MaxUpload:  1,
			BufferSize: 100 << 20,
		},
	}
	objStore, _ := object.CreateStorage("mem", "", "", "")
	store := chunk.NewCachedStore(objStore, *conf.Chunk)
	fs, _ := NewFileSystem(&conf, m, store)
	ctx := meta.Background
	fs.Delete(ctx, "/hello")
	f, err := fs.Create(ctx, "/hello", 0644)
	if err != 0 {
		t.Fatalf("create /hello: %s", err)
	}
	if n, err := f.Write(ctx, []byte("world")); err != 0 || n != 5 {
		t.Fatalf("write 5 bytes: %d %s", n, err)
	}
	var buf = make([]byte, 10)
	if n, err := f.Pread(ctx, buf, 2); err != nil || n != 3 || string(buf[:n]) != "rld" {
		t.Fatalf("pread(2): %d %s %s", n, err, string(buf[:n]))
	}
	if n, err := f.Seek(ctx, -3, io.SeekEnd); err != nil || n != 2 {
		t.Fatalf("seek 3 bytes before end: %d %s", n, err)
	}
	if n, err := f.Write(ctx, []byte("t")); err != 0 || n != 1 {
		t.Fatalf("write 5 bytes: %d %s", n, err)
	}
	if n, err := f.Seek(ctx, -2, io.SeekCurrent); err != nil || n != 1 {
		t.Fatalf("seek 2 bytes before current: %d %s", n, err)
	}
	if n, err := f.Read(ctx, buf); err != nil || n != 4 || string(buf[:n]) != "otld" {
		t.Fatalf("read(): %d %s %s", n, err, string(buf[:n]))
	}
	defer fs.Delete(ctx, "/hello")
	if _, err := fs.Stat(ctx, "/hello"); err != 0 {
		t.Fatalf("stat /hello: %s", err)
	}
	if err := fs.Delete(ctx, "/hello"); err != 0 {
		t.Fatalf("delete /hello: %s", err)
	}
}
