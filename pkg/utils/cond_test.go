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

// Hold waiters between releasing the lock and receiving their notification.
type condTestLocker struct {
	sync.Mutex
	unlocked chan struct{}
	resume   chan struct{}
}

func (m *condTestLocker) Unlock() {
	m.Mutex.Unlock()
	m.unlocked <- struct{}{}
	<-m.resume
}

func TestCondNotificationBeforeReceive(t *testing.T) {
	for _, tc := range []struct {
		name      string
		waiters   int
		signals   int
		broadcast bool
		timeout   bool
	}{
		{name: "Signal", waiters: 1, signals: 1},
		{name: "MultipleSignals", waiters: 8, signals: 8},
		{name: "SignalOne", waiters: 8, signals: 1},
		{name: "Broadcast", waiters: 8, broadcast: true},
		{name: "SignalAfterTimeout", waiters: 1, signals: 1, timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &condTestLocker{
				unlocked: make(chan struct{}, tc.waiters),
				resume:   make(chan struct{}),
			}
			c := NewCond(m)
			if tc.timeout {
				close(m.resume)
				m.Lock()
				timedOut := c.WaitWithTimeout(0)
				m.Mutex.Unlock()
				if !timedOut {
					t.Fatal("wait without a notification should time out")
				}
				<-m.unlocked
				m.resume = make(chan struct{})
			}
			done := make(chan bool, tc.waiters)
			for i := 0; i < tc.waiters; i++ {
				go func() {
					m.Lock()
					timedOut := c.WaitWithTimeout(100 * time.Millisecond)
					m.Mutex.Unlock()
					done <- timedOut
				}()
			}
			for i := 0; i < tc.waiters; i++ {
				<-m.unlocked
			}
			m.Lock()
			for i := 0; i < tc.signals; i++ {
				c.Signal()
			}
			if tc.broadcast {
				c.Broadcast()
			}
			close(m.resume)
			m.Mutex.Unlock()
			var notified int
			for i := 0; i < tc.waiters; i++ {
				if !<-done {
					notified++
				}
			}
			want := tc.signals
			if tc.broadcast {
				want = tc.waiters
			}
			if notified != want {
				t.Fatalf("notified %d waiters, want %d", notified, want)
			}
		})
	}
}

func TestCondNotificationWithoutWaiters(t *testing.T) {
	var m sync.Mutex
	c := NewCond(&m)
	m.Lock()
	defer m.Unlock()
	for _, notify := range []func(){c.Signal, c.Broadcast} {
		// A notification without waiters must not be saved for a future wait.
		notify()
		if !c.WaitWithTimeout(time.Millisecond) {
			t.Fatal("notification was delivered to a future waiter")
		}
	}
}
