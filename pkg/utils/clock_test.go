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
