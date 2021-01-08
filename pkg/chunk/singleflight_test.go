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

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSigleFlight(t *testing.T) {
	g := &Controller{}
	gp := &sync.WaitGroup{}
	for i := 0; i < 100000; i++ {
		gp.Add(1)
		go func(k int) {
			p, _ := g.Execute(strconv.Itoa(k/1000), func() (*Page, error) {
				time.Sleep(time.Microsecond * 1000)
				return NewOffPage(100), nil
			})
			p.Release()
			gp.Done()
		}(i)
	}
	gp.Wait()
}
