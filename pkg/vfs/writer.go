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
	"math/rand"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	flushDuration = time.Second * 5
)

type FileWriter interface {
	Write(ctx meta.Context, offset uint64, data []byte) syscall.Errno
	Flush(ctx meta.Context) syscall.Errno
	Close(ctx meta.Context) syscall.Errno
	GetLength() uint64
	Truncate(length uint64)
}

type DataWriter interface {
	Open(inode Ino, fleng uint64) FileWriter
	Flush(ctx meta.Context, inode Ino) syscall.Errno
	GetLength(inode Ino) uint64
	Truncate(inode Ino, length uint64)
}

type sliceWriter struct {
	id      uint64
	chunk   *chunkWriter
	off     uint32
	length  uint32
	soff    uint32
	slen    uint32
	writer  chunk.Writer
	freezed bool
	done    bool
	err     syscall.Errno
	notify  *utils.Cond
	started time.Time
	lastMod time.Time
}

func (s *sliceWriter) prepareID(ctx meta.Context, retry bool) {
	f := s.chunk.file
	f.Lock()
	for s.id == 0 {
		var id uint64
		f.Unlock()
		st := f.w.m.NewChunk(ctx, f.inode, s.chunk.indx, s.off, &id)
		f.Lock()
		if st != 0 && st != syscall.EIO {
			s.err = st
			break
		}
		if !retry || st == 0 {
			if s.id == 0 {
				s.id = id
			}
			break
		}
		f.Unlock()
		logger.Debugf("meta is not available: %s", st)
		time.Sleep(time.Millisecond * 100)
		f.Lock()
	}
	if s.writer != nil && s.writer.ID() == 0 {
		s.writer.SetID(s.id)
	}
	f.Unlock()
}

func (s *sliceWriter) markDone() {
	f := s.chunk.file
	f.Lock()
	s.done = true
	s.notify.Signal()
	f.Unlock()
}

// freezed, no more data
func (s *sliceWriter) flushData() {
	defer s.markDone()
	if s.slen == 0 {
		return
	}
	s.prepareID(meta.Background, true)
	if s.err != 0 {
		logger.Infof("flush inode:%d chunk: %s", s.chunk.file.inode, s.err)
		s.writer.Abort()
		return
	}
	s.length = s.slen
	if err := s.writer.Finish(int(s.length)); err != nil {
		logger.Errorf("upload chunk %v (length: %v) fail: %s", s.id, s.length, err)
		s.writer.Abort()
		s.err = syscall.EIO
	}
	s.writer = nil
}

// protected by s.chunk.file
func (s *sliceWriter) write(ctx meta.Context, off uint32, data []uint8) syscall.Errno {
	f := s.chunk.file
	_, err := s.writer.WriteAt(data, int64(off))
	if err != nil {
		logger.Warnf("write: chunk: %d off: %d %s", s.id, off, err)
		return syscall.EIO
	}
	if off+uint32(len(data)) > s.slen {
		s.slen = off + uint32(len(data))
	}
	s.lastMod = time.Now()
	if s.slen == meta.ChunkSize {
		s.freezed = true
		go s.flushData()
	} else if int(s.slen) >= f.w.blockSize {
		if s.id > 0 {
			err := s.writer.FlushTo(int(s.slen))
			if err != nil {
				logger.Warnf("write: chunk: %d off: %d %s", s.id, off, err)
				return syscall.EIO
			}
		} else if int(off) <= f.w.blockSize {
			go s.prepareID(ctx, false)
		}
	}
	return 0
}

type chunkWriter struct {
	indx   uint32
	file   *fileWriter
	slices []*sliceWriter
}

// protected by file
func (c *chunkWriter) findWritableSlice(pos uint32, size uint32) *sliceWriter {
	blockSize := uint32(c.file.w.blockSize)
	for i := range c.slices {
		s := c.slices[len(c.slices)-1-i]
		if !s.freezed {
			flushoff := s.slen / blockSize * blockSize
			if pos >= s.off+flushoff && pos <= s.off+s.slen {
				return s
			} else if i > 3 {
				s.freezed = true
				go s.flushData()
			}
		}
		if pos < s.off+s.slen && s.off < pos+size {
			// overlaped
			// TODO: write into multiple slices
			return nil
		}
	}
	return nil
}

func (c *chunkWriter) commitThread() {
	f := c.file
	defer f.w.free(f)
	f.Lock()
	defer f.Unlock()
	// the slices should be committed in the order that are created
	for len(c.slices) > 0 {
		s := c.slices[0]
		for !s.done {
			if s.notify.WaitWithTimeout(time.Millisecond*100) && !s.freezed && time.Since(s.started) > flushDuration*2 {
				s.freezed = true
				go s.flushData()
			}
		}
		err := s.err
		f.Unlock()

		if err == 0 {
			var ss = meta.Slice{Chunkid: s.id, Size: s.length, Off: s.soff, Len: s.slen}
			err = f.w.m.Write(meta.Background, f.inode, c.indx, s.off, ss)
			f.w.reader.Invalidate(f.inode, uint64(c.indx)*meta.ChunkSize+uint64(s.off), uint64(ss.Len))
		}

		f.Lock()
		if err != 0 {
			if err != syscall.ENOENT && err != syscall.ENOSPC {
				logger.Warnf("write inode:%d error: %s", f.inode, err)
				err = syscall.EIO
			}
			f.err = err
			logger.Errorf("write inode:%d indx:%d  %s", f.inode, c.indx, err)
		}
		c.slices = c.slices[1:]
	}
	f.freeChunk(c)
}

type fileWriter struct {
	sync.Mutex
	w *dataWriter

	inode        Ino
	length       uint64
	err          syscall.Errno
	flushwaiting uint16
	writewaiting uint16
	refs         uint16
	chunks       map[uint32]*chunkWriter

	flushcond *utils.Cond // wait for chunks==nil (flush)
	writecond *utils.Cond // wait for flushwaiting==0 (write)
}

// protected by file
func (f *fileWriter) findChunk(i uint32) *chunkWriter {
	c := f.chunks[i]
	if c == nil {
		c = &chunkWriter{indx: i, file: f}
		f.chunks[i] = c
	}
	return c
}

// protected by file
func (f *fileWriter) freeChunk(c *chunkWriter) {
	delete(f.chunks, c.indx)
	if len(f.chunks) == 0 && f.flushwaiting > 0 {
		f.flushcond.Broadcast()
	}
}

// protected by file
func (f *fileWriter) writeChunk(ctx meta.Context, indx uint32, off uint32, data []byte) syscall.Errno {
	c := f.findChunk(indx)
	s := c.findWritableSlice(off, uint32(len(data)))
	if s == nil {
		s = &sliceWriter{
			chunk:   c,
			off:     off,
			writer:  f.w.store.NewWriter(0),
			notify:  utils.NewCond(&f.Mutex),
			started: time.Now(),
		}
		c.slices = append(c.slices, s)
		if len(c.slices) == 1 {
			f.w.Lock()
			f.refs++
			f.w.Unlock()
			go c.commitThread()
		}
	}
	return s.write(ctx, off-s.off, data)
}

func (f *fileWriter) totalSlices() int {
	var cnt int
	f.Lock()
	for _, c := range f.chunks {
		cnt += len(c.slices)
	}
	f.Unlock()
	return cnt
}

func (w *dataWriter) usedBufferSize() int64 {
	return utils.AllocMemory() - w.store.UsedMemory()
}

func (f *fileWriter) Write(ctx meta.Context, off uint64, data []byte) syscall.Errno {
	for {
		if f.totalSlices() < 1000 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if f.w.usedBufferSize() > f.w.bufferSize {
		// slow down
		time.Sleep(time.Millisecond * 10)
		for f.w.usedBufferSize() > f.w.bufferSize*2 {
			time.Sleep(time.Millisecond * 100)
		}
	}

	s := time.Now()
	f.Lock()
	defer f.Unlock()
	size := uint64(len(data))
	f.writewaiting++
	for f.flushwaiting > 0 {
		if f.writecond.WaitWithTimeout(time.Second) && ctx.Canceled() {
			f.writewaiting--
			logger.Warnf("write %d interrupted after %d", f.inode, time.Since(s))
			return syscall.EINTR
		}
	}
	f.writewaiting--

	indx := uint32(off / meta.ChunkSize)
	pos := uint32(off % meta.ChunkSize)
	for len(data) > 0 {
		n := uint32(len(data))
		if pos+n > meta.ChunkSize {
			n = meta.ChunkSize - pos
		}
		if st := f.writeChunk(ctx, indx, pos, data[:n]); st != 0 {
			return st
		}
		data = data[n:]
		indx++
		pos = (pos + n) % meta.ChunkSize
	}
	if off+size > f.length {
		f.length = off + size
	}
	return f.err
}

func (f *fileWriter) flush(ctx meta.Context, writeback bool) syscall.Errno {
	s := time.Now()
	f.Lock()
	defer f.Unlock()
	f.flushwaiting++

	var err syscall.Errno
	var wait = time.Second * time.Duration((f.w.maxRetries+1)*(f.w.maxRetries+1)/2)
	if wait < time.Minute*5 {
		wait = time.Minute * 5
	}
	var deadline = time.Now().Add(wait)
	for len(f.chunks) > 0 && err == 0 {
		for _, c := range f.chunks {
			for _, s := range c.slices {
				if !s.freezed {
					s.freezed = true
					go s.flushData()
				}
			}
		}
		if f.flushcond.WaitWithTimeout(time.Second*3) && ctx.Canceled() {
			logger.Warnf("flush %d interrupted after %d", f.inode, time.Since(s))
			err = syscall.EINTR
			break
		}
		if time.Now().After(deadline) {
			logger.Errorf("flush %d timeout after waited %s", f.inode, wait)
			for _, c := range f.chunks {
				for _, s := range c.slices {
					logger.Errorf("pending slice %d-%d: %+v", f.inode, c.indx, *s)
				}
			}
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			logger.Warnf("All goroutines (%d):\n%s", runtime.NumGoroutine(), buf[:n])
			err = syscall.EIO
			break
		}
	}
	f.flushwaiting--
	if f.flushwaiting == 0 && f.writewaiting > 0 {
		f.writecond.Broadcast()
	}
	if err == 0 {
		err = f.err
	}
	return err
}

func (f *fileWriter) Flush(ctx meta.Context) syscall.Errno {
	return f.flush(ctx, false)
}

func (f *fileWriter) Close(ctx meta.Context) syscall.Errno {
	defer f.w.free(f)
	return f.Flush(ctx)
}

func (f *fileWriter) GetLength() uint64 {
	f.Lock()
	defer f.Unlock()
	return f.length
}

func (f *fileWriter) Truncate(length uint64) {
	f.Lock()
	defer f.Unlock()
	// TODO: truncate write buffer if length < f.length
	f.length = length
}

type dataWriter struct {
	sync.Mutex
	m          meta.Meta
	store      chunk.ChunkStore
	reader     DataReader
	blockSize  int
	bufferSize int64
	files      map[Ino]*fileWriter
	maxRetries uint32
}

func NewDataWriter(conf *Config, m meta.Meta, store chunk.ChunkStore, reader DataReader) DataWriter {
	w := &dataWriter{
		m:          m,
		store:      store,
		reader:     reader,
		blockSize:  conf.Chunk.BlockSize,
		bufferSize: int64(conf.Chunk.BufferSize),
		files:      make(map[Ino]*fileWriter),
		maxRetries: uint32(conf.Meta.Retries),
	}
	go w.flushAll()
	return w
}

func (w *dataWriter) flushAll() {
	for {
		w.Lock()
		now := time.Now()
		for _, f := range w.files {
			f.refs++
			w.Unlock()
			tooMany := f.totalSlices() > 800
			f.Lock()

			lastBit := uint32(rand.Int() % 2) // choose half of chunks randomly
			for i, c := range f.chunks {
				hs := len(c.slices) / 2
				for j, s := range c.slices {
					if !s.freezed && (now.Sub(s.started) > flushDuration || now.Sub(s.lastMod) > time.Second ||
						tooMany && i%2 == lastBit && j <= hs) {
						s.freezed = true
						go s.flushData()
					}
				}
			}
			f.Unlock()
			w.free(f)
			w.Lock()
		}
		w.Unlock()
		time.Sleep(time.Millisecond * 100)
	}
}

func (w *dataWriter) Open(inode Ino, len uint64) FileWriter {
	w.Lock()
	defer w.Unlock()
	f, ok := w.files[inode]
	if !ok {
		f = &fileWriter{
			w:      w,
			inode:  inode,
			length: len,
			chunks: make(map[uint32]*chunkWriter),
		}
		f.flushcond = utils.NewCond(f)
		f.writecond = utils.NewCond(f)
		w.files[inode] = f
	}
	f.refs++
	return f
}

func (w *dataWriter) find(inode Ino) *fileWriter {
	w.Lock()
	defer w.Unlock()
	return w.files[inode]
}

func (w *dataWriter) free(f *fileWriter) {
	w.Lock()
	defer w.Unlock()
	f.refs--
	if f.refs == 0 {
		delete(w.files, f.inode)
	}
}

func (w *dataWriter) Flush(ctx meta.Context, inode Ino) syscall.Errno {
	f := w.find(inode)
	if f != nil {
		return f.Flush(ctx)
	}
	return 0
}

func (w *dataWriter) GetLength(inode Ino) uint64 {
	f := w.find(inode)
	if f != nil {
		return f.GetLength()
	}
	return 0
}

func (w *dataWriter) Truncate(inode Ino, len uint64) {
	f := w.find(inode)
	if f != nil {
		f.Truncate(len)
	}
}
