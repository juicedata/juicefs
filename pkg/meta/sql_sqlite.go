//go:build !nosqlite
// +build !nosqlite

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package meta

import (
	"github.com/mattn/go-sqlite3"
)

func isSQLiteDuplicateEntryErr(err error) bool {
	if e, ok := err.(sqlite3.Error); ok {
		return e.Code == sqlite3.ErrConstraint
	}
	return false
}

func init() {
	errBusy = sqlite3.ErrBusy
	dupErrorCheckers = append(dupErrorCheckers, isSQLiteDuplicateEntryErr)
	Register("sqlite3", newSQLMeta)
}
