// Copyright 2025 JuiceFS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
	"google.golang.org/protobuf/proto"
)

// mockLogger implements the Logger interface for testing.
type mockLogger struct {
	messages []string
}

func (m *mockLogger) Println(v ...interface{}) {
	m.messages = append(m.messages, fmt.Sprint(v...))
}

func (m *mockLogger) Warnf(format string, args ...interface{}) {
	m.messages = append(m.messages, fmt.Sprintf(format, args...))
}

func TestNewRemoteWriter(t *testing.T) {
	tests := []struct {
		name    string
		config  *RemoteWriteConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: &RemoteWriteConfig{
				URL: "http://localhost:9090/api/v1/write",
			},
			wantErr: false,
		},
		{
			name: "missing URL",
			config: &RemoteWriteConfig{
				Auth: "user:pass",
			},
			wantErr: true,
			errMsg:  "missing URL",
		},
		{
			name: "with all options",
			config: &RemoteWriteConfig{
				URL:          "http://localhost:9090/api/v1/write",
				Auth:         "user:pass",
				Interval:     5 * time.Second,
				Timeout:      10 * time.Second,
				CommonLabels: map[string]string{"job": "test"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw, err := NewRemoteWriter(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewRemoteWriter() expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("NewRemoteWriter() error = %v, want error containing %v", err, tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("NewRemoteWriter() unexpected error = %v", err)
				return
			}
			if rw == nil {
				t.Errorf("NewRemoteWriter() returned nil")
				return
			}

			// Check defaults
			if rw.url != tt.config.URL {
				t.Errorf("NewRemoteWriter() url = %v, want %v", rw.url, tt.config.URL)
			}
			if tt.config.Timeout == 0 && rw.timeout != defaultRemoteWriteTimeout {
				t.Errorf("NewRemoteWriter() timeout = %v, want %v", rw.timeout, defaultRemoteWriteTimeout)
			}
			if tt.config.Gatherer == nil && rw.gatherer != prometheus.DefaultGatherer {
				t.Errorf("NewRemoteWriter() gatherer should be DefaultGatherer")
			}
		})
	}
}

func TestRemoteWriter_convertMetricsToTimeSeries(t *testing.T) {
	// Create test registry with various metric types
	registry := prometheus.NewRegistry()

	// Counter
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "A test counter",
	})
	counter.Add(5)
	registry.MustRegister(counter)

	// Gauge
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_gauge",
		Help: "A test gauge",
	})
	gauge.Set(10)
	registry.MustRegister(gauge)

	// Histogram
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_histogram",
		Help:    "A test histogram",
		Buckets: []float64{0.1, 0.5, 1.0, 5.0},
	})
	histogram.Observe(0.3)
	histogram.Observe(0.8)
	histogram.Observe(2.0)
	registry.MustRegister(histogram)

	// Summary
	summary := prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "test_summary",
		Help:       "A test summary",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	summary.Observe(0.2)
	summary.Observe(0.6)
	summary.Observe(1.5)
	registry.MustRegister(summary)

	rw := &RemoteWriter{
		commonLabels: map[string]string{"job": "test"},
	}

	mfs, err := registry.Gather()
	if err == nil && rw.commonLabels != nil {
		for _, mf := range mfs {
			for _, metric := range mf.Metric {
				for k, v := range rw.commonLabels {
					metric.Label = append(metric.Label, &dto.LabelPair{
						Name:  proto.String(k),
						Value: proto.String(v),
					})
				}
			}
		}
	}
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	tsList, err := rw.ConvertMetricsToTimeSeries(mfs)
	if err != nil {
		t.Fatalf("convertMetricsToTimeSeries() error = %v", err)
	}

	if len(tsList) == 0 {
		t.Fatalf("convertMetricsToTimeSeries() returned empty time series")
	}

	// Check that we have the expected metrics
	metricNames := make(map[string]bool)
	for _, ts := range tsList {
		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				metricNames[label.Value] = true
				break
			}
		}
	}

	expectedMetrics := []string{"test_counter", "test_gauge", "test_histogram_bucket", "test_histogram_sum", "test_histogram_count", "test_summary", "test_summary_sum", "test_summary_count"}
	for _, expected := range expectedMetrics {
		if !metricNames[expected] {
			t.Errorf("Expected metric %s not found in time series", expected)
		}
	}

	// Check that common labels are added
	for _, ts := range tsList {
		hasJobLabel := false
		for _, label := range ts.Labels {
			if label.Name == "job" && label.Value == "test" {
				hasJobLabel = true
				break
			}
		}
		if !hasJobLabel {
			t.Errorf("Common label 'job=test' not found in time series")
		}
	}
}

func TestRemoteWriter_Push(t *testing.T) {
	// Create a test server
	var receivedData []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Content-Encoding") != "snappy" {
			t.Errorf("Expected snappy encoding")
		}
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("Expected protobuf content type")
		}

		// Read and decompress the body
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedData, _ = snappy.Decode(nil, buf)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create test registry
	registry := prometheus.NewRegistry()
	// Counter
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "A test counter",
	})
	counter.Add(5)
	registry.MustRegister(counter)

	// Gauge
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_gauge",
		Help: "A test gauge",
	})
	gauge.Set(10)
	registry.MustRegister(gauge)

	// Histogram
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_histogram",
		Help:    "A test histogram",
		Buckets: []float64{0.1, 0.5, 1.0, 5.0},
	})
	histogram.Observe(0.3)
	histogram.Observe(0.8)
	histogram.Observe(2.0)
	registry.MustRegister(histogram)

	// Summary
	summary := prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "test_summary",
		Help:       "A test summary",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	summary.Observe(0.2)
	summary.Observe(0.6)
	summary.Observe(1.5)
	registry.MustRegister(summary)

	logger := &mockLogger{}
	rw, err := NewRemoteWriter(&RemoteWriteConfig{
		URL:      server.URL,
		Gatherer: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewRemoteWriter() error = %v", err)
	}

	err = rw.Push()
	if err != nil {
		t.Errorf("Push() error = %v", err)
	}

	if len(receivedData) == 0 {
		t.Errorf("No data received by server")
	}

	// Verify the received data can be unmarshaled
	var wr prompb.WriteRequest
	if err := wr.Unmarshal(receivedData); err != nil {
		t.Errorf("Failed to unmarshal received data: %v", err)
	}

	if len(wr.Timeseries) == 0 {
		t.Errorf("No time series in received data")
	}
}

func TestRemoteWriter_PushWithAuth(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_metric",
		Help: "A test metric",
	})
	counter.Add(1)
	registry.MustRegister(counter)

	rw, err := NewRemoteWriter(&RemoteWriteConfig{
		URL:      server.URL,
		Auth:     "testuser:testpass",
		Gatherer: registry,
	})
	if err != nil {
		t.Fatalf("NewRemoteWriter() error = %v", err)
	}

	err = rw.Push()
	if err != nil {
		t.Errorf("Push() error = %v", err)
	}

	if !strings.Contains(authHeader, "Basic") {
		t.Errorf("Expected Basic auth header, got: %s", authHeader)
	}
}
