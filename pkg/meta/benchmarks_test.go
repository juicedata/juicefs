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

	"github.com/sirupsen/logrus"
)

const (
	redisAddr = "redis://127.0.0.1/10"
	// redisAddr = "redis://127.0.0.1:7369" // Titan
	sqlAddr = "sqlite3://juicefs.db"
	// sqlAddr = "mysql://root:@/juicefs" // MySQL
	// sqlAddr = "mysql://root:@tcp(127.0.0.1:4000)/juicefs" // TiDB
	tkvAddr = "tikv://127.0.0.1:2379/juicefs"
)

func init() {
	utils.SetLogLevel(logrus.InfoLevel)
	// utils.SetOutFile("bench-test.log")
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

func encodeSlicesAsBuf(nSlices uint32) []byte {
	w := utils.NewBuffer(nSlices * sliceBytes)
	for i := uint32(0); i < nSlices; i++ {
		w.Put32(0)
		w.Put64(1014)
		w.Put32(122)
		w.Put32(0)
		w.Put32(122)
	}
	return w.Bytes()
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

func BenchmarkReadSliceBuf(b *testing.B) {
	cases := []struct {
		desc string
		size uint32
	}{
		{"small", 4},
		{"mid", 64},
		{"large", 1024},
	}
	for _, c := range cases {
		b.Run(c.desc, func(b *testing.B) {
			buf := encodeSlicesAsBuf(c.size)
			b.ResetTimer()
			var slices []*slice
			for i := 0; i < b.N; i++ {
				slices = readSliceBuf(buf)
			}
			if len(slices) != int(c.size) {
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

func benchMkdir(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchMkdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Mkdir(ctx, parent, fmt.Sprintf("d%d", i), 0755, 0, 0, nil, nil); err != 0 {
			b.Fatalf("mkdir: %s", err)
		}
	}
}

func benchMvdir(b *testing.B, m Meta) { // rename dir
	var parent Ino
	if err := prepareParent(m, "benchMvdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Mkdir(ctx, parent, "d0", 0755, 0, 0, nil, nil); err != 0 {
		b.Fatalf("mkdir: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Rename(ctx, parent, fmt.Sprintf("d%d", i), parent, fmt.Sprintf("d%d", i+1), 0, nil, nil); err != 0 {
			b.Fatalf("rename dir: %s", err)
		}
	}
}

func benchRmdir(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchRmdir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := m.Mkdir(ctx, parent, "dir", 0755, 0, 0, nil, nil); err != 0 {
			b.Fatalf("mkdir: %s", err)
		}
		b.StartTimer()
		if err := m.Rmdir(ctx, parent, "dir"); err != 0 {
			b.Fatalf("rmdir: %s", err)
		}
	}
}

func benchResolve(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchResolve", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var child Ino = parent
	for i := 0; i < 5; i++ {
		if err := m.Mkdir(ctx, child, "d", 0755, 0, 0, &child, nil); err != 0 {
			b.Fatalf("mkdir: %s", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Resolve(ctx, parent, "d/d/d/d/d", nil, nil); err != 0 {
			if err == syscall.ENOTSUP {
				b.SkipNow()
				return
			}
			b.Fatalf("resolve: %s", err)
		}
	}
}

func benchReaddir(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchReaddir", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	for j := 0; j < n; j++ {
		if err := m.Create(ctx, parent, fmt.Sprintf("f%d", j), 0644, 022, 0, nil, nil); err != 0 {
			b.Fatalf("create: %s", err)
		}
	}
	var entries []*Entry
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Readdir(ctx, parent, 1, &entries); err != 0 {
			b.Fatalf("readdir: %s", err)
		}
		if len(entries) != n+2 {
			b.Fatalf("files: %d != %d", len(entries), n+2)
		}
	}
}

func benchMknod(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchMknod", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Mknod(ctx, parent, fmt.Sprintf("f%d", i), TypeFile, 0644, 022, 0, nil, nil); err != 0 {
			b.Fatalf("mknod: %s", err)
		}
	}
}

func benchCreate(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchCreate", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Create(ctx, parent, fmt.Sprintf("f%d", i), 0644, 022, 0, nil, nil); err != 0 {
			b.Fatalf("create: %s", err)
		}
	}
}

func benchRename(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchRename", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "f0", 0644, 022, 0, nil, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Rename(ctx, parent, fmt.Sprintf("f%d", i), parent, fmt.Sprintf("f%d", i+1), 0, nil, nil); err != 0 {
			b.Fatalf("rename file: %s", err)
		}
	}
}

func benchUnlink(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchUnlink", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := m.Create(ctx, parent, "file", 0644, 022, 0, nil, nil); err != 0 {
			b.Fatalf("create: %s", err)
		}
		b.StartTimer()
		if err := m.Unlink(ctx, parent, "file"); err != 0 {
			b.Fatalf("unlink: %s", err)
		}
	}
}

func benchLookup(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchLookup", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, nil, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	var inode Ino
	var attr Attr
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Lookup(ctx, parent, "file", &inode, &attr); err != 0 {
			b.Fatalf("lookup: %s", err)
		}
	}
}

func benchGetAttr(b *testing.B, m Meta) {
	var parent, inode Ino
	if err := prepareParent(m, "benchGetAttr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	var attr Attr
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.GetAttr(ctx, inode, &attr); err != 0 {
			b.Fatalf("getattr: %s", err)
		}
	}
}

func benchSetAttr(b *testing.B, m Meta) {
	var parent, inode Ino
	if err := prepareParent(m, "benchSetAttr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	var attr = Attr{Mode: 0644}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		attr.Mode ^= 1
		if err := m.SetAttr(ctx, inode, SetAttrMode, 0, &attr); err != 0 {
			b.Fatalf("setattr: %s", err)
		}
	}
}

func benchAccess(b *testing.B, m Meta) { // contains a Getattr
	var parent, inode Ino
	if err := prepareParent(m, "benchAccess", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	myCtx := NewContext(100, 1, []uint32{1})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Access(myCtx, inode, 4, nil); err != 0 && err != syscall.EACCES {
			b.Fatalf("access: %s", err)
		}
	}
}

func benchSetXattr(b *testing.B, m Meta) {
	var parent, inode Ino
	if err := prepareParent(m, "benchSetXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	value := []byte("value0")
	for i := 0; i < b.N; i++ {
		value[5] = byte(i%10 + 48)
		if err := m.SetXattr(ctx, inode, "key", value, 0); err != 0 {
			b.Fatalf("setxattr: %s", err)
		}
	}
}

func benchGetXattr(b *testing.B, m Meta) {
	var parent, inode Ino
	if err := prepareParent(m, "benchGetXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	if err := m.SetXattr(ctx, inode, "key", []byte("value"), 0); err != 0 {
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

func benchRemoveXattr(b *testing.B, m Meta) {
	var parent, inode Ino
	if err := prepareParent(m, "benchRemoveXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := m.SetXattr(ctx, inode, "key", []byte("value"), 0); err != 0 {
			b.Fatalf("setxattr: %s", err)
		}
		b.StartTimer()
		if err := m.RemoveXattr(ctx, inode, "key"); err != 0 {
			b.Fatalf("removexattr: %s", err)
		}
	}
}

func benchListXattr(b *testing.B, m Meta, n int) {
	var parent, inode Ino
	if err := prepareParent(m, "benchListXattr", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	if err := m.Create(ctx, parent, "fxattr", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	for j := 0; j < n; j++ {
		if err := m.SetXattr(ctx, inode, fmt.Sprintf("key%d", j), []byte("value"), 0); err != 0 {
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
	if err := m.Create(ctx, parent, "source", 0644, 022, 0, &inode, nil); err != 0 {
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
	if err := m.Create(ctx, parent, "source", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Symlink(ctx, parent, fmt.Sprintf("s%d", i), "/benchSymlink/source", nil, nil); err != 0 {
			b.Fatalf("symlink: %s", err)
		}
	}
}

/*
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
*/

func benchNewChunk(b *testing.B, m Meta) {
	ctx := Background
	var chunkid uint64
	for i := 0; i < b.N; i++ {
		if err := m.NewChunk(ctx, 1, 0, 0, &chunkid); err != 0 {
			b.Fatalf("newchunk: %s", err)
		}
	}
}

func benchWrite(b *testing.B, m Meta) {
	var parent Ino
	if err := prepareParent(m, "benchWrite", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	var (
		chunkid uint64
		offset  uint32
		step    uint32 = 1024
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.NewChunk(ctx, inode, 0, 0, &chunkid); err != 0 {
			b.Fatalf("newchunk: %s", err)
		}
		if err := m.Write(ctx, inode, 0, offset, Slice{Chunkid: chunkid, Size: step, Len: step}); err != 0 {
			b.Fatalf("write: %s", err)
		}
		offset += step
		if offset+step > ChunkSize {
			offset = 0
		}
	}
}

func benchRead(b *testing.B, m Meta, n int) {
	var parent Ino
	if err := prepareParent(m, "benchRead", &parent); err != nil {
		b.Fatal(err)
	}
	ctx := Background
	var inode Ino
	if err := m.Create(ctx, parent, "file", 0644, 022, 0, &inode, nil); err != 0 {
		b.Fatalf("create: %s", err)
	}
	var chunkid uint64
	var step uint32 = 1024
	for j := 0; j < n; j++ {
		if err := m.NewChunk(ctx, inode, 0, 0, &chunkid); err != 0 {
			b.Fatalf("newchunk: %s", err)
		}
		if err := m.Write(ctx, inode, 0, uint32(j)*step, Slice{Chunkid: chunkid, Size: step, Len: step}); err != 0 {
			b.Fatalf("write: %s", err)
		}
	}
	var slices []Slice
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Read(ctx, inode, 0, &slices); err != 0 {
			b.Fatalf("read: %s", err)
		}
	}
}

func benchmarkDir(b *testing.B, m Meta) { // mkdir, rename dir, rmdir, readdir
	_ = m.Init(Format{Name: "benchmarkDir"}, true)
	_ = m.NewSession()
	b.Run("mkdir", func(b *testing.B) { benchMkdir(b, m) })
	b.Run("mvdir", func(b *testing.B) { benchMvdir(b, m) })
	b.Run("rmdir", func(b *testing.B) { benchRmdir(b, m) })
	b.Run("resolve", func(b *testing.B) { benchResolve(b, m) })
	b.Run("readdir_10", func(b *testing.B) { benchReaddir(b, m, 10) })
	b.Run("readdir_1k", func(b *testing.B) { benchReaddir(b, m, 1000) })
	// b.Run("readdir_100k", func(b *testing.B) { benchReaddir(b, m, 100000) })
}

func BenchmarkRedisDir(b *testing.B) {
	m := NewClient(redisAddr, &Config{})
	benchmarkDir(b, m)
}
func BenchmarkSQLDir(b *testing.B) {
	m := NewClient(sqlAddr, &Config{})
	benchmarkDir(b, m)
}

func BenchmarkTKVDir(b *testing.B) {
	m := NewClient(tkvAddr, &Config{})
	benchmarkDir(b, m)
}

func benchmarkFile(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "benchmarkFile"}, true)
	_ = m.NewSession()
	b.Run("mknod", func(b *testing.B) { benchMknod(b, m) })
	b.Run("create", func(b *testing.B) { benchCreate(b, m) })
	b.Run("rename", func(b *testing.B) { benchRename(b, m) })
	b.Run("unlink", func(b *testing.B) { benchUnlink(b, m) })
	b.Run("lookup", func(b *testing.B) { benchLookup(b, m) })
	b.Run("getattr", func(b *testing.B) { benchGetAttr(b, m) })
	b.Run("setattr", func(b *testing.B) { benchSetAttr(b, m) })
	b.Run("access", func(b *testing.B) { benchAccess(b, m) })
}

func BenchmarkRedisFile(b *testing.B) {
	m := NewClient(redisAddr, &Config{})
	benchmarkFile(b, m)
}

func BenchmarkSQLFile(b *testing.B) {
	m := NewClient(sqlAddr, &Config{})
	benchmarkFile(b, m)
}

func BenchmarkTKVFile(b *testing.B) {
	m := NewClient(tkvAddr, &Config{})
	benchmarkFile(b, m)
}

func benchmarkXattr(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "benchmarkXattr"}, true)
	_ = m.NewSession()
	b.Run("setxattr", func(b *testing.B) { benchSetXattr(b, m) })
	b.Run("getxattr", func(b *testing.B) { benchGetXattr(b, m) })
	b.Run("removexattr", func(b *testing.B) { benchRemoveXattr(b, m) })
	b.Run("listxattr_1", func(b *testing.B) { benchListXattr(b, m, 1) })
	b.Run("listxattr_10", func(b *testing.B) { benchListXattr(b, m, 10) })
}

func BenchmarkRedisXattr(b *testing.B) {
	m := NewClient(redisAddr, &Config{})
	benchmarkXattr(b, m)
}

func BenchmarkSQLXattr(b *testing.B) {
	m := NewClient(sqlAddr, &Config{})
	benchmarkXattr(b, m)
}

func BenchmarkTKVXattr(b *testing.B) {
	m := NewClient(tkvAddr, &Config{})
	benchmarkXattr(b, m)
}

func benchmarkLink(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "benchmarkLink"}, true)
	_ = m.NewSession()
	b.Run("link", func(b *testing.B) { benchLink(b, m) })
	b.Run("symlink", func(b *testing.B) { benchSymlink(b, m) })
	// maybe meaningless since symlink would be cached
	// b.Run("readlink", func(b *testing.B) { benchReadlink(b, m) })
}

func BenchmarkRedisLink(b *testing.B) {
	m := NewClient(redisAddr, &Config{})
	benchmarkLink(b, m)
}

func BenchmarkSQLLink(b *testing.B) {
	m := NewClient(sqlAddr, &Config{})
	benchmarkLink(b, m)
}

func BenchmarkTKVLink(b *testing.B) {
	m := NewClient(tkvAddr, &Config{})
	benchmarkLink(b, m)
}

func benchmarkData(b *testing.B, m Meta) {
	_ = m.Init(Format{Name: "benchmarkData"}, true)
	m.OnMsg(DeleteChunk, func(args ...interface{}) error { return nil })
	m.OnMsg(CompactChunk, func(args ...interface{}) error { return nil })
	_ = m.NewSession()
	b.Run("newchunk", func(b *testing.B) { benchNewChunk(b, m) })
	b.Run("write", func(b *testing.B) { benchWrite(b, m) })
	b.Run("read_1", func(b *testing.B) { benchRead(b, m, 1) })
	b.Run("read_10", func(b *testing.B) { benchRead(b, m, 10) })
}

func BenchmarkRedisData(b *testing.B) {
	m := NewClient(redisAddr, &Config{})
	benchmarkData(b, m)
}

func BenchmarkSQLData(b *testing.B) {
	m := NewClient(sqlAddr, &Config{})
	benchmarkData(b, m)
}

func BenchmarkTKVData(b *testing.B) {
	m := NewClient(tkvAddr, &Config{})
	benchmarkData(b, m)
}
