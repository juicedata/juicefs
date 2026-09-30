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
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafePath(t *testing.T) {
	cases := []struct {
		root, key string
		want      string
		ok        bool
	}{
		// directory root
		{"/data/", "", "/data/", true},
		{"/data/", ".", "/data/", true},
		{"/data/", "a/b", "/data/a/b", true},
		{"/data/", "a/", "/data/a/", true},
		{"/data/", "a/..", "/data/", true},
		{"/data/", "a/../b", "/data/b", true},
		{"/data/", "c/../t/", "/data/t/", true},
		{"/data/", "a//b", "/data/a/b", true},
		{"/data/", "./a/./b", "/data/a/b", true},
		{"/data/", "/a", "/data/a", true},
		{"/data/", "..", "", false},
		{"/data/", "../x", "", false},
		{"/data/", "a/../../x", "", false},
		{"/data/", "/../x", "", false},
		{"/data/", "../data2/x", "", false},
		// non-directory root: the boundary is its parent directory
		{"/data/prefix-", "", "/data/prefix-", true},
		{"/data/prefix-", "a", "/data/prefix-a", true},
		{"/data/prefix-", "/../x", "/data/x", true},
		{"/data/prefix-", "/../../x", "", false},
		// file system root
		{"/", "", "/", true},
		{"/", "a", "/a", true},
		{"/", "../a", "/a", true},
		// relative roots (nfs/cifs/gluster, withPrefix)
		{"", "", "", true},
		{"", "a/b", "a/b", true},
		{"", "a/./b/", "a/b/", true},
		{"", "..", "", false},
		{"", "../a", "", false},
		{"", "a/../../b", "", false},
		// '\' is a normal character in POSIX paths
		{"", `..\a`, `..\a`, true},
		{"backup/", `..\a`, `backup/..\a`, true},
		{"./", "", "./", true},
		{"./", "./", "./", true},
		{"./", "a", "a", true},
		{"./", "a/", "a/", true},
		{"./", "../a", "", false},
		{"backup/", "", "backup/", true},
		{"backup/", "a", "backup/a", true},
		{"backup/", "../a", "", false},
		{"backup/", "../backup2/a", "", false},
		{"backup", "", "backup", true},
		{"backup", "-a", "backup-a", true},
		{"backup", "/../a", "a", true},
		{"backup", "/../../a", "", false},
		// Windows style root for filestore
		{"C:/data/", "a", "C:/data/a", true},
		{"C:/data/", "../a", "", false},
		{"C:/", "a", "C:/a", true},
		{"C:/", "../a", "", false},
	}
	for _, c := range cases {
		p, err := safePath(c.root, c.key)
		if (err == nil) != c.ok || p != c.want {
			t.Errorf("safePath(%q, %q) = (%q, %v), want (%q, ok=%v)", c.root, c.key, p, err, c.want, c.ok)
		}
	}
}

func TestSafeRawPath(t *testing.T) {
	if p, err := safeRawPath("/base/", "a//./b"); err != nil || p != "/base/a//./b" {
		t.Fatalf("safeRawPath = %q, %v", p, err)
	}
	if _, err := safeRawPath("/base/", "../x/"); err == nil {
		t.Fatal("safeRawPath(../x/) should fail")
	}
}

func TestSafeBackslashPath(t *testing.T) {
	cases := []struct {
		root, key string
		want      string
		ok        bool
	}{
		{"", "", "", true},
		{"", `a\b`, "a/b", true},
		{"", `a\b\`, "a/b/", true},
		{"", `a\..\b`, "b", true},
		{"", `..\a`, "", false},
		{"", `a\..\..\b`, "", false},
		{"", `a/..\..\b`, "", false},
		{"backup/", `a\b`, "backup/a/b", true},
		{"backup/", `..\outside/proof.txt`, "", false},
		{`backup\`, `..\a`, "", false},
	}
	for _, c := range cases {
		p, err := safeBackslashPath(c.root, c.key)
		if (err == nil) != c.ok || p != c.want {
			t.Errorf("safeBackslashPath(%q, %q) = (%q, %v), want (%q, ok=%v)", c.root, c.key, p, err, c.want, c.ok)
		}
	}
}

func TestSafeLocalPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root") + "/"
	if p, err := safeLocalPath(root, "a/b"); err != nil || p != filepath.Join(root, "a", "b") {
		t.Fatalf("safeLocalPath(a/b) = %q, %v", p, err)
	}
	if p, err := safeLocalPath(root, ""); err != nil || p != filepath.Clean(root) {
		t.Fatalf("safeLocalPath('') = %q, %v", p, err)
	}
	if _, err := safeLocalPath(root, "../x"); err == nil {
		t.Fatal("safeLocalPath(../x) should fail")
	}
	if runtime.GOOS == "windows" {
		if _, err := safeLocalPath(root, `..\x`); err == nil {
			t.Fatal(`safeLocalPath(..\x) should fail`)
		}
		if p, err := safeLocalPath("//server/share/backup/", "a/b"); err != nil || p != `\\server\share\backup\a\b` {
			t.Fatalf("safeLocalPath(UNC) = %q, %v", p, err)
		}
		if _, err := safeLocalPath("//server/share/backup/", "../x"); err == nil {
			t.Fatal("safeLocalPath(UNC, ../x) should fail")
		}
	}
}
