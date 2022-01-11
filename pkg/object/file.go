/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dirSuffix = "/"
)

type filestore struct {
	DefaultObjectStorage
	root string
}

func (d *filestore) String() string {
	if runtime.GOOS == "windows" {
		return "file:///" + d.root
	}
	return "file://" + d.root
}

func (d *filestore) path(key string) string {
	if strings.HasSuffix(d.root, dirSuffix) {
		return filepath.Join(d.root, key)
	}
	return d.root + key
}

func (d *filestore) Head(key string) (Object, error) {
	p := d.path(key)

	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if fi.IsDir() {
		size = 0
	}
	return &obj{
		key,
		size,
		fi.ModTime(),
		fi.IsDir(),
	}, nil
}

func (d *filestore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	p := d.path(key)

	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}

	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if finfo.IsDir() {
		_ = f.Close()
		return ioutil.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	if off > 0 {
		if _, err := f.Seek(off, 0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if limit > 0 {
		defer f.Close()
		buf := make([]byte, limit)
		if n, err := f.Read(buf); err != nil {
			return nil, err
		} else {
			return ioutil.NopCloser(bytes.NewBuffer(buf[:n])), nil
		}
	}
	return f, nil
}

func (d *filestore) Put(key string, in io.Reader) error {
	p := d.path(key)

	if strings.HasSuffix(key, dirSuffix) || key == "" && strings.HasSuffix(d.root, dirSuffix) {
		return os.MkdirAll(p, os.FileMode(0755))
	}

	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp"+strconv.Itoa(rand.Int()))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(p), os.FileMode(0755)); err != nil {
			return err
		}
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	}
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(f, in, *buf)
	if err != nil {
		_ = f.Close()
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}
	err = os.Rename(tmp, p)
	return err
}

func (d *filestore) Copy(dst, src string) error {
	r, err := d.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	return d.Put(dst, r)
}

func (d *filestore) Delete(key string) error {
	err := os.Remove(d.path(key))
	if err != nil && os.IsNotExist(err) {
		err = nil
	}
	return err
}

// walk recursively descends path, calling w.
func walk(path string, info os.FileInfo, walkFn filepath.WalkFunc) error {
	err := walkFn(path, info, nil)
	if err != nil {
		if info.IsDir() && err == filepath.SkipDir {
			return nil
		}
		return err
	}

	if !info.IsDir() {
		return nil
	}

	entries, err := readDirSorted(path)
	if err != nil {
		return walkFn(path, info, err)
	}

	for _, e := range entries {
		p := filepath.Join(path, e.Name())
		if e.IsDir() {
			p = filepath.ToSlash(p + "/")
		}
		in, err := e.Info()
		if err == nil {
			err = walk(p, in, walkFn)
		}
		if err != nil && err != filepath.SkipDir && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Walk walks the file tree rooted at root, calling walkFn for each file or
// directory in the tree, including root. All errors that arise visiting files
// and directories are filtered by walkFn. The files are walked in lexical
// order, which makes the output deterministic but means that for very
// large directories Walk can be inefficient.
// Walk always follow symbolic links.
func Walk(root string, walkFn filepath.WalkFunc) error {
	info, err := os.Stat(root)
	if err != nil {
		err = walkFn(root, nil, err)
	} else {
		err = walk(root, info, walkFn)
	}
	if err == filepath.SkipDir {
		return nil
	}
	return err
}

type mEntry struct {
	os.DirEntry
	name string
	fi   os.FileInfo
}

func (m *mEntry) Name() string {
	return m.name
}

func (m *mEntry) Info() (os.FileInfo, error) {
	if m.fi != nil {
		return m.fi, nil
	}
	return m.DirEntry.Info()
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func readDirSorted(dirname string) ([]os.DirEntry, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	for i, e := range entries {
		if e.IsDir() {
			entries[i] = &mEntry{e, e.Name() + dirSuffix, nil}
		} else if !e.Type().IsRegular() {
			// follow symlink
			fi, err := os.Stat(filepath.Join(dirname, e.Name()))
			if err != nil {
				logger.Warnf("skip broken symlink %s: %s", filepath.Join(dirname, e.Name()), err)
				continue
			}
			name := e.Name()
			if fi.IsDir() {
				name = e.Name() + dirSuffix
			}
			entries[i] = &mEntry{e, name, fi}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}

func (d *filestore) List(prefix, marker string, limit int64) ([]Object, error) {
	return nil, notSupported
}

func (d *filestore) ListAll(prefix, marker string) (<-chan Object, error) {
	listed := make(chan Object, 10240)
	go func() {
		var walkRoot string
		if strings.HasSuffix(d.root, dirSuffix) {
			walkRoot = d.root
		} else {
			// If the root is not ends with `/`, we'll list the directory root resides.
			walkRoot = path.Dir(d.root)
		}

		_ = Walk(walkRoot, func(path string, info os.FileInfo, err error) error {
			if runtime.GOOS == "windows" {
				path = strings.Replace(path, "\\", "/", -1)
			}

			if err != nil {
				// skip broken symbolic link
				if fi, err1 := os.Lstat(path); err1 == nil && fi.Mode()&os.ModeSymlink != 0 {
					logger.Warnf("skip unreachable symlink: %s (%s)", path, err)
					return nil
				}
				if os.IsNotExist(err) {
					logger.Warnf("skip not exist file or directory: %s", path)
					return nil
				}
				listed <- nil
				logger.Errorf("list %s: %s", path, err)
				return err
			}

			if !strings.HasPrefix(path, d.root) {
				if info.IsDir() && path != walkRoot {
					return filepath.SkipDir
				}
				return nil
			}

			key := path[len(d.root):]
			if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
				if info.IsDir() && !strings.HasPrefix(prefix, key) && !strings.HasPrefix(marker, key) {
					return filepath.SkipDir
				}
				return nil
			}
			owner, group := getOwnerGroup(info)
			f := &file{
				obj{
					key,
					info.Size(),
					info.ModTime(),
					info.IsDir(),
				},
				owner,
				group,
				info.Mode(),
			}
			if info.IsDir() {
				f.size = 0
			}
			listed <- f
			return nil
		})
		close(listed)
	}()
	return listed, nil
}

func (d *filestore) Chtimes(path string, mtime time.Time) error {
	p := d.path(path)
	return os.Chtimes(p, mtime, mtime)
}

func (d *filestore) Chmod(path string, mode os.FileMode) error {
	p := d.path(path)
	return os.Chmod(p, mode)
}

func (d *filestore) Chown(path string, owner, group string) error {
	p := d.path(path)
	uid := lookupUser(owner)
	gid := lookupGroup(group)
	return os.Chown(p, uid, gid)
}

func newDisk(root, accesskey, secretkey string) (ObjectStorage, error) {
	// For Windows, the path looks like /C:/a/b/c/
	if runtime.GOOS == "windows" && strings.HasPrefix(root, "/") {
		root = root[1:]
	}
	if strings.HasSuffix(root, dirSuffix) {
		logger.Debugf("Ensure directory %s", root)
		if err := os.MkdirAll(root, 0755); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", root, err)
		}
	} else {
		dir := path.Dir(root)
		logger.Debugf("Ensure directory %s", dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", dir, err)
		}
	}
	return &filestore{root: root}, nil
}

func init() {
	Register("file", newDisk)
}
