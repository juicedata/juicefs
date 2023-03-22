//go:build !nomysql
// +build !nomysql

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

package object

import (
	_ "github.com/go-sql-driver/mysql"
)

func init() {
	Register("mysql", func(addr, user, pass, token string) (ObjectStorage, error) {
		return newSQLStore("mysql", removeScheme(addr), user, pass)
	})
}
