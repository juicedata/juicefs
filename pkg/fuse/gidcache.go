/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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

package fuse

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type cItem struct {
	gids   []uint32
	expire time.Time
}

type gidCache struct {
	sync.Mutex
	groups  map[uint32]*cItem
	cacheto time.Duration
}

func newGidCache(cacheto time.Duration) *gidCache {
	g := &gidCache{
		groups:  make(map[uint32]*cItem),
		cacheto: cacheto,
	}
	go g.cleanup()
	return g
}

func (g *gidCache) cleanup() {
	for {
		g.Lock()
		now := time.Now()
		for k, gs := range g.groups {
			if gs.expire.Before(now) {
				delete(g.groups, k)
			}
		}
		g.Unlock()
		time.Sleep(time.Second * 10)
	}
}

func findProcessGroups(pid, gid uint32) []uint32 {
	if runtime.GOOS == "darwin" {
		return []uint32{gid}
	}
	path := fmt.Sprintf("/proc/%d/status", pid)
	f, err := os.Open(path)
	if err != nil {
		return []uint32{gid}
	}
	defer f.Close()
	buf, err := io.ReadAll(f)
	if err != nil {
		return []uint32{gid}
	}

	p := bytes.Index(buf, []byte("Groups:"))
	if p < 0 {
		return []uint32{gid}
	}
	buf = buf[p+7:]
	last := bytes.IndexByte(buf, '\n')
	if last >= 0 {
		buf = buf[:last]
	}
	parts := bytes.Split(buf, []byte(" "))
	gids := []uint32{gid}
	for _, p := range parts {
		g, err := strconv.Atoi(string(bytes.TrimSpace(p)))
		if err == nil && uint32(g) != gid {
			gids = append(gids, uint32(g))
		}
	}
	return gids
}

func (g *gidCache) get(pid, gid uint32) []uint32 {
	if g.cacheto == 0 || pid == 0 || gid == 0 {
		return []uint32{gid}
	}
	now := time.Now()
	g.Lock()
	defer g.Unlock()
	it := g.groups[pid]
	if it != nil && it.expire.Before(now) {
		it = nil
	}
	if it == nil {
		it = &cItem{findProcessGroups(pid, gid), now.Add(g.cacheto)}
		g.groups[pid] = it
	}
	return it.gids
}
