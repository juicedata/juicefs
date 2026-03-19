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
	"net"
	"testing"
	"time"
)

// startTCPListener starts a TCP listener on the given address and returns it.
// The listener accepts connections in background and immediately closes them.
func startTCPListener(t *testing.T, addr string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen on %s: %v", addr, err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln
}

func getPort(t *testing.T, ln net.Listener) string {
	t.Helper()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestDialParallel_OnlyPrimaries(t *testing.T) {
	ln := startTCPListener(t, "127.0.0.1:0")
	defer ln.Close()
	port := getPort(t, ln)

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialParallel(context.Background(), dialer, "tcp",
		[]net.IP{net.ParseIP("127.0.0.1")}, nil, port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn.Close()
}

func TestDialParallel_OnlyFallbacks(t *testing.T) {
	// Bug reproduced: empty primaries should not panic
	ln := startTCPListener(t, "127.0.0.1:0")
	defer ln.Close()
	port := getPort(t, ln)

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialParallel(context.Background(), dialer, "tcp",
		nil, []net.IP{net.ParseIP("127.0.0.1")}, port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn.Close()
}

func TestDialParallel_PrimaryFailsFast_FallbackSucceeds(t *testing.T) {
	// Primary (IPv6 ::1) has no listener → fails fast (connection refused)
	// Fallback (127.0.0.1) has a listener → succeeds
	ln := startTCPListener(t, "127.0.0.1:0")
	defer ln.Close()
	port := getPort(t, ln)

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialParallel(context.Background(), dialer, "tcp",
		[]net.IP{net.ParseIP("::1")},       // primary - will fail (no listener on ::1:port)
		[]net.IP{net.ParseIP("127.0.0.1")}, // fallback - has listener
		port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn.Close()
}

func TestDialParallel_BothFail(t *testing.T) {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	_, err := dialParallel(context.Background(), dialer, "tcp",
		[]net.IP{net.ParseIP("::1")},
		[]net.IP{net.ParseIP("127.0.0.1")},
		"0")
	if err == nil {
		t.Fatal("expected error when both groups fail, got nil")
	}
}

func TestSplitIPsByVersion(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("fe80::1"),
	}
	v6, v4 := splitIPsByVersion(ips)
	if len(v6) != 2 {
		t.Errorf("expected 2 IPv6, got %d", len(v6))
	}
	if len(v4) != 2 {
		t.Errorf("expected 2 IPv4, got %d", len(v4))
	}
}
