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
	"os"

	"golang.org/x/sys/windows"
)

func GetFileInode(path string) (uint64, error) {
	// FIXME support directory
	fd, err := windows.Open(path, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer windows.Close(fd)
	var data windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(fd, &data)
	if err != nil {
		return 0, err
	}
	return uint64(data.FileIndexHigh)<<32 + uint64(data.FileIndexLow), nil
}
