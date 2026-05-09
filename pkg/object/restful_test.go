/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"crypto/tls"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

type countingRoundTripper struct {
	hits uint64
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddUint64(&c.hits, 1)
	return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
}

func TestRoundRobinTransportDistributesEvenly(t *testing.T) {
	const poolSize = 8
	const totalRequests = 800

	counters := make([]*countingRoundTripper, poolSize)
	transports := make([]*http.Transport, poolSize)
	for i := range transports {
		counters[i] = &countingRoundTripper{}
		transports[i] = &http.Transport{}
	}

	rt := &roundRobinTransport{transports: transports}

	roundRobin := func(req *http.Request) (*http.Response, error) {
		idx := atomic.AddUint64(&rt.counter, 1) % uint64(len(counters))
		return counters[idx].RoundTrip(req)
	}

	var wg sync.WaitGroup
	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", "http://example.test/", nil)
			_, err := roundRobin(req)
			if err != nil {
				t.Errorf("roundtrip: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadUint64(&rt.counter); got != totalRequests {
		t.Fatalf("counter = %d, want %d", got, totalRequests)
	}

	expected := uint64(totalRequests / poolSize)
	for i, c := range counters {
		hits := atomic.LoadUint64(&c.hits)
		if hits != expected {
			t.Errorf("transport %d hits = %d, want %d", i, hits, expected)
		}
	}
}

func TestRoundRobinTransportCloseIdleConnections(t *testing.T) {
	transports := make([]*http.Transport, 4)
	for i := range transports {
		transports[i] = &http.Transport{}
	}
	rt := &roundRobinTransport{transports: transports}
	rt.CloseIdleConnections()
}

func TestRoundRobinTransportSharesTLSConfig(t *testing.T) {
	tlsCfg := &tls.Config{}
	transports := make([]*http.Transport, 4)
	for i := range transports {
		transports[i] = newHTTPTransport(tlsCfg)
	}
	tlsCfg.InsecureSkipVerify = true
	for i, tr := range transports {
		if !tr.TLSClientConfig.InsecureSkipVerify {
			t.Fatalf("transport %d does not see shared TLS config update", i)
		}
	}
}

func TestGetHttpTransportReturnsConcrete(t *testing.T) {
	tr := GetHttpTransport()
	if tr == nil {
		t.Fatal("GetHttpTransport returned nil")
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
}
