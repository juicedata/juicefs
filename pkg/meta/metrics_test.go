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

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestInitMetrics(t *testing.T) {
	InitMetrics()
	for _, collector := range []prometheus.Collector{txDist, txRestart, opDist} {
		if _, ok := prometheus.Register(collector).(prometheus.AlreadyRegisteredError); !ok {
			t.Fatalf("TestInitMetrics Failed")
		}
	}
}
