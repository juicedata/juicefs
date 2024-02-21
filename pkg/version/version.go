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
		Major:      1,
		Minor:      2,
		Patch:      1,
		PreRelease: "dev",
		Build:      fmt.Sprintf("%s.%s", revisionDate, revision),
	}
)

type Semver struct {
	Major, Minor, Patch uint64
	PreRelease, Build   string
}

func Version() string {
	pr := ver.PreRelease
	if pr != "" {
		pr = "-" + pr
	}
	if strings.Contains(ver.Build, "Format") {
		ver.Build = "unknown"
	}
	return fmt.Sprintf("%d.%d.%d%s+%s", ver.Major, ver.Minor, ver.Patch, pr, ver.Build)
}

func SetVersion(semver Semver) {
	ver = semver
}

func Compare(vs string) (int, error) {
	v := Parse(vs)
	if v == nil {
		return 1, fmt.Errorf("invalid version string: %s", vs)
	}
	var less bool
	if ver.Major != v.Major {
		less = ver.Major < v.Major
	} else if ver.Minor != v.Minor {
		less = ver.Minor < v.Minor
	} else if ver.Patch != v.Patch {
		less = ver.Patch < v.Patch
	} else if ver.PreRelease != v.PreRelease {
		less = ver.PreRelease < v.PreRelease
		if ver.PreRelease == "" || v.PreRelease == "" {
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
		v.PreRelease = vs[p+1:]
		vs = vs[:p]
	}

	ps := strings.Split(vs, ".")
	if len(ps) > 3 {
		return nil
	}
	var err error
	if v.Major, err = strconv.ParseUint(ps[0], 10, 64); err != nil {
		return nil
	}
	if len(ps) > 1 {
		if v.Minor, err = strconv.ParseUint(ps[1], 10, 64); err != nil {
			return nil
		}
	}
	if len(ps) > 2 {
		if v.Patch, err = strconv.ParseUint(ps[2], 10, 64); err != nil {
			return nil
		}
	}
	return &v
}
