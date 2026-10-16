//go:build !noqiniu && !nos3
// +build !noqiniu,!nos3

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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qiniu/go-sdk/v7/auth"
	qiniuclient "github.com/qiniu/go-sdk/v7/client"
	"github.com/qiniu/go-sdk/v7/storage"
	"github.com/qiniu/go-sdk/v7/storagev2/apis"
	"github.com/qiniu/go-sdk/v7/storagev2/http_client"
)

func TestQiniuPrivateGet_ContextCanceled(t *testing.T) {
	started := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.Header.Get("Range")
		<-r.Context().Done()
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	t.Cleanup(func() { httpClient = oldClient })
	t.Setenv("QINIU_DOMAIN", server.URL)

	store := &qiniu{cred: auth.New("access-key", "secret-key")}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		body, err := store.Get(ctx, "/object", 3, 2)
		if body != nil {
			_ = body.Close()
		}
		result <- err
	}()

	select {
	case gotRange := <-started:
		if gotRange != "bytes=3-4" {
			t.Errorf("Range header: got %q, want %q", gotRange, "bytes=3-4")
		}
	case <-time.After(time.Second):
		t.Fatal("Qiniu Get did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Qiniu Get did not stop after context cancellation")
	}
}

func qiniuContextStore(server *httptest.Server) *qiniu {
	cfg := &storage.Config{Zone: &storage.Zone{RsHost: server.URL}, RsfHost: server.URL}
	client := server.Client()
	client.Timeout = 2 * time.Second
	cred := auth.New("access-key", "secret-key")
	return &qiniu{
		s3client: s3client{bucket: "bucket"},
		bm:       storage.NewBucketManagerEx(cred, cfg, &qiniuclient.Client{Client: client}),
		api: apis.NewStorage(&http_client.Options{
			BasicHTTPClient: client, Credentials: cred, Regions: cfg.GetRegion(),
			UseInsecureProtocol: true,
			HostRetryConfig:     &http_client.RetryConfig{}, HostsRetryConfig: &http_client.RetryConfig{},
		}),
	}
}

func qiniuContextOperation(store *qiniu, ctx context.Context, operation string) error {
	switch operation {
	case "stat":
		_, err := store.Head(ctx, "key")
		return err
	case "copy":
		return store.Copy(ctx, "destination", "key")
	case "delete":
		return store.Delete(ctx, "key")
	case "list":
		_, _, _, err := store.List(ctx, "", "", "", "", 10, false)
		return err
	default:
		panic("unexpected Qiniu operation")
	}
}

func TestQiniuObjectOperations_Context(t *testing.T) {
	for _, operation := range []string{"stat", "copy", "delete", "list"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				started := make(chan struct{}, 1)
				stopped := make(chan struct{}, 1)
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					started <- struct{}{}
					select {
					case <-r.Context().Done():
						stopped <- struct{}{}
					case <-release:
					}
				}))
				defer server.Close()
				defer close(release)
				ctx, cancel := context.WithCancel(context.Background())
				want := context.Canceled
				if mode == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
					want = context.DeadlineExceeded
				}
				defer cancel()
				store := qiniuContextStore(server)
				result := make(chan error, 1)
				go func() { result <- qiniuContextOperation(store, ctx, operation) }()
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("Qiniu request did not start")
				}
				if mode == "cancel" {
					cancel()
				}
				select {
				case err := <-result:
					if !errors.Is(err, want) {
						t.Fatalf("expected %v, got %v", want, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("Qiniu request ignored caller context")
				}
				select {
				case <-stopped:
				case <-time.After(2 * time.Second):
					t.Fatal("underlying HTTP request remained active")
				}
			})
		}
	}
}

func TestQiniuList_OptionsAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodPost || r.URL.Path != "/list" || q.Get("bucket") != "bucket" ||
			q.Get("prefix") != "dir/" || q.Get("delimiter") != "/" ||
			q.Get("marker") != "previous" || q.Get("limit") != "1000" {
			t.Errorf("unexpected List request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(storage.ListFilesRet{
			Marker: "next",
			Items: []storage.ListItem{
				{Key: "dir/a", Fsize: 1, PutTime: 10000000},
				{Key: "dir/z", Fsize: 7, PutTime: 20000000},
			},
			CommonPrefixes: []string{"dir/m/"},
		})
	}))
	defer server.Close()
	store := &qiniu{
		s3client: s3client{bucket: "bucket"},
		bm: storage.NewBucketManagerEx(auth.New("access-key", "secret-key"),
			&storage.Config{RsfHost: server.URL}, &qiniuclient.Client{Client: server.Client()}),
	}
	objects, more, marker, err := store.List(context.Background(), "dir/", "dir/a", "previous", "/", 1001, false)
	if err != nil {
		t.Fatal(err)
	}
	if !more || marker != "next" || len(objects) != 2 {
		t.Fatalf("unexpected page: %v, %v, %q", objects, more, marker)
	}
	if objects[0].Key() != "dir/m/" || !objects[0].IsDir() || objects[1].Key() != "dir/z" ||
		objects[1].Size() != 7 || objects[1].Mtime().Unix() != 2 {
		t.Fatalf("unexpected List metadata: %v", objects)
	}
}
