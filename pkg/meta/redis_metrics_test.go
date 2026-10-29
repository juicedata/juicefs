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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

func newRedisMetricsTestClient(t *testing.T) (*redis.Client, *redisMetricsActivity) {
	t.Helper()
	dialer, dials := redisMetricsDialer()
	c := redis.NewClient(&redis.Options{
		Addr: "unused:6379", Protocol: 2, MaxRetries: -1,
		PoolSize: 1, MaxActiveConns: 1, PoolTimeout: time.Second,
		Dialer: dialer, DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
	})
	t.Cleanup(func() { _ = c.Close() })
	return c, dials
}

func gatherRedisPoolMetrics(t *testing.T, registry *prometheus.Registry) map[string]map[string]float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	expectedTypes := map[string]dto.MetricType{
		"juicefs_redis_pool_connections":                 dto.MetricType_GAUGE,
		"juicefs_redis_pool_idle_connections":            dto.MetricType_GAUGE,
		"juicefs_redis_pool_pending_requests":            dto.MetricType_GAUGE,
		"juicefs_redis_pool_waits_total":                 dto.MetricType_COUNTER,
		"juicefs_redis_pool_wait_duration_seconds_total": dto.MetricType_COUNTER,
		"juicefs_redis_pool_timeouts_total":              dto.MetricType_COUNTER,
	}
	result := make(map[string]map[string]float64)
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "juicefs_redis_pool_") {
			continue
		}
		if want, ok := expectedTypes[family.GetName()]; !ok || family.GetType() != want {
			t.Fatalf("unexpected pool metric name/type: %s %s", family.GetName(), family.GetType())
		}
		values := make(map[string]float64)
		for _, metric := range family.Metric {
			node := ""
			foundNode := false
			for _, label := range metric.Label {
				if label.GetName() == "node" {
					node = label.GetValue()
					foundNode = true
				}
			}
			if !foundNode {
				t.Fatalf("missing node label on %s", family.GetName())
			}
			switch family.GetType() {
			case dto.MetricType_GAUGE:
				values[node] = metric.GetGauge().GetValue()
			case dto.MetricType_COUNTER:
				values[node] = metric.GetCounter().GetValue()
			default:
				t.Fatalf("unexpected metric type: %s", family.GetType())
			}
		}
		result[family.GetName()] = values
	}
	return result
}

func redisMetricsRegistry(t *testing.T, m *redisPoolMetrics) *prometheus.Registry {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	if err := prometheus.WrapRegistererWithPrefix("juicefs_", registry).Register(m); err != nil {
		t.Fatal(err)
	}
	return registry
}

func waitRedisMetrics(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for connection pool stats")
		}
		time.Sleep(time.Millisecond)
	}
}

func testRedisPoolContention(t *testing.T, client *redis.Client, registry *prometheus.Registry, node string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := client.Conn()
	defer conn.Close()
	if err := conn.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Ping(ctx).Err() }()
	waitRedisMetrics(t, func() bool { return client.PoolStats().PendingRequests > 0 })
	values := gatherRedisPoolMetrics(t, registry)
	if values["juicefs_redis_pool_connections"][node] != 1 || values["juicefs_redis_pool_idle_connections"][node] != 0 || values["juicefs_redis_pool_pending_requests"][node] != 1 {
		t.Fatalf("unexpected occupied pool metrics: %v", values)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	values = gatherRedisPoolMetrics(t, registry)
	if values["juicefs_redis_pool_waits_total"][node] < 1 || values["juicefs_redis_pool_wait_duration_seconds_total"][node] <= 0 || values["juicefs_redis_pool_pending_requests"][node] != 0 {
		t.Fatalf("successful wait not exported: %v", values)
	}
	conn = client.Conn()
	defer conn.Close()
	if err := conn.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	before := client.PoolStats()
	if err := client.Ping(ctx).Err(); err != redis.ErrPoolTimeout {
		t.Fatalf("expected pool timeout, got %v", err)
	}
	after := client.PoolStats()
	values = gatherRedisPoolMetrics(t, registry)
	if after.WaitCount != before.WaitCount || after.WaitDurationNs != before.WaitDurationNs || values["juicefs_redis_pool_timeouts_total"][node] <= 0 {
		t.Fatalf("pool timeout was not counted separately from successful waits: %v", values)
	}
}

func TestRedisPoolMetrics(t *testing.T) {
	client, dials := newRedisMetricsTestClient(t)
	metrics := newRedisPoolMetrics()
	metrics.track("", client)
	registry := redisMetricsRegistry(t, metrics)
	testRedisPoolContention(t, client, registry, "")
	values := gatherRedisPoolMetrics(t, registry)
	if len(values) != 6 {
		t.Fatalf("expected six metric families, got %v", values)
	}
	beforeDials, beforeCommands := dials.dials.Load(), dials.commands.Load()
	for range 3 {
		_ = gatherRedisPoolMetrics(t, registry)
	}
	if dials.dials.Load() != beforeDials || dials.commands.Load() != beforeCommands {
		t.Fatal("collecting metrics performed Redis I/O")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	values = gatherRedisPoolMetrics(t, registry)
	if values["juicefs_redis_pool_connections"][""] != 0 || values["juicefs_redis_pool_idle_connections"][""] != 0 {
		t.Fatalf("closed pool connections not zero: %v", values)
	}
}

func TestRedisPoolMetricsCluster(t *testing.T) {
	dialer, dials := redisMetricsDialer()
	var address atomic.Value
	address.Store("node-a:6379")
	var loads atomic.Int64
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"seed:6379"}, Protocol: 2, MaxRetries: -1,
		PoolSize: 1, MaxActiveConns: 1, PoolTimeout: time.Second,
		Dialer: dialer, DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			loads.Add(1)
			return []redis.ClusterSlot{{Start: 0, End: 16383, Nodes: []redis.ClusterNode{{Addr: address.Load().(string)}}}}, nil
		},
	})
	defer client.Close()
	metrics := newRedisPoolMetrics()
	client.OnNewNode(func(node *redis.Client) { metrics.track(node.Options().Addr, node) })
	registry := redisMetricsRegistry(t, metrics)
	if values := gatherRedisPoolMetrics(t, registry); len(values) != 0 {
		t.Fatal(values)
	}
	if loads.Load() != 0 || dials.dials.Load() != 0 {
		t.Fatal("collect loaded cluster topology")
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	var first *redis.Client
	if err := client.ForEachMaster(context.Background(), func(_ context.Context, child *redis.Client) error { first = child; return nil }); err != nil {
		t.Fatal(err)
	}
	testRedisPoolContention(t, first, registry, "node-a:6379")
	beforeLoads, beforeDials, beforeCommands := loads.Load(), dials.dials.Load(), dials.commands.Load()
	_ = gatherRedisPoolMetrics(t, registry)
	if loads.Load() != beforeLoads || dials.dials.Load() != beforeDials || dials.commands.Load() != beforeCommands {
		t.Fatal("collect performed network/topology work")
	}
	address.Store("node-b:6379")
	if err := client.ForEachMaster(context.Background(), func(ctx context.Context, child *redis.Client) error { return child.Ping(ctx).Err() }); err != nil {
		t.Fatal(err)
	}
	values := gatherRedisPoolMetrics(t, registry)
	if _, ok := values["juicefs_redis_pool_connections"]["node-b:6379"]; !ok {
		t.Fatalf("new cluster node missing: %v", values)
	}
	if values["juicefs_redis_pool_waits_total"]["node-b:6379"] != 0 {
		t.Fatal("new node inherited another node's counters")
	}
}

type redisMetricsRejectRegisterer struct {
	prometheus.Registerer
}

func (r redisMetricsRejectRegisterer) Register(collector prometheus.Collector) error {
	if _, ok := collector.(*redisPoolMetrics); ok {
		return errors.New("test registration failure")
	}
	return r.Registerer.Register(collector)
}

func TestRedisPoolMetricsRegistration(t *testing.T) {
	for _, mode := range []string{"standalone", "sentinel"} {
		t.Run(mode, func(t *testing.T) {
			var client *redis.Client
			if mode == "sentinel" {
				dialer, _ := redisMetricsDialer()
				client = redis.NewFailoverClient(&redis.FailoverOptions{
					MasterName: "metadata", SentinelAddrs: []string{"unused:26379"},
					Protocol: 2, MaxRetries: -1, PoolSize: 1, MaxActiveConns: 1,
					PoolTimeout: time.Second, Dialer: dialer, DisableIdentity: true,
				})
			} else {
				client, _ = newRedisMetricsTestClient(t)
			}
			defer client.Close()
			metrics := newRedisPoolMetrics()
			metrics.track("", client)
			m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client, poolMetrics: metrics}
			m.InitMetrics(nil)
			registry := prometheus.NewPedanticRegistry()
			m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.WrapRegistererWith(prometheus.Labels{"test_label": "value"}, registry)))
			if values := gatherRedisPoolMetrics(t, registry); len(values) != 6 {
				t.Fatalf("missing pools: %v", values)
			}
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, family := range families {
				if !strings.HasPrefix(family.GetName(), "juicefs_redis_pool_") {
					continue
				}
				for _, metric := range family.Metric {
					found := false
					for _, label := range metric.Label {
						if label.GetName() == "test_label" && label.GetValue() == "value" {
							found = true
						}
					}
					if !found {
						t.Fatalf("custom label missing from %s", family.GetName())
					}
				}
			}
			testRedisPoolContention(t, client, registry, "")
		})
	}
	for _, label := range []string{"custom", "", "registration error"} {
		t.Run("registration failure/node="+label, func(t *testing.T) {
			client, _ := newRedisMetricsTestClient(t)
			metrics := newRedisPoolMetrics()
			metrics.track("", client)
			m := &redisMeta{baseMeta: newBaseMeta("metrics-test", testConfig()), rdb: client, poolMetrics: metrics}
			registry := prometheus.NewPedanticRegistry()
			var registerer prometheus.Registerer
			if label == "registration error" {
				registerer = redisMetricsRejectRegisterer{Registerer: prometheus.WrapRegistererWithPrefix("juicefs_", registry)}
			} else {
				registerer = prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.WrapRegistererWith(prometheus.Labels{"node": label}, registry))
			}
			m.InitMetrics(registerer)
			if values := gatherRedisPoolMetrics(t, registry); len(values) != 0 {
				t.Fatalf("conflicting collector was registered: %v", values)
			}
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, family := range families {
				if family.GetName() == "juicefs_transaction_durations_histogram_seconds" {
					found = true
				}
			}
			if !found {
				t.Fatal("common metadata metrics missing after pool collector failure")
			}
		})
	}
}

type redisMetricsBlockingCloseConn struct {
	net.Conn
	closing chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *redisMetricsBlockingCloseConn) Close() error {
	c.once.Do(func() {
		close(c.closing)
		<-c.release
	})
	return c.Conn.Close()
}

func TestRedisPoolMetricsLifecycle(t *testing.T) {
	t.Run("slow pool does not block tracking", func(t *testing.T) {
		closing := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		dialer, _ := redisMetricsDialer()
		client := redis.NewClient(&redis.Options{
			Addr: "unused:6379", Protocol: 2, MaxRetries: -1, DisableIdentity: true,
			Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := dialer(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &redisMetricsBlockingCloseConn{Conn: conn, closing: closing, release: release}, nil
			},
			MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
		})
		t.Cleanup(func() { _ = client.Close() })
		if err := client.Ping(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		metrics := newRedisPoolMetrics()
		metrics.track("slow:6379", client)
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			_ = client.Close()
		}()
		defer func() {
			unblock()
			<-closed
		}()
		// Closing a connection holds the pool mutex, so PoolStats must wait.
		<-closing
		collected := make(chan struct{})
		go func() {
			defer close(collected)
			metrics.Collect(make(chan prometheus.Metric, 6))
		}()
		defer func() {
			unblock()
			<-collected
		}()
		waitRedisMetrics(t, func() bool {
			stacks := make([]byte, 1<<20)
			n := runtime.Stack(stacks, true)
			for _, stack := range strings.Split(string(stacks[:n]), "\n\n") {
				if strings.Contains(stack, "(*redisPoolMetrics).Collect") && strings.Contains(stack, "(*Client).PoolStats") {
					return true
				}
			}
			return false
		})
		if !metrics.mu.TryLock() {
			t.Fatal("collecting a blocked pool holds the tracking mutex")
		}
		metrics.mu.Unlock()
		replacement, _ := newRedisMetricsTestClient(t)
		metrics.track("new:6379", replacement)
	})

	metrics := newRedisPoolMetrics()
	registry := redisMetricsRegistry(t, metrics)
	oldClient, _ := newRedisMetricsTestClient(t)
	metrics.track("node:6379", oldClient)
	if err := oldClient.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	replacement, _ := newRedisMetricsTestClient(t)
	metrics.track("node:6379", replacement)
	// A stale collection snapshot must not remove a replacement for this node.
	metrics.untrack("node:6379", weak.Make(oldClient))
	values := gatherRedisPoolMetrics(t, registry)
	if len(values["juicefs_redis_pool_connections"]) != 1 || values["juicefs_redis_pool_connections"]["node:6379"] != 0 {
		t.Fatalf("replaced node was retained: %v", values)
	}
	metrics.mu.Lock()
	metrics.clients["collected:6379"] = weak.Pointer[redis.Client]{}
	metrics.mu.Unlock()
	_ = gatherRedisPoolMetrics(t, registry)
	metrics.mu.Lock()
	_, exists := metrics.clients["collected:6379"]
	metrics.mu.Unlock()
	if exists {
		t.Fatal("collected client was not pruned")
	}
	metrics.mu.Lock()
	metrics.clients["retired:6379"] = weak.Pointer[redis.Client]{}
	metrics.mu.Unlock()
	// Adding another node prunes dead entries even if nobody scrapes metrics.
	metrics.track("node:6379", replacement)
	metrics.mu.Lock()
	_, exists = metrics.clients["retired:6379"]
	metrics.mu.Unlock()
	if exists {
		t.Fatal("adding a node retained a collected client")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				metrics.track("node:6379", replacement)
				if _, err := registry.Gather(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	runtime.KeepAlive(oldClient)
	runtime.KeepAlive(replacement)
}

// Called by the existing real Cluster integration test as well as the offline
// tests above, so pool waits are covered by test.meta.non-core.
func testRedisClusterPoolMetrics(t *testing.T, addr string) {
	t.Helper()
	conf := testConfig()
	conf.Retries = 0
	raw, err := newRedisMeta("redis", addr+"?pool_size=1&max_active_conns=1&pool_timeout=1s", conf)
	if err != nil {
		t.Fatal(err)
	}
	m := raw.(*redisMeta)
	defer m.Shutdown()
	registry := prometheus.NewPedanticRegistry()
	m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
	children := make(chan *redis.Client, 1)
	if err := m.rdb.(*redis.ClusterClient).ForEachMaster(context.Background(), func(_ context.Context, node *redis.Client) error {
		select {
		case children <- node:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var child *redis.Client
	select {
	case child = <-children:
	default:
		t.Fatal("no cluster master found")
	}
	testRedisPoolContention(t, child, registry, child.Options().Addr)
}
