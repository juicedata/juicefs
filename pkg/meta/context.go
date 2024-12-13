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
	"context"
)

type CtxKey string

type Context interface {
	context.Context
	Gid() uint32
	Gids() []uint32
	Uid() uint32
	Pid() uint32
	WithValue(k, v interface{})
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

func (c *wrapContext) WithValue(k, v interface{}) {
	c.Context = context.WithValue(c.Context, k, v)
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
