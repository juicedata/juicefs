//go:build !windows
// +build !windows

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

package vfs

import (
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	testIocSetFlags = 0x40086602
	testIocGetFlags = 0x80086601
	testSecrmFl     = 0x00000001
	testImmutableFl = 0x00000010
	testAppendFl    = 0x00000020
	testNodumpFl    = 0x00000040
)

func testSetFlags(v *VFS, ctx Context, ino Ino, iflag uint64) syscall.Errno {
	bufIn := make([]byte, 8)
	utils.NativeEndian.PutUint64(bufIn, iflag)
	return v.Ioctl(ctx, ino, testIocSetFlags, 0, bufIn, nil)
}

func testGetFlags(t *testing.T, v *VFS, ctx Context, ino Ino) uint64 {
	t.Helper()
	bufOut := make([]byte, 8)
	if e := v.Ioctl(ctx, ino, testIocGetFlags, 0, nil, bufOut); e != 0 {
		t.Fatalf("get flags: %s", e)
	}
	return utils.NativeEndian.Uint64(bufOut)
}

func testMetaFlags(t *testing.T, v *VFS, ino Ino) uint8 {
	t.Helper()
	var attr Attr
	if e := v.Meta.GetAttr(meta.Background(), ino, &attr); e != 0 {
		t.Fatalf("get attr: %s", e)
	}
	return attr.Flags
}

// FS_IOC_SETFLAGS carries the whole resulting flag mask rather than a delta, and
// on kernels older than 5.13 -- or whenever FS_IOC_SETFLAGS_32 is used -- it is
// handed to the filesystem without the kernel checking it at all. So the rules
// vfs_fileattr_set() applies have to be reproduced here:
//
//	immutable/append : only root may change them, in either direction
//	skip-trash       : the owner may change it, like the kernel does for FS_SECRM_FL
//	any flag         : the caller must own the file
//
// Flags that this ioctl does not map, such as the Windows attributes, must be
// left alone.
func TestIoctlSetFlagsPermission(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	rootCtx := NewLogContext(meta.NewContext(1, 0, []uint32{0}))
	userCtx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))
	otherCtx := NewLogContext(meta.NewContext(11, 4, []uint32{5}))

	// owned by the unprivileged user, so only the flag rules can protect it
	newFile := func(name string) Ino {
		t.Helper()
		fe, e := v.Mknod(rootCtx, 1, name, 0666|syscall.S_IFREG, 0, 0)
		if e != 0 {
			t.Fatalf("mknod %s: %s", name, e)
		}
		if _, e := v.SetAttr(rootCtx, fe.Inode, meta.SetAttrUID|meta.SetAttrGID, 0, 0, 1, 2, 0, 0, 0, 0, 0); e != 0 {
			t.Fatalf("chown %s: %s", name, e)
		}
		return fe.Inode
	}

	t.Run("privileged flags need root", func(t *testing.T) {
		for _, c := range []struct {
			name string
			flag uint64
		}{
			{"immutable", testImmutableFl},
			{"append", testAppendFl},
		} {
			ino := newFile(c.name)

			// a non-root user cannot raise a protected flag
			if e := testSetFlags(v, userCtx, ino, c.flag); e != syscall.EPERM {
				t.Fatalf("non-root setting %s should be EPERM, got %s", c.name, e)
			}
			if e := testSetFlags(v, rootCtx, ino, c.flag); e != 0 {
				t.Fatalf("root should be able to set %s: %s", c.name, e)
			}
			if flags := testGetFlags(t, v, rootCtx, ino); flags&c.flag == 0 {
				t.Fatalf("%s flag was not set: %x", c.name, flags)
			}

			// the regression: clearing sends a mask without the protected bit
			if e := testSetFlags(v, userCtx, ino, 0); e != syscall.EPERM {
				t.Fatalf("non-root clearing %s should be EPERM, got %s", c.name, e)
			}
			if flags := testGetFlags(t, v, rootCtx, ino); flags&c.flag == 0 {
				t.Fatalf("%s flag was cleared by a non-root user: %x", c.name, flags)
			}

			// a write that does not change any protected flag is not privileged,
			// matching fileattr_set_prepare()
			if e := testSetFlags(v, userCtx, ino, c.flag); e != 0 {
				t.Fatalf("non-root no-op write of %s should be allowed, got %s", c.name, e)
			}

			if e := testSetFlags(v, rootCtx, ino, 0); e != 0 {
				t.Fatalf("root should be able to clear %s: %s", c.name, e)
			}
			if flags := testGetFlags(t, v, rootCtx, ino); flags&c.flag != 0 {
				t.Fatalf("%s flag should be cleared: %x", c.name, flags)
			}
			if e := testSetFlags(v, userCtx, ino, 0); e != 0 {
				t.Fatalf("non-root no-op write on unflagged file should be allowed, got %s", e)
			}
		}
	})

	// the kernel only gates APPEND and IMMUTABLE on CAP_LINUX_IMMUTABLE, so an
	// owner is free to toggle skip-trash on their own files
	t.Run("skip-trash needs no privilege", func(t *testing.T) {
		ino := newFile("skip-trash")

		if e := testSetFlags(v, userCtx, ino, testSecrmFl); e != 0 {
			t.Fatalf("owner should be able to set skip-trash: %s", e)
		}
		if flags := testGetFlags(t, v, userCtx, ino); flags&testSecrmFl == 0 {
			t.Fatalf("skip-trash flag was not set: %x", flags)
		}
		if e := testSetFlags(v, userCtx, ino, 0); e != 0 {
			t.Fatalf("owner should be able to clear skip-trash: %s", e)
		}
		if flags := testGetFlags(t, v, userCtx, ino); flags&testSecrmFl != 0 {
			t.Fatalf("skip-trash flag should be cleared: %x", flags)
		}
	})

	// inode_owner_or_capable(): a non-owner may not touch the flags at all, not
	// even the ones that need no privilege, and not even when nothing changes
	t.Run("non-owner is rejected", func(t *testing.T) {
		ino := newFile("non-owner")

		if e := testSetFlags(v, otherCtx, ino, 0); e != syscall.EPERM {
			t.Fatalf("non-owner no-op write should be EPERM, got %s", e)
		}
		if e := testSetFlags(v, otherCtx, ino, testSecrmFl); e != syscall.EPERM {
			t.Fatalf("non-owner setting skip-trash should be EPERM, got %s", e)
		}
		if e := testSetFlags(v, otherCtx, ino, testImmutableFl); e != syscall.EPERM {
			t.Fatalf("non-owner setting immutable should be EPERM, got %s", e)
		}
		if flags := testGetFlags(t, v, rootCtx, ino); flags != 0 {
			t.Fatalf("flags should be untouched: %x", flags)
		}
	})

	// FS_IOC_SETFLAGS does not carry the Windows attributes, so a client that
	// cannot see them must not drop them
	t.Run("keeps unmapped flags", func(t *testing.T) {
		ino := newFile("unmapped")
		hidden := &Attr{Flags: meta.FlagWindowsHidden | meta.FlagWindowsArchive}
		if e := v.Meta.SetAttr(rootCtx, ino, meta.SetAttrFlag, 0, hidden); e != 0 {
			t.Fatalf("set windows attributes: %s", e)
		}

		if e := testSetFlags(v, rootCtx, ino, testImmutableFl); e != 0 {
			t.Fatalf("root should be able to set immutable: %s", e)
		}
		if flags := testMetaFlags(t, v, ino); flags != meta.FlagWindowsHidden|meta.FlagWindowsArchive|meta.FlagImmutable {
			t.Fatalf("windows attributes were dropped: %x", flags)
		}

		if e := testSetFlags(v, rootCtx, ino, 0); e != 0 {
			t.Fatalf("root should be able to clear immutable: %s", e)
		}
		if flags := testMetaFlags(t, v, ino); flags != meta.FlagWindowsHidden|meta.FlagWindowsArchive {
			t.Fatalf("windows attributes were dropped: %x", flags)
		}
	})

	t.Run("unsupported flags are rejected", func(t *testing.T) {
		ino := newFile("unsupported")
		if e := testSetFlags(v, rootCtx, ino, testNodumpFl); e != syscall.ENOTSUP {
			t.Fatalf("unsupported flag should be ENOTSUP, got %s", e)
		}
	})
}
