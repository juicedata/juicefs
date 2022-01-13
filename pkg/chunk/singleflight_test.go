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

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSingleFlight(t *testing.T) {
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
