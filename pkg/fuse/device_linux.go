// Copyright 2020 Chaos Mesh Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package fuse

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/pkg/errors"

	"golang.org/x/sys/unix"
)

// ensureFuseDev ensures /dev/fuse exists. If not, it will create one
func ensureFuseDev() {
	if _, err := os.Open("/dev/fuse"); os.IsNotExist(err) {
		// 10, 229 according to https://www.kernel.org/doc/Documentation/admin-guide/devices.txt
		fuse := unix.Mkdev(10, 229)
		if err := syscall.Mknod("/dev/fuse", 0o666|syscall.S_IFCHR, int(fuse)); err != nil {
			logger.Errorf("mknod /dev/fuse: %v", err)
		}
	}
}

// grantAccess appends 'c 10:229 rwm' to devices.allow
func grantAccess() error {
	pid := os.Getpid()
	cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)
	cgroupFile, err := os.Open(cgroupPath)
	if err != nil {
		return errors.Wrapf(err, "open %s", cgroupPath)
	}
	defer cgroupFile.Close()

	cgroupScanner := bufio.NewScanner(cgroupFile)
	var deviceCgroup string
	for cgroupScanner.Scan() {
		if err := cgroupScanner.Err(); err != nil {
			return errors.Wrap(err, "read cgroup file")
		}
		var (
			text  = cgroupScanner.Text()
			parts = strings.SplitN(text, ":", 3)
		)
		if len(parts) < 3 {
			return errors.Errorf("invalid cgroup entry: %q", text)
		}

		if parts[1] == "devices" {
			deviceCgroup = parts[2]
		}
	}

	if len(deviceCgroup) == 0 {
		return errors.Errorf("fail to find device cgroup")
	}

	deviceListPath := path.Join("/sys/fs/cgroup/devices" + deviceCgroup, "/devices.list")
	deviceAllowPath := path.Join("/sys/fs/cgroup/devices" + deviceCgroup, "/devices.allow")

	// check if fuse is already allowed
	deviceListFile, err := os.OpenFile(deviceListPath, os.O_RDONLY, 0)
	if err != nil {
		return errors.Wrapf(err, "open %s", deviceListPath)
	}
	defer deviceListFile.Close()
	deviceListScanner := bufio.NewScanner(deviceListFile)
	for deviceListScanner.Scan() {
		if err := deviceListScanner.Err(); err != nil {
			return errors.Wrap(err, "read device list file")
		}
		var (
			text  = deviceListScanner.Text()
			parts = strings.SplitN(text, " ", 3)
		)
		if len(parts) < 3 {
			return errors.Errorf("invalid device list entry: %q", text)
		}

		if (parts[0] == "c" || parts[0] == "a") && (parts[1] == "10:229" || parts[1] == "*:*") && parts[2] == "rwm" {
			logger.Debug("/dev/fuse is already granted")
			// fuse is already allowed
			return nil
		}
	}

	f, err := os.OpenFile(deviceAllowPath, os.O_WRONLY, 0)
	if err != nil {
		return errors.Wrapf(err, "open %s", deviceAllowPath)
	}
	defer f.Close()
	// 10, 229 according to https://www.kernel.org/doc/Documentation/admin-guide/devices.txt
	content := "c 10:229 rwm"
	_, err = f.WriteString(content)
	if err != nil {
		return errors.Wrapf(err, "write %s to %s", content, deviceAllowPath)
	}
	logger.Debug("/dev/fuse is granted")
	return nil
}
