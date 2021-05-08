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

func prepareParent(m Meta, name string, inode *Ino) error {
	ctx := Background
	if err := Remove(m, ctx, 1, name); err != 0 && err != syscall.ENOENT {
		return fmt.Errorf("remove: %s", err)
	}
	if err := m.Mkdir(ctx, 1, name, 0755, 0, 0, inode, nil); err != 0 {
		return fmt.Errorf("mkdir: %s", err)
	}
	return nil
}

func mkdir(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchMkdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j++ {
			if err := m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
	}
}

func mvdir(b *testing.B, m Meta, n int) { // rename dir
	var parent Ino
	if err := prepareParent(m, "benchMvdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Rename(ctx, parent, fmt.Sprintf("d%d_%d", i, j), parent, fmt.Sprintf("rd%d_%d", i, j), nil, nil); err != 0 {
				b.Fatalf("rename dir: %s", err)
			}
		}
	}
}

func rmdir(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchRmdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Mkdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j), 0755, 0, 0, nil, nil); err != 0 {
				b.Fatalf("mkdir: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Rmdir(ctx, parent, fmt.Sprintf("d%d_%d", i, j)); err != 0 {
				b.Fatalf("rmdir: %s", err)
			}
		}
	}
}

func readdir(b *testing.B, m Meta, n int) {
	ctx := Background
	dname := fmt.Sprintf("largedir%d", n)
	var inode Ino
	var es []*Entry
	if m.Lookup(ctx, 1, dname, &inode, nil) == 0 && m.Readdir(ctx, inode, 0, &es) == 0 && len(es) == n+2 {
	} else {
		_ = Remove(m, ctx, 1, dname)
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

func mknod(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchMknod", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j++ {
			if err := m.Mknod(ctx, parent, fmt.Sprintf("f%d_%d", i, j), TypeFile, 0644, 022, 0, nil, nil); err != 0 {
				b.Fatalf("mknod: %s", err)
			}
		}
	}
}

func create(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchCreate", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, nil, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
		}
	}
}

func rename(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchRename", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, nil, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Rename(ctx, parent, fmt.Sprintf("f%d_%d", i, j), parent, fmt.Sprintf("rf%d_%d", i, j), nil, nil); err != 0 {
				b.Fatalf("rename file: %s", err)
			}
		}
	}
}

func unlink(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchUnlink", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, nil, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Unlink(ctx, parent, fmt.Sprintf("f%d_%d", i, j)); err != 0 {
				b.Fatalf("unlink: %s", err)
			}
		}
	}
}

func lookup(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchLookup", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	var attr Attr
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, nil, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Lookup(ctx, parent, fmt.Sprintf("f%d_%d", i, j), &inode, &attr); err != 0 {
				b.Fatalf("lookup: %s", err)
			}
		}
	}
}

func getattr(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchGetAttr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	var attr Attr
	inodes := make([]Ino, n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, &inode, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
			inodes[j] = inode
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.GetAttr(ctx, inodes[j], &attr); err != 0 {
				b.Fatalf("getattr: %s", err)
			}
		}
	}
}

func setattr(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchSetAttr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	var attr = Attr{Mode: 0755}
	inodes := make([]Ino, n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, &inode, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
			inodes[j] = inode
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.SetAttr(ctx, inodes[j], SetAttrMode, 0, &attr); err != 0 {
				b.Fatalf("setattr: %s", err)
			}
		}
	}
}

func access(b *testing.B, m Meta, n int) { // contains a Getattr
	var parent Ino
	if err := prepareParent(m, "benchAccess", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	myCtx := NewContext(100, 1, []uint32{1})
	var inode Ino
	inodes := make([]Ino, n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < n; j++ {
			if err := m.Create(ctx, parent, fmt.Sprintf("f%d_%d", i, j), 0644, 022, &inode, nil); err != 0 {
				b.Fatalf("create: %s", err)
			}
			inodes[j] = inode
		}
		b.StartTimer()
		for j := 0; j < n; j++ {
			if err := m.Access(myCtx, inodes[j], 4, nil); err != 0 && err != syscall.EACCES {
				b.Fatalf("access: %s", err)
			}
		}
	}
}

func setxattr(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchSetXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	value := []byte("value0")
	for i := 0; i < b.N; i++ {
		value[5] = byte(i%10 + 48)
		if err := m.SetXattr(ctx, inode, "key", value); err != 0 {
			b.Fatalf("setxattr: %s", err)
		}
	}
}

func getxattr(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchGetXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	if err := m.SetXattr(ctx, inode, "key", []byte("value")); err != 0 {
		b.Fatalf("setxattr: %s", err)
	}
	var buf []byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.GetXattr(ctx, inode, "key", &buf); err != 0 {
			b.Fatalf("getxattr: %s", err)
		}
	}
}

func removexattr(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchRemoveXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := m.SetXattr(ctx, inode, "key", []byte("value")); err != 0 {
			b.Fatalf("setxattr: %s", err)
		}
		b.StartTimer()
		if err := m.RemoveXattr(ctx, inode, "key"); err != 0 {
			b.Fatalf("removexattr: %s", err)
		}
	}
}

func listxattr(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchListXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	for j := 0; j < n; j++ {
		if err := m.SetXattr(ctx, inode, fmt.Sprintf("key%d", j), []byte("value")); err != 0 {
			b.Fatalf("setxattr: %s", err)
		}
	}
	var buf []byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.ListXattr(ctx, inode, &buf); err != 0 {
			b.Fatalf("removexattr: %s", err)
		}
	}
}

func benchLink(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchLink", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "source", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Link(ctx, inode, parent, fmt.Sprintf("l%d", i), nil); err != 0 {
			b.Fatalf("link: %s", err)
		}
	}
}

func benchSymlink(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchSymlink", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "source", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Symlink(ctx, parent, fmt.Sprintf("s%d", i), "/benchSymlink/source", nil, nil); err != 0 {
			b.Fatalf("symlink: %s", err)
		}
	}
}

func benchReadlink(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchReadlink", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "source", 0644, 022, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	if err := m.Symlink(ctx, parent, "slink", "/benchReadlink/source", &inode, nil); err != 0 {
		b.Fatalf("symlink: %s", err)
	}
	var buf []byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.ReadLink(ctx, inode, &buf); err != 0 {
			b.Fatalf("readlink: %s", err)
		}
	}
}

func benchmarkDir(b *testing.B, m Meta) { // mkdir, rename dir, rmdir
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()
	cases := []struct {
		desc string
		size int
	}{
		{"1", 1},
		// {"100", 100},
		// {"10k", 10000},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("mkdir_%s", c.desc), func(b *testing.B) { mkdir(b, m, c.size) })
		b.Run(fmt.Sprintf("mvdir_%s", c.desc), func(b *testing.B) { mvdir(b, m, c.size) })
		b.Run(fmt.Sprintf("rmdir_%s", c.desc), func(b *testing.B) { rmdir(b, m, c.size) })
	}
}

func BenchmarkRedisDir(b *testing.B) {
	m := NewClient("redis://127.0.0.1/10", &Config{})
	benchmarkDir(b, m)
}
func BenchmarkSQLDir(b *testing.B) {
	m := NewClient("sqlite3://test.db", &Config{})
	benchmarkDir(b, m)
}

func benchmarkReaddir(b *testing.B, m Meta) {
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

func BenchmarkRedisReaddir(b *testing.B) {
	m := NewClient("redis://127.0.0.1/10", &Config{})
	benchmarkReaddir(b, m)
}

func BenchmarkSQLReaddir(b *testing.B) {
	m := NewClient("sqlite3://test.db", &Config{})
	benchmarkReaddir(b, m)
}

func benchmarkFile(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()
	cases := []struct {
		desc string
		size int
	}{
		{"1", 1},
		// {"100", 100},
		// {"10k", 10000},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("mknod_%s", c.desc), func(b *testing.B) { mknod(b, m, c.size) })
		b.Run(fmt.Sprintf("create_%s", c.desc), func(b *testing.B) { create(b, m, c.size) })
		b.Run(fmt.Sprintf("rename_%s", c.desc), func(b *testing.B) { rename(b, m, c.size) })
		b.Run(fmt.Sprintf("unlink_%s", c.desc), func(b *testing.B) { unlink(b, m, c.size) })
		b.Run(fmt.Sprintf("lookup_%s", c.desc), func(b *testing.B) { lookup(b, m, c.size) })
		b.Run(fmt.Sprintf("getattr_%s", c.desc), func(b *testing.B) { getattr(b, m, c.size) })
		b.Run(fmt.Sprintf("setattr_%s", c.desc), func(b *testing.B) { setattr(b, m, c.size) })
		b.Run(fmt.Sprintf("access_%s", c.desc), func(b *testing.B) { access(b, m, c.size) })
	}
}

func BenchmarkRedisFile(b *testing.B) {
	m := NewClient("redis://127.0.0.1/10", &Config{})
	benchmarkFile(b, m)
}

func BenchmarkSQLFile(b *testing.B) {
	m := NewClient("sqlite3://test.db", &Config{})
	benchmarkFile(b, m)
}

func benchmarkXattr(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()
	b.Run("setxattr", func(b *testing.B) { setxattr(b, m) })
	b.Run("getxattr", func(b *testing.B) { getxattr(b, m) })
	b.Run("removexattr", func(b *testing.B) { removexattr(b, m) })
	b.Run("listxattr_1", func(b *testing.B) { listxattr(b, m, 1) })
	b.Run("listxattr_10", func(b *testing.B) { listxattr(b, m, 10) })
}

func BenchmarkRedisXattr(b *testing.B) {
	m := NewClient("redis://127.0.0.1/10", &Config{})
	benchmarkXattr(b, m)
}

func BenchmarkSQLXattr(b *testing.B) {
	m := NewClient("sqlite3://test.db", &Config{})
	benchmarkXattr(b, m)
}

func benchmarkLink(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "bench"}, true)
	_ = m.NewSession()
	b.Run("link", func(b *testing.B) { benchLink(b, m) })
	b.Run("symlink", func(b *testing.B) { benchSymlink(b, m) })
	// maybe meaningless since symlink would be cached
	// b.Run("readlink", func(b *testing.B) { benchReadlink(b, m) })
}

func BenchmarkRedisLink(b *testing.B) {
	m := NewClient("redis://127.0.0.1/10", &Config{})
	benchmarkLink(b, m)
}

func BenchmarkSQLLink(b *testing.B) {
	m := NewClient("sqlite3://test.db", &Config{})
	benchmarkLink(b, m)
}
