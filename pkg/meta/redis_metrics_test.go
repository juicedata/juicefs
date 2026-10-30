//go:build !noredis
// +build !noredis

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
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func gatherRedisPoolMetrics(t *testing.T, registry *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	pools := make(map[string]*dto.MetricFamily)
	common := false
	for _, family := range families {
		if strings.HasPrefix(family.GetName(), "juicefs_redis_pool_") {
			pools[family.GetName()] = family
		}
		if family.GetName() == "juicefs_transaction_durations_histogram_seconds" {
			common = true
		}
	}
	require.True(t, common, "common metadata metrics missing")
	return pools
}

type redisMetricsPushClient struct {
	requests int
	path     string
	families map[string]*dto.MetricFamily
}

func (c *redisMetricsPushClient) Do(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	c.requests++
	c.path = req.URL.Path
	c.families = make(map[string]*dto.MetricFamily)
	decoder := expfmt.NewDecoder(req.Body, expfmt.ResponseFormat(req.Header))
	for {
		family := &dto.MetricFamily{}
		if err := decoder.Decode(family); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		c.families[family.GetName()] = family
	}
	return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
}

func TestRedisPoolMetrics(t *testing.T) {
	dialer := func(context.Context, string, string) (net.Conn, error) {
		t.Error("collecting metrics performed Redis I/O")
		return nil, errors.New("unexpected Redis I/O")
	}
	clients := map[string]*redis.Client{
		"standalone": redis.NewClient(&redis.Options{Addr: "unused:6379", Dialer: dialer}),
		"sentinel": redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName: "metadata", SentinelAddrs: []string{"unused:26379"}, Dialer: dialer,
		}),
	}
	for name, client := range clients {
		t.Cleanup(func() { _ = client.Close() })
		t.Run(name, func(t *testing.T) {
			m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client}
			m.InitMetrics(nil)
			registry := prometheus.NewPedanticRegistry()
			m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.WrapRegistererWith(prometheus.Labels{"node": "custom"}, registry)))
			pools := gatherRedisPoolMetrics(t, registry)
			expected := map[string]dto.MetricType{
				"juicefs_redis_pool_hit_total":          dto.MetricType_COUNTER,
				"juicefs_redis_pool_miss_total":         dto.MetricType_COUNTER,
				"juicefs_redis_pool_timeout_total":      dto.MetricType_COUNTER,
				"juicefs_redis_pool_conn_total_current": dto.MetricType_GAUGE,
				"juicefs_redis_pool_conn_idle_current":  dto.MetricType_GAUGE,
				"juicefs_redis_pool_conn_stale_total":   dto.MetricType_COUNTER,
			}
			require.Len(t, pools, len(expected))
			for name, typ := range expected {
				family := pools[name]
				require.NotNil(t, family, name)
				require.Equal(t, typ, family.GetType(), name)
				require.Len(t, family.Metric, 1, name)
				labels := family.Metric[0].Label
				require.Len(t, labels, 1, name)
				require.Equal(t, "node", labels[0].GetName(), name)
				require.Equal(t, "custom", labels[0].GetValue(), name)
			}
		})
	}

	t.Run("push labels", func(t *testing.T) {
		for _, label := range []string{"node", "scope", "store", "stale_read", "type", "metrics_scope"} {
			t.Run(label, func(t *testing.T) {
				m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: clients["standalone"]}
				registry := prometheus.NewPedanticRegistry()
				m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
				pools := gatherRedisPoolMetrics(t, registry)
				require.Len(t, pools, 6)
				for name, family := range pools {
					require.Len(t, family.Metric, 1, name)
					require.Empty(t, family.Metric[0].Label, name)
				}
				httpClient := &redisMetricsPushClient{}
				err := push.New("http://unused.invalid", "juicefs").Gatherer(registry).
					Grouping(label, "sdk-host").Client(httpClient).Push()
				require.NoError(t, err)
				require.Equal(t, 1, httpClient.requests)
				require.Contains(t, httpClient.path, "/"+label+"/sdk-host")
				for name := range pools {
					require.Contains(t, httpClient.families, name)
				}
				require.Contains(t, httpClient.families, "juicefs_transaction_durations_histogram_seconds")
				require.Contains(t, httpClient.families, "juicefs_meta_ops_durations_histogram_seconds")
			})
		}
	})

	t.Run("registration failure", func(t *testing.T) {
		m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: clients["standalone"]}
		registry := prometheus.NewPedanticRegistry()
		counter := prometheus.NewCounter(prometheus.CounterOpts{
			Name: "juicefs_redis_pool_hit_total", Help: "Conflicting metric.",
		})
		counter.Add(7)
		gauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "juicefs_redis_pool_conn_total_current", Help: "Conflicting gauge.",
		})
		gauge.Set(9)
		registry.MustRegister(counter, gauge)
		m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
		pools := gatherRedisPoolMetrics(t, registry)
		require.Len(t, pools, 2)
		require.Equal(t, "Conflicting metric.", pools["juicefs_redis_pool_hit_total"].GetHelp())
		require.Equal(t, float64(7), pools["juicefs_redis_pool_hit_total"].Metric[0].GetCounter().GetValue())
		require.Equal(t, "Conflicting gauge.", pools["juicefs_redis_pool_conn_total_current"].GetHelp())
		require.Equal(t, float64(9), pools["juicefs_redis_pool_conn_total_current"].Metric[0].GetGauge().GetValue())
	})
}
