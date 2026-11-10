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
	"context"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func feedObjects(objs []Object) <-chan Object {
	ch := make(chan Object) // unbuffered: the producer must never be left blocked
	go func() {
		defer close(ch)
		for _, o := range objs {
			ch <- o
		}
	}()
	return ch
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %s", err)
		}
		if len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("temporary files are left in %s: %v", dir, entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSortObjects(t *testing.T) {
	old := externalSortChunkSize
	defer func() { externalSortChunkSize = old }()

	mtime := time.Unix(1700000000, 123456789)
	want := map[string]Object{}
	var objs []Object
	add := func(o Object) {
		objs = append(objs, o)
		want[o.Key()] = o
	}
	for i := 0; i < 200; i++ {
		add(&obj{key: fmt.Sprintf("dir/%04d", i), size: int64(i), mtime: mtime.Add(time.Duration(i) * time.Second)})
	}
	add(&obj{key: "dir/", isDir: true, mtime: mtime})
	add(&obj{key: "a\nb", size: 1, mtime: mtime})
	add(&obj{key: "中文/文件", size: 2, mtime: mtime})
	add(&obj{key: "no-mtime", size: 3})
	add(&file{obj{key: "f/file", size: 4, mtime: mtime}, "alice", "staff", 0640, false})
	add(&file{obj{key: "f/link", mtime: mtime}, "", "", os.ModeSymlink | 0777, true})
	add(&file{obj{key: "f/dir/", isDir: true, mtime: mtime}, "root", "root", os.ModeDir | 0755, false})
	objs = append(objs, &obj{key: "dir/0042", size: 42, mtime: mtime.Add(42 * time.Second)}) // duplicate
	var wantKeys []string
	for k := range want {
		wantKeys = append(wantKeys, k)
	}
	sort.Strings(wantKeys)

	for _, chunk := range []int{1 << 10, 7} {
		t.Run(fmt.Sprintf("chunk %d", chunk), func(t *testing.T) {
			externalSortChunkSize = chunk
			rand.Shuffle(len(objs), func(i, j int) { objs[i], objs[j] = objs[j], objs[i] })
			dir := t.TempDir()
			var keys []string
			for o := range SortObjects(context.Background(), dir, feedObjects(objs)) {
				if o == nil {
					t.Fatalf("unexpected listing failure")
				}
				w := want[o.Key()]
				if w == nil {
					t.Fatalf("unexpected key %q", o.Key())
				}
				if o.Size() != w.Size() || o.IsDir() != w.IsDir() || o.IsSymlink() != w.IsSymlink() || !o.Mtime().Equal(w.Mtime()) {
					t.Fatalf("object %q = (%d, %v, %v, %s), want (%d, %v, %v, %s)", o.Key(),
						o.Size(), o.IsDir(), o.IsSymlink(), o.Mtime(), w.Size(), w.IsDir(), w.IsSymlink(), w.Mtime())
				}
				if wf, ok := w.(File); ok {
					f, ok := o.(File)
					if !ok || f.Owner() != wf.Owner() || f.Group() != wf.Group() || f.Mode() != wf.Mode() {
						t.Fatalf("file %q = %+v, want %+v", o.Key(), o, w)
					}
				}
				keys = append(keys, o.Key())
			}
			if !reflect.DeepEqual(keys, wantKeys) {
				t.Fatalf("keys = %q, want %q", keys, wantKeys)
			}
			assertDirEmpty(t, dir)
		})
	}

	for _, chunk := range []int{1 << 10, 7} {
		t.Run(fmt.Sprintf("listing failure with chunk %d", chunk), func(t *testing.T) {
			externalSortChunkSize = chunk
			var failed []Object
			for i := 0; i < 50; i++ {
				failed = append(failed, &obj{key: fmt.Sprintf("k%d", 50-i)})
			}
			failed = append(failed, nil, &obj{key: "after-failure"})
			dir := t.TempDir()
			var got []Object
			for o := range SortObjects(context.Background(), dir, feedObjects(failed)) {
				got = append(got, o)
			}
			if len(got) != 1 || got[0] != nil {
				t.Fatalf("got %v, want only a nil object for the failure", got)
			}
			assertDirEmpty(t, dir)
		})
	}

	t.Run("caller stops early", func(t *testing.T) {
		externalSortChunkSize = 7
		var many []Object
		for i := 0; i < 5000; i++ {
			many = append(many, &obj{key: fmt.Sprintf("k%05d", 5000-i)})
		}
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		in := make(chan Object)
		produced := make(chan struct{})
		go func() {
			defer close(produced)
			defer close(in)
			for _, o := range many {
				in <- o
			}
		}()
		out := SortObjects(ctx, dir, in)
		if o := <-out; o == nil || o.Key() != "k00001" {
			t.Fatalf("first object = %v", o)
		}
		cancel()
		for o := range out {
			if o == nil {
				t.Fatalf("unexpected failure marker after cancel")
			}
		}
		<-produced
		assertDirEmpty(t, dir)
	})
}
