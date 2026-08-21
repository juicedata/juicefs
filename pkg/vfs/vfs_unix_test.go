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
	testImmutableFl = 0x00000010
	testAppendFl    = 0x00000020
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

// Changing the immutable or append-only flag requires root, in either direction.
// chattr sends the full resulting flag mask, so clearing a protected flag arrives
// as a missing bit rather than a set one; guarding only the incoming bits let a
// non-root owner clear the flag and then modify the file. This mirrors the kernel's
// fileattr_set_prepare(), which gates on the delta between old and new flags.
func TestIoctlSetFlagsPermission(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	rootCtx := NewLogContext(meta.NewContext(1, 0, []uint32{0}))
	userCtx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))

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
}
