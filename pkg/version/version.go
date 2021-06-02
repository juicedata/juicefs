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

package version

import (
	"fmt"
	"strconv"
	"strings"
)

var (
	version      = "0.14-dev"
	revision     = "$Format:%h$"
	revisionDate = "$Format:%as$"
)

// GetFullVersion returns version in format - `VERSION (REVISIONDATE REVISION)`
// value is assigned in Makefile
func GetFullVersion() string {
	return fmt.Sprintf("%v (%v %v)", version, revisionDate, revision)
}

type Version struct {
	Major, Minor, Patch int
}

func ParseVersion(v string) (*Version, error) {
	if v == "" {
		return &Version{}, nil
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid version: %v", v)
	}
	var major, minor, patch int
	var err error
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(parts[1], "-dev") {
		parts[1] = parts[1][:len(parts[1])-4]
		patch = -1
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return nil, err
	}
	if len(parts) > 2 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return nil, err
		}
	}
	return &Version{major, minor, patch}, nil
}

func (v *Version) OlderThan(v2 Version) bool {
	if v.Major < v2.Major {
		return true
	}
	if v.Major > v2.Major {
		return false
	}
	if v.Minor < v2.Minor {
		return true
	}
	if v.Minor > v2.Minor {
		return false
	}
	return v.Patch < v2.Patch
}

func (v *Version) String() string {
	if v.Patch < 0 {
		return fmt.Sprintf("%d.%d-dev", v.Major, v.Minor)
	} else {
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	}
}
