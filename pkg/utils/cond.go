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
	"sync/atomic"
	"time"
	"unsafe"
)

// Cond is similar to sync.Cond, but you can wait with a timeout.
type Cond struct {
	signal unsafe.Pointer
	L      sync.Locker
}

func (c *Cond) getChan() chan bool {
	p := atomic.LoadPointer(&c.signal)
	return *(*chan bool)(p)
}

// Signal wakes up a waiter.
// It's allowed but not required for the caller to hold L.
func (c *Cond) Signal() {
	ch := c.getChan()
	select {
	case ch <- true:
	default:
	}
}

// Broadcast wake up all the waiters.
// It's required for the caller to hold L.
func (c *Cond) Broadcast() {
	newCh := make(chan bool)
	old := atomic.SwapPointer(&c.signal, unsafe.Pointer(&newCh))
	close(*(*chan bool)(old))
}

// Wait until Signal() or Broadcast() is called.
func (c *Cond) Wait() {
	c.L.Unlock()
	<-c.getChan()
	c.L.Lock()
}

var timerPool = sync.Pool{
	New: func() interface{} {
		return time.NewTimer(time.Second)
	},
}

// WaitWithTimeout wait for a signal or a period of timeout eclipsed.
// returns true in case of timeout else false
func (c *Cond) WaitWithTimeout(d time.Duration) bool {
	c.L.Unlock()
	t := timerPool.Get().(*time.Timer)
	t.Reset(d)
	defer func() {
		t.Stop()
		timerPool.Put(t)
		c.L.Lock()
	}()
	ch := c.getChan()
	select {
	case <-ch:
		return false
	case <-t.C:
		return true
	}
}

// NewCond creates a Cond.
func NewCond(lock sync.Locker) *Cond {
	ch := make(chan bool)
	return &Cond{unsafe.Pointer(&ch), lock}
}
