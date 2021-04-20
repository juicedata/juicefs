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
