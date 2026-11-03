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
	"errors"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"sync/atomic"
)

// retiringConn caps requests served by a keep-alive conn so the http.Transport
// pool churns and the LB in front of the object-storage endpoint sees fresh
// distribution. Opt in via JFS_HTTP_MAX_REQUESTS_PER_CONN > 0.
//
// Two-mode retirement. Preferred path — through rotatingRoundTripper — counts
// each request exactly once when the conn is handed out (GotConn) and sends
// Connection: close on the request that reaches the cap; the server closes
// cleanly and the conn never closes itself mid-request. Fallback path — direct
// *http.Transport callers like obs/swift/tos SDKs — flags the conn at the cap,
// closes it after gracePeriodAfterMark more boundaries and returns
// errConnRetired, relying on transport-level retry.
//
// Fallback boundaries are detected as Write-after-Read. TLS handshake adds 1-2
// to the effective count, negligible at typical caps. Expect: 100-continue
// uploads count twice ("100 Continue" is read before the body is written),
// which is why the preferred path does not use this heuristic. State lives
// entirely inside the struct: when the underlying conn dies, the struct is
// GC'd — no map, no goroutine, no timer.
type retiringConn struct {
	net.Conn
	maxRequests         int32
	requestCount        int32
	lastWasRead         int32
	retired             int32
	boundariesAfterMark int32
	managed             int32 // used via rotatingRoundTripper
}

const gracePeriodAfterMark = 2

var errConnRetired = errors.New("juicefs: connection retired after reaching max requests")

func newRetiringConn(c net.Conn, max int) net.Conn {
	if max <= 0 {
		return c
	}
	jittered := max
	if half := max / 2; half > 0 {
		jittered = max + rand.Intn(half) - max/4
		if jittered < 1 {
			jittered = 1
		}
	}
	return &retiringConn{Conn: c, maxRequests: int32(jittered)}
}

// onRoundTrip counts one request on the preferred path and reports whether it
// reached the cap. It also turns off the fallback heuristic for this conn.
func (c *retiringConn) onRoundTrip() bool {
	atomic.StoreInt32(&c.managed, 1)
	return atomic.AddInt32(&c.requestCount, 1) >= c.maxRequests
}

func (c *retiringConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		atomic.StoreInt32(&c.lastWasRead, 1)
	}
	return n, err
}

func (c *retiringConn) Write(b []byte) (int, error) {
	if atomic.LoadInt32(&c.managed) == 0 && atomic.SwapInt32(&c.lastWasRead, 0) == 1 {
		if atomic.LoadInt32(&c.retired) == 1 {
			if atomic.AddInt32(&c.boundariesAfterMark, 1) > gracePeriodAfterMark {
				_ = c.Conn.Close()
				return 0, errConnRetired
			}
		} else if atomic.LoadInt32(&c.requestCount) > c.maxRequests {
			atomic.StoreInt32(&c.retired, 1)
		}
		atomic.AddInt32(&c.requestCount, 1)
	}
	return c.Conn.Write(b)
}

type rotatingRoundTripper struct {
	inner http.RoundTripper
}

func (t *rotatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	origConnection, hadOrig := req.Header["Connection"]

	cloned := req.Clone(req.Context())
	cloned = cloned.WithContext(httptrace.WithClientTrace(cloned.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			conn := info.Conn
			if tc, ok := conn.(*tls.Conn); ok {
				conn = tc.NetConn()
			}
			if rc, ok := conn.(*retiringConn); ok && rc.onRoundTrip() {
				cloned.Header.Set("Connection", "close")
			} else if hadOrig {
				cloned.Header["Connection"] = origConnection
			} else {
				cloned.Header.Del("Connection")
			}
		},
	}))
	return t.inner.RoundTrip(cloned)
}

func (t *rotatingRoundTripper) CloseIdleConnections() {
	type closer interface{ CloseIdleConnections() }
	if c, ok := t.inner.(closer); ok {
		c.CloseIdleConnections()
	}
}

func envIntDefault(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

func configureHTTPTransport(transport *http.Transport) http.RoundTripper {
	transport.MaxIdleConnsPerHost = envIntDefault("JFS_HTTP_MAX_IDLE_CONNS_PER_HOST", transport.MaxIdleConnsPerHost)
	maxReqs := envIntDefault("JFS_HTTP_MAX_REQUESTS_PER_CONN", 0)
	if maxReqs > 0 && transport.DialContext != nil {
		dialContext := transport.DialContext
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return newRetiringConn(conn, maxReqs), nil
		}
	}

	if maxReqs > 0 {
		return &rotatingRoundTripper{inner: transport}
	}
	return transport
}
