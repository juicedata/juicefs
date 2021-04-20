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
