/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

package sync

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/juicedata/juicefs/pkg/object"
)

type parallelDownloader struct {
	sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	stopWatch  func() bool
	notify     *sync.Cond
	src        object.ObjectStorage
	key        string
	fsize      int64
	blockSize  int64
	concurrent chan int
	buffers    map[int64]*[]byte
	off        int64
	err        error
}

func (r *parallelDownloader) hasErr() bool {
	r.Lock()
	defer r.Unlock()
	return r.err != nil || r.ctx.Err() != nil
}

func (r *parallelDownloader) setErr(err error) {
	r.Lock()
	defer r.Unlock()
	if r.err == nil {
		r.err = err
	}
}

const downloadBufSize = 10 << 20

var downloadBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 0, downloadBufSize)
		return &buf
	},
}

func (r *parallelDownloader) download() {
	for off := int64(0); off < r.fsize; off += r.blockSize {
		select {
		case <-r.ctx.Done():
			r.setErr(r.ctx.Err())
			r.notify.Broadcast()
			return
		case r.concurrent <- 1:
		}
		go func(off int64) {
			var size = r.blockSize
			if off+r.blockSize > r.fsize {
				size = r.fsize - off
			}
			var saved bool
			if !r.hasErr() {
				var e error
				if limiter != nil {
					e = limiter.Wait(r.ctx, size)
				}
				var in io.ReadCloser
				if e == nil {
					e = try(r.ctx, 3, func() error {
						var err error
						in, err = r.src.Get(r.ctx, r.key, off, size)
						return err
					})
				}
				if e != nil {
					r.setErr(e)
				} else { //nolint:typecheck
					defer in.Close()
					p := downloadBufPool.Get().(*[]byte)
					*p = (*p)[:size]
					_, e = io.ReadFull(in, *p)
					if e != nil {
						r.setErr(e)
						downloadBufPool.Put(p)
					} else {
						r.Lock()
						if r.buffers != nil {
							r.buffers[off] = p
							saved = true
						} else {
							downloadBufPool.Put(p)
						}
						r.Unlock()
					}
				}
			}
			if !saved {
				<-r.concurrent
			}
			r.notify.Signal()
		}(off)
	}
}

func (r *parallelDownloader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.off >= r.fsize {
		return 0, io.EOF
	}
	off := r.off / r.blockSize * r.blockSize
	r.Lock()
	for r.err == nil && r.ctx.Err() == nil && r.buffers[off] == nil {
		r.notify.Wait()
	}
	p := r.buffers[off]
	err := r.err
	if err == nil {
		err = r.ctx.Err()
	}
	r.Unlock()
	if p == nil {
		return 0, err
	}
	n := copy(b, (*p)[r.off-off:])
	r.off += int64(n)
	if r.off == off+int64(len(*p)) {
		downloadBufPool.Put(p)
		r.Lock()
		delete(r.buffers, off)
		r.Unlock()
		<-r.concurrent
	}
	if copiedBytes != nil {
		copiedBytes.IncrInt64(int64(n))
	}
	return n, nil
}

func (r *parallelDownloader) Close() {
	r.cancel()
	if r.stopWatch != nil {
		r.stopWatch()
	}
	r.Lock()
	defer r.Unlock()
	for _, p := range r.buffers {
		downloadBufPool.Put(p)
	}
	r.buffers = nil
	if r.err == nil {
		r.err = errors.New("closed")
	}
	r.notify.Broadcast()
}

func newParallelDownloader(ctx context.Context, store object.ObjectStorage, key string, size int64, bSize int64, concurrent chan int) *parallelDownloader {
	if bSize < 1 {
		panic("concurrent and blockSize must be positive integer")
	}
	dlCtx, cancel := context.WithCancel(ctx)
	down := &parallelDownloader{
		ctx:        dlCtx,
		cancel:     cancel,
		src:        store,
		key:        key,
		fsize:      size,
		blockSize:  bSize,
		concurrent: concurrent,
		buffers:    make(map[int64]*[]byte),
	}
	down.notify = sync.NewCond(down)
	down.stopWatch = context.AfterFunc(dlCtx, func() {
		down.Lock()
		if down.err == nil {
			down.err = dlCtx.Err()
		}
		down.notify.Broadcast()
		down.Unlock()
	})
	go down.download()
	return down
}
