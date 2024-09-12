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

package meta

import "github.com/juicedata/juicefs/pkg/utils"

type slice struct {
	id    uint64
	size  uint32
	off   uint32
	len   uint32
	pos   uint32
	left  *slice
	right *slice
}

func newSlice(pos uint32, id uint64, cleng, off, len uint32) *slice {
	if len == 0 {
		return nil
	}
	s := &slice{}
	s.pos = pos
	s.id = id
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
	s.id = rb.Get64()
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
		right = newSlice(pos, s.id, s.size, s.off+l, s.len-l)
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

func marshalSlice(pos uint32, id uint64, size, off, len uint32) []byte {
	w := utils.NewBuffer(sliceBytes)
	w.Put32(pos)
	w.Put64(id)
	w.Put32(size)
	w.Put32(off)
	w.Put32(len)
	return w.Bytes()
}

func readSlices(vals []string) []*slice {
	slices := make([]slice, len(vals))
	ss := make([]*slice, len(vals))
	for i, val := range vals {
		if len(val) != sliceBytes {
			logger.Errorf("corrupt slice: len=%d, val=%v", len(val), []byte(val))
			return nil
		}
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
		chunk = append(chunk, Slice{Id: s.id, Size: s.size, Off: s.off, Len: s.len})
		pos += s.len
	})
	return chunk
}

func compactChunk(slices []*slice) (uint32, uint32, uint32, uint32, []Slice) {
	var pos uint32 = ChunkSize
	for _, s := range slices {
		if s.pos < pos {
			pos = s.pos
			if pos == 0 {
				break
			}
		}
	}
	ss := buildSlice(slices)
	if pos > 0 && len(ss) > 0 {
		// remove left padding
		ss = ss[1:]
	}

	var head, tail uint32
	trimmed := ss
	for len(trimmed) > 0 && trimmed[0].Id == 0 {
		head += trimmed[0].Len
		trimmed = trimmed[1:]
	}
	for n := len(trimmed); n > 0 && trimmed[n-1].Id == 0; n-- {
		tail += trimmed[n-1].Len
		trimmed = trimmed[:n-1]
	}
	if len(trimmed) == 0 {
		tail = head - 1
		head = 0
		trimmed = []Slice{{Len: 1}}
	}
	var size uint32
	for _, c := range trimmed {
		size += c.Len
	}
	return pos, head, size, tail, trimmed
}

func shouldSkip(s *slice, rest []*slice, lastWrite uint32) (bool, uint32) {
	pos, head, write, tail, _ := compactChunk(rest)
	if pos < s.pos+s.len && s.pos < pos+head+write+tail {
		return false, 0 // overlap
	}
	reduced := lastWrite - write
	if write == 0 || reduced*5 < lastWrite || reduced < 2<<20 || reduced < s.size {
		return false, 0
	}
	return true, write
}

func skipSome(slices []*slice) (int, int) {
	var head, tail int
	_, _, lastWrite, _, _ := compactChunk(slices)
	for ; head < len(slices)-1; head++ {
		skip, write := shouldSkip(slices[head], slices[head+1:], lastWrite)
		if !skip {
			break
		}
		lastWrite = write
	}
	for tail = len(slices); tail > head+1; tail-- {
		skip, write := shouldSkip(slices[tail-1], slices[:tail-1], lastWrite)
		if !skip {
			break
		}
		lastWrite = write
	}
	return head, tail
}
