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

package meta

import (
	"fmt"
	"strconv"
	"strings"
)

type version struct {
	major, minor, patch int
}

var oldestSupportedVer = version{2, 2, 0}

func parseVersion(v string) (ver version, err error) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		err = fmt.Errorf("invalid version: %v", v)
		return
	}
	ver.major, err = strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	ver.minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	ver.patch, err = strconv.Atoi(parts[2])
	return
}

func (ver version) olderThan(v2 version) bool {
	if ver.major < v2.major {
		return true
	}
	if ver.major > v2.major {
		return false
	}
	if ver.minor < v2.minor {
		return true
	}
	if ver.minor > v2.minor {
		return false
	}
	return ver.patch < v2.patch
}

func (ver version) String() string {
	return fmt.Sprintf("%d.%d.%d", ver.major, ver.minor, ver.patch)
}

type redisInfo struct {
	aofEnabled      bool
	clusterEnabled  bool
	maxMemoryPolicy string
	version         string
}

func checkRedisInfo(rawInfo string) (info redisInfo, err error) {
	lines := strings.Split(strings.TrimSpace(rawInfo), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		kvPair := strings.SplitN(l, ":", 2)
		key, val := kvPair[0], kvPair[1]
		switch key {
		case "aof_enabled":
			info.aofEnabled = val == "1"
			if val == "0" {
				logger.Warnf("AOF is not enabled, you may lose data if Redis is not shutdown properly.")
			}
		case "cluster_enabled":
			info.clusterEnabled = val == "1"
			if val != "0" {
				logger.Warnf("Redis cluster is not supported, some operation may fail unexpected.")
			}
		case "maxmemory_policy":
			info.maxMemoryPolicy = val
			if val != "noeviction" {
				logger.Warnf("maxmemory_policy is %q, please set it to 'noeviction'.", val)
			}
		case "redis_version":
			info.version = val
			ver, err := parseVersion(val)
			if err != nil {
				logger.Warnf("Failed to parse Redis server version: %q", ver)
			} else {
				if ver.olderThan(oldestSupportedVer) {
					logger.Warnf("Redis version should not be older than %s", oldestSupportedVer)
				}
			}
		}
	}
	return
}
