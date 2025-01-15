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

package fuse

import (
	"context"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// Ino is an alias to meta.Ino
type Ino = meta.Ino

// Attr is an alias to meta.Attr
type Attr = meta.Attr

// Context is an alias to vfs.LogContext
type Context = vfs.LogContext

type fuseContext struct {
	context.Context
	start    time.Time
	header   *fuse.InHeader
	canceled bool
	cancel   <-chan struct{}

	checkPermission bool
}

var gidcache = newGidCache(time.Minute * 5)

var contextPool = sync.Pool{
	New: func() interface{} {
		return &fuseContext{}
	},
}

func (fs *fileSystem) newContext(cancel <-chan struct{}, header *fuse.InHeader) *fuseContext {
	ctx := contextPool.Get().(*fuseContext)
	ctx.Context = context.Background()
	ctx.start = time.Now()
	ctx.canceled = false
	ctx.cancel = cancel
	ctx.header = header
	ctx.checkPermission = fs.conf.NonDefaultPermission && header.Uid != 0
	if header.Uid == 0 && fs.conf.RootSquash != nil {
		ctx.checkPermission = true
		ctx.header.Uid = fs.conf.RootSquash.Uid
		ctx.header.Gid = fs.conf.RootSquash.Gid
	}
	if fs.conf.AllSquash != nil {
		ctx.checkPermission = true
		ctx.header.Uid = fs.conf.AllSquash.Uid
		ctx.header.Gid = fs.conf.AllSquash.Gid
	}
	return ctx
}

func releaseContext(ctx *fuseContext) {
	contextPool.Put(ctx)
}

func (c *fuseContext) Uid() uint32 {
	return c.header.Uid
}

func (c *fuseContext) Gid() uint32 {
	return c.header.Gid
}

func (c *fuseContext) Gids() []uint32 {
	if c.checkPermission {
		return gidcache.get(c.Pid(), c.Gid())
	}
	return []uint32{c.header.Gid}
}

func (c *fuseContext) Pid() uint32 {
	return c.header.Pid
}

func (c *fuseContext) Duration() time.Duration {
	return time.Since(c.start)
}

func (c *fuseContext) Cancel() {
	c.canceled = true
}

func (c *fuseContext) CheckPermission() bool {
	return c.checkPermission
}

func (c *fuseContext) Canceled() bool {
	if c.Duration() < time.Second {
		return false
	}
	if c.canceled {
		return true
	}
	select {
	case <-c.cancel:
		return true
	default:
		return false
	}
}

func (c *fuseContext) WithValue(k, v interface{}) {
	c.Context = context.WithValue(c.Context, k, v)
}

func (c *fuseContext) Err() error {
	return syscall.EINTR
}

// func (c *fuseContext) Done() <-chan struct{} {
// 	return c.cancel
// }
