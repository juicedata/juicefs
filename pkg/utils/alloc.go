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
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var used int64

// Alloc returns size bytes memory from Go heap.
func Alloc(size int) []byte {
	zeros := PowerOf2(size)
	b := *pools[zeros].Get().(*[]byte)
	if cap(b) < size {
		panic(fmt.Sprintf("%d < %d", cap(b), size))
	}
	atomic.AddInt64(&used, int64(cap(b)))
	return b[:size]
}

// Free returns memory to Go heap.
func Free(b []byte) {
	// buf could be zero length
	atomic.AddInt64(&used, -int64(cap(b)))
	pools[PowerOf2(cap(b))].Put(&b)
}

// AllocMemory returns the allocated memory
func AllocMemory() int64 {
	return atomic.LoadInt64(&used)
}

var pools []*sync.Pool

func PowerOf2(s int) int {
	var bits int
	var p int = 1
	for p < s {
		bits++
		p *= 2
	}
	return bits
}

func init() {
	pools = make([]*sync.Pool, 33) // 1 - 8G
	for i := 0; i < 33; i++ {
		func(bits int) {
			pools[i] = &sync.Pool{
				New: func() interface{} {
					b := make([]byte, 1<<bits)
					return &b
				},
			}
		}(i)
	}
	go func() {
		for {
			time.Sleep(time.Minute * 10)
			runtime.GC()
		}
	}()
}
