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

package main

import (
	"flag"
	"fmt"
	"runtime"
	"time"
)

type ino uint64

type slice struct {
	id   uint64
	size uint32
}

type keyMaps struct {
	valid     map[uint64]uint32
	pending   map[uint64]uint32
	compacted map[uint64]uint32
	total     int64
	bytes     uint64
}

var retained map[ino][]slice

func addSlice(km *keyMaps, s slice, blockSize int) {
	km.total += int64(int(s.size-1)/blockSize) + 1
	km.bytes += uint64(s.size)
}

func buildCurrent(slices map[ino][]slice, blockSize int) keyMaps {
	km := keyMaps{
		valid:     make(map[uint64]uint32),
		pending:   make(map[uint64]uint32),
		compacted: make(map[uint64]uint32),
	}
	for _, s := range slices[0] {
		km.pending[s.id] = s.size
		addSlice(&km, s, blockSize)
	}
	for _, s := range slices[1] {
		km.compacted[s.id] = s.size
		addSlice(&km, s, blockSize)
	}
	for inode, ss := range slices {
		if inode == 0 || inode == 1 {
			continue
		}
		for _, s := range ss {
			km.valid[s.id] = s.size
			addSlice(&km, s, blockSize)
		}
	}
	return km
}

func buildOld(slices map[ino][]slice, blockSize int) keyMaps {
	km := keyMaps{
		valid:     make(map[uint64]uint32),
		pending:   make(map[uint64]uint32),
		compacted: make(map[uint64]uint32),
	}
	for _, s := range slices[0] {
		km.pending[s.id] = s.size
		addSlice(&km, s, blockSize)
	}
	slices[0] = nil
	for _, s := range slices[1] {
		km.compacted[s.id] = s.size
		addSlice(&km, s, blockSize)
	}
	slices[1] = nil
	for _, ss := range slices {
		for _, s := range ss {
			km.valid[s.id] = s.size
			addSlice(&km, s, blockSize)
		}
	}
	return km
}

func printMem(label string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("%s heap_alloc_mb=%.1f heap_inuse_mb=%.1f sys_mb=%.1f num_gc=%d\n",
		label,
		float64(m.HeapAlloc)/1024/1024,
		float64(m.HeapInuse)/1024/1024,
		float64(m.Sys)/1024/1024,
		m.NumGC,
	)
}

func scanObjects(km keyMaps, lookups int, allocEvery int, allocBytes int) uint64 {
	var checksum uint64
	var noise [][]byte
	validCount := len(km.valid)
	for i := 0; i < lookups; i++ {
		id := uint64(i%validCount) + 1000000
		checksum += uint64(km.valid[id])
		if allocEvery > 0 && i%allocEvery == 0 {
			noise = append(noise, make([]byte, allocBytes))
			if len(noise) > 16 {
				noise = noise[:0]
			}
		}
	}
	return checksum
}

func main() {
	mode := flag.String("mode", "current", "current, old-real, or old-retained")
	count := flag.Int("slices", 5000000, "number of normal slices")
	lookups := flag.Int("lookups", 20000000, "object scan lookups")
	allocEvery := flag.Int("alloc-every", 50000, "allocate during every N lookups to trigger GC")
	allocBytes := flag.Int("alloc-bytes", 1<<20, "bytes allocated per allocation burst")
	blockSize := flag.Int("block-size", 4096, "block size")
	flag.Parse()

	start := time.Now()
	slices := make(map[ino][]slice, 3)
	normal := make([]slice, *count)
	for i := range normal {
		normal[i] = slice{id: uint64(i) + 1000000, size: uint32(*blockSize)}
	}
	slices[100] = normal
	normal = nil
	slices[0] = []slice{{id: 10, size: uint32(*blockSize)}}
	slices[1] = []slice{{id: 20, size: uint32(*blockSize)}}
	runtime.GC()
	printMem("after_generate")

	var km keyMaps
	switch *mode {
	case "current":
		km = buildCurrent(slices, *blockSize)
		slices = nil
	case "old-real":
		km = buildOld(slices, *blockSize)
	case "old-retained":
		km = buildOld(slices, *blockSize)
		retained = slices
	default:
		panic("unknown mode: " + *mode)
	}
	runtime.GC()
	printMem("after_flatten_gc")

	checksum := scanObjects(km, *lookups, *allocEvery, *allocBytes)
	runtime.GC()
	printMem("after_scan_gc")
	runtime.KeepAlive(retained)

	fmt.Printf("mode=%s slices=%d lookups=%d total=%d bytes=%d checksum=%d elapsed=%s\n",
		*mode, *count+2, *lookups, km.total, km.bytes, checksum, time.Since(start).Round(time.Millisecond))
}
