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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qiniu/go-sdk/v7/auth"
	qiniuclient "github.com/qiniu/go-sdk/v7/client"
	"github.com/qiniu/go-sdk/v7/storage"
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
	cfg := &storage.Config{Zone: &storage.Zone{RsHost: server.URL}}
	client := server.Client()
	client.Timeout = 2 * time.Second
	return &qiniu{
		s3client: s3client{bucket: "bucket"},
		bm: storage.NewBucketManagerEx(auth.New("access-key", "secret-key"), cfg,
			&qiniuclient.Client{Client: client}),
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
	default:
		panic("unexpected Qiniu operation")
	}
}

func TestQiniuObjectOperations_Context(t *testing.T) {
	for _, operation := range []string{"stat", "copy", "delete"} {
		for _, mode := range []string{"pre-canceled", "in-flight", "expired", "deadline"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				started := make(chan struct{}, 1)
				stopped := make(chan struct{}, 1)
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					select {
					case started <- struct{}{}:
					default:
					}
					select {
					case <-r.Context().Done():
						stopped <- struct{}{}
						return
					case <-release:
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Reqid", "local-context-test")
					_, _ = w.Write([]byte(`{"fsize":7,"putTime":10000000}`))
				}))
				defer server.Close()
				defer close(release)
				ctx, cancel := context.WithCancel(context.Background())
				wantErr := context.Canceled
				if mode == "pre-canceled" {
					cancel()
				} else if mode == "expired" || mode == "deadline" {
					cancel()
					timeout := 100 * time.Millisecond
					if mode == "expired" {
						timeout = -time.Second
					}
					ctx, cancel = context.WithTimeout(context.Background(), timeout)
					wantErr = context.DeadlineExceeded
				}
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- qiniuContextOperation(qiniuContextStore(server), ctx, operation) }()
				if mode == "in-flight" || mode == "deadline" {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("Qiniu request did not start")
					}
					if mode == "in-flight" {
						cancel()
					}
				}
				select {
				case err := <-result:
					if !errors.Is(err, wantErr) {
						t.Fatalf("expected %v, got %v", wantErr, err)
					}
				case <-time.After(time.Second):
					t.Fatal("Qiniu request ignored caller context")
				}
				if mode == "in-flight" || mode == "deadline" {
					select {
					case <-stopped:
					case <-time.After(time.Second):
						t.Fatal("underlying HTTP request remained active")
					}
				} else {
					select {
					case <-started:
						t.Fatal("request started with an already finished context")
					default:
					}
				}
			})
		}
	}
}

func TestQiniuObjectOperations_Response(t *testing.T) {
	for _, operation := range []string{"stat", "copy", "delete"} {
		for _, status := range []int{http.StatusOK, 612, http.StatusForbidden} {
			t.Run(fmt.Sprintf("%s/%d", operation, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantMethod := http.MethodPost
					wantPath := "/" + operation + "/" + storage.EncodedEntry("bucket", "key")
					if operation == "stat" {
						wantMethod = http.MethodGet
					} else if operation == "copy" {
						wantPath += "/" + storage.EncodedEntry("bucket", "destination") + "/force/true"
					}
					if r.Method != wantMethod || r.URL.Path != wantPath {
						t.Errorf("request: got %s %s, want %s %s", r.Method, r.URL.Path, wantMethod, wantPath)
					}
					if !strings.HasPrefix(r.Header.Get("Authorization"), "Qiniu access-key:") {
						t.Errorf("missing Qiniu authorization: %q", r.Header.Get("Authorization"))
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Reqid", "local-context-test")
					w.WriteHeader(status)
					body := `{"fsize":7,"putTime":10000000}`
					if status == 612 {
						body = `{"error":"no such file or directory"}`
					} else if status == http.StatusForbidden {
						body = `{"error":"permission denied"}`
					}
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				store := qiniuContextStore(server)
				err := qiniuContextOperation(store, context.Background(), operation)
				if status == http.StatusOK || (status == 612 && operation == "delete") {
					if err != nil {
						t.Fatal(err)
					}
				} else if status == 612 && operation == "stat" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("expected os.ErrNotExist, got %v", err)
					}
				} else if err == nil {
					t.Fatal("expected storage error")
				}
				if status == http.StatusOK && operation == "stat" {
					obj, err := store.Head(context.Background(), "key")
					if err != nil || obj.Key() != "key" || obj.Size() != 7 || !obj.Mtime().Equal(time.Unix(1, 0)) || obj.IsDir() {
						t.Fatalf("unexpected object: %+v, %v", obj, err)
					}
				}
			})
		}
	}
}
