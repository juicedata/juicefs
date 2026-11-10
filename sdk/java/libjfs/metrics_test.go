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

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/require"
)

func TestPushLabelsRegisterer(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	reg := pushLabelsRegisterer{
		Registerer: prometheus.WrapRegistererWithPrefix("juicefs_", registry),
		labels:     prometheus.Labels{"store": "custom"},
	}
	reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "existing_metric", ConstLabels: prometheus.Labels{"store": "original"},
	}))
	require.NoError(t, reg.Register(prometheus.NewCounter(prometheus.CounterOpts{Name: "optional_metric_total"})))
	require.Equal(t, prometheus.Labels{"store": "custom"}, reg.PushLabels())
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 2)
	require.Equal(t, "juicefs_existing_metric", families[0].GetName())
	require.Len(t, families[0].Metric[0].Label, 1)
	require.Equal(t, "original", families[0].Metric[0].Label[0].GetValue())
	require.Equal(t, "juicefs_optional_metric_total", families[1].GetName())
	require.Empty(t, families[1].Metric[0].Label)
}

type metricsRoundTripper func(*http.Request) (*http.Response, error)

func (f metricsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMetricsPushLabels(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	reg := pushLabelsRegisterer{
		Registerer: prometheus.WrapRegistererWithPrefix("juicefs_", registry),
		labels:     prometheus.Labels{"store": "custom"},
	}
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "existing_metric"})
	gauge.Set(3)
	reg.MustRegister(gauge)
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "optional_metric_total"})
	counter.Add(7)
	require.NoError(t, reg.Register(counter))

	t.Run("Pushgateway", func(t *testing.T) {
		requests := 0
		client := &http.Client{Transport: metricsRoundTripper(func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			requests++
			require.Contains(t, req.URL.Path, "/store/custom")
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(string(body), "\njuicefs_existing_metric 3\n"))
			require.Equal(t, 1, strings.Count(string(body), "\njuicefs_optional_metric_total 7\n"))
			return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
		})}
		pusher := push.New("http://unused.invalid", "juicefs").Gatherer(registry).
			Client(client).Format(expfmt.NewFormat(expfmt.TypeTextPlain))
		for name, value := range reg.PushLabels() {
			pusher.Grouping(name, value)
		}
		require.NoError(t, pusher.Push())
		require.Equal(t, 1, requests)
	})

	t.Run("Graphite", func(t *testing.T) {
		server, err := newMockGraphite("127.0.0.1:0")
		require.NoError(t, err)
		defer server.Close()
		bridge, err := NewBridge(&Config{
			URL: server.Addr().String(), Gatherer: registry, UseTags: true, CommonLabels: reg.PushLabels(),
		})
		require.NoError(t, err)
		require.NoError(t, bridge.Push())
		select {
		case body := <-server.readc:
			require.Len(t, strings.Split(strings.TrimSpace(body), "\n"), 2)
			require.Equal(t, 1, strings.Count(body, "juicefs_existing_metric;store=custom 3 "))
			require.Equal(t, 1, strings.Count(body, "juicefs_optional_metric_total;store=custom 7 "))
		case err := <-server.errc:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatal("no metrics received by Graphite")
		}
	})

	t.Run("remote write", func(t *testing.T) {
		writer, err := NewRemoteWriter(&RemoteWriteConfig{
			URL: "http://unused.invalid", Gatherer: registry, CommonLabels: reg.PushLabels(),
		})
		require.NoError(t, err)
		requests := 0
		writer.client.Transport = metricsRoundTripper(func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			requests++
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			body, err = snappy.Decode(nil, body)
			require.NoError(t, err)
			var batch prompb.WriteRequest
			require.NoError(t, batch.Unmarshal(body))
			require.Len(t, batch.Timeseries, 2)
			values := make(map[string]float64)
			for _, series := range batch.Timeseries {
				labels := make(map[string]string)
				for _, label := range series.Labels {
					require.NotContains(t, labels, label.Name)
					labels[label.Name] = label.Value
				}
				require.Len(t, labels, 2)
				require.Equal(t, "custom", labels["store"])
				require.NotContains(t, values, labels["__name__"])
				require.Len(t, series.Samples, 1)
				values[labels["__name__"]] = series.Samples[0].Value
			}
			require.Equal(t, map[string]float64{"juicefs_existing_metric": 3, "juicefs_optional_metric_total": 7}, values)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		})
		require.NoError(t, writer.Push())
		require.Equal(t, 1, requests)
	})
}
