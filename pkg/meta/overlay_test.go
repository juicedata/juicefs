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

package meta

import (
	"syscall"
	"testing"
	"time"
)

func newTestMemMeta(t *testing.T) Meta {
	t.Helper()
	m := NewClient("memkv://overlay-test", DefaultConf())
	if err := m.Reset(); err != nil {
		t.Fatalf("reset memkv: %v", err)
	}
	if err := m.Init(&Format{Name: "test", BlockSize: 4096}, true); err != nil {
		t.Fatalf("init memkv: %v", err)
	}
	return m
}

func mustMkdir(t *testing.T, m Meta, parent Ino, name string) Ino {
	t.Helper()
	var ino Ino
	var attr Attr
	if st := m.Mkdir(Background(), parent, name, 0755, 0, 0, &ino, &attr); st != 0 {
		t.Fatalf("mkdir %q under %d: %s", name, parent, st)
	}
	return ino
}

func mustCreateWithSlice(t *testing.T, m Meta, parent Ino, name string, sliceID uint64, length uint32) Ino {
	t.Helper()
	var ino Ino
	var attr Attr
	ctx := Background()
	if st := m.Create(ctx, parent, name, 0644, 0, syscall.O_RDWR, &ino, &attr); st != 0 {
		t.Fatalf("create %q: %s", name, st)
	}
	if sliceID != 0 {
		if st := m.Write(ctx, ino, 0, 0, Slice{Id: sliceID, Size: length, Len: length}, time.Now()); st != 0 {
			t.Fatalf("write slice to %q: %s", name, st)
		}
	}
	if st := m.Close(ctx, ino); st != 0 {
		t.Fatalf("close %q: %s", name, st)
	}
	return ino
}

func TestOverlayValidation(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	mustMkdir(t, base, RootInode, "tpl")

	if _, err := NewOverlayMeta(ctx, base, "", []string{"/tpl"}, false); err == nil {
		t.Fatal("expected error when subdir is empty in rw mode")
	}
	if _, err := NewOverlayMeta(ctx, base, "/tpl", []string{"/tpl"}, false); err == nil {
		t.Fatal("expected error when subdir equals lower-dir")
	}
	if _, err := NewOverlayMeta(ctx, base, "/tpl/sub", []string{"/tpl"}, false); err == nil {
		t.Fatal("expected error when subdir is inside lower-dir")
	}
	if _, err := NewOverlayMeta(ctx, base, "/sb", []string{"/not-exist"}, false); err == nil {
		t.Fatal("expected error when lower-dir does not exist")
	}
}

func TestOverlayBasicCopyUpWhiteoutOpaque(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	// Prepare lower template: /tpl/src/main.go (slice 1001, 4096B) and /tpl/src/old.txt
	tplIno := mustMkdir(t, base, RootInode, "tpl")
	srcIno := mustMkdir(t, base, tplIno, "src")
	lowerMainIno := mustCreateWithSlice(t, base, srcIno, "main.go", 1001, 4096)
	mustCreateWithSlice(t, base, srcIno, "old.txt", 1002, 128)

	ov, err := NewOverlayMeta(ctx, base, "/sandboxes/sb1", []string{"/tpl"}, false)
	if err != nil {
		t.Fatalf("NewOverlayMeta: %v", err)
	}

	// 1. Lookup & Readdir merge from lower
	var ovSrcVid Ino
	var attr Attr
	if st := ov.Lookup(ctx, RootInode, "src", &ovSrcVid, &attr, false); st != 0 || attr.Typ != TypeDirectory {
		t.Fatalf("lookup src: st=%s typ=%d", st, attr.Typ)
	}
	var entries []*Entry
	if st := ov.Readdir(ctx, ovSrcVid, 1, &entries); st != 0 || len(entries) != 2 {
		t.Fatalf("readdir src: st=%s len=%d", st, len(entries))
	}

	// 2. Open for write triggers zero-data-copy Copy-Up; vid stays stable and slice 1001 is shared
	var ovMainVid Ino
	if st := ov.Lookup(ctx, ovSrcVid, "main.go", &ovMainVid, &attr, false); st != 0 {
		t.Fatalf("lookup main.go: %s", st)
	}
	if st := ov.Open(ctx, ovMainVid, syscall.O_RDWR, &attr); st != 0 {
		t.Fatalf("open main.go rw: %s", st)
	}
	// Append new slice 2001 at offset 4096
	if st := ov.Write(ctx, ovMainVid, 0, 4096, Slice{Id: 2001, Size: 512, Len: 512}, time.Now()); st != 0 {
		t.Fatalf("write main.go: %s", st)
	}
	_ = ov.Close(ctx, ovMainVid)

	// Upper view sees both original slice 1001 and new slice 2001
	var ovSlices []Slice
	if st := ov.Read(ctx, ovMainVid, 0, &ovSlices); st != 0 || len(ovSlices) != 2 || ovSlices[0].Id != 1001 || ovSlices[1].Id != 2001 {
		t.Fatalf("unexpected overlay slices: st=%s slices=%+v", st, ovSlices)
	}
	// Lower template file remains untouched (only slice 1001)
	var lowerSlices []Slice
	if st := base.Read(ctx, lowerMainIno, 0, &lowerSlices); st != 0 || len(lowerSlices) != 1 || lowerSlices[0].Id != 1001 {
		t.Fatalf("lower file was modified! st=%s slices=%+v", st, lowerSlices)
	}

	// 3. Unlink lower file creates whiteout and hides it from Lookup & Readdir
	if st := ov.Unlink(ctx, ovSrcVid, "old.txt"); st != 0 {
		t.Fatalf("unlink old.txt: %s", st)
	}
	if st := ov.Lookup(ctx, ovSrcVid, "old.txt", nil, nil, false); st != syscall.ENOENT {
		t.Fatalf("expected ENOENT after unlink, got %s", st)
	}
	if st := ov.Readdir(ctx, ovSrcVid, 1, &entries); st != 0 || len(entries) != 1 || string(entries[0].Name) != "main.go" {
		t.Fatalf("unexpected entries after unlink: st=%s entries=%+v", st, entries)
	}

	// 4. Rmdir non-empty fails, then delete remaining file -> rmdir -> recreate dir (opaque)
	if st := ov.Rmdir(ctx, RootInode, "src"); st != syscall.ENOTEMPTY {
		t.Fatalf("expected ENOTEMPTY, got %s", st)
	}
	if st := ov.Unlink(ctx, ovSrcVid, "main.go"); st != 0 {
		t.Fatalf("unlink main.go: %s", st)
	}
	if st := ov.Rmdir(ctx, RootInode, "src"); st != 0 {
		t.Fatalf("rmdir src: %s", st)
	}
	if st := ov.Lookup(ctx, RootInode, "src", nil, nil, false); st != syscall.ENOENT {
		t.Fatalf("expected ENOENT after rmdir src, got %s", st)
	}

	// Recreate src: must be empty (opaque) and not expose lower files
	var newSrcVid Ino
	if st := ov.Mkdir(ctx, RootInode, "src", 0755, 0, 0, &newSrcVid, &attr); st != 0 {
		t.Fatalf("recreate src: %s", st)
	}
	if st := ov.Readdir(ctx, newSrcVid, 1, &entries); st != 0 || len(entries) != 0 {
		t.Fatalf("expected empty opaque dir after recreate, got st=%s len=%d", st, len(entries))
	}
}

func TestOverlayMultiLowerAndRename(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	// L0: a.txt (slice 10), b.txt (slice 20), dir1/f.txt
	l0 := mustMkdir(t, base, RootInode, "l0")
	mustCreateWithSlice(t, base, l0, "a.txt", 10, 100)
	mustCreateWithSlice(t, base, l0, "b.txt", 20, 100)
	l0Dir := mustMkdir(t, base, l0, "dir1")
	mustCreateWithSlice(t, base, l0Dir, "f.txt", 30, 100)

	// L1 (higher priority): overrides a.txt (slice 11), whiteouts b.txt
	l1 := mustMkdir(t, base, RootInode, "l1")
	mustCreateWithSlice(t, base, l1, "a.txt", 11, 200)
	mustCreateWithSlice(t, base, l1, whiteoutName("b.txt"), 0, 0)

	ov, err := NewOverlayMeta(ctx, base, "/sb2", []string{"/l1", "/l0"}, false)
	if err != nil {
		t.Fatalf("NewOverlayMeta multi-lower: %v", err)
	}

	// a.txt comes from l1 (slice 11)
	var aVid Ino
	var attr Attr
	if st := ov.Lookup(ctx, RootInode, "a.txt", &aVid, &attr, false); st != 0 {
		t.Fatalf("lookup a.txt: %s", st)
	}
	var slices []Slice
	if st := ov.Read(ctx, aVid, 0, &slices); st != 0 || len(slices) != 1 || slices[0].Id != 11 {
		t.Fatalf("expected a.txt from l1 (slice 11), got st=%s slices=%+v", st, slices)
	}

	// b.txt is whiteouted by l1
	if st := ov.Lookup(ctx, RootInode, "b.txt", nil, nil, false); st != syscall.ENOENT {
		t.Fatalf("expected b.txt ENOENT, got %s", st)
	}

	// Rename lower file a.txt -> a_renamed.txt
	if st := ov.Rename(ctx, RootInode, "a.txt", RootInode, "a_renamed.txt", 0, nil, nil); st != 0 {
		t.Fatalf("rename a.txt: %s", st)
	}
	if st := ov.Lookup(ctx, RootInode, "a.txt", nil, nil, false); st != syscall.ENOENT {
		t.Fatalf("expected old a.txt ENOENT after rename, got %s", st)
	}
	var renamedVid Ino
	if st := ov.Lookup(ctx, RootInode, "a_renamed.txt", &renamedVid, &attr, false); st != 0 {
		t.Fatalf("lookup a_renamed.txt: %s", st)
	}
	if st := ov.Read(ctx, renamedVid, 0, &slices); st != 0 || len(slices) != 1 || slices[0].Id != 11 {
		t.Fatalf("expected renamed file to preserve slice 11, got st=%s slices=%+v", st, slices)
	}

	// Rename lower directory copies merged tree to upper (zero-data-copy), preserves src ino for FUSE, and hides original
	var origDir1Vid Ino
	if st := ov.Lookup(ctx, RootInode, "dir1", &origDir1Vid, &attr, false); st != 0 {
		t.Fatalf("lookup dir1 before rename: %s", st)
	}
	var renamedDirIno Ino
	if st := ov.Rename(ctx, RootInode, "dir1", RootInode, "dir2", 0, &renamedDirIno, nil); st != 0 {
		t.Fatalf("rename lower dir1 -> dir2: %s", st)
	}
	if renamedDirIno != origDir1Vid {
		t.Fatalf("expected renamed dir to preserve ino %d, got %d", origDir1Vid, renamedDirIno)
	}
	if st := ov.Lookup(ctx, RootInode, "dir1", nil, nil, false); st != syscall.ENOENT {
		t.Fatalf("expected dir1 ENOENT after rename, got %s", st)
	}
	// Kernel continues using origDir1Vid after FUSE rename: Readdir/Unlink/Rmdir via it must work
	var dir2Entries []*Entry
	if st := ov.Readdir(ctx, origDir1Vid, 1, &dir2Entries); st != 0 || len(dir2Entries) != 1 || string(dir2Entries[0].Name) != "f.txt" {
		t.Fatalf("readdir via preserved dir ino failed: st=%s entries=%+v", st, dir2Entries)
	}
	var dir2Vid, fVid Ino
	if st := ov.Lookup(ctx, RootInode, "dir2", &dir2Vid, &attr, false); st != 0 || attr.Typ != TypeDirectory || dir2Vid != origDir1Vid {
		t.Fatalf("lookup dir2: st=%s typ=%d vid=%d want=%d", st, attr.Typ, dir2Vid, origDir1Vid)
	}
	if st := ov.Lookup(ctx, dir2Vid, "f.txt", &fVid, &attr, false); st != 0 {
		t.Fatalf("lookup dir2/f.txt: %s", st)
	}
	if st := ov.Read(ctx, fVid, 0, &slices); st != 0 || len(slices) != 1 || slices[0].Id != 30 {
		t.Fatalf("expected dir2/f.txt to preserve slice 30, got st=%s slices=%+v", st, slices)
	}
	// Removing contents and rmdir of renamed dir2 must succeed cleanly (no ENOTEMPTY)
	if st := ov.Unlink(ctx, dir2Vid, "f.txt"); st != 0 {
		t.Fatalf("unlink dir2/f.txt: %s", st)
	}
	if st := ov.Rmdir(ctx, RootInode, "dir2"); st != 0 {
		t.Fatalf("rmdir dir2: %s", st)
	}
	// Original lower dir1/f.txt remains intact
	var origDir1Ino Ino
	var origDir1Attr Attr
	if st := base.Lookup(ctx, l0, "dir1", &origDir1Ino, &origDir1Attr, false); st != 0 {
		t.Fatalf("lower dir1 was unexpectedly modified: %s", st)
	}
}

func TestOverlayRootOwnershipHardlinkAndDirHandler(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	tpl := mustMkdir(t, base, RootInode, "tpl")
	setAttr := Attr{Uid: 1001, Gid: 1002, Mode: 0750}
	if st := base.SetAttr(ctx, tpl, SetAttrUID|SetAttrGID|SetAttrMode, 0, &setAttr); st != 0 {
		t.Fatalf("setattr tpl: %s", st)
	}
	mustCreateWithSlice(t, base, tpl, "obj.dat", 500, 64)

	ov, err := NewOverlayMeta(ctx, base, "/sb-owner", []string{"/tpl"}, false)
	if err != nil {
		t.Fatalf("NewOverlayMeta: %v", err)
	}

	// 1. Root inherits uid/gid/mode from lower template (fixes git dubious ownership)
	var rootAttr Attr
	if st := ov.GetAttr(ctx, RootInode, &rootAttr); st != 0 {
		t.Fatalf("getattr root: %s", st)
	}
	if rootAttr.Uid != 1001 || rootAttr.Gid != 1002 || (rootAttr.Mode&07777) != 0750 {
		t.Fatalf("root attr mismatch: got uid=%d gid=%d mode=%o, want 1001/1002/0750", rootAttr.Uid, rootAttr.Gid, rootAttr.Mode&07777)
	}

	// 2. Hardlink shares the exact same vid across both names
	var origVid, linkVid Ino
	var attr Attr
	if st := ov.Lookup(ctx, RootInode, "obj.dat", &origVid, &attr, false); st != 0 {
		t.Fatalf("lookup obj.dat: %s", st)
	}
	if st := ov.Link(ctx, origVid, RootInode, "obj_link.dat", &attr); st != 0 {
		t.Fatalf("link obj_link.dat: %s", st)
	}
	if st := ov.Lookup(ctx, RootInode, "obj_link.dat", &linkVid, &attr, false); st != 0 {
		t.Fatalf("lookup obj_link.dat: %s", st)
	}
	if linkVid != origVid {
		t.Fatalf("hardlink vid mismatch: orig=%d link=%d", origVid, linkVid)
	}

	// 3. NewDirHandler includes "." and ".." entries
	dh, st := ov.NewDirHandler(ctx, RootInode, true, nil)
	if st != 0 {
		t.Fatalf("NewDirHandler: %s", st)
	}
	defer dh.Close()
	list, st := dh.List(ctx, 0)
	if st != 0 {
		t.Fatalf("DirHandler.List: %s", st)
	}
	if len(list) < 2 || string(list[0].Name) != "." || string(list[1].Name) != ".." {
		t.Fatalf("expected '.' and '..' first in DirHandler, got %+v", list)
	}
}

func TestOverlayBoundedCacheEvictionFallback(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	tpl := mustMkdir(t, base, RootInode, "tpl")
	sub := mustMkdir(t, base, tpl, "deep")
	lowerFileIno := mustCreateWithSlice(t, base, sub, "data.bin", 777, 1024)

	m, err := NewOverlayMeta(ctx, base, "/sb-evict", []string{"/tpl"}, false)
	if err != nil {
		t.Fatalf("NewOverlayMeta: %v", err)
	}
	ov := m.(*overlayMeta)

	// Look up deep/data.bin -> returns real lower inodes
	var deepIno, fileIno Ino
	var attr Attr
	if st := ov.Lookup(ctx, RootInode, "deep", &deepIno, &attr, false); st != 0 {
		t.Fatalf("lookup deep: %s", st)
	}
	if st := ov.Lookup(ctx, deepIno, "data.bin", &fileIno, &attr, false); st != 0 {
		t.Fatalf("lookup data.bin: %s", st)
	}
	if fileIno != lowerFileIno {
		t.Fatalf("expected real lower ino %d, got %d", lowerFileIno, fileIno)
	}

	// Copy-Up & write must succeed via stateless GetPaths resolution, keeping exposed ino stable
	if st := ov.Open(ctx, fileIno, syscall.O_RDWR, &attr); st != 0 {
		t.Fatalf("open after cache eviction: %s", st)
	}
	if st := ov.Write(ctx, fileIno, 0, 1024, Slice{Id: 888, Size: 256, Len: 256}, time.Now()); st != 0 {
		t.Fatalf("write after cache eviction: %s", st)
	}
	_ = ov.Close(ctx, fileIno)

	var slices []Slice
	if st := ov.Read(ctx, fileIno, 0, &slices); st != 0 || len(slices) != 2 || slices[0].Id != 777 || slices[1].Id != 888 {
		t.Fatalf("unexpected slices after eviction copy-up: st=%s slices=%+v", st, slices)
	}
	// Lookup after copy-up still returns the same exposed inode (lowerFileIno)
	var lookedUpIno Ino
	if st := ov.Lookup(ctx, deepIno, "data.bin", &lookedUpIno, &attr, false); st != 0 || lookedUpIno != fileIno {
		t.Fatalf("expected stable ino %d after copy-up, got %d (st=%s)", fileIno, lookedUpIno, st)
	}
}

func TestOverlayAncestorDirInodeStability(t *testing.T) {
	ctx := Background()
	base := newTestMemMeta(t)
	defer base.Shutdown()

	// Template: /tpl/repo/.git/index
	tpl := mustMkdir(t, base, RootInode, "tpl")
	repo := mustMkdir(t, base, tpl, "repo")
	gitDir := mustMkdir(t, base, repo, ".git")
	mustCreateWithSlice(t, base, gitDir, "index", 900, 64)

	ov, err := NewOverlayMeta(ctx, base, "/sb-cwd", []string{"/tpl"}, false)
	if err != nil {
		t.Fatalf("NewOverlayMeta: %v", err)
	}

	// Simulate: cd repo -> cwd holds repoIno
	var repoIno, ovGitIno Ino
	var attr Attr
	if st := ov.Lookup(ctx, RootInode, "repo", &repoIno, &attr, false); st != 0 {
		t.Fatalf("lookup repo: %s", st)
	}
	if st := ov.Lookup(ctx, repoIno, ".git", &ovGitIno, &attr, false); st != 0 {
		t.Fatalf("lookup .git: %s", st)
	}

	// Trigger write inside repo/.git -> materializes intermediate ancestor "repo" in upper
	var lockIno Ino
	if st := ov.Create(ctx, ovGitIno, "index.lock", 0644, 0, syscall.O_RDWR|syscall.O_CREAT, &lockIno, &attr); st != 0 {
		t.Fatalf("create index.lock: %s", st)
	}
	_ = ov.Close(ctx, lockIno)

	// Simulate getcwd(): parent Readdir and Lookup for "repo" MUST still match original repoIno
	var rootEntries []*Entry
	if st := ov.Readdir(ctx, RootInode, 1, &rootEntries); st != 0 {
		t.Fatalf("readdir root: %s", st)
	}
	found := false
	for _, e := range rootEntries {
		if string(e.Name) == "repo" {
			found = true
			if e.Inode != repoIno {
				t.Fatalf("ancestor dir 'repo' inode drifted in Readdir: got %d, want %d", e.Inode, repoIno)
			}
		}
	}
	if !found {
		t.Fatal("repo not found in root Readdir")
	}

	var repoAfter Ino
	if st := ov.Lookup(ctx, RootInode, "repo", &repoAfter, &attr, false); st != 0 || repoAfter != repoIno {
		t.Fatalf("ancestor dir 'repo' inode drifted in Lookup: got %d, want %d (st=%s)", repoAfter, repoIno, st)
	}
}
