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

package juicefs

import (
	"errors"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
)

var (
	_ fs.StatFS      = (*clientFS)(nil)
	_ fs.ReadDirFS   = (*clientFS)(nil)
	_ fs.ReadFileFS  = (*clientFS)(nil)
	_ fs.ReadLinkFS  = (*clientFS)(nil)
	_ fs.ReadDirFile = (*File)(nil)
)

// newFSTree disables the attribute cache, which keeps the old mtime of a
// directory after its entries change (P55).
func newFSTree(t *testing.T) *Client {
	t.Helper()
	conf := testConfig()
	conf.AttrCache = 0
	c, err := New("test", newTestVolume(t), conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.MkdirAll("a/b/c", 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir("empty", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "a/x.txt", "hello")
	writeFile(t, c, "a/b/y.go", "package y\n")
	writeFile(t, c, "a/b/c/z", "")
	if err := c.Symlink("x.txt", "a/link"); err != nil {
		t.Fatal(err)
	}
	if err := c.Symlink("a/b", "lnb"); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFS(t *testing.T) {
	c := newFSTree(t)
	fsys := c.FS()
	if err := fstest.TestFS(fsys, "a/x.txt", "a/b/y.go", "a/b/c/z", "a/link", "empty", "lnb"); err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(fsys, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(sub, "x.txt", "b/y.go", "link"); err != nil {
		t.Fatal(err)
	}

	if data, err := fs.ReadFile(fsys, "a/link"); err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile through a symlink: %q, %v", data, err)
	}
	if target, err := fs.ReadLink(fsys, "a/link"); err != nil || target != "x.txt" {
		t.Fatalf("ReadLink: %q, %v", target, err)
	}
	if fi, err := fs.Lstat(fsys, "lnb"); err != nil || fi.Mode().Type() != fs.ModeSymlink {
		t.Fatalf("Lstat: %v", err)
	}
	if fi, err := fs.Stat(fsys, "lnb"); err != nil || !fi.IsDir() {
		t.Fatalf("Stat through a symlink: %v", err)
	}
	if fi, err := fs.Stat(fsys, "."); err != nil || !fi.IsDir() || fi.Sys().(*Stat).Inode != 1 {
		t.Fatalf("Stat(.): %v", err)
	}

	var walked []string
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && (p[0] == '.' || d.Name() == "trash") {
			return fs.SkipDir
		}
		walked = append(walked, p)
		return nil
	})
	want := []string{".", "a", "a/b", "a/b/c", "a/b/c/z", "a/b/y.go", "a/link", "a/x.txt", "empty", "lnb"}
	if err != nil || !slices.Equal(walked, want) {
		t.Fatalf("WalkDir: %q, %v", walked, err)
	}
	if matches, err := fs.Glob(fsys, "a/*/*.go"); err != nil || !slices.Equal(matches, []string{"a/b/y.go"}) {
		t.Fatalf("Glob: %q, %v", matches, err)
	}
}

func TestFSInvalidPath(t *testing.T) {
	fsys := newFSTree(t).FS()
	calls := map[string]func(string) error{
		"open":     func(name string) error { return second(fsys.Open(name)) },
		"stat":     func(name string) error { return second(fs.Stat(fsys, name)) },
		"lstat":    func(name string) error { return second(fs.Lstat(fsys, name)) },
		"readdir":  func(name string) error { return second(fs.ReadDir(fsys, name)) },
		"readfile": func(name string) error { return second(fs.ReadFile(fsys, name)) },
		"readlink": func(name string) error { return second(fs.ReadLink(fsys, name)) },
	}
	for _, name := range []string{"", "/", "/a", "a/", "./a", "a/./x.txt", "../a", "a/../a", "a//x.txt"} {
		for op, call := range calls {
			if err := call(name); !isPathError(err, op, fs.ErrInvalid) {
				t.Errorf("%s(%q): %v", op, name, err)
			}
		}
	}
	for op, call := range calls {
		if err := call("missing"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s(missing): %v", op, err)
		}
	}
}

func TestFSOpen(t *testing.T) {
	c := newFSTree(t)
	f, err := c.FS().Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*File); !ok {
		t.Fatalf("Open returns %T", f)
	}
	if _, err := f.(fs.ReadDirFile).ReadDir(-1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if f, err := c.FS().Open("missing"); f != nil || err == nil {
		t.Fatalf("Open(missing): %#v, %v", f, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FS().Open("a"); !isPathError(err, "open", fs.ErrClosed) {
		t.Fatalf("Open after Close: %v", err)
	}
}
