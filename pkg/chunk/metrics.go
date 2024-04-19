/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package chunk

import "github.com/prometheus/client_golang/prometheus"

// CacheManager Metrics
type cacheManagerMetrics struct {
	cacheDrops      prometheus.Counter
	cacheWrites     prometheus.Counter
	cacheEvicts     prometheus.Counter
	cacheWriteBytes prometheus.Counter
	cacheWriteHist  prometheus.Histogram
}

func newCacheManagerMetrics(reg prometheus.Registerer) *cacheManagerMetrics {
	metrics := &cacheManagerMetrics{}
	metrics.initMetrics()
	metrics.registerMetrics(reg)
	return metrics
}

func (c *cacheManagerMetrics) registerMetrics(reg prometheus.Registerer) {
	if reg != nil {
		reg.MustRegister(c.cacheDrops)
		reg.MustRegister(c.cacheWrites)
		reg.MustRegister(c.cacheEvicts)
		reg.MustRegister(c.cacheWriteHist)
		reg.MustRegister(c.cacheWriteBytes)
	}
}

func (c *cacheManagerMetrics) initMetrics() {
	c.cacheDrops = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_drops",
		Help: "dropped block",
	})
	c.cacheWrites = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_writes",
		Help: "written cached block",
	})
	c.cacheEvicts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_evicts",
		Help: "evicted cache blocks",
	})
	c.cacheWriteBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_write_bytes",
		Help: "write bytes of cached block",
	})
	c.cacheWriteHist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "blockcache_write_hist_seconds",
		Help:    "write cached block latency distribution",
		Buckets: prometheus.ExponentialBuckets(0.00001, 2, 20),
	})
}
