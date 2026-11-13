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

package utils

import (
	"container/list"
	"sync"
	"time"
)

// Cond is similar to sync.Cond, but you can wait with a timeout.
type Cond struct {
	L       sync.Locker
	waiters list.List
}

// Signal wakes up a waiter.
// It's required for the caller to hold L.
func (c *Cond) Signal() {
	if e := c.waiters.Front(); e != nil {
		w := e.Value.(*condWaiter)
		c.waiters.Remove(e)
		w.notified = true
		w.notify <- struct{}{}
	}
}

// Broadcast wake up all the waiters.
// It's required for the caller to hold L.
func (c *Cond) Broadcast() {
	for c.waiters.Len() > 0 {
		c.Signal()
	}
}

type condWaiter struct {
	timer    *time.Timer
	notify   chan struct{}
	notified bool
}

var waiterPool = sync.Pool{}

// WaitWithTimeout wait for a signal or a period of timeout eclipsed.
// returns true in case of timeout else false
func (c *Cond) WaitWithTimeout(d time.Duration) bool {
	var w *condWaiter
	if e := waiterPool.Get(); e == nil {
		w = &condWaiter{timer: time.NewTimer(d), notify: make(chan struct{}, 1)}
	} else {
		w = e.(*condWaiter)
		w.timer.Reset(d)
	}
	waiter := c.waiters.PushBack(w)
	c.L.Unlock()
	timeout := false
	select {
	case <-w.notify:
		if !w.timer.Stop() {
			<-w.timer.C
		}
	case <-w.timer.C:
		timeout = true
	}
	c.L.Lock()
	if w.notified {
		if timeout {
			<-w.notify
		}
		timeout = false
	} else {
		c.waiters.Remove(waiter)
	}
	w.notified = false
	waiterPool.Put(w)
	return timeout
}

// NewCond creates a Cond.
func NewCond(lock sync.Locker) *Cond {
	return &Cond{L: lock}
}
