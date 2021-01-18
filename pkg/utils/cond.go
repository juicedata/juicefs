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

// Cond struct contains a channel and lock used for synchronization
type Cond struct {
	L      sync.Locker
	signal chan bool
}

// Signal sends true to the channel
func (c *Cond) Signal() {
	select {
	case c.signal <- true:
	default:
	}
}

// Broadcast sends true to channel
func (c *Cond) Broadcast() {
	for {
		select {
		case c.signal <- true:
		default:
			return
		}
	}
}

// Wait till signal is recieived
func (c *Cond) Wait() {
	c.L.Unlock()
	defer c.L.Lock()
	<-c.signal
}

var timerPool = sync.Pool{
	New: func() interface{} {
		return time.NewTimer(time.Second)
	},
}

// WaitWithTimeout waits till timeout or message in signal channel
// returns true incase of timeout else false
func (c *Cond) WaitWithTimeout(d time.Duration) bool {
	c.L.Unlock()
	t := timerPool.Get().(*time.Timer)
	t.Reset(d)
	defer func() {
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

// NewCond creates & returns pointer to new Cond
func NewCond(lock sync.Locker) *Cond {
	return &Cond{lock, make(chan bool, 1)}
}
