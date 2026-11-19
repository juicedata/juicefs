/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package main

import (
	"testing"
	"unsafe"
)

func TestToBufLargerThan1GiB(t *testing.T) {
	data := make([]byte, 1<<30+1)
	data[0], data[len(data)-1] = 'h', 't'

	buf := toBuf(uintptr(unsafe.Pointer(&data[0])), int32(len(data)))
	if len(buf) != len(data) || cap(buf) != len(data) {
		t.Fatalf("len=%d cap=%d, want %d", len(buf), cap(buf), len(data))
	}
	if buf[0] != 'h' || buf[len(buf)-1] != 't' {
		t.Fatalf("buf does not alias the source memory")
	}
}
