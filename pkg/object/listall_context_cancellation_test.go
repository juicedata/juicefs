//go:build !nos3

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
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// The real SDK handles every request. Only test teardown bypasses it so a
// failing regression can join the old infinite-retry producer safely.
type stoppableS3Listing struct {
	*s3client
	released atomic.Bool
	calls    atomic.Int32
	errors   chan struct{}
}

func (s *stoppableS3Listing) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	s.calls.Add(1)
	if s.released.Load() {
		return nil, false, "", nil
	}
	objects, more, next, err := s.s3client.List(ctx, prefix, marker, token, delimiter, limit, followLink)
	if err != nil && s.errors != nil {
		select {
		case s.errors <- struct{}{}:
		default:
		}
	}
	return objects, more, next, err
}

func nativeS3Listing(server *httptest.Server) *s3client {
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:  server.Client(),
		Retryer:     func() aws.Retryer { return aws.NopRetryer{} },
	}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(server.URL)
		options.UsePathStyle = true
	})
	return &s3client{bucket: "bucket", s3: client}
}

func writeNativeListPage(w http.ResponseWriter, key, token string) {
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bucket</Name><IsTruncated>%t</IsTruncated><NextContinuationToken>%s</NextContinuationToken><Contents><Key>%s</Key><LastModified>2026-01-01T00:00:00Z</LastModified><Size>1</Size></Contents></ListBucketResult>`, token != "", token, key)
}

func TestListAllNativeS3StopsCanceledRetry(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "backoff"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			var once sync.Once
			var stopOnce sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("continuation-token") == "next" {
					once.Do(func() { close(started) })
					if mode == "backoff" {
						w.Header().Set("Content-Type", "application/xml")
						w.WriteHeader(http.StatusServiceUnavailable)
						fmt.Fprint(w, "<Error><Code>SlowDown</Code><Message>retry delay</Message></Error>")
						stopOnce.Do(func() { close(stopped) })
						return
					}
					<-r.Context().Done()
					stopOnce.Do(func() { close(stopped) })
					return
				}
				writeNativeListPage(w, "a", "next")
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
			}
			defer cancel()
			store := &stoppableS3Listing{s3client: nativeS3Listing(server), errors: make(chan struct{}, 1)}
			out, err := ListAll(ctx, store, "", "", false, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				store.released.Store(true)
				cancel()
				deadline := time.After(2 * time.Second)
				for {
					select {
					case _, ok := <-out:
						if !ok {
							return
						}
					case <-deadline:
						t.Error("test teardown could not join listing producer")
						return
					}
				}
			}()
			if first := <-out; first == nil || first.Key() != "a" {
				t.Fatal("first real SDK page was not delivered")
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("next-page request did not reach server")
			}
			if mode == "backoff" {
				select {
				case <-store.errors:
					// Let the producer enter its normal retry delay.
					time.Sleep(25 * time.Millisecond)
				case <-time.After(time.Second):
					t.Fatal("real SDK did not return the transient error")
				}
			}
			if mode != "deadline" {
				cancel()
			}
			<-ctx.Done()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("SDK request did not stop after cancellation")
			}
			deadline := time.After(350 * time.Millisecond)
			failed := false
			for {
				select {
				case item, ok := <-out:
					if !ok {
						if !failed {
							t.Error("canceled enumeration closed without the failure sentinel")
						}
						return
					}
					if item != nil {
						t.Fatal("canceled next page produced an object")
					}
					failed = true
				case <-deadline:
					t.Fatalf("SDK request stopped but ListAll keeps retrying: %d List calls", store.calls.Load())
				}
			}
		})
	}
}

func TestListAllNativeS3RetriesTransientError(t *testing.T) {
	var nextCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("continuation-token") != "next" {
			writeNativeListPage(w, "a", "next")
			return
		}
		if nextCalls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "<Error><Code>SlowDown</Code><Message>transient test failure</Message></Error>")
			return
		}
		writeNativeListPage(w, "b", "")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := ListAll(ctx, nativeS3Listing(server), "", "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for item := range out {
		if item == nil {
			t.Fatal("transient retry produced an error sentinel")
		}
		keys = append(keys, item.Key())
	}
	if fmt.Sprint(keys) != "[a b]" || nextCalls.Load() != 2 {
		t.Fatalf("keys %v, next-page requests %d", keys, nextCalls.Load())
	}
}
