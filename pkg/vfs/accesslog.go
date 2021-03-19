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

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	opsDurationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fuse_ops_durations_histogram_seconds",
		Help:    "Operations latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
	})
)

type logReader struct {
	sync.Mutex
	buffer chan []byte
	last   []byte
}

var (
	readerLock sync.Mutex
	readers    map[uint64]*logReader
)

func init() {
	readers = make(map[uint64]*logReader)
}

func logit(ctx Context, format string, args ...interface{}) {
	used := ctx.Duration()
	opsDurationsHistogram.Observe(used.Seconds())
	readerLock.Lock()
	defer readerLock.Unlock()
	if len(readers) == 0 && used < time.Second*10 {
		return
	}

	cmd := fmt.Sprintf(format, args...)
	t := utils.Now()
	ts := t.Format("2006.01.02 15:04:05.000000")
	cmd += fmt.Sprintf(" <%.6f>", used.Seconds())
	if ctx.Pid() != 0 && used >= time.Second*10 {
		logger.Infof("slow operation: %s", cmd)
	}
	line := []byte(fmt.Sprintf("%s [uid:%d,gid:%d,pid:%d] %s\n", ts, ctx.Uid(), ctx.Gid(), ctx.Pid(), cmd))

	for _, r := range readers {
		select {
		case r.buffer <- line:
		default:
		}
	}
}

func openAccessLog(fh uint64) uint64 {
	readerLock.Lock()
	defer readerLock.Unlock()
	readers[fh] = &logReader{buffer: make(chan []byte, 10240)}
	return fh
}

func closeAccessLog(fh uint64) {
	readerLock.Lock()
	defer readerLock.Unlock()
	delete(readers, fh)
}

func readAccessLog(fh uint64, buf []byte) int {
	readerLock.Lock()
	r, ok := readers[fh]
	readerLock.Unlock()
	if !ok {
		return 0
	}
	r.Lock()
	defer r.Unlock()
	var n int
	if len(r.last) > 0 {
		n = copy(buf, r.last)
		r.last = r.last[n:]
	}
	var t = time.NewTimer(time.Second)
	defer t.Stop()
	for n < len(buf) {
		select {
		case line := <-r.buffer:
			l := copy(buf[n:], line)
			n += l
			if l < len(line) {
				r.last = line[l:]
				return n
			}
		case <-t.C:
			if n == 0 {
				n = copy(buf, []byte("#\n"))
			}
			return n
		}
	}
	return n
}
