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

package sync

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

// unorderedStore serves a fixed sequence of pages whose entries are not in
// lexicographic order, mimicking backends such as TOS buckets with
// hierarchical namespace, where directories are ordered by name without the
// trailing "/" and entries of one directory come before its sub-directories.
type unorderedStore struct {
	object.ObjectStorage
	pages [][]object.Object
}

func (u *unorderedStore) String() string { return "unordered://" }

func (u *unorderedStore) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]object.Object, bool, string, error) {
	idx := 0
	if token != "" {
		idx, _ = strconv.Atoi(token)
	}
	if idx >= len(u.pages) {
		return nil, false, "", nil
	}
	next := ""
	if idx+1 < len(u.pages) {
		next = strconv.Itoa(idx + 1)
	}
	return u.pages[idx], next != "", next, nil
}

func TestListCommonPrefixSortsUnorderedPages(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	keys := []string{"a/x.conda", "a/x/", "a/x.txt", "a/b/", "a/.cache/", "a/z", "a/y-1", "a/y/"}
	byKey := map[string]object.Object{}
	for _, k := range keys {
		if err := mem.Put(ctx, k, bytes.NewReader(nil)); err != nil {
			t.Fatal(err)
		}
		o, err := mem.Head(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		byKey[k] = o
	}
	// Two pages, each internally out of order and the second page starting
	// below the last key of the first page ("a/x.conda" < "a/x/").
	pages := [][]object.Object{
		{byKey["a/x/"], byKey["a/b/"], byKey["a/y/"], byKey["a/.cache/"]},
		{byKey["a/x.conda"], byKey["a/z"], byKey["a/y-1"], byKey["a/x.txt"]},
	}
	store := &unorderedStore{ObjectStorage: mem, pages: pages}

	cp := make(chan object.Object, 100)
	var childPrefixes []string
	srckeys, err := listCommonPrefix(store, "a/", cp, false, "", func(k string) { childPrefixes = append(childPrefixes, k) })
	if err != nil {
		t.Fatalf("listCommonPrefix: %v", err)
	}
	var got []string
	for o := range srckeys {
		got = append(got, o.Key())
	}
	close(cp)
	var dirs []string
	for o := range cp {
		dirs = append(dirs, o.Key())
	}

	wantFiles := []string{"a/x.conda", "a/x.txt", "a/y-1", "a/z"}
	wantDirs := []string{"a/.cache/", "a/b/", "a/x/", "a/y/"}
	assertEqualStrings(t, "files", got, wantFiles)
	assertEqualStrings(t, "dirs", dirs, wantDirs)
	assertEqualStrings(t, "child prefixes", childPrefixes, wantDirs)
}

func assertEqualStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}
