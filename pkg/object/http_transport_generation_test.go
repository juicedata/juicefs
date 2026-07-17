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
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type generationConnIDKey struct{}

type generationConnTracker struct {
	next   atomic.Int64
	ids    sync.Map
	closed chan int64
}

type generationBlockingCloseBody struct {
	closeStarted chan struct{}
	releaseClose chan struct{}
	started      sync.Once
}

func (b *generationBlockingCloseBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (b *generationBlockingCloseBody) Close() error {
	b.started.Do(func() { close(b.closeStarted) })
	<-b.releaseClose
	return nil
}

func (t *generationConnTracker) connContext(ctx context.Context, conn net.Conn) context.Context {
	id := t.next.Add(1)
	t.ids.Store(conn, id)
	return context.WithValue(ctx, generationConnIDKey{}, id)
}

func (t *generationConnTracker) connState(conn net.Conn, state http.ConnState) {
	if state != http.StateClosed {
		return
	}
	if id, ok := t.ids.LoadAndDelete(conn); ok {
		t.closed <- id.(int64)
	}
}

func (t *generationConnTracker) waitClosed(tb testing.TB, want int64) {
	tb.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case id := <-t.closed:
			if id == want {
				return
			}
		case <-timer.C:
			tb.Fatalf("connection %d was not closed", want)
		}
	}
}

func TestHTTPTransportGenerationDrainsInFlightRequest(t *testing.T) {
	tests := []struct {
		name string
		tls  bool
		h2   bool
	}{
		{name: "http1"},
		{name: "https-http1", tls: true},
		{name: "https-http2", tls: true, h2: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()

			tracker := &generationConnTracker{closed: make(chan int64, 8)}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := r.Context().Value(generationConnIDKey{}).(int64)
				w.Header().Set("X-Conn-ID", strconv.FormatInt(id, 10))
				if r.URL.Path == "/hold" {
					w.(http.Flusher).Flush()
					<-release
					_, _ = io.WriteString(w, "done")
					return
				}
				_, _ = fmt.Fprint(w, id)
			}))
			server.Config.ConnContext = tracker.connContext
			server.Config.ConnState = tracker.connState
			server.EnableHTTP2 = test.h2
			if test.tls {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()

			var base *http.Transport
			if test.tls {
				base = server.Client().Transport.(*http.Transport).Clone()
				base.ForceAttemptHTTP2 = test.h2
			} else {
				base = &http.Transport{}
			}
			client := &http.Client{
				Transport: newHTTPTransportGeneration(base, 20*time.Millisecond),
				Timeout:   5 * time.Second,
			}
			defer client.CloseIdleConnections()

			first, err := client.Get(server.URL + "/hold")
			if err != nil {
				t.Fatalf("first request: %v", err)
			}
			defer first.Body.Close()
			if test.h2 && first.ProtoMajor != 2 {
				t.Fatalf("first request used %s, want HTTP/2", first.Proto)
			}
			firstID, err := strconv.ParseInt(first.Header.Get("X-Conn-ID"), 10, 64)
			if err != nil {
				t.Fatalf("first connection ID: %v", err)
			}

			time.Sleep(50 * time.Millisecond)
			second, err := client.Get(server.URL)
			if err != nil {
				t.Fatalf("second request: %v", err)
			}
			secondBody, err := io.ReadAll(second.Body)
			_ = second.Body.Close()
			if err != nil {
				t.Fatalf("second response: %v", err)
			}
			secondID, err := strconv.ParseInt(string(secondBody), 10, 64)
			if err != nil {
				t.Fatalf("second connection ID: %v", err)
			}
			if firstID == secondID {
				t.Fatalf("expired generation reused connection %d", firstID)
			}

			close(release)
			released = true
			body, err := io.ReadAll(first.Body)
			if err != nil {
				t.Fatalf("in-flight response was interrupted: %v", err)
			}
			if string(body) != "done" {
				t.Fatalf("first response body = %q, want done", body)
			}
			if err := first.Body.Close(); err != nil {
				t.Fatalf("close first response: %v", err)
			}
			tracker.waitClosed(t, firstID)
		})
	}
}

func TestHTTPTransportGenerationConcurrentRotation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	transport := newHTTPTransportGeneration(&http.Transport{}, time.Hour).(*httpTransportGeneration)
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	transport.mu.Lock()
	old := transport.current
	old.expiresAt = time.Now().Add(-time.Second)
	transport.mu.Unlock()

	const requests = 256
	if _, err := runHTTPTransportGenerationBurst(client, server.URL, requests); err != nil {
		t.Fatal(err)
	}

	transport.mu.Lock()
	current := transport.current
	oldRetiring := old.retiring
	oldInFlight := old.inFlight
	currentInFlight := current.inFlight
	transport.mu.Unlock()

	if current == old {
		t.Fatal("transport generation was not rotated")
	}
	if !oldRetiring {
		t.Fatal("old transport generation is not retiring")
	}
	if oldInFlight != 0 {
		t.Fatalf("old generation has %d in-flight requests, want 0", oldInFlight)
	}
	if currentInFlight != 0 {
		t.Fatalf("current generation has %d in-flight requests, want 0", currentInFlight)
	}
}

func TestHTTPTransportGenerationSmoothTransition(t *testing.T) {
	transport := newHTTPTransportGeneration(&http.Transport{}, time.Hour).(*httpTransportGeneration)
	old := transport.acquire()
	transport.release(old)

	now := time.Now()
	transport.mu.Lock()
	old.transitionAt = now.Add(-9 * time.Second)
	old.expiresAt = now.Add(time.Second)
	transport.mu.Unlock()

	next := transport.acquire()
	transport.release(next)
	if next == old {
		t.Fatal("transition request used the old generation")
	}
	transport.mu.Lock()
	prepared := transport.next
	transport.mu.Unlock()
	if prepared != next {
		t.Fatal("next generation was not prepared")
	}
	preparedExpiry := next.expiresAt

	now = time.Now()
	transport.mu.Lock()
	old.transitionAt = now.Add(-time.Second)
	old.expiresAt = now.Add(time.Second)
	transport.mu.Unlock()
	nextRequests := 0
	for range 1000 {
		generation := transport.acquire()
		if generation == next {
			nextRequests++
		}
		transport.release(generation)
	}
	if nextRequests < 350 || nextRequests > 650 {
		t.Fatalf("mid-transition requests routed to next = %d, want roughly 500", nextRequests)
	}

	now = time.Now()
	transport.mu.Lock()
	old.transitionAt = now.Add(-9 * time.Second)
	old.expiresAt = now.Add(time.Second)
	transport.mu.Unlock()
	for range 100 {
		generation := transport.acquire()
		transport.release(generation)
		if generation != next {
			t.Fatal("late-transition request used the old generation")
		}
	}

	transport.mu.Lock()
	old.expiresAt = time.Now().Add(-time.Second)
	transport.mu.Unlock()
	promoted := transport.acquire()
	transport.release(promoted)
	if promoted != next {
		t.Fatal("prepared generation was not promoted")
	}
	if !promoted.expiresAt.Equal(preparedExpiry) {
		t.Fatal("promoting the prepared generation reset its lifetime")
	}
	if !old.retiring {
		t.Fatal("old generation is not retiring")
	}
}

func TestHTTPTransportGenerationDrainsHTTP2AfterBodyClose(t *testing.T) {
	tracker := &generationConnTracker{closed: make(chan int64, 8)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Context().Value(generationConnIDKey{}).(int64)
		w.Header().Set("X-Conn-ID", strconv.FormatInt(id, 10))
		_, _ = io.WriteString(w, "done")
	}))
	server.Config.ConnContext = tracker.connContext
	server.Config.ConnState = tracker.connState
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	base := server.Client().Transport.(*http.Transport).Clone()
	base.ForceAttemptHTTP2 = true
	client := &http.Client{
		Transport: newHTTPTransportGeneration(base, 20*time.Millisecond),
		Timeout:   5 * time.Second,
	}
	defer client.CloseIdleConnections()

	requestBody := &generationBlockingCloseBody{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
	released := false
	defer func() {
		if !released {
			close(requestBody.releaseClose)
		}
	}()
	req, err := http.NewRequest(http.MethodPost, server.URL, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	first, err := client.Do(req)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	defer first.Body.Close()
	if first.ProtoMajor != 2 {
		t.Fatalf("first request used %s, want HTTP/2", first.Proto)
	}
	firstID, err := strconv.ParseInt(first.Header.Get("X-Conn-ID"), 10, 64)
	if err != nil {
		t.Fatalf("first connection ID: %v", err)
	}

	select {
	case <-requestBody.closeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("request body was not closed")
	}

	time.Sleep(50 * time.Millisecond)
	second, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	secondID, err := strconv.ParseInt(second.Header.Get("X-Conn-ID"), 10, 64)
	if err != nil {
		_ = second.Body.Close()
		t.Fatalf("second connection ID: %v", err)
	}
	_, readErr := io.Copy(io.Discard, second.Body)
	closeErr := second.Body.Close()
	if readErr != nil {
		t.Fatalf("second response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close second response: %v", closeErr)
	}
	if firstID == secondID {
		t.Fatalf("expired generation reused connection %d", firstID)
	}

	body, err := io.ReadAll(first.Body)
	if err != nil {
		t.Fatalf("first response: %v", err)
	}
	if string(body) != "done" {
		t.Fatalf("first response body = %q, want done", body)
	}
	close(requestBody.releaseClose)
	released = true
	if err := first.Body.Close(); err != nil {
		t.Fatalf("close first response: %v", err)
	}
	tracker.waitClosed(t, firstID)
}

func TestHTTPTransportGenerationDisabled(t *testing.T) {
	base := &http.Transport{}
	if got := newHTTPTransportGeneration(base, 0); got != base {
		t.Fatalf("disabled generation transport = %T, want base transport", got)
	}
}

func TestHTTPTransportGenerationClonesBaseLazily(t *testing.T) {
	base := &http.Transport{TLSClientConfig: &tls.Config{}}
	transport := newHTTPTransportGeneration(base, time.Minute).(*httpTransportGeneration)
	base.TLSClientConfig.ServerName = "configured.example.com"

	generation := transport.acquire()
	defer transport.release(generation)
	if got := generation.transport.TLSClientConfig.ServerName; got != "configured.example.com" {
		t.Fatalf("cloned TLS server name = %q, want configured.example.com", got)
	}
}

func TestParseHTTPConnMaxAge(t *testing.T) {
	t.Setenv(httpTransMaxAgeEnv, "")
	if got := parseHTTPConnMaxAge(); got != 0 {
		t.Fatalf("empty value = %s, want 0", got)
	}
	t.Setenv(httpTransMaxAgeEnv, "30m")
	if got := parseHTTPConnMaxAge(); got != 30*time.Minute {
		t.Fatalf("valid value = %s, want 30m", got)
	}
	for _, value := range []string{"invalid", "-1s"} {
		t.Setenv(httpTransMaxAgeEnv, value)
		if got := parseHTTPConnMaxAge(); got != 0 {
			t.Fatalf("value %q = %s, want 0", value, got)
		}
	}
}

func TestGenerationBodyReleasesOnce(t *testing.T) {
	var releases atomic.Int32
	var closes atomic.Int32
	body := &generationBody{
		ReadCloser: io.NopCloser(strings.NewReader("body")),
		done: func() {
			releases.Add(1)
		},
		afterClose: func() {
			closes.Add(1)
		},
	}
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release count = %d, want 1", got)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
}

func TestJitterHTTPConnMaxAge(t *testing.T) {
	const maxAge = time.Hour
	for range 100 {
		got := jitterHTTPConnMaxAge(maxAge)
		if got < 54*time.Minute || got >= 66*time.Minute {
			t.Fatalf("jittered max age %s is outside [54m, 66m)", got)
		}
	}
}

type generationBurstResult struct {
	latency time.Duration
	err     error
}

/*
type generationPipeAddr string

func (a generationPipeAddr) Network() string { return string(a) }
func (a generationPipeAddr) String() string  { return string(a) }

type generationPipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newGenerationPipeListener() *generationPipeListener {
	return &generationPipeListener{
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
}

func (l *generationPipeListener) DialContext(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, context.Cause(ctx)
	case <-l.done:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

func (l *generationPipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *generationPipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *generationPipeListener) Addr() net.Addr {
	return generationPipeAddr("generation-pipe")
}
*/

func runHTTPTransportGenerationBurst(client *http.Client, url string, requests int) ([]time.Duration, error) {
	start := make(chan struct{})
	results := make(chan generationBurstResult, requests)
	for range requests {
		go func() {
			<-start
			started := time.Now()
			resp, err := client.Get(url)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				if closeErr := resp.Body.Close(); err == nil {
					err = closeErr
				}
			}
			results <- generationBurstResult{latency: time.Since(started), err: err}
		}()
	}
	close(start)

	latencies := make([]time.Duration, 0, requests)
	for range requests {
		result := <-results
		if result.err != nil {
			return nil, result.err
		}
		latencies = append(latencies, result.latency)
	}
	return latencies, nil
}

/*
func BenchmarkHTTPTransportGenerationBurst(b *testing.B) {
	for _, test := range []struct {
		name       string
		transition bool
	}{
		{name: "warm-pool"},
		{name: "smooth-transition", transition: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkHTTPTransportGenerationBurst(b, test.transition)
		})
	}
}

func benchmarkHTTPTransportGenerationBurst(b *testing.B, transition bool) {
	const (
		requests  = 64
		dialDelay = 5 * time.Millisecond
	)

	warmReady := make(chan struct{}, requests)
	warmRelease := make(chan struct{})
	listener := newGenerationPipeListener()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warm" {
			warmReady <- struct{}{}
			<-warmRelease
		}
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = server.Serve(listener) }()
	defer listener.Close()
	defer server.Close()

	var dials atomic.Int64
	base := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dials.Add(1)
			timer := time.NewTimer(dialDelay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
			return listener.DialContext(ctx)
		},
		IdleConnTimeout:     time.Minute,
		MaxIdleConns:        requests,
		MaxIdleConnsPerHost: requests,
	}
	transport := newHTTPTransportGeneration(base, time.Hour).(*httpTransportGeneration)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()

	go func() {
		for range requests {
			<-warmReady
		}
		close(warmRelease)
	}()
	const serverURL = "http://generation.test"
	if _, err := runHTTPTransportGenerationBurst(client, serverURL+"/warm", requests); err != nil {
		b.Fatal(err)
	}

	latencies := make([]time.Duration, 0, b.N*requests)
	var measuredDials, transitionDials int64
	b.ResetTimer()
	for range b.N {
		if transition {
			b.StopTimer()
			now := time.Now()
			transport.mu.Lock()
			transport.current.transitionAt = now.Add(-9 * time.Second)
			transport.current.expiresAt = now.Add(time.Second)
			transport.mu.Unlock()
			before := dials.Load()
			if _, err := runHTTPTransportGenerationBurst(client, serverURL, requests); err != nil {
				b.Fatal(err)
			}
			transitionDials += dials.Load() - before
			transport.mu.Lock()
			transport.current.expiresAt = time.Now().Add(-time.Second)
			transport.mu.Unlock()
			b.StartTimer()
		}
		before := dials.Load()
		batch, err := runHTTPTransportGenerationBurst(client, serverURL, requests)
		if err != nil {
			b.Fatal(err)
		}
		measuredDials += dials.Load() - before
		latencies = append(latencies, batch...)
	}
	b.StopTimer()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	b.ReportMetric(requests, "requests/burst")
	b.ReportMetric(float64(dialDelay)/float64(time.Millisecond), "dial-delay-ms")
	b.ReportMetric(float64(measuredDials)/float64(b.N), "dials/burst")
	b.ReportMetric(float64(transitionDials)/float64(b.N), "transition-dials/burst")
	b.ReportMetric(float64(latencies[len(latencies)/2])/float64(time.Millisecond), "p50-ms")
	b.ReportMetric(float64(latencies[len(latencies)*95/100])/float64(time.Millisecond), "p95-ms")
	b.ReportMetric(float64(latencies[len(latencies)-1])/float64(time.Millisecond), "max-ms")
}
*/
