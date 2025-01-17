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

package chunk

import (
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/utils"
)

var pageStack = os.Getenv("JFS_PAGE_STACK") != ""

// Page is a page with refcount
type Page struct {
	refs    int32
	offheap bool
	dep     *Page
	Data    []byte
	stack   []byte
}

// NewPage create a new page.
func NewPage(data []byte) *Page {
	return &Page{refs: 1, Data: data}
}

func NewOffPage(size int) *Page {
	if size <= 0 {
		panic("size of page should > 0")
	}
	p := utils.Alloc(size)
	page := &Page{refs: 1, offheap: true, Data: p}
	if pageStack {
		page.stack = debug.Stack()
	}
	runtime.SetFinalizer(page, func(p *Page) {
		refcnt := atomic.LoadInt32(&p.refs)
		if refcnt != 0 {
			logger.Errorf("refcount of page %p (%d bytes) is not zero: %d, created by: %s", p, cap(p.Data), refcnt, string(p.stack))
			if refcnt > 0 {
				p.Release()
			}
		}
	})
	return page
}

func (p *Page) Slice(off, len int) *Page {
	p.Acquire()
	np := NewPage(p.Data[off : off+len])
	np.dep = p
	return np
}

// Acquire increase the refcount
func (p *Page) Acquire() {
	if pageStack {
		p.stack = append(p.stack, debug.Stack()...)
	}
	atomic.AddInt32(&p.refs, 1)
}

// Release decrease the refcount
func (p *Page) Release() {
	if pageStack {
		p.stack = append(p.stack, debug.Stack()...)
	}
	if atomic.AddInt32(&p.refs, -1) == 0 {
		if p.offheap {
			utils.Free(p.Data)
		}
		if p.dep != nil {
			p.dep.Release()
			p.dep = nil
		}
		p.Data = nil
	}
}

type pageReader struct {
	p   *Page
	off int
}

func NewPageReader(p *Page) *pageReader {
	p.Acquire()
	return &pageReader{p, 0}
}

func (r *pageReader) Read(buf []byte) (int, error) {
	n, err := r.ReadAt(buf, int64(r.off))
	r.off += n
	return n, err
}

func (r *pageReader) ReadAt(buf []byte, off int64) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if r.p == nil {
		return 0, errors.New("page is already released")
	}
	if int(off) == len(r.p.Data) {
		return 0, io.EOF
	}
	n := copy(buf, r.p.Data[off:])
	if n < len(buf) {
		return n, io.EOF
	}
	return n, nil
}

func (r *pageReader) Close() error {
	if r.p != nil {
		r.p.Release()
		r.p = nil
	}
	return nil
}
