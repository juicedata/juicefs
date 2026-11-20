/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type fsckHeadStorage struct {
	object.ObjectStorage
	failures map[string]bool
	checked  []string
}

func (s *fsckHeadStorage) Head(ctx context.Context, key string) (object.Object, error) {
	s.checked = append(s.checked, key)
	if s.failures[filepath.Base(key)] {
		return nil, fmt.Errorf("injected HEAD failure: %w", os.ErrPermission)
	}
	return s.ObjectStorage.Head(ctx, key)
}

func (s *fsckHeadStorage) ListAll(context.Context, string, string, bool) (<-chan object.Object, error) {
	objects := make(chan object.Object, 1)
	objects <- nil // Force fsck to verify every referenced block with Head.
	close(objects)
	return objects, nil
}

func TestFsckIncompleteObjectChecks(t *testing.T) {
	const storageName = "fsck-head-test"
	var storage *fsckHeadStorage
	object.Register(storageName, func(bucket, accessKey, secretKey, token string) (object.ObjectStorage, error) {
		return storage, nil
	})
	for _, tc := range []struct {
		name     string
		failures int
		withPath bool
	}{
		{"healthy-path", 0, true},
		{"one-head-failure", 1, true},
		{"all-heads-fail", 2, true},
		{"failed-listing-recovered-by-heads", 0, false},
		{"failed-listing-and-head", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket := t.TempDir() + "/"
			local, err := object.CreateStorage("file", bucket, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			storage = &fsckHeadStorage{ObjectStorage: local, failures: make(map[string]bool)}
			metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "fsck.db")
			m := meta.NewClient(metaURL, nil)
			defer m.Shutdown()
			if err := m.Init(&meta.Format{Name: "fsck-test", Storage: storageName, Bucket: bucket, BlockSize: 4096}, true); err != nil {
				t.Fatal(err)
			}
			ctx := meta.Background()
			for i := 0; i < 2; i++ {
				var inode meta.Ino
				if st := m.Create(ctx, 1, fmt.Sprintf("f%d", i), 0644, 0, 0, &inode, &meta.Attr{}); st != 0 {
					t.Fatal(st)
				}
				var id uint64
				if st := m.NewSlice(ctx, &id); st != 0 {
					t.Fatal(st)
				}
				if st := m.Write(ctx, inode, 0, 0, meta.Slice{Id: id, Size: 4, Len: 4}, time.Now()); st != 0 {
					t.Fatal(st)
				}
				key := fmt.Sprintf("%d_0_4", id)
				if err := local.Put(context.Background(), "fsck-test/chunks/0/0/"+key, bytes.NewReader([]byte("data"))); err != nil {
					t.Fatal(err)
				}
				storage.failures[key] = i < tc.failures
			}
			args := []string{"juicefs", "fsck", metaURL}
			if tc.withPath {
				args = append(args, "--path", "/", "--recursive")
			}
			err = Main(args)
			if len(storage.checked) != 2 {
				t.Fatalf("checked %d blocks, expected both blocks to be checked", len(storage.checked))
			}
			if tc.failures == 0 && err != nil {
				t.Fatal(err)
			}
			if tc.failures > 0 && (err == nil || !strings.Contains(err.Error(), "could not verify")) {
				t.Fatalf("expected an incomplete-check error, got %v", err)
			}
		})
	}
}

func TestFsck(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/f%d.txt", testMountPoint, i)
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("write file failed: %s", err)
		}
	}
	if err := Main([]string{"", "fsck", testMeta}); err != nil {
		t.Fatalf("fsck failed: %s", err)
	}
	if err := Main([]string{"", "fsck", testMeta, "--path", "/f3.txt"}); err != nil {
		t.Fatalf("fsck failed: %s", err)
	}
}

func TestFsckRepairDirMode(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	if err := os.MkdirAll(testMountPoint+"/testdir", 0755); err != nil {
		t.Fatalf("mkdir failed: %s", err)
	}

	if err := Main([]string{"", "fsck", testMeta, "--path", "/testdir", "--repair-dir-mode", "0700"}); err != nil {
		t.Fatalf("fsck with repair-dir-mode 0700 failed: %s", err)
	}

	if err := Main([]string{"", "fsck", testMeta, "--path", "/testdir", "--repair-dir-mode", "0755"}); err != nil {
		t.Fatalf("fsck with repair-dir-mode 0755 failed: %s", err)
	}
}
