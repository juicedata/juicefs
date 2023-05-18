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

func GetSysInfo() string {
	var (
		kernel    string
		osVersion []byte
		hardware  []byte
	)

	kernel, _ = GetKernelInfo()

	osVersion, _ = exec.Command("sw_vers").Output()

	hardware, _ = exec.Command("system_profiler", "SPMemoryDataType", "SPStorageDataType").Output()

	return fmt.Sprintf(`
Kernel: 
%s
OS: 
%s
Hardware: 
%s`, kernel, string(osVersion), string(hardware))
}
