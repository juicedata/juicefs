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
	length     uint64
	mode       uint8
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
	data []byte
}

func (h *handle) addOp(ctx Context) {
	h.ops = append(h.ops, ctx)
}

func (h *handle) removeOp(ctx Context) {
	h.Lock()
	for i, c := range h.ops {
		if c == ctx {
			h.ops[i] = h.ops[len(h.ops)-1]
			h.ops = h.ops[:len(h.ops)-1]
			break
		}
	}
	h.Unlock()
}

func (h *handle) cancelOp(pid uint32) {
	if pid == 0 {
		return
	}
	for _, c := range h.ops {
		if c.Pid() == pid || c.Pid() > 0 && c.Duration() > time.Second {
			c.Cancel()
		}
	}
}

func (h *handle) Rlock(ctx Context) bool {
	h.Lock()
	for (h.writing | h.writers) != 0 {
		if h.cond.WaitWithTimeout(time.Millisecond*100) && ctx.Canceled() {
			h.Unlock()
			return false
		}
	}
	h.readers++
	if h.reader == nil {
		h.reader = reader.Open(h.inode, h.length)
	}
	h.addOp(ctx)
	h.Unlock()
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
		if h.cond.WaitWithTimeout(time.Millisecond*100) && ctx.Canceled() {
			h.writers--
			h.Unlock()
			return false
		}
	}
	h.writers--
	h.writing = 1
	if h.writer == nil {
		h.writer = writer.Open(h.inode, h.length)
	}
	h.addOp(ctx)
	h.Unlock()
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
	}
	if h.writer != nil {
		h.writer.Close(meta.Background)
	}
}

var (
	handles   map[Ino][]*handle
	hanleLock sync.Mutex
	nextfh    uint64 = 1
)

func newHandle(inode Ino) *handle {
	hanleLock.Lock()
	defer hanleLock.Unlock()
	fh := nextfh
	h := &handle{inode: inode, fh: fh}
	nextfh++
	h.cond = utils.NewCond(h)
	handles[inode] = append(handles[inode], h)
	return h
}

func findHandle(inode Ino, fh uint64) *handle {
	hanleLock.Lock()
	defer hanleLock.Unlock()
	for _, f := range handles[inode] {
		if f.fh == fh {
			return f
		}
	}
	return nil
}

func releaseHandle(inode Ino, fh uint64) {
	hanleLock.Lock()
	defer hanleLock.Unlock()
	hs := handles[inode]
	for i, f := range hs {
		if f.fh == fh {
			if i+1 < len(hs) {
				hs[i] = hs[len(hs)-1]
			}
			if len(hs) > 1 {
				handles[inode] = hs[:len(hs)-1]
			} else {
				delete(handles, inode)
			}
			break
		}
	}
}

func updateHandleLength(inode Ino, length uint64) {
	hanleLock.Lock()
	for _, h := range handles[inode] {
		h.Lock()
		h.length = length
		h.Unlock()
	}
	hanleLock.Unlock()
}

func newFileHandle(mode uint8, inode Ino, length uint64) uint64 {
	h := newHandle(inode)
	h.Lock()
	defer h.Unlock()
	h.length = length
	h.mode = mode
	if mode == modeRead {
		h.reader = reader.Open(inode, length)
	} else if mode == modeWrite {
		h.writer = writer.Open(inode, length)
	}
	return h.fh
}

func releaseFileHandle(ino Ino, fh uint64) {
	h := findHandle(ino, fh)
	if h != nil {
		h.Lock()
		// rwlock_wait_for_unlock:
		for (h.writing | h.writers | h.readers) != 0 {
			h.cond.WaitWithTimeout(time.Millisecond * 100)
		}
		h.writing = 1 // for remove
		h.Unlock()
		h.Close()
		releaseHandle(ino, fh)
	}
}
