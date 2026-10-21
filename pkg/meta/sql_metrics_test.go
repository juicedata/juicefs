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
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestSQLPoolMetrics(t *testing.T) {
	m, err := newSQLMeta("sqlite3", filepath.Join(t.TempDir(), "metrics.db")+"?max_open_conns=1&max_idle_conns=1", testConfig())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, m.Shutdown()) })
	m.InitMetrics(nil)
	r := prometheus.NewPedanticRegistry()
	reg := prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.WrapRegistererWith(prometheus.Labels{"vol_name": "test"}, r))
	m.InitMetrics(reg)
	assertBaseMetricsRegistered(t, m.getBase(), reg)
	db := m.(*dbMeta).db.DB().DB

	check := func() {
		t.Helper()
		stats := db.Stats()
		want := map[string]struct {
			typ   dto.MetricType
			value float64
		}{
			"max_open_connections":        {dto.MetricType_GAUGE, float64(stats.MaxOpenConnections)},
			"open_connections":            {dto.MetricType_GAUGE, float64(stats.OpenConnections)},
			"in_use_connections":          {dto.MetricType_GAUGE, float64(stats.InUse)},
			"idle_connections":            {dto.MetricType_GAUGE, float64(stats.Idle)},
			"wait_count_total":            {dto.MetricType_COUNTER, float64(stats.WaitCount)},
			"wait_duration_seconds_total": {dto.MetricType_COUNTER, stats.WaitDuration.Seconds()},
			"max_idle_closed_total":       {dto.MetricType_COUNTER, float64(stats.MaxIdleClosed)},
			"max_idle_time_closed_total":  {dto.MetricType_COUNTER, float64(stats.MaxIdleTimeClosed)},
			"max_lifetime_closed_total":   {dto.MetricType_COUNTER, float64(stats.MaxLifetimeClosed)},
		}
		families, err := r.Gather()
		require.NoError(t, err)
		for _, f := range families {
			name, ok := strings.CutPrefix(f.GetName(), "juicefs_go_sql_")
			if !ok {
				continue
			}
			w, ok := want[name]
			require.True(t, ok, "unexpected metric %s", f.GetName())
			require.Equal(t, w.typ, f.GetType(), name)
			require.Len(t, f.Metric, 1, name)
			metric := f.Metric[0]
			value := metric.GetGauge().GetValue()
			if w.typ == dto.MetricType_COUNTER {
				value = metric.GetCounter().GetValue()
			}
			require.Equal(t, w.value, value, name)
			labels := make(map[string]string)
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			require.Equal(t, map[string]string{"db_name": "metadata", "vol_name": "test"}, labels, name)
			delete(want, name)
		}
		require.Empty(t, want, "missing SQL pool metrics")
	}
	check()
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, 1, db.Stats().InUse)
	check()

	// Hold the only connection so another acquisition must wait for the pool.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := db.Conn(ctx)
		if c != nil {
			_ = c.Close()
		}
		done <- err
	}()
	require.Eventually(t, func() bool { return db.Stats().WaitCount == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Positive(t, db.Stats().WaitDuration)
	check()
	require.NoError(t, conn.Close())
	require.Equal(t, 1, db.Stats().Idle)
	check()
	db.SetMaxIdleConns(0)
	require.Equal(t, int64(1), db.Stats().MaxIdleClosed)
	check()

	// Scraping and connection acquisition can run concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := r.Gather(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Error(err)
			break
		}
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
}

func TestNonSQLPoolMetrics(t *testing.T) {
	// Metrics initialization must not access a SQL pool for other engines.
	for name, m := range map[string]Meta{
		"redis": &redisMeta{baseMeta: newBaseMeta("", testConfig())},
		"kv":    &kvMeta{baseMeta: newBaseMeta("", testConfig())},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(m.getBase().of.close)
			m.InitMetrics(nil)
			r := prometheus.NewPedanticRegistry()
			m.InitMetrics(r)
			assertBaseMetricsRegistered(t, m.getBase(), r)
			families, err := r.Gather()
			require.NoError(t, err)
			for _, f := range families {
				require.False(t, strings.HasPrefix(f.GetName(), "go_sql_"), f.GetName())
			}
		})
	}
}

func assertBaseMetricsRegistered(t *testing.T, m *baseMeta, reg prometheus.Registerer) {
	t.Helper()
	for _, c := range []prometheus.Collector{m.txDist, m.txRestart, m.opDist, m.opCount, m.opDuration} {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		require.ErrorAs(t, reg.Register(c), &alreadyRegistered)
	}
}
