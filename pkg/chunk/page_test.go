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
	_ = r.Close()
	if n, err := r.ReadAt(buf, 5); n != 0 || err == nil {
		t.Fatalf("read should fail after close")
	}
}
