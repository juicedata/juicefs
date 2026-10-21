//go:build !nosqlite

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestMetaInitMetrics(t *testing.T) {
	sql, err := newSQLMeta("sqlite3", filepath.Join(t.TempDir(), "metrics.db"), testConfig())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sql.Shutdown()) })

	for name, m := range map[string]Meta{
		"sql":   sql,
		"redis": &redisMeta{baseMeta: newBaseMeta("", testConfig())},
		"kv":    &kvMeta{baseMeta: newBaseMeta("", testConfig())},
	} {
		if name != "sql" {
			t.Cleanup(m.getBase().of.close)
		}
		t.Run(name, func(t *testing.T) {
			m.InitMetrics(nil)
			registry := prometheus.NewRegistry()
			m.InitMetrics(registry)
			families, err := registry.Gather()
			require.NoError(t, err)
			metricNames := make(map[string]bool)
			for _, family := range families {
				metricNames[family.GetName()] = true
			}
			require.Contains(t, metricNames, "transaction_durations_histogram_seconds")
			require.Equal(t, name == "sql", metricNames["go_sql_open_connections"])
		})
	}
}
