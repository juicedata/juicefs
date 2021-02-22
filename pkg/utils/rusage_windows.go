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

import "golang.org/x/sys/windows"

type Rusage struct {
	kernel windows.Filetime
	user   windows.Filetime
}

func (ru *Rusage) GetUtime() float64 {
	return float64((int64(ru.user.HighDateTime)<<32)+int64(ru.user.LowDateTime)) / 10 / 1e6
}

func (ru *Rusage) GetStime() float64 {
	return float64((int64(ru.kernel.HighDateTime)<<32)+int64(ru.kernel.LowDateTime)) / 10 / 1e6
}

func GetRusage() *Rusage {
	h := windows.CurrentProcess()
	var creation, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	if err == nil {
		return &Rusage{kernel, user}
	}
	return nil
}
