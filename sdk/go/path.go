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
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Paths are passed to the backend unchanged; only the semantics that os adds
// on top of the system calls are implemented here (design 6.2, 6.5).

func (c *Client) check() error {
	if c.closed.Load() {
		return fs.ErrClosed
	}
	return nil
}

func isDir(st *Stat) bool {
	return st.Mode&syscall.S_IFMT == syscall.S_IFDIR
}

// Stat is os.Stat; the Sys method of the result returns a *Stat.
func (c *Client) Stat(name string) (fs.FileInfo, error) {
	return c.stat("stat", name, true)
}

func (c *Client) Lstat(name string) (fs.FileInfo, error) {
	return c.stat("lstat", name, false)
}

func (c *Client) stat(op, name string, follow bool) (fs.FileInfo, error) {
	if err := c.check(); err != nil {
		return nil, pathError(op, name, err)
	}
	st, err := c.backend.stat(name, follow)
	if err != nil {
		return nil, pathError(op, name, err)
	}
	return newFileInfo(name, st), nil
}

// Mkdir applies the umask read by New.
func (c *Client) Mkdir(name string, perm fs.FileMode) error {
	err := c.check()
	if err == nil {
		err = c.backend.mkdir(name, syscallMode(perm), c.umask)
	}
	return pathError("mkdir", name, err)
}

func (c *Client) MkdirAll(name string, perm fs.FileMode) error {
	if err := c.check(); err != nil {
		return pathError("mkdir", name, err)
	}
	if st, err := c.backend.stat(name, true); err == nil {
		if isDir(st) {
			return nil
		}
		return pathError("mkdir", name, syscall.ENOTDIR)
	}
	err := c.backend.mkdirAll(name, syscallMode(perm), c.umask)
	if err != nil {
		if st, err1 := c.backend.stat(name, false); err1 == nil && isDir(st) {
			return nil
		}
	}
	return pathError("mkdir", name, err)
}

func (c *Client) Remove(name string) error {
	err := c.check()
	if err == nil {
		err = c.backend.remove(name)
	}
	return pathError("remove", name, err)
}

// Rmdir is Remove for directories only: it returns ENOTDIR for other files.
func (c *Client) Rmdir(name string) error {
	err := c.check()
	if err == nil {
		err = c.backend.rmdir(name)
	}
	return pathError("rmdir", name, err)
}

// RemoveAll is os.RemoveAll; files go to the trash if it is enabled.
func (c *Client) RemoveAll(name string) error {
	if name == "" {
		return nil
	}
	err := c.check()
	if err == nil {
		if endsWithDot(name) {
			err = syscall.EINVAL
		} else {
			err = c.backend.removeAll(name)
		}
	}
	if err == syscall.ENOENT {
		return nil
	}
	return pathError("RemoveAll", name, err)
}

func endsWithDot(name string) bool {
	return name == "." || strings.HasSuffix(name, "/.")
}

// Rename is os.Rename: an existing directory newname gives EEXIST.
func (c *Client) Rename(oldname, newname string) error {
	return linkError("rename", oldname, newname, c.rename(oldname, newname))
}

func (c *Client) rename(oldname, newname string) error {
	if err := c.check(); err != nil {
		return err
	}
	// Same check as os.Rename.
	if nst, err := c.backend.stat(newname, false); err == nil && isDir(nst) {
		ost, err := c.backend.stat(oldname, false)
		if err != nil {
			return err
		}
		if oldname == newname || ost.Inode != nst.Inode {
			return syscall.EEXIST
		}
	}
	return c.backend.rename(oldname, newname)
}

// Link does not follow a symbolic link oldname, unlike os.Link on macOS.
func (c *Client) Link(oldname, newname string) error {
	err := c.check()
	if err == nil {
		err = c.backend.link(oldname, newname)
	}
	return linkError("link", oldname, newname, err)
}

// Symlink stores an absolute oldname relative to the directory of newname, as
// the Python SDK does, so that the link works wherever the volume is mounted.
func (c *Client) Symlink(oldname, newname string) error {
	err := c.check()
	if err == nil {
		target := oldname
		if path.IsAbs(oldname) {
			// Both are absolute, so filepath.Rel cannot fail.
			dir := path.Dir(path.Clean("/" + newname))
			if rel, err := filepath.Rel(dir, oldname); err == nil {
				target = rel
			}
		}
		err = c.backend.symlink(target, newname)
	}
	return linkError("symlink", oldname, newname, err)
}

func (c *Client) Readlink(name string) (string, error) {
	err := c.check()
	if err == nil {
		var target string
		if target, err = c.backend.readlink(name); err == nil {
			return target, nil
		}
	}
	return "", pathError("readlink", name, err)
}

func (c *Client) Truncate(name string, size int64) error {
	err := c.check()
	if err == nil {
		if size < 0 {
			err = syscall.EINVAL
		} else {
			err = c.backend.truncate(name, uint64(size))
		}
	}
	return pathError("truncate", name, err)
}

func (c *Client) Chmod(name string, mode fs.FileMode) error {
	err := c.check()
	if err == nil {
		err = c.backend.chmod(name, syscallMode(mode))
	}
	return pathError("chmod", name, err)
}

func (c *Client) Chown(name string, uid, gid int) error {
	return pathError("chown", name, c.chown(name, uid, gid, true))
}

func (c *Client) Lchown(name string, uid, gid int) error {
	return pathError("lchown", name, c.chown(name, uid, gid, false))
}

func (c *Client) chown(name string, uid, gid int, follow bool) error {
	if err := c.check(); err != nil {
		return err
	}
	if uid == -1 || gid == -1 {
		st, err := c.backend.stat(name, follow)
		if err != nil {
			return err
		}
		if uid == -1 {
			uid = int(st.Uid)
		}
		if gid == -1 {
			gid = int(st.Gid)
		}
	}
	return c.backend.chown(name, uint32(uid), uint32(gid), follow)
}

func (c *Client) Chtimes(name string, atime, mtime time.Time) error {
	err := c.check()
	if err == nil {
		err = c.backend.utime(name, atime, mtime)
	}
	return pathError("chtimes", name, err)
}

// ReadDir is os.ReadDir; the Info of an entry is read with the directory.
func (c *Client) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := c.check(); err != nil {
		return nil, pathError("readdir", name, err)
	}
	infos, err := c.backend.readdir(name)
	if err != nil {
		return nil, pathError("readdir", name, err)
	}
	slices.SortFunc(infos, func(a, b *fileInfo) int { return strings.Compare(a.name, b.name) })
	entries := make([]fs.DirEntry, len(infos))
	for i, fi := range infos {
		entries[i] = fs.FileInfoToDirEntry(fi)
	}
	return entries, nil
}
