/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package utils

import (
	"runtime"
	"sync"
	"time"
	"unsafe"
)

var slabs = make(map[uintptr][]byte)
var used int64
var slabsMutex sync.Mutex

// Alloc returns size bytes memory from Go heap.
func Alloc(size int) []byte {
	b := make([]byte, size)
	ptr := unsafe.Pointer(&b[0])
	slabsMutex.Lock()
	slabs[uintptr(ptr)] = b
	used += int64(size)
	slabsMutex.Unlock()
	return b
}

// Free returns memory to Go heap.
func Free(buf []byte) {
	// buf could be zero when writing
	p := unsafe.Pointer(&buf[:1][0])
	slabsMutex.Lock()
	if b, ok := slabs[uintptr(p)]; !ok {
		panic("invalid pointer")
	} else {
		used -= int64(len(b))
	}
	delete(slabs, uintptr(p))
	slabsMutex.Unlock()
}

// UsedMemory returns the memory used
// function is thread safe
func UsedMemory() int64 {
	slabsMutex.Lock()
	defer slabsMutex.Unlock()
	return used
}

func init() {
	go func() {
		for {
			time.Sleep(time.Minute * 10)
			runtime.GC()
		}
	}()
}
