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

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlight(t *testing.T) {
	g := &Controller{}
	gp := &sync.WaitGroup{}
	var cache sync.Map
	var n int32
	iters := 100000
	for i := 0; i < iters; i++ {
		gp.Add(1)
		go func(k int) {
			p, _ := g.Execute(strconv.Itoa(k/1000), func() (*Page, error) {
				time.Sleep(time.Microsecond * 50000) // In most cases 50ms is enough to run 1000 goroutines
				atomic.AddInt32(&n, 1)
				return NewOffPage(100), nil
			})
			p.Release()
			cache.LoadOrStore(strconv.Itoa(k/1000), p)
			gp.Done()
		}(i)
	}
	gp.Wait()

	nv := int(atomic.LoadInt32(&n))
	if nv != iters/1000 {
		t.Fatalf("singleflight doesn't take effect: %v", nv)
	}

	// verify the ref
	cache.Range(func(key any, value any) bool {
		if value.(*Page).refs != 0 {
			t.Fatal("refs of page is not 0")
		}
		return true
	})

}
