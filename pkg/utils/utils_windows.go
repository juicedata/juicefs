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
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func GetFileInode(path string) (uint64, error) {
	pathU16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	fd, err := windows.CreateFile(pathU16, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
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

func GetKernelVersion() (major, minor int) { return }

func GetDev(fpath string) int { return -1 }

func GetSysInfo() string {
	sysInfo, _ := exec.Command("systeminfo").Output()
	return string(sysInfo)
}

func GetUmask() int { return 0 }

func SetUmask(umask int) int {
	return 0
}

func ErrnoName(err syscall.Errno) string {
	return strconv.Itoa(int(err))
}
