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

import "fmt"

var (
	version      = "0.17.5"
	revision     = "$Format:%h$"
	revisionDate = "$Format:%as$"
)

// Version returns version in format - `VERSION (REVISIONDATE REVISION)`
// value is assigned in Makefile
func Version() string {
	return fmt.Sprintf("%v (%v %v)", version, revisionDate, revision)
}
