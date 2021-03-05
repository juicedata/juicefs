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

package meta

import "github.com/prometheus/client_golang/prometheus"

var (
	redisTxDist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "juicefs_redis_tx_durations_histogram_seconds",
		Help:    "Redis transactions latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.0001, 1.5, 30),
	})
	redisTxRestart = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "juicefs_redis_transaction_restart",
		Help: "The number of times a Redis transaction is restarted.",
	})
)

func init() {
	prometheus.MustRegister(redisTxDist)
	prometheus.MustRegister(redisTxRestart)
}
