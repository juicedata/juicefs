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

func TestRUsage(t *testing.T) {
	//u := GetRusage()
	var s string
	for i := 0; i < 1000; i++ {
		s += time.Now().String()
	}
	// don't optimize the loop
	if len(s) < 10 {
		panic("unreachable")
	}
	_ = GetRusage()
	// cancelled due to high machine load
	//if u2.GetUtime()-u.GetUtime() < 0.0001 {
	//	t.Fatalf("invalid utime: %f", u2.GetStime()-u.GetStime())
	//}
}
