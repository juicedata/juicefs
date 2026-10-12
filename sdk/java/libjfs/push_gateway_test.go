// Copyright 2026 JuiceFS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPushGatewayPushWithAuth(t *testing.T) {
	// Push explicitly so this test does not start the periodic background loop.
	pOnce.Do(func() {})
	previousPushers := pushers
	t.Cleanup(func() {
		pushers = previousPushers
		pOnce = sync.Once{}
	})

	tests := []struct {
		name     string
		auth     string
		username string
		password string
		wantAuth bool
	}{
		{"ordinary", "testuser:testpass", "testuser", "testpass", true},
		{"password colon", "testuser:pa:ss", "testuser", "pa:ss", true},
		{"multiple colons", "testuser:pa::ss", "testuser", "pa::ss", true},
		{"leading colon", "testuser::secret", "testuser", ":secret", true},
		{"trailing colon", "testuser:secret:", "testuser", "secret:", true},
		{"empty password", "testuser:", "testuser", "", true},
		{"empty username", ":secret", "", "secret", true},
		{"no auth", "", "", "", false},
		{"missing separator", "testuser", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username, password, ok := r.BasicAuth()
				if ok != tt.wantAuth || username != tt.username || password != tt.password {
					t.Errorf("BasicAuth() = (%q, %q, %v), want (%q, %q, %v)",
						username, password, ok, tt.username, tt.password, tt.wantAuth)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			push2Gateway(server.URL, tt.auth, time.Hour, prometheus.NewRegistry(), nil)
			if err := pushers[len(pushers)-1].Push(); err != nil {
				t.Fatalf("Push() error = %v", err)
			}
		})
	}
}
