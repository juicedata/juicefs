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

package vfs

import (
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
)

type smodeCase struct {
	mode uint16
	str  string
}

var cases = []smodeCase{
	{syscall.S_IFDIR | 00755, "drwxr-xr-x"},
	{syscall.S_IFREG | 01644, "-rw-r--r-T"},
	{syscall.S_IFLNK | 03755, "lrwxr-sr-t"},
	{syscall.S_IFSOCK | 06700, "srws--S---"},
}

func TestSmode(t *testing.T) {
	for _, s := range cases {
		res := smode(s.mode).String()
		if res != s.str {
			t.Fatalf("str of %o: %s != %s", s.mode, res, s.str)
		}
	}
}

func TestEntryString(t *testing.T) {
	var e *Entry
	if e.String() != "" {
		t.Fatalf("empty entry should be ''")
	}
	e = &Entry{Inode: 2, Name: []byte("test")}
	if e.String() != " (2)" {
		t.Fatalf("empty entry should be ` (2)`")
	}

	e.Attr = &meta.Attr{
		Typ:    meta.TypeFile,
		Mode:   01755,
		Nlink:  1,
		Uid:    2,
		Gid:    3,
		Atime:  4,
		Mtime:  5,
		Ctime:  6,
		Length: 7,
	}
	if e.String() != " (2,[-rwxr-xr-t:0101755,1,2,3,4,5,6,7])" {
		t.Fatalf("string of entry is not expected: %s", e.String())
	}
}

func TestError(t *testing.T) {
	if strerr(0) != "OK" {
		t.Fatalf("expect 'OK' but got %q", strerr(0))
	}
	if strerr(syscall.EACCES) != "permission denied" {
		t.Fatalf("expect 'Access denied', but got %q", strerr(syscall.EACCES))
	}
}
