/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func testKeysEqual(objs []Object, expectedKeys []string) error {
	gottenKeys := make([]string, len(objs))
	for idx, obj := range objs {
		gottenKeys[idx] = obj.Key()
	}
	if len(gottenKeys) != len(expectedKeys) {
		return fmt.Errorf("Expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
			strings.Join(gottenKeys, ", "))
	}

	for idx, key := range gottenKeys {
		if key != expectedKeys[idx] {
			return fmt.Errorf("Expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
				strings.Join(gottenKeys, ", "))
		}
	}
	return nil
}

func TestDisk2(t *testing.T) {
	s, _ := newDisk("/tmp/abc/", "", "", "")
	s = WithPrefix(s, "prefix/")
	testFileSystem(t, s)
}

func TestSftp2(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	sftp, _ := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	testFileSystem(t, sftp)
}

func TestHDFS2(t *testing.T) { //skip mutate
	if os.Getenv("HDFS_ADDR") == "" {
		t.Skip()
	}
	dfs, _ := newHDFS(os.Getenv("HDFS_ADDR"), "", "", "")
	testFileSystem(t, dfs)
}

func testFileSystem(t *testing.T, s ObjectStorage) {
	keys := []string{
		"x/",
		"x/x.txt",
		"xy.txt",
		"xyz/",
		"xyz/xyz.txt",
	}
	// initialize directory tree
	for _, key := range keys {
		if err := s.Put(key, bytes.NewReader([]byte{})); err != nil {
			t.Fatalf("PUT object `%s` failed: %q", key, err)
		}
	}
	if o, err := s.Head("x/"); err != nil {
		t.Fatalf("Head x/: %s", err)
	} else if f, ok := o.(File); !ok {
		t.Fatalf("Head should return File")
	} else if !f.IsDir() {
		t.Fatalf("x/ should be a dir")
	}
	// cleanup
	defer func() {
		// delete reversely, directory only can be deleted when it's empty
		objs, err := listAll(s, "", "", 100)
		if err != nil {
			t.Fatalf("listall failed: %s", err)
		}
		gottenKeys := make([]string, len(objs))
		for idx, obj := range objs {
			gottenKeys[idx] = obj.Key()
		}
		idx := len(gottenKeys) - 1
		for ; idx >= 0; idx-- {
			if err := s.Delete(gottenKeys[idx]); err != nil {
				t.Fatalf("DELETE object `%s` failed: %q", gottenKeys[idx], err)
			}
		}
	}()
	objs, err := listAll(s, "x/", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys := []string{"x/", "x/x.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	objs, err = listAll(s, "x", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{"x/", "x/x.txt", "xy.txt", "xyz/", "xyz/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	objs, err = listAll(s, "xy", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{"xy.txt", "xyz/", "xyz/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	if ss, ok := s.(SupportSymlink); ok {
		// a< a- < a/ < a0    <    b< b- < b/ < b0
		_ = s.Put("a-", bytes.NewReader([]byte{}))
		_ = s.Put("a0", bytes.NewReader([]byte{}))
		_ = s.Put("b-", bytes.NewReader([]byte{}))
		_ = s.Put("b0", bytes.NewReader([]byte{}))
		_ = s.Put("xyz/ol1/p.txt", bytes.NewReader([]byte{}))
		_ = ss.Symlink("./xyz/ol1/", "a")
		_ = ss.Symlink("./xyz/notExist/", "b")
		objs, err = listAll(s, "", "", 100)
		if err != nil {
			t.Fatalf("listall failed: %s", err)
		}
		expectedKeys = []string{"", "a-", "a/", "a/p.txt", "a0", "b", "b-", "b0", "x/", "x/x.txt", "xy.txt", "xyz/", "xyz/ol1/", "xyz/ol1/p.txt", "xyz/xyz.txt"}
		if err = testKeysEqual(objs, expectedKeys); err != nil {
			t.Fatalf("testKeysEqual fail: %s", err)
		}
	}
}
