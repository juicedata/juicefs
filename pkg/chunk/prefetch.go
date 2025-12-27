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
	"sync"
)

type prefetcher struct {
	sync.Mutex
	pending chan string
	busy    map[string]bool
	op      func(key string)
}

func newPrefetcher(parallel int, fetch func(string)) *prefetcher {
	p := &prefetcher{
		pending: make(chan string, 10),
		busy:    make(map[string]bool),
		op:      fetch,
	}
	for i := 0; i < parallel; i++ {
		go p.do()
	}
	return p
}

func (p *prefetcher) do() {
	for key := range p.pending {
		p.op(key)

		p.Lock()
		delete(p.busy, key)
		p.Unlock()
	}
}

func (p *prefetcher) fetch(key string) {
	p.Lock()
	defer p.Unlock()
	if _, ok := p.busy[key]; ok {
		return
	}
	select {
	case p.pending <- key:
		p.busy[key] = true
	default:
	}
}
