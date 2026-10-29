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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	tikvmetrics "github.com/tikv/client-go/v2/metrics"
	"google.golang.org/protobuf/proto"
)

type metricsPushClient func(*http.Request) (*http.Response, error)

func (f metricsPushClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
		if result["juicefs_tikv_client_go_backoff_seconds"] != nil {
			t.Error("TiKV backoff metrics should not be registered")
		}
		if result["juicefs_tikv_client_go_rpc_err_total"] != nil {
			t.Error("TiKV RPC error metrics should not be registered")
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
					if _, ok := labels["metrics_scope"]; ok || labels["custom"] != "value" {
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
			if len(first) != 2 {
				t.Errorf("got %d fixture samples, want 2", len(first))
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

	t.Run("SDK PushGateway custom scope label", func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		newMeta("sdk-push-labels").InitMetrics(prometheus.WrapRegistererWithPrefix("juicefs_", registry))
		pusher := push.New("http://pushgateway", "juicefs").Gatherer(registry)
		commonLabels := prometheus.Labels{"vol_name": "sdk-push-labels", "mp": "sdk-test", "metrics_scope": "custom"}
		for name, value := range commonLabels {
			pusher.Grouping(name, value)
		}
		pushed := make(map[string]*dto.MetricFamily)
		pusher.Client(metricsPushClient(func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			if !strings.Contains(req.URL.Path, "/metrics_scope/custom") {
				t.Errorf("custom scope grouping label was lost: %s", req.URL.Path)
			}
			decoder := expfmt.NewDecoder(req.Body, expfmt.FmtProtoDelim)
			for {
				family := new(dto.MetricFamily)
				if err := decoder.Decode(family); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("decode pushed metrics: %v", err)
				}
				pushed[family.GetName()] = family
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}))
		if err := pusher.Push(); err != nil {
			t.Fatalf("push SDK metrics with custom scope label: %v", err)
		}
		if pushed["juicefs_transaction_durations_histogram_seconds"] == nil {
			t.Error("common metadata metrics were not pushed")
		}
		for name, typ := range expected {
			if family := pushed[name]; family == nil || family.GetType() != typ {
				t.Errorf("TiKV metric %s was not pushed", name)
			}
		}
	})

	t.Run("custom scope label", func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		reg := prometheus.WrapRegistererWithPrefix("juicefs_",
			prometheus.WrapRegistererWith(prometheus.Labels{"metrics_scope": "custom"}, registry))
		newMeta("custom-label").InitMetrics(reg)
		families := gather(t, registry)
		for name, typ := range expected {
			family := families[name]
			if family == nil || family.GetType() != typ {
				t.Errorf("TiKV metric %s was lost", name)
				continue
			}
			for _, metric := range family.Metric {
				found := false
				for _, label := range metric.Label {
					if label.GetName() == "metrics_scope" && label.GetValue() == "custom" {
						found = true
					}
				}
				if !found {
					t.Errorf("TiKV metric %s lost custom scope label", name)
				}
			}
		}
	})

	t.Run("skip only conflicting collector", func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		reg := prometheus.WrapRegistererWithPrefix("juicefs_", registry)
		reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tikv_client_go_batch_client_no_available_connection_total",
			Help: "Conflicting metric for testing.",
		}))
		newMeta("conflicting-collector").InitMetrics(reg)
		families := gather(t, registry)
		for name, typ := range expected {
			if name == "juicefs_tikv_client_go_batch_client_no_available_connection_total" {
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
