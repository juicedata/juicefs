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
	"maps"
	"sync"
	"weak"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type redisPoolMetrics struct {
	mu      sync.Mutex
	clients map[string]weak.Pointer[redis.Client]
	descs   [6]*prometheus.Desc
}

func newRedisPoolMetrics() *redisPoolMetrics {
	metrics := [6][2]string{
		{"redis_pool_connections", "Number of connections in the Redis command pool."},
		{"redis_pool_idle_connections", "Number of idle connections in the Redis command pool."},
		{"redis_pool_pending_requests", "Number of requests acquiring a Redis command connection."},
		{"redis_pool_waits_total", "Total number of waits that acquired a Redis command pool permit."},
		{"redis_pool_wait_duration_seconds_total", "Total time spent waiting to acquire a Redis command pool permit."},
		{"redis_pool_timeouts_total", "Total number of Redis command connection pool wait timeouts."},
	}
	m := &redisPoolMetrics{clients: make(map[string]weak.Pointer[redis.Client])}
	for i, metric := range metrics {
		m.descs[i] = prometheus.NewDesc(metric[0], metric[1], []string{"node"}, nil)
	}
	return m
}

func (m *redisPoolMetrics) track(node string, client *redis.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for node, ref := range m.clients {
		if ref.Value() == nil {
			delete(m.clients, node)
		}
	}
	m.clients[node] = weak.Make(client)
}

func (m *redisPoolMetrics) untrack(node string, ref weak.Pointer[redis.Client]) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clients[node] == ref {
		delete(m.clients, node)
	}
}

func (m *redisPoolMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.descs {
		ch <- desc
	}
}

func (m *redisPoolMetrics) Collect(ch chan<- prometheus.Metric) {
	// Read individual pools: ClusterClient.PoolStats omits wait statistics and
	// can refresh cluster topology. Weak pointers avoid retaining retired nodes.
	m.mu.Lock()
	clients := maps.Clone(m.clients)
	m.mu.Unlock()
	for node, ref := range clients {
		client := ref.Value()
		if client == nil {
			m.untrack(node, ref)
			continue
		}
		stat := client.PoolStats()
		values := [6]float64{
			float64(stat.TotalConns), float64(stat.IdleConns), float64(stat.PendingRequests),
			float64(stat.WaitCount), float64(stat.WaitDurationNs) / 1e9, float64(stat.Timeouts),
		}
		for i, value := range values {
			typ := prometheus.GaugeValue
			if i >= 3 {
				typ = prometheus.CounterValue
			}
			ch <- prometheus.MustNewConstMetric(m.descs[i], typ, value, node)
		}
	}
}

func (m *redisMeta) InitMetrics(reg prometheus.Registerer) {
	m.baseMeta.InitMetrics(reg)
	if reg != nil {
		if err := reg.Register(m.poolMetrics); err != nil {
			logger.Warnf("Register Redis connection pool metrics: %s", err)
		}
	}
}
