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
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

// unorderedStore returns keys in descending order, three per page.
// Tokens and StartAfter follow that order.
type unorderedStore struct {
	object.ObjectStorage
}

func (s *unorderedStore) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]object.Object, bool, string, error) {
	all, _, _, err := s.ObjectStorage.List(ctx, prefix, "", "", delimiter, math.MaxInt64, followLink)
	if err != nil {
		return nil, false, "", err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key() > all[j].Key() })
	var start int
	if token != "" {
		start, _ = strconv.Atoi(token)
	} else if startAfter != "" {
		start = sort.Search(len(all), func(i int) bool { return all[i].Key() < startAfter })
	}
	end := min(start+3, len(all))
	return all[start:end], end < len(all), strconv.Itoa(end), nil
}

func newUnorderedStore(t *testing.T, keys []string) object.ObjectStorage {
	m, _ := object.CreateStorage("mem", "", "", "", "")
	for _, k := range keys {
		if err := m.Put(ctx, k, bytes.NewReader([]byte(k))); err != nil {
			t.Fatalf("put %s: %s", k, err)
		}
	}
	return &unorderedStore{m}
}

func collectKeys(t *testing.T, ch <-chan object.Object) []string {
	t.Helper()
	var keys []string
	for o := range ch {
		if o == nil {
			t.Fatalf("listing failed after %v", keys)
		}
		keys = append(keys, o.Key())
	}
	return keys
}

func TestListSortedWithUnorderedStore(t *testing.T) {
	store := newUnorderedStore(t, []string{"a/x/y", "a/x.conda", "a/x.txt", "a/w", "b"})
	sortDir := t.TempDir()

	ch, err := listAll(ctx, store, "", "", "", true, true, "")
	if err != nil {
		t.Fatalf("list all: %s", err)
	}
	var failed bool
	for o := range ch {
		failed = failed || o == nil
	}
	if !failed {
		t.Fatalf("listing the unordered store without sorting should fail")
	}

	// flat listing, resumed after a key in the byte order
	ch, err = listAllWithExternalSort(ctx, store, "", "a/x.conda", "", true, false, sortDir)
	if err != nil {
		t.Fatalf("list all: %s", err)
	}
	if keys, want := collectKeys(t, ch), []string{"a/x.txt", "a/x/y", "b"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	ch, err = listAllWithExternalSort(ctx, store, "", "a/w", "a/x/y", true, true, sortDir)
	if err != nil {
		t.Fatalf("list all: %s", err)
	}
	if keys, want := collectKeys(t, ch), []string{"a/w", "a/x.conda", "a/x.txt", "a/x/y"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	// one level, resumed after "a/x.conda": the store would skip "a/x/" if it were sent as StartAfter
	cp := make(chan object.Object, 10)
	var children []string
	ch, err = listCommonPrefixWithExternalSort(ctx, store, "a/", cp, true, "a/x.conda", func(p string) { children = append(children, p) }, sortDir)
	if err != nil {
		t.Fatalf("list level: %s", err)
	}
	if keys, want := collectKeys(t, ch), []string{"a/x.txt"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	close(cp)
	if want := []string{"a/x/"}; !reflect.DeepEqual(children, want) || len(cp) != 1 {
		t.Fatalf("child prefixes = %v (%d sent), want %v", children, len(cp), want)
	}
}

func TestSyncExternalSort(t *testing.T) {
	srcKeys := []string{"a", "a/x/y", "a/x.conda", "a/x.txt", "foo.conda", "foo-bar", "z"}
	for i := 0; i < 20; i++ {
		srcKeys = append(srcKeys, fmt.Sprintf("foo/%02d", i), fmt.Sprintf("foo/sub/%02d", i))
	}
	sort.Strings(srcKeys)

	run := func(t *testing.T, listThreads, listDepth int, sortDir string) ([]string, bool, error) {
		t.Helper()
		src := newUnorderedStore(t, srcKeys)
		dst, _ := object.CreateStorage("mem", "", "", "", "")
		for _, k := range []string{"foo/05", "foo/zz-stale", "stale", "a/x/stale"} {
			_ = dst.Put(ctx, k, bytes.NewReader([]byte("old")))
		}
		err := Sync(src, dst, &Config{
			Threads:         4,
			ListThreads:     listThreads,
			ListDepth:       listDepth,
			ExternalSortDir: sortDir,
			DeleteDst:       true,
			ForceUpdate:     true,
			Quiet:           true,
			Limit:           -1,
			MaxSize:         math.MaxInt64,
		})
		ch, _ := ListAll(dst, "", "", "", true)
		keys := collectAll(ch)
		updated := true
		for _, k := range keys {
			r, err := dst.Get(ctx, k, 0, -1)
			if err != nil {
				t.Fatalf("get %s: %s", k, err)
			}
			if data, _ := io.ReadAll(r); string(data) != k {
				updated = false
			}
		}
		return keys, updated, err
	}

	for _, c := range []struct{ listThreads, listDepth int }{{1, 1}, {4, 1}, {4, 3}} {
		t.Run(fmt.Sprintf("list-threads=%d,list-depth=%d", c.listThreads, c.listDepth), func(t *testing.T) {
			sortDir := t.TempDir()
			keys, updated, err := run(t, c.listThreads, c.listDepth, sortDir)
			if err != nil {
				t.Fatalf("sync: %s", err)
			}
			if !reflect.DeepEqual(keys, srcKeys) || !updated {
				t.Fatalf("dst keys = %v (updated %v), want %v", keys, updated, srcKeys)
			}
		})
	}
}
