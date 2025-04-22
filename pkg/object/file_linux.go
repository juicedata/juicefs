/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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

package object

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// nolint:unused
func getAtime(fi os.FileInfo) time.Time {
	if sst, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(sst.Atim.Unix())
	}
	return fi.ModTime()
}

func lchtimes(name string, atime time.Time, mtime time.Time) error {
	var ts = make([]unix.Timespec, 2)
	// only change mtime
	ts[0] = unix.Timespec{Sec: unix.UTIME_OMIT, Nsec: unix.UTIME_OMIT}
	ts[1] = unix.NsecToTimespec(mtime.UnixNano())

	if e := unix.UtimesNanoAt(unix.AT_FDCWD, name, ts, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return &os.PathError{Op: "lchtimes", Path: name, Err: e}
	}
	return nil
}
