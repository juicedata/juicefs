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

package utils

import (
	"fmt"
	"reflect"
	"testing"
)

func assertEqual(t *testing.T, a interface{}, b interface{}) {
	if reflect.DeepEqual(a, b) {
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
	assertEqual(t, b.Len(), 20)

	r := ReadBuffer(b.Bytes())
	assertEqual(t, r.Get8(), uint8(1))
	assertEqual(t, r.Get16(), uint16(2))
	assertEqual(t, r.Get32(), uint32(3))
	assertEqual(t, r.Get64(), uint64(4))
	assertEqual(t, r.HasMore(), true)
	assertEqual(t, r.Left(), 5)
	if len(r.Buffer()) != 5 {
		t.Fatal("rest buffer should be 5 bytes")
	}
	assertEqual(t, string(r.Get(5)), "hello")
	r.Seek(10)
	assertEqual(t, r.Left(), 10)
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
