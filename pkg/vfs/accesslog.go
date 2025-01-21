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

package vfs

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"strconv"
	"strings"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	opsDurationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fuse_ops_durations_histogram_seconds",
		Help:    "Operations latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
	})
	opsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fuse_ops_total",
		Help: "Total number of operations.",
	}, []string{"method"})
	opsDurations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fuse_ops_durations_seconds",
		Help: "Operations latency in seconds.",
	}, []string{"method"})
	opsIOErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fuse_ops_io_errors",
		Help: "Number of IO errors.",
	}, []string{"errno"})
)

type logReader struct {
	sync.Mutex
	buffer chan []byte
	last   []byte
}

var (
	readerLock sync.RWMutex
	readers    map[uint64]*logReader
)

func init() {
	readers = make(map[uint64]*logReader)
}

func logit(ctx Context, method string, err syscall.Errno, format string, args ...interface{}) {
	used := ctx.Duration()
	opsDurationsHistogram.Observe(used.Seconds())
	opsTotal.WithLabelValues(method).Inc()
	opsDurations.WithLabelValues(method).Add(used.Seconds())
	if err != 0 {
		opsIOErrors.WithLabelValues(utils.ErrnoName(err)).Inc()
	}
	readerLock.RLock()
	defer readerLock.RUnlock()
	if len(readers) == 0 && used < time.Second*10 {
		return
	}
	for i, a := range args {
		switch v := a.(type) {
		case string:
			if !strconv.CanBackquote(v) {
				args[i] = strings.Trim(strconv.Quote(v), "\"")
			}
		}
	}
	cmd := fmt.Sprintf(method+" "+format, args...)
	t := utils.Now()
	ts := t.Format("2006.01.02 15:04:05.000000")
	cmd += fmt.Sprintf(" - %s <%.6f>", strerr(err), used.Seconds())
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
	readerLock.RLock()
	r, ok := readers[fh]
	readerLock.RUnlock()
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
			}
		case <-t.C:
			if n == 0 {
				n = copy(buf, "#\n")
			}
			return n
		}
	}
	return n
}
