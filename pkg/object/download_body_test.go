//go:build !noqiniu && !nos3 && !nodragonfly
// +build !noqiniu,!nos3,!nodragonfly

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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qiniu/go-sdk/v7/auth"
)

func TestDownloadResponseBodyOwnership(t *testing.T) {
	for _, backend := range []string{"qiniu", "dragonfly"} {
		t.Run(backend, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusPartialContent, http.StatusNotFound,
				http.StatusRequestedRangeNotSatisfiable, http.StatusInternalServerError} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					stopped := make(chan struct{})
					release := make(chan struct{})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						_, _ = w.Write([]byte("data"))
						w.(http.Flusher).Flush()
						select {
						case <-r.Context().Done():
							close(stopped)
						case <-release:
						}
					}))
					defer server.Close()
					defer close(release)
					var store ObjectStorage
					if backend == "qiniu" {
						oldClient := httpClient
						httpClient = server.Client()
						t.Cleanup(func() { httpClient = oldClient })
						t.Setenv("QINIU_DOMAIN", server.URL)
						store = &qiniu{cred: auth.New("access-key", "secret-key")}
					} else {
						store = &dragonfly{endpoint: server.URL, bucket: "bucket", client: server.Client()}
					}
					body, err := store.Get(context.Background(), "/object", 0, 4)
					if status == http.StatusOK || status == http.StatusPartialContent {
						if err != nil {
							t.Fatal(err)
						}
						defer body.Close()
						data := make([]byte, 4)
						if _, err = io.ReadFull(body, data); err != nil || string(data) != "data" {
							t.Fatalf("successful response body is not readable: %q, %v", data, err)
						}
						select {
						case <-stopped:
							t.Fatal("successful response body closed before the caller closed it")
						default:
						}
						if err = body.Close(); err != nil {
							t.Fatal(err)
						}
					} else if err == nil || body != nil {
						if body != nil {
							_ = body.Close()
						}
						t.Fatalf("expected rejected status %d with no body, got %v", status, err)
					}
					select {
					case <-stopped:
					case <-time.After(time.Second):
						t.Fatal("response body was not closed; server request is still running")
					}
				})
			}
		})
	}
}
