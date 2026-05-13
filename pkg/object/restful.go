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
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/viki-org/dnscache"
)

var resolver = dnscache.New(time.Minute)
var httpClient *http.Client
var httpTransport *http.Transport // raw transport, exposed via GetHttpTransport for SDKs needing the concrete type

// retiringConn caps requests served by a keep-alive conn so the http.Transport
// pool churns and the LB in front of the object-storage endpoint sees fresh
// distribution. Opt in via JFS_HTTP_MAX_REQUESTS_PER_CONN > 0.
//
// Two-mode retirement: at the cap we flag the conn (in-flight Write still
// completes). Preferred path — through rotatingRoundTripper — turns the flag
// into a Connection: close header on the next request; the server closes
// cleanly. Fallback path — direct *http.Transport callers like obs/swift/tos
// SDKs — closes the conn after gracePeriodAfterMark more boundaries and
// returns errConnRetired, relying on transport-level retry.
//
// Boundaries are detected as Write-after-Read. TLS handshake adds 1-2 to the
// effective count, negligible at typical caps. State lives entirely inside
// the struct: when the underlying conn dies, the struct is GC'd — no map,
// no goroutine, no timer.
type retiringConn struct {
	net.Conn
	maxRequests         int32
	requestCount        int32
	lastWasRead         int32
	retired             int32 // 0 = healthy; 1 = marked, awaiting graceful close
	boundariesAfterMark int32
}

// gracePeriodAfterMark bounds how many boundaries the friendly path
// (Connection: close injection) gets before fallback fires. Friendly path
// dies after 1 boundary; 2 leaves a small race buffer.
const gracePeriodAfterMark = 2

var errConnRetired = errors.New("juicefs: connection retired after reaching max requests")

func newRetiringConn(c net.Conn, max int) net.Conn {
	if max <= 0 {
		return c
	}
	// Per-conn ±25% jitter: prevents same-batch conns from retiring together
	// (storm) and breaks deterministic LB stickiness.
	jittered := max
	if half := max / 2; half > 0 {
		jittered = max + rand.Intn(half) - max/4
		if jittered < 1 {
			jittered = 1
		}
	}
	return &retiringConn{Conn: c, maxRequests: int32(jittered)}
}

// IsRetired is read by rotatingRoundTripper at GotConn to decide whether to
// inject Connection: close on the upcoming request.
func (c *retiringConn) IsRetired() bool {
	return atomic.LoadInt32(&c.retired) == 1
}

func (c *retiringConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		atomic.StoreInt32(&c.lastWasRead, 1)
	}
	return n, err
}

func (c *retiringConn) Write(b []byte) (int, error) {
	// Boundary = Write following a Read; one boundary per HTTP request.
	if atomic.SwapInt32(&c.lastWasRead, 0) == 1 {
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

// rotatingRoundTripper injects Connection: close on requests routed to a
// marked retiringConn, so the server closes the conn cleanly after responding
// (RFC 7230). The caller never sees an error from retirement on this path.
//
// Per-RoundTrip overhead: one req.Clone (~1-2 μs, Header map copy dominates)
// plus one httptrace.WithClientTrace setup (~hundreds of ns). Reflection only
// fires if the caller already attached an httptrace; for typical use there is
// none.
type rotatingRoundTripper struct {
	inner http.RoundTripper
}

func (t *rotatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture user-set Connection header so retries onto a non-retired conn
	// don't silently lose it.
	origConnection, hadOrig := req.Header["Connection"]

	cloned := req.Clone(req.Context())
	cloned = cloned.WithContext(httptrace.WithClientTrace(cloned.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if rc, ok := info.Conn.(*retiringConn); ok && rc.IsRetired() {
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

func init() {
	// Defaults preserve historical behavior: 500 idle conns per host and no
	// per-conn request cap. Operators opt in via JFS_HTTP_MAX_REQUESTS_PER_CONN
	// and may tighten the idle pool via JFS_HTTP_MAX_IDLE_CONNS_PER_HOST.
	maxIdle := envIntDefault("JFS_HTTP_MAX_IDLE_CONNS_PER_HOST", 500)
	maxReqs := envIntDefault("JFS_HTTP_MAX_REQUESTS_PER_CONN", 0)
	httpTransport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   time.Second * 20,
		ResponseHeaderTimeout: time.Second * 30,
		IdleConnTimeout:       time.Second * 300,
		MaxIdleConnsPerHost:   maxIdle,
		ReadBufferSize:        32 << 10,
		WriteBufferSize:       32 << 10,
		Dial: func(network string, address string) (net.Conn, error) {
			separator := strings.LastIndex(address, ":")
			host := address[:separator]
			port := address[separator:]
			ips, err := resolver.Fetch(host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("No such host: %s", host)
			}
			var conn net.Conn
			n := len(ips)
			first := rand.Intn(n)
			dialer := &net.Dialer{Timeout: time.Second * 10}
			for i := 0; i < n; i++ {
				ip := ips[(first+i)%n]
				address = ip.String()
				if port != "" {
					address = net.JoinHostPort(address, port[1:])
				}
				conn, err = dialer.Dial(network, address)
				if err == nil {
					return newRetiringConn(conn, maxReqs), nil
				}
			}
			return nil, err
		},
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{},
	}
	// Wrap with rotatingRoundTripper for the friendly path; SDKs that pull
	// raw *http.Transport via GetHttpTransport bypass this wrapper.
	var rt http.RoundTripper = httpTransport
	if maxReqs > 0 {
		rt = &rotatingRoundTripper{inner: httpTransport}
	}
	httpClient = &http.Client{
		Transport: rt,
		Timeout:   time.Hour,
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}

// GetHttpTransport returns the underlying *http.Transport for SDKs that
// require the concrete type instead of http.RoundTripper.
func GetHttpTransport() *http.Transport {
	return httpTransport
}

func cleanup(response *http.Response) {
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
}

type RestfulStorage struct {
	DefaultObjectStorage
	endpoint  string
	accessKey string
	secretKey string
	signName  string
	signer    func(*http.Request, string, string, string)
}

func (s *RestfulStorage) String() string {
	return s.endpoint
}

var HEADER_NAMES = []string{"Content-MD5", "Content-Type", "Date"}

func (s *RestfulStorage) request(method, key string, body io.Reader, headers map[string]string) (*http.Response, error) {
	uri := s.endpoint + "/" + key
	req, err := http.NewRequest(method, uri, body)
	if err != nil {
		return nil, err
	}
	if f, ok := body.(*os.File); ok {
		st, err := f.Stat()
		if err == nil {
			req.ContentLength = st.Size()
		}
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	for key := range headers {
		req.Header.Add(key, headers[key])
	}
	s.signer(req, s.accessKey, s.secretKey, s.signName)
	return httpClient.Do(req)
}

func parseError(resp *http.Response) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("request failed: %s", err)
	}
	return fmt.Errorf("status: %v, message: %s", resp.StatusCode, string(data))
}

func (s *RestfulStorage) Head(key string) (Object, error) {
	resp, err := s.request("HEAD", key, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return nil, parseError(resp)
	}

	lastModified := resp.Header.Get("Last-Modified")
	if lastModified == "" {
		return nil, fmt.Errorf("cannot get last modified time")
	}
	mtime, _ := time.Parse(time.RFC1123, lastModified)
	return &obj{
		key,
		resp.ContentLength,
		mtime,
		strings.HasSuffix(key, "/"),
		"",
	}, nil
}

func getRange(off, limit int64) string {
	if off > 0 || limit > 0 {
		if limit > 0 {
			return fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			return fmt.Sprintf("bytes=%d-", off)
		}
	}
	return ""
}

func checkGetStatus(statusCode int, partial bool) error {
	var expected = http.StatusOK
	if partial {
		expected = http.StatusPartialContent
	}
	if statusCode != expected {
		return fmt.Errorf("expected status code %d, but got %d", expected, statusCode)
	}
	return nil
}

func (s *RestfulStorage) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	headers := make(map[string]string)
	if off > 0 || limit > 0 {
		headers["Range"] = getRange(off, limit)
	}
	resp, err := s.request("GET", key, nil, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, parseError(resp)
	}
	if err = checkGetStatus(resp.StatusCode, len(headers) > 0); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (u *RestfulStorage) Put(key string, body io.Reader, getters ...AttrGetter) error {
	resp, err := u.request("PUT", key, body, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) Copy(dst, src string) error {
	in, err := s.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer in.Close()
	d, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return s.Put(dst, bytes.NewReader(d))
}

func (s *RestfulStorage) Delete(key string, getters ...AttrGetter) error {
	resp, err := s.request("DELETE", key, nil, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	return nil, false, "", notSupported
}

var _ ObjectStorage = &RestfulStorage{}
