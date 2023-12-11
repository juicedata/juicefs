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
	"io/fs"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

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
	p := d.path(newName)
	if _, err := os.Stat(filepath.Dir(p)); err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(p), os.FileMode(0777)); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(oldName, p)
}

func (d *filestore) Readlink(name string) (string, error) {
	return os.Readlink(d.path(name))
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
	return filepath.Clean(d.root + key)
}

func (d *filestore) Head(key string) (Object, error) {
	p := d.path(key)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return toFile(key, fi, false, getOwnerGroup), nil
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
	if finfo.IsDir() || off > finfo.Size() {
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

func (d *filestore) Put(key string, in io.Reader) (err error) {
	p := d.path(key)

	if strings.HasSuffix(key, dirSuffix) || key == "" && strings.HasSuffix(d.root, dirSuffix) {
		return os.MkdirAll(p, os.FileMode(0777))
	}

	var tmp string
	if PutInplace {
		tmp = p
	} else {
		name := filepath.Base(p)
		if len(name) > 200 {
			name = name[:200]
		}
		tmp = filepath.Join(filepath.Dir(p), "."+name+".tmp"+strconv.Itoa(rand.Int()))
		defer func() {
			if err != nil {
				_ = os.Remove(tmp)
			}
		}()
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(p), os.FileMode(0777)); err != nil {
			return err
		}
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
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
		err = os.Rename(tmp, p)
	}
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
func readDirSorted(dir string, followLink bool) ([]*mEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	mEntries := make([]*mEntry, len(entries))
	for i, e := range entries {
		if e.IsDir() {
			mEntries[i] = &mEntry{e, e.Name() + dirSuffix, nil, false}
		} else if !e.Mode().IsRegular() && followLink {
			fi, err := os.Stat(filepath.Join(dir, e.Name()))
			if err != nil {
				mEntries[i] = &mEntry{e, e.Name(), nil, true}
				continue
			}
			name := e.Name()
			if fi.IsDir() {
				name = e.Name() + dirSuffix
			}
			mEntries[i] = &mEntry{e, name, fi, false}
		} else {
			mEntries[i] = &mEntry{e, e.Name(), nil, !e.Mode().IsRegular()}
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].Name() < mEntries[j].Name() })
	return mEntries, err
}

func (d *filestore) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if delimiter != "/" {
		return nil, notSupported
	}
	var dir string = d.root + prefix
	var objs []Object
	if !strings.HasSuffix(dir, dirSuffix) {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := d.Head(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		objs = append(objs, obj)
	}
	entries, err := readDirSorted(dir, followLink)
	if err != nil {
		if os.IsPermission(err) {
			logger.Warnf("skip %s: %s", dir, err)
			return nil, nil
		}
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
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
	return objs, nil
}

func (d *filestore) Chmod(key string, mode os.FileMode) error {
	p := d.path(key)
	return os.Chmod(p, mode)
}

func (d *filestore) Chown(key string, owner, group string) error {
	p := d.path(key)
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	return os.Lchown(p, uid, gid)
}

func newDisk(root, accesskey, secretkey, token string) (ObjectStorage, error) {
	// For Windows, the path looks like /C:/a/b/c/
	if runtime.GOOS == "windows" {
		root = strings.TrimPrefix(root, "/")
	}
	if strings.HasSuffix(root, dirSuffix) {
		logger.Debugf("Ensure directory %s", root)
		if err := os.MkdirAll(root, 0777); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", root, err)
		}
	} else {
		dir := filepath.Dir(root)
		logger.Debugf("Ensure directory %s", dir)
		if err := os.MkdirAll(dir, 0777); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", dir, err)
		}
	}
	return &filestore{root: root}, nil
}

func init() {
	Register("file", newDisk)
}
