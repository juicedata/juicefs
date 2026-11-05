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

package juicefs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestPathError(t *testing.T) {
	if err := pathError("stat", "/a", syscall.Errno(0)); err != nil {
		t.Fatalf("Errno(0): got %v", err)
	}
	if err := pathError("stat", "/a", nil); err != nil {
		t.Fatalf("nil: got %v", err)
	}
	if err := pathError("read", "/a", io.EOF); err != io.EOF {
		t.Fatalf("io.EOF must not be wrapped: got %v", err)
	}

	err := pathError("stat", "/a", syscall.ENOENT)
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Op != "stat" || pe.Path != "/a" || pe.Err != syscall.ENOENT {
		t.Fatalf("got %#v", err)
	}
	if !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("errors.Is failed for %v", err)
	}
	if err.Error() != "stat /a: "+syscall.ENOENT.Error() {
		t.Fatalf("message: %q", err.Error())
	}
}

func TestLinkError(t *testing.T) {
	if err := linkError("rename", "/a", "/b", syscall.Errno(0)); err != nil {
		t.Fatalf("Errno(0): got %v", err)
	}
	err := linkError("rename", "/a", "/b", syscall.EEXIST)
	var le *os.LinkError
	if !errors.As(err, &le) || le.Op != "rename" || le.Old != "/a" || le.New != "/b" {
		t.Fatalf("got %#v", err)
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("errors.Is(%v, fs.ErrExist) = false", err)
	}
}

func TestSyscallError(t *testing.T) {
	if err := syscallError("status", syscall.Errno(0)); err != nil {
		t.Fatalf("Errno(0): got %v", err)
	}
	err := syscallError("status", syscall.EIO)
	var se *os.SyscallError
	if !errors.As(err, &se) || se.Syscall != "status" || se.Err != syscall.EIO {
		t.Fatalf("got %#v", err)
	}
	if !errors.Is(err, syscall.EIO) || err.Error() != "status: "+syscall.EIO.Error() {
		t.Fatalf("got %v", err)
	}
}
