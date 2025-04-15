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

package vfs

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
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
	dirHandler meta.DirHandler
	readAt     time.Time

	// for file
	flags      uint32
	locks      uint8
	flockOwner uint64 // kernel 3.1- does not pass lock_owner in release()
	ofdOwner   uint64 // OFD lock
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
	bctx    meta.Context
}

func (h *handle) Write(buf []byte) (int, error) {
	h.Lock()
	defer h.Unlock()
	h.data = append(h.data, buf...)
	return len(buf), nil
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
		h.reader.Close(meta.Background())
		h.reader = nil
	}
	if h.writer != nil {
		_ = h.writer.Close(meta.Background())
		h.writer = nil
	}
}

func (v *VFS) newHandle(inode Ino, readOnly bool) *handle {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	var lowBits uint64
	if readOnly {
		lowBits = 1
	}
	for v.handleIno[v.nextfh] > 0 || v.nextfh&1 != lowBits {
		v.nextfh++ // skip recovered fd
	}
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
	hs := v.handles[inode]
	if len(hs) <= 1 {
		return hs
	}
	// copy hs so it will not be modified by releaseHandle
	hs2 := make([]*handle, len(hs))
	copy(hs2, hs)
	return hs2
}

const O_RECOVERED = 1 << 31 // is recovered fd

func (v *VFS) findHandle(inode Ino, fh uint64) *handle {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	for _, f := range v.handles[inode] {
		if f.fh == fh {
			return f
		}
	}
	if fh&1 == 1 && inode != controlInode {
		f := &handle{inode: inode, fh: fh, flags: O_RECOVERED}
		f.cond = utils.NewCond(f)
		v.handles[inode] = append(v.handles[inode], f)
		if v.handleIno[fh] == 0 {
			v.handleIno[fh] = inode
		}
		return f
	}
	return nil
}

func (v *VFS) releaseHandle(inode Ino, fh uint64) {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	hs := v.handles[inode]
	for i, f := range hs {
		if f.fh == fh {
			if hs[i].dirHandler != nil {
				hs[i].dirHandler.Close()
				hs[i].dirHandler = nil
			}
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
	h := v.newHandle(inode, (flags&O_ACCMODE) == syscall.O_RDONLY)
	h.Lock()
	defer h.Unlock()
	h.flags = flags
	switch flags & O_ACCMODE {
	case syscall.O_RDONLY:
		h.reader = v.reader.Open(inode, length)
	case syscall.O_WRONLY: // FUSE writeback_cache mode need reader even for WRONLY
		fallthrough
	case syscall.O_RDWR:
		h.reader = v.reader.Open(inode, length)
		h.writer = v.writer.Open(inode, length)
	}
	return h.fh
}

func (v *VFS) releaseFileHandle(ino Ino, fh uint64) {
	h := v.findHandle(ino, fh)
	if h != nil {
		v.releaseHandle(ino, fh)
		h.Lock()
		for (h.writing | h.writers | h.readers) != 0 {
			h.cond.WaitWithTimeout(time.Millisecond * 100)
		}
		h.Unlock()
		h.Close()
	}
}

func (v *VFS) invalidateDirHandle(parent Ino, name string, inode Ino, attr *Attr) {
	v.hanleM.Lock()
	hs := v.handles[parent]
	v.hanleM.Unlock()
	for _, h := range hs {
		h.Lock()
		if h.dirHandler != nil {
			if inode > 0 {
				h.dirHandler.Insert(inode, name, attr)
			} else {
				h.dirHandler.Delete(name)
			}
		}
		h.Unlock()
	}
}

type state struct {
	Handler map[uint64]saveHandle
	NextFh  uint64
}

type saveHandle struct {
	Inode      uint64
	Length     uint64
	Flags      uint32
	UseLocks   uint8
	FlockOwner uint64
	Off        uint64
	Data       string
}

func (v *VFS) dumpAllHandles(path string) (err error) {
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	var vfsState state
	vfsState.Handler = make(map[uint64]saveHandle)
	for ino, hs := range v.handles {
		if ino == controlInode {
			// the job is lost, can't be recovered
			continue
		}
		for _, h := range hs {
			h.Lock()
			if ino == logInode {
				readerLock.RLock()
				reader := readers[h.fh]
				readerLock.RUnlock()
				if reader == nil {
					continue
				}
				reader.Lock()
			OUTER:
				for {
					select {
					case line := <-reader.buffer:
						reader.last = append(reader.last, line...)
					default:
						break OUTER
					}
				}
				h.data = reader.last
				reader.Unlock()
			}
			var length uint64
			if h.writer != nil {
				length = h.writer.GetLength()
				err := h.writer.Flush(meta.Background())
				if err != 0 {
					logger.Errorf("flush writer of %d: %s", ino, err)
				}
			} else if h.reader != nil {
				length = h.reader.GetLength()
			}
			s := saveHandle{
				Inode:      uint64(h.inode),
				Length:     length,
				Flags:      h.flags,
				UseLocks:   h.locks,
				FlockOwner: h.flockOwner,
				Off:        h.off,
				Data:       hex.EncodeToString(h.data),
			}
			h.Unlock()
			vfsState.Handler[h.fh] = s
		}
	}
	vfsState.NextFh = v.nextfh
	d, err := json.Marshal(vfsState)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(d)
	if err != nil {
		return err
	}
	return
}

func (v *VFS) loadAllHandles(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var vfsState state
	err = json.Unmarshal(d, &vfsState)
	if err != nil {
		return err
	}
	v.hanleM.Lock()
	defer v.hanleM.Unlock()
	for fh, s := range vfsState.Handler {
		data, err := hex.DecodeString(s.Data)
		if err != nil {
			logger.Warnf("decode data for inode %d: %s", s.Inode, err)
		}
		h := &handle{
			inode:      Ino(s.Inode),
			fh:         fh,
			flags:      s.Flags,
			locks:      s.UseLocks,
			flockOwner: s.FlockOwner,
			off:        s.Off,
		}
		h.cond = utils.NewCond(h)
		v.handles[h.inode] = append(v.handles[h.inode], h)
		v.handleIno[fh] = h.inode
		if s.Inode == logInode {
			openAccessLog(fh)
			readers[fh].last = data
			continue
		}
		h.data = data
		switch s.Flags & O_ACCMODE {
		case syscall.O_RDONLY:
			h.reader = v.reader.Open(h.inode, s.Length)
		case syscall.O_WRONLY: // FUSE writeback_cache mode need reader even for WRONLY
			fallthrough
		case syscall.O_RDWR:
			h.reader = v.reader.Open(h.inode, s.Length)
			h.writer = v.writer.Open(h.inode, s.Length)
		}
	}
	if len(v.handleIno) > 0 {
		logger.Infof("load %d handles from %s", len(v.handleIno), path)
	}
	v.nextfh = vfsState.NextFh
	// _ = os.Remove(path)
	return nil
}
