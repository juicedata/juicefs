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
	"fmt"
	"os"
	"os/exec"

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

func GetKernelVersion() (major, minor int) { return }

func GetDev(fpath string) int { return -1 }

func GetEntry() (string, error) {
	entry, err := exec.Command("systeminfo").Output()
	if err != nil {
		return "", fmt.Errorf("Failed to execute command `systeminfo`: %s", err)
	}
	return string(entry), nil
}
