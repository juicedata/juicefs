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

package chunk

import "sync"

type request struct {
	wg  sync.WaitGroup
	val *Page
	ref int
	err error
}

type Controller struct {
	sync.Mutex
	rs map[string]*request
}

func (con *Controller) Execute(key string, fn func() (*Page, error)) (*Page, error) {
	con.Lock()
	if con.rs == nil {
		con.rs = make(map[string]*request)
	}
	if c, ok := con.rs[key]; ok {
		c.ref++
		con.Unlock()
		c.wg.Wait()
		c.val.Acquire()
		con.Lock()
		c.ref--
		if c.ref == 0 {
			c.val.Release()
		}
		con.Unlock()
		return c.val, c.err
	}
	c := new(request)
	c.wg.Add(1)
	c.ref++
	con.rs[key] = c
	con.Unlock()

	c.val, c.err = fn()
	c.val.Acquire()
	c.wg.Done()

	con.Lock()
	c.ref--
	if c.ref == 0 {
		c.val.Release()
	}
	delete(con.rs, key)
	con.Unlock()

	return c.val, c.err
}
