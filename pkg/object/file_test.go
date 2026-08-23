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

func TestFilestoreRejectsSymlinkAncestorEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "dest") + "/"
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store, err := newDisk(root, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "link/pwned.txt", strings.NewReader("PWNED")); err == nil {
		t.Fatal("expected Put to reject a symlink ancestor escaping the storage root")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("escaped file should not exist, got %v", err)
	}
	if err := store.Put(context.Background(), "nested/ok.txt", strings.NewReader("OK")); err != nil {
		t.Fatalf("legitimate nested Put failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "nested", "ok.txt")); err != nil || string(data) != "OK" {
		t.Fatalf("legitimate nested Put produced %q, %v", data, err)
	}
	links := store.(SupportSymlink)
	if err := links.Symlink(outside, "external-link"); err != nil {
		t.Fatalf("creating a symlink object should remain supported: %v", err)
	}
	if target, err := links.Readlink("external-link"); err != nil || target != outside {
		t.Fatalf("readlink returned %q, %v", target, err)
	}
}
