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

package sync

import (
	"regexp"
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

// The sync registerer adds a "juicefs_sync_" prefix (cmd/sync.go); the bare
// names must follow the Prometheus naming convention (lowercase).
func TestInitSyncMetricsNames(t *testing.T) {
	p := utils.NewProgress(true)
	handled = p.AddCountBar("Scanned objects", 0)
	excluded = p.AddCountSpinner("Excluded objects")
	excludedBytes = p.AddByteSpinner("Excluded bytes")
	skipped = p.AddCountSpinner("Skipped objects")
	skippedBytes = p.AddByteSpinner("Skipped bytes")
	extra = p.AddCountSpinner("Extra objects")
	extraBytes = p.AddByteSpinner("Extra bytes")
	pending = p.AddCountSpinner("Pending objects")
	copied = p.AddCountSpinner("Copied objects")
	copiedBytes = p.AddByteSpinner("Copied bytes")
	failed = p.AddCountSpinner("Failed objects")
	deleted = p.AddCountSpinner("Deleted objects")
	checked = p.AddCountSpinner("Checked objects")
	checkedBytes = p.AddByteSpinner("Checked bytes")
	listedPrefix = p.AddCountSpinner("Prefix")

	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_sync_", registry)
	initSyncMetrics(&Config{Registerer: registerer})

	mfs, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	valid := regexp.MustCompile(`^juicefs_sync_[a-z_][a-z0-9_]*$`)
	var invalid []string
	for _, mf := range mfs {
		if !valid.MatchString(mf.GetName()) {
			invalid = append(invalid, mf.GetName())
		}
	}
	if len(invalid) > 0 {
		t.Errorf("metric names violating the Prometheus naming convention: %v", invalid)
	}
}
