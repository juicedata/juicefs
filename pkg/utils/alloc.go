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

var bufferUsed int64
var otherUsed int64

func Alloc(size int, forOther bool) []byte {
	zeros := powerOf2(size)
	b := *pools[zeros].Get().(*[]byte)
	if cap(b) < size {
		panic(fmt.Sprintf("%d < %d", cap(b), size))
	}
	if forOther {
		atomic.AddInt64(&otherUsed, int64(cap(b)))
	} else {
		atomic.AddInt64(&bufferUsed, int64(cap(b)))
	}
	return b[:size]
}

// Free returns memory to Go heap.
func Free(b []byte, forOther bool) {
	// buf could be zero length
	if forOther {
		atomic.AddInt64(&otherUsed, -int64(cap(b)))
	} else {
		atomic.AddInt64(&bufferUsed, -int64(cap(b)))
	}
	pools[powerOf2(cap(b))].Put(&b)
}

func BufferUsedMemory() int64 {
	return atomic.LoadInt64(&bufferUsed)
}

var pools []*sync.Pool

func powerOf2(s int) int {
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
