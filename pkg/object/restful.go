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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viki-org/dnscache"
)

var resolver = dnscache.New(time.Minute)
var httpClient *http.Client
var httpTransport *http.Transport

// parseHTTPConnMaxAge reads the opt-in connection max age from the environment.
// Invalid values are ignored so the process can keep the previous behavior.
func parseHTTPConnMaxAge() time.Duration {
	value := os.Getenv("JFS_HTTP_CONN_MAX_AGE")
	if value == "" {
		return 0
	}
	maxAge, err := time.ParseDuration(value)
	if err != nil || maxAge < 0 {
		logger.Warnf("invalid JFS_HTTP_CONN_MAX_AGE=%q, ignored", value)
		return 0
	}
	return maxAge
}

// jitterHTTPConnMaxAge spreads retirement times so a large batch of connections
// does not age out at exactly the same moment.
func jitterHTTPConnMaxAge(maxAge time.Duration) time.Duration {
	if maxAge <= 0 {
		return 0
	}
	delta := maxAge / 10
	if delta <= 0 {
		return maxAge
	}
	return maxAge - delta + time.Duration(rand.Int63n(int64(2*delta)))
}

// splitIPsByVersion keeps IPv6 and IPv4 addresses separate so dialing can
// preserve the existing happy-eyeballs style fallback order.
func splitIPsByVersion(ips []net.IP) ([]net.IP, []net.IP) {
	ipv6 := make([]net.IP, 0, len(ips))
	ipv4 := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip.To4() == nil {
			ipv6 = append(ipv6, ip)
		} else {
			ipv4 = append(ipv4, ip)
		}
	}
	return ipv6, ipv4
}

// dialParallel is adapted from the Go standard library.
// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
func dialParallel(ctx context.Context, dialer *net.Dialer, network string, primaries, fallbacks []net.IP, port string) (net.Conn, error) {
	if len(fallbacks) == 0 {
		return dialRandom(ctx, dialer, network, primaries, port)
	}

	returned := make(chan struct{})
	defer close(returned)

	type dialResult struct {
		net.Conn
		error
		primary bool
		done    bool
	}
	results := make(chan dialResult) // unbuffered

	startRacer := func(ctx context.Context, primary bool) {
		ras := primaries
		if !primary {
			ras = fallbacks
		}
		c, err := dialRandom(ctx, dialer, network, ras, port)
		select {
		case results <- dialResult{Conn: c, error: err, primary: primary, done: true}:
		case <-returned:
			if c != nil {
				c.Close()
			}
		}
	}

	var primary, fallback dialResult

	// Start the main racer.
	primaryCtx, primaryCancel := context.WithCancel(ctx)
	defer primaryCancel()
	go startRacer(primaryCtx, true)

	// Start the timer for the fallback racer.
	fallbackTimer := time.NewTimer(300 * time.Millisecond)
	defer fallbackTimer.Stop()

	for {
		select {
		case <-fallbackTimer.C:
			fallbackCtx, fallbackCancel := context.WithCancel(ctx)
			defer fallbackCancel()
			go startRacer(fallbackCtx, false)

		case res := <-results:
			if res.error == nil {
				return res.Conn, nil
			}
			if res.primary {
				primary = res
			} else {
				fallback = res
			}
			if primary.done && fallback.done {
				return nil, errors.Join(primary.error, fallback.error)
			}
			if res.primary && fallbackTimer.Stop() {
				// If we were able to stop the timer, that means it
				// was running (hadn't yet started the fallback), but
				// we just got an error on the primary path, so start
				// the fallback immediately (in 0 nanoseconds).
				fallbackTimer.Reset(0)
			}
		}
	}
}

// dialRandom tries the provided addresses in a randomized order until one
// succeeds or the context is canceled.
func dialRandom(ctx context.Context, dialer *net.Dialer, network string, ips []net.IP, port string) (net.Conn, error) {
	var lastErr error
	n := len(ips)
	if n == 0 {
		return nil, fmt.Errorf("no addresses to dial")
	}
	first := rand.Intn(n)
	for i := 0; i < n; i++ {
		ip := ips[(first+i)%n]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// init builds the shared HTTP transport first, then optionally wraps it with
// connection max-age management while keeping the base *http.Transport exposed
// for SDK paths that require the concrete type.
func init() {
	dialer := &net.Dialer{Timeout: time.Second * 10}
	httpConnMaxAge := parseHTTPConnMaxAge()
	httpTransport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   time.Second * 20,
		ResponseHeaderTimeout: time.Second * 30,
		IdleConnTimeout:       time.Second * 300,
		MaxIdleConnsPerHost:   500,
		ReadBufferSize:        32 << 10,
		WriteBufferSize:       32 << 10,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			ips, err := resolver.Fetch(host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			ipv6, ipv4 := splitIPsByVersion(ips)
			return dialParallel(ctx, dialer, network, ipv6, ipv4, port)
		},
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{},
	}
	httpClient = &http.Client{
		Transport: newHTTPConnMaxAgeTransport(httpTransport, httpConnMaxAge),
		Timeout:   time.Hour,
	}
}

// GetHttpClient returns the shared object-storage HTTP client.
func GetHttpClient() *http.Client {
	return httpClient
}

// GetHttpTransport returns the shared base transport for integrations that
// need direct access to the concrete *http.Transport.
func GetHttpTransport() *http.Transport {
	return httpTransport
}

// cleanup drains and closes a response body so its connection can be reused.
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

func (s *RestfulStorage) request(ctx context.Context, method, key string, body io.Reader, headers map[string]string) (*http.Response, error) {
	uri := s.endpoint + "/" + key
	req, err := http.NewRequestWithContext(ctx, method, uri, body)
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

func (s *RestfulStorage) Head(ctx context.Context, key string) (Object, error) {
	resp, err := s.request(ctx, "HEAD", key, nil, nil)
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

func (s *RestfulStorage) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	headers := make(map[string]string)
	if off > 0 || limit > 0 {
		headers["Range"] = getRange(off, limit)
	}
	resp, err := s.request(ctx, "GET", key, nil, headers)
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

func (u *RestfulStorage) Put(ctx context.Context, key string, body io.Reader, getters ...AttrGetter) error {
	resp, err := u.request(ctx, "PUT", key, body, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) Copy(ctx context.Context, dst, src string) error {
	in, err := s.Get(ctx, src, 0, -1)
	if err != nil {
		return err
	}
	defer in.Close()
	d, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return s.Put(ctx, dst, bytes.NewReader(d))
}

func (s *RestfulStorage) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	resp, err := s.request(ctx, "DELETE", key, nil, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	return nil, false, "", notSupported
}

var _ ObjectStorage = (*RestfulStorage)(nil)
