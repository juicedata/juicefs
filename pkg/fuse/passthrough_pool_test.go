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

package fuse

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPassthroughPoolReuse: a checked-in backing is handed back by the next
// checkout without a new registration (no server round trip), and pooled
// entries come back most-recently-parked first.
func TestPassthroughPoolReuse(t *testing.T) {
	dir := t.TempDir()
	p := &passthroughState{dir: dir, files: make(map[uint64]*ptFile)}

	mk := func(name string) *ptBacking {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			t.Fatal(err)
		}
		return &ptBacking{path: f.Name(), f: f, backingID: int32(len(p.pool) + 1)}
	}

	b1, b2 := mk("pool-1.tmp"), mk("pool-2.tmp")
	// Under the cap, checkin parks without touching the (nil) server —
	// a server call here would panic the test.
	p.checkin(b1)
	p.checkin(b2)
	if len(p.pool) != 2 {
		t.Fatalf("pool size = %d, want 2", len(p.pool))
	}

	// Checkout must reuse parked backings (nil server: a registration
	// attempt would panic), most recently parked first.
	got, ok := p.checkout()
	if !ok || got != b2 {
		t.Fatalf("checkout = %v, %v; want %v (LIFO reuse)", got, ok, b2)
	}
	got, ok = p.checkout()
	if !ok || got != b1 {
		t.Fatalf("checkout = %v, %v; want %v", got, ok, b1)
	}
	if len(p.pool) != 0 {
		t.Fatalf("pool size = %d, want 0", len(p.pool))
	}
}

// TestPassthroughDisabledLatch: once registration hit a permanent error the
// state stops attempting registrations entirely — checkout must return false
// before touching the (nil) server or the filesystem.
func TestPassthroughDisabledLatch(t *testing.T) {
	p := &passthroughState{dir: t.TempDir(), files: make(map[uint64]*ptFile), disabled: true}
	if b, ok := p.checkout(); ok || b != nil {
		t.Fatalf("checkout on disabled state = %v, %v; want nil, false", b, ok)
	}
	if p.poolSeq != 0 {
		t.Fatalf("disabled checkout still allocated a staging sequence")
	}
}

// TestPassthroughPoolStaleCleanup: a fresh state removes leftover staging
// files from a crashed predecessor in the same directory.
func TestPassthroughPoolStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "pool-9.tmp")
	if err := os.WriteFile(stale, []byte("junk"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = newPassthroughState(nil, dir)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale staging file survived: %v", err)
	}
}
