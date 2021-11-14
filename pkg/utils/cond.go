/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"time"
)

// Cond is similar to sync.Cond, but you can wait without a timeout.
type Cond struct {
	L       sync.Locker
	signal  chan bool
	waiters int
}

// Signal wakes up a waiter.
func (c *Cond) Signal() {
	select {
	case c.signal <- true:
	default:
	}
}

// Broadcast wake up all the waiters.
func (c *Cond) Broadcast() {
	for {
		c.L.Lock()
		n := c.waiters
		c.L.Unlock()
		if n == 0 {
			return
		}
		select {
		case c.signal <- true:
		default:
		}
	}
}

// Wait until Signal() or Broadcast() is called.
func (c *Cond) Wait() {
	c.waiters++
	c.L.Unlock()
	defer c.L.Lock()
	<-c.signal
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
		c.waiters--
		t.Stop()
		timerPool.Put(t)
	}()
	defer c.L.Lock()
	select {
	case <-c.signal:
		return false
	case <-t.C:
		return true
	}
}

// NewCond creates a Cond.
func NewCond(lock sync.Locker) *Cond {
	return &Cond{lock, make(chan bool, 1), 0}
}
