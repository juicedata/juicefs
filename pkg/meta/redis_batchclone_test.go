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

package meta

import (
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisSliceRefCount(t *testing.T, m *redisMeta, id uint64, size uint32) int64 {
	t.Helper()
	v, err := m.rdb.HGet(Background(), m.sliceRefs(), m.sliceKey(id, size)).Int64()
	if err == redis.Nil {
		return 0
	}
	if err != nil {
		t.Fatalf("HGet sliceRef for %d/%d: %v", id, size, err)
	}
	return v
}

func TestRedisBatchCloneSharedChunkRefs(t *testing.T) {
	metaClient, err := newRedisMeta("redis", "127.0.0.1:6379/11", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	m, ok := metaClient.(*redisMeta)
	if !ok {
		t.Fatalf("expected *redisMeta, got %T", metaClient)
	}
	defer m.Shutdown()

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_shared", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_shared: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_shared", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_shared: %s", st)
	}

	var fileA Ino
	if st := m.Mknod(ctx, srcDir, "file_A", TypeFile, 0644, 022, 0, "", &fileA, nil); st != 0 {
		t.Fatalf("mknod file_A: %s", st)
	}
	var sliceID uint64
	if st := m.NewSlice(ctx, &sliceID); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	const chunkSize = uint32(4096)
	if st := m.Write(ctx, fileA, 0, 0, Slice{Id: sliceID, Size: chunkSize, Off: 0, Len: chunkSize}, time.Now()); st != 0 {
		t.Fatalf("write file_A: %s", st)
	}

	// file_B is a hardlink to file_A, so two entries in the same batch share the exact source chunk.
	if st := m.Link(ctx, fileA, srcDir, "file_B", nil); st != 0 {
		t.Fatalf("link file_B -> file_A: %s", st)
	}

	before := redisSliceRefCount(t, m, sliceID, chunkSize)

	var listed []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &listed); st != 0 {
		t.Fatalf("readdir src_shared: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range listed {
		n := string(e.Name)
		if n == "." || n == ".." {
			continue
		}
		if n == "file_A" || n == "file_B" {
			batchEntries = append(batchEntries, e)
		}
	}
	if len(batchEntries) != 2 {
		t.Fatalf("expected 2 batch entries, got %d", len(batchEntries))
	}

	var cloned uint64
	st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned)
	if st != 0 {
		t.Fatalf("BatchClone shared chunk entries: %s", st)
	}
	if cloned != 2 {
		t.Fatalf("BatchClone cloned count mismatch: want 2 got %d", cloned)
	}

	after := redisSliceRefCount(t, m, sliceID, chunkSize)
	if after != before+2 {
		t.Fatalf("sliceRef mismatch after batch clone: before=%d after=%d want=%d", before, after, before+2)
	}

	var dstA, dstB Ino
	var dstAAttr, dstBAttr Attr
	if st := m.Lookup(ctx, dstDir, "file_A", &dstA, &dstAAttr, false); st != 0 {
		t.Fatalf("lookup dst file_A: %s", st)
	}
	if st := m.Lookup(ctx, dstDir, "file_B", &dstB, &dstBAttr, false); st != 0 {
		t.Fatalf("lookup dst file_B: %s", st)
	}
	if dstA == dstB {
		t.Fatalf("cloned hardlink entries should become independent files, got same inode %d", dstA)
	}
	if dstAAttr.Typ != TypeFile || dstBAttr.Typ != TypeFile {
		t.Fatalf("cloned entries should be files, got types %d and %d", dstAAttr.Typ, dstBAttr.Typ)
	}
	if dstAAttr.Nlink != 1 || dstBAttr.Nlink != 1 {
		t.Fatalf("cloned files should have nlink=1, got %d and %d", dstAAttr.Nlink, dstBAttr.Nlink)
	}

	// Sanity check that batch path did not request fallback.
	if st == syscall.ENOTSUP {
		t.Fatalf("BatchClone unexpectedly returned ENOTSUP")
	}
}

func TestRedisBatchCloneMixedFilesAndSymlinks(t *testing.T) {
	metaClient, err := newRedisMeta("redis", "127.0.0.1:6379/12", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	m, ok := metaClient.(*redisMeta)
	if !ok {
		t.Fatalf("expected *redisMeta, got %T", metaClient)
	}
	defer m.Shutdown()

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_mix", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_mix: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_mix", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_mix: %s", st)
	}

	for _, name := range []string{"file1", "file2", "file3"} {
		var ino Ino
		if st := m.Mknod(ctx, srcDir, name, TypeFile, 0644, 022, 0, "", &ino, nil); st != 0 {
			t.Fatalf("mknod %s: %s", name, st)
		}
	}

	var link1, link2 Ino
	if st := m.Symlink(ctx, srcDir, "link_to_file1", "file1", &link1, nil); st != 0 {
		t.Fatalf("symlink link_to_file1: %s", st)
	}
	if st := m.Symlink(ctx, srcDir, "link_to_file3", "file3", &link2, nil); st != 0 {
		t.Fatalf("symlink link_to_file3: %s", st)
	}

	var listed []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &listed); st != 0 {
		t.Fatalf("readdir src_mix: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range listed {
		n := string(e.Name)
		if n == "." || n == ".." {
			continue
		}
		batchEntries = append(batchEntries, e)
	}
	if len(batchEntries) != 5 {
		t.Fatalf("expected 5 entries in batch, got %d", len(batchEntries))
	}

	var cloned uint64
	st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned)
	if st != 0 {
		t.Fatalf("BatchClone mixed files/symlinks: %s", st)
	}
	if cloned != 5 {
		t.Fatalf("BatchClone cloned count mismatch: want 5 got %d", cloned)
	}

	for _, name := range []string{"file1", "file2", "file3"} {
		var ino Ino
		var attr Attr
		if st := m.Lookup(ctx, dstDir, name, &ino, &attr, false); st != 0 {
			t.Fatalf("lookup cloned %s: %s", name, st)
		}
		if attr.Typ != TypeFile {
			t.Fatalf("cloned %s type mismatch: want file got %d", name, attr.Typ)
		}
	}

	checkLink := func(name string, want string) {
		var ino Ino
		var attr Attr
		if st := m.Lookup(ctx, dstDir, name, &ino, &attr, false); st != 0 {
			t.Fatalf("lookup cloned symlink %s: %s", name, st)
		}
		if attr.Typ != TypeSymlink {
			t.Fatalf("cloned %s type mismatch: want symlink got %d", name, attr.Typ)
		}
		var got []byte
		if st := m.ReadLink(ctx, ino, &got); st != 0 {
			t.Fatalf("readlink %s: %s", name, st)
		}
		if string(got) != want {
			t.Fatalf("symlink target mismatch for %s: want %q got %q", name, want, string(got))
		}
	}
	checkLink("link_to_file1", "file1")
	checkLink("link_to_file3", "file3")

	if st == syscall.ENOTSUP {
		t.Fatalf("BatchClone unexpectedly returned ENOTSUP")
	}
}

func TestRedisBatchCloneSpaceAccounting(t *testing.T) {
	metaClient, err := newRedisMeta("redis", "127.0.0.1:6379/13", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	m, ok := metaClient.(*redisMeta)
	if !ok {
		t.Fatalf("expected *redisMeta, got %T", metaClient)
	}
	defer m.Shutdown()

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_space", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_space: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_space", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_space: %s", st)
	}

	fileSizes := []uint32{4096, 8192, 12288}
	var expectedSpaceDelta uint64
	var expectedInodeDelta uint64 = uint64(len(fileSizes))
	for i, sz := range fileSizes {
		var ino Ino
		name := "f" + string(rune('1'+i))
		if st := m.Mknod(ctx, srcDir, name, TypeFile, 0644, 022, 0, "", &ino, nil); st != 0 {
			t.Fatalf("mknod %s: %s", name, st)
		}
		var sid uint64
		if st := m.NewSlice(ctx, &sid); st != 0 {
			t.Fatalf("new slice for %s: %s", name, st)
		}
		if st := m.Write(ctx, ino, 0, 0, Slice{Id: sid, Size: sz, Off: 0, Len: sz}, time.Now()); st != 0 {
			t.Fatalf("write %s: %s", name, st)
		}
		expectedSpaceDelta += uint64(align4K(uint64(sz)))
	}

	var totalBefore, availBefore, iusedBefore, iavailBefore uint64
	if st := m.StatFS(ctx, RootInode, &totalBefore, &availBefore, &iusedBefore, &iavailBefore); st != 0 {
		t.Fatalf("StatFS before clone: %s", st)
	}
	usedBefore := totalBefore - availBefore

	var listed []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &listed); st != 0 {
		t.Fatalf("readdir src_space: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range listed {
		n := string(e.Name)
		if n == "." || n == ".." {
			continue
		}
		batchEntries = append(batchEntries, e)
	}
	if len(batchEntries) != len(fileSizes) {
		t.Fatalf("expected %d batch entries, got %d", len(fileSizes), len(batchEntries))
	}

	var cloned uint64
	st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned)
	if st != 0 {
		t.Fatalf("BatchClone space accounting: %s", st)
	}
	if cloned != uint64(len(fileSizes)) {
		t.Fatalf("BatchClone cloned count mismatch: want %d got %d", len(fileSizes), cloned)
	}

	var totalAfter, availAfter, iusedAfter, iavailAfter uint64
	deadline := time.Now().Add(2 * time.Second)
	for {
		if st := m.StatFS(ctx, RootInode, &totalAfter, &availAfter, &iusedAfter, &iavailAfter); st != 0 {
			t.Fatalf("StatFS after clone: %s", st)
		}
		usedDelta := (totalAfter - availAfter) - usedBefore
		inodeDelta := iusedAfter - iusedBefore
		if usedDelta == expectedSpaceDelta && inodeDelta == expectedInodeDelta {
			t.Logf("space/inode delta verified: usedDelta=%d inodeDelta=%d", usedDelta, inodeDelta)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("space/inode delta mismatch: usedDelta=%d want=%d inodeDelta=%d want=%d",
				usedDelta, expectedSpaceDelta, inodeDelta, expectedInodeDelta)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRedisBatchCloneMultiChunkFile(t *testing.T) {
	metaClient, err := newRedisMeta("redis", "127.0.0.1:6379/14", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	m, ok := metaClient.(*redisMeta)
	if !ok {
		t.Fatalf("expected *redisMeta, got %T", metaClient)
	}
	defer m.Shutdown()

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_multi_chunk", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_multi_chunk: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_multi_chunk", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_multi_chunk: %s", st)
	}

	var srcFile Ino
	if st := m.Mknod(ctx, srcDir, "bigfile", TypeFile, 0644, 022, 0, "", &srcFile, nil); st != 0 {
		t.Fatalf("mknod bigfile: %s", st)
	}

	var slice1, slice2 uint64
	if st := m.NewSlice(ctx, &slice1); st != 0 {
		t.Fatalf("new slice1: %s", st)
	}
	if st := m.NewSlice(ctx, &slice2); st != 0 {
		t.Fatalf("new slice2: %s", st)
	}

	const chunkSize1 = uint32(4096)
	const chunkSize2 = uint32(8192)
	if st := m.Write(ctx, srcFile, 0, 0, Slice{Id: slice1, Size: chunkSize1, Off: 0, Len: chunkSize1}, time.Now()); st != 0 {
		t.Fatalf("write chunk 0: %s", st)
	}
	if st := m.Write(ctx, srcFile, 1, 0, Slice{Id: slice2, Size: chunkSize2, Off: 0, Len: chunkSize2}, time.Now()); st != 0 {
		t.Fatalf("write chunk 1: %s", st)
	}

	beforeRef1 := redisSliceRefCount(t, m, slice1, chunkSize1)
	beforeRef2 := redisSliceRefCount(t, m, slice2, chunkSize2)

	var listed []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &listed); st != 0 {
		t.Fatalf("readdir src_multi_chunk: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range listed {
		n := string(e.Name)
		if n == "." || n == ".." {
			continue
		}
		batchEntries = append(batchEntries, e)
	}
	if len(batchEntries) != 1 {
		t.Fatalf("expected 1 batch entry, got %d", len(batchEntries))
	}

	srcChunk0, err := m.rdb.LRange(ctx, m.chunkKey(srcFile, 0), 0, -1).Result()
	if err != nil {
		t.Fatalf("read src chunk 0: %v", err)
	}
	srcChunk1, err := m.rdb.LRange(ctx, m.chunkKey(srcFile, 1), 0, -1).Result()
	if err != nil {
		t.Fatalf("read src chunk 1: %v", err)
	}
	if len(srcChunk0) == 0 || len(srcChunk1) == 0 {
		t.Fatalf("source file should contain two non-empty chunks, got chunk0=%d chunk1=%d", len(srcChunk0), len(srcChunk1))
	}

	var cloned uint64
	st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned)
	if st != 0 {
		t.Fatalf("BatchClone multi-chunk file: %s", st)
	}
	if cloned != 1 {
		t.Fatalf("BatchClone cloned count mismatch: want 1 got %d", cloned)
	}

	afterRef1 := redisSliceRefCount(t, m, slice1, chunkSize1)
	afterRef2 := redisSliceRefCount(t, m, slice2, chunkSize2)
	if afterRef1 != beforeRef1+1 {
		t.Fatalf("slice1 ref mismatch: before=%d after=%d want=%d", beforeRef1, afterRef1, beforeRef1+1)
	}
	if afterRef2 != beforeRef2+1 {
		t.Fatalf("slice2 ref mismatch: before=%d after=%d want=%d", beforeRef2, afterRef2, beforeRef2+1)
	}

	var dstIno Ino
	var dstAttr Attr
	if st := m.Lookup(ctx, dstDir, "bigfile", &dstIno, &dstAttr, false); st != 0 {
		t.Fatalf("lookup cloned bigfile: %s", st)
	}
	if dstAttr.Length <= ChunkSize {
		t.Fatalf("cloned file should span multiple chunks, length=%d chunkSize=%d", dstAttr.Length, ChunkSize)
	}

	dstChunk0, err := m.rdb.LRange(ctx, m.chunkKey(dstIno, 0), 0, -1).Result()
	if err != nil {
		t.Fatalf("read dst chunk 0: %v", err)
	}
	dstChunk1, err := m.rdb.LRange(ctx, m.chunkKey(dstIno, 1), 0, -1).Result()
	if err != nil {
		t.Fatalf("read dst chunk 1: %v", err)
	}
	if !reflect.DeepEqual(srcChunk0, dstChunk0) {
		t.Fatalf("chunk 0 data mismatch after clone")
	}
	if !reflect.DeepEqual(srcChunk1, dstChunk1) {
		t.Fatalf("chunk 1 data mismatch after clone")
	}
}

func TestRedisBatchClonePartialFailureLeavesState(t *testing.T) {
	metaClient, err := newRedisMeta("redis", "127.0.0.1:6379/15", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	m, ok := metaClient.(*redisMeta)
	if !ok {
		t.Fatalf("expected *redisMeta, got %T", metaClient)
	}
	defer m.Shutdown()

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := Background()
	var srcDir, dstDir Ino
	if st := m.Mkdir(ctx, RootInode, "src_partial", 0777, 022, 0, &srcDir, nil); st != 0 {
		t.Fatalf("mkdir src_partial: %s", st)
	}
	if st := m.Mkdir(ctx, RootInode, "dst_partial", 0777, 022, 0, &dstDir, nil); st != 0 {
		t.Fatalf("mkdir dst_partial: %s", st)
	}

	var srcFile Ino
	if st := m.Mknod(ctx, srcDir, "victim", TypeFile, 0644, 022, 0, "", &srcFile, nil); st != 0 {
		t.Fatalf("mknod victim: %s", st)
	}
	var sid uint64
	if st := m.NewSlice(ctx, &sid); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	const chunkSize = uint32(4096)
	if st := m.Write(ctx, srcFile, 0, 0, Slice{Id: sid, Size: chunkSize, Off: 0, Len: chunkSize}, time.Now()); st != 0 {
		t.Fatalf("write victim: %s", st)
	}

	var listed []*Entry
	if st := m.Readdir(ctx, srcDir, 1, &listed); st != 0 {
		t.Fatalf("readdir src_partial: %s", st)
	}
	var batchEntries []*Entry
	for _, e := range listed {
		n := string(e.Name)
		if n == "." || n == ".." {
			continue
		}
		batchEntries = append(batchEntries, e)
	}
	if len(batchEntries) != 1 {
		t.Fatalf("expected 1 batch entry, got %d", len(batchEntries))
	}

	var totalBefore, availBefore, iusedBefore, iavailBefore uint64
	if st := m.StatFS(ctx, RootInode, &totalBefore, &availBefore, &iusedBefore, &iavailBefore); st != 0 {
		t.Fatalf("StatFS before failure injection: %s", st)
	}
	usedBefore := totalBefore - availBefore

	// Fault injection: force sliceRef HINCRBY to fail with WRONGTYPE.
	if err := m.rdb.Set(ctx, m.sliceRefs(), "wrong-type", 0).Err(); err != nil {
		t.Fatalf("inject wrong type into %s: %v", m.sliceRefs(), err)
	}

	var cloned uint64
	st := m.getBase().BatchClone(ctx, srcDir, dstDir, batchEntries, CLONE_MODE_PRESERVE_ATTR, 022, &cloned)
	if st == 0 {
		t.Fatalf("expected batch clone failure after WRONGTYPE injection")
	}
	if st == syscall.ENOTSUP {
		t.Fatalf("expected hard failure, got ENOTSUP fallback")
	}
	if cloned != 0 {
		t.Fatalf("count should not be incremented on failed BatchClone, got %d", cloned)
	}

	// Validate current behavior: failed call can still leave cloned state written.
	var leakedIno Ino
	var leakedAttr Attr
	if st := m.Lookup(ctx, dstDir, "victim", &leakedIno, &leakedAttr, false); st != 0 {
		t.Fatalf("expected leaked cloned entry after failed BatchClone, lookup got: %s", st)
	}

	var totalAfter, availAfter, iusedAfter, iavailAfter uint64
	if st := m.StatFS(ctx, RootInode, &totalAfter, &availAfter, &iusedAfter, &iavailAfter); st != 0 {
		t.Fatalf("StatFS after failed BatchClone: %s", st)
	}
	usedDelta := (totalAfter - availAfter) - usedBefore
	inodeDelta := iusedAfter - iusedBefore
	if usedDelta == 0 || inodeDelta == 0 {
		t.Fatalf("expected partial writes after failed BatchClone, got usedDelta=%d inodeDelta=%d", usedDelta, inodeDelta)
	}
	t.Logf("failed BatchClone left state: status=%s leakedIno=%d usedDelta=%d inodeDelta=%d", st, leakedIno, usedDelta, inodeDelta)
}
