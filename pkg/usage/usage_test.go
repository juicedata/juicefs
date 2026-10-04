/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

type trackedUsageBody struct {
	io.ReadCloser
	closes int
}

func (b *trackedUsageBody) Close() error {
	b.closes++
	return b.ReadCloser.Close()
}

type trackedUsageTransport struct {
	transport *http.Transport
	body      *trackedUsageBody
}

func (t *trackedUsageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.transport.RoundTrip(req)
	if err == nil {
		t.body = &trackedUsageBody{ReadCloser: resp.Body}
		resp.Body = t.body
	}
	return resp, err
}

func TestSendUsageClosesResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		short  bool
	}{
		{name: "success", status: http.StatusOK, body: "OK"},
		{name: "empty success", status: http.StatusOK},
		{name: "HTTP error", status: http.StatusServiceUnavailable, body: "unavailable"},
		{name: "read error", status: http.StatusOK, body: "short", short: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.short {
					w.Header().Set("Content-Length", "10")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			transport := &trackedUsageTransport{transport: &http.Transport{}}
			defer transport.transport.CloseIdleConnections()
			oldURL, oldClient := reportUrl, http.DefaultClient
			reportUrl = server.URL
			http.DefaultClient = &http.Client{Transport: transport, Timeout: 3 * time.Second}
			defer func() { reportUrl, http.DefaultClient = oldURL, oldClient }()

			err := sendUsage(usage{VolumeID: "test-volume", Version: "test"})
			if tc.short {
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("read error = %v, want unexpected EOF", err)
				}
			} else if tc.status != http.StatusOK {
				if err == nil || !strings.Contains(err.Error(), "503") {
					t.Fatalf("HTTP error = %v, want 503", err)
				}
			} else if err != nil {
				t.Fatalf("sendUsage: %v", err)
			}
			if transport.body == nil {
				t.Fatal("no real HTTP response body")
			}
			defer transport.body.ReadCloser.Close()
			if transport.body.closes != 1 {
				t.Errorf("response body Close calls = %d, want 1", transport.body.closes)
			}
		})
	}
}

// Reading to EOF can already make a connection reusable; Close is still required.
func TestSendUsageSuccessfulResponseReusesConnection(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "OK")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	oldURL, oldClient := reportUrl, http.DefaultClient
	reportUrl = server.URL
	http.DefaultClient = &http.Client{Transport: transport, Timeout: 3 * time.Second}
	defer func() { reportUrl, http.DefaultClient = oldURL, oldClient }()
	for i := 0; i < 2; i++ {
		if err := sendUsage(usage{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Errorf("connections = %d, want reuse of one connection", got)
	}
}

// nolint:errcheck
func TestUsageReport(t *testing.T) {
	// invalid addr
	reportUrl = "http://127.0.0.1/report-usage"
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	go ReportUsage(m, "unittest")
	// wait for it to report to unavailable address, it should not panic.
	time.Sleep(time.Millisecond * 100)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	mux := http.NewServeMux()
	var u usage
	done := make(chan bool)
	mux.HandleFunc("/report-usage", func(rw http.ResponseWriter, r *http.Request) {
		d, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(d, &u)
		_, _ = rw.Write([]byte("OK"))
		done <- true
	})
	go http.Serve(l, mux)

	addr := l.Addr().String()
	reportUrl = fmt.Sprintf("http://%s/report-usage", addr)
	go ReportUsage(m, "unittest")

	deadline := time.NewTimer(time.Second * 3)
	select {
	case <-done:
		if u.MetaEngine != "memkv" {
			t.Fatalf("unexpected meta engine: %s", u.MetaEngine)
		}
		if u.Version != "unittest" {
			t.Fatalf("unexpected version: %s", u.Version)
		}
	case <-deadline.C:
		t.Fatalf("no report after 3 seconds")
	}
	time.Sleep(time.Millisecond * 100) // wait for the client to finish
}
