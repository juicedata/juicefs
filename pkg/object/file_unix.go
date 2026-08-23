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

func openFileAt(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func chmodAt(parent *os.File, name string, mode os.FileMode) error {
	if err := unix.Fchmodat(int(parent.Fd()), name, uint32(mode.Perm()), 0); err != nil {
		return &os.PathError{Op: "chmodat", Path: name, Err: err}
	}
	return nil
}

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
	root, name, err := d.rootedPath(key, false)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, base, err := openRootParent(root, name)
	if err != nil {
		return err
	}
	defer parent.Close()
	return lchtimesat(int(parent.Fd()), base, time.Time{}, mtime)
}
