/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package vfs

import (
	"fmt"
	"sync"
	"time"
)

const (
	maxLineLength = 1000
)

var (
	readerLock sync.Mutex
	readers    map[uint64]chan []byte
)

func init() {
	readers = make(map[uint64]chan []byte)
}

func logit(ctx Context, format string, args ...interface{}) {
	used := ctx.Duration()
	readerLock.Lock()
	defer readerLock.Unlock()
	if len(readers) == 0 || used > time.Second*10 {
		return
	}

	cmd := fmt.Sprintf(format, args...)
	t := time.Now()
	ts := t.Format("2006.01.02 15:04:05.000000")
	cmd += fmt.Sprintf(" <%.6f>", used.Seconds())
	if ctx.Pid() != 0 && used > time.Second*10 {
		logger.Infof("slow operation: %s", cmd)
	}
	line := []byte(fmt.Sprintf("%s [uid:%d,gid:%d,pid:%d] %s\n", ts, ctx.Uid(), ctx.Gid(), ctx.Pid(), cmd))

	for _, ch := range readers {
		select {
		case ch <- line:
		default:
		}
	}
}

func openAccessLog(fh uint64) uint64 {
	readerLock.Lock()
	defer readerLock.Unlock()
	readers[fh] = make(chan []byte, 1024)
	return fh
}

func closeAccessLog(fh uint64) {
	readerLock.Lock()
	defer readerLock.Unlock()
	delete(readers, fh)
}

func readAccessLog(fh uint64, buf []byte) int {
	readerLock.Lock()
	buffer, ok := readers[fh]
	readerLock.Unlock()
	if !ok {
		return 0
	}
	var n int
	var t = time.NewTimer(time.Second)
	select {
	case l := <-buffer:
		n = copy(buf, l)
		for n+maxLineLength <= len(buf) {
			select {
			case l = <-buffer:
				n += copy(buf[n:], l)
			default:
				return n
			}
		}
		return n
	case <-t.C:
		n = copy(buf, []byte("#\n"))
	}
	return n
}
