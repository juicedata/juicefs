//go:build !windows
// +build !windows

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
	"bytes"
	"os"
	"strconv"
	"syscall"
)

func MemoryUsage() (virt, rss uint64) {
	stat, err := os.ReadFile("/proc/self/stat")
	if err == nil {
		stats := bytes.Split(stat, []byte(" "))
		if len(stats) >= 24 {
			v, _ := strconv.ParseUint(string(stats[22]), 10, 64)
			r, _ := strconv.ParseUint(string(stats[23]), 10, 64)
			return v, r * 4096
		}
	}

	var ru syscall.Rusage
	err = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if err == nil {
		return uint64(ru.Maxrss), uint64(ru.Maxrss)
	}
	return
}
