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
		p.Lock()
		if _, ok := p.busy[key]; !ok {
			p.busy[key] = true
			p.Unlock()

			p.op(key)

			p.Lock()
			delete(p.busy, key)
		}
		p.Unlock()
	}
}

func (p *prefetcher) fetch(key string) {
	select {
	case p.pending <- key:
	default:
	}
}
