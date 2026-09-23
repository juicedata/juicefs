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
	"fmt"
	"testing"
)

func TestBlockKey_NoHashPrefix(t *testing.T) {
	// id=123456789: id/1000000=123, id/1000=123456
	got := BlockKey(123456789, 2, 4194304, false)
	want := "chunks/123/123456/123456789_2_4194304"
	if got != want {
		t.Errorf("BlockKey() = %q, want %q", got, want)
	}
}

func TestBlockKey_HashPrefix(t *testing.T) {
	// id=123456789: id%256 = 123456789%256 = 21 -> hex "15"
	got := BlockKey(123456789, 0, 4194304, true)
	want := "chunks/15/123/123456789_0_4194304"
	if got != want {
		t.Errorf("BlockKey() = %q, want %q", got, want)
	}
}

func TestSliceBlockKeys(t *testing.T) {
	// length=10MB, blockSize=4MB -> 3 blocks: 4MB, 4MB, 2MB
	length := 10 * 1024 * 1024
	blockSize := 4 * 1024 * 1024
	keys := SliceBlockKeys(100, length, blockSize, false)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	// Last block should be 2MB = 2097152 bytes
	lastBlock := keys[2]
	wantLast := "chunks/0/0/100_2_2097152"
	if lastBlock != wantLast {
		t.Errorf("last key = %q, want %q", lastBlock, wantLast)
	}
	// First block should be full blockSize
	wantFirst := "chunks/0/0/100_0_4194304"
	if keys[0] != wantFirst {
		t.Errorf("first key = %q, want %q", keys[0], wantFirst)
	}
}

func TestSliceBlockKeys_ExactMultiple(t *testing.T) {
	// length=8MB, blockSize=4MB -> 2 blocks, each 4MB
	length := 8 * 1024 * 1024
	blockSize := 4 * 1024 * 1024
	keys := SliceBlockKeys(100, length, blockSize, false)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	for i, k := range keys {
		want := "chunks/0/0/100_" + itoa(i) + "_4194304"
		if k != want {
			t.Errorf("key[%d] = %q, want %q", i, k, want)
		}
	}
}

func TestSliceBlockKeys_Empty(t *testing.T) {
	keys := SliceBlockKeys(100, 0, 4194304, false)
	if keys != nil {
		t.Errorf("expected nil for length=0, got %v", keys)
	}
	keys = SliceBlockKeys(100, -1, 4194304, false)
	if keys != nil {
		t.Errorf("expected nil for length=-1, got %v", keys)
	}
}

// itoa is a simple int-to-string helper for test assertions.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
