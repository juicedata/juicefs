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

import "syscall"

type Rusage struct {
	syscall.Rusage
}

// GetUtime returns the user time in seconds.
func (ru *Rusage) GetUtime() float64 {
	return float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
}

// GetStime returns the system time in seconds.
func (ru *Rusage) GetStime() float64 {
	return float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
}

// GetRusage returns CPU usage of current process.
func GetRusage() *Rusage {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return &Rusage{ru}
}
