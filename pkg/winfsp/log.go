//go:build windows
// +build windows

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package winfsp

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/utils"
)

const RotateAccessLog = 300 << 20 // 300 MiB

func (j *juice) log(ctx fs.LogContext, format string, args ...interface{}) {
	var failed bool
	for _, a := range args {
		if eno, ok := a.(syscall.Errno); ok && eno == syscall.EIO {
			failed = true
		}
	}
	j.logM.Lock()
	buffer := j.logBuffer
	j.logM.Unlock()
	if buffer == nil && !failed {
		return
	}
	now := utils.Now()
	cmd := fmt.Sprintf(format, args...)
	ts := now.Format("2006.01.02 15:04:05.000000")
	used := ctx.Duration()
	cmd += fmt.Sprintf(" <%.6f>", used.Seconds())
	line := fmt.Sprintf("%s [uid:%d,gid:%d,pid:%d] %s\n", ts, ctx.Uid(), ctx.Gid(), ctx.Pid(), cmd)
	if failed {
		logger.Errorf("failed operation: %s", line)
	}
	if buffer == nil {
		return
	}
	select {
	case buffer <- line:
	default:
		logger.Debugf("log dropped: %s", line[:len(line)-1])
	}
}

func (fs *juice) flushLog(f *os.File, path string, rotateCount int) {
	buf := make([]byte, 0, 128<<10)
	var lastcheck = time.Now()
	numFiles := rotateCount

	for {
		line := <-fs.logBuffer
		buf = append(buf[:0], []byte(line)...)
	LOOP:
		for len(buf) < (128 << 10) {
			select {
			case line = <-fs.logBuffer:
				buf = append(buf, []byte(line)...)
			default:
				break LOOP
			}
		}
		_, err := f.Write(buf)
		if err != nil {
			logger.Errorf("write access log: %s", err)
			break
		}
		if lastcheck.Add(time.Minute).After(time.Now()) {
			continue
		}
		lastcheck = time.Now()
		fi, err := f.Stat()
		if err != nil {
			logger.Errorf("stat access log: %s", err)
			continue
		}
		if fi.Size() > RotateAccessLog {
			_ = f.Close()
			fi, err = os.Stat(path)
			if err == nil && fi.Size() > RotateAccessLog {
				tmp := fmt.Sprintf("%s.%p", path, fs)
				if os.Rename(path, tmp) == nil {
					for i := numFiles - 1; i > 0; i-- {
						_ = os.Rename(path+"."+strconv.Itoa(i), path+"."+strconv.Itoa(i+1))
					}
					_ = os.Rename(tmp, path+".1")
				} else {
					fi, err = os.Stat(path)
					if err == nil && fi.Size() > RotateAccessLog*int64(numFiles) {
						logger.Infof("can't rename %s, truncate it", path)
						_ = os.Truncate(path, 0)
					}
				}
			}
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				logger.Errorf("open %s: %s", path, err)
				break
			}
			_ = os.Chmod(path, 0666)
		}
	}
}
