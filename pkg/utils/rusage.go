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

import "syscall"

type Rusage struct {
	syscall.Rusage
}

func (ru *Rusage) GetUtime() float64 {
	return float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
}

func (ru *Rusage) GetStime() float64 {
	return float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
}

func GetRusage() *Rusage {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return &Rusage{ru}
}
