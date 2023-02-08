/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"testing"
	"time"
)

func TestProgresBar(t *testing.T) {
	p := NewProgress(true)
	bar := p.AddCountBar("Bar", 0)
	cp := p.AddCountSpinner("Spinner")
	bp := p.AddByteSpinner("Spinner")
	bar.SetTotal(50)
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond)
		bar.Increment()
		if i%2 == 0 {
			bar.IncrTotal(1)
			cp.Increment()
			bp.IncrInt64(1024)
		}
	}
	bar.Done()
	p.Done()
	if bar.Current() != 100 || cp.Current() != 50 || bp.Current() != 50*1024 {
		t.Fatalf("Final values: bar %d, count %d, bytes: %d", bar.Current(), cp.Current(), bp.Current())
	}

	p = NewProgress(true)
	dp := p.AddDoubleSpinner("Spinner")
	go func() {
		for i := 0; i < 100; i++ {
			time.Sleep(time.Millisecond)
			dp.IncrInt64(1024)
		}
		dp.Done()
	}()
	p.Wait()
	if c, b := dp.Current(); c != 100 || b != 102400 {
		t.Fatalf("Final values: count %d, bytes %d", c, b)
	}
}
