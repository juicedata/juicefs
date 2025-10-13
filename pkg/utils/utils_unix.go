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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func GetFileInode(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if sst, ok := fi.Sys().(*syscall.Stat_t); ok {
		return sst.Ino, nil
	}
	return 0, nil
}

func GetDev(fpath string) int { // ID of device containing file
	fi, err := os.Stat(fpath)
	if err != nil {
		return -1
	}
	if sst, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(sst.Dev)
	}
	return -1
}

func GetKernelInfo() (string, error) {
	kernel, err := exec.Command("uname", "-a").Output()
	if err != nil {
		return "", err
	}

	// Ignore hostname information
	tmp := strings.Split(string(kernel), " ")
	result := strings.Join(append(tmp[:1], tmp[2:]...), " ")
	return result, nil
}

func GetUmask() int {
	umask := syscall.Umask(0)
	syscall.Umask(umask)
	return umask
}

func SetUmask(umask int) int {
	return syscall.Umask(umask)
}

func ErrnoName(err syscall.Errno) string {
	errName := unix.ErrnoName(err)
	if errName == "" {
		errName = strconv.Itoa(int(err))
	}
	return errName
}
