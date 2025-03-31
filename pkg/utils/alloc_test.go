/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
)

func TestAlloc(t *testing.T) {
	old := AllocMemory()
	b := Alloc(10)
	if AllocMemory()-old != 16 {
		t.Fatalf("alloc 16 bytes, but got %d", AllocMemory()-old)
	}
	Free(b)
	if AllocMemory()-old != 0 {
		t.Fatalf("free all allocated memory, but got %d", AllocMemory()-old)
	}
}

func PowerOf2Loop(s int) int {
	var bits int
	var p int = 1
	for p < s {
		bits++
		p *= 2
	}
	return bits
}

func BenchmarkPowerOf2(b *testing.B) {
	b.Run("bits.Len", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for j := 0; j < 100000; j++ {
				_ = PowerOf2(j)
			}
		}
	})

	b.Run("Loop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for j := 0; j < 100000; j++ {
				_ = PowerOf2Loop(j)
			}
		}
	})
}
