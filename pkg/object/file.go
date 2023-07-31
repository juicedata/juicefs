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
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	dirSuffix = "/"
)

var TryCFR bool // try copy_file_range

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
	return d.root + key
}

func (d *filestore) Head(key string) (Object, error) {
	p := d.path(key)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return d.toFile(key, fi, false), nil
}

func (d *filestore) toFile(key string, fi fs.FileInfo, isSymlink bool) *file {
	size := fi.Size()
	if fi.IsDir() {
		size = 0
	}
	owner, group := getOwnerGroup(fi)
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

func (d *filestore) Put(key string, in io.Reader) error {
	p := d.path(key)

	if strings.HasSuffix(key, dirSuffix) || key == "" && strings.HasSuffix(d.root, dirSuffix) {
		return os.MkdirAll(p, os.FileMode(0777))
	}

	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp"+strconv.Itoa(rand.Int()))
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
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

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

type mEntry struct {
	os.DirEntry
	name      string
	fi        os.FileInfo
	isSymlink bool
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

func (m *mEntry) IsDir() bool {
	if m.fi != nil {
		return m.fi.IsDir()
	}
	return m.DirEntry.IsDir()
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func readDirSorted(dirname string, followLink bool) ([]*mEntry, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	mEntries := make([]*mEntry, len(entries))
	for i, e := range entries {
		if e.IsDir() {
			mEntries[i] = &mEntry{e, e.Name() + dirSuffix, nil, false}
		} else if !e.Type().IsRegular() && followLink {
			fi, err := os.Stat(filepath.Join(dirname, e.Name()))
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
			mEntries[i] = &mEntry{e, e.Name(), nil, !e.Type().IsRegular()}
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
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			p = filepath.ToSlash(p + "/")
		}
		if !strings.HasPrefix(p, d.root) {
			continue
		}
		key := p[len(d.root):]
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			logger.Warnf("stat %s: %s", p, err)
			continue
		}
		f := d.toFile(key, info, e.isSymlink)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	return objs, nil
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
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	return os.Chown(p, uid, gid)
}

func newDisk(root, accesskey, secretkey, token string) (ObjectStorage, error) {
	// For Windows, the path looks like /C:/a/b/c/
	if runtime.GOOS == "windows" && strings.HasPrefix(root, "/") {
		root = root[1:]
	}
	if strings.HasSuffix(root, dirSuffix) {
		logger.Debugf("Ensure directory %s", root)
		if err := os.MkdirAll(root, 0777); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", root, err)
		}
	} else {
		dir := path.Dir(root)
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
