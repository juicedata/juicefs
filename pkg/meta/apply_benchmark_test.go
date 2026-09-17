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
	"runtime"
	"testing"
	"time"
)

func BenchmarkApplyContext(b *testing.B) {
	b.Run("TimeNow", func(b *testing.B) {
		var now time.Time
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			now = time.Now()
		}
		runtime.KeepAlive(now)
	})

	b.Run("WithChangelogApplyState", func(b *testing.B) {
		var state *changelogApplyState
		ctx := Background().WithValue(changelogApplyStateKey{}, &changelogApplyState{Time: time.Now()})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			state = getChangelogApplyState(ctx)
			_ = state.Time
		}
		runtime.KeepAlive(state)
	})
}
