//go:build go1.26

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

package object

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type connIDKey struct{}

func newConnIDServer(handler http.HandlerFunc) *httptest.Server {
	var nextID int64
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connIDKey{}, atomic.AddInt64(&nextID, 1))
	}
	server.Start()
	return server
}

func newHTTP2ConnIDServer(handler http.HandlerFunc) *httptest.Server {
	var nextID int64
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connIDKey{}, atomic.AddInt64(&nextID, 1))
	}
	server.StartTLS()
	return server
}

func TestHTTPConnMaxAgeTransportRetiresExpiredIdleConn(t *testing.T) {
	server := newConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Context().Value(connIDKey{}))
	})
	defer server.Close()

	client := &http.Client{
		Transport: newHTTPConnMaxAgeTransport(&http.Transport{}, 10*time.Millisecond),
	}

	first := getConnID(t, client, server.URL)
	time.Sleep(50 * time.Millisecond)
	second := getConnID(t, client, server.URL)

	if first == second {
		t.Fatalf("connection was reused after max age: first=%d second=%d", first, second)
	}
}

func TestHTTPConnMaxAgeTransportDoesNotCloseInFlightConn(t *testing.T) {
	releaseBody := make(chan struct{})
	server := newConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		id := r.Context().Value(connIDKey{})
		if r.URL.Path != "/hold" {
			fmt.Fprint(w, id)
			return
		}
		w.Header().Set("X-Conn-ID", fmt.Sprint(id))
		w.(http.Flusher).Flush()
		<-releaseBody
		fmt.Fprint(w, id)
	})
	defer server.Close()

	client := &http.Client{
		Transport: newHTTPConnMaxAgeTransport(&http.Transport{}, 10*time.Millisecond),
	}

	resp, err := client.Get(server.URL + "/hold")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer resp.Body.Close()
	firstID, err := strconv.ParseInt(resp.Header.Get("X-Conn-ID"), 10, 64)
	if err != nil {
		t.Fatalf("invalid first connection id: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	secondID := getConnID(t, client, server.URL)
	if firstID == secondID {
		t.Fatalf("expired in-flight connection accepted a new request: first=%d second=%d", firstID, secondID)
	}

	close(releaseBody)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expired in-flight response was interrupted: %v", err)
	}
	if string(body) != strconv.FormatInt(firstID, 10) {
		t.Fatalf("unexpected first response body: %q", body)
	}
}

func TestHTTPConnMaxAgeTransportDisabledReturnsBaseTransport(t *testing.T) {
	base := &http.Transport{}
	if got := newHTTPConnMaxAgeTransport(base, 0); got != base {
		t.Fatalf("disabled max age should return the base transport: got %T", got)
	}
}

func TestHTTPConnMaxAgeTransportRetiresExpiredHTTP2Conn(t *testing.T) {
	releaseBody := make(chan struct{})
	server := newHTTP2ConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		id := r.Context().Value(connIDKey{})
		if r.URL.Path != "/hold" {
			fmt.Fprint(w, id)
			return
		}
		w.Header().Set("X-Conn-ID", fmt.Sprint(id))
		w.(http.Flusher).Flush()
		<-releaseBody
		fmt.Fprint(w, id)
	})
	defer server.Close()

	base := server.Client().Transport.(*http.Transport).Clone()
	base.ForceAttemptHTTP2 = true
	client := &http.Client{
		Transport: newHTTPConnMaxAgeTransport(base, 10*time.Millisecond),
	}

	resp, err := client.Get(server.URL + "/hold")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("first request did not use HTTP/2: %s", resp.Proto)
	}
	firstID, err := strconv.ParseInt(resp.Header.Get("X-Conn-ID"), 10, 64)
	if err != nil {
		t.Fatalf("invalid first connection id: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	secondResp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.ProtoMajor != 2 {
		t.Fatalf("second request did not use HTTP/2: %s", secondResp.Proto)
	}
	body, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("read second response: %v", err)
	}
	secondID, err := strconv.ParseInt(string(body), 10, 64)
	if err != nil {
		t.Fatalf("parse second connection id %q: %v", body, err)
	}
	if firstID == secondID {
		t.Fatalf("expired HTTP/2 connection accepted a new request: first=%d second=%d", firstID, secondID)
	}

	close(releaseBody)
	firstBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expired HTTP/2 in-flight response was interrupted: %v", err)
	}
	if string(firstBody) != strconv.FormatInt(firstID, 10) {
		t.Fatalf("unexpected first response body: %q", firstBody)
	}
}

func TestHTTPConnMaxAgeTransportHTTP2ConcurrentMissUsesSingleConn(t *testing.T) {
	server := newHTTP2ConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, r.Context().Value(connIDKey{}))
	})
	defer server.Close()

	base := server.Client().Transport.(*http.Transport).Clone()
	base.ForceAttemptHTTP2 = true
	base.MaxIdleConnsPerHost = 1
	client := &http.Client{
		Transport: newHTTPConnMaxAgeTransport(base, time.Minute),
	}

	const n = 8
	start := make(chan struct{})
	ids := make(chan int64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := client.Get(server.URL)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if resp.ProtoMajor != 2 {
				errs <- fmt.Errorf("unexpected protocol %s", resp.Proto)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errs <- err
				return
			}
			id, err := strconv.ParseInt(string(body), 10, 64)
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent request failed: %v", err)
		}
	}

	var first int64
	for id := range ids {
		if first == 0 {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("concurrent HTTP/2 misses opened multiple connections: first=%d got=%d", first, id)
		}
	}
}

func TestHTTPConnMaxAgeTransportHTTP1BusyConnWaitsInsteadOfDialing(t *testing.T) {
	releaseBody := make(chan struct{})
	server := newConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		id := r.Context().Value(connIDKey{})
		if r.URL.Path != "/hold" {
			fmt.Fprint(w, id)
			return
		}
		w.Header().Set("X-Conn-ID", fmt.Sprint(id))
		w.(http.Flusher).Flush()
		<-releaseBody
		fmt.Fprint(w, id)
	})
	defer server.Close()

	base := &http.Transport{MaxIdleConnsPerHost: 1}
	client := &http.Client{
		Transport: newHTTPConnMaxAgeTransport(base, time.Minute),
	}

	resp, err := client.Get(server.URL + "/hold")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer resp.Body.Close()
	firstID, err := strconv.ParseInt(resp.Header.Get("X-Conn-ID"), 10, 64)
	if err != nil {
		t.Fatalf("invalid first connection id: %v", err)
	}

	result := make(chan int64, 1)
	errCh := make(chan error, 1)
	go func() {
		id, err := getConnIDWithError(client, server.URL)
		if err != nil {
			errCh <- err
			return
		}
		result <- id
	}()

	select {
	case err := <-errCh:
		t.Fatalf("second request failed early: %v", err)
	case id := <-result:
		t.Fatalf("second request completed before busy conn was released: second=%d first=%d", id, firstID)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseBody)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	if string(body) != strconv.FormatInt(firstID, 10) {
		t.Fatalf("unexpected first response body: %q", body)
	}

	select {
	case err := <-errCh:
		t.Fatalf("second request failed after release: %v", err)
	case id := <-result:
		if id != firstID {
			t.Fatalf("second request used a different connection instead of waiting: first=%d second=%d", firstID, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second request")
	}
}

func TestHTTPConnMaxAgeTransportSweepDoesNotSkipLiveConn(t *testing.T) {
	server := newConnIDServer(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Context().Value(connIDKey{}))
	})
	defer server.Close()

	base := &http.Transport{}
	rt := newHTTPConnMaxAgeTransport(base, time.Minute)
	transport, ok := rt.(*httpConnMaxAgeTransport)
	if !ok {
		t.Fatalf("unexpected transport type %T", rt)
	}

	reqURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	addr := canonicalHTTPAddr(reqURL)
	key := reqURL.Scheme + "://" + addr

	closedConn := newTestManagedConn(t, base, key, reqURL, 0)
	liveConn := newTestManagedConn(t, base, key, reqURL, 0)
	expiredConn := newTestManagedConn(t, base, key, reqURL, -time.Hour)
	if err := closedConn.cc.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}

	transport.buckets[key] = &connBucket{conns: []*managedHTTPConn{closedConn, liveConn, expiredConn}}
	got, err := transport.pickConn(newConnRequest(t, reqURL))
	if err != nil {
		t.Fatalf("pick conn: %v", err)
	}
	defer got.cc.Release()
	if got != liveConn {
		t.Fatalf("sweep skipped the only live connection")
	}
}

func getConnID(t *testing.T, client *http.Client, url string) int64 {
	t.Helper()
	id, err := getConnIDWithError(client, url)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return id
}

func getConnIDWithError(client *http.Client, target string) (int64, error) {
	resp, err := client.Get(target)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(string(body), 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func newTestManagedConn(t *testing.T, base *http.Transport, key string, reqURL *url.URL, age time.Duration) *managedHTTPConn {
	t.Helper()
	cc, err := base.NewClientConn(context.Background(), reqURL.Scheme, canonicalHTTPAddr(reqURL))
	if err != nil {
		t.Fatalf("new client conn: %v", err)
	}
	return &managedHTTPConn{
		key:       key,
		cc:        cc,
		createdAt: time.Now().Add(age),
		maxAge:    time.Millisecond,
	}
}

func newConnRequest(t *testing.T, reqURL *url.URL) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}
