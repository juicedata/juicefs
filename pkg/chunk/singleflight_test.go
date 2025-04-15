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
	"bytes"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlight(t *testing.T) {
	g := NewController()
	gp := &sync.WaitGroup{}
	var cache sync.Map
	var n int32
	var piggyback atomic.Int64
	iters := 100000
	for i := 0; i < iters; i++ {
		gp.Add(2)
		go func(k int) {
			p, _ := g.Execute(strconv.Itoa(k/100), func() (*Page, error) {
				time.Sleep(time.Microsecond * 500000) // In most cases 500ms is enough to run 100 goroutines
				atomic.AddInt32(&n, 1)
				page := NewOffPage(100)
				copy(page.Data, make([]byte, 100)) // zeroed
				copy(page.Data, strconv.Itoa(k/100))
				return page, nil
			})
			p.Release()
			cache.LoadOrStore(strconv.Itoa(k/100), p)
			gp.Done()
		}(i)
		go func(k int) {
			defer gp.Done()
			page, _ := g.TryPiggyback(strconv.Itoa(k / 100))
			if page != nil {
				expected := make([]byte, 100)
				copy(expected, strconv.Itoa(k/100))
				if bytes.Compare(page.Data, expected) != 0 {
					t.Fatalf("got %x, want %x, key: %d", page.Data, expected, k/100)
				}
				page.Release()
				piggyback.Add(1)
			}
		}(i)
	}
	gp.Wait()

	nv := int(atomic.LoadInt32(&n))
	if nv != iters/100 {
		t.Fatalf("singleflight doesn't take effect: %v", nv)
	}
	if piggyback.Load() == 0 {
		t.Fatal("never piggybacked?")
	}

	// verify the ref
	cache.Range(func(key any, value any) bool {
		if value.(*Page).refs != 0 {
			t.Fatalf("refs of page is not 0, got: %d, key: %s", value.(*Page).refs, key)
		}
		return true
	})

}
