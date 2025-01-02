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

package vfs

import (
	"context"
	"testing"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

func TestCompact(t *testing.T) {
	cconf := chunk.Config{
		BlockSize:  256 * 1024,
		Compress:   "lz4",
		MaxUpload:  2,
		BufferSize: 30 << 20,
		CacheSize:  10 << 20,
		CacheDir:   "memory",
	}
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	store := chunk.NewCachedStore(blob, cconf, nil)

	// prepare the slices
	var slices []meta.Slice
	var total int
	for i := 0; i < 100; i++ {
		buf := make([]byte, 100+i*100)
		for j := range buf {
			buf[j] = byte(i)
		}
		cid := uint64(i)
		w := store.NewWriter(cid)
		if n, e := w.WriteAt(buf, 0); e != nil {
			t.Fatalf("write chunk %d: %s", cid, e)
		} else {
			total += n
		}
		if e := w.Finish(len(buf)); e != nil {
			t.Fatalf("flush chunk %d: %s", cid, e)
		}
		slices = append(slices, meta.Slice{Id: cid, Size: uint32(len(buf)), Len: uint32(len(buf))})
	}

	// compact
	var cid uint64 = 1000
	err := Compact(cconf, store, slices, cid)
	if err != nil {
		t.Fatalf("compact %d slices : %s", len(slices), err)
	}

	// verify result
	r := store.NewReader(cid, total)
	var off int
	for i := 0; i < 100; i++ {
		buf := make([]byte, 100+i*100)
		page := chunk.NewPage(buf)
		n, err := r.ReadAt(context.Background(), page, off)
		if err != nil {
			t.Fatalf("read chunk %d at %d: %s", cid, off, err)
		} else if n != len(buf) {
			t.Fatalf("short read: %d", n)
		}
		for j := range buf {
			if buf[j] != byte(i) {
				t.Fatalf("invalid byte at %d: %d !=%d", j, buf[j], i)
			}
		}
		off += len(buf)
		defer page.Release()
	}

	// failed
	_ = store.Remove(1, 200)
	err = Compact(cconf, store, slices, cid)
	if err == nil {
		t.Fatalf("compact should fail with read but got nil")
	}

	// TODO: inject write failure
}
