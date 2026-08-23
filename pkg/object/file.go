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
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	dirSuffix = "/"
)

var TryCFR bool // try copy_file_range
var PutInplace bool

type filestore struct {
	DefaultObjectStorage
	root string
}

func (d *filestore) Symlink(oldName, newName string) error {
	root, name, err := d.rootedPath(newName, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(name), os.FileMode(0777)); err != nil {
		return err
	}
	return root.Symlink(oldName, name)
}

func (d *filestore) Readlink(name string) (string, error) {
	root, name, err := d.rootedPath(name, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return root.Readlink(name)
}

func (d *filestore) String() string {
	if runtime.GOOS == "windows" {
		return "file:///" + d.root
	}
	return "file://" + d.root
}

func (d *filestore) path(key string) (string, error) {
	var p string
	if strings.HasSuffix(d.root, dirSuffix) {
		p = filepath.Join(d.root, key)
	} else {
		p = filepath.Clean(d.root + key)
	}

	boundary := d.root
	if !strings.HasSuffix(boundary, dirSuffix) {
		boundary = filepath.Dir(boundary)
	}

	rel, err := filepath.Rel(boundary, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key %q escapes storage root %q", key, d.root)
	}

	return p, nil
}

func (d *filestore) rootedPath(key string, create bool) (*os.Root, string, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, "", err
	}
	boundary := d.root
	if !strings.HasSuffix(boundary, dirSuffix) {
		boundary = filepath.Dir(boundary)
	}
	if create {
		if err := os.MkdirAll(boundary, os.FileMode(0777)); err != nil {
			return nil, "", err
		}
	}
	root, err := os.OpenRoot(boundary)
	if err != nil {
		return nil, "", err
	}
	name, err := filepath.Rel(boundary, p)
	if err != nil {
		_ = root.Close()
		return nil, "", err
	}
	return root, name, nil
}

func openRootFileFollowingFinal(root *os.Root, name string) (*os.File, error) {
	parent, base, err := openRootParent(root, name)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openFileAt(parent, base)
}

func openRootParent(root *os.Root, name string) (*os.File, string, error) {
	parent, err := root.Open(filepath.Dir(name))
	if err != nil {
		return nil, "", err
	}
	fi, err := parent.Stat()
	if err != nil {
		_ = parent.Close()
		return nil, "", err
	}
	if !fi.IsDir() {
		_ = parent.Close()
		return nil, "", &os.PathError{Op: "open", Path: filepath.Dir(name), Err: syscall.ENOTDIR}
	}
	return parent, filepath.Base(name), nil
}

func (d *filestore) Head(ctx context.Context, key string) (Object, error) {
	root, name, err := d.rootedPath(key, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	isSymlink := fi.Mode()&os.ModeSymlink != 0
	if isSymlink {
		f, err := openRootFileFollowingFinal(root, name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		fi, err = f.Stat()
		if err != nil {
			return nil, err
		}
	}
	return toFile(key, fi, isSymlink, getOwnerGroup), nil
}

func toFile(key string, fi fs.FileInfo, isSymlink bool, ownerGetter func(fs.FileInfo) (string, string)) *file {
	size := fi.Size()
	if fi.IsDir() {
		size = 0
	}
	owner, group := ownerGetter(fi)
	return &file{
		obj{
			key,
			size,
			fi.ModTime(),
			fi.IsDir(),
			"",
			"",
		},
		owner,
		group,
		fi.Mode(),
		isSymlink,
	}
}

type SectionReaderCloser struct {
	*io.SectionReader
	io.Closer
}

func (d *filestore) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	root, name, err := d.rootedPath(key, false)
	if err != nil {
		return nil, err
	}
	f, err := openRootFileFollowingFinal(root, name)
	_ = root.Close()
	if err != nil {
		return nil, err
	}

	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if finfo.IsDir() || off >= finfo.Size() {
		_ = f.Close()
		return io.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	if limit > 0 {
		return &SectionReaderCloser{
			SectionReader: io.NewSectionReader(f, off, limit),
			Closer:        f,
		}, nil
	}
	return f, nil
}

func (d *filestore) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) (err error) {
	root, name, err := d.rootedPath(key, true)
	if err != nil {
		return err
	}
	defer root.Close()

	if strings.HasSuffix(key, dirSuffix) || key == "" && strings.HasSuffix(d.root, dirSuffix) {
		return root.MkdirAll(name, os.FileMode(0777))
	}

	var tmp string
	if PutInplace {
		tmp = name
	} else {
		base := filepath.Base(name)
		if len(base) > 200 {
			base = base[:200]
		}
		tmp = TmpFilePath(name, base)
		defer func() {
			if err != nil {
				if e := root.Remove(tmp); e != nil && !os.IsNotExist(e) {
					logger.Warnf("delete %s: %s", tmp, e)
				}
			}
		}()
	}
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil && os.IsNotExist(err) {
		if err := root.MkdirAll(filepath.Dir(name), os.FileMode(0777)); err != nil {
			return err
		}
		f, err = root.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	}
	if err != nil {
		return err
	}

	if TryCFR {
		_, err = io.Copy(f, in)
	} else {
		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		_, err = io.CopyBuffer(onlyWriter{f}, in, *buf)
	}
	if err != nil {
		_ = f.Close()
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}
	if !PutInplace {
		err = root.Rename(tmp, name)
	}
	return err
}

func (d *filestore) Copy(ctx context.Context, dst, src string) error {
	r, err := d.Get(ctx, src, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	return d.Put(ctx, dst, r)
}

func (d *filestore) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	root, name, err := d.rootedPath(key, false)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(name)
	if err != nil && os.IsNotExist(err) {
		err = nil
	}
	return err
}

type mEntry struct {
	os.FileInfo
	name      string
	fi        os.FileInfo
	isSymlink bool
}

func (m *mEntry) Name() string {
	return m.name
}

func (m *mEntry) Info() os.FileInfo {
	if m.fi != nil {
		return m.fi
	}
	return m.FileInfo
}

func (m *mEntry) IsDir() bool {
	if m.fi != nil {
		return m.fi.IsDir()
	}
	return m.FileInfo.IsDir()
}

// readDirSorted reads the directory named by dir and returns
// a sorted list of directory entries.
func readDirSorted(root *os.Root, dir string, followLink bool) ([]*mEntry, error) {
	f, err := openRootFileFollowingFinal(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	mEntries := make([]*mEntry, 0, len(entries))
	for _, e := range entries {
		isSymlink := e.Mode()&os.ModeSymlink != 0
		if e.IsDir() {
			mEntries = append(mEntries, &mEntry{e, e.Name() + dirSuffix, nil, false})
		} else if isSymlink && followLink {
			target, err := openFileAt(f, e.Name())
			if err != nil {
				mEntries = append(mEntries, &mEntry{e, e.Name(), nil, true})
				continue
			}
			fi, err := target.Stat()
			_ = target.Close()
			if err != nil {
				mEntries = append(mEntries, &mEntry{e, e.Name(), nil, true})
				continue
			}
			name := e.Name()
			if fi.IsDir() {
				name = e.Name() + dirSuffix
			} else if !fi.Mode().IsRegular() {
				logger.Warnf("%s is not a regular file, ignore it", name)
				continue
			}
			mEntries = append(mEntries, &mEntry{e, name, fi, false})
		} else {
			if !isSymlink && !e.Mode().IsRegular() {
				logger.Warnf("%s is not a regular file, ignore it", e.Name())
				continue
			}
			mEntries = append(mEntries, &mEntry{e, e.Name(), nil, isSymlink})
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].Name() < mEntries[j].Name() })
	return mEntries, err
}

func (d *filestore) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", notSupported
	}
	prefixPath, err := d.path(prefix)
	if err != nil {
		return nil, false, "", err
	}
	root, _, err := d.rootedPath(prefix, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, "", nil
		}
		return nil, false, "", err
	}
	defer root.Close()
	var dir string = prefixPath
	var objs []Object
	if !strings.HasSuffix(d.root+prefix, dirSuffix) {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := d.Head(ctx, prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, "", nil
			}
			return nil, false, "", err
		}
		objs = append(objs, obj)
	}
	boundary := d.root
	if !strings.HasSuffix(boundary, dirSuffix) {
		boundary = filepath.Dir(boundary)
	}
	rootedDir, err := filepath.Rel(boundary, dir)
	if err != nil {
		return nil, false, "", err
	}
	entries, err := readDirSorted(root, rootedDir, followLink)
	if err != nil {
		if os.IsPermission(err) {
			logger.Warnf("skip %s: %s", dir, err)
			return nil, false, "", nil
		}
		if os.IsNotExist(err) {
			logger.Debugf("skip %s: %s", dir, err)
			return nil, false, "", nil
		}
		return nil, false, "", err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		if e.IsDir() {
			p = p + "/"
		}
		if !strings.HasPrefix(p, d.root) {
			continue
		}
		key := p[len(d.root):]
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		info := e.Info()
		f := toFile(key, info, e.isSymlink, getOwnerGroup)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	return generateListResult(objs, limit)
}

func (d *filestore) Chmod(key string, mode os.FileMode) error {
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
	return chmodAt(parent, base, mode)
}

func (d *filestore) Chown(key string, owner, group string) error {
	root, name, err := d.rootedPath(key, false)
	if err != nil {
		return err
	}
	defer root.Close()
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	if uid == -1 || gid == -1 {
		return fmt.Errorf("user(%s):group(%s) not found", owner, group)
	}
	return root.Lchown(name, uid, gid)
}

func newDisk(root, accesskey, secretkey, token string) (ObjectStorage, error) {
	// For Windows, the path looks like /C:/a/b/c/
	if runtime.GOOS == "windows" {
		root = strings.TrimPrefix(root, "/")
	}
	return &filestore{root: root}, nil
}

func init() {
	Register("file", newDisk)
}
