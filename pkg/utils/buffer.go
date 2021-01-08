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

package utils

import (
	"encoding/binary"
	"unsafe"
)

type Buffer struct {
	endian binary.ByteOrder
	off    int
	buf    []byte
}

func NewBuffer(sz uint32) *Buffer {
	return FromBuffer(make([]byte, sz))
}

func ReadBuffer(buf []byte) *Buffer {
	return FromBuffer(buf)
}

func FromBuffer(buf []byte) *Buffer {
	return &Buffer{binary.BigEndian, 0, buf}
}

func (b *Buffer) Len() int {
	return len(b.buf)
}

func (b *Buffer) HasMore() bool {
	return b.off < len(b.buf)
}

func (b *Buffer) Left() int {
	return len(b.buf) - b.off
}

func (b *Buffer) Seek(p int) {
	b.off = p
}

func (b *Buffer) Buffer() []byte {
	return b.buf[b.off:]
}

func (b *Buffer) Put8(v uint8) {
	b.buf[b.off] = v
	b.off++
}

func (b *Buffer) Get8() uint8 {
	v := b.buf[b.off]
	b.off++
	return v
}

func (b *Buffer) Put16(v uint16) {
	b.endian.PutUint16(b.buf[b.off:b.off+2], v)
	b.off += 2
}

func (b *Buffer) Get16() uint16 {
	v := b.endian.Uint16(b.buf[b.off : b.off+2])
	b.off += 2
	return v
}

func (b *Buffer) Put32(v uint32) {
	b.endian.PutUint32(b.buf[b.off:b.off+4], v)
	b.off += 4
}

func (b *Buffer) Get32() uint32 {
	v := b.endian.Uint32(b.buf[b.off : b.off+4])
	b.off += 4
	return v
}

func (b *Buffer) Put64(v uint64) {
	b.endian.PutUint64(b.buf[b.off:b.off+8], v)
	b.off += 8
}

func (b *Buffer) Get64() uint64 {
	v := b.endian.Uint64(b.buf[b.off : b.off+8])
	b.off += 8
	return v
}

func (b *Buffer) Put(v []byte) {
	l := len(v)
	copy(b.buf[b.off:b.off+l], v)
	b.off += l
}

func (b *Buffer) Get(l int) []byte {
	b.off += l
	return b.buf[b.off-l : b.off]
}

func (b *Buffer) SetBytes(buf []byte) {
	b.endian = binary.BigEndian
	b.off = 0
	b.buf = buf
}

func (b *Buffer) Bytes() []byte {
	return b.buf
}

var nativeEndian binary.ByteOrder

func NewNativeBuffer(buf []byte) *Buffer {
	return &Buffer{nativeEndian, 0, buf}
}

func init() {
	buf := [2]byte{}
	*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(0xABCD)

	switch buf {
	case [2]byte{0xCD, 0xAB}:
		nativeEndian = binary.LittleEndian
	case [2]byte{0xAB, 0xCD}:
		nativeEndian = binary.BigEndian
	default:
		panic("Could not determine native endianness.")
	}
}
