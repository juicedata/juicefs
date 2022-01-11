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
