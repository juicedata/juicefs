/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"os/exec"
)

func GetKernelVersion() (major, minor int) { return }

func GetSysInfo() (string, error) {
	var (
		kernel    string
		osVersion []byte
		hardware  []byte
		err       error
	)

	if kernel, err = GetKernelInfo(); err != nil {
		return "", fmt.Errorf("failed to execute command `uname`: %s", err)
	}

	if osVersion, err = exec.Command("sw_vers").Output(); err != nil {
		return "", fmt.Errorf("failed to execute command `sw_vers`: %s", err)
	}

	if hardware, err = exec.Command("system_profiler", "SPMemoryDataType", "SPStorageDataType").Output(); err != nil {
		return "", fmt.Errorf("failed to execute command `system_profiler`: %s", err)
	}

	return fmt.Sprintf(`
Kernel: 
%s
OS: 
%s
Hardware: 
%s`, kernel, string(osVersion), string(hardware)), nil
}
