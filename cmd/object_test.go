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
package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func testKeysEqual(objs []object.Object, expectedKeys []string) error {
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

// copied from pkg/object/filesystem_test.go
func testFileSystem(t *testing.T, s object.ObjectStorage) {
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
	} else if f, ok := o.(object.File); !ok {
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

	if ss, ok := s.(object.SupportSymlink); ok {
		// a< a- < a/ < a0    <    b< b- < b/ < b0
		_ = s.Put("a-", bytes.NewReader([]byte{}))
		_ = s.Put("a0", bytes.NewReader([]byte{}))
		_ = s.Put("b-", bytes.NewReader([]byte{}))
		_ = s.Put("b0", bytes.NewReader([]byte{}))
		_ = s.Put("xyz/ol1/p.txt", bytes.NewReader([]byte{}))
		if err = ss.Symlink("./xyz/ol1/", "a"); err != nil {
			t.Fatalf("symlink a %s", err)
		}
		if target, err := ss.Readlink("a"); err != nil || target != "./xyz/ol1/" {
			t.Fatalf("readlink a %s %s", target, err)
		}
		if err = ss.Symlink("/xyz/notExist/", "b"); err != nil {
			t.Fatalf("symlink b %s", err)
		}
		if target, err := ss.Readlink("b"); err != nil || target != "/xyz/notExist/" {
			t.Fatalf("readlink b %s %s", target, err)
		}
		objs, err = listAll(s, "", "", 100)
		if err != nil {
			t.Fatalf("listall failed: %s", err)
		}
		expectedKeys = []string{"", "a-", "a/", "a/p.txt", "a0", "b", "b-", "b0", "x/", "x/x.txt", "xy.txt", "xyz/", "xyz/ol1/", "xyz/ol1/p.txt", "xyz/xyz.txt"}
		if err = testKeysEqual(objs, expectedKeys); err != nil {
			t.Fatalf("testKeysEqual fail: %s", err)
		}
	}

	// put a file with very long name
	longName := strings.Repeat("a", 255)
	if err := s.Put("dir/"+longName, bytes.NewReader([]byte{0})); err != nil {
		t.Fatalf("PUT a file with long name `%s` failed: %q", longName, err)
	}
}

func TestJFS(t *testing.T) {
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
		Meta: meta.DefaultConf(),
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize << 10,
			MaxUpload:  1,
			BufferSize: 100 << 20,
		},
		DirEntryTimeout: time.Millisecond * 100,
		EntryTimeout:    time.Millisecond * 100,
		AttrTimeout:     time.Millisecond * 100,
		AccessLog:       "/tmp/juicefs.access.log",
	}
	objStore, _ := object.CreateStorage("mem", "", "", "", "")
	store := chunk.NewCachedStore(objStore, *conf.Chunk, nil)
	jfs, err := fs.NewFileSystem(&conf, m, store)
	if err != nil {
		t.Fatalf("initialize  failed: %s", err)
	}

	jstore := &juiceFS{object.DefaultObjectStorage{}, "test", uint16(utils.GetUmask()), jfs}
	testFileSystem(t, jstore)
	testFileSystem(t, object.WithPrefix(jstore, "unittest/"))
}
