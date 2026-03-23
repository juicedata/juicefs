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
	"fmt"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

func isMySQLDuplicateEntryErr(err error) bool {
	if e, ok := err.(*mysql.MySQLError); ok {
		return e.Number == 1062
	}
	return false
}

// recoveryMysqlPwd escaping is not necessary for mysql password https://github.com/go-sql-driver/mysql#password
func recoveryMysqlPwd(addr string) string {
	colonIndex := strings.Index(addr, ":")
	atIndex := strings.LastIndex(addr, "@")
	if colonIndex != -1 && colonIndex < atIndex {
		pwd := addr[colonIndex+1 : atIndex]
		if parse, err := url.Parse("mysql://root:" + pwd + "@127.0.0.1"); err == nil {
			if originPwd, ok := parse.User.Password(); ok {
				addr = fmt.Sprintf("%s:%s%s", addr[:colonIndex], originPwd, addr[atIndex:])
			}
		}
	}
	return addr
}

func createMySQLEngine(dsn string) (*xorm.Engine, error) {
	cfg, err := mysql.ParseDSN(recoveryMysqlPwd(dsn))
	if err != nil {
		return nil, err
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}

	var engine *xorm.Engine
	for _, key := range []string{"transaction_isolation", "tx_isolation"} {
		cfg.Params[key] = "'repeatable-read'"
		engine, err = xorm.NewEngine("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, fmt.Errorf("unable to create engine: %s", err)
		}

		if err = engine.Ping(); err == nil {
			return engine, nil
		}

		_ = engine.Close()
		delete(cfg.Params, key)

		if !isUnknownTransactionIsolationErr(err, key) {
			return nil, fmt.Errorf("ping database: %s", err)
		}
	}

	return nil, fmt.Errorf("failed to set isolation level: %s", err)
}

func isUnknownTransactionIsolationErr(err error, key string) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), fmt.Sprintf("unknown system variable '%s'", key))
}

func init() {
	dupErrorCheckers = append(dupErrorCheckers, isMySQLDuplicateEntryErr)
	engineCreator["mysql"] = createMySQLEngine
	Register("mysql", newSQLMeta)
}
