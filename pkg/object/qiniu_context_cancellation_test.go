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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qiniu/go-sdk/v7/auth"
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
