//go:build !nomysql
// +build !nomysql

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

package meta

import (
	"github.com/go-sql-driver/mysql"
)

func isMySQLDuplicateEntryErr(err error) bool {
	if e, ok := err.(*mysql.MySQLError); ok {
		return e.Number == 1062
	}
	return false
}

func setMySQLTransactionIsolation(dns string) (string, error) {
	cfg, err := mysql.ParseDSN(dns)
	if err != nil {
		return "", err
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["transaction_isolation"] = "'repeatable-read'"
	return cfg.FormatDSN(), nil
}

func init() {
	dupErrorCheckers = append(dupErrorCheckers, isMySQLDuplicateEntryErr)
	setTransactionIsolation = setMySQLTransactionIsolation
	Register("mysql", newSQLMeta)
}
