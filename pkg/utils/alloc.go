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
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var used int64

// Alloc returns size bytes memory from Go heap.
func Alloc(size int) []byte {
	b := Alloc0(size)
	atomic.AddInt64(&used, int64(cap(b)))
	return b
}

// Alloc returns size bytes memory from Go heap.
func Alloc0(size int) []byte {
	zeros := PowerOf2(size)
	b := *pools[zeros].Get().(*[]byte)
	if cap(b) < size {
		panic(fmt.Sprintf("%d < %d", cap(b), size))
	}
	return b[:size]
}

// Free returns memory to Go heap.
func Free(b []byte) {
	// buf could be zero length
	atomic.AddInt64(&used, -int64(cap(b)))
	Free0(b)
}

// Free returns memory to Go heap.
func Free0(b []byte) {
	// buf could be zero length
	pools[PowerOf2(cap(b))].Put(&b)
}

// AllocMemory returns the allocated memory
func AllocMemory() int64 {
	return atomic.LoadInt64(&used)
}

var pools []*sync.Pool

// PowerOf2 returns the smallest power of 2 that is >= s
func PowerOf2(s int) int {
	if s <= 0 {
		return 0
	}
	// Find position of the most significant bit (MSB)
	return bits.Len(uint(s - 1))
}

func init() {
	pools = make([]*sync.Pool, 34) // 1 - 8G
	for i := 0; i < 34; i++ {
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
