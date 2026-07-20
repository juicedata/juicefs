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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeHTTPConn struct {
	closed     int32
	readReturn int
	readErr    error
}

func (f *fakeHTTPConn) Read(b []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	n := f.readReturn
	if n > len(b) {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		b[i] = 'r'
	}
	return n, nil
}

func (f *fakeHTTPConn) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeHTTPConn) Close() error {
	atomic.StoreInt32(&f.closed, 1)
	return nil
}
func (f *fakeHTTPConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (f *fakeHTTPConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (f *fakeHTTPConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeHTTPConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeHTTPConn) SetWriteDeadline(time.Time) error { return nil }

func driveHTTPRequest(t *testing.T, c net.Conn, withBody bool) error {
	t.Helper()
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\n\r\n")); err != nil {
		return err
	}
	if withBody {
		if _, err := c.Write([]byte("body")); err != nil {
			return err
		}
	}
	_, err := c.Read(make([]byte, 16))
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	return nil
}

func TestRetiringConnDisabledWhenMaxZero(t *testing.T) {
	f := &fakeHTTPConn{readReturn: 4}
	if got := newRetiringConn(f, 0); got != f {
		t.Fatalf("max=0 should return raw conn, got %T", got)
	}
	if got := newRetiringConn(f, -1); got != f {
		t.Fatalf("max<0 should return raw conn, got %T", got)
	}
}

func TestRetiringConnJitterProducesVariation(t *testing.T) {
	const max = 1000
	seen := make(map[int32]struct{})
	for i := 0; i < 50; i++ {
		c := newRetiringConn(&fakeHTTPConn{}, max).(*retiringConn)
		seen[c.maxRequests] = struct{}{}
		if c.maxRequests < int32(max*3/4) || c.maxRequests >= int32(max*5/4) {
			t.Fatalf("jittered threshold %d out of range", c.maxRequests)
		}
	}
	if len(seen) < 5 {
		t.Fatalf("expected jitter spread, got %d distinct values", len(seen))
	}
}

func TestRetiringConnFallback(t *testing.T) {
	f := &fakeHTTPConn{readReturn: 4}
	allowed := 1 + 3 + 1 + 1 + gracePeriodAfterMark
	c := newRetiringConn(f, 3)
	for i := 0; i < allowed; i++ {
		if err := driveHTTPRequest(t, c, true); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := driveHTTPRequest(t, c, false); !errors.Is(err, errConnRetired) {
		t.Fatalf("request %d should retire connection, got %v", allowed+1, err)
	}
	if atomic.LoadInt32(&f.closed) != 1 {
		t.Fatal("retired connection was not closed")
	}
}

func TestRotatingRoundTripperInjectsConnectionClose(t *testing.T) {
	var seenClose bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenClose = seenClose || strings.EqualFold(r.Header.Get("Connection"), "close")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := &http.Transport{
		MaxIdleConnsPerHost: 1,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return newRetiringConn(conn, 2), nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: &rotatingRoundTripper{inner: transport}, Timeout: 5 * time.Second}

	for i := 0; i < 8; i++ {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if !seenClose {
		t.Fatal("expected a request with Connection: close")
	}
}

func TestEnvIntDefault(t *testing.T) {
	const key = "JFS_TEST_ENV_INT_DEFAULT"
	t.Setenv(key, "")
	if got := envIntDefault(key, 7); got != 7 {
		t.Fatalf("empty env: got %d", got)
	}
	t.Setenv(key, "42")
	if got := envIntDefault(key, 7); got != 42 {
		t.Fatalf("valid env: got %d", got)
	}
	t.Setenv(key, "invalid")
	if got := envIntDefault(key, 7); got != 7 {
		t.Fatalf("invalid env: got %d", got)
	}
	t.Setenv(key, "-1")
	if got := envIntDefault(key, 7); got != 7 {
		t.Fatalf("negative env: got %d", got)
	}
}

func TestConfigureHTTPTransportPreservesDialContext(t *testing.T) {
	t.Setenv("JFS_HTTP_MAX_REQUESTS_PER_CONN", "10")
	t.Setenv("JFS_HTTP_MAX_IDLE_CONNS_PER_HOST", "23")
	fakeConn := &fakeHTTPConn{}
	var calls int32
	transport := &http.Transport{
		MaxIdleConnsPerHost: 500,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			atomic.AddInt32(&calls, 1)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return fakeConn, nil
		},
	}
	roundTripper := configureHTTPTransport(transport)
	if _, ok := roundTripper.(*rotatingRoundTripper); !ok {
		t.Fatalf("transport type = %T", roundTripper)
	}
	if transport.MaxIdleConnsPerHost != 23 {
		t.Fatalf("MaxIdleConnsPerHost = %d", transport.MaxIdleConnsPerHost)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transport.DialContext(ctx, "tcp", "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled DialContext returned %v", err)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "unused")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conn.(*retiringConn); !ok {
		t.Fatalf("connection type = %T", conn)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("DialContext calls = %d", calls)
	}
}

func TestGetHttpTransportReturnsConcreteTransport(t *testing.T) {
	if GetHttpTransport() == nil {
		t.Fatal("GetHttpTransport returned nil")
	}
	if GetHttpTransport().TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
}
