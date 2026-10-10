//go:build !noetcd
// +build !noetcd

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
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestEtcdTLSConfig(t *testing.T) {
	for _, tc := range []struct {
		name, query, serverName        string
		wantTLS, skipVerify, wantError bool
	}{
		{name: "plaintext"},
		{name: "empty_skip_verify", query: "insecure-skip-verify="},
		{name: "skip_verify_only", query: "insecure-skip-verify=1", wantTLS: true, skipVerify: true},
		{name: "nonempty_skip_verify", query: "insecure-skip-verify=0", wantTLS: true, skipVerify: true},
		{name: "server_name", query: "server-name=etcd", serverName: "etcd", wantTLS: true},
		{name: "server_name_and_skip_verify", query: "server-name=etcd&insecure-skip-verify=1", serverName: "etcd", wantTLS: true, skipVerify: true},
		{name: "missing_ca", query: "cacert=" + url.QueryEscape(filepath.Join(t.TempDir(), "missing.pem")), wantError: true},
		{name: "partial_client_certificate", query: "cert=" + url.QueryEscape(filepath.Join(t.TempDir(), "missing.pem")), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse("etcd://127.0.0.1:2379?" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			conf, err := buildTlsConfig(u)
			if (err != nil) != tc.wantError {
				t.Fatalf("buildTlsConfig error = %v, wantError %v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if (conf != nil) != tc.wantTLS {
				t.Fatalf("TLS enabled = %v, want %v", conf != nil, tc.wantTLS)
			}
			if conf != nil && (conf.InsecureSkipVerify != tc.skipVerify || conf.ServerName != tc.serverName) {
				t.Fatalf("skipVerify/serverName = %v/%q, want %v/%q", conf.InsecureSkipVerify, conf.ServerName, tc.skipVerify, tc.serverName)
			}
		})
	}
}

func TestEtcdTLSConfigHandshake(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	for _, tc := range []struct {
		name, query string
		wantError   bool
	}{
		{name: "skip_verify", query: "insecure-skip-verify=1"},
		{name: "verify_untrusted_certificate", query: "server-name=127.0.0.1", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse("etcd://" + server.Listener.Addr().String() + "?" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			conf, err := buildTlsConfig(u)
			if err != nil || conf == nil {
				t.Fatalf("expected TLS configuration, got %v, %v", conf, err)
			}
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", server.Listener.Addr().String(), conf)
			if conn != nil {
				defer conn.Close()
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("TLS handshake error = %v, wantError %v", err, tc.wantError)
			}
		})
	}
}
