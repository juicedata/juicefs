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

package utils

import (
	"sync"
	"testing"
	"time"
)

func TestCond(t *testing.T) {
	// test Wait and Signal
	var m sync.Mutex
	c := NewCond(&m)
	var ready bool
	start := time.Now()
	go func() {
		for i := 0; i < 10; i++ {
			m.Lock()
			ready = true
			c.Signal()
			for ready {
				c.WaitWithTimeout(time.Millisecond * 100)
			}
			m.Unlock()
		}
	}()
	for i := 0; i < 10; i++ {
		m.Lock()
		for !ready {
			c.WaitWithTimeout(time.Millisecond * 100)
		}
		ready = false
		c.Signal()
		m.Unlock()
	}
	if ready {
		t.Fatalf("the work should finish with ready = false")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the work should finish in 1 second")
	}

	// test WaitWithTimeout
	done := make(chan bool)
	var timeout bool
	go func() {
		m.Lock()
		defer m.Unlock()
		timeout = c.WaitWithTimeout(time.Millisecond * 10)
		done <- true
	}()
	select {
	case <-done:
		if !timeout {
			t.Fatalf("it should timeout")
		}
	case <-time.NewTimer(time.Second).C:
		t.Fatalf("wait did not return after 1 second")
	}

	// test Broadcast to wake up all goroutines
	var N = 1000
	done2 := make(chan bool, N)
	var wg2 sync.WaitGroup
	for i := 0; i < N; i++ {
		wg2.Add(1)
		go func() {
			m.Lock()
			wg2.Done()
			timeout := c.WaitWithTimeout(time.Second)
			m.Unlock()
			done2 <- timeout
		}()
	}
	wg2.Wait()
	m.Lock()
	c.Broadcast()
	m.Unlock()
	deadline := time.NewTimer(time.Millisecond * 500)
	for i := 0; i < N; i++ {
		select {
		case timeout := <-done2:
			if timeout {
				t.Fatalf("cond should not timeout")
			}
		case <-deadline.C:
			t.Fatalf("not all goroutines wakeup in 500 ms; i %d", i)
		}
	}
}
