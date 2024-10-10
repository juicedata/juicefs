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

import "sync"

type request struct {
	wg   sync.WaitGroup
	val  *Page
	dups int
	err  error
}

type Controller struct {
	sync.Mutex
	rs map[string]*request
}

func NewController() *Controller {
	return &Controller{
		rs: make(map[string]*request),
	}
}

func (con *Controller) Execute(key string, fn func() (*Page, error)) (*Page, error) {
	con.Lock()
	if c, ok := con.rs[key]; ok {
		c.dups++
		con.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := new(request)
	c.wg.Add(1)
	con.rs[key] = c
	con.Unlock()

	c.val, c.err = fn()

	con.Lock()
	for i := 0; i < c.dups; i++ {
		// Acquire for the pending Execute
		c.val.Acquire()
	}
	delete(con.rs, key)
	con.Unlock()

	c.wg.Done()

	return c.val, c.err
}

func (con *Controller) TryPiggyback(key string) (*Page, error) {
	con.Lock()
	if c, ok := con.rs[key]; ok {
		c.dups++
		con.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	con.Unlock()
	return nil, nil
}
