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

package vfs

import (
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

type handle struct {
	sync.Mutex
	inode Ino
	fh    uint64

	// for dir
	children []*meta.Entry

	// for file
	locks      uint8
	flockOwner uint64 // kernel 3.1- does not pass lock_owner in release()
	reader     FileReader
	writer     FileWriter
	ops        []Context

	// rwlock
	writing uint32
	readers uint32
	writers uint32
	cond    *utils.Cond

	// internal files
	off     uint64
	data    []byte
	pending []byte
}

func (h *handle) addOp(ctx Context) {
	h.Lock()
	defer h.Unlock()
	h.ops = append(h.ops, ctx)
}

func (h *handle) removeOp(ctx Context) {
	h.Lock()
	defer h.Unlock()
	for i, c := range h.ops {
		if c == ctx {
			h.ops[i] = h.ops[len(h.ops)-1]
			h.ops = h.ops[:len(h.ops)-1]
			break
		}
	}
}

func (h *handle) cancelOp(pid uint32) {
	if pid == 0 {
		return
	}
	h.Lock()
	defer h.Unlock()
	for _, c := range h.ops {
		if c.Pid() == pid || c.Pid() > 0 && c.Duration() > time.Second {
			c.Cancel()
		}
	}
}

func (h *handle) Rlock(ctx Context) bool {
	h.Lock()
	for (h.writing | h.writers) != 0 {
		if h.cond.WaitWithTimeout(time.Second) && ctx.Canceled() {
			h.Unlock()
			logger.Warnf("read lock %d interrupted", h.inode)
			return false
		}
	}
	h.readers++
	h.Unlock()
	h.addOp(ctx)
	return true
}

func (h *handle) Runlock() {
	h.Lock()
	h.readers--
	if h.readers == 0 {
		h.cond.Broadcast()
	}
	h.Unlock()
}

func (h *handle) Wlock(ctx Context) bool {
	h.Lock()
	h.writers++
	for (h.readers | h.writing) != 0 {
		if h.cond.WaitWithTimeout(time.Second) && ctx.Canceled() {
			h.writers--
			h.Unlock()
			logger.Warnf("write lock %d interrupted", h.inode)
			return false
		}
	}
	h.writers--
	h.writing = 1
	h.Unlock()
	h.addOp(ctx)
	return true
}

func (h *handle) Wunlock() {
	h.Lock()
	h.writing = 0
	h.cond.Broadcast()
	h.Unlock()
}

func (h *handle) Close() {
	if h.reader != nil {
		h.reader.Close(meta.Background)
		h.reader = nil
	}
	if h.writer != nil {
		h.writer.Close(meta.Background)
		h.writer = nil
	}
}

func (v *VFS) newHandle(inode Ino) *handle {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	fh := v.nextfh
	h := &handle{inode: inode, fh: fh}
	v.nextfh++
	h.cond = utils.NewCond(h)
	v.handles[inode] = append(v.handles[inode], h)
	return h
}

func (v *VFS) findAllHandles(inode Ino) []*handle {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	return v.handles[inode]
}

func (v *VFS) findHandle(inode Ino, fh uint64) *handle {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	for _, f := range v.handles[inode] {
		if f.fh == fh {
			return f
		}
	}
	return nil
}

func (v *VFS) releaseHandle(inode Ino, fh uint64) {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	hs := v.handles[inode]
	for i, f := range hs {
		if f.fh == fh {
			if i+1 < len(hs) {
				hs[i] = hs[len(hs)-1]
			}
			if len(hs) > 1 {
				v.handles[inode] = hs[:len(hs)-1]
			} else {
				delete(v.handles, inode)
			}
			break
		}
	}
}

func (v *VFS) newFileHandle(inode Ino, length uint64, flags uint32) uint64 {
	h := v.newHandle(inode)
	h.Lock()
	defer h.Unlock()
	switch flags & O_ACCMODE {
	case syscall.O_RDONLY:
		h.reader = v.Reader.Open(inode, length)
	case syscall.O_WRONLY: // FUSE writeback_cache mode need reader even for WRONLY
		fallthrough
	case syscall.O_RDWR:
		h.reader = v.Reader.Open(inode, length)
		h.writer = v.Writer.Open(inode, length)
	}
	return h.fh
}

func (v *VFS) releaseFileHandle(ino Ino, fh uint64) {
	h := v.findHandle(ino, fh)
	if h != nil {
		h.Lock()
		// rwlock_wait_for_unlock:
		for (h.writing | h.writers | h.readers) != 0 {
			h.cond.WaitWithTimeout(time.Millisecond * 100)
		}
		h.writing = 1 // for remove
		h.Unlock()
		h.Close()
		v.releaseHandle(ino, fh)
	}
}
