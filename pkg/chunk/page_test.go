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
	"io"
	"testing"
)

func TestPage(t *testing.T) {
	p1 := NewOffPage(1)
	if len(p1.Data) != 1 {
		t.Fail()
	}
	if cap(p1.Data) != 1 {
		t.Fail()
	}
	p1.Acquire()
	p1.Release()
	if p1.Data == nil {
		t.Fail()
	}

	p2 := p1.Slice(0, 1)
	p1.Release()
	if p1.Data == nil {
		t.Fail()
	}

	p2.Release()
	if p2.Data != nil {
		t.Fail()
	}
	if p1.Data != nil {
		t.Fail()
	}
}

func TestPageReader(t *testing.T) {
	data := []byte("hello")
	p := NewPage(data)
	r := NewPageReader(p)

	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Fatalf("read should return 0")
	}
	buf := make([]byte, 3)
	if n, err := r.Read(buf); n != 3 || err != nil {
		t.Fatalf("read should return 3 but got %d", n)
	}
	if n, err := r.Read(buf); n != 2 || (err != nil && err != io.EOF) {
		t.Fatalf("read should return 2 but got %d", n)
	}
	if n, err := r.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("read should return 0")
	}
	if n, err := r.ReadAt(buf, 4); n != 1 || (err != nil && err != io.EOF) {
		t.Fatalf("read should return 1")
	}
	if n, err := r.ReadAt(buf, 5); n != 0 || err != io.EOF {
		t.Fatalf("read should return 0")
	}
	r.Close()
	if n, err := r.ReadAt(buf, 5); n != 0 || err == nil {
		t.Fatalf("read should fail after close")
	}
}
