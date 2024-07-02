/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package chunk

import (
	"os"
	"syscall"
	"time"

	sys "golang.org/x/sys/windows"
)

func getAtime(fi os.FileInfo) time.Time {
	stat, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if ok {
		return time.Unix(0, stat.LastAccessTime.Nanoseconds())
	} else {
		return time.Unix(0, 0)
	}
}

func dropOSCache(r ReadCloser) {}

func getNlink(fi os.FileInfo) int {
	return 1
}

func getDiskUsage(path string) (uint64, uint64, uint64, uint64) {
	var freeBytes, total, totalFree uint64
	err := sys.GetDiskFreeSpaceEx(sys.StringToUTF16Ptr(path), &freeBytes, &total, &totalFree)
	if err != nil {
		logger.Errorf("GetDiskFreeSpaceEx %s: %s", path, err.Error())
		return 1, 1, 1, 1
	}
	return total, freeBytes, 1, 1
}

func changeMode(dir string, st os.FileInfo, mode os.FileMode) {}

func inRootVolume(dir string) bool { return false }
