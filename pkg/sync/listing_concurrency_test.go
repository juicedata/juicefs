/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"os"
	"path/filepath"
	"runtime"
	gosync "sync"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

type gatedListingStore struct {
	object.ObjectStorage
	entered chan struct{}
	release chan struct{}
	once    gosync.Once
}

func (s *gatedListingStore) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]object.Object, bool, string, error) {
	if prefix != "" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, false, "", ctx.Err()
		}
	}
	return s.ObjectStorage.List(ctx, prefix, marker, token, delimiter, limit, followLink)
}

func TestParallelListingBoundsWaitingGoroutines(t *testing.T) {
	src, err := object.CreateStorage("mem", "bounded-src", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := object.CreateStorage("mem", "bounded-dst", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2048; i++ {
		if err := src.Put(ctx, fmt.Sprintf("dir-%04d/file", i), bytes.NewReader([]byte("x"))); err != nil {
			t.Fatal(err)
		}
	}
	gated := &gatedListingStore{ObjectStorage: src, entered: make(chan struct{}), release: make(chan struct{})}
	config := newTestConfig()
	config.EnableCheckpoint = false
	config.ListDepth = 1
	before := runtime.NumGoroutine()
	done := make(chan error, 1)
	go func() { done <- Sync(gated, dst, config) }()
	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		close(gated.release)
		t.Fatal("child listing did not start")
	}
	peak := 0
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		peak = max(peak, runtime.NumGoroutine()-before)
		if peak > 128 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(gated.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sync: %s", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("sync did not finish after releasing the listing gate")
	}
	t.Logf("waiting goroutine delta with ListThreads=%d: %d", config.ListThreads, peak)
	if peak > 128 {
		t.Errorf("waiting goroutines grew with directory count: %d", peak)
	}
}

func TestParallelListingNestedInline(t *testing.T) {
	for _, listThreads := range []int{1, 2, 3} {
		for _, filesFrom := range []bool{false, true} {
			t.Run(fmt.Sprintf("threads-%d/files-from-%v", listThreads, filesFrom), func(t *testing.T) {
				src, err := object.CreateStorage("mem", "nested-src", "", "", "")
				if err != nil {
					t.Fatal(err)
				}
				dst, err := object.CreateStorage("mem", "nested-dst", "", "", "")
				if err != nil {
					t.Fatal(err)
				}
				var prefixes bytes.Buffer
				keys := make([]string, 0, 64)
				for i := 0; i < 16; i++ {
					fmt.Fprintf(&prefixes, "dir-%02d/\n", i)
					for j := 0; j < 4; j++ {
						key := fmt.Sprintf("dir-%02d/sub-%02d/leaf/file", i, j)
						keys = append(keys, key)
						if err := src.Put(ctx, key, bytes.NewReader([]byte(key))); err != nil {
							t.Fatal(err)
						}
					}
				}
				config := newTestConfig()
				config.EnableCheckpoint = false
				config.ListThreads = listThreads
				config.ListDepth = 3
				if filesFrom {
					config.FilesFrom = filepath.Join(t.TempDir(), "prefixes")
					if err := os.WriteFile(config.FilesFrom, prefixes.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := Sync(src, dst, config); err != nil {
					t.Fatal(err)
				}
				for _, key := range keys {
					r, err := dst.Get(ctx, key, 0, -1)
					if err != nil {
						t.Fatalf("get destination %q: %v", key, err)
					}
					data, err := io.ReadAll(r)
					_ = r.Close()
					if err != nil || !bytes.Equal(data, []byte(key)) {
						t.Fatalf("incomplete destination %q: data=%q, err=%v", key, data, err)
					}
				}
			})
		}
	}
}
