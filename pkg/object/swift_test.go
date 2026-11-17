//go:build !noswift
// +build !noswift

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseSwiftEndpoint(t *testing.T) {
	for _, c := range []struct{ in, container, host string }{
		{"http://mybucket.swift.example.com", "mybucket", "swift.example.com"},
		{"https://swift.example.com/mybucket", "mybucket", "swift.example.com"},
		{"https://swift.example.com/user+tag@example.com/", "user+tag@example.com", "swift.example.com"},
		{"https://swift.example.com:8443/a.b", "a.b", "swift.example.com:8443"},
	} {
		uri, err := url.ParseRequestURI(c.in)
		if err != nil {
			t.Fatalf("parse %s: %s", c.in, err)
		}
		container, host, err := parseSwiftEndpoint(uri)
		if err != nil || container != c.container || host != c.host {
			t.Errorf("%s: got %q %q %v, want %q %q", c.in, container, host, err, c.container, c.host)
		}
	}
	for _, in := range []string{"http://localhost", "http://swift.example.com/a/b", "http://swift.example.com/a/b/"} {
		uri, _ := url.ParseRequestURI(in)
		if _, _, err := parseSwiftEndpoint(uri); err == nil {
			t.Errorf("%s: expected an error", in)
		}
	}
}

// fakeSwift serves V1 auth at /auth/v1.0 and Keystone v3 auth at
// /v3/auth/tokens, and counts the requests each one gets.
type fakeSwift struct {
	*httptest.Server
	v1, v3   atomic.Int32
	v3Body   map[string]interface{}
	userName string
	password string
}

func newFakeSwift(t *testing.T) *fakeSwift {
	f := &fakeSwift{userName: "user@example.com", password: "secret"}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/v1.0":
			f.v1.Add(1)
			if r.Header.Get("X-Auth-User") != f.userName || r.Header.Get("X-Auth-Key") != f.password {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-Storage-Url", f.URL+"/v1/AUTH_v1")
			w.Header().Set("X-Auth-Token", "tk-v1")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v3/auth/tokens" && r.Method == http.MethodPost:
			f.v3.Add(1)
			_ = json.NewDecoder(r.Body).Decode(&f.v3Body)
			w.Header().Set("X-Subject-Token", "tk-v3")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":{"expires_at":"2099-01-01T00:00:00.000000Z","catalog":[{"type":"object-store","endpoints":[{"interface":"public","region":"RegionOne","region_id":"RegionOne","url":"%s/v1/AUTH_v3"}]}]}}`, f.URL)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestSwiftAuthV1(t *testing.T) {
	f := newFakeSwift(t)
	// Keystone settings in the environment must not affect a V1 volume.
	other := newFakeSwift(t)
	t.Setenv("OS_AUTH_URL", other.URL+"/v3")
	t.Setenv("ST_AUTH", other.URL+"/auth/v1.0")
	t.Setenv("ST_AUTH_VERSION", "3")
	t.Setenv("OS_PROJECT_NAME", "storage")

	ep := f.URL + "/" + f.userName
	s, err := newSwiftOSS(ep, f.userName, f.password, "")
	if err != nil {
		t.Fatalf("%s: %s", ep, err)
	}
	so := s.(*swiftOSS)
	if so.container != f.userName || so.storageUrl != f.URL+"/v1/AUTH_v1" || so.conn.AuthVersion != 1 {
		t.Fatalf("got container %q storage %q version %d", so.container, so.storageUrl, so.conn.AuthVersion)
	}
	if f.v1.Load() == 0 || f.v3.Load() != 0 {
		t.Fatalf("expected V1 auth only, got v1=%d v3=%d", f.v1.Load(), f.v3.Load())
	}
	if other.v1.Load() != 0 || other.v3.Load() != 0 {
		t.Fatalf("auth went to the server named in the environment")
	}
	if _, err := newSwiftOSS(f.URL+"/c", f.userName, "wrong", ""); err == nil {
		t.Fatal("expected an auth error for a wrong password")
	}
}

func TestSwiftAuthKeystoneV3(t *testing.T) {
	f := newFakeSwift(t)
	q := url.Values{
		"auth-url":         {f.URL + "/v3"},
		"auth-version":     {"3"},
		"tenant":           {"storage"},
		"domain-id":        {"default"},
		"tenant-domain-id": {"default"},
	}
	ep := f.URL + "/" + url.PathEscape(f.userName) + "?" + q.Encode()
	s, err := newSwiftOSS(ep, f.userName, f.password, "")
	if err != nil {
		t.Fatalf("%s: %s", ep, err)
	}
	so := s.(*swiftOSS)
	if so.container != f.userName || so.storageUrl != f.URL+"/v1/AUTH_v3" {
		t.Fatalf("got container %q storage %q", so.container, so.storageUrl)
	}
	if f.v3.Load() != 1 || f.v1.Load() != 0 {
		t.Fatalf("expected one Keystone v3 auth, got v1=%d v3=%d", f.v1.Load(), f.v3.Load())
	}
	b, _ := json.Marshal(f.v3Body)
	for _, want := range []string{`"name":"user@example.com"`, `"password":"secret"`, `"name":"storage"`, `"id":"default"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Keystone request %s is missing %s", b, want)
		}
	}

	for _, bad := range []string{"0", "4", "x"} {
		q.Set("auth-version", bad)
		if _, err := newSwiftOSS(f.URL+"/c?"+q.Encode(), f.userName, f.password, ""); err == nil {
			t.Errorf("auth-version=%s: expected an error", bad)
		}
	}
}
