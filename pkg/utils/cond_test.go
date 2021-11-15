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

package utils

import (
	"sync"
	"testing"
	"time"
)

func TestCond(t *testing.T) {
	// test Wait and Signal
	var m sync.Mutex
	l := NewCond(&m)
	done := make(chan bool)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		m.Lock()
		wg.Done()
		l.Wait()
		m.Unlock()

		l.Signal()
		done <- true
	}()
	wg.Wait()
	m.Lock()
	l.Signal()
	l.Wait()
	m.Unlock()
	select {
	case <-done:
	case <-time.NewTimer(time.Second).C:
		t.Fatalf("the other goroutine did not return after 1 second")
	}

	// test WaitWithTimeout
	var timeout bool
	go func() {
		m.Lock()
		defer m.Unlock()
		timeout = l.WaitWithTimeout(time.Millisecond * 10)
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
			timeout := l.WaitWithTimeout(time.Second)
			m.Unlock()
			done2 <- timeout
		}()
	}
	wg2.Wait()
	m.Lock()
	l.Broadcast()
	m.Unlock()
	deadline := time.NewTimer(time.Millisecond * 500)
	for i := 0; i < N; i++ {
		select {
		case timeout := <-done2:
			if timeout {
				t.Fatalf("cond should not timeout")
			}
		case <-deadline.C:
			t.Fatalf("not all goroutines wakeup in 500 ms")
		}
	}
}
