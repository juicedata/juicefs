/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

// Reference: https://semver.org; NOT strictly followed.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

var (
	revision     = "$Format:%h$" // value is assigned in Makefile
	revisionDate = "$Format:%as$"
	ver          = Semver{
		major:      1,
		minor:      1,
		patch:      0,
		preRelease: "dev",
		build:      fmt.Sprintf("%s.%s", revisionDate, revision),
	}
)

type Semver struct {
	major, minor, patch uint64
	preRelease, build   string
}

func Version() string {
	pr := ver.preRelease
	if pr != "" {
		pr = "-" + pr
	}
	if strings.Contains(ver.build, "Format") {
		ver.build = "unknown"
	}
	return fmt.Sprintf("%d.%d.%d%s+%s", ver.major, ver.minor, ver.patch, pr, ver.build)
}

func Compare(vs string) (int, error) {
	v := Parse(vs)
	if v == nil {
		return 1, fmt.Errorf("invalid version string: %s", vs)
	}
	var less bool
	if ver.major != v.major {
		less = ver.major < v.major
	} else if ver.minor != v.minor {
		less = ver.minor < v.minor
	} else if ver.patch != v.patch {
		less = ver.patch < v.patch
	} else if ver.preRelease != v.preRelease {
		less = ver.preRelease < v.preRelease
		if ver.preRelease == "" || v.preRelease == "" {
			less = !less
		}
	} else {
		return 0, nil
	}
	if less {
		return -1, nil
	} else {
		return 1, nil
	}
}

func Parse(vs string) *Semver {
	if p := strings.Index(vs, "+"); p > 0 {
		vs = vs[:p] // ignore build information
	}
	var v Semver
	if p := strings.Index(vs, "-"); p > 0 {
		v.preRelease = vs[p+1:]
		vs = vs[:p]
	}

	ps := strings.Split(vs, ".")
	if len(ps) > 3 {
		return nil
	}
	var err error
	if v.major, err = strconv.ParseUint(ps[0], 10, 64); err != nil {
		return nil
	}
	if len(ps) > 1 {
		if v.minor, err = strconv.ParseUint(ps[1], 10, 64); err != nil {
			return nil
		}
	}
	if len(ps) > 2 {
		if v.patch, err = strconv.ParseUint(ps[2], 10, 64); err != nil {
			return nil
		}
	}
	return &v
}
