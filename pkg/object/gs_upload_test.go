//go:build !nogs

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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type gsUploadErrorReader struct{ err error }

func (r gsUploadErrorReader) Read([]byte) (int, error) { return 0, r.err }

func gsWriterMonitors() int {
	var stack bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&stack, 2)
	return strings.Count(stack.String(), "cloud.google.com/go/storage.(*Writer).monitorCancel(")
}

func TestGSPutReleasesWriter(t *testing.T) {
	for _, test := range []struct {
		name string
		size int
		fail bool
	}{
		{"source_error_buffered", 1024, true},
		{"source_error_after_chunk", 5<<20 + 1, true},
		{"empty", 0, false},
		{"small", 1024, false},
		{"multiple_chunks", 5<<20 + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var commits atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("uploadType") == "resumable" {
					w.Header().Set("Location", server.URL+"/upload/session")
					w.WriteHeader(http.StatusOK)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				if strings.HasSuffix(r.Header.Get("Content-Range"), "/*") {
					w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(body)-1))
					w.Header().Set("X-Http-Status-Code-Override", "308")
					w.WriteHeader(http.StatusOK)
					return
				}
				commits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"name":"key","bucket":"bucket","size":"0"}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := storage.NewClient(ctx, option.WithEndpoint(server.URL), option.WithHTTPClient(server.Client()), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.SetRetry(storage.WithPolicy(storage.RetryNever))
			before := gsWriterMonitors()
			defer func() {
				cancel()
				deadline := time.Now().Add(time.Second)
				for gsWriterMonitors() > before && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if n := gsWriterMonitors(); n > before {
					t.Errorf("cleanup left %d SDK writer monitors alive", n-before)
				}
			}()
			payload := bytes.Repeat([]byte("x"), test.size)
			var source io.Reader = bytes.NewReader(payload)
			readErr := errors.New("source failed")
			if test.fail {
				source = io.MultiReader(source, gsUploadErrorReader{readErr})
			}
			g := &gs{clients: []*storage.Client{client}, bucket: "bucket"}
			err = g.Put(ctx, "key", source)
			if test.fail && !errors.Is(err, readErr) || !test.fail && err != nil {
				t.Fatalf("Put returned %v (source failure=%t)", err, test.fail)
			}
			if ctx.Err() != nil {
				t.Fatalf("Put canceled its caller: %v", ctx.Err())
			}
			deadline := time.Now().Add(time.Second)
			for gsWriterMonitors() > before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if n := gsWriterMonitors(); n > before {
				t.Errorf("Put left %d SDK writer monitors alive after return", n-before)
			}
			if test.fail && commits.Load() != 0 || !test.fail && commits.Load() != 1 {
				t.Errorf("commits=%d, source failure=%t", commits.Load(), test.fail)
			}
		})
	}
}
