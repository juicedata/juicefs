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

package utils

import (
	"testing"
	"time"
)

// The first effective sample is exact for SimpleEWMA (value starts at 0, so
// Add(v) sets value=v), which lets us assert the raw bytes/second precisely.
func TestRealtimeSpeedComputesBytesPerSecond(t *testing.T) {
	rt := newRealtimeSpeed()
	base := time.Unix(1600000000, 0)
	rt.update(0, base)                       // baseline, no sample yet
	rt.update(1000, base.Add(2*time.Second)) // delta=1000 over 2s -> 500 B/s
	if got := rt.avg.Value(); got != 500 {
		t.Fatalf("expected 500 B/s, got %v", got)
	}
}

// A second call within speedSampleInterval must be debounced: it returns the
// cached string and leaves the moving average untouched.
func TestRealtimeSpeedDebouncesWithinInterval(t *testing.T) {
	rt := newRealtimeSpeed()
	base := time.Unix(1600000000, 0)
	rt.update(0, base)                              // baseline
	first := rt.update(1000, base.Add(time.Second)) // effective sample -> 1000 B/s
	if rt.avg.Value() != 1000 {
		t.Fatalf("setup: expected 1000, got %v", rt.avg.Value())
	}
	// only 50ms after the last effective sample (< 300ms) -> debounced
	second := rt.update(999999, base.Add(time.Second+50*time.Millisecond))
	if second != first {
		t.Fatalf("expected debounced msg %q, got %q", first, second)
	}
	if rt.avg.Value() != 1000 {
		t.Fatalf("expected avg unchanged at 1000, got %v", rt.avg.Value())
	}
}

// A negative delta (e.g. sync rolls back current via IncrInt64(-n)) must be
// clamped to 0 so the speed never goes negative. The first sample is exact, so
// clamped -> 0 while un-clamped would be -500.
func TestRealtimeSpeedClampsNegativeDelta(t *testing.T) {
	rt := newRealtimeSpeed()
	base := time.Unix(1600000000, 0)
	rt.update(1000, base)                 // baseline last=1000
	rt.update(500, base.Add(time.Second)) // delta=-500 -> clamp to 0
	if got := rt.avg.Value(); got != 0 {
		t.Fatalf("expected clamped 0 B/s, got %v", got)
	}
}

// The parenthesised value is the cumulative average since the first sample:
// current / elapsed. With 600 bytes over 3s total, avg must be 200 B/s.
func TestRealtimeSpeedAverageSinceStart(t *testing.T) {
	rt := newRealtimeSpeed()
	base := time.Unix(1600000000, 0)
	rt.update(0, base)                             // start
	rt.update(600, base.Add(time.Second))          // effective sample
	msg := rt.update(600, base.Add(3*time.Second)) // avg = 600 / 3s = 200 B/s
	want := rt.format(rt.avg.Value(), 200)
	if msg != want {
		t.Fatalf("expected %q, got %q", want, msg)
	}
}
