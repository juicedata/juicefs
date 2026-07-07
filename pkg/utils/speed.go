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

package utils

import (
	"fmt"
	"time"

	"github.com/VividCortex/ewma"
	"github.com/vbauerster/mpb/v7/decor"
)

// speedSampleInterval is the minimum gap between two effective samples. It is
// slightly larger than mpb's 120ms refresh rate so each sampling window is
// stable and the reading does not jitter.
const speedSampleInterval = 300 * time.Millisecond

// realtimeSpeed reports an instantaneous transfer speed (EWMA-smoothed
// Δbytes/Δt between samples) together with the cumulative average speed since
// the first sample. It is only ever called by mpb's render loop (serial per
// bar), so it needs no locking.
type realtimeSpeed struct {
	start    time.Time
	last     int64
	lastTime time.Time
	avg      ewma.MovingAverage
	msg      string
}

func newRealtimeSpeed() *realtimeSpeed {
	r := &realtimeSpeed{avg: ewma.NewMovingAverage()}
	r.msg = r.format(0, 0)
	return r
}

// format renders the instantaneous speed with the cumulative average in
// parentheses, e.g. " 52.9 MiB/s (avg 51.0 MiB/s)".
func (r *realtimeSpeed) format(inst, avg float64) string {
	return fmt.Sprintf("% .1f/s (avg % .1f/s)", decor.SizeB1024(int64(inst)), decor.SizeB1024(int64(avg)))
}

func (r *realtimeSpeed) update(current int64, now time.Time) string {
	if r.lastTime.IsZero() { // first call: record baseline only
		r.start = now
		r.last, r.lastTime = current, now
		return r.msg
	}
	dt := now.Sub(r.lastTime).Seconds()
	if dt < speedSampleInterval.Seconds() { // debounce: reuse last reading
		return r.msg
	}
	delta := current - r.last
	if delta < 0 { // negative delta (e.g. sync rollback) -> clamp to 0
		delta = 0
	}
	r.avg.Add(float64(delta) / dt)
	r.last, r.lastTime = current, now
	var avgSpeed float64
	if el := now.Sub(r.start).Seconds(); el > 0 {
		cur := current
		if cur < 0 {
			cur = 0
		}
		avgSpeed = float64(cur) / el
	}
	r.msg = r.format(r.avg.Value(), avgSpeed)
	return r.msg
}
