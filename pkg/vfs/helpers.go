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
	"fmt"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

const (
	MODE_MASK_R = 4
	MODE_MASK_W = 2
	MODE_MASK_X = 1
)

func strerr(errno syscall.Errno) string {
	if errno == 0 {
		return "OK"
	}
	return errno.Error()
}

var typestr = map[uint16]byte{
	syscall.S_IFSOCK: 's',
	syscall.S_IFLNK:  'l',
	syscall.S_IFREG:  '-',
	syscall.S_IFBLK:  'b',
	syscall.S_IFDIR:  'd',
	syscall.S_IFCHR:  'c',
	syscall.S_IFIFO:  'f',
	0:                '?',
}

type smode uint16

func (mode smode) String() string {
	s := []byte("?rwxrwxrwx")
	s[0] = typestr[uint16(mode)&(syscall.S_IFMT&0xffff)]
	if (mode & syscall.S_ISUID) != 0 {
		s[3] = 's'
	}
	if (mode & syscall.S_ISGID) != 0 {
		s[6] = 's'
	}
	if (mode & syscall.S_ISVTX) != 0 {
		s[9] = 't'
	}
	for i := uint16(0); i < 9; i++ {
		if (mode & (1 << i)) == 0 {
			if s[9-i] == 's' || s[9-i] == 't' {
				s[9-i] &= 0xDF
			} else {
				s[9-i] = '-'
			}
		}
	}
	return string(s)
}

// Entry is an alias of meta.Entry, which is used to generate the string
// representation lazily.
type Entry meta.Entry

func (entry *Entry) String() string {
	if entry == nil {
		return ""
	}
	if entry.Attr == nil {
		return fmt.Sprintf(" (%d)", entry.Inode)
	}
	a := entry.Attr
	mode := a.SMode()
	return fmt.Sprintf(" (%d,[%s:0%06o,%d,%d,%d,%d,%d,%d,%d])",
		entry.Inode, smode(mode), mode, a.Nlink, a.Uid, a.Gid,
		a.Atime, a.Mtime, a.Ctime, a.Length)
}

// LogContext is an interface to add duration on meta.Context.
type LogContext interface {
	meta.Context
	Duration() time.Duration
}

type logContext struct {
	meta.Context
	start time.Time
}

func (ctx *logContext) Duration() time.Duration {
	return time.Since(ctx.start)
}

// NewLogContext creates an LogContext starting from now.
func NewLogContext(ctx meta.Context) LogContext {
	return &logContext{ctx, time.Now()}
}
