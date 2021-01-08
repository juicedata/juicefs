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
	"fmt"
	"testing"
)

func assertEqual(t *testing.T, a interface{}, b interface{}) {
	if a == b {
		return
	}
	message := fmt.Sprintf("%v != %v", a, b)
	t.Fatal(message)
}

func TestBuffer(t *testing.T) {
	b := NewBuffer(20)
	b.Put8(1)
	b.Put16(2)
	b.Put32(3)
	b.Put64(4)
	b.Put([]byte("hello"))

	r := ReadBuffer(b.Bytes())
	assertEqual(t, r.Get8(), uint8(1))
	assertEqual(t, r.Get16(), uint16(2))
	assertEqual(t, r.Get32(), uint32(3))
	assertEqual(t, r.Get64(), uint64(4))
	assertEqual(t, string(r.Get(5)), "hello")
}

func TestSetBytes(t *testing.T) {
	var w Buffer
	w.SetBytes(make([]byte, 3))
	w.Put8(1)
	w.Put16(2)
	r := ReadBuffer(w.Bytes())
	assertEqual(t, r.Get8(), uint8(1))
	assertEqual(t, r.Get16(), uint16(2))
}

func TestNativeBuffer(t *testing.T) {
	b := NewNativeBuffer(make([]byte, 20))
	b.Put8(1)
	b.Put16(2)
	b.Put32(3)
	b.Put64(4)
	b.Put([]byte("hello"))

	r := NewNativeBuffer(b.Bytes())
	assertEqual(t, r.Get8(), uint8(1))
	assertEqual(t, r.Get16(), uint16(2))
	assertEqual(t, r.Get32(), uint32(3))
	assertEqual(t, r.Get64(), uint64(4))
	assertEqual(t, string(r.Get(5)), "hello")
}
