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

package meta

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type CtxKey string

type Context interface {
	context.Context
	Gid() uint32
	Gids() []uint32
	Uid() uint32
	Pid() uint32
	WithValue(k, v interface{}) Context // should remain const semantics, so user can chain it
	Cancel()
	Canceled() bool
	CheckPermission() bool
}

func Background() Context {
	return WrapContext(context.Background())
}

type wrapContext struct {
	context.Context
	cancel func()
	pid    uint32
	uid    uint32
	gids   []uint32
}

func (c *wrapContext) Uid() uint32 {
	return c.uid
}

func (c *wrapContext) Gid() uint32 {
	return c.gids[0]
}

func (c *wrapContext) Gids() []uint32 {
	return c.gids
}

func (c *wrapContext) Pid() uint32 {
	return c.pid
}

func (c *wrapContext) Cancel() {
	c.cancel()
}

func (c *wrapContext) Canceled() bool {
	return c.Err() != nil
}

func (c *wrapContext) WithValue(k, v interface{}) Context {
	wc := *c // gids is a const, so it's safe to shallow copy
	wc.Context = context.WithValue(c.Context, k, v)
	return &wc
}

func (c *wrapContext) CheckPermission() bool {
	return true
}

func NewContext(pid, uid uint32, gids []uint32) Context {
	return wrap(context.Background(), pid, uid, gids)
}

func WrapContext(ctx context.Context) Context {
	return wrap(ctx, 0, 0, []uint32{0})
}

func wrap(ctx context.Context, pid, uid uint32, gids []uint32) Context {
	c, cancel := context.WithCancel(ctx)
	return &wrapContext{c, cancel, pid, uid, gids}
}

func containsGid(ctx Context, gid uint32) bool {
	for _, g := range ctx.Gids() {
		if g == gid {
			return true
		}
	}
	return false
}

type SizedCache[K comparable, V any] struct {
	mu       sync.RWMutex
	cache    map[K]V
	capacity int
}

func NewSizedCache[K comparable, V any](capacity int) *SizedCache[K, V] {
	return &SizedCache[K, V]{
		cache:    make(map[K]V, capacity),
		capacity: capacity,
	}
}

func (sc *SizedCache[K, V]) Get(key K) (V, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	value, found := sc.cache[key]
	return value, found
}

func (sc *SizedCache[K, V]) Put(key K, value V) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if len(sc.cache) >= sc.capacity {
		for k := range sc.cache {
			delete(sc.cache, k)
			break
		}
	}
	sc.cache[key] = value
}

var procCache = NewSizedCache[uint32, string](100)

func ProcOf(pid uint32) (proc string) {
	if runtime.GOOS != "linux" {
		return ""
	}
	proc, found := procCache.Get(pid)
	if found {
		return proc
	}
	defer func() {
		procCache.Put(pid, proc)
	}()
	path := fmt.Sprintf("/proc/%d/cmdline", pid)
	f, err := os.Open(path)
	if err != nil { // from other namespace
		return ""
	}
	defer f.Close()
	buf, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	p := bytes.Index(buf, []byte{0}) // some are separated by null
	if p < 0 {
		p = len(buf)
	}
	if sp := bytes.IndexByte(buf[:p], ' '); sp > 0 { // some are separated by space
		p = sp
	}
	if len(buf[:p]) == 0 { // some are empty
		return ""
	}
	return filepath.Base(string(buf[:p])) // some are full path
}
