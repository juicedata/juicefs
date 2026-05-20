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
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type httpConnMaxAgeTransport struct {
	base   *http.Transport
	maxAge time.Duration

	mu      sync.Mutex
	buckets map[string]*connBucket
}

type connBucket struct {
	conns   []*managedHTTPConn
	dialing bool
	waiters []chan struct{}
}

type managedHTTPConn struct {
	key       string
	cc        *http.ClientConn
	createdAt time.Time
	maxAge    time.Duration
	retiring  bool
}

// newHTTPConnMaxAgeTransport wraps the shared base transport with a Go 1.26
// ClientConn pool when max age is enabled. With max age disabled it returns
// the original transport so legacy behavior stays unchanged.
func newHTTPConnMaxAgeTransport(base *http.Transport, maxAge time.Duration) http.RoundTripper {
	if maxAge <= 0 {
		return base
	}
	return &httpConnMaxAgeTransport{
		base:    base,
		maxAge:  maxAge,
		buckets: make(map[string]*connBucket),
	}
}

// RoundTrip sends the request through a managed ClientConn and then rechecks
// whether that connection should be retired after the request state changes.
func (t *httpConnMaxAgeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	mc, err := t.pickConn(req)
	if err != nil {
		return nil, err
	}
	resp, err := mc.cc.RoundTrip(req)
	t.onConnStateChange(mc)
	return resp, err
}

// pickConn selects a managed connection for the request, waiting when the
// per-host pool is full and another request is expected to free capacity.
func (t *httpConnMaxAgeTransport) pickConn(req *http.Request) (*managedHTTPConn, error) {
	key, addr, err := keyAndAddr(req)
	if err != nil {
		return nil, err
	}

	for {
		candidates, toClose, waiter, dialNew := t.prepareConnAttempt(key)
		closeClientConns(toClose)

		// Reserve is done outside the pool lock because ClientConn may run the
		// state hook synchronously from Reserve/Release/RoundTrip/Body.Close.
		for _, mc := range candidates {
			if mc.cc.Reserve() != nil {
				continue
			}
			if t.shouldUseReservedConn(mc) {
				return mc, nil
			}
			mc.cc.Release()
			t.onConnStateChange(mc)
		}

		if dialNew {
			return t.dialManagedConn(req, key, addr)
		}
		if waiter == nil {
			continue
		}
		// Waiters are edge-triggered: any pool state change wakes them up and
		// they retry the full selection flow from scratch.
		select {
		case <-waiter:
		case <-req.Context().Done():
			t.removeWaiter(key, waiter)
			return nil, req.Context().Err()
		}
	}
}

// prepareConnAttempt sweeps the bucket, returns immediately usable candidates,
// or decides whether the caller should become the single dialer or wait.
func (t *httpConnMaxAgeTransport) prepareConnAttempt(key string) ([]*managedHTTPConn, []*http.ClientConn, chan struct{}, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket := t.getOrCreateBucketLocked(key)
	candidates, toClose := t.sweepBucketLocked(bucket)
	if len(candidates) > 0 {
		return candidates, toClose, nil, false
	}

	if len(bucket.conns) < t.maxConnsPerHost() && !bucket.dialing {
		bucket.dialing = true
		return nil, toClose, nil, true
	}

	waiter := make(chan struct{})
	bucket.waiters = append(bucket.waiters, waiter)
	return nil, toClose, waiter, false
}

// dialManagedConn creates a new ClientConn for the host and installs a state
// hook so later request completions can retire or wake waiters for it.
func (t *httpConnMaxAgeTransport) dialManagedConn(req *http.Request, key, addr string) (*managedHTTPConn, error) {
	cc, err := t.base.NewClientConn(req.Context(), req.URL.Scheme, addr)
	if err != nil {
		t.finishDial(key, nil)
		return nil, err
	}
	if err := cc.Reserve(); err != nil {
		_ = cc.Close()
		t.finishDial(key, nil)
		return nil, err
	}

	mc := &managedHTTPConn{
		key:       key,
		cc:        cc,
		createdAt: time.Now(),
		maxAge:    jitterHTTPConnMaxAge(t.maxAge),
	}
	cc.SetStateHook(func(*http.ClientConn) {
		t.onConnStateChange(mc)
	})
	t.finishDial(key, mc)
	return mc, nil
}

// finishDial clears the dialing flag, publishes the new connection if one was
// created, and wakes every waiter to let them compete for the new capacity.
func (t *httpConnMaxAgeTransport) finishDial(key string, mc *managedHTTPConn) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket := t.getOrCreateBucketLocked(key)
	bucket.dialing = false
	if mc != nil {
		bucket.conns = append(bucket.conns, mc)
	}
	t.notifyWaitersLocked(bucket)
	t.cleanupBucketLocked(key, bucket)
}

// shouldUseReservedConn confirms that a connection reserved outside the pool
// lock is still valid, still pooled, and not already marked for retirement.
func (t *httpConnMaxAgeTransport) shouldUseReservedConn(mc *managedHTTPConn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket := t.buckets[mc.key]
	return bucket != nil && t.containsLocked(bucket, mc) && !mc.retiring && mc.cc.Err() == nil && !t.expiredLocked(mc)
}

// onConnStateChange reacts to ClientConn state transitions. The pool only
// mutates state under lock; Close happens after unlocking to avoid re-entering
// this hook while the mutex is still held.
func (t *httpConnMaxAgeTransport) onConnStateChange(mc *managedHTTPConn) {
	var closeConn *http.ClientConn

	t.mu.Lock()
	bucket := t.buckets[mc.key]
	if bucket == nil || !t.containsLocked(bucket, mc) {
		t.mu.Unlock()
		return
	}

	if mc.cc.Err() != nil {
		t.removeLocked(bucket, mc)
	} else if t.expiredLocked(mc) {
		mc.retiring = true
		if mc.cc.InFlight() == 0 {
			t.removeLocked(bucket, mc)
			closeConn = mc.cc
		}
	}
	t.notifyWaitersLocked(bucket)
	t.cleanupBucketLocked(mc.key, bucket)
	t.mu.Unlock()

	if closeConn != nil {
		_ = closeConn.Close()
	}
}

// sweepBucketLocked rebuilds the live connection slice in one pass so the
// caller never deletes from the slice while iterating over it.
func (t *httpConnMaxAgeTransport) sweepBucketLocked(bucket *connBucket) ([]*managedHTTPConn, []*http.ClientConn) {
	kept := bucket.conns[:0]
	var candidates []*managedHTTPConn
	var toClose []*http.ClientConn

	for _, mc := range bucket.conns {
		if mc.cc.Err() != nil {
			toClose = append(toClose, mc.cc)
			continue
		}
		if t.expiredLocked(mc) {
			mc.retiring = true
			if mc.cc.InFlight() == 0 {
				toClose = append(toClose, mc.cc)
				continue
			}
		}
		kept = append(kept, mc)
		if !mc.retiring && mc.cc.Available() > 0 {
			candidates = append(candidates, mc)
		}
	}
	bucket.conns = kept
	return candidates, toClose
}

// removeWaiter unregisters a waiter that timed out or was canceled before the
// bucket had a chance to wake it up.
func (t *httpConnMaxAgeTransport) removeWaiter(key string, waiter chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket := t.buckets[key]
	if bucket == nil {
		return
	}
	for i, w := range bucket.waiters {
		if w == waiter {
			bucket.waiters = append(bucket.waiters[:i], bucket.waiters[i+1:]...)
			break
		}
	}
	t.cleanupBucketLocked(key, bucket)
}

// notifyWaitersLocked wakes every blocked picker because any pool state change
// may make a connection reusable or permit one more dial.
func (t *httpConnMaxAgeTransport) notifyWaitersLocked(bucket *connBucket) {
	for _, waiter := range bucket.waiters {
		close(waiter)
	}
	bucket.waiters = nil
}

// getOrCreateBucketLocked returns the per-host bucket, creating it on demand.
func (t *httpConnMaxAgeTransport) getOrCreateBucketLocked(key string) *connBucket {
	bucket := t.buckets[key]
	if bucket == nil {
		bucket = &connBucket{}
		t.buckets[key] = bucket
	}
	return bucket
}

// cleanupBucketLocked removes empty buckets once they no longer hold pooled
// connections, an active dialer, or blocked waiters.
func (t *httpConnMaxAgeTransport) cleanupBucketLocked(key string, bucket *connBucket) {
	if len(bucket.conns) == 0 && !bucket.dialing && len(bucket.waiters) == 0 {
		delete(t.buckets, key)
	}
}

// expiredLocked reports whether the connection has exceeded its configured
// lifetime and should stop accepting new requests.
func (t *httpConnMaxAgeTransport) expiredLocked(mc *managedHTTPConn) bool {
	return time.Since(mc.createdAt) >= mc.maxAge
}

// containsLocked reports whether the bucket still owns the managed connection.
func (t *httpConnMaxAgeTransport) containsLocked(bucket *connBucket, mc *managedHTTPConn) bool {
	for _, conn := range bucket.conns {
		if conn == mc {
			return true
		}
	}
	return false
}

// removeLocked removes a managed connection from the bucket if it is still
// present. The caller is responsible for any later Close.
func (t *httpConnMaxAgeTransport) removeLocked(bucket *connBucket, mc *managedHTTPConn) {
	for i, conn := range bucket.conns {
		if conn == mc {
			bucket.conns = append(bucket.conns[:i], bucket.conns[i+1:]...)
			return
		}
	}
}

// maxConnsPerHost derives the pool cap from the shared base transport and
// falls back to a conservative default when the base transport leaves it unset.
func (t *httpConnMaxAgeTransport) maxConnsPerHost() int {
	if t.base.MaxIdleConnsPerHost > 0 {
		return t.base.MaxIdleConnsPerHost
	}
	return 2
}

// keyAndAddr validates the request target and returns both the pool key and the
// canonical host:port used by Transport.NewClientConn.
func keyAndAddr(req *http.Request) (string, string, error) {
	if req.URL == nil {
		return "", "", fmt.Errorf("http: nil Request.URL")
	}
	addr := canonicalHTTPAddr(req.URL)
	if addr == "" {
		return "", "", fmt.Errorf("http: no Host in request URL")
	}
	return req.URL.Scheme + "://" + addr, addr, nil
}

// canonicalHTTPAddr normalizes a request URL into host:port form.
func canonicalHTTPAddr(u *url.URL) string {
	host := u.Hostname()
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
	}
	return net.JoinHostPort(host, port)
}

// closeClientConns closes a batch of ClientConn values outside pool locks.
func closeClientConns(conns []*http.ClientConn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}
