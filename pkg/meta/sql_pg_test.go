//go:build !nopg
// +build !nopg

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
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

func TestPostgresRetrySQLState(t *testing.T) {
	db, err := xorm.NewEngine("pgx", "postgres://localhost/unused?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()
	m := &dbMeta{db: db}
	for _, tc := range []struct {
		code  string
		retry bool
	}{
		{"40001", true},  // serialization failure, including CockroachDB
		{"40P01", true},  // deadlock
		{"23503", false}, // foreign key violation
		{"42501", false}, // insufficient privilege
	} {
		t.Run(tc.code, func(t *testing.T) {
			err := &pgconn.PgError{Code: tc.code, Message: "server-specific transaction error"}
			require.Equal(t, tc.retry, m.shouldRetry(err))
			require.Equal(t, tc.retry, m.shouldRetry(fmt.Errorf("wrapped: %w", err)))
		})
	}
}
