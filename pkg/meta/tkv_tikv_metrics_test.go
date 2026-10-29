//go:build !notikv
// +build !notikv

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
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	tikvmetrics "github.com/tikv/client-go/v2/metrics"
	"google.golang.org/protobuf/proto"
)

func TestTiKVClientMetrics(t *testing.T) {
	// Use distinct children so real TiKV client tests do not affect these samples.
	tikvmetrics.TiKVSendReqHistogram.WithLabelValues("metrics-test", "metrics-test", "false", "general").Observe(0.25)
	tikvmetrics.TiKVBackoffHistogram.WithLabelValues("metrics-test").Observe(0.125)
	tikvmetrics.TiKVRPCErrorCounter.WithLabelValues("metrics-test", "metrics-test").Inc()
	tikvmetrics.TiKVBatchPendingRequests.WithLabelValues("metrics-test").Observe(3)
	t.Cleanup(func() {
		tikvmetrics.TiKVSendReqHistogram.DeleteLabelValues("metrics-test", "metrics-test", "false", "general")
		tikvmetrics.TiKVBackoffHistogram.DeleteLabelValues("metrics-test")
		tikvmetrics.TiKVRPCErrorCounter.DeleteLabelValues("metrics-test", "metrics-test")
		tikvmetrics.TiKVBatchPendingRequests.DeleteLabelValues("metrics-test")
	})

	expected := map[string]dto.MetricType{
		"juicefs_tikv_client_go_request_seconds":                            dto.MetricType_HISTOGRAM,
		"juicefs_tikv_client_go_backoff_seconds":                            dto.MetricType_HISTOGRAM,
		"juicefs_tikv_client_go_rpc_err_total":                              dto.MetricType_COUNTER,
		"juicefs_tikv_client_go_batch_pending_requests":                     dto.MetricType_HISTOGRAM,
		"juicefs_tikv_client_go_batch_client_wait_connection_establish":     dto.MetricType_HISTOGRAM,
		"juicefs_tikv_client_go_batch_client_no_available_connection_total": dto.MetricType_COUNTER,
	}
	newMeta := func(volume string) *kvMeta {
		return &kvMeta{
			baseMeta: newBaseMeta(volume, testConfig()),
			client:   withPrefix(&tikvClient{}, []byte(volume)),
		}
	}
	gather := func(t *testing.T, registry *prometheus.Registry) map[string]*dto.MetricFamily {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatalf("gather metrics: %v", err)
		}
		result := make(map[string]*dto.MetricFamily)
		for _, family := range families {
			result[family.GetName()] = family
		}
		if result["juicefs_transaction_durations_histogram_seconds"] == nil {
			t.Error("common metadata metrics were not registered")
		}
		return result
	}

	t.Run("private registries share process metrics", func(t *testing.T) {
		first := make(map[string]*dto.Metric)
		for _, volume := range []string{"volume-a", "volume-b"} {
			registry := prometheus.NewPedanticRegistry()
			reg := prometheus.WrapRegistererWithPrefix("juicefs_",
				prometheus.WrapRegistererWith(prometheus.Labels{"custom": "value"}, registry))
			m := newMeta(volume)
			m.InitMetrics(reg)
			families := gather(t, registry)
			count := 0
			for name, family := range families {
				if !strings.HasPrefix(name, "juicefs_tikv_client_go_") {
					continue
				}
				count++
				if typ, ok := expected[name]; !ok || family.GetType() != typ {
					t.Errorf("unexpected metric family %s with type %s", name, family.GetType())
				}
				for _, metric := range family.Metric {
					labels := make(map[string]string)
					for _, label := range metric.Label {
						labels[label.GetName()] = label.GetValue()
					}
					if labels["metrics_scope"] != "process" || labels["custom"] != "value" {
						t.Errorf("metric %s has unexpected labels: %v", name, labels)
					}
					if labels["type"] == "metrics-test" || labels["store"] == "metrics-test" {
						if previous := first[name]; previous != nil && !proto.Equal(previous, metric) {
							t.Errorf("process sample %s differs between volume registries", name)
						}
						first[name] = metric
					}
				}
			}
			if count != len(expected) {
				t.Errorf("got %d TiKV metric families, want %d", count, len(expected))
			}
			if len(first) != 4 {
				t.Errorf("got %d fixture samples, want 4", len(first))
			}
			// Retrying backend registration must not panic or replace collectors.
			m.client.(tkvMetricsClient).initMetrics(reg)
			gather(t, registry)
		}
	})

	t.Run("nil registerer", func(t *testing.T) {
		newMeta("nil-registry").InitMetrics(nil)
		(&tikvClient{}).initMetrics(nil)
	})

	t.Run("conflicting scope label", func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		reg := prometheus.WrapRegistererWithPrefix("juicefs_",
			prometheus.WrapRegistererWith(prometheus.Labels{"metrics_scope": "custom"}, registry))
		newMeta("conflicting-label").InitMetrics(reg)
		for name := range gather(t, registry) {
			if strings.HasPrefix(name, "juicefs_tikv_client_go_") {
				t.Errorf("conflicting metric %s was registered", name)
			}
		}
	})

	t.Run("skip only conflicting collector", func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		reg := prometheus.WrapRegistererWithPrefix("juicefs_", registry)
		reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tikv_client_go_rpc_err_total",
			Help: "Conflicting metric for testing.",
		}))
		newMeta("conflicting-collector").InitMetrics(reg)
		families := gather(t, registry)
		for name, typ := range expected {
			if name == "juicefs_tikv_client_go_rpc_err_total" {
				typ = dto.MetricType_GAUGE
			}
			if family := families[name]; family == nil || family.GetType() != typ {
				t.Errorf("metric %s was lost or replaced", name)
			}
		}
	})

	t.Run("other KV backends", func(t *testing.T) {
		m, err := newKVMeta("memkv", "metrics-test", testConfig())
		if err != nil {
			t.Fatal(err)
		}
		registry := prometheus.NewPedanticRegistry()
		m.InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
		for name := range gather(t, registry) {
			if strings.HasPrefix(name, "juicefs_tikv_client_go_") {
				t.Errorf("TiKV metric %s registered for memkv", name)
			}
		}
	})
}
