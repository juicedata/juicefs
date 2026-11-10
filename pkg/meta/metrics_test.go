/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// The mount registerer adds a "juicefs_" prefix to every metric (cmd.wrapRegister),
// so metric definitions must not embed the prefix themselves, otherwise they are
// exported as "juicefs_juicefs_*" and never match the documented names.
func TestMetricsNotDoublePrefixed(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-metric-names", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}
	b := m.getBase()
	// force label children so the vec names show up in Gather()
	b.bgjobDuration.WithLabelValues("test_job", "ok").Observe(0.1)
	b.bgjobDels.WithLabelValues("test_job").Inc()

	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_", registry)
	m.InitMetrics(registerer)
	m.InitSharedMetrics(registerer)

	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var doubled []string
	found := false
	for _, mf := range mfs {
		name := mf.GetName()
		if strings.Contains(name, "juicefs_juicefs_") {
			doubled = append(doubled, name)
		}
		if name == "juicefs_bgjob_duration_seconds" {
			found = true
		}
	}
	if len(doubled) > 0 {
		t.Errorf("metrics exported with doubled juicefs_ prefix: %s", strings.Join(doubled, ", "))
	}
	if !found {
		t.Error("juicefs_bgjob_duration_seconds is not exported")
	}
}
