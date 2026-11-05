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
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func isLinkError(err error, op string, want error) bool {
	var le *os.LinkError
	return errors.As(err, &le) && le.Op == op && errors.Is(err, want)
}

func isSyscallError(err error, op string, want error) bool {
	var se *os.SyscallError
	return errors.As(err, &se) && se.Syscall == op && errors.Is(err, want)
}

func TestXattr(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	if err := c.Mkdir("d", 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.Symlink("f", "lnk"); err != nil {
		t.Fatal(err)
	}
	get := func(name, attr string) string {
		t.Helper()
		v, err := c.Getxattr(name, attr)
		if err != nil {
			t.Fatalf("Getxattr(%s, %s): %v", name, attr, err)
		}
		return string(v)
	}
	list := func(name string) []string {
		t.Helper()
		attrs, err := c.Listxattr(name)
		if err != nil {
			t.Fatalf("Listxattr(%s): %v", name, err)
		}
		slices.Sort(attrs)
		return attrs
	}

	if attrs := list("f"); attrs == nil || len(attrs) != 0 {
		t.Fatalf("Listxattr without attributes: %#v", attrs)
	}
	if err := c.Setxattr("f", "user.a", []byte("v1"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Setxattr("f", "user.a", []byte("v2"), 0); err != nil || get("f", "user.a") != "v2" {
		t.Fatalf("replace with flags 0: %v", err)
	}
	if err := c.Setxattr("f", "user.a", nil, XattrCreate); !isPathError(err, "setxattr", syscall.EEXIST) {
		t.Fatalf("XattrCreate on an existing attribute: %v", err)
	}
	if err := c.Setxattr("f", "user.b", nil, XattrReplace); !isPathError(err, "setxattr", meta.ENOATTR) {
		t.Fatalf("XattrReplace on a missing attribute: %v", err)
	}
	if err := c.Setxattr("f", "user.b", []byte("b"), XattrCreate); err != nil {
		t.Fatal(err)
	}
	if err := c.Setxattr("f", "user.b", []byte("B"), XattrReplace); err != nil || get("f", "user.b") != "B" {
		t.Fatalf("XattrReplace: %v", err)
	}
	for _, flags := range []XattrFlag{-1, 3, 4} {
		if err := c.Setxattr("f", "user.c", nil, flags); !isPathError(err, "setxattr", syscall.EINVAL) {
			t.Fatalf("flags %d: %v", flags, err)
		}
	}
	if attrs := list("f"); !slices.Equal(attrs, []string{"user.a", "user.b"}) {
		t.Fatalf("Listxattr: %q", attrs)
	}

	if _, err := c.Getxattr("f", "user.c"); !isPathError(err, "getxattr", meta.ENOATTR) {
		t.Fatalf("Getxattr of a missing attribute: %v", err)
	}
	if err := c.Removexattr("f", "user.c"); !isPathError(err, "removexattr", meta.ENOATTR) {
		t.Fatalf("Removexattr of a missing attribute: %v", err)
	}
	if err := c.Removexattr("f", "user.b"); err != nil {
		t.Fatal(err)
	}
	if attrs := list("f"); !slices.Equal(attrs, []string{"user.a"}) {
		t.Fatalf("Listxattr after Removexattr: %q", attrs)
	}

	if err := c.Setxattr("f", "user.empty", []byte{}, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Getxattr("f", "user.empty"); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("empty value: %#v, %v", v, err)
	}
	t.Run("nil", func(t *testing.T) {
		t.Skip("P52: the SQL engine fails to create an attribute with a nil value")
		if err := c.Setxattr("f", "user.nil", nil, 0); err != nil {
			t.Fatal(err)
		}
	})
	// The largest value on Linux, which jfs_getXattr returned as zeros (P16).
	big := bytes.Repeat([]byte("ab\x00"), 64<<10/3+1)[:64<<10]
	if err := c.Setxattr("f", "user.big", big, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Getxattr("f", "user.big"); err != nil || !bytes.Equal(v, big) {
		t.Fatalf("64 KiB value: %d bytes, %v", len(v), err)
	}

	if err := c.Setxattr("lnk", "user.l", []byte("l"), 0); err != nil || get("f", "user.l") != "l" {
		t.Fatalf("Setxattr through a symlink: %v", err)
	}
	if get("lnk", "user.a") != "v2" || !slices.Contains(list("lnk"), "user.l") {
		t.Fatal("Getxattr and Listxattr do not follow symlinks")
	}
	if err := c.Removexattr("lnk", "user.l"); err != nil || slices.Contains(list("f"), "user.l") {
		t.Fatalf("Removexattr through a symlink: %v", err)
	}
	if err := c.Setxattr("d", "user.d", []byte("d"), 0); err != nil || get("d", "user.d") != "d" {
		t.Fatalf("directory: %v", err)
	}

	for _, name := range []string{"missing", "d/missing", "f/"} {
		want := syscall.ENOENT
		if name == "f/" {
			want = syscall.ENOTDIR
		}
		if _, err := c.Getxattr(name, "user.a"); !isPathError(err, "getxattr", want) {
			t.Errorf("Getxattr(%s): %v", name, err)
		}
		if _, err := c.Listxattr(name); !isPathError(err, "listxattr", want) {
			t.Errorf("Listxattr(%s): %v", name, err)
		}
		if err := c.Setxattr(name, "user.a", nil, 0); !isPathError(err, "setxattr", want) {
			t.Errorf("Setxattr(%s): %v", name, err)
		}
		if err := c.Removexattr(name, "user.a"); !isPathError(err, "removexattr", want) {
			t.Errorf("Removexattr(%s): %v", name, err)
		}
	}
}

// TestXattrLimits expects the checks of Linux (design P50).
func TestXattrLimits(t *testing.T) {
	t.Skip("P50: pkg/fs does not check the xattr limits and permissions")
	c := newTestClient(t)
	createFile(t, c, "f")
	long := "user." + strings.Repeat("x", 251)
	for _, attr := range []string{"", long} {
		if err := c.Setxattr("f", attr, nil, 0); !isPathError(err, "setxattr", syscall.ERANGE) {
			t.Errorf("Setxattr(%q): %v", attr, err)
		}
		if _, err := c.Getxattr("f", attr); !isPathError(err, "getxattr", syscall.ERANGE) {
			t.Errorf("Getxattr(%q): %v", attr, err)
		}
		if err := c.Removexattr("f", attr); !isPathError(err, "removexattr", syscall.ERANGE) {
			t.Errorf("Removexattr(%q): %v", attr, err)
		}
	}
	if err := c.Setxattr("f", long[:255], []byte("v"), 0); err != nil {
		t.Errorf("255-byte name: %v", err)
	}
	if err := c.Setxattr("f", "user.big", make([]byte, 64<<10+1), 0); !isPathError(err, "setxattr", syscall.E2BIG) {
		t.Errorf("value above 64 KiB: %v", err)
	}

	if os.Geteuid() == 0 {
		return
	}
	if err := c.Setxattr("f", "user.a", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Chmod("f", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Getxattr("f", "user.a"); !isPathError(err, "getxattr", syscall.EACCES) {
		t.Errorf("Getxattr without read permission: %v", err)
	}
	if err := c.Setxattr("f", "user.b", []byte("v"), 0); !isPathError(err, "setxattr", syscall.EACCES) {
		t.Errorf("Setxattr without write permission: %v", err)
	}
	if err := c.Removexattr("f", "user.a"); !isPathError(err, "removexattr", syscall.EACCES) {
		t.Errorf("Removexattr without write permission: %v", err)
	}
	if _, err := c.Listxattr("f"); err != nil {
		t.Errorf("Listxattr needs no permission: %v", err)
	}
}

func TestClone(t *testing.T) {
	c := newTestClient(t)
	if err := c.MkdirAll("src/sub", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "src/a", "hello")
	writeFile(t, c, "src/sub/b", "world")
	past := time.Unix(1600000000, 0)
	if err := c.Chtimes("src/a", past, past); err != nil {
		t.Fatal(err)
	}

	if err := c.Clone("src/a", "a2", false); err != nil {
		t.Fatal(err)
	}
	if readFile(t, c, "a2") != "hello" {
		t.Fatal("clone of a file differs")
	}
	if fi, err := c.Stat("a2"); err != nil || fi.ModTime().Equal(past) {
		t.Fatalf("clone without preserve keeps mtime: %v", err)
	}
	writeFile(t, c, "a2", "changed")
	if readFile(t, c, "src/a") != "hello" {
		t.Fatal("writing the clone changes the source")
	}
	if err := c.Clone("src/a", "a3", true); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Stat("a3"); err != nil || !fi.ModTime().Equal(past) {
		t.Fatalf("clone with preserve: %v", err)
	}

	if err := c.Clone("src", "dst", false); err != nil {
		t.Fatal(err)
	}
	if readFile(t, c, "dst/a") != "hello" || readFile(t, c, "dst/sub/b") != "world" {
		t.Fatal("clone of a directory differs")
	}
	if err := c.Clone("src", "dst", false); !isLinkError(err, "clone", syscall.EEXIST) {
		t.Fatalf("existing dst: %v", err)
	}
	if err := c.Clone("missing", "x", false); !isLinkError(err, "clone", syscall.ENOENT) {
		t.Fatalf("missing src: %v", err)
	}
	if err := c.Clone("src/a", "missing/x", false); !isLinkError(err, "clone", syscall.ENOENT) {
		t.Fatalf("missing parent of dst: %v", err)
	}
}

func TestInfo(t *testing.T) {
	c := newTestClient(t)
	if err := c.MkdirAll("d/sub", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "d/a", "abc")
	writeFile(t, c, "d/sub/b", "12345")
	if err := c.Symlink("d", "lnk"); err != nil {
		t.Fatal(err)
	}
	const unit = 4 << 10
	for _, tc := range []struct {
		name string
		opts *InfoOptions
		want InfoResult
	}{
		{"d", &InfoOptions{Strict: true}, InfoResult{Length: 3, Size: 3 * unit, Files: 1, Dirs: 2}},
		{"d", &InfoOptions{Recursive: true, Strict: true}, InfoResult{Length: 8, Size: 4 * unit, Files: 2, Dirs: 2}},
		{"lnk", &InfoOptions{Recursive: true, Strict: true}, InfoResult{Length: 8, Size: 4 * unit, Files: 2, Dirs: 2}},
		{"d/a", nil, InfoResult{Length: 3, Size: unit, Files: 1}},
	} {
		r, err := c.Info(tc.name, tc.opts)
		if err != nil || *r != tc.want {
			t.Errorf("Info(%s, %+v): %+v, %v, want %+v", tc.name, tc.opts, r, err, tc.want)
		}
	}
	if _, err := c.Info("d", nil); err != nil {
		t.Fatalf("Info with directory statistics: %v", err)
	}
	if _, err := c.Info("missing", nil); !isPathError(err, "info", syscall.ENOENT) {
		t.Fatalf("missing: %v", err)
	}
}

func TestSummary(t *testing.T) {
	c := newTestClient(t)
	if err := c.MkdirAll("d/sub", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "d/a", "abc")
	writeFile(t, c, "d/b", strings.Repeat("b", 10000))
	writeFile(t, c, "d/sub/x", "x")
	if err := c.Symlink("a", "d/lnk"); err != nil {
		t.Fatal(err)
	}
	const unit = 4 << 10
	summary := func(name string, depth, entries int) *TreeSummary {
		t.Helper()
		s, err := c.Summary(name, depth, entries)
		if err != nil {
			t.Fatalf("Summary(%s, %d, %d): %v", name, depth, entries, err)
		}
		return s
	}

	s := summary("d", 0, 1)
	if s.Path != "d" || s.Type != fs.ModeDir || s.Size != 8*unit || s.Files != 4 || s.Dirs != 2 || s.Children != nil {
		t.Fatalf("depth 0: %+v", s)
	}

	s = summary("/d/", 1, 10)
	if s.Path != "d" || s.Files != 4 || s.Dirs != 2 || len(s.Children) != 4 {
		t.Fatalf("depth 1: %+v", s)
	}
	types := map[string]fs.FileMode{}
	for _, ch := range s.Children {
		types[ch.Path] = ch.Type
	}
	if want := map[string]fs.FileMode{"a": 0, "b": 0, "sub": fs.ModeDir, "lnk": fs.ModeSymlink}; !maps.Equal(types, want) {
		t.Fatalf("children: %v", types)
	}
	if b, sub := s.Children[0], s.Children[1]; b.Path != "b" || b.Size != 3*unit || b.Files != 1 ||
		sub.Path != "sub" || sub.Size != 2*unit || sub.Files != 1 || sub.Dirs != 1 || sub.Children != nil {
		t.Fatalf("children are not sorted by size: %+v, %+v", b, sub)
	}

	s = summary("d", 1, 1)
	if len(s.Children) != 2 || s.Children[0].Path != "b" {
		t.Fatalf("entries 1: %+v", s.Children)
	}
	if rest := s.Children[1]; rest.Path != "..." || rest.Size != 4*unit || rest.Files != 3 || rest.Dirs != 1 {
		t.Fatalf("merged entries: %+v", rest)
	}
	s = summary("d", 2, 10)
	if sub := s.Children[1]; len(sub.Children) != 1 || sub.Children[0].Path != "sub/x" {
		t.Fatalf("depth 2: %+v", sub)
	}
	if s := summary("/", 0, 1); s.Path != "/" || s.Files != 4 || s.Dirs != 3 {
		t.Fatalf("root: %+v", s)
	}

	for _, args := range [][2]int{{-1, 1}, {256, 1}, {0, -1}, {0, 256}} {
		if _, err := c.Summary("d", args[0], args[1]); !isPathError(err, "summary", syscall.EINVAL) {
			t.Errorf("Summary(d, %d, %d): %v", args[0], args[1], err)
		}
	}
	if _, err := c.Summary("missing", 0, 1); !isPathError(err, "summary", syscall.ENOENT) {
		t.Fatalf("missing: %v", err)
	}
	t.Run("file", func(t *testing.T) {
		t.Skip("P51: pkg/fs reports the root of the summary of a file as a directory")
		if s := summary("d/a", 0, 1); s.Type != 0 || s.Files != 1 || s.Dirs != 0 {
			t.Fatalf("file: %+v", s)
		}
	})
}

func TestDirQuota(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("q", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, "q/f", "x")
	getQuota := func(name string) *Quota {
		t.Helper()
		q, err := c.GetQuota(name)
		if err != nil {
			t.Fatalf("GetQuota(%s): %v", name, err)
		}
		return q
	}

	if q := getQuota("q"); q != nil {
		t.Fatalf("no quota: %+v", q)
	}
	if err := c.SetQuota("q", QuotaOptions{Capacity: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	// The usage excludes the directory itself.
	want := Quota{MaxSpace: 1 << 30, MaxInodes: -1, UsedSpace: 4 << 10, UsedInodes: 1}
	if q := getQuota("q"); q == nil || *q != want {
		t.Fatalf("GetQuota: %+v, want %+v", q, want)
	}
	if err := c.SetQuota("/q", QuotaOptions{Inodes: 10}); err != nil {
		t.Fatal(err)
	}
	want.MaxInodes = 10
	if q := getQuota("q"); q == nil || *q != want {
		t.Fatalf("GetQuota after setting inodes: %+v, want %+v", q, want)
	}
	qs, err := c.ListQuota()
	if err != nil || len(qs) != 1 || qs["/q"] == nil || *qs["/q"] != want {
		t.Fatalf("ListQuota: %v, %v", qs, err)
	}
	qs, err = c.CheckQuota("q", nil)
	if err != nil || len(qs) != 1 || qs["q"] == nil || *qs["q"] != want {
		t.Fatalf("CheckQuota: %v, %v", qs, err)
	}
	if _, err = c.CheckQuota("q", &CheckQuotaOptions{Repair: true, Strict: true}); err != nil {
		t.Fatalf("CheckQuota with repair: %v", err)
	}
	if err := c.DelQuota("q"); err != nil {
		t.Fatal(err)
	}
	if q := getQuota("q"); q != nil {
		t.Fatalf("GetQuota after DelQuota: %+v", q)
	}

	if err := c.SetQuota("new/dir", QuotaOptions{Inodes: 1, Create: true}); err != nil {
		t.Fatal(err)
	}
	if fi, err := c.Stat("new/dir"); err != nil || !fi.IsDir() {
		t.Fatalf("SetQuota with Create: %v", err)
	}

	if err := c.SetQuota("q", QuotaOptions{}); !isPathError(err, "setquota", syscall.EINVAL) {
		t.Fatalf("no limit: %v", err)
	}
	if err := c.SetQuota("missing", QuotaOptions{Inodes: 1}); !isPathError(err, "setquota", syscall.ENOENT) {
		t.Fatalf("missing directory: %v", err)
	}
	if err := c.SetQuota("", QuotaOptions{Inodes: 1}); !isPathError(err, "setquota", syscall.EINVAL) {
		t.Fatalf("SetQuota(\"\"): %v", err)
	}
	if _, err := c.GetQuota(""); !isPathError(err, "getquota", syscall.EINVAL) {
		t.Fatalf("GetQuota(\"\"): %v", err)
	}
	if err := c.DelQuota(""); !isPathError(err, "delquota", syscall.EINVAL) {
		t.Fatalf("DelQuota(\"\"): %v", err)
	}
}

func TestUserGroupQuota(t *testing.T) {
	c := newTestClient(t)
	createFile(t, c, "f")
	t.Run("check", func(t *testing.T) {
		t.Skip("P29: CheckQuota(\"\") fails on a volume with files")
		if _, err := c.CheckQuota("", nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, name := range []string{"uid:1234", "gid:1234"} {
		if err := c.SetQuota(name, QuotaOptions{Capacity: 1 << 20, Inodes: 5}); err != nil {
			t.Fatalf("SetQuota(%s): %v", name, err)
		}
		q, err := c.GetQuota(name)
		if err != nil || q == nil || q.MaxSpace != 1<<20 || q.MaxInodes != 5 {
			t.Fatalf("GetQuota(%s): %+v, %v", name, q, err)
		}
	}
	qs, err := c.ListQuota()
	if err != nil || qs["uid:1234"] == nil || qs["gid:1234"] == nil {
		t.Fatalf("ListQuota: %v, %v", qs, err)
	}
	if _, err := c.CheckQuota("", &CheckQuotaOptions{Repair: true}); err != nil {
		t.Fatalf("CheckQuota(\"\") with repair: %v", err)
	}
	for _, name := range []string{"uid:1234", "gid:1234"} {
		if err := c.DelQuota(name); err != nil {
			t.Fatalf("DelQuota(%s): %v", name, err)
		}
		// The usage is still kept (P53).
		if q, err := c.GetQuota(name); err != nil || q != nil && (q.MaxSpace > 0 || q.MaxInodes > 0) {
			t.Fatalf("GetQuota(%s) after DelQuota: %+v, %v", name, q, err)
		}
	}
	if err := c.SetQuota("uid:x", QuotaOptions{Inodes: 1}); !isPathError(err, "setquota", syscall.EINVAL) {
		t.Fatalf("invalid uid: %v", err)
	}
}

func TestWarmup(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("w", 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("w", 100<<10)
	writeFile(t, c, "w/a", data)

	r, err := c.Warmup([]string{"w", "missing"}, nil)
	if err != nil || r == nil || r.FileCount != 1 || r.TotalBytes != uint64(len(data)) {
		t.Fatalf("Warmup: %+v, %v", r, err)
	}
	check := func() *WarmupResult {
		t.Helper()
		r, err := c.Warmup([]string{"/w/a"}, &WarmupOptions{Check: true, Evict: true, Threads: 2})
		if err != nil || r == nil || r.FileCount != 1 {
			t.Fatalf("check: %+v, %v", r, err)
		}
		return r
	}
	if r := check(); r.MissBytes != 0 || sum(r.Locations) != uint64(len(data)) {
		t.Fatalf("check after warmup: %+v", r)
	}
	if r, err := c.Warmup([]string{"w"}, &WarmupOptions{Evict: true}); err != nil || r.FileCount != 1 {
		t.Fatalf("evict: %+v, %v", r, err)
	}
	if r := check(); r.MissBytes != uint64(len(data)) || sum(r.Locations) != 0 {
		t.Fatalf("check after evict: %+v", r)
	}

	if r, err := c.Warmup([]string{"missing"}, &WarmupOptions{Background: true}); r != nil || err != nil {
		t.Fatalf("background: %+v, %v", r, err)
	}
	if _, err := c.Warmup([]string{"w"}, &WarmupOptions{Threads: -1}); !isSyscallError(err, "warmup", syscall.EINVAL) {
		t.Fatalf("negative threads: %v", err)
	}
}

func sum(m map[string]uint64) uint64 {
	var n uint64
	for _, v := range m {
		n += v
	}
	return n
}

func TestStatus(t *testing.T) {
	c := newTestClient(t)
	writeFile(t, c, "f", "data")

	st, err := c.Status(false)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Setting
	if s.Name != "test" || s.UUID == "" || s.Storage != "file" || s.Bucket == "" || s.BlockSize != 4<<20 ||
		s.Compression != "" || s.EncryptAlgo != "" || s.TrashDays != 0 {
		t.Fatalf("Setting: %+v", s)
	}
	if st.Stat.UsedInodes == 0 || st.Stat.AvailableInodes == 0 || st.Stat.AvailableSpace == 0 || st.Stat.Trash != nil {
		t.Fatalf("Stat: %+v", st.Stat)
	}
	var sid uint64
	for _, ss := range st.Sessions {
		if ss.ProcessID == os.Getpid() {
			sid = ss.Sid
		}
		if ss.Sustained != nil || ss.Flocks != nil || ss.Plocks != nil {
			t.Fatalf("Status has the details of session %+v", ss)
		}
	}
	if sid == 0 {
		t.Fatalf("own session missing in %+v", st.Sessions)
	}

	if st, err = c.Status(true); err != nil || st.Stat.Trash == nil {
		t.Fatalf("Status(true): %+v, %v", st, err)
	}

	ss, err := c.SessionInfo(sid)
	if err != nil || ss.Sid != sid || ss.ProcessID != os.Getpid() || ss.HostName == "" || ss.Version == "" ||
		ss.Expire.Before(time.Now()) {
		t.Fatalf("SessionInfo: %+v, %v", ss, err)
	}
	if _, err := c.SessionInfo(sid + 1000); !isSyscallError(err, "sessioninfo", syscall.EIO) {
		t.Fatalf("missing session: %v", err)
	}
}

func TestAdminClosed(t *testing.T) {
	c := newTestClient(t)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for op, err := range map[string]error{
		"getxattr":    second(c.Getxattr("f", "user.a")),
		"listxattr":   second(c.Listxattr("f")),
		"setxattr":    c.Setxattr("f", "user.a", nil, 0),
		"removexattr": c.Removexattr("f", "user.a"),
		"info":        second(c.Info("f", nil)),
		"summary":     second(c.Summary("f", 0, 1)),
		"setquota":    c.SetQuota("f", QuotaOptions{Inodes: 1}),
		"getquota":    second(c.GetQuota("f")),
		"delquota":    c.DelQuota("f"),
		"checkquota":  second(c.CheckQuota("f", nil)),
	} {
		if !isPathError(err, op, fs.ErrClosed) {
			t.Errorf("%s: %v", op, err)
		}
	}
	if err := c.Clone("a", "b", false); !isLinkError(err, "clone", fs.ErrClosed) {
		t.Errorf("clone: %v", err)
	}
	for op, err := range map[string]error{
		"listquota":   second(c.ListQuota()),
		"warmup":      second(c.Warmup(nil, nil)),
		"status":      second(c.Status(false)),
		"sessioninfo": second(c.SessionInfo(1)),
	} {
		if !isSyscallError(err, op, fs.ErrClosed) {
			t.Errorf("%s: %v", op, err)
		}
	}
}
