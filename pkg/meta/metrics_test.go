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
