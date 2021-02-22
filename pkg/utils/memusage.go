// +build !windows

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
	"bytes"
	"io/ioutil"
	"strconv"
	"syscall"
)

func MemoryUsage() (virt, rss uint64) {
	stat, err := ioutil.ReadFile("/proc/self/stat")
	if err == nil {
		stats := bytes.Split(stat, []byte(" "))
		if len(stats) >= 24 {
			v, _ := strconv.ParseUint(string(stats[22]), 10, 64)
			r, _ := strconv.ParseUint(string(stats[23]), 10, 64)
			return uint64(v), uint64(r) * 4096
		}
	}

	var ru syscall.Rusage
	err = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if err == nil {
		return uint64(ru.Maxrss), uint64(ru.Maxrss)
	}
	return
}
