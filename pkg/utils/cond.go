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
	"sync"
	"time"
)

// Cond is similar to sync.Cond, but you can wait with a timeout.
type Cond struct {
	L       sync.Locker
	signal  chan bool
	waiters int
}

// Signal wakes up a waiter.
// It's allowed but not required for the caller to hold L.
func (c *Cond) Signal() {
	select {
	case c.signal <- true:
	default:
	}
}

// Broadcast wake up all the waiters.
// It's required for the caller to hold L.
func (c *Cond) Broadcast() {
	for c.waiters > 0 {
		c.L.Unlock()
		c.Signal()
		c.L.Lock()
	}
}

// Wait until Signal() or Broadcast() is called.
func (c *Cond) Wait() {
	c.waiters++
	c.L.Unlock()
	<-c.signal
	c.L.Lock()
	c.waiters--
}

var timerPool = sync.Pool{
	New: func() interface{} {
		return time.NewTimer(time.Second)
	},
}

// WaitWithTimeout wait for a signal or a period of timeout eclipsed.
// returns true in case of timeout else false
func (c *Cond) WaitWithTimeout(d time.Duration) bool {
	c.waiters++
	c.L.Unlock()
	t := timerPool.Get().(*time.Timer)
	t.Reset(d)
	defer func() {
		t.Stop()
		timerPool.Put(t)
		c.L.Lock()
		c.waiters--

	}()
	select {
	case <-c.signal:
		return false
	case <-t.C:
		return true
	}
}

// NewCond creates a Cond.
func NewCond(lock sync.Locker) *Cond {
	return &Cond{lock, make(chan bool), 0}
}
