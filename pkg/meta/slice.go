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

import "github.com/juicedata/juicefs/pkg/utils"

type slice struct {
	chunkid uint64
	size    uint32
	off     uint32
	len     uint32
	pos     uint32
	left    *slice
	right   *slice
}

func newSlice(pos uint32, chunkid uint64, cleng, off, len uint32) *slice {
	if len == 0 {
		return nil
	}
	s := &slice{}
	s.pos = pos
	s.chunkid = chunkid
	s.size = cleng
	s.off = off
	s.len = len
	s.left = nil
	s.right = nil
	return s
}

func (s *slice) read(buf []byte) {
	rb := utils.ReadBuffer(buf)
	s.pos = rb.Get32()
	s.chunkid = rb.Get64()
	s.size = rb.Get32()
	s.off = rb.Get32()
	s.len = rb.Get32()
}

func (s *slice) cut(pos uint32) (left, right *slice) {
	if s == nil {
		return nil, nil
	}
	if pos <= s.pos {
		if s.left == nil {
			s.left = newSlice(pos, 0, 0, 0, s.pos-pos)
		}
		left, s.left = s.left.cut(pos)
		return left, s
	} else if pos < s.pos+s.len {
		l := pos - s.pos
		right = newSlice(pos, s.chunkid, s.size, s.off+l, s.len-l)
		right.right = s.right
		s.len = l
		s.right = nil
		return s, right
	} else {
		if s.right == nil {
			s.right = newSlice(s.pos+s.len, 0, 0, 0, pos-s.pos-s.len)
		}
		s.right, right = s.right.cut(pos)
		return s, right
	}
}

func (s *slice) visit(f func(*slice)) {
	if s == nil {
		return
	}
	s.left.visit(f)
	right := s.right
	f(s) // s could be freed
	right.visit(f)
}

const sliceBytes = 24

func marshalSlice(pos uint32, chunkid uint64, size, off, len uint32) []byte {
	w := utils.NewBuffer(sliceBytes)
	w.Put32(pos)
	w.Put64(chunkid)
	w.Put32(size)
	w.Put32(off)
	w.Put32(len)
	return w.Bytes()
}

func readSlices(vals []string) []*slice {
	slices := make([]slice, len(vals))
	ss := make([]*slice, len(vals))
	for i, val := range vals {
		s := &slices[i]
		s.read([]byte(val))
		ss[i] = s
	}
	return ss
}

func readSliceBuf(buf []byte) []*slice {
	if len(buf)%sliceBytes != 0 {
		logger.Errorf("corrupt slices: len=%d", len(buf))
		return nil
	}
	nSlices := len(buf) / sliceBytes
	slices := make([]slice, nSlices)
	ss := make([]*slice, nSlices)
	for i := 0; i < len(buf); i += sliceBytes {
		s := &slices[i/sliceBytes]
		s.read(buf[i:])
		ss[i/sliceBytes] = s
	}
	return ss
}

func buildSlice(ss []*slice) []Slice {
	var root *slice
	for i := range ss {
		s := new(slice)
		*s = *ss[i]
		var right *slice
		s.left, right = root.cut(s.pos)
		_, s.right = right.cut(s.pos + s.len)
		root = s
	}
	var pos uint32
	var chunk []Slice
	root.visit(func(s *slice) {
		if s.pos > pos {
			chunk = append(chunk, Slice{Size: s.pos - pos, Len: s.pos - pos})
			pos = s.pos
		}
		chunk = append(chunk, Slice{Chunkid: s.chunkid, Size: s.size, Off: s.off, Len: s.len})
		pos += s.len
	})
	return chunk
}

func compactChunk(ss []*slice) (uint32, uint32, []Slice) {
	var chunk = buildSlice(ss)
	var pos uint32
	if len(chunk) > 0 && chunk[0].Chunkid == 0 {
		pos = chunk[0].Len
		chunk = chunk[1:]
	}
	var size uint32
	for _, c := range chunk {
		size += uint32(c.Len)
	}
	return pos, size, chunk
}

func skipSome(chunk []*slice) int {
	var skipped int
	var total = len(chunk)
	for skipped < total {
		ss := chunk[skipped:]
		pos, size, c := compactChunk(ss)
		first := ss[0]
		if first.len < (1<<20) || first.len*5 < size || size == 0 {
			// it's too small
			break
		}
		isFirst := func(pos uint32, s Slice) bool {
			return pos == first.pos && s.Chunkid == first.chunkid && s.Off == first.off && s.Len == first.len
		}
		if !isFirst(pos, c[0]) {
			// it's not the first slice, compact it
			break
		}
		skipped++
	}
	return skipped
}
