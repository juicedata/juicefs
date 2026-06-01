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

// fakeConn implements net.Conn over an in-memory script of read/write
// operations. Tests drive it by calling Read/Write on the wrapper; the
// underlying counters reveal what made it through.
type fakeConn struct {
	reads      int32
	writes     int32
	closed     int32
	readReturn int // bytes to claim were read each call
	readErr    error
}

func (f *fakeConn) Read(b []byte) (int, error) {
	atomic.AddInt32(&f.reads, 1)
	if f.readErr != nil {
		return 0, f.readErr
	}
	n := f.readReturn
	if n > len(b) {
		n = len(b)
	}
	if n == 0 {
		return 0, nil
	}
	for i := 0; i < n; i++ {
		b[i] = 'r'
	}
	return n, nil
}

func (f *fakeConn) Write(b []byte) (int, error) {
	atomic.AddInt32(&f.writes, 1)
	return len(b), nil
}

func (f *fakeConn) Close() error {
	atomic.StoreInt32(&f.closed, 1)
	return nil
}

func (f *fakeConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

// driveRequest simulates one HTTP request-response cycle on top of the
// retiringConn: the caller writes the request bytes (one Write of headers,
// one optional Write of body), then reads the response bytes.
func driveRequest(t *testing.T, c net.Conn, withBody bool) (writeErr error) {
	t.Helper()
	if _, writeErr = c.Write([]byte("GET / HTTP/1.1\r\n\r\n")); writeErr != nil {
		return writeErr
	}
	if withBody {
		if _, err := c.Write([]byte("body")); err != nil {
			return err
		}
	}
	buf := make([]byte, 16)
	if _, err := c.Read(buf); err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	return nil
}

func TestRetiringConnDisabledWhenMaxZero(t *testing.T) {
	f := &fakeConn{readReturn: 4}
	if got := newRetiringConn(f, 0); got != f {
		t.Fatalf("max=0 should return raw conn, got wrapper")
	}
	if got := newRetiringConn(f, -1); got != f {
		t.Fatalf("max<0 should return raw conn, got wrapper")
	}
}

func TestRetiringConnJitterProducesVariation(t *testing.T) {
	// With a meaningful max, repeated wrapping should produce a spread of
	// thresholds in roughly [0.75*max, 1.25*max). This breaks deterministic
	// herd retirement when many conns are dialed together.
	const max = 1000
	seen := make(map[int32]struct{})
	minSeen, maxSeen := int32(max), int32(max)
	for i := 0; i < 50; i++ {
		c := newRetiringConn(&fakeConn{}, max).(*retiringConn)
		seen[c.maxRequests] = struct{}{}
		if c.maxRequests < minSeen {
			minSeen = c.maxRequests
		}
		if c.maxRequests > maxSeen {
			maxSeen = c.maxRequests
		}
		if c.maxRequests < int32(max*3/4) || c.maxRequests >= int32(max*5/4) {
			t.Fatalf("jittered threshold %d out of [0.75*%d, 1.25*%d)", c.maxRequests, max, max)
		}
	}
	if len(seen) < 5 {
		t.Fatalf("expected jitter spread, only saw %d distinct values out of 50 (range %d..%d)", len(seen), minSeen, maxSeen)
	}
}

func TestRetiringConnAllowsExactlyMaxRequests(t *testing.T) {
	f := &fakeConn{readReturn: 4}
	// Allowed budget: 1 (first req has no boundary) + max + 1 (count crosses cap)
	// + 1 (mark) + gracePeriodAfterMark.
	allowed := 1 + 3 + 1 + 1 + gracePeriodAfterMark
	c := newRetiringConn(f, 3)
	for i := 0; i < allowed; i++ {
		if err := driveRequest(t, c, false); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if atomic.LoadInt32(&f.closed) != 0 {
		t.Fatalf("conn must stay open inside the grace window")
	}
	if err := driveRequest(t, c, false); !errors.Is(err, errConnRetired) {
		t.Fatalf("request %d should fall back to hard close, got %v", allowed+1, err)
	}
	if atomic.LoadInt32(&f.closed) != 1 {
		t.Fatalf("conn should be closed once fallback fires")
	}
}

func TestRetiringConnIgnoresMidRequestWrites(t *testing.T) {
	// Multi-Write request body must not be counted as multiple boundaries.
	f := &fakeConn{readReturn: 4}
	allowed := 1 + 2 + 1 + 1 + gracePeriodAfterMark
	c := newRetiringConn(f, 2)
	for i := 0; i < allowed; i++ {
		if err := driveRequest(t, c, true); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := driveRequest(t, c, true); !errors.Is(err, errConnRetired) {
		t.Fatalf("request %d should fall back to hard close, got %v", allowed+1, err)
	}
}

func TestRetiringConnDoesNotFailInFlightCaller(t *testing.T) {
	// The Write that crosses the cap must not surface an error to the
	// caller; closure is deferred to a later boundary.
	f := &fakeConn{readReturn: 4}
	c := newRetiringConn(f, 1)
	for i := 0; i < 3; i++ {
		if err := driveRequest(t, c, false); err != nil {
			t.Fatalf("request %d should succeed (deferred close): %v", i+1, err)
		}
	}
	if atomic.LoadInt32(&f.closed) != 0 {
		t.Fatalf("conn must stay open until the next boundary")
	}
}

func TestRetiringConnZeroByteReadDoesNotMarkBoundary(t *testing.T) {
	// (0, nil) Read must not flip lastWasRead, otherwise idle keep-alive
	// polls would be miscounted as response boundaries.
	f := &fakeConn{readReturn: 0}
	c := newRetiringConn(f, 1)
	buf := make([]byte, 4)
	for i := 0; i < 3; i++ {
		if _, err := c.Read(buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if _, err := c.Write([]byte("x")); err != nil {
			t.Fatalf("write %d should not retire: %v", i, err)
		}
	}
}

func TestRetiringConnReadErrorPassesThrough(t *testing.T) {
	want := errors.New("network down")
	f := &fakeConn{readErr: want}
	c := newRetiringConn(f, 5)
	if _, err := c.Read(make([]byte, 4)); !errors.Is(err, want) {
		t.Fatalf("expected read error %v, got %v", want, err)
	}
}

func TestRetiringConnIsRetiredReflectsState(t *testing.T) {
	// IsRetired starts false and flips to true once the cap is crossed.
	c := newRetiringConn(&fakeConn{readReturn: 4}, 2).(*retiringConn)
	if c.IsRetired() {
		t.Fatalf("fresh conn should not be retired")
	}
	// max=2: req1 no boundary, req2/req3 increment, req4 (Load=2,!>2) count→3, req5 (Load=3,>2) MARKS.
	for i := 0; i < 5; i++ {
		if err := driveRequest(t, c, false); err != nil {
			t.Fatalf("request %d should not fail before grace expiry: %v", i+1, err)
		}
	}
	if !c.IsRetired() {
		t.Fatalf("after crossing cap, IsRetired must be true")
	}
}

func TestRetiringConnFriendlyPathDoesNotFallback(t *testing.T) {
	// Inside the grace window after marking, the conn must stay open — the
	// outer RoundTripper is expected to clean up via Connection: close.
	f := &fakeConn{readReturn: 4}
	c := newRetiringConn(f, 2).(*retiringConn)
	allowed := 1 + 2 + 1 + 1 + 1 // mark + 1 grace boundary, well inside gracePeriodAfterMark
	for i := 0; i < allowed; i++ {
		if err := driveRequest(t, c, false); err != nil {
			t.Fatalf("request %d inside grace window: %v", i+1, err)
		}
	}
	if !c.IsRetired() {
		t.Fatalf("conn should be marked retired by now")
	}
	if atomic.LoadInt32(&f.closed) != 0 {
		t.Fatalf("conn must not be closed inside grace window")
	}
}

func TestRotatingRoundTripperInjectsConnectionCloseOnRetired(t *testing.T) {
	// End-to-end: real httptest server. After the cap is crossed, the next
	// request through that conn must carry Connection: close so the server
	// drops the conn cleanly.
	const maxReqs = 2

	var seenConnHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenConnHeaders = append(seenConnHeaders, r.Header.Get("Connection"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tp := &http.Transport{
		MaxIdleConnsPerHost: 1,
		Dial: func(network, address string) (net.Conn, error) {
			conn, err := net.Dial(network, address)
			if err != nil {
				return nil, err
			}
			return newRetiringConn(conn, maxReqs), nil
		},
	}
	defer tp.CloseIdleConnections()

	rt := &rotatingRoundTripper{inner: tp}
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	for i := 0; i < 8; i++ {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	var sawClose bool
	for _, h := range seenConnHeaders {
		if strings.EqualFold(h, "close") {
			sawClose = true
			break
		}
	}
	if !sawClose {
		t.Fatalf("expected at least one request to carry Connection: close, headers seen: %v", seenConnHeaders)
	}
}

func TestRotatingRoundTripperLeavesNonRetiredConnsAlone(t *testing.T) {
	// Conns that aren't *retiringConn (or aren't retired) must not get
	// their Connection header rewritten.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Connection"); got != "" && !strings.EqualFold(got, "keep-alive") {
			t.Errorf("unexpected Connection header on healthy conn: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tp := &http.Transport{}
	defer tp.CloseIdleConnections()
	rt := &rotatingRoundTripper{inner: tp}
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	for i := 0; i < 3; i++ {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func TestRotatingRoundTripperPreservesUserConnectionHeader(t *testing.T) {
	// User-set Connection header must survive when the conn is not retired.
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Connection")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tp := &http.Transport{}
	defer tp.CloseIdleConnections()
	rt := &rotatingRoundTripper{inner: tp}
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Connection", "keep-alive")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if !strings.EqualFold(seen, "keep-alive") {
		t.Fatalf("user-set Connection header was lost: got %q", seen)
	}
}

func TestEnvIntDefault(t *testing.T) {
	const k = "JFS_TEST_ENV_INT_DEFAULT"
	t.Setenv(k, "")
	if got := envIntDefault(k, 7); got != 7 {
		t.Fatalf("empty env: want 7, got %d", got)
	}
	t.Setenv(k, "42")
	if got := envIntDefault(k, 7); got != 42 {
		t.Fatalf("set env: want 42, got %d", got)
	}
	t.Setenv(k, "not-a-number")
	if got := envIntDefault(k, 7); got != 7 {
		t.Fatalf("invalid env: want fallback 7, got %d", got)
	}
	t.Setenv(k, "-3")
	if got := envIntDefault(k, 7); got != 7 {
		t.Fatalf("negative env: want fallback 7, got %d", got)
	}
}

// BenchmarkRetiringConnRead measures the per-Read overhead of the wrapper:
// one atomic.StoreInt32 vs the raw Conn.Read.
func BenchmarkRetiringConnRead(b *testing.B) {
	buf := make([]byte, 4096)
	b.Run("raw", func(b *testing.B) {
		c := &fakeConn{readReturn: 4096}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = c.Read(buf)
		}
	})
	b.Run("wrapped", func(b *testing.B) {
		c := newRetiringConn(&fakeConn{readReturn: 4096}, 1000)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = c.Read(buf)
		}
	})
}

// BenchmarkRetiringConnWriteBoundary measures the per-Write overhead at a
// request boundary (worst case: SwapInt32 returns 1, Load + Add fire).
func BenchmarkRetiringConnWriteBoundary(b *testing.B) {
	buf := []byte("GET / HTTP/1.1\r\n\r\n")
	b.Run("raw", func(b *testing.B) {
		c := &fakeConn{}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = c.Write(buf)
		}
	})
	b.Run("wrapped-boundary-each-write", func(b *testing.B) {
		// Force every Write to be a boundary by toggling lastWasRead via Read
		// between Writes. Measures the boundary-hit hot path.
		c := newRetiringConn(&fakeConn{readReturn: 1}, 1<<30).(*retiringConn)
		small := make([]byte, 1)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = c.Read(small)
			_, _ = c.Write(buf)
		}
	})
	b.Run("wrapped-no-boundary", func(b *testing.B) {
		// Only the first Write is a boundary (no prior Read). Subsequent ones
		// see lastWasRead==0 and skip the increment branch entirely.
		c := newRetiringConn(&fakeConn{}, 1<<30)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = c.Write(buf)
		}
	})
}
