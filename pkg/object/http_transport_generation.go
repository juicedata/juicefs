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
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

const httpTransMaxAgeEnv = "JFS_HTTP_TRANS_MAX_AGE"

type httpTransportGeneration struct {
	base   *http.Transport
	maxAge time.Duration

	mu      sync.Mutex
	current *transportGeneration
	next    *transportGeneration
}

type transportGeneration struct {
	transport    *http.Transport
	transitionAt time.Time
	expiresAt    time.Time
	inFlight     int
	retiring     bool
}

type generationBody struct {
	io.ReadCloser
	done       func()
	afterClose func()
	once       sync.Once
	closeOnce  sync.Once
}

func parseHTTPConnMaxAge() time.Duration {
	value := os.Getenv(httpTransMaxAgeEnv)
	if value == "" {
		return 0
	}
	maxAge, err := time.ParseDuration(value)
	if err != nil || maxAge < 0 {
		logger.Warnf("invalid %s=%q, ignored", httpTransMaxAgeEnv, value)
		return 0
	}
	return maxAge
}

func newHTTPTransportGeneration(base *http.Transport, maxAge time.Duration) http.RoundTripper {
	if maxAge <= 0 {
		return base
	}
	return &httpTransportGeneration{base: base, maxAge: maxAge}
}

func (t *httpTransportGeneration) RoundTrip(req *http.Request) (*http.Response, error) {
	generation := t.acquire()
	resp, err := generation.transport.RoundTrip(req)
	if err != nil {
		t.release(generation)
		return nil, err
	}
	if resp.Body == nil {
		t.release(generation)
		return resp, nil
	}
	resp.Body = &generationBody{
		ReadCloser: resp.Body,
		done: func() {
			t.release(generation)
		},
		afterClose: func() {
			t.closeRetired(generation)
		},
	}
	return resp, nil
}

func (t *httpTransportGeneration) acquire() *transportGeneration {
	var retired *http.Transport

	t.mu.Lock()
	now := time.Now()
	if t.current == nil {
		t.current = t.newGeneration(now)
	}
	generation := t.current
	if !now.Before(t.current.expiresAt) {
		t.current.retiring = true
		retired = t.current.transport
		if t.next == nil {
			t.next = t.newGeneration(now)
		}
		t.current, t.next = t.next, nil
		generation = t.current
	} else if !now.Before(t.current.transitionAt) {
		created := false
		if t.next == nil {
			t.next = t.newGeneration(now)
			created = true
		}
		if created || t.useNext(now) {
			generation = t.next
		}
	}
	generation.inFlight++
	t.mu.Unlock()

	if retired != nil {
		retired.CloseIdleConnections()
	}
	return generation
}

func (t *httpTransportGeneration) newGeneration(now time.Time) *transportGeneration {
	generation := &transportGeneration{transport: t.base.Clone()}
	t.activate(generation, now)
	return generation
}

func (t *httpTransportGeneration) activate(generation *transportGeneration, now time.Time) {
	maxAge := jitterHTTPConnMaxAge(t.maxAge)
	generation.transitionAt = now.Add(maxAge - maxAge/10)
	generation.expiresAt = now.Add(maxAge)
}

func (t *httpTransportGeneration) useNext(now time.Time) bool {
	transition := t.current.expiresAt.Sub(t.current.transitionAt)
	elapsed := now.Sub(t.current.transitionAt)
	return transition <= 0 ||
		elapsed >= transition-transition/10 ||
		rand.Int63n(int64(transition)) < int64(elapsed)
}

func (t *httpTransportGeneration) release(generation *transportGeneration) {
	var closeIdle bool

	t.mu.Lock()
	generation.inFlight--
	closeIdle = generation.retiring && generation.inFlight == 0
	t.mu.Unlock()

	if closeIdle {
		generation.transport.CloseIdleConnections()
	}
}

func (t *httpTransportGeneration) closeRetired(generation *transportGeneration) {
	t.mu.Lock()
	closeIdle := generation.retiring && generation.inFlight == 0
	t.mu.Unlock()

	if closeIdle {
		generation.transport.CloseIdleConnections()
	}
}

func (t *httpTransportGeneration) CloseIdleConnections() {
	t.mu.Lock()
	current := t.current
	next := t.next
	t.mu.Unlock()

	if current != nil {
		current.transport.CloseIdleConnections()
	}
	if next != nil {
		next.transport.CloseIdleConnections()
	}
	t.base.CloseIdleConnections()
}

func (b *generationBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.done)
	}
	return n, err
}

func (b *generationBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	b.closeOnce.Do(b.afterClose)
	return err
}

func jitterHTTPConnMaxAge(maxAge time.Duration) time.Duration {
	delta := maxAge / 10
	if delta <= 0 {
		return maxAge
	}
	return maxAge - delta + time.Duration(rand.Int63n(int64(2*delta)))
}
