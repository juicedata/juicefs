/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	reqsHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "object_request_durations_histogram_seconds",
		Help:    "Object requests latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.01, 1.5, 20),
	}, []string{"method"})
	reqErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "object_request_errors",
		Help: "failed requests to object store",
	})
)

type withMetrics struct {
	ObjectStorage
}

// WithMetrics retuns a object storage that exposes metrics of requests.
func WithMetrics(os ObjectStorage) ObjectStorage {
	return &withMetrics{os}
}

func (p *withMetrics) track(method string, fn func() error) error {
	start := time.Now()
	err := fn()
	used := time.Since(start)
	reqsHistogram.WithLabelValues(method).Observe(used.Seconds())
	if err != nil {
		reqErrors.Add(1)
	}
	return err
}

func (p *withMetrics) Head(key string) (obj *Object, err error) {
	p.track("HEAD", func() error {
		obj, err = p.ObjectStorage.Head(key)
		return err
	})
	return
}

func (p *withMetrics) Get(key string, off, limit int64) (r io.ReadCloser, err error) {
	p.track("GET", func() error {
		r, err = p.ObjectStorage.Get(key, off, limit)
		return err
	})
	return
}

func (p *withMetrics) Put(key string, in io.Reader) error {
	return p.track("PUT", func() error {
		return p.ObjectStorage.Put(key, in)
	})
}

func (p *withMetrics) Delete(key string) error {
	return p.track("DELETE", func() error {
		return p.ObjectStorage.Delete(key)
	})
}

var _ ObjectStorage = &withMetrics{}
