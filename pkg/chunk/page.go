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

package chunk

import (
	"errors"
	"io"
	"runtime"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/utils"
)

// Page is a page with refcount
type Page struct {
	refs    int32
	offheap bool
	dep     *Page
	Data    []byte
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
	runtime.SetFinalizer(page, func(p *Page) {
		refcnt := atomic.LoadInt32(&p.refs)
		if refcnt != 0 {
			logger.Errorf("refcount of page %p is not zero: %d", p, refcnt)
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
	atomic.AddInt32(&p.refs, 1)
}

// Release decrease the refcount
func (p *Page) Release() {
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
