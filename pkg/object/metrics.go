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
	dataBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "object_request_data_bytes",
		Help: "Object requests size in bytes.",
	}, []string{"method"})
)

type readCounter struct {
	io.Reader
	method string
}

func newReadCounter(r io.Reader, method string) *readCounter {
	return &readCounter{r, method}
}

// Read update metrics while reading data.
func (counter *readCounter) Read(buf []byte) (int, error) {
	n, err := counter.Reader.Read(buf)
	dataBytes.WithLabelValues(counter.method).Add(float64(n))
	return n, err
}

// WriteTo try to write data into `w` without copying.
func (counter *readCounter) WriteTo(w io.Writer) (n int64, err error) {
	if wt, ok := counter.Reader.(io.WriterTo); ok {
		n, err = wt.WriteTo(w)
	} else {
		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		n, err = io.CopyBuffer(w, counter.Reader, *buf)
	}
	dataBytes.WithLabelValues(counter.method).Add(float64(n))
	return
}

// Close closes the underlying reader
func (counter *readCounter) Close() error {
	if rc, ok := counter.Reader.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

// Seek call the Seek in underlying reader.
func (counter *readCounter) Seek(offset int64, whence int) (int64, error) {
	return counter.Reader.(io.Seeker).Seek(offset, whence)
}

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
	err = p.track("HEAD", func() error {
		obj, err = p.ObjectStorage.Head(key)
		return err
	})
	return
}

func (p *withMetrics) Get(key string, off, limit int64) (r io.ReadCloser, err error) {
	err = p.track("GET", func() error {
		r, err = p.ObjectStorage.Get(key, off, limit)
		if err == nil {
			r = newReadCounter(r, "GET")
		}
		return err
	})
	return
}

func (p *withMetrics) Put(key string, in io.Reader) error {
	return p.track("PUT", func() error {
		return p.ObjectStorage.Put(key, newReadCounter(in, "PUT"))
	})
}

func (p *withMetrics) Delete(key string) error {
	return p.track("DELETE", func() error {
		return p.ObjectStorage.Delete(key)
	})
}

var _ ObjectStorage = &withMetrics{}
