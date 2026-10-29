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
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/extra/redisprometheus/v9"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

type redisMetricsActivity struct {
	dials    atomic.Int64
	commands atomic.Int64
}

func redisMetricsDialer() (func(context.Context, string, string) (net.Conn, error), *redisMetricsActivity) {
	activity := new(redisMetricsActivity)
	return func(context.Context, string, string) (net.Conn, error) {
		activity.dials.Add(1)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			r := bufio.NewReader(server)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
				if err != nil {
					return
				}
				args := make([]string, n)
				for i := range args {
					line, err = r.ReadString('\n')
					if err != nil {
						return
					}
					size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
					if err != nil || size < 0 {
						return
					}
					data := make([]byte, size+2)
					if _, err = io.ReadFull(r, data); err != nil {
						return
					}
					args[i] = string(data[:size])
				}
				activity.commands.Add(1)
				reply := "+OK\r\n"
				switch strings.ToLower(args[0]) {
				case "hello":
					reply = "-ERR unknown command 'hello'\r\n"
				case "ping":
					reply = "+PONG\r\n"
				case "sentinel":
					if strings.EqualFold(args[1], "get-master-addr-by-name") {
						reply = "*2\r\n$6\r\nunused\r\n$4\r\n6379\r\n"
					} else {
						reply = "*0\r\n"
					}
				case "subscribe":
					reply = "*3\r\n$9\r\nsubscribe\r\n$14\r\n+switch-master\r\n:1\r\n"
				}
				if _, err = io.WriteString(server, reply); err != nil {
					return
				}
			}
		}()
		return client, nil
	}, activity
}

func newRedisMetricsTestClient(t *testing.T, mode string) (*redis.Client, *redisMetricsActivity) {
	t.Helper()
	dialer, activity := redisMetricsDialer()
	var client *redis.Client
	if mode == "sentinel" {
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName: "metadata", SentinelAddrs: []string{"unused:26379"},
			Protocol: 2, MaxRetries: -1, PoolSize: 1, MaxActiveConns: 1,
			PoolTimeout: 20 * time.Millisecond, Dialer: dialer, DisableIdentity: true,
		})
	} else {
		client = redis.NewClient(&redis.Options{
			Addr: "unused:6379", Protocol: 2, MaxRetries: -1,
			PoolSize: 1, MaxActiveConns: 1, PoolTimeout: 20 * time.Millisecond,
			Dialer: dialer, DisableIdentity: true,
			MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
		})
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, activity
}

func gatherRedisPoolMetrics(t *testing.T, registry *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
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
	if !common {
		t.Fatal("common metadata metrics missing")
	}
	return pools
}

func TestRedisPoolMetrics(t *testing.T) {
	for _, mode := range []string{"standalone", "sentinel"} {
		t.Run(mode, func(t *testing.T) {
			client, activity := newRedisMetricsTestClient(t, mode)
			m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client}
			m.InitMetrics(nil)
			registry := prometheus.NewPedanticRegistry()
			m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.WrapRegistererWith(prometheus.Labels{"node": "custom"}, registry)))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			conn := client.Conn()
			defer conn.Close()
			if err := conn.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			if err := client.Ping(ctx).Err(); err != redis.ErrPoolTimeout {
				t.Fatalf("expected pool timeout, got %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			stats := client.PoolStats()
			pools := gatherRedisPoolMetrics(t, registry)
			expected := []struct {
				name  string
				typ   dto.MetricType
				value float64
			}{
				{"juicefs_redis_pool_hit_total", dto.MetricType_COUNTER, float64(stats.Hits)},
				{"juicefs_redis_pool_miss_total", dto.MetricType_COUNTER, float64(stats.Misses)},
				{"juicefs_redis_pool_timeout_total", dto.MetricType_COUNTER, float64(stats.Timeouts)},
				{"juicefs_redis_pool_conn_total_current", dto.MetricType_GAUGE, 1},
				{"juicefs_redis_pool_conn_idle_current", dto.MetricType_GAUGE, 1},
				{"juicefs_redis_pool_conn_stale_total", dto.MetricType_COUNTER, float64(stats.StaleConns)},
			}
			if len(pools) != len(expected) {
				t.Fatalf("unexpected pool metrics: %v", pools)
			}
			if stats.Hits == 0 || stats.Misses == 0 || stats.Timeouts == 0 {
				t.Fatalf("expected pool reuse, creation and timeout: %+v", stats)
			}
			for _, want := range expected {
				family := pools[want.name]
				if family == nil || family.GetType() != want.typ || len(family.Metric) != 1 {
					t.Fatalf("unexpected metric family %s: %v", want.name, family)
				}
				metric := family.Metric[0]
				value := metric.GetCounter().GetValue()
				if want.typ == dto.MetricType_GAUGE {
					value = metric.GetGauge().GetValue()
				}
				if value != want.value {
					t.Errorf("%s: got %v, want %v", want.name, value, want.value)
				}
				if len(metric.Label) != 1 || metric.Label[0].GetName() != "node" || metric.Label[0].GetValue() != "custom" {
					t.Errorf("custom node label missing on %s: %v", want.name, metric.Label)
				}
			}
			if mode == "standalone" {
				beforeDials, beforeCommands := activity.dials.Load(), activity.commands.Load()
				for range 3 {
					_ = gatherRedisPoolMetrics(t, registry)
				}
				if activity.dials.Load() != beforeDials || activity.commands.Load() != beforeCommands {
					t.Fatal("collecting metrics performed Redis I/O")
				}
			}
		})
	}
}

type redisMetricsPushClient struct {
	requests int
}

func (c *redisMetricsPushClient) Do(*http.Request) (*http.Response, error) {
	c.requests++
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestRedisPoolMetricsPushLabels(t *testing.T) {
	client, _ := newRedisMetricsTestClient(t, "standalone")
	m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client}
	registry := prometheus.NewPedanticRegistry()
	m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
	if pools := gatherRedisPoolMetrics(t, registry); len(pools) != 6 {
		t.Fatal(pools)
	}
	httpClient := &redisMetricsPushClient{}
	err := push.New("http://unused.invalid", "juicefs").Gatherer(registry).
		Grouping("node", "sdk-host").Client(httpClient).Push()
	if err != nil {
		t.Fatalf("push with custom node label: %v", err)
	}
	if httpClient.requests != 1 {
		t.Fatalf("expected one push, got %d", httpClient.requests)
	}
}

func TestRedisPoolMetricsCluster(t *testing.T) {
	dialer, activity := redisMetricsDialer()
	var loads atomic.Int64
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"unused:6379"}, Dialer: dialer,
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			loads.Add(1)
			return nil, errors.New("unexpected topology load")
		},
	})
	defer client.Close()
	m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client}
	registry := prometheus.NewPedanticRegistry()
	m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
	if pools := gatherRedisPoolMetrics(t, registry); len(pools) != 0 {
		t.Fatal(pools)
	}
	if loads.Load() != 0 || activity.dials.Load() != 0 {
		t.Fatal("collecting metrics loaded cluster topology")
	}
}

type redisMetricsRejectRegisterer struct {
	prometheus.Registerer
}

func (r redisMetricsRejectRegisterer) Register(collector prometheus.Collector) error {
	if _, ok := collector.(*redisprometheus.Collector); ok {
		return errors.New("test registration failure")
	}
	return r.Registerer.Register(collector)
}

func TestRedisPoolMetricsRegistration(t *testing.T) {
	client, _ := newRedisMetricsTestClient(t, "standalone")
	m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client}
	registry := prometheus.NewPedanticRegistry()
	m.InitMetrics(redisMetricsRejectRegisterer{prometheus.WrapRegistererWithPrefix("juicefs_", registry)})
	if pools := gatherRedisPoolMetrics(t, registry); len(pools) != 0 {
		t.Fatal(pools)
	}
}
