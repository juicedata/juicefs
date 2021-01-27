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

package meta

import (
	"context"
	"strconv"
)

type Ino uint64

func (i Ino) String() string {
	return strconv.FormatUint(uint64(i), 10)
}

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
}

type emptyContext struct {
	context.Context
}

func (ctx emptyContext) Gid() uint32    { return 0 }
func (ctx emptyContext) Gids() []uint32 { return []uint32{0} }
func (ctx emptyContext) Uid() uint32    { return 0 }
func (ctx emptyContext) Pid() uint32    { return 1 }
func (ctx emptyContext) Cancel()        {}
func (ctx emptyContext) Canceled() bool { return false }
func (ctx emptyContext) WithValue(k, v interface{}) {
	ctx.Context = context.WithValue(ctx.Context, k, v)
}

var Background Context = emptyContext{context.Background()}

type myContext struct {
	context.Context
	pid  uint32
	uid  uint32
	gids []uint32
}

func (c *myContext) Uid() uint32 {
	return c.uid
}

func (c *myContext) Gid() uint32 {
	return c.gids[0]
}

func (c *myContext) Gids() []uint32 {
	return c.gids
}

func (c *myContext) Pid() uint32 {
	return c.pid
}

func (c *myContext) Cancel() {}

func (c *myContext) Canceled() bool {
	return false
}

func (c *myContext) WithValue(k, v interface{}) {
	c.Context = context.WithValue(c.Context, k, v)
}

func NewContext(pid, uid uint32, gids []uint32) Context {
	return &myContext{context.Background(), pid, uid, gids}
}
