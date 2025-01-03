/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package vfs

import (
	"os"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestFill(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	entry, _ := v.Mkdir(ctx, 1, "test", 0777, 022)
	fe, fh, _ := v.Create(ctx, entry.Inode, "file", 0644, 0, uint32(os.O_WRONLY))
	_ = v.Write(ctx, fe.Inode, []byte("hello"), 0, fh)
	_ = v.Flush(ctx, fe.Inode, fh, 0)
	v.Release(ctx, fe.Inode, fh)
	_, _ = v.Symlink(ctx, "test/file", 1, "sym")
	_, _ = v.Symlink(ctx, "/tmp/testfile", 1, "sym2")
	_, _ = v.Symlink(ctx, "testfile", 1, "sym3")

	// normal cases
	v.cache(meta.Background(), WarmupCache, []string{"/test/file", "/test", "/sym", "/"}, 2, nil)

	// remove chunk
	var slices []meta.Slice
	_ = v.Meta.Read(meta.Background(), fe.Inode, 0, &slices)
	for _, s := range slices {
		_ = v.Store.Remove(s.Id, int(s.Size))
	}
	// bad cases
	v.cache(meta.Background(), WarmupCache, []string{"/test/file", "/sym2", "/sym3", "/.stats", "/not_exists"}, 2, nil)
}
