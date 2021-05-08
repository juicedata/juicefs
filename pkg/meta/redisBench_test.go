/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package meta

import (
	"fmt"
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
)

func encodeSlices(size int) []string {
	w := utils.NewBuffer(24)
	w.Put32(0)
	w.Put64(1014)
	w.Put32(122)
	w.Put32(0)
	w.Put32(122)
	v := string(w.Bytes())
	vals := make([]string, size)
	for i := range vals {
		vals[i] = v
	}
	return vals
}

func BenchmarkReadSlices(b *testing.B) {
	cases := []struct {
		desc string
		size int
	}{
		{"small", 4},
		{"mid", 64},
		{"large", 1024},
	}
	for _, c := range cases {
		b.Run(c.desc, func(b *testing.B) {
			vals := encodeSlices(c.size)
			b.ResetTimer()
			var slices []*slice
			for i := 0; i < b.N; i++ {
				slices = readSlices(vals)
			}
			if len(slices) != len(vals) {
				b.Fail()
			}
		})
	}
}

func benchmarkReaddir(b *testing.B, n int) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		b.Skipf("redis is not available: %s", err)
	}
	_ = m.NewSession()
	ctx := Background
	var inode Ino
	dname := fmt.Sprintf("largedir%d", n)
	var es []*Entry
	if m.Lookup(ctx, 1, dname, &inode, nil) == 0 && m.Readdir(ctx, inode, 0, &es) == 0 && len(es) == n+2 {
	} else {
		_ = m.Rmr(ctx, 1, dname)
		_ = m.Mkdir(ctx, 1, dname, 0755, 0, 0, &inode, nil)
		for j := 0; j < n; j++ {
			_ = m.Create(ctx, inode, fmt.Sprintf("d%d", j), 0755, 0, nil, nil)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var entries []*Entry
		if e := m.Readdir(ctx, inode, 1, &entries); e != 0 {
			b.Fatalf("readdir: %s", e)
		}
		if len(entries) != n+2 {
			b.Fatalf("files: %d != %d", len(entries), n+2)
		}
	}
}

func BenchmarkReaddir10(b *testing.B) {
	benchmarkReaddir(b, 10)
}

func BenchmarkReaddir1k(b *testing.B) {
	benchmarkReaddir(b, 1000)
}

func BenchmarkReaddir100k(b *testing.B) {
	benchmarkReaddir(b, 100000)
}

func BenchmarkReaddir10m(b *testing.B) {
	benchmarkReaddir(b, 10000000)
}
