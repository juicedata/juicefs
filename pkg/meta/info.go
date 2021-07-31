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

type redisVersion struct {
	ver          string
	major, minor int
}

var oldestSupportedVer = redisVersion{"2.2.x", 2, 2}

func parseRedisVersion(v string) (ver redisVersion, err error) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		err = fmt.Errorf("invalid redisVersion: %v", v)
		return
	}
	ver.ver = v
	ver.major, err = strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	ver.minor, err = strconv.Atoi(parts[1])
	return
}

func (ver redisVersion) olderThan(v2 redisVersion) bool {
	if ver.major < v2.major {
		return true
	}
	if ver.major > v2.major {
		return false
	}
	return ver.minor < v2.minor
}

func (ver redisVersion) String() string {
	return ver.ver
}

type redisInfo struct {
	aofEnabled      bool
	clusterEnabled  bool
	maxMemoryPolicy string
	redisVersion    string
}

func checkRedisInfo(rawInfo string) (info redisInfo, err error) {
	lines := strings.Split(strings.TrimSpace(rawInfo), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		kvPair := strings.SplitN(l, ":", 2)
		if len(kvPair) < 2 {
			continue
		}
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
			info.redisVersion = val
			ver, err := parseRedisVersion(val)
			if err != nil {
				logger.Warnf("Failed to parse Redis server version %q: %s", ver, err)
			} else {
				if ver.olderThan(oldestSupportedVer) {
					logger.Warnf("Redis version should not be older than %s", oldestSupportedVer)
				}
			}
		}
	}
	return
}
