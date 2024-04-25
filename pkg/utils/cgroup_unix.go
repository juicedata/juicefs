//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
)

var flushRatio = 0.95

const cgroupMemoryDir = "/sys/fs/cgroup/memory/"

func init() {
	if r := os.Getenv("JUICEFS_MEMORY_FLUSH_RATIO"); r != "" {
		ratio, err := strconv.ParseFloat(r, 64)
		if err == nil {
			flushRatio = ratio
		}
	}
}

// Read an int64 from a file
func readUint64(fpath string) (uint64, error) {
	contentAsB, err := os.ReadFile(fpath)
	if err != nil {
		return 0, err
	}
	contentAsStr := strings.TrimSuffix(string(contentAsB), "\n")
	return strconv.ParseUint(contentAsStr, 10, 64)
}

type memoryStat struct {
	usage        uint64 // usage from memory.usage_in_bytes
	rss          uint64 // rss from memory.stat
	cache        uint64 // cache from memory.stat
	inactiveFile uint64 // inactive_file from memory.stat
	dirty        uint64 // dirty page from memory.stat
}

// Memory usage referenced by `kubectl top` and OOM killer, it should match metric `container_memory_working_set_bytes` - see:
// https://github.com/google/cadvisor/blob/80e65740c169abb5097d848f0b44debd0fa20876/container/libcontainer/handler.go#L792
func (s memoryStat) WorkingSet() uint64 {
	workingSet := s.usage
	if workingSet < s.inactiveFile {
		return 0
	}
	return workingSet - s.inactiveFile
}

func (s memoryStat) String() string {
	return humanize.IBytes(s.usage) + " (rss: " + humanize.IBytes(s.rss) + ", cache: " +
		humanize.IBytes(s.cache) + ", dirty: " + humanize.IBytes(s.dirty) + ")"
}

func readMemoryStat() (s memoryStat, err error) {
	statPath := filepath.Join(cgroupMemoryDir, "memory.stat")
	bytes, err := os.ReadFile(statPath)
	if err != nil {
		return
	}
	s, err = parseMemoryStat(string(bytes))
	if err != nil {
		return
	}
	s.usage, err = readUint64(filepath.Join(cgroupMemoryDir, "memory.usage_in_bytes"))
	return
}

func parseMemoryStat(content string) (memoryStat, error) {
	result := memoryStat{}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\n")
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		n, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return result, err
		}

		switch parts[0] {
		case "rss":
			result.rss = n
		case "cache":
			result.cache = n
		case "inactive_file", "total_inactive_file":
			result.inactiveFile = n
		case "dirty":
			result.dirty = n
		}
	}
	return result, nil
}

// flushes dirty pages in container to avoid OOM killer by cgroupv1, see - https://github.com/kubernetes/kubernetes/issues/43916
func FlushDirtyPages() {
	limit, err := readUint64(filepath.Join(cgroupMemoryDir, "memory.limit_in_bytes"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
			// Don't complain if we don't have permission or maybe we are in cgropupv2.
			return
		}
		logger.Errorf("Failed to get memory limit from cgroup: %v", err)
		return
	}
	logger.Debugf("Memory limit from cgroup: %s, flush ratio: %g", humanize.IBytes(uint64(limit)), flushRatio)

	for {
		time.Sleep(10 * time.Second)
		stats, err := readMemoryStat()
		if err != nil {
			logger.Errorf("Failed to get memory stats from cgroup: %v", err)
			return
		}

		if float64(stats.WorkingSet()) > float64(limit)*flushRatio && stats.dirty > 0 {
			// We are about to be OOM killed (in cgroupv1), actively save ourself by flushing dirty pages
			start := time.Now()
			f, err := os.OpenFile(filepath.Join(cgroupMemoryDir, "memory.force_empty"), os.O_WRONLY, 0400)
			if err == nil {
				_, err = f.WriteString("0")
				_ = f.Close()
			}
			logger.Warnf("Memory usage(%s) is above threshold %g (limit %s), flush cache costs %s, err: %v",
				stats, flushRatio, humanize.IBytes(limit), time.Since(start), err)
		}
	}
}
