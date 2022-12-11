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

package utils

import (
	"log"
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	now := Now()
	if time.Since(now).Microseconds() > 1000 {
		t.Fatal("time is not accurate")
	}
	c1 := Clock()
	c2 := Clock()
	if c2 < c1 {
		t.Fatalf("clock is not monotonic: %s > %s", c1, c2)
	}
}

func BenchmarkNow(b *testing.B) {
	var now time.Time
	for i := 0; i < b.N; i++ {
		now = Now()
	}
	log.Print(now)
}

func BenchmarkClock(b *testing.B) {
	var now time.Duration
	for i := 0; i < b.N; i++ {
		now = Clock()
	}
	log.Print(now)
}
