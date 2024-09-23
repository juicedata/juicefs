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
		minor:      3,
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

func SetVersion(v string) {
	ver = *Parse(v)
}

func GetVersion() Semver {
	return ver
}

func CompareVersions(v1, v2 *Semver) (int, error) {
	if v1 == nil || v2 == nil {
		return 0, fmt.Errorf("v1 %v and v2 %v can't be nil", v1, v2)
	}
	var less bool
	if v1.major != v2.major {
		less = v1.major < v2.major
	} else if v1.minor != v2.minor {
		less = v1.minor < v2.minor
	} else if v1.patch != v2.patch {
		less = v1.patch < v2.patch
	} else if v1.preRelease != v2.preRelease {
		less = v1.preRelease < v2.preRelease
		if v1.preRelease == "" || v2.preRelease == "" {
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
