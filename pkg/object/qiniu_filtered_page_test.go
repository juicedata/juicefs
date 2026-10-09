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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/qiniu/go-sdk/v7/auth"
	qiniuclient "github.com/qiniu/go-sdk/v7/client"
	"github.com/qiniu/go-sdk/v7/storage"
)

func filteredPageStore(t *testing.T, pages map[string]storage.ListFilesRet) (*qiniu, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marker := r.URL.Query().Get("marker")
		mu.Lock()
		requests = append(requests, marker)
		mu.Unlock()
		page, ok := pages[marker]
		if !ok {
			t.Errorf("unexpected marker %q", marker)
			http.Error(w, "unexpected marker", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(server.Close)
	store := &qiniu{
		s3client: s3client{bucket: "bucket"},
		bm: storage.NewBucketManagerEx(auth.New("test-access", "test-secret"),
			&storage.Config{RsfHost: server.URL}, &qiniuclient.Client{Client: server.Client()}),
	}
	return store, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

func TestQiniuFilteredPages(t *testing.T) {
	t.Run("visible-first-page-keeps-cursor", func(t *testing.T) {
		store, requests := filteredPageStore(t, map[string]storage.ListFilesRet{
			"": {Marker: "tail", Items: []storage.ListItem{{Key: "dir/z"}}},
		})
		objects, more, marker, err := store.List(context.Background(), "dir/", "dir/m", "", "", 10, false)
		if err != nil || len(objects) != 1 || objects[0].Key() != "dir/z" || !more || marker != "tail" || len(requests()) != 1 {
			t.Fatalf("objects=%v more=%v marker=%q err=%v requests=%v", objects, more, marker, err, requests())
		}
	})
	t.Run("skip-filtered-directory-page", func(t *testing.T) {
		store, requests := filteredPageStore(t, map[string]storage.ListFilesRet{
			"":       {Marker: "second", CommonPrefixes: []string{"dir/a/"}},
			"second": {CommonPrefixes: []string{"dir/z/"}},
		})
		objects, more, marker, err := store.List(context.Background(), "dir/", "dir/m", "", "/", 10, false)
		if err != nil || len(objects) != 1 || objects[0].Key() != "dir/z/" || !objects[0].IsDir() || more || marker != "" {
			t.Fatalf("objects=%v more=%v marker=%q err=%v", objects, more, marker, err)
		}
		if !reflect.DeepEqual(requests(), []string{"", "second"}) {
			t.Fatalf("unexpected page requests: %v", requests())
		}
	})
	t.Run("skip-filtered-object-page", func(t *testing.T) {
		store, requests := filteredPageStore(t, map[string]storage.ListFilesRet{
			"":       {Marker: "second", Items: []storage.ListItem{{Key: "dir/a"}}},
			"second": {Marker: "tail", Items: []storage.ListItem{{Key: "dir/z"}}},
		})
		objects, more, marker, err := store.List(context.Background(), "dir/", "dir/m", "", "", 10, false)
		if err != nil || len(objects) != 1 || objects[0].Key() != "dir/z" || !more || marker != "tail" {
			t.Fatalf("objects=%v more=%v marker=%q err=%v", objects, more, marker, err)
		}
		if !reflect.DeepEqual(requests(), []string{"", "second"}) {
			t.Fatalf("unexpected page requests: %v", requests())
		}
	})
	t.Run("list-all-preserves-start-bound", func(t *testing.T) {
		store, requests := filteredPageStore(t, map[string]storage.ListFilesRet{
			"":       {Marker: "second", Items: []storage.ListItem{{Key: "dir/a"}}},
			"second": {Marker: "third", Items: []storage.ListItem{{Key: "dir/b"}}},
			"third":  {Items: []storage.ListItem{{Key: "dir/z"}}},
		})
		objects, err := ListAll(context.Background(), store, "dir/", "dir/m", false, true)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for obj := range objects {
			if obj == nil {
				t.Fatal("ListAll error sentinel")
			}
			keys = append(keys, obj.Key())
		}
		if !reflect.DeepEqual(keys, []string{"dir/z"}) {
			t.Fatalf("keys=%v, want only dir/z", keys)
		}
		if !reflect.DeepEqual(requests(), []string{"", "second", "third"}) {
			t.Fatalf("unexpected page requests: %v", requests())
		}
	})
	t.Run("filtered-final-page-stops", func(t *testing.T) {
		store, requests := filteredPageStore(t, map[string]storage.ListFilesRet{
			"": {Items: []storage.ListItem{{Key: "dir/a"}}},
		})
		objects, more, marker, err := store.List(context.Background(), "dir/", "dir/m", "", "", 10, false)
		if err != nil || len(objects) != 0 || more || marker != "" || len(requests()) != 1 {
			t.Fatalf("objects=%v more=%v marker=%q err=%v requests=%v", objects, more, marker, err, requests())
		}
	})
}
