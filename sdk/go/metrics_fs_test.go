//go:build !libjfs

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

package juicefs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPushLabels(t *testing.T) {
	labels := pushLabels("vol", "")
	if labels["vol_name"] != "vol" || labels["mp"] != "sdk-"+strconv.Itoa(os.Getpid()) {
		t.Fatalf("labels %v", labels)
	}
	if h, err := os.Hostname(); err == nil && labels["instance"] != h {
		t.Fatalf("instance %q, want %q", labels["instance"], h)
	}

	labels = pushLabels("vol", "env:prod;vol_name:other")
	if labels["env"] != "prod" || labels["vol_name"] != "other" {
		t.Fatalf("labels %v", labels)
	}
}

func TestPushGateway(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	pushed := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(paths)
	}

	conf := testConfig()
	conf.PushGateway = server.URL
	conf.PushInterval = 50 * time.Millisecond
	conf.PushAuth = "user:pass:word"
	conf.PushLabels = "env:test"
	c, err := New("test", newTestVolume(t), conf)
	if err != nil {
		t.Fatal(err)
	}
	for start := time.Now(); pushed() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Since(start) > 5*time.Second {
			t.Fatal("no periodic push")
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	n := pushed()
	time.Sleep(3 * conf.PushInterval)
	if pushed() != n {
		t.Fatal("pushing continues after Close")
	}

	mu.Lock()
	defer mu.Unlock()
	last := paths[len(paths)-1]
	for _, want := range []string{"/metrics/job/juicefs", "/vol_name/test", "/env/test", "/mp/sdk-" + strconv.Itoa(os.Getpid())} {
		if !strings.Contains(last, want) {
			t.Fatalf("push path %q has no %q", last, want)
		}
	}
	// "user:pass:word" is split at the first colon (P31).
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "pass:word")
	if auth != req.Header.Get("Authorization") {
		t.Fatalf("Authorization %q", auth)
	}
}
