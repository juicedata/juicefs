//go:build !nobos
// +build !nobos

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

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos"
)

func TestBOSGet_ContextCanceled(t *testing.T) {
	tests := []struct {
		name      string
		off       int64
		limit     int64
		wantRange string
	}{
		{name: "full", limit: -1},
		{name: "offset", off: 3, limit: -1, wantRange: "bytes=3-"},
		{name: "range", off: 3, limit: 2, wantRange: "bytes=3-4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- r.Header.Get("Range")
				<-r.Context().Done()
			}))
			defer server.Close()

			client, err := bos.NewClient("access-key", "secret-key", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.Config.Retry = bce.NewNoRetryPolicy()
			store := &bosclient{bucket: "bucket", c: client}

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				body, err := store.Get(ctx, "object", test.off, test.limit)
				if body != nil {
					_ = body.Close()
				}
				result <- err
			}()

			select {
			case gotRange := <-started:
				if gotRange != test.wantRange {
					t.Errorf("Range header: got %q, want %q", gotRange, test.wantRange)
				}
			case <-time.After(time.Second):
				t.Fatal("BOS Get did not start")
			}
			cancel()

			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("BOS Get did not stop after context cancellation")
			}
		})
	}
}
