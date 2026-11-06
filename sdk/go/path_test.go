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
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// errnoOf returns 0 for nil and notErrno for errors without an errno.
func errnoOf(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var e syscall.Errno
	if errors.As(err, &e) {
		return e
	}
	return notErrno
}

const notErrno = ^syscall.Errno(0)

func exists(t *testing.T, c *Client, name string) bool {
	t.Helper()
	_, err := c.Stat(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%s): %v", name, err)
	}
	return err == nil
}

// newTree creates files d/sub/x, d/g and f, empty directories e and e2, and
// lnd -> d, lnf -> f, dang -> nonexist, lnx -> d/sub.
func newTree(t *testing.T, c *Client, dir string) {
	t.Helper()
	for _, d := range []string{dir, dir + "/d", dir + "/d/sub", dir + "/e", dir + "/e2"} {
		if err := c.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"/d/sub/x", "/d/g", "/f"} {
		createFile(t, c, dir+f)
	}
	for link, target := range map[string]string{"lnd": "d", "lnf": "f", "dang": "nonexist", "lnx": "d/sub"} {
		if err := c.Symlink(target, dir+"/"+link); err != nil {
			t.Fatal(err)
		}
	}
}

// pathCase is a call in a tree made by newTree. want is the result of os on
// Linux; a case with a known issue (design 13.11) is skipped.
type pathCase struct {
	op    string
	args  []string
	want  syscall.Errno
	known string
}

// TestPathForms checks that the backend resolves names as Linux does. TKV
// engines differ from SQLite for Rmdir and Rename (P41).
func TestPathForms(t *testing.T) {
	c := newTestClient(t)
	ops := map[string]func(p []string) error{
		"Stat":     func(p []string) error { return second(c.Stat(p[0])) },
		"Readlink": func(p []string) error { return second(c.Readlink(p[0])) },
		"Truncate": func(p []string) error { return c.Truncate(p[0], 1) },
		"Chmod":    func(p []string) error { return c.Chmod(p[0], 0755) },
		"Chown":    func(p []string) error { return c.Chown(p[0], -1, -1) },
		"Lchown":   func(p []string) error { return c.Lchown(p[0], -1, -1) },
		"Chtimes":  func(p []string) error { return c.Chtimes(p[0], time.Now(), time.Now()) },
		"ReadDir":  func(p []string) error { return second(c.ReadDir(p[0])) },
		"Mkdir":    func(p []string) error { return c.Mkdir(p[0], 0755) },
		"Remove":   func(p []string) error { return c.Remove(p[0]) },
		"RemoveAll": func(p []string) error {
			return c.RemoveAll(p[0])
		},
		"Rmdir":   func(p []string) error { return c.Rmdir(p[0]) },
		"Rename":  func(p []string) error { return c.Rename(p[0], p[1]) },
		"Link":    func(p []string) error { return c.Link(p[0], p[1]) },
		"Symlink": func(p []string) error { return c.Symlink(p[0], p[1]) },
		"Lstat": func(p []string) error {
			fi, err := c.Lstat(p[0])
			if err == nil && fi.Mode().Type() != fs.ModeDir {
				return fmt.Errorf("type %v, want a directory", fi.Mode().Type())
			}
			return err
		},
	}
	const (
		ok      = syscall.Errno(0)
		enoent  = syscall.ENOENT
		enotdir = syscall.ENOTDIR
		eexist  = syscall.EEXIST
		einval  = syscall.EINVAL
		eisdir  = syscall.EISDIR
		eperm   = syscall.EPERM
		ebusy   = syscall.EBUSY
		enotemp = syscall.ENOTEMPTY
	)
	one := func(op, name string, want syscall.Errno, known ...string) pathCase {
		return pathCase{op, []string{name}, want, strings.Join(known, "")}
	}
	two := func(op, oldname, newname string, want syscall.Errno, known ...string) pathCase {
		return pathCase{op, []string{oldname, newname}, want, strings.Join(known, "")}
	}
	var cases []pathCase
	for _, x := range []struct {
		name string
		want syscall.Errno
	}{
		{"f/", enotdir}, {"d/", ok}, {"lnd/", ok}, {"lnf/", enotdir}, {"f/.", enotdir}, {"d/.", ok},
		{"f/..", enotdir}, {"d/..", ok}, {"f/x", enotdir}, {"lnf/x", enotdir}, {"f/x/y", enotdir},
		{"lnx/../g", ok}, {"nonexist/x", enoent},
	} {
		cases = append(cases, one("Stat", x.name, x.want))
	}
	cases = append(cases,
		one("Lstat", "lnd/", ok), one("Lstat", "lnd/.", ok), one("Lstat", "lnf/", enotdir),
		one("Readlink", "lnd/", einval), one("Readlink", "lnf/", enotdir), one("Readlink", "lnd/.", einval),
		one("Readlink", "f", einval), one("Readlink", "d", einval), one("Readlink", "lnf", ok),
		one("Readlink", "f/x", enotdir),
		one("Truncate", "f/", enotdir), one("Truncate", "f/.", enotdir), one("Truncate", "d/", eisdir),
		one("Truncate", "lnf/", enotdir), one("Truncate", "d", eisdir), one("Truncate", "f", ok),
		one("Truncate", "lnf", ok), one("Truncate", "f/x", enotdir),
	)
	for _, op := range []string{"Chmod", "Chown", "Lchown", "Chtimes"} {
		cases = append(cases, one(op, "f/", enotdir), one(op, "d/", ok), one(op, "f/.", enotdir),
			one(op, "lnd/", ok), one(op, "f/x", enotdir))
	}
	cases = append(cases,
		one("ReadDir", "f/", enotdir), one("ReadDir", "d/", ok), one("ReadDir", "f", enotdir, "P38"),
		one("ReadDir", "lnd/", ok), one("ReadDir", "lnd", ok), one("ReadDir", "f/x", enotdir),

		one("Mkdir", "new/", ok), one("Mkdir", "f/", eexist), one("Mkdir", "d/", eexist),
		one("Mkdir", "new/.", enoent), one("Mkdir", "d/.", eexist), one("Mkdir", "d/..", eexist),
		one("Mkdir", "f/.", enotdir, "P37"), one("Mkdir", "f/..", enotdir, "P37"), one("Mkdir", "g/..", enoent),
		one("Mkdir", "lnd/", eexist), one("Mkdir", "dang/", eexist), one("Mkdir", "f/x", enotdir),
		one("Mkdir", "f/x/y", enotdir), one("Mkdir", "lnx/../new", ok),

		one("Remove", "f/", enotdir), one("Remove", "e/", ok), one("Remove", "lnd/", enotdir),
		one("Remove", "lnf/", enotdir), one("Remove", "e/.", einval), one("Remove", "d/sub/..", enotemp),
		one("Remove", "nonexist/", enoent), one("Remove", "f/.", enotdir), one("Remove", "f/..", enotdir),
		one("Remove", "e/..", enotemp), one("Remove", "g/..", enoent), one("Remove", "f/x", enotdir),
		one("Remove", "d", enotemp), one("Remove", "f", ok), one("Remove", "lnd", ok),

		// "." is rejected by the Client, as os.RemoveAll does.
		one("RemoveAll", "f/", ok), one("RemoveAll", "e/", ok), one("RemoveAll", "lnd/", ok),
		one("RemoveAll", "d/.", einval), one("RemoveAll", "nonexist/", ok), one("RemoveAll", "f/x", enotdir, "P36"),
		one("RemoveAll", "f/x/y", enotdir), one("RemoveAll", "f/.", einval), one("RemoveAll", "f/..", enotdir, "P36"),
		one("RemoveAll", "g/..", ok),
		one("RemoveAll", "d/sub/..", enotemp, "P35"),

		one("Rmdir", "f/", enotdir), one("Rmdir", "e/", ok), one("Rmdir", "e/.", einval),
		one("Rmdir", "d/sub/..", enotemp), one("Rmdir", "f", enotdir), one("Rmdir", "d", enotemp),
		one("Rmdir", "lnd", enotdir), one("Rmdir", "f/x", enotdir),

		// EEXIST for an existing directory comes from the check of os.Rename.
		two("Rename", "f/", "g", enotdir, "P32"), two("Rename", "f", "g/", enotdir, "P32"),
		two("Rename", "e/", "g/", ok), two("Rename", "e/", "g", ok), two("Rename", "e", "g/", ok),
		two("Rename", "e/.", "g", ebusy, "P37"), two("Rename", "e", "g/.", enoent),
		two("Rename", "d/sub/..", "g", ebusy, "P37"), two("Rename", "lnd/", "g", enotdir, "P32"),
		two("Rename", "f/.", "g", enotdir), two("Rename", "f/..", "g", enotdir, "P37"),
		two("Rename", "e/..", "g", ebusy, "P37"), two("Rename", "g/.", "x", enoent), two("Rename", "e", "f/.", enotdir),
		two("Rename", "e", "e2/.", eexist), two("Rename", "e", "lnd/", eexist), two("Rename", "lnd", "g/", enotdir, "P32"),
		two("Rename", "e/", "f", enotdir), two("Rename", "f", "e2/", eexist), two("Rename", "e", "e2", eexist),
		two("Rename", "f", "e", eexist), two("Rename", "e", "e", eexist), two("Rename", "f", "f", ok),
		two("Rename", "f", "g", ok), two("Rename", "f", "lnf", ok), two("Rename", "f", "f/x", enotdir, "P36"),
		two("Rename", "e", "e/x", einval), two("Rename", "f/x", "g", enotdir), two("Rename", "e", "f/x", enotdir),
		two("Rename", "lnx/../g", "h", ok),

		two("Link", "f/", "g", enotdir), two("Link", "f", "g/", enoent, "P32"), two("Link", "lnf/", "g", enotdir),
		two("Link", "d/", "g", eperm), two("Link", "lnd/", "g", eperm), two("Link", "d", "g", eperm),
		two("Link", "f", "e/", eexist), two("Link", "f", "e", eexist), two("Link", "f", "f/.", enotdir, "P37"),
		two("Link", "f", "e/.", eexist), two("Link", "f", "g/..", enoent), two("Link", "f", "g", ok),
		two("Link", "f", "f/x", enotdir), two("Link", "f/x", "g", enotdir),
	)
	for _, x := range []struct {
		name  string
		want  syscall.Errno
		known string
	}{
		{"g/", enoent, "P32"}, {"e/", eexist, "P32"}, {"f/", eexist, "P32"}, {"g/.", enoent, ""},
		{"f/.", enotdir, "P37"}, {"e/..", eexist, ""}, {"dang/", eexist, "P32"}, {"g", ok, ""}, {"f/x", enotdir, ""},
	} {
		cases = append(cases, pathCase{"Symlink", []string{"x", x.name}, x.want, x.known})
	}

	for i, x := range cases {
		desc := fmt.Sprintf("%s(%s)", x.op, strings.Join(x.args, ","))
		if x.known != "" {
			t.Logf("skip %s: known issue %s of pkg/fs", desc, x.known)
			continue
		}
		dir := fmt.Sprintf("/t%d", i)
		newTree(t, c, dir)
		args := make([]string, len(x.args))
		for j, a := range x.args {
			args[j] = dir + "/" + a
		}
		if x.op == "Symlink" {
			args[0] = x.args[0]
		}
		if got := errnoOf(ops[x.op](args)); got != x.want {
			t.Errorf("%s: got %v (%d), want %v (%d)", desc, got, got, x.want, x.want)
		}
	}
}

func TestPathResolve(t *testing.T) {
	c := newTestClient(t)
	newTree(t, c, "t")
	g, err := c.Stat("/t/d/g")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"t/lnx/../g", "/t/lnx/../g", "./t/d/g", "//t/d/g", "t/d/sub/../g", "../t/d/g", "t/lnd/sub/../g"} {
		fi, err := c.Stat(name)
		if err != nil || fi.Sys().(*Stat).Inode != g.Sys().(*Stat).Inode {
			t.Fatalf("Stat(%q): %v", name, err)
		}
	}
	if err := c.Mkdir("t/lnx/../new", 0755); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Stat("t/d/new"); err != nil || !fi.IsDir() {
		t.Fatalf("Mkdir(t/lnx/../new) did not create t/d/new: %v", err)
	}
	if err := c.Rename("t/lnx/../g", "t/h"); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Stat("t/h"); err != nil || fi.Sys().(*Stat).Inode != g.Sys().(*Stat).Inode {
		t.Fatalf("Rename(t/lnx/../g): %v", err)
	}
	if err := c.Symlink("/t/d", "t/link"); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Stat("./t/link/new"); err != nil || !fi.IsDir() {
		t.Fatalf("Stat via link: %v", err)
	}
}

// TestNotDir checks that a name through a file gives ENOTDIR whatever its mode.
func TestNotDir(t *testing.T) {
	c := newTestClient(t)
	for _, mode := range []fs.FileMode{0644, 0755} {
		f := fmt.Sprintf("f%o", mode)
		createFile(t, c, f)
		if err := c.Chmod(f, mode); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{f + "/x", f + "/x/y"} {
			for _, x := range []struct {
				desc  string
				call  func() error
				known string
			}{
				{"Stat", func() error { return second(c.Stat(name)) }, ""},
				{"Lstat", func() error { return second(c.Lstat(name)) }, ""},
				{"Mkdir", func() error { return c.Mkdir(name, 0755) }, ""},
				{"MkdirAll", func() error { return c.MkdirAll(name, 0755) }, ""},
				{"Remove", func() error { return c.Remove(name) }, ""},
				{"Rmdir", func() error { return c.Rmdir(name) }, ""},
				{"RemoveAll", func() error { return c.RemoveAll(name) }, "P36"},
				{"Rename to", func() error { return c.Rename(f, name) }, "P36"},
				{"Rename from", func() error { return c.Rename(name, "z") }, ""},
				{"Link", func() error { return c.Link(f, name) }, ""},
				{"Symlink", func() error { return c.Symlink("z", name) }, ""},
				{"Readlink", func() error { return second(c.Readlink(name)) }, ""},
				{"Truncate", func() error { return c.Truncate(name, 0) }, ""},
				{"Chmod", func() error { return c.Chmod(name, 0755) }, ""},
				{"Chown", func() error { return c.Chown(name, -1, -1) }, ""},
				{"Chtimes", func() error { return c.Chtimes(name, time.Now(), time.Now()) }, ""},
				{"ReadDir", func() error { return second(c.ReadDir(name)) }, ""},
				{"Mkdir dot", func() error { return c.Mkdir(name+"/.", 0755) }, ""},
				{"Remove dotdot", func() error { return c.Remove(name + "/..") }, ""},
			} {
				if x.known != "" {
					t.Logf("skip %s(%s): known issue %s of pkg/fs", x.desc, name, x.known)
					continue
				}
				if err := x.call(); errnoOf(err) != syscall.ENOTDIR {
					t.Errorf("%s(%s): %v", x.desc, name, err)
				}
			}
		}
	}
	if _, err := c.Stat("nonexist/x"); errnoOf(err) != syscall.ENOENT {
		t.Fatalf("Stat(nonexist/x): %v", err)
	}
	if os.Geteuid() == 0 {
		return
	}
	// A directory without search permission still gives EACCES.
	if err := c.MkdirAll("locked/sub", 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.Chmod("locked", 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat("locked/sub"); errnoOf(err) != syscall.EACCES {
		t.Fatalf("Stat(locked/sub): %v", err)
	}
}

func TestPathRoot(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("e", 0755); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		desc  string
		call  func() error
		want  syscall.Errno
		known string
	}{
		{"Remove(/)", func() error { return c.Remove("/") }, syscall.EBUSY, "P26"},
		{"Remove(.)", func() error { return c.Remove(".") }, syscall.EINVAL, ""},
		{"Rmdir(/)", func() error { return c.Rmdir("/") }, syscall.EBUSY, "P26"},
		{"RemoveAll(/)", func() error { return c.RemoveAll("/") }, syscall.EBUSY, "P26"},
		{"RemoveAll(.)", func() error { return c.RemoveAll(".") }, syscall.EINVAL, ""},
		{"RemoveAll(e/..)", func() error { return c.RemoveAll("e/..") }, syscall.ENOTEMPTY, "P35"},
		{"Mkdir(/)", func() error { return c.Mkdir("/", 0755) }, syscall.EEXIST, ""},
		{"Rename(/,x)", func() error { return c.Rename("/", "x") }, syscall.EBUSY, "P26"},
		{"Rename(e,/)", func() error { return c.Rename("e", "/") }, syscall.EEXIST, ""},
		{"Link(/,x)", func() error { return c.Link("/", "x") }, syscall.EPERM, ""},
		{"Symlink(x,/)", func() error { return c.Symlink("x", "/") }, syscall.EEXIST, "P32"},
	} {
		if x.known != "" {
			t.Logf("skip %s: known issue %s of pkg/fs", x.desc, x.known)
			continue
		}
		if err := x.call(); errnoOf(err) != x.want {
			t.Errorf("%s: got %v, want %v", x.desc, err, x.want)
		}
	}
	for _, name := range []string{"/", ".", "..", "//", "/..", "e/.."} {
		fi, err := c.Stat(name)
		if err != nil || !fi.IsDir() || fi.Sys().(*Stat).Inode != 1 {
			t.Fatalf("Stat(%q): %v %v", name, fi, err)
		}
	}
	if fi, _ := c.Stat("/"); fi.Name() != "/" {
		t.Fatalf("Name of the root: %q", fi.Name())
	}
	if !exists(t, c, "e") {
		t.Fatal("e removed")
	}
	if !exists(t, c, "/.config") {
		t.Fatal("/.config does not exist")
	}
}

func TestEmptyName(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("a", 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveAll(""); err != nil {
		t.Fatalf("RemoveAll(\"\"): %v", err)
	}
	// pkg/fs resolves "" as the root, so these would change the root or,
	// for Rename("a", ""), lose a.
	for _, x := range []struct {
		desc string
		call func() error
	}{
		{"Stat", func() error { return second(c.Stat("")) }},
		{"Lstat", func() error { return second(c.Lstat("")) }},
		{"Mkdir", func() error { return c.Mkdir("", 0755) }},
		{"MkdirAll", func() error { return c.MkdirAll("", 0755) }},
		{"Remove", func() error { return c.Remove("") }},
		{"Rmdir", func() error { return c.Rmdir("") }},
		{"Rename old", func() error { return c.Rename("", "x") }},
		{"Rename new", func() error { return c.Rename("a", "") }},
		{"Link old", func() error { return c.Link("", "x") }},
		{"Link new", func() error { return c.Link("a", "") }},
		{"Symlink target", func() error { return c.Symlink("", "x") }},
		{"Symlink link", func() error { return c.Symlink("a", "") }},
		{"Readlink", func() error { return second(c.Readlink("")) }},
		{"Truncate", func() error { return c.Truncate("", 0) }},
		{"Chmod", func() error { return c.Chmod("", 0700) }},
		{"Chown", func() error { return c.Chown("", -1, -1) }},
		{"Lchown", func() error { return c.Lchown("", -1, -1) }},
		{"Chtimes", func() error { return c.Chtimes("", time.Now(), time.Now()) }},
		{"ReadDir", func() error { return second(c.ReadDir("")) }},
	} {
		// pkg/fs resolves "" as the root (P27); the target of Symlink is not
		// resolved, and Linux gives ENOENT (P39).
		known := "P27"
		switch x.desc {
		case "Rename old":
			known = ""
		case "Symlink target":
			known = "P39"
		}
		if known != "" {
			t.Logf("skip %s(\"\"): known issue %s of pkg/fs", x.desc, known)
			continue
		}
		if err := x.call(); errnoOf(err) != syscall.ENOENT {
			t.Errorf("%s(\"\"): %v", x.desc, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestErrorTypes(t *testing.T) {
	c := newTestClient(t)
	var pe *fs.PathError
	for op, err := range map[string]error{
		"stat":      second(c.Stat("x")),
		"lstat":     second(c.Lstat("x")),
		"mkdir":     c.Mkdir("/", 0755),
		"remove":    c.Remove("x"),
		"rmdir":     c.Rmdir("x"),
		"RemoveAll": c.RemoveAll("."),
		"readlink":  second(c.Readlink("x")),
		"truncate":  c.Truncate("x", 0),
		"chmod":     c.Chmod("x", 0),
		"chown":     c.Chown("x", 0, 0),
		"lchown":    c.Lchown("x", 0, 0),
		"chtimes":   c.Chtimes("x", time.Time{}, time.Time{}),
		"readdir":   second(c.ReadDir("x")),
	} {
		if !errors.As(err, &pe) || pe.Op != op {
			t.Errorf("%s: %#v", op, err)
		}
	}
	var le *os.LinkError
	for op, err := range map[string]error{
		"rename":  c.Rename("x", "y"),
		"link":    c.Link("x", "y"),
		"symlink": c.Symlink("x", "/"),
	} {
		if !errors.As(err, &le) || le.Op != op || le.Old != "x" {
			t.Errorf("%s: %#v", op, err)
		}
	}
	if err := c.Rename("x", "y"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("errors.Is(%v, fs.ErrNotExist) = false", err)
	}
}

func TestClosed(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for desc, err := range map[string]error{
		"Stat":      second(c.Stat("d")),
		"Lstat":     second(c.Lstat("d")),
		"Mkdir":     c.Mkdir("x", 0755),
		"MkdirAll":  c.MkdirAll("x", 0755),
		"Remove":    c.Remove("d"),
		"Rmdir":     c.Rmdir("d"),
		"RemoveAll": c.RemoveAll("d"),
		"Rename":    c.Rename("d", "x"),
		"Link":      c.Link("d", "x"),
		"Symlink":   c.Symlink("d", "x"),
		"Readlink":  second(c.Readlink("d")),
		"Truncate":  c.Truncate("d", 0),
		"Chmod":     c.Chmod("d", 0700),
		"Chown":     c.Chown("d", -1, -1),
		"Lchown":    c.Lchown("d", -1, -1),
		"Chtimes":   c.Chtimes("d", time.Now(), time.Now()),
		"ReadDir":   second(c.ReadDir("d")),
	} {
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s after Close: %v", desc, err)
		}
	}
}

func TestStatAttrs(t *testing.T) {
	c := newTestClient(t)
	before := time.Now().Add(-time.Second)
	createFile(t, c, "f")
	if err := c.Symlink("f", "ln"); err != nil {
		t.Fatal(err)
	}
	fi, err := c.Stat("/ln")
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*Stat)
	// On macOS a new file takes the group of its directory, as BSD does
	// (meta inheritGid); on Linux it takes the gid of the process.
	wantGid := uint32(os.Getegid())
	if runtime.GOOS == "darwin" {
		root, _ := c.Stat("/")
		wantGid = root.Sys().(*Stat).Gid
	}
	if fi.Name() != "ln" || fi.Mode() != fs.FileMode(0666&^c.umask) || fi.Size() != 0 || fi.IsDir() ||
		st.Uid != uint32(os.Geteuid()) || st.Gid != wantGid || st.Nlink != 1 || fi.ModTime().Before(before) {
		t.Fatalf("Stat: %v %+v", fi.Mode(), st)
	}
	lfi, err := c.Lstat("ln")
	if err != nil {
		t.Fatal(err)
	}
	if lfi.Mode().Type() != fs.ModeSymlink || lfi.Size() != 1 || lfi.Sys().(*Stat).Inode == st.Inode {
		t.Fatalf("Lstat: %v %+v", lfi.Mode(), lfi.Sys())
	}
	if err := c.Symlink("nonexist", "dang"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]syscall.Errno{
		"nonexist": syscall.ENOENT, "dang": syscall.ENOENT, "f/": syscall.ENOTDIR, "f/x": syscall.ENOTDIR,
	} {
		if _, err := c.Stat(name); errnoOf(err) != want {
			t.Fatalf("Stat(%s): %v, want %v", name, err, want)
		}
	}
}

// TestStatDirChanged expects the attributes of a directory to change with its
// entries at once, as the FUSE kernel invalidates them.
func TestStatDirChanged(t *testing.T) {
	t.Skip("P55: pkg/fs keeps the cached attributes of a directory after its entries change")
	c := newTestClient(t)
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	old, err := c.Stat("d")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := c.Mkdir("d/sub", 0755); err != nil {
		t.Fatal(err)
	}
	fi, err := c.Stat("d")
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().After(old.ModTime()) || fi.Sys().(*Stat).Nlink != old.Sys().(*Stat).Nlink+1 {
		t.Fatalf("mtime %v -> %v, nlink %d -> %d", old.ModTime(), fi.ModTime(),
			old.Sys().(*Stat).Nlink, fi.Sys().(*Stat).Nlink)
	}
}

func TestMkdir(t *testing.T) {
	c := newTestClient(t)
	for name, perm := range map[string]fs.FileMode{
		"plain":  0777,
		"setgid": 0755 | fs.ModeSetgid,
		"sticky": 0777 | fs.ModeSticky,
		"num":    0o2755, // 02000 is not fs.ModeSetgid and is dropped, as in os
	} {
		if err := c.Mkdir(name, perm); err != nil {
			t.Fatal(err)
		}
		fi, err := c.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		want := fs.ModeDir | perm&(fs.ModePerm|fs.ModeSetgid|fs.ModeSticky)&^fs.FileMode(c.umask)
		if fi.Mode() != want {
			t.Errorf("Mkdir(%s, %v): mode %v, want %v", name, perm, fi.Mode(), want)
		}
	}
	if err := c.Mkdir("plain", 0755); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Mkdir existing: %v", err)
	}
	if err := c.Mkdir("x/y", 0755); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Mkdir without parent: %v", err)
	}

	createFile(t, c, "f")
	if err := c.Symlink("plain", "ln"); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name string
		want syscall.Errno
	}{
		{"a/b/c", 0}, {"a/b/c", 0}, {"a/b", 0}, {"/", 0}, {"ln", 0}, {"ln/n", 0}, {"a/b/../d/", 0},
		{"f", syscall.ENOTDIR}, {"f/x", syscall.ENOTDIR}, {"f/x/y", syscall.ENOTDIR},
	} {
		err := c.MkdirAll(x.name, 0750)
		if errnoOf(err) != x.want {
			t.Errorf("MkdirAll(%s): %v, want %v", x.name, err, x.want)
		}
		if x.want == 0 {
			if fi, err := c.Stat(x.name); err != nil || !fi.IsDir() {
				t.Errorf("MkdirAll(%s): not a directory: %v", x.name, err)
			}
		}
	}
	if fi, _ := c.Stat("a/b"); fi.Mode() != fs.ModeDir|0750&^fs.FileMode(c.umask) {
		t.Fatalf("MkdirAll mode: %v", fi.Mode())
	}
	if !exists(t, c, "a/d") {
		t.Fatal("MkdirAll(a/b/../d/) did not create a/d")
	}
}

func TestRemove(t *testing.T) {
	c := newTestClient(t)
	newTree(t, c, "t")
	if err := c.Remove("t/d"); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("Remove non-empty: %v", err)
	}
	if err := c.Rmdir("t/f"); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("Rmdir file: %v", err)
	}
	for _, name := range []string{"t/f", "t/e", "t/lnd"} {
		if err := c.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Rmdir("t/e2"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("t/f"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove missing: %v", err)
	}
	if !exists(t, c, "t/d") {
		t.Fatal("Remove(t/lnd) removed the target")
	}
	if err := c.RemoveAll("t"); err != nil {
		t.Fatal(err)
	}
	if exists(t, c, "t") {
		t.Fatal("RemoveAll(t) left t")
	}
	if err := c.RemoveAll("t"); err != nil {
		t.Fatalf("RemoveAll missing: %v", err)
	}
	for i, name := range []string{"f/", "f///", "d/", "lnf/", "lnd/", "lnd///"} {
		dir := fmt.Sprintf("trailing%d", i)
		newTree(t, c, dir)
		if err := c.RemoveAll(dir + "/" + name); err != nil {
			t.Fatalf("RemoveAll(%s): %v", name, err)
		}
		_, err := c.Lstat(dir + "/" + strings.TrimRight(name, "/"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("RemoveAll(%s) left the entry: %v", name, err)
		}
		if strings.HasPrefix(name, "ln") && (!exists(t, c, dir+"/f") || !exists(t, c, dir+"/d/sub/x")) {
			t.Fatalf("RemoveAll(%s) removed a symlink target", name)
		}
	}
}

func TestRename(t *testing.T) {
	c := newTestClient(t)
	newTree(t, c, "t")
	createFile(t, c, "t/g")
	g, _ := c.Stat("t/g")
	if err := c.Rename("t/g", "t/f"); err != nil {
		t.Fatal(err)
	}
	if f, _ := c.Stat("t/f"); f.Sys().(*Stat).Inode != g.Sys().(*Stat).Inode {
		t.Fatal("Rename did not replace t/f")
	}
	if err := c.Rename("t/d", "t/d/sub/x2"); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("Rename into itself: %v", err)
	}
	if err := c.Rename("t/d", "/u"); err != nil {
		t.Fatal(err)
	}
	if !exists(t, c, "/u/sub/x") {
		t.Fatal("Rename did not move the directory")
	}
	if err := c.Rename("t/lnd", "t/lnd2"); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Lstat("t/lnd2"); err != nil || fi.Mode().Type() != fs.ModeSymlink {
		t.Fatalf("Rename symlink: %v", err)
	}
}

func TestLink(t *testing.T) {
	c := newTestClient(t)
	newTree(t, c, "t")
	if err := c.Link("t/f", "t/f2"); err != nil {
		t.Fatal(err)
	}
	f, _ := c.Stat("t/f")
	f2, _ := c.Stat("t/f2")
	if f.Sys().(*Stat).Inode != f2.Sys().(*Stat).Inode || f2.Sys().(*Stat).Nlink != 2 {
		t.Fatalf("Link: %+v %+v", f.Sys(), f2.Sys())
	}
	if err := c.Link("t/lnf", "t/lnf2"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := c.Lstat("t/lnf2"); fi.Mode().Type() != fs.ModeSymlink {
		t.Fatalf("Link(symlink): %v", fi.Mode())
	}
}

func TestSymlink(t *testing.T) {
	c := newTestClient(t)
	for _, d := range []string{"d", "c"} {
		if err := c.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for i, x := range []struct{ oldname, newname, want string }{
		{"/d/file", "/d/link", "file"},
		{"/a/b/file", "/c/link", "../a/b/file"},
		{"/nonexist", "/d/link", "../nonexist"},
		{"file", "/d/link", "file"},
		{"../x/file", "/d/link", "../x/file"},
		{"/d/file", "d/link", "file"},      // P28: same as "/d/link"
		{"file", "d/link", "file"},         // P28: kept, not "../file"
		{"sub/file", "d/link", "sub/file"}, // P28
		{"/", "/d/link", ".."},
		{"x\x00", "/d/link", ""},
	} {
		newname := fmt.Sprintf("%s%d", x.newname, i)
		err := c.Symlink(x.oldname, newname)
		if x.want == "" {
			if !errors.Is(err, syscall.EINVAL) {
				t.Errorf("Symlink(%q, %q): %v", x.oldname, newname, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got, err := c.Readlink(newname); got != x.want || err != nil {
			t.Errorf("Symlink(%q, %q): stored %q (%v), want %q", x.oldname, newname, got, err, x.want)
		}
	}
	if err := c.Symlink("d", "d/link0"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Symlink existing: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	if err := c.Symlink("f", "ln"); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{100, 5 << 30, 1, 0} {
		if err := c.Truncate("ln", size); err != nil {
			t.Fatal(err)
		}
		if fi, _ := c.Stat("f"); fi.Size() != size {
			t.Fatalf("size %d, want %d", fi.Size(), size)
		}
	}
	if err := c.Truncate("f", -1); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("negative size: %v", err) // P34
	}
}

func TestChmod(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	if err := c.Symlink("f", "ln"); err != nil {
		t.Fatal(err)
	}
	// As the kernel does, setgid is cleared unless the file belongs to a
	// group of the caller, and a new file may belong to the root's group.
	if err := c.Chown("f", -1, os.Getegid()); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []fs.FileMode{0600, 0755 | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky, 0o2644, fs.ModeDir | 0640} {
		if err := c.Chmod("ln", mode); err != nil {
			t.Fatal(err)
		}
		want := mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
		if fi, _ := c.Stat("f"); fi.Mode() != want {
			t.Errorf("Chmod(%v): mode %v, want %v", mode, fi.Mode(), want)
		}
	}
	if fi, _ := c.Lstat("ln"); fi.Mode().Perm() != 0777 {
		t.Fatalf("Chmod changed the link: %v", fi.Mode())
	}
}

func TestChown(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	if err := c.Symlink("f", "ln"); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if err := c.Chown("ln", -1, -1); err != nil {
		t.Fatal(err)
	}
	if err := c.Chown("ln", uid, gid); err != nil {
		t.Fatal(err)
	}
	groups, _ := os.Getgroups()
	other := -1
	for _, g := range groups {
		if g != gid {
			other = g
			break
		}
	}
	if other == -1 {
		t.Skip("the process has only one group")
	}
	check := func(name string, wantGid int) {
		t.Helper()
		fi, err := c.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		if st := fi.Sys().(*Stat); st.Uid != uint32(uid) || st.Gid != uint32(wantGid) {
			t.Fatalf("%s: uid %d gid %d, want %d %d", name, st.Uid, st.Gid, uid, wantGid)
		}
	}
	if err := c.Lchown("ln", -1, other); err != nil {
		t.Fatal(err)
	}
	check("ln", other)
	check("f", gid)
	if err := c.Chown("ln", -1, other); err != nil {
		t.Fatal(err)
	}
	check("f", other)
}

func TestChtimes(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	if err := c.Symlink("f", "ln"); err != nil {
		t.Fatal(err)
	}
	atime := time.Unix(1700000000, 123456789)
	mtime := time.Unix(1600000000, 987654321)
	if err := c.Chtimes("ln", atime, mtime); err != nil {
		t.Fatal(err)
	}
	times := func() (time.Time, time.Time) {
		fi, _ := c.Stat("f")
		st := fi.Sys().(*Stat)
		return st.Atime, st.Mtime
	}
	if a, m := times(); !a.Equal(atime) || !m.Equal(mtime) {
		t.Fatalf("times %v %v, want %v %v", a, m, atime, mtime)
	}
	if err := c.Chtimes("f", time.Time{}, atime); err != nil {
		t.Fatal(err)
	}
	if a, m := times(); !a.Equal(atime) || !m.Equal(atime) {
		t.Fatalf("zero atime: %v %v", a, m)
	}
	if err := c.Chtimes("f", time.Unix(0, 0), time.Unix(0, 0)); err != nil {
		t.Fatalf("Chtimes(1970): %v", err)
	}
	if a, m := times(); !a.Equal(time.Unix(0, 0)) || !m.Equal(time.Unix(0, 0)) {
		t.Fatalf("times %v %v, want 1970", a, m)
	}
}

func TestChtimesBefore1970(t *testing.T) {
	t.Skip("known issue P40 of pkg/meta: SetAttr replaces a negative time with the current time")
	c := newTestClient(t)
	createFile(t, c, "f")
	old := time.Unix(-100, 5)
	if err := c.Chtimes("f", old, old); err != nil {
		t.Fatal(err)
	}
	if fi, _ := c.Stat("f"); !fi.Sys().(*Stat).Atime.Equal(old) || !fi.ModTime().Equal(old) {
		t.Fatalf("times %+v, want %v", fi.Sys(), old)
	}
}

func TestReadDir(t *testing.T) {
	c := newTestClient(t)
	newTree(t, c, "t")
	entries, err := c.ReadDir("t")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"d", "dang", "e", "e2", "f", "lnd", "lnf", "lnx"}
	if !slices.Equal(names, want) {
		t.Fatalf("names %v, want %v", names, want)
	}
	types := map[string]fs.FileMode{"d": fs.ModeDir, "f": 0, "lnd": fs.ModeSymlink, "dang": fs.ModeSymlink}
	for _, e := range entries {
		typ, ok := types[e.Name()]
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil || e.Type() != typ || info.Mode().Type() != typ || e.IsDir() != (typ == fs.ModeDir) {
			t.Errorf("%s: type %v, info %v, err %v", e.Name(), e.Type(), info.Mode(), err)
		}
		if st, _ := c.Lstat("t/" + e.Name()); st.Sys().(*Stat).Inode != info.Sys().(*Stat).Inode {
			t.Errorf("%s: inode differs from Lstat", e.Name())
		}
	}
	if entries, err := c.ReadDir("t/lnd"); err != nil || len(entries) != 2 || entries[0].Name() != "g" || entries[1].Name() != "sub" {
		t.Fatalf("ReadDir via symlink: %v %v", entries, err)
	}
	if entries, err := c.ReadDir("t/e"); err != nil || len(entries) != 0 {
		t.Fatalf("ReadDir empty: %v %v", entries, err)
	}
	if _, err := c.ReadDir("t/nonexist"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDir missing: %v", err)
	}
}
