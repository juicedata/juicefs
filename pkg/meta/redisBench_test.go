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
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
)

func init() {
	utils.SetOutFile("bench-test.log")
}

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

func mkdir(b *testing.B, m Meta, n int) {
	var err syscall.Errno
	ctx := Background
	dname := "benchMkdir"
	if err = m.Rmr(ctx, 1, dname); err != 0 && err != syscall.ENOENT {
		b.Fatalf("rmr: %s", err)
	}
	var parent Ino
	if err = m.Mkdir(ctx, 1, dname, 0755, 0, 0, &parent, nil); err != 0 {
		b.Fatalf("mkdir: %s", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j++ {
			if err = m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
	}
}

func mvdir(b *testing.B, m Meta, n int) { // rename dir
	var err syscall.Errno
	ctx := Background
	dname := "benchMvdir"
	if err = m.Rmr(ctx, 1, dname); err != 0 && err != syscall.ENOENT {
		b.Fatalf("rmr: %s", err)
	}
	var parent Ino
	if err = m.Mkdir(ctx, 1, dname, 0755, 0, 0, &parent, nil); err != 0 {
		b.Fatalf("mkdir: %s", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err = m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err = m.Rename(ctx, parent, fmt.Sprintf("d%d_%d", i, j), parent, fmt.Sprintf("rd%d_%d", i, j), nil, nil); err != 0 {
				b.Fatalf("rename dir: %s", err)
			}
		}
	}
}

func rmdir(b *testing.B, m Meta, n int) {
	var err syscall.Errno
	ctx := Background
	dname := "benchRmdir"
	if err = m.Rmr(ctx, 1, dname); err != 0 && err != syscall.ENOENT {
		b.Fatalf("rmr: %s", err)
	}
	var parent Ino
	if err = m.Mkdir(ctx, 1, dname, 0755, 0, 0, &parent, nil); err != 0 {
		b.Fatalf("mkdir: %s", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err = m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err = m.Rmdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j)); err != 0 {
				b.Fatalf("rmdir: %s", err)
			}
		}
	}
}

func BenchmarkDir(b *testing.B) { // mkdir, rename dir, rmdir
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		b.Skipf("redis is not available: %s", err)
	}
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()

	cases := []struct {
		desc string
		size int
	}{
		{"1", 1},
		{"100", 100},
		{"10k", 10000},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("mkdir_%s", c.desc), func(b *testing.B) { mkdir(b, m, c.size) })
		b.Run(fmt.Sprintf("mvdir_%s", c.desc), func(b *testing.B) { mvdir(b, m, c.size) })
		b.Run(fmt.Sprintf("rmdir_%s", c.desc), func(b *testing.B) { rmdir(b, m, c.size) })
	}
}

func readdir(b *testing.B, m Meta, n int) {
	ctx := Background
	dname := fmt.Sprintf("largedir%d", n)
	var inode Ino
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

func BenchmarkReaddir(b *testing.B) {
	var conf RedisConfig
	m, err := NewRedisMeta("redis://127.0.0.1/10", &conf)
	if err != nil {
		b.Skipf("redis is not available: %s", err)
	}
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()

	cases := []struct {
		desc string
		size int
	}{
		{"10", 10},
		{"1k", 1000},
		// {"100k", 100000},
		// {"10m", 10000000},
	}
	for _, c := range cases {
		b.Run(c.desc, func(b *testing.B) { readdir(b, m, c.size) })
	}
}
