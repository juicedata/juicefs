/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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
	"fmt"
	"sync"
)

func DynAlloc(size int) []byte {
	zeros := powerOf2(size)
	b := *dynPools[zeros].Get().(*[]byte)
	if cap(b) < size {
		panic(fmt.Sprintf("%d < %d", cap(b), size))
	}
	return b[:size]
}

// DynFree b may be longer than the original, in the actual size into the pool
func DynFree(b []byte) {
	dynPools[FloorPowerOf2(cap(b))].Put(&b)
}

func FloorPowerOf2(s int) int {
	var bits int
	var p = 1
	for p <= s {
		bits++
		p *= 2
	}
	return bits - 1
}

var dynPools []*sync.Pool

func init() {
	dynPools = make([]*sync.Pool, 34) // 1 - 8G
	for i := 0; i < 34; i++ {
		func(bits int) {
			dynPools[i] = &sync.Pool{
				New: func() interface{} {
					b := make([]byte, 1<<bits)
					return &b
				},
			}
		}(i)
	}
}
