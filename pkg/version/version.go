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

package version

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

var (
	version      = "1.0.0-beta"
	revision     = "$Format:%h$"
	revisionDate = "$Format:%as$"
)

func init() {
	if v := Canonical(version); v == "" {
		panic("Invalid version: " + version)
	} else {
		version = v
	}
	// values are assigned in Makefile
	if !strings.Contains(revision, "Format") && !strings.Contains(revisionDate, "Format") {
		version = fmt.Sprintf("%s+%s.%s", version, revisionDate, revision)
	}
}

func Version() string {
	return version
}

func Canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if p := strings.Index(v, "+"); p > 0 {
		v = v[:p]
	}
	return semver.Canonical(v)
}

func Compare(v, w string) int {
	return semver.Compare(Canonical(v), Canonical(w))
}
