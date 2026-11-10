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

func TestKVReaddirCursorDeleted(t *testing.T) {
	m := NewClient("memkv://", nil)
	m.OnMsg(DeleteSlice, func(args ...interface{}) error { return nil })
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	defer m.Shutdown()
	if err := m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	ctx := Background()

	const batchNum = 3
	old := DirBatchNum["kv"]
	DirBatchNum["kv"] = batchNum
	defer func() { DirBatchNum["kv"] = old }()

	var parent Ino
	if st := m.Mkdir(ctx, 1, "d", 0777, 022, 0, &parent, &Attr{}); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}

	// Create entries with lexicographically sorted names so the traversal spans
	// multiple batches and every batch boundary lands on a deletable cursor.
	const total = batchNum*2 + 1
	names := make([]string, total)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("f%02d", i)
		names[i] = name
		var ino Ino
		if st := m.Mknod(ctx, parent, name, TypeFile, 0644, 022, 0, "", &ino, nil); st != 0 {
			t.Fatalf("mknod %s: %s", name, st)
		}
	}

	h, st := m.NewDirHandler(ctx, parent, false, nil)
	if st != 0 {
		t.Fatalf("new dir handler: %s", st)
	}
	defer h.Close()

	seen := make(map[string]bool, total)
	offset := 0
	for {
		entries, st := h.List(ctx, offset)
		if st != 0 {
			t.Fatalf("list at offset %d: %s", offset, st)
		}
		if len(entries) == 0 {
			break
		}
		batch := make([]string, 0, len(entries))
		for _, e := range entries {
			name := string(e.Name)
			if name == "." || name == ".." {
				continue
			}
			seen[name] = true
			batch = append(batch, name)
		}
		offset += len(entries)
		// Simulate a concurrent recursive removal: delete the entries we just
		// listed (including the one that becomes the next pagination cursor)
		// before fetching the next batch.
		for _, name := range batch {
			if st := m.Unlink(ctx, parent, name); st != 0 {
				t.Fatalf("unlink %s: %s", name, st)
			}
		}
	}

	for _, name := range names {
		if !seen[name] {
			t.Fatalf("entry %s was skipped during readdir (issue #7223)", name)
		}
	}
}
