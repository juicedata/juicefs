/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package fs

import "github.com/prometheus/client_golang/prometheus"

var (
	readSizeHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sdk_read_size_bytes",
		Help:    "size of read distributions.",
		Buckets: prometheus.LinearBuckets(4096, 4096, 32),
	})
	writtenSizeHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sdk_written_size_bytes",
		Help:    "size of write distributions.",
		Buckets: prometheus.LinearBuckets(4096, 4096, 32),
	})
	opsDurationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sdk_ops_durations_histogram_seconds",
		Help:    "Operations latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
	})
)
