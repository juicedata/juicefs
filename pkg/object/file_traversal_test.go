/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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

package object

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFilestoreRejectsKeyPathTraversal verifies that an object key
// containing ".." segments, as could arrive from a source object storage
// backend's own key listing during e.g. `juicefs sync`, cannot cause a
// filestore-backed destination to write outside its configured root.
func TestFilestoreRejectsKeyPathTraversal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "dest") + "/"
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	store, err := newDisk(root, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	maliciousKey := "../outside/pwned.txt"
	if err := store.Put(context.Background(), maliciousKey, strings.NewReader("PWNED")); err == nil {
		t.Fatal("expected Put to reject a key escaping the storage root")
	}

	escaped := filepath.Join(outside, "pwned.txt")
	if _, statErr := os.Stat(escaped); statErr == nil {
		t.Fatalf("object escaped destination root and was written to %s", escaped)
	}

	if _, err := store.Head(context.Background(), maliciousKey); err == nil {
		t.Fatal("expected Head to reject a key escaping the storage root")
	}

	if _, _, _, err := store.List(context.Background(), maliciousKey, "", "", "/", 1000, false); err == nil {
		t.Fatal("expected List to reject a prefix escaping the storage root")
	}

	mtimeChanger, ok := store.(MtimeChanger)
	if !ok {
		t.Fatal("filestore should support changing mtime")
	}
	if err := mtimeChanger.Chtimes(maliciousKey, time.Now()); err == nil {
		t.Fatal("expected Chtimes to reject a key escaping the storage root")
	}
}
