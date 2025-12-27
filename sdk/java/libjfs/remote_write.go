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

// Package main provides remote write functionality for pushing Prometheus metrics
// to remote write endpoints.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
	"google.golang.org/protobuf/proto"
)

const (
	defaultRemoteWriteTimeout = 15 * time.Second
)

// RemoteWriteConfig defines the remote write configuration.
type RemoteWriteConfig struct {
	// The URL to push metrics to. Required.
	URL string

	// Basic authentication string in format "username:password". Optional.
	Auth string

	// The interval to use for pushing data. Defaults to 15 seconds.
	Interval time.Duration

	// The timeout for pushing metrics. Defaults to 15 seconds.
	Timeout time.Duration

	// The Gatherer to use for metrics. Defaults to prometheus.DefaultGatherer.
	Gatherer prometheus.Gatherer

	// Common labels to add to all metrics. Optional.
	CommonLabels map[string]string

	// The logger that messages are written to. Defaults to no logging.
	Logger Logger

	// ErrorHandling defines how errors are handled.
	ErrorHandling HandlerErrorHandling
}

// RemoteWriter pushes metrics to the configured remote write endpoint.
type RemoteWriter struct {
	url           string
	gatherer      prometheus.Gatherer
	auth          string
	interval      time.Duration
	timeout       time.Duration
	errorHandling HandlerErrorHandling
	logger        Logger
	commonLabels  map[string]string
	client        *http.Client
}

// NewRemoteWriter returns a pointer to a new RemoteWriter struct.
func NewRemoteWriter(c *RemoteWriteConfig) (*RemoteWriter, error) {
	rw := &RemoteWriter{}

	if c.URL == "" {
		return nil, errors.New("missing URL")
	}
	rw.url = c.URL

	rw.auth = c.Auth

	var z time.Duration
	if c.Interval == z {
		rw.interval = defaultRemoteWriteTimeout
	} else {
		rw.interval = c.Interval
	}

	if c.Timeout == z {
		rw.timeout = defaultRemoteWriteTimeout
	} else {
		rw.timeout = c.Timeout
	}

	if c.Gatherer == nil {
		rw.gatherer = prometheus.DefaultGatherer
	} else {
		rw.gatherer = c.Gatherer
	}

	rw.commonLabels = c.CommonLabels
	rw.logger = c.Logger
	rw.errorHandling = c.ErrorHandling

	rw.client = &http.Client{
		Timeout: rw.timeout,
	}

	return rw, nil
}

// Push pushes Prometheus metrics to the configured remote write endpoint.
func (rw *RemoteWriter) Push() error {
	// Gather metrics from registry
	mfs, err := rw.gatherer.Gather()
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
	if err != nil || len(mfs) == 0 {
		switch rw.errorHandling {
		case AbortOnError:
			return err
		case ContinueOnError:
			if rw.logger != nil {
				rw.logger.Println("continue on error:", err)
			}
		default:
			return err
		}
	}

	// Convert metrics to TimeSeries
	tsList, err := rw.ConvertMetricsToTimeSeries(mfs)
	if err != nil {
		return fmt.Errorf("convert metrics: %w", err)
	}

	if len(tsList) == 0 {
		return nil // No samples to push
	}

	// Send to remote write endpoint
	wr := &prompb.WriteRequest{Timeseries: tsList}
	data, err := wr.Marshal()
	if err != nil {
		return fmt.Errorf("marshal protobuf: %w", err)
	}

	compressed := snappy.Encode(nil, data)
	req, err := http.NewRequest("POST", rw.url, bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	if rw.auth != "" {
		if strings.Contains(rw.auth, ":") {
			parts := strings.Split(rw.auth, ":")
			req.SetBasicAuth(parts[0], parts[1])
		}
	}

	resp, err := rw.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("remote_write failed: %s", resp.Status)
	}

	return nil
}

// ConvertMetricsToTimeSeries converts Prometheus metric families to TimeSeries.
func (rw *RemoteWriter) ConvertMetricsToTimeSeries(mfs []*dto.MetricFamily) ([]prompb.TimeSeries, error) {
	now := model.Time(time.Now().UnixMilli())
	samples, err := expfmt.ExtractSamples(&expfmt.DecodeOptions{
		Timestamp: now,
	}, mfs...)
	if err != nil {
		return nil, fmt.Errorf("extract samples: %w", err)
	}

	var tsList []prompb.TimeSeries
	for _, sample := range samples {
		// Convert model.Metric to prompb.Label slice
		labels := make([]prompb.Label, 0, len(sample.Metric))
		for name, value := range sample.Metric {
			labels = append(labels, prompb.Label{
				Name:  string(name),
				Value: string(value),
			})
		}

		tsList = append(tsList, prompb.TimeSeries{
			Labels: labels,
			Samples: []prompb.Sample{{
				Value:     float64(sample.Value),
				Timestamp: int64(sample.Timestamp),
			}},
		})
	}

	return tsList, nil
}
