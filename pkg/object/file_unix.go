//go:build !windows
// +build !windows

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

package object

import (
	"os"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/sftp"
	"golang.org/x/sys/unix"
)

func getOwnerGroup(info os.FileInfo) (string, string) {
	var owner, group string
	switch st := info.Sys().(type) {
	case *syscall.Stat_t:
		owner = utils.UserName(int(st.Uid))
		group = utils.GroupName(int(st.Gid))
	case *sftp.FileStat:
		owner = utils.UserName(int(st.UID))
		group = utils.GroupName(int(st.GID))
	}
	return owner, group
}

func (d *filestore) Chtimes(key string, mtime time.Time) error {
	p := d.path(key)
	return lchtimes(p, mtime, mtime)
}

func lchtimes(name string, atime time.Time, mtime time.Time) error {
	var utimes [2]unix.Timeval
	set := func(i int, t time.Time) {
		if !t.IsZero() {
			utimes[i] = unix.NsecToTimeval(t.UnixNano())
		}
	}
	set(0, atime)
	set(1, mtime)
	if e := unix.Lutimes(name, utimes[0:]); e != nil {
		return &os.PathError{Op: "lchtimes", Path: name, Err: e}
	}
	return nil
}
