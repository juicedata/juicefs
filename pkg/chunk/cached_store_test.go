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
	"context"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

func BenchmarkCachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config)
	w := store.NewWriter(1)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	time.Sleep(time.Millisecond * 100)
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(1, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}

func BenchmarkUncachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	config.CacheSize = 0
	store := NewCachedStore(blob, config)
	w := store.NewWriter(2)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(2, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}
