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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func openFile(t *testing.T, c *Client, name string, flag int) *File {
	t.Helper()
	f, err := c.OpenFile(name, flag, 0666)
	if err != nil {
		t.Fatalf("OpenFile(%s, %#o): %v", name, flag, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func readFile(t *testing.T, c *Client, name string) string {
	t.Helper()
	data, err := c.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return string(data)
}

func writeFile(t *testing.T, c *Client, name, data string) {
	t.Helper()
	if err := c.WriteFile(name, []byte(data), 0666); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func isPathError(err error, op string, want error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && pe.Op == op && errors.Is(err, want)
}

// TestOpenModes opens the flags of the Python modes (design 7.1) on an
// existing file, a missing file and a directory; want is that of os on Linux.
func TestOpenModes(t *testing.T) {
	c := newTestClient(t)
	const (
		rw = os.O_RDWR
		wo = os.O_WRONLY
		ct = os.O_CREATE | os.O_TRUNC
		ca = os.O_CREATE | os.O_APPEND
		cx = os.O_CREATE | os.O_EXCL
	)
	// Each opened file is read and written one byte; content is that of the
	// existing file "hello" afterwards.
	for i, m := range []struct {
		py              string
		flag            int
		file, miss, dir syscall.Errno
		content         string
		create          bool
	}{
		{"r", os.O_RDONLY, 0, syscall.ENOENT, 0, "hello", false},
		{"r+", rw, 0, syscall.ENOENT, syscall.EISDIR, "hxllo", false},
		{"w", wo | ct, 0, 0, syscall.EISDIR, "x", true},
		{"w+", rw | ct, 0, 0, syscall.EISDIR, "x", true},
		{"a", wo | ca, 0, 0, syscall.EISDIR, "hellox", true},
		{"a+", rw | ca, 0, 0, syscall.EISDIR, "hellox", true},
		{"x", wo | cx, syscall.EEXIST, 0, syscall.EEXIST, "hello", true},
		{"x+", rw | cx, syscall.EEXIST, 0, syscall.EEXIST, "hello", true},
	} {
		dir := fmt.Sprintf("m%d", i)
		if err := c.MkdirAll(dir+"/d", 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, c, dir+"/f", "hello")
		for name, want := range map[string]syscall.Errno{"f": m.file, "miss": m.miss, "d": m.dir} {
			f, err := c.OpenFile(dir+"/"+name, m.flag, 0666)
			if got := errnoOf(err); got != want {
				t.Errorf("%s %s: got %v, want %v", m.py, name, got, want)
			}
			if err != nil {
				if !isPathError(err, "open", want) {
					t.Errorf("%s %s: %#v", m.py, name, err)
				}
				continue
			}
			readable, writable := m.flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY, m.flag&(os.O_WRONLY|os.O_RDWR) != 0
			_, rerr := f.Read(make([]byte, 1))
			_, werr := f.Write([]byte("x"))
			if name == "d" { // EISDIR for reading
				readable, writable = true, false
			}
			if (errnoOf(rerr) == syscall.EBADF) == readable || (errnoOf(werr) == syscall.EBADF) == writable {
				t.Errorf("%s %s: read %v, write %v", m.py, name, rerr, werr)
			}
			if err := f.Close(); err != nil {
				t.Errorf("%s %s: close %v", m.py, name, err)
			}
		}
		if got := readFile(t, c, dir+"/f"); got != m.content {
			t.Errorf("%s: content %q, want %q", m.py, got, m.content)
		}
		if exists(t, c, dir+"/miss") != m.create {
			t.Errorf("%s: created %v, want %v", m.py, !m.create, m.create)
		}
	}
}

// TestOpenPaths checks design 7.1: want is the result of os.OpenFile on Linux
// in a tree made by newTree.
func TestOpenPaths(t *testing.T) {
	c := newTestClient(t)
	const (
		ro = os.O_RDONLY
		wo = os.O_WRONLY
		cr = os.O_CREATE
		ex = os.O_EXCL
	)
	for i, x := range []struct {
		name  string
		flag  int
		want  syscall.Errno
		known string
	}{
		{"d", ro, 0, ""},
		{"d", ro | os.O_APPEND, 0, ""},
		{"lnd", ro, 0, ""},
		{"d", wo, syscall.EISDIR, ""},
		{"d", os.O_RDWR, syscall.EISDIR, ""},
		{"d", ro | cr, syscall.EISDIR, ""},
		{"d", ro | os.O_TRUNC, syscall.EISDIR, ""},
		{"d", ro | cr | ex, syscall.EEXIST, ""},
		{"d/", ro, 0, ""},
		{"d/.", ro, 0, ""},
		{"d/..", ro, 0, ""},
		{"d/.", wo, syscall.EISDIR, ""},
		{"d/.", wo | cr, syscall.EISDIR, ""},
		{"d/..", wo | cr, syscall.EISDIR, ""},
		{"d/.", wo | cr | ex, syscall.EEXIST, ""},
		{"f/", ro, syscall.ENOTDIR, ""},
		{"lnf/", ro, syscall.ENOTDIR, ""},
		{"f/x", wo | cr, syscall.ENOTDIR, ""},
		{"f/", wo | cr, syscall.EISDIR, "P45"},
		{"new/", wo | cr, syscall.EISDIR, "P45"},
		{"new/", wo | cr | ex, syscall.EISDIR, "P45"},
		{"dang/", wo | cr, syscall.EISDIR, "P45"},
		{"dang", wo | cr, 0, "P46"},
		{"dang", wo | cr | ex, syscall.EEXIST, ""},
		{"lnf", wo | cr | ex, syscall.EEXIST, ""},
		{"nonexist", ro, syscall.ENOENT, ""},
		{"f", ro | ex, 0, ""},
		{"f", wo | os.O_RDWR, syscall.EINVAL, ""}, // Q7; Linux opens it
		{"f", wo | os.O_SYNC, syscall.EINVAL, ""}, // Q7
		{"f", ro | syscall.O_NOFOLLOW, syscall.EINVAL, ""},
	} {
		desc := fmt.Sprintf("OpenFile(%s, %#o)", x.name, x.flag)
		if x.known != "" {
			t.Logf("skip %s: known issue %s", desc, x.known)
			continue
		}
		dir := fmt.Sprintf("t%d", i)
		newTree(t, c, dir)
		f, err := c.OpenFile(dir+"/"+x.name, x.flag, 0666)
		if got := errnoOf(err); got != x.want {
			t.Errorf("%s: got %v (%d), want %v (%d)", desc, got, got, x.want, x.want)
		}
		if err == nil {
			_ = f.Close()
		} else if !isPathError(err, "open", x.want) {
			t.Errorf("%s: %#v", desc, err)
		}
	}
}

func TestOpenPermission(t *testing.T) {
	c := newTestClient(t)
	root := os.Geteuid() == 0
	want := func(e syscall.Errno) syscall.Errno {
		if root {
			return 0
		}
		return e
	}
	writeFile(t, c, "ro", "hello")
	writeFile(t, c, "wo", "hello")
	if err := c.Chmod("ro", 0444); err != nil {
		t.Fatal(err)
	}
	if err := c.Chmod("wo", 0200); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir("nr", 0300); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name  string
		flag  int
		want  syscall.Errno
		known string
	}{
		{"ro", os.O_RDONLY, 0, ""},
		{"ro", os.O_WRONLY, want(syscall.EACCES), ""},
		{"wo", os.O_RDONLY, want(syscall.EACCES), ""},
		{"wo", os.O_WRONLY, 0, ""},
		{"ro", os.O_RDONLY | os.O_TRUNC, want(syscall.EACCES), ""},
		{"nr", os.O_RDONLY, want(syscall.EACCES), "P43"},
	} {
		desc := fmt.Sprintf("OpenFile(%s, %#o)", x.name, x.flag)
		if x.known != "" && x.want != 0 {
			t.Logf("skip %s: known issue %s", desc, x.known)
			continue
		}
		f, err := c.OpenFile(x.name, x.flag, 0)
		if got := errnoOf(err); got != x.want {
			t.Errorf("%s: got %v, want %v", desc, got, x.want)
		}
		if err == nil {
			_ = f.Close()
		}
	}
	// O_TRUNC truncates whatever the access mode, as open(2) on Linux and
	// macOS; without the permission the file is kept.
	want0 := "hello"
	if root {
		want0 = ""
	}
	if err := c.Chmod("ro", 0644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c, "ro"); got != want0 {
		t.Fatalf("ro after O_RDONLY|O_TRUNC: %q, want %q", got, want0)
	}
	writeFile(t, c, "t", "hello")
	f := openFile(t, c, "t", os.O_RDONLY|os.O_TRUNC)
	if st, err := f.Stat(); err != nil || st.Size() != 0 {
		t.Fatalf("Stat after O_RDONLY|O_TRUNC: %v %v", st, err)
	}
}

func TestOpenPerm(t *testing.T) {
	c := newTestClient(t)
	for _, perm := range []fs.FileMode{0600, 0777, 0755 | fs.ModeSetuid | fs.ModeSticky, 0o2755} {
		name := fmt.Sprintf("f%o", uint32(perm))
		f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		st, err := c.Stat(name)
		want := perm & (fs.ModePerm | fs.ModeSetuid | fs.ModeSticky) &^ fs.FileMode(c.umask)
		if err != nil || st.Mode() != want {
			t.Errorf("perm %v: mode %v, want %v (%v)", perm, st.Mode(), want, err)
		}
	}
	// perm is ignored for an existing file
	if err := c.WriteFile("f600", []byte("x"), 0777); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.Stat("f600"); st.Mode() != 0600&^fs.FileMode(c.umask) {
		t.Fatalf("mode of existing file: %v", st.Mode())
	}
}

// TestOpenCreateConcurrently covers P5: a file created by others between open
// and create is opened.
func TestOpenCreateConcurrently(t *testing.T) {
	c := newTestClient(t)
	for i := range 10 {
		name := fmt.Sprintf("f%d", i)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0666)
				if err != nil {
					t.Errorf("OpenFile(%s): %v", name, err)
					return
				}
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
}

// TestReadWrite follows the reads and writes in test() of the Python SDK.
func TestReadWrite(t *testing.T) {
	c := newTestClient(t)
	if err := c.MkdirAll("d", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "d/file", "hello")
	f := openFile(t, c, "d/file", os.O_RDWR|os.O_CREATE|os.O_APPEND)
	if n, err := f.WriteString("world"); n != 5 || err != nil {
		t.Fatalf("WriteString: %d %v", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c, "d/file"); got != "helloworld" {
		t.Fatalf("content %q", got)
	}

	writeFile(t, c, "d/lines", "first\rpart\nsecond\nlast")
	f = openFile(t, c, "d/lines", os.O_RDONLY)
	r := bufio.NewReader(f)
	for _, want := range []string{"first\rpart\n", "second\n", "last"} {
		if line, err := r.ReadString('\n'); line != want || (err != nil) != (want == "last") {
			t.Fatalf("ReadString: %q %v, want %q", line, err, want)
		}
	}
	if _, err := r.ReadString('\n'); err != io.EOF {
		t.Fatalf("ReadString at end: %v", err)
	}

	if _, err := c.OpenFile("d/d/file", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenFile in a missing directory: %v", err)
	}

	// "a": writes go to the end whatever the offset
	f = openFile(t, c, "d/file", os.O_WRONLY|os.O_CREATE|os.O_APPEND)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if off, err := f.Seek(0, io.SeekEnd); off != 2 || err != nil {
		t.Fatalf("Seek end after Truncate: %d %v", off, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c, "d/file"); got != "he" {
		t.Fatalf("content %q", got)
	}
}

func TestReadAt(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "0123456789")
	f := openFile(t, c, "f", os.O_RDONLY)
	buf := make([]byte, 5)
	for _, x := range []struct {
		off  int64
		n    int
		data string
		err  error
	}{
		{0, 5, "01234", nil},
		{7, 3, "789", io.EOF},
		{10, 0, "", io.EOF},
		{100, 0, "", io.EOF},
	} {
		n, err := f.ReadAt(buf, x.off)
		if n != x.n || string(buf[:n]) != x.data || err != x.err {
			t.Errorf("ReadAt(%d): %d %q %v", x.off, n, buf[:n], err)
		}
	}
	if n, err := f.ReadAt(nil, 100); n != 0 || err != nil {
		t.Errorf("ReadAt(nil, 100): %d %v", n, err)
	}
	var pe *fs.PathError
	if _, err := f.ReadAt(buf, -1); !errors.As(err, &pe) || pe.Op != "readat" || pe.Err.Error() != "negative offset" {
		t.Errorf("ReadAt(-1): %#v", err)
	}
	// ReadAt does not change the offset
	if n, err := f.Read(buf); n != 5 || string(buf) != "01234" || err != nil {
		t.Fatalf("Read: %d %q %v", n, buf, err)
	}
	if n, err := f.Read(make([]byte, 10)); n != 5 || err != nil {
		t.Fatalf("Read rest: %d %v", n, err)
	}
	if n, err := f.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("Read at end: %d %v", n, err)
	}
	if off, err := f.Seek(100, io.SeekStart); off != 100 || err != nil {
		t.Fatalf("Seek beyond end: %d %v", off, err)
	}
	if n, err := f.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("Read beyond end: %d %v", n, err)
	}
}

// TestZeroLength checks that os does not check the file for empty reads and
// for an empty WriteAt, but does for an empty Write (design 7.2).
func TestZeroLength(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "hello")
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	ro := openFile(t, c, "f", os.O_RDONLY)
	wo := openFile(t, c, "f", os.O_WRONLY)
	d := openFile(t, c, "d", os.O_RDONLY)
	if _, err := ro.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	for desc, f := range map[string]func() (int, error){
		"Read at end":         func() (int, error) { return ro.Read(nil) },
		"Read write-only":     func() (int, error) { return wo.Read(nil) },
		"Read directory":      func() (int, error) { return d.Read([]byte{}) },
		"ReadAt beyond end":   func() (int, error) { return ro.ReadAt(nil, 100) },
		"ReadAt directory":    func() (int, error) { return d.ReadAt(nil, 0) },
		"WriteAt read-only":   func() (int, error) { return ro.WriteAt(nil, 0) },
		"Write write-only":    func() (int, error) { return wo.Write(nil) },
		"WriteAt write-only":  func() (int, error) { return wo.WriteAt(nil, 100) },
		"WriteString nothing": func() (int, error) { return wo.WriteString("") },
	} {
		if n, err := f(); n != 0 || err != nil {
			t.Errorf("%s: %d %v", desc, n, err)
		}
	}
	for desc, err := range map[string]error{
		"Write read-only": second(ro.Write(nil)),
		"Write directory": second(d.Write(nil)),
	} {
		if !isPathError(err, "write", syscall.EBADF) {
			t.Errorf("%s: %v", desc, err)
		}
	}
	if _, err := ro.ReadAt(nil, -1); !isPathError(err, "readat", err) {
		t.Errorf("ReadAt(nil, -1): %v", err)
	}
	if got := readFile(t, c, "f"); got != "hello" {
		t.Fatalf("content %q", got)
	}
}

func TestAccessErrors(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "hello")
	ro := openFile(t, c, "f", os.O_RDONLY)
	wo := openFile(t, c, "f", os.O_WRONLY)
	buf := make([]byte, 1)
	for _, x := range []struct {
		desc, op string
		err      error
		want     syscall.Errno
	}{
		{"Read write-only", "read", second(wo.Read(buf)), syscall.EBADF},
		{"ReadAt write-only", "read", second(wo.ReadAt(buf, 0)), syscall.EBADF},
		{"Write read-only", "write", second(ro.Write(buf)), syscall.EBADF},
		{"WriteAt read-only", "write", second(ro.WriteAt(buf, 0)), syscall.EBADF},
		{"Truncate read-only", "truncate", ro.Truncate(1), syscall.EINVAL},
		{"Truncate negative", "truncate", wo.Truncate(-1), syscall.EINVAL},
		{"Seek negative", "seek", second(ro.Seek(-1, io.SeekStart)), syscall.EINVAL},
		{"Seek whence 3", "seek", second(ro.Seek(0, 3)), syscall.EINVAL},
		{"Seek whence 4", "seek", second(ro.Seek(0, 4)), syscall.EINVAL},
		{"ReadDir file", "readdir", second(ro.ReadDir(-1)), syscall.ENOTDIR},
		{"Readdirnames file", "readdir", second(ro.Readdirnames(1)), syscall.ENOTDIR},
	} {
		if !isPathError(x.err, x.op, x.want) {
			t.Errorf("%s: %#v, want %s %v", x.desc, x.err, x.op, x.want)
		}
	}
	var pe *fs.PathError
	if _, err := wo.WriteAt(buf, -1); !errors.As(err, &pe) || pe.Op != "writeat" || pe.Err.Error() != "negative offset" {
		t.Errorf("WriteAt(-1): %#v", err)
	}
	// as fsync(2) and fchmod(2), which do not need write access
	if err := ro.Sync(); err != nil {
		t.Errorf("Sync read-only: %v", err)
	}
	if err := ro.Chmod(0600); err != nil {
		t.Errorf("Chmod read-only: %v", err)
	}
	if entries, err := ro.ReadDir(-1); entries == nil || len(entries) != 0 || err == nil {
		t.Errorf("ReadDir file: %v %v", entries, err)
	}
	if got := readFile(t, c, "f"); got != "hello" {
		t.Fatalf("content %q", got)
	}
}

func TestAppend(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "hello")
	f := openFile(t, c, "f", os.O_RDWR|os.O_APPEND)
	off := func() int64 {
		off, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		return off
	}
	if o := off(); o != 0 {
		t.Fatalf("offset at open: %d", o)
	}
	if _, err := f.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if o := off(); o != 7 {
		t.Fatalf("offset after write: %d", o)
	}
	if _, err := f.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	if o := off(); o != 9 {
		t.Fatalf("offset after seek and write: %d", o)
	}
	n, err := f.WriteAt([]byte("x"), 0)
	var pe *fs.PathError
	if n != 0 || err == nil || errors.As(err, &pe) || err.Error() != "os: invalid use of WriteAt on file opened with O_APPEND" {
		t.Fatalf("WriteAt with O_APPEND: %d %#v", n, err)
	}
	// reads follow the offset
	if _, err := f.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(f); string(data) != "abcd" || err != nil {
		t.Fatalf("ReadAll: %q %v", data, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c, "f"); got != "helloabcd" {
		t.Fatalf("content %q", got)
	}
}

func TestFileStat(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	f := openFile(t, c, "d/f", os.O_RDWR|os.O_CREATE)
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	st, err := f.Stat()
	if err != nil || st.Name() != "f" || st.Size() != 5 || !st.Mode().IsRegular() {
		t.Fatalf("Stat: %v %v", st, err)
	}
	lst, _ := c.Lstat("d/f")
	if st.Sys().(*Stat).Inode != lst.Sys().(*Stat).Inode {
		t.Fatal("inode differs from Lstat")
	}
	// fstat sees the writes of others
	g := openFile(t, c, "d/f", os.O_WRONLY)
	if _, err := g.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if st, err := f.Stat(); err != nil || st.Size() != 10 {
		t.Fatalf("Stat after the write of another handle: %v %v", st, err)
	}
	if err := f.Chmod(0600); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.Stat(); st.Mode() != 0600 {
		t.Fatalf("mode after Chmod: %v", st.Mode())
	}
	if st, _ := c.Stat("d/f"); st.Mode() != 0600 {
		t.Fatalf("mode by path after Chmod: %v", st.Mode())
	}
	d := openFile(t, c, "d", os.O_RDONLY)
	if st, err := d.Stat(); err != nil || st.Name() != "d" || !st.IsDir() {
		t.Fatalf("Stat directory: %v %v", st, err)
	}
	r := openFile(t, c, "/", os.O_RDONLY)
	if st, err := r.Stat(); err != nil || st.Name() != "/" || !st.IsDir() || r.Name() != "/" {
		t.Fatalf("Stat root: %v %v", st, err)
	}
}

func TestFileChown(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "")
	f := openFile(t, c, "f", os.O_RDONLY)
	st, _ := f.Stat()
	uid, gid := int(st.Sys().(*Stat).Uid), int(st.Sys().(*Stat).Gid)
	if err := f.Chown(-1, -1); err != nil {
		t.Fatal(err)
	}
	if err := f.Chown(uid, gid); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := f.Chown(uid+1, -1); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("Chown to another user: %v", err)
		}
		return
	}
	for _, x := range []struct{ uid, gid int }{{1000, -1}, {-1, 1001}} {
		if err := f.Chown(x.uid, x.gid); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := c.Stat("f"); st.Sys().(*Stat).Uid != 1000 || st.Sys().(*Stat).Gid != 1001 {
		t.Fatalf("owner %d:%d", st.Sys().(*Stat).Uid, st.Sys().(*Stat).Gid)
	}
	if known := "P42"; known != "" {
		t.Logf("skip Chown back to the owner at open: known issue %s", known)
		return
	}
	if err := f.Chown(uid, gid); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.Stat("f"); int(st.Sys().(*Stat).Uid) != uid || int(st.Sys().(*Stat).Gid) != gid {
		t.Fatalf("owner %d:%d, want %d:%d", st.Sys().(*Stat).Uid, st.Sys().(*Stat).Gid, uid, gid)
	}
}

func TestReadFileWriteFile(t *testing.T) {
	c := newTestClient(t)
	if err := c.WriteFile("f", []byte("hello"), 0640); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.Stat("f"); st.Mode() != 0640&^fs.FileMode(c.umask) {
		t.Fatalf("mode %v", st.Mode())
	}
	if err := c.WriteFile("f", []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, c, "f"); got != "hi" {
		t.Fatalf("content %q", got)
	}
	writeFile(t, c, "empty", "")
	if data, err := c.ReadFile("empty"); err != nil || data == nil || len(data) != 0 {
		t.Fatalf("ReadFile empty: %q %v", data, err)
	}
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile("d"); !isPathError(err, "read", syscall.EISDIR) {
		t.Fatalf("ReadFile directory: %#v", err)
	}
	if _, err := c.ReadFile("missing"); !isPathError(err, "open", fs.ErrNotExist) {
		t.Fatalf("ReadFile missing: %#v", err)
	}
	if err := c.WriteFile("d", nil, 0666); !isPathError(err, "open", syscall.EISDIR) {
		t.Fatalf("WriteFile directory: %#v", err)
	}
	if conf := readFile(t, c, "/.config"); !strings.HasPrefix(conf, "{") || !strings.Contains(conf, `"Meta"`) {
		t.Fatalf("/.config: %q", conf)
	}
	if stats := readFile(t, c, ".stats"); !strings.Contains(stats, "juicefs_") {
		t.Fatalf("/.stats: %q", stats)
	}
}

// TestLargeFile crosses chunks (64 MiB) and blocks with unaligned IO.
func TestLargeFile(t *testing.T) {
	c := newTestClient(t)
	data := make([]byte, 65<<20+12345)
	for i := range data {
		data[i] = byte(i*7 + i>>13)
	}
	f := openFile(t, c, "big", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	for b := data; len(b) > 0; {
		n := min(len(b), 3<<20+17)
		if _, err := f.Write(b[:n]); err != nil {
			t.Fatal(err)
		}
		b = b[n:]
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadFile("big")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("ReadFile: %d bytes, %v", len(got), err)
	}
	f = openFile(t, c, "big", os.O_RDONLY)
	for _, off := range []int64{0, 4<<20 - 1, 64<<20 - 100, int64(len(data)) - 10} {
		buf := make([]byte, 1000)
		n, err := f.ReadAt(buf, off)
		want := data[off:min(off+1000, int64(len(data)))]
		if !bytes.Equal(buf[:n], want) || (n < len(buf)) != (err == io.EOF) {
			t.Fatalf("ReadAt(%d): %d %v", off, n, err)
		}
	}
}

func TestDirHandle(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c", "a", "b"} {
		writeFile(t, c, "d/"+name, "")
	}
	d := openFile(t, c, "d", os.O_RDONLY)
	buf := make([]byte, 1)
	for _, x := range []struct {
		desc, op string
		err      error
		want     syscall.Errno
	}{
		{"Read", "read", second(d.Read(buf)), syscall.EISDIR},
		{"ReadAt", "read", second(d.ReadAt(buf, 0)), syscall.EISDIR},
		{"Write", "write", second(d.Write(buf)), syscall.EBADF},
		{"Truncate", "truncate", d.Truncate(0), syscall.EINVAL},
		{"Seek negative", "seek", second(d.Seek(-1, io.SeekStart)), syscall.EINVAL},
	} {
		if !isPathError(x.err, x.op, x.want) {
			t.Errorf("%s: %#v", x.desc, x.err)
		}
	}
	if err := d.Sync(); err != nil {
		t.Errorf("Sync: %v", err)
	}

	names := func(n int) ([]string, error) {
		t.Helper()
		names, err := d.Readdirnames(n)
		if names == nil {
			t.Errorf("Readdirnames(%d) returns nil", n)
		}
		return names, err
	}
	if got, err := names(1); !slices.Equal(got, []string{"a"}) || err != nil {
		t.Fatalf("Readdirnames(1): %v %v", got, err)
	}
	if entries, err := d.ReadDir(1); len(entries) != 1 || entries[0].Name() != "b" || err != nil {
		t.Fatalf("ReadDir(1): %v %v", entries, err)
	}
	if infos, err := d.Readdir(-1); len(infos) != 1 || infos[0].Name() != "c" || err != nil {
		t.Fatalf("Readdir(-1): %v %v", infos, err)
	}
	if got, err := names(-1); len(got) != 0 || err != nil {
		t.Fatalf("Readdirnames(-1) at end: %v %v", got, err)
	}
	if got, err := names(1); len(got) != 0 || err != io.EOF {
		t.Fatalf("Readdirnames(1) at end: %v %v", got, err)
	}
	if entries, err := d.ReadDir(1); entries == nil || len(entries) != 0 || err != io.EOF {
		t.Fatalf("ReadDir(1) at end: %v %v", entries, err)
	}
	if infos, err := d.Readdir(1); infos == nil || len(infos) != 0 || err != io.EOF {
		t.Fatalf("Readdir(1) at end: %v %v", infos, err)
	}

	// Seek reads the directory again
	writeFile(t, c, "d/0", "")
	if off, err := d.Seek(0, io.SeekStart); off != 0 || err != nil {
		t.Fatalf("Seek(0): %d %v", off, err)
	}
	if got, err := names(-1); !slices.Equal(got, []string{"0", "a", "b", "c"}) || err != nil {
		t.Fatalf("Readdirnames after rewind: %v %v", got, err)
	}
	if off, err := d.Seek(-2, io.SeekCurrent); off != 2 || err != nil {
		t.Fatalf("Seek(-2, current): %d %v", off, err)
	}
	if got, err := names(-1); !slices.Equal(got, []string{"b", "c"}) || err != nil {
		t.Fatalf("Readdirnames after Seek(2): %v %v", got, err)
	}
	if off, err := d.Seek(-1, io.SeekEnd); off != 3 || err != nil {
		t.Fatalf("Seek(-1, end): %d %v", off, err)
	}
	if got, err := names(-1); !slices.Equal(got, []string{"c"}) || err != nil {
		t.Fatalf("Readdirnames after Seek(-1, end): %v %v", got, err)
	}
	if off, err := d.Seek(10, io.SeekStart); off != 10 || err != nil {
		t.Fatalf("Seek(10): %d %v", off, err)
	}
	if got, err := names(1); len(got) != 0 || err != io.EOF {
		t.Fatalf("Readdirnames(1) after Seek beyond end: %v %v", got, err)
	}

	entries, err := c.ReadDir("d")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	fentries, err := d.ReadDir(-1)
	if err != nil || len(fentries) != len(entries) {
		t.Fatalf("ReadDir(-1): %v %v", fentries, err)
	}
	for i, e := range fentries {
		info, _ := e.Info()
		want, _ := entries[i].Info()
		if e.Name() != entries[i].Name() || e.Type() != entries[i].Type() || info.Sys().(*Stat).Inode != want.Sys().(*Stat).Inode {
			t.Errorf("entry %d: %v, want %v", i, e, entries[i])
		}
	}
}

// TestDirByHandle checks Q22 and Q23: an open directory is bound to its
// inode, as os does with fstat, fchmod, fchown and getdents.
func TestDirByHandle(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("a", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "a/x", "")
	d := openFile(t, c, "a", os.O_RDONLY)
	if err := c.Rename("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir("a", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "a/y", "")
	if names, err := d.Readdirnames(-1); !slices.Equal(names, []string{"x"}) || err != nil {
		t.Fatalf("Readdirnames after rename: %v %v", names, err)
	}
	if err := d.Chmod(0700); err != nil {
		t.Fatal(err)
	}
	b, _ := c.Stat("b")
	a, _ := c.Stat("a")
	if b.Mode().Perm() != 0700 || a.Mode().Perm() == 0700 {
		t.Fatalf("Chmod changed a %v, b %v", a.Mode(), b.Mode())
	}
	st, err := d.Stat()
	if err != nil || st.Sys().(*Stat).Inode != b.Sys().(*Stat).Inode || d.Name() != "a" || st.Name() != "a" {
		t.Fatalf("Stat after rename: %v %v", st, err)
	}
	if err := c.RemoveAll("b"); err != nil {
		t.Fatal(err)
	}
	if known := "P47"; known != "" {
		t.Logf("skip the removed directory: known issue %s", known)
		return
	}
	if _, err := d.Stat(); err != nil {
		t.Fatalf("Stat after remove: %v", err)
	}
	if err := d.Chmod(0755); err != nil {
		t.Fatalf("Chmod after remove: %v", err)
	}
}

func TestFileClosed(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "hello")
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := c.OpenFile("f", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Open("d")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []*File{f, d} {
		if err := x.Close(); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 1)
	for _, x := range []struct {
		op  string
		err error
	}{
		{"close", f.Close()},
		{"read", second(f.Read(buf))},
		{"read", second(f.Read(nil))},
		{"read", second(f.ReadAt(buf, 0))},
		{"write", second(f.Write(buf))},
		{"write", second(f.Write(nil))},
		{"write", second(f.WriteAt(buf, 0))},
		{"seek", second(f.Seek(0, io.SeekStart))},
		{"stat", second(f.Stat())},
		{"truncate", f.Truncate(0)},
		{"sync", f.Sync()},
		{"chmod", f.Chmod(0600)},
		{"chown", f.Chown(-1, -1)},
		{"readdir", second(d.ReadDir(-1))},
		{"readdir", second(d.Readdir(-1))},
		{"readdir", second(d.Readdirnames(-1))},
	} {
		if !isPathError(x.err, x.op, fs.ErrClosed) {
			t.Errorf("%s after Close: %#v", x.op, x.err)
		}
	}
	// os does not access the file for these
	if n, err := f.ReadAt(nil, 0); n != 0 || err != nil {
		t.Errorf("ReadAt(nil) after Close: %v", err)
	}
	if n, err := f.WriteAt(nil, 0); n != 0 || err != nil {
		t.Errorf("WriteAt(nil) after Close: %v", err)
	}

	var nf *File
	for desc, err := range map[string]error{
		"Read":         second(nf.Read(buf)),
		"ReadAt":       second(nf.ReadAt(buf, 0)),
		"Write":        second(nf.Write(buf)),
		"WriteAt":      second(nf.WriteAt(buf, 0)),
		"WriteString":  second(nf.WriteString("x")),
		"Seek":         second(nf.Seek(0, io.SeekStart)),
		"Stat":         second(nf.Stat()),
		"Truncate":     nf.Truncate(0),
		"Sync":         nf.Sync(),
		"Close":        nf.Close(),
		"Chmod":        nf.Chmod(0),
		"Chown":        nf.Chown(0, 0),
		"ReadDir":      second(nf.ReadDir(0)),
		"Readdir":      second(nf.Readdir(0)),
		"Readdirnames": second(nf.Readdirnames(0)),
	} {
		if err != fs.ErrInvalid {
			t.Errorf("%s of nil File: %#v", desc, err)
		}
	}
}

// TestClientCloseFiles checks that Client.Close closes the open files and
// that the data is kept (design 9).
func TestClientCloseFiles(t *testing.T) {
	metaURL := newTestVolume(t)
	c, err := New("test", metaURL, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.Create("f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	g, err := c.Create("g")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := c.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for desc, err := range map[string]error{
		"Write":      second(f.Write([]byte("x"))),
		"Close file": f.Close(),
		"Close dir":  d.Close(),
		"Open":       second(c.Open("f")),
		"OpenFile":   second(c.OpenFile("x", os.O_WRONLY|os.O_CREATE, 0666)),
		"ReadFile":   second(c.ReadFile("f")),
		"WriteFile":  c.WriteFile("x", nil, 0666),
	} {
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s after Client.Close: %v", desc, err)
		}
	}
	c, err = New("test", metaURL, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := readFile(t, c, "f"); got != "hello" {
		t.Fatalf("content after Client.Close: %q", got)
	}
}

func TestFileConcurrently(t *testing.T) {
	c := newTestClient(t)
	data := bytes.Repeat([]byte("0123456789"), 1<<16)
	if err := c.WriteFile("f", data, 0666); err != nil {
		t.Fatal(err)
	}
	f := openFile(t, c, "f", os.O_RDWR)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			buf := make([]byte, 4096)
			for j := range 50 {
				off := int64((i*50 + j) * 1000 % (len(data) - len(buf)))
				if n, err := f.ReadAt(buf, off); n != len(buf) || err != nil {
					t.Errorf("ReadAt(%d): %d %v", off, n, err)
					return
				}
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			for range 50 {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					t.Error(err)
				}
				if _, err := f.Read(make([]byte, 100)); err != nil {
					t.Error(err)
				}
				if _, err := f.Stat(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	// Fewer than 5 slices in the chunk: more make reads start a compaction,
	// which outlives Client.Close (design 13.2).
	wg.Go(func() {
		for j := range 3 {
			if _, err := f.WriteAt([]byte("0123456789"), int64(j*10)); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()

	// Close the Client while files are used: the calls in progress may fail
	// in any way, but those after Close return fs.ErrClosed.
	files := make([]*File, 8)
	for i := range files {
		var err error
		if files[i], err = c.Open("f"); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		wg.Go(func() {
			buf := make([]byte, 1000)
			for {
				if _, err := f.ReadAt(buf, 0); err != nil {
					return
				}
			}
		})
	}
	wg.Go(func() {
		for {
			f, err := c.Open("f")
			if err != nil {
				return
			}
			_ = f.Close()
		}
	})
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	wg.Wait()
	for _, f := range files {
		if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadAt after Client.Close: %v", err)
		}
	}
	if _, err := c.Open("f"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Open after Client.Close: %v", err)
	}
}

// blockingFile blocks reads and writes until release is closed.
type blockingFile struct {
	fileBackend
	started chan struct{}
	release chan struct{}
}

func (b *blockingFile) pread(p []byte, off int64) (int, error) {
	b.started <- struct{}{}
	<-b.release
	return len(p), nil
}

func (b *blockingFile) pwrite(p []byte, off int64) (int, error) {
	return b.pread(p, off)
}

func (b *blockingFile) close() error { return nil }

// TestFileNotSerialized checks that the calls on a File are not serialized
// by the SDK, and that Close does not wait for them (design 7.2).
func TestFileNotSerialized(t *testing.T) {
	b := &blockingFile{started: make(chan struct{}), release: make(chan struct{})}
	f := &File{c: &Client{files: map[*File]struct{}{}}, f: b, name: "f", readable: true, writable: true}
	f.size.Store(100)
	calls := []func(){
		func() { _, _ = f.ReadAt(make([]byte, 10), 0) },
		func() { _, _ = f.ReadAt(make([]byte, 10), 50) },
		func() { _, _ = f.Read(make([]byte, 10)) },
		func() { _, _ = f.Read(make([]byte, 10)) },
		func() { _, _ = f.WriteAt(make([]byte, 10), 20) },
		func() { _, _ = f.Write(make([]byte, 10)) },
	}
	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Go(call)
	}
	timeout := time.After(10 * time.Second)
	for range calls {
		select {
		case <-b.started:
		case <-timeout:
			t.Fatal("calls are serialized")
		}
	}
	closed := make(chan error)
	go func() { closed <- f.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-timeout:
		t.Fatal("Close waits for the calls in progress")
	}
	close(b.release)
	wg.Wait()
	if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("ReadAt after Close: %v", err)
	}
	// the offset has moved by the two reads and the write
	if off := f.off.Load(); off != 30 {
		t.Fatalf("offset %d, want 30", off)
	}
}
