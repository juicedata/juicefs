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
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

/*
 * state of sliceReader
 *
 *    <-- REFRESH
 *   |      |
 *  NEW -> BUSY  -> READY
 *          |         |
 *        BREAK ---> INVALID
 */
const (
	NEW = iota
	BUSY
	REFRESH
	BREAK
	READY
	INVALID
)

const readSessions = 2

var readBufferUsed atomic.Int64

type sstate uint8

func (m sstate) valid() bool { return m != BREAK && m != INVALID }

var stateNames = []string{"NEW", "BUSY", "REFRESH", "BREAK", "READY", "INVALID"}

func (m sstate) String() string {
	if m <= INVALID {
		return stateNames[m]
	}
	panic("<unknown>")
}

type FileReader interface {
	Read(ctx meta.Context, off uint64, buf []byte) (int, syscall.Errno)
	GetLength() uint64
	Close(ctx meta.Context)
}

type DataReader interface {
	Open(inode Ino, length uint64) FileReader
	Truncate(inode Ino, length uint64)
	Invalidate(inode Ino, off, length uint64)
}

type frange struct {
	off uint64
	len uint64
}

func (r *frange) String() string         { return fmt.Sprintf("[%d,%d,%d)", r.off, r.len, r.end()) }
func (r *frange) end() uint64            { return r.off + r.len }
func (r *frange) contain(p uint64) bool  { return r.off < p && p < r.end() }
func (r *frange) overlap(a *frange) bool { return a.off < r.end() && r.off < a.end() }
func (r *frange) include(a *frange) bool { return r.off <= a.off && a.end() <= r.end() }

// protected by file
type sliceReader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	file       *fileReader
	block      *frange
	state      sstate
	page       *chunk.Page
	indx       uint32
	currentPos uint32
	lastAccess time.Time
	cond       *utils.Cond
	next       *sliceReader
	prev       **sliceReader
	refs       uint16
}

func (s *sliceReader) delay(delay time.Duration) {
	time.AfterFunc(delay, s.run)
}

func (s *sliceReader) done(err syscall.Errno, delay time.Duration) {
	f := s.file
	switch s.state {
	case BUSY:
		s.state = NEW // failed
	case BREAK:
		s.state = INVALID
	case REFRESH:
		s.state = NEW
	}
	if err != 0 {
		if !f.closing {
			logger.Errorf("read file %d: %s", f.inode, err)
		}
		f.err = err
	}
	if f.shouldStop() {
		s.state = INVALID
	}

	switch s.state {
	case NEW:
		s.delay(delay)
	case READY:
		s.cond.Broadcast()
	case INVALID:
		if s.refs == 0 {
			s.delete()
			if f.closing && f.slices == nil {
				f.r.Lock()
				if f.refs == 0 {
					f.delete()
				}
				f.r.Unlock()
			}
		} else {
			s.cond.Broadcast()
		}
	}
	runtime.Goexit()
}

func retry_time(trycnt uint32) time.Duration {
	if trycnt < 30 {
		return time.Millisecond * time.Duration((trycnt-1)*300+1)
	}
	return time.Second * 10
}

func (s *sliceReader) run() {
	f := s.file
	f.Lock()
	defer f.Unlock()
	if s.state != NEW || f.shouldStop() {
		s.done(0, 0)
	}
	s.state = BUSY
	indx := s.indx
	inode := f.inode
	f.Unlock()

	var slices []meta.Slice
	err := f.r.m.Read(meta.Background(), inode, indx, &slices)
	f.Lock()
	length := f.length
	if s.state != BUSY || f.err != 0 || f.closing {
		s.done(0, 0)
	}
	if err == syscall.ENOENT {
		s.done(err, 0)
	} else if err != 0 {
		f.tried++
		trycnt := f.tried
		if trycnt > f.r.maxRetries {
			s.done(syscall.EIO, 0)
		} else {
			s.done(0, retry_time(trycnt))
		}
	}

	s.currentPos = 0
	if s.block.off > length {
		s.block.len = 0
		s.state = READY
		s.done(0, 0)
	} else if s.block.end() > length {
		s.block.len = length - s.block.off
	}
	need := s.block.len
	f.Unlock()

	p := s.page.Slice(0, int(need))
	defer p.Release()
	var n int

	ctx := context.WithValue(s.ctx, meta.CtxKey("inode"), inode) // Output inode in log for debugging
	n = f.r.Read(ctx, p, slices, (uint32(s.block.off))%meta.ChunkSize)

	f.Lock()
	if s.state != BUSY || f.shouldStop() {
		s.done(0, 0)
	}
	if n == int(need) {
		s.state = READY
		s.currentPos = uint32(n)
		s.file.tried = 0
		s.lastAccess = time.Now()
		s.done(0, 0)
	} else {
		s.currentPos = 0 // start again from beginning
		err = syscall.EIO
		f.tried++
		_ = f.r.m.InvalidateChunkCache(meta.Background(), inode, indx)
		if f.tried > f.r.maxRetries {
			s.done(err, 0)
		} else {
			s.done(0, retry_time(f.tried))
		}
	}
}

func (s *sliceReader) invalidate() {
	switch s.state {
	case NEW:
	case BUSY:
		s.state = REFRESH
		// TODO: interrupt reader
	case READY:
		if s.refs > 0 {
			s.state = NEW
			go s.run()
		} else {
			s.state = INVALID
			s.delete() // nobody wants it anymore, so delete it
		}
	}
}

func (s *sliceReader) drop() {
	if s.state <= BREAK {
		if s.refs == 0 {
			s.state = BREAK
			// TODO: interrupt reader
		}
	} else {
		if s.refs == 0 {
			s.delete() // nobody wants it anymore, so delete it
		} else if s.state == READY {
			s.state = INVALID // somebody still using it, so mark it for removal
		}
	}
	s.cancel()
}

func (s *sliceReader) delete() {
	*(s.prev) = s.next
	if s.next != nil {
		s.next.prev = s.prev
	} else {
		s.file.last = s.prev
	}
	readBufferUsed.Add(-int64(cap(s.page.Data)))
	s.page.Release()
}

type session struct {
	lastOffset uint64
	total      uint64
	readahead  uint64
	atime      time.Time
}

type fileReader struct {
	// protected by itself
	inode    Ino
	length   uint64
	err      syscall.Errno
	tried    uint32
	sessions [readSessions]session
	slices   *sliceReader
	last     **sliceReader

	sync.Mutex
	closing bool

	// protected by r
	refs uint16
	next *fileReader
	r    *dataReader
}

func (f *fileReader) GetLength() uint64 {
	f.Lock()
	defer f.Unlock()
	return f.length
}

// protected by f
func (f *fileReader) newSlice(block *frange) *sliceReader {
	s := &sliceReader{}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.file = f
	s.lastAccess = time.Now()
	s.indx = uint32(block.off / meta.ChunkSize)
	s.block = &frange{block.off, block.len} // random read
	blockend := (block.off/f.r.blockSize + 1) * f.r.blockSize
	if s.block.end() > f.length {
		s.block.len = f.length - s.block.off
	}
	if s.block.end() > blockend {
		s.block.len = blockend - s.block.off
	}
	block.off = s.block.end()
	block.len -= s.block.len
	s.page = chunk.NewOffPage(int(s.block.len))
	s.cond = utils.NewCond(&f.Mutex)
	s.prev = f.last
	*(f.last) = s
	f.last = &(s.next)
	go s.run()
	readBufferUsed.Add(int64(cap(s.page.Data)))
	return s
}

func (f *fileReader) delete() {
	r := f.r
	i := r.files[f.inode]
	if i == f {
		if i.next != nil {
			r.files[f.inode] = i.next
		} else {
			delete(r.files, f.inode)
		}
	} else {
		for i != nil {
			if i.next == f {
				i.next = f.next
				break
			}
			i = i.next
		}
	}
	f.next = nil
}

func (f *fileReader) acquire() {
	f.r.Lock()
	defer f.r.Unlock()
	f.refs++
}

func (f *fileReader) release() {
	f.r.Lock()
	defer f.r.Unlock()
	f.refs--
	if f.refs == 0 && f.slices == nil {
		f.delete()
	}
}

func (f *fileReader) guessSession(block *frange) int {
	idx := -1
	var closestOff uint64
	for i, ses := range f.sessions {
		if ses.lastOffset > closestOff && ses.lastOffset <= block.off && block.off <= ses.lastOffset+ses.readahead+f.r.blockSize {
			idx = i
			closestOff = ses.lastOffset
		}
	}
	if idx == -1 {
		for i, ses := range f.sessions {
			bt := ses.readahead / 8
			if bt < f.r.blockSize {
				bt = f.r.blockSize
			}
			min := ses.lastOffset - bt
			if ses.lastOffset < bt {
				min = 0
			}
			if min <= block.off && block.off < ses.lastOffset && (closestOff == 0 || ses.lastOffset < closestOff) {
				idx = i
				closestOff = ses.lastOffset
			}
		}
	}
	if idx == -1 {
		for i, ses := range f.sessions {
			if ses.total == 0 {
				idx = i
				break
			}
			if idx == -1 || ses.atime.Before(f.sessions[idx].atime) {
				idx = i
			}
		}
		f.sessions[idx].lastOffset = block.off
		f.sessions[idx].total = block.len
		f.sessions[idx].readahead = 0
	} else {
		if block.end() > f.sessions[idx].lastOffset {
			f.sessions[idx].total += block.end() - f.sessions[idx].lastOffset
		}
	}
	f.sessions[idx].atime = time.Now()
	return idx
}

func (f *fileReader) checkReadahead(block *frange) int {
	idx := f.guessSession(block)
	ses := &f.sessions[idx]
	seqdata := ses.total
	readahead := ses.readahead
	used := uint64(readBufferUsed.Load())
	if readahead == 0 && f.r.blockSize <= f.r.readAheadMax && (block.off == 0 || seqdata > block.len) { // begin with read-ahead turned on
		ses.readahead = f.r.blockSize
	} else if readahead < f.r.readAheadMax && seqdata >= readahead && f.r.readAheadTotal > used+readahead*4 {
		ses.readahead *= 2
	} else if readahead >= f.r.blockSize && (f.r.readAheadTotal < used+readahead/2 || seqdata < readahead/4) {
		ses.readahead /= 2
	}
	if ses.readahead >= f.r.blockSize {
		ahead := frange{block.end(), ses.readahead}
		f.readAhead(&ahead)
	}
	if block.end() > ses.lastOffset {
		ses.lastOffset = block.end()
	}
	return idx
}

func (f *fileReader) need(block *frange) bool {
	for _, ses := range f.sessions {
		if ses.total == 0 {
			break
		}
		bt := ses.readahead / 8
		if bt < f.r.blockSize {
			bt = f.r.blockSize
		}
		b := &frange{ses.lastOffset - bt, ses.readahead*2 + f.r.blockSize*2}
		if ses.lastOffset < bt {
			b.off = 0
		}
		if block.overlap(b) {
			return true
		}
	}
	return false
}

// cleanup unused requests
func (f *fileReader) cleanupRequests(block *frange) {
	now := time.Now()
	var cnt int
	f.visit(func(s *sliceReader) bool {
		if !s.state.valid() ||
			!block.overlap(s.block) && (s.lastAccess.Add(time.Second*30).Before(now) || !f.need(s.block)) {
			s.drop()
		} else if !block.overlap(s.block) {
			cnt++
		}
		return true
	})
	f.visit(func(s *sliceReader) bool {
		if !block.overlap(s.block) && cnt > f.r.maxRequests {
			s.drop()
			cnt--
		}
		return cnt > f.r.maxRequests
	})
}

func (f *fileReader) releaseIdleBuffer() {
	f.Lock()
	defer f.Unlock()
	now := time.Now()
	var idle = time.Minute
	used := readBufferUsed.Load()
	if used > int64(f.r.readAheadTotal) {
		idle /= time.Duration(used / int64(f.r.readAheadTotal))
	}
	f.visit(func(s *sliceReader) bool {
		if !s.state.valid() || s.lastAccess.Add(idle).Before(now) || !f.need(s.block) {
			s.drop()
		}
		return true
	})
}

func (f *fileReader) splitRange(block *frange) []uint64 {
	ranges := []uint64{block.off, block.end()}
	contain := func(p uint64) bool {
		for _, i := range ranges {
			if i == p {
				return true
			}
		}
		return false
	}
	f.visit(func(s *sliceReader) bool {
		if s.state.valid() {
			if block.contain(s.block.off) && !contain(s.block.off) {
				ranges = append(ranges, s.block.off)
			}
			if block.contain(s.block.end()) && !contain(s.block.end()) {
				ranges = append(ranges, s.block.end())
			}
		}
		return true
	})
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i] < ranges[j]
	})
	return ranges
}

// protected by f
func (f *fileReader) readAhead(block *frange) {
	f.visit(func(r *sliceReader) bool {
		if r.state.valid() && r.block.off <= block.off && r.block.end() > block.off {
			if r.state == READY && block.len > f.r.blockSize && r.block.off == block.off && r.block.off%f.r.blockSize == 0 {
				// next block is ready, reduce readahead by a block
				block.len -= f.r.blockSize / 2
			}
			if r.block.end() <= block.end() {
				block.len = block.end() - r.block.end()
			} else {
				block.len = 0
			}
			block.off = r.block.end()
		}
		return true
	})
	if block.len > 0 && block.off < f.length && uint64(readBufferUsed.Load()) < f.r.readAheadTotal {
		if block.len < f.r.blockSize {
			block.len += f.r.blockSize - block.end()%f.r.blockSize // align to end of a block
		}
		f.newSlice(block)
		if block.len > 0 {
			f.readAhead(block)
		}
	}
}

type req struct {
	frange
	s *sliceReader
}

func (f *fileReader) prepareRequests(ranges []uint64) []*req {
	var reqs []*req
	edges := len(ranges)
	for i := 0; i < edges-1; i++ {
		var added bool
		b := frange{ranges[i], ranges[i+1] - ranges[i]}
		f.visit(func(s *sliceReader) bool {
			if !added && s.state.valid() && s.block.include(&b) {
				s.refs++
				s.lastAccess = time.Now()
				reqs = append(reqs, &req{frange{ranges[i] - s.block.off, b.len}, s})
				added = true
				return false
			}
			return true
		})
		if !added {
			for b.len > 0 {
				s := f.newSlice(&b)
				s.refs++
				reqs = append(reqs, &req{frange{0, s.block.len}, s})
			}
		}
	}
	return reqs
}

func (f *fileReader) shouldStop() bool {
	return f.err != 0 || f.closing
}

func (f *fileReader) waitForIO(ctx meta.Context, reqs []*req, buf []byte) (int, syscall.Errno) {
	start := time.Now()
	for _, req := range reqs {
		s := req.s
		for s.state != READY && uint64(s.currentPos) < s.block.len {
			if s.cond.WaitWithTimeout(time.Second) {
				if ctx.Canceled() {
					logger.Warnf("read %d interrupted after %s", f.inode, time.Since(start))
					return 0, syscall.EINTR
				}
			}
			if f.shouldStop() {
				return 0, f.err
			}
		}
	}

	var n int
	for _, req := range reqs {
		s := req.s
		if req.off < s.block.len && s.block.off+req.off < f.length {
			if req.end() > s.block.len {
				logger.Warnf("not enough bytes (%d < %d), restart read", s.block.len, req.end())
				return 0, syscall.EAGAIN
			}
			if s.block.off+req.end() > f.length {
				req.len = f.length - s.block.off - req.off
			}
			n += copy(buf[n:], s.page.Data[req.off:req.end()])
		}
	}
	return n, 0
}

func (f *fileReader) Read(ctx meta.Context, offset uint64, buf []byte) (int, syscall.Errno) {
	if f.r.readBufferUsed() > f.r.bufferSize {
		time.Sleep(time.Millisecond * 10)             // slow down
		for f.r.readBufferUsed() > f.r.bufferSize*2 { // readahead uses 80% of buffer, stop here to avoid OOM
			time.Sleep(time.Millisecond * 100)
		}
	}
	f.Lock()
	defer f.Unlock()
	f.acquire()
	defer f.release()

	if f.err != 0 || f.closing {
		return 0, f.err
	}

	size := uint64(len(buf))
	if offset >= f.length || size == 0 {
		return 0, 0
	}
	block := &frange{offset, size}
	if block.end() > f.length {
		block.len = f.length - block.off
	}

	f.cleanupRequests(block)
	var lastBS uint64 = 32 << 10
	if block.off+lastBS > f.length {
		lastblock := frange{f.length - lastBS, lastBS}
		if f.length < lastBS {
			lastblock = frange{0, f.length}
		}
		f.readAhead(&lastblock)
	}
	ranges := f.splitRange(block)
	reqs := f.prepareRequests(ranges)
	defer func() {
		for _, req := range reqs {
			s := req.s
			s.refs--
			if s.refs == 0 && s.state == INVALID {
				s.delete()
			}
		}
	}()
	f.checkReadahead(block)
	return f.waitForIO(ctx, reqs, buf)
}

func (f *fileReader) visit(fn func(s *sliceReader) bool) {
	var next *sliceReader
	for s := f.slices; s != nil; s = next {
		next = s.next
		if !fn(s) {
			break
		}
	}
}

func (f *fileReader) Close(ctx meta.Context) {
	f.Lock()
	f.closing = true
	f.visit(func(s *sliceReader) bool {
		s.drop()
		return true
	})
	f.release()
	f.Unlock()
}

type dataReader struct {
	sync.Mutex
	m              meta.Meta
	store          chunk.ChunkStore
	files          map[Ino]*fileReader
	blockSize      uint64
	bufferSize     int64
	readAheadMax   uint64
	readAheadTotal uint64
	maxRequests    int
	maxRetries     uint32
}

func NewDataReader(conf *Config, m meta.Meta, store chunk.ChunkStore) DataReader {
	var readAheadTotal = 256 << 20
	if conf.Chunk.BufferSize > 0 {
		readAheadTotal = int(conf.Chunk.BufferSize / 10 * 8) // 80% of total buffer
	}
	readAheadMax := min(conf.Chunk.Readahead, readAheadTotal)
	r := &dataReader{
		m:              m,
		store:          store,
		files:          make(map[Ino]*fileReader),
		blockSize:      uint64(conf.Chunk.BlockSize),
		bufferSize:     int64(conf.Chunk.BufferSize),
		readAheadTotal: uint64(readAheadTotal),
		readAheadMax:   uint64(readAheadMax),
		maxRequests:    readAheadMax/conf.Chunk.BlockSize*readSessions + 1,
		maxRetries:     uint32(conf.Meta.Retries),
	}
	go r.checkReadBuffer()
	return r
}

func (r *dataReader) readBufferUsed() int64 {
	used := readBufferUsed.Load()
	return used
}

func (r *dataReader) checkReadBuffer() {
	for {
		r.Lock()
		for _, f := range r.files {
			for f != nil {
				r.Unlock()
				f.releaseIdleBuffer()
				r.Lock()
				f = f.next
			}
		}
		r.Unlock()
		time.Sleep(time.Second)
	}
}

func (r *dataReader) Open(inode Ino, length uint64) FileReader {
	f := &fileReader{
		r:      r,
		inode:  inode,
		length: length,
	}
	f.last = &(f.slices)

	r.Lock()
	f.refs = 1
	f.next = r.files[inode]
	r.files[inode] = f
	r.Unlock()
	return f
}

func (r *dataReader) visit(inode Ino, fn func(*fileReader)) {
	// r could be hold inside f, so Unlock r first to avoid deadlock
	r.Lock()
	var fs []*fileReader
	f := r.files[inode]
	for f != nil {
		fs = append(fs, f)
		f = f.next
	}
	r.Unlock()
	for _, f := range fs {
		f.Lock()
		fn(f)
		f.Unlock()
	}
}

func (r *dataReader) Truncate(inode Ino, length uint64) {
	r.visit(inode, func(f *fileReader) {
		if length < f.length {
			f.visit(func(s *sliceReader) bool {
				if s.block.off+s.block.len > length {
					s.invalidate()
				}
				return true
			})
		}
		f.length = length
	})
}

func (r *dataReader) Invalidate(inode Ino, off, length uint64) {
	b := frange{off, length}
	r.visit(inode, func(f *fileReader) {
		if off+length > f.length {
			f.length = off + length
		}
		f.visit(func(s *sliceReader) bool {
			if b.overlap(s.block) {
				s.invalidate()
			}
			return true
		})
	})
}

func (r *dataReader) readSlice(ctx context.Context, s *meta.Slice, page *chunk.Page, off int) error {
	buf := page.Data
	read := 0
	if s.Id == 0 {
		for read < len(buf) {
			buf[read] = 0
			read++
		}
		return nil
	}

	reader := r.store.NewReader(s.Id, int(s.Size))
	for read < len(buf) {
		p := page.Slice(read, len(buf)-read)
		n, err := reader.ReadAt(ctx, p, off+int(s.Off))
		p.Release()
		if n == 0 && err != nil {
			logger.Warningf("fail to read sliceId %d (off:%d, size:%d, clen: %d, inode: %d): %s",
				s.Id, off+int(s.Off), len(buf)-read, s.Size, ctx.Value(meta.CtxKey("inode")), err)
			return err
		}
		read += n
		off += n
	}
	return nil
}

func (r *dataReader) Read(ctx context.Context, page *chunk.Page, slices []meta.Slice, offset uint32) int {
	if len(slices) > 16 {
		return r.readManySlices(ctx, page, slices, offset)
	}
	read := 0
	var pos uint32
	errs := make(chan error, 10)
	waits := 0
	buf := page.Data
	size := len(buf)
	for i := 0; i < len(slices); i++ {
		if read < size && offset < pos+slices[i].Len {
			toread := min(size-read, int(pos+slices[i].Len-offset))
			go func(s *meta.Slice, p *chunk.Page, off, pos uint32) {
				defer p.Release()
				errs <- r.readSlice(ctx, s, p, int(off))
			}(&slices[i], page.Slice(read, toread), offset-pos, pos)
			read += toread
			offset += uint32(toread)
			waits++
		}
		pos += slices[i].Len
	}
	for read < size {
		buf[read] = 0
		read++
	}
	var err error
	// wait for all goroutine to return, otherwise they may access invalid memory
	for waits > 0 {
		if e := <-errs; e != nil {
			err = e
		}
		waits--
	}
	if err != nil {
		return 0
	}
	return read
}

func (r *dataReader) readManySlices(ctx context.Context, page *chunk.Page, slices []meta.Slice, offset uint32) int {
	read := 0
	var pos uint32
	var err error
	errs := make(chan error, 10)
	waits := 0
	buf := page.Data
	size := len(buf)
	concurrency := make(chan byte, 16)

SLICES:
	for i := 0; i < len(slices); i++ {
		if read < size && offset < pos+slices[i].Len {
			toread := min(size-read, int(pos+slices[i].Len-offset))
		WAIT:
			for {
				select {
				case concurrency <- 1:
					break WAIT
				case e := <-errs:
					waits--
					if e != nil {
						err = e
						break SLICES
					}
				}
			}
			go func(s *meta.Slice, p *chunk.Page, off int, pos uint32) {
				defer p.Release()
				errs <- r.readSlice(ctx, s, p, off)
				<-concurrency
			}(&slices[i], page.Slice(read, toread), int(offset-pos), pos)

			read += toread
			offset += uint32(toread)
			waits++
		}
		pos += slices[i].Len
	}
	// wait for all jobs done, otherwise they may access invalid memory
	for waits > 0 {
		if e := <-errs; e != nil {
			err = e
		}
		waits--
	}
	if err != nil {
		return 0
	}
	for read < size {
		buf[read] = 0
		read++
	}
	return read
}
