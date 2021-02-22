/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"syscall"
	"time"
	"unsafe"
)

type clock struct {
	t    time.Time
	tick time.Duration
}

var last *clock

func Now() time.Time {
	c := last
	return c.t.Add(Clock() - c.tick)
}

// Clock returns the number of milliseconds that have elapsed since the program
// was started.
var Clock func() time.Duration

func init() {
	QPCTimer := func() func() time.Duration {
		lib, _ := syscall.LoadLibrary("kernel32.dll")
		qpc, _ := syscall.GetProcAddress(lib, "QueryPerformanceCounter")
		qpf, _ := syscall.GetProcAddress(lib, "QueryPerformanceFrequency")
		if qpc == 0 || qpf == 0 {
			return nil
		}

		var freq, start uint64
		syscall.Syscall(qpf, 1, uintptr(unsafe.Pointer(&freq)), 0, 0)
		syscall.Syscall(qpc, 1, uintptr(unsafe.Pointer(&start)), 0, 0)
		if freq <= 0 {
			return nil
		}

		freqns := float64(freq) / 1e9
		return func() time.Duration {
			var now uint64
			syscall.Syscall(qpc, 1, uintptr(unsafe.Pointer(&now)), 0, 0)
			return time.Duration(float64(now-start) / freqns)
		}
	}
	if Clock = QPCTimer(); Clock == nil {
		// Fallback implementation
		start := time.Now()
		Clock = func() time.Duration { return time.Since(start) }
	}
	last = &clock{time.Now(), Clock()}
	go func() {
		for {
			last = &clock{time.Now(), Clock()}
			time.Sleep(time.Hour)
		}
	}()
}
