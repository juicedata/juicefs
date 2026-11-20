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
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func GetCurrentUID() int {
	return os.Getuid()
}

func GetCurrentGID() int {
	return os.Getgid()
}

func GetCurrentUserSIDStr() string {
	return ""
}

func GetCurrentUserGroupSIDStr() string {
	return ""
}

func IsWinAdminOrElevatedPrivilege() bool {
	return false
}

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

func GetFileInodeNotFollow(path string) (uint64, error) {
	fi, err := os.Lstat(path)
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
	umask, err := ReadUmask()
	if err != nil {
		logger.Warnf("read umask: %s, using 0777", err)
		return 0o777
	}
	return umask
}

// ReadUmask returns the process umask without changing it.
func ReadUmask() (int, error) {
	if runtime.GOOS == "linux" {
		if status, err := os.ReadFile("/proc/self/status"); err == nil {
			if _, value, ok := strings.Cut(string(status), "\nUmask:"); ok {
				line, _, _ := strings.Cut(value, "\n")
				if mask, err := strconv.ParseUint(strings.TrimSpace(line), 8, 9); err == nil {
					return int(mask), nil
				}
			}
		}
	}

	// The child inherits the mask. An empty environment avoids shell startup overrides.
	cmd := exec.Command("/bin/sh", "-c", "umask")
	cmd.Env = []string{}
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("run /bin/sh -c umask: %w", err)
	}
	mask, err := strconv.ParseUint(strings.TrimSpace(string(out)), 8, 9)
	if err != nil {
		return 0, fmt.Errorf("parse output of /bin/sh -c umask: %w", err)
	}
	return int(mask), nil
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
