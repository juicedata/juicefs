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
	"net/url"
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
	uri, _ := url.ParseRequestURI("http://localhost")
	if _, _, err := parseSwiftEndpoint(uri); err == nil {
		t.Error("expected an error for a host with no container")
	}
}
