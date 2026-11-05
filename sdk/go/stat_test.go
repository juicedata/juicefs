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
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFileMode(t *testing.T) {
	for _, c := range []struct {
		mode uint32
		want fs.FileMode
	}{
		{syscall.S_IFREG | 0644, 0644},
		{syscall.S_IFDIR | 0755, fs.ModeDir | 0755},
		{syscall.S_IFLNK | 0777, fs.ModeSymlink | 0777},
		{syscall.S_IFIFO | 0600, fs.ModeNamedPipe | 0600},
		{syscall.S_IFSOCK | 0755, fs.ModeSocket | 0755},
		{syscall.S_IFCHR | 0620, fs.ModeDevice | fs.ModeCharDevice | 0620},
		{syscall.S_IFBLK | 0660, fs.ModeDevice | 0660},
		{syscall.S_IFREG | 04755, fs.ModeSetuid | 0755},
		{syscall.S_IFDIR | 02775, fs.ModeDir | fs.ModeSetgid | 0775},
		{syscall.S_IFDIR | 01777, fs.ModeDir | fs.ModeSticky | 0777},
		{syscall.S_IFREG | 07000, fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky},
	} {
		if got := fileMode(c.mode); got != c.want {
			t.Errorf("fileMode(%#o) = %v, want %v", c.mode, got, c.want)
		}
	}
}

func TestFileModeLikeOS(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	sub := filepath.Join(dir, "dir")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0755|fs.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0777|fs.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file", "dir", "link", "fifo"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		mode := uint32(fi.Sys().(*syscall.Stat_t).Mode)
		if got := fileMode(mode); got != fi.Mode() {
			t.Errorf("%s: fileMode(%#o) = %v, os gives %v", name, mode, got, fi.Mode())
		}
	}
}

func TestSyscallMode(t *testing.T) {
	for _, c := range []struct {
		mode fs.FileMode
		want uint16
	}{
		{0755, 0755},
		{fs.ModeDir | 0700, 0700},
		{0755 | fs.ModeSetuid, 04755},
		{0755 | fs.ModeSetgid, 02755},
		{0777 | fs.ModeSticky, 01777},
		{0777 | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky, 07777},
		// As with os, the numeric 02000 is not fs.ModeSetgid and is dropped.
		{0o2755, 0755},
	} {
		if got := syscallMode(c.mode); got != c.want {
			t.Errorf("syscallMode(%v) = %#o, want %#o", c.mode, got, c.want)
		}
	}
	for _, m := range []fs.FileMode{0640, fs.ModeSetuid | 0755, fs.ModeSetgid | 0750, fs.ModeSticky | 0777} {
		if got := fileMode(syscall.S_IFREG | uint32(syscallMode(m))); got != m {
			t.Errorf("round trip of %v: got %v", m, got)
		}
	}
}

func TestFileInfo(t *testing.T) {
	mtime := time.Unix(1700000000, 123456789)
	st := &Stat{Inode: 2, Mode: syscall.S_IFDIR | 0755, Size: 4096, Mtime: mtime}
	fi := newFileInfo("/a/b", st)
	if fi.Name() != "b" || fi.Size() != 4096 || fi.Mode() != fs.ModeDir|0755 ||
		!fi.ModTime().Equal(mtime) || !fi.IsDir() || fi.Sys() != st {
		t.Fatalf("got name %q size %d mode %v mtime %v dir %v sys %v",
			fi.Name(), fi.Size(), fi.Mode(), fi.ModTime(), fi.IsDir(), fi.Sys())
	}

	file := newFileInfo("f", &Stat{Mode: syscall.S_IFREG | 0644, Size: 10})
	if file.IsDir() || file.Size() != 10 || file.Mode() != 0644 {
		t.Fatalf("file: dir %v size %d mode %v", file.IsDir(), file.Size(), file.Mode())
	}

	// As os.Stat.
	for name, want := range map[string]string{
		"/":     "/",
		"//":    "/",
		".":     ".",
		"a":     "a",
		"/a/b/": "b",
		"a/..":  "..",
	} {
		if got := newFileInfo(name, st).Name(); got != want {
			t.Errorf("Name of %q = %q, want %q", name, got, want)
		}
	}
}

func TestDirEntry(t *testing.T) {
	fi := newFileInfo("link", &Stat{Mode: syscall.S_IFLNK | 0777, Size: 4})
	de := fs.FileInfoToDirEntry(fi)
	info, err := de.Info()
	if de.Name() != "link" || de.IsDir() || de.Type() != fs.ModeSymlink || info != fi || err != nil {
		t.Fatalf("got name %q dir %v type %v info %v err %v", de.Name(), de.IsDir(), de.Type(), info, err)
	}
	dir := fs.FileInfoToDirEntry(newFileInfo("d", &Stat{Mode: syscall.S_IFDIR | 0755}))
	if !dir.IsDir() || dir.Type() != fs.ModeDir {
		t.Fatalf("dir: dir %v type %v", dir.IsDir(), dir.Type())
	}
}
