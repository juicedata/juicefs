/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package meta

import (
	"fmt"
	"testing"
)

// "big" wins on size, "many" wins on inodes, so topN=1 tells the two sort keys apart.
func TestGetTreeSummarySortBy(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-summary-sort", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}

	ctx := Background()
	var dir, ino Ino
	var attr Attr
	if st := m.Mkdir(ctx, RootInode, "big", 0755, 0, 0, &dir, &attr); st != 0 {
		t.Fatalf("mkdir big: %s", st)
	}
	if st := m.Create(ctx, dir, "f", 0644, 0, 0, &ino, &attr); st != 0 {
		t.Fatalf("create big/f: %s", st)
	}
	if st := m.Truncate(ctx, ino, 0, 10<<20, &attr, false); st != 0 {
		t.Fatalf("truncate big/f: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "many", 0755, 0, 0, &dir, &attr); st != 0 {
		t.Fatalf("mkdir many: %s", st)
	}
	for i := 0; i < 20; i++ {
		if st := m.Create(ctx, dir, fmt.Sprintf("f-%d", i), 0644, 0, 0, &ino, &attr); st != 0 {
			t.Fatalf("create many/f-%d: %s", i, st)
		}
	}

	for _, c := range []struct {
		sortBy TreeSort
		top    string
	}{
		{SortBySize, "big"},
		{SortByInodes, "many"},
	} {
		tree := &TreeSummary{Inode: RootInode}
		if st := m.GetTreeSummary(ctx, tree, 1, 1, true, c.sortBy, nil); st != 0 {
			t.Fatalf("tree summary: %s", st)
		}
		if len(tree.Children) != 2 || tree.Children[0].Path != c.top {
			t.Fatalf("sortBy %d: expect %q on top, got %+v", c.sortBy, c.top, tree.Children[0])
		}
		if tree.Children[1].Path != "..." {
			t.Fatalf("sortBy %d: expect omitted entry, got %q", c.sortBy, tree.Children[1].Path)
		}
	}
}
