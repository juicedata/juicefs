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
	"path/filepath"
	"syscall"
	"testing"
)

func TestNewAndClose(t *testing.T) {
	c := newTestClient(t)
	if c.Name() != "test" {
		t.Fatalf("Name() = %q", c.Name())
	}
	if c.Gatherer() == nil {
		t.Fatal("Gatherer() = nil")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %s", err)
	}
}

func TestUmask(t *testing.T) {
	old := syscall.Umask(027)
	defer syscall.Umask(old)
	c := newTestClient(t)
	if c.umask != 027 {
		t.Fatalf("umask %o, want 027", c.umask)
	}
	if mask := syscall.Umask(old); mask != 027 {
		t.Fatalf("umask changed to %o by New", mask)
	}
}

func TestNewErrors(t *testing.T) {
	metaURL := newTestVolume(t)

	conf := testConfig()
	conf.MaxDeletes = -1
	if _, err := New("test", metaURL, conf); err == nil {
		t.Fatal("negative MaxDeletes: no error")
	}

	conf = testConfig()
	conf.PushLabels = "a:b;c"
	if _, err := New("test", metaURL, conf); err == nil {
		t.Fatal("invalid PushLabels: no error")
	}

	empty := "sqlite3://" + filepath.Join(t.TempDir(), "empty.db")
	if _, err := New("test", empty, testConfig()); err == nil {
		t.Fatal("unformatted volume: no error")
	}
}
