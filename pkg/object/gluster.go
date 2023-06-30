//go:build gluster
// +build gluster

/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/gogfapi/gfapi"
)

type gluster struct {
	DefaultObjectStorage
	name string
	vol  *gfapi.Volume
}

func (c *gluster) String() string {
	return fmt.Sprintf("gluster://%s/", c.name)
}

func (c *gluster) Head(key string) (Object, error) {
	fi, err := c.vol.Stat(key)
	if err != nil {
		return nil, err
	}
	return c.toFile(key, fi, false), nil
}

func (d *gluster) toFile(key string, fi fs.FileInfo, isSymlink bool) *file {
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

func (c *gluster) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := c.vol.Open(key)
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

func (c *gluster) Put(key string, in io.Reader) error {
	if strings.HasSuffix(key, dirSuffix) {
		return c.vol.MkdirAll(key, os.FileMode(0777))
	}
	p := key
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp"+strconv.Itoa(rand.Int()))
	f, err := c.vol.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil && os.IsNotExist(err) {
		if err := c.vol.MkdirAll(filepath.Dir(p), os.FileMode(0777)); err != nil {
			return err
		}
		f, err = c.vol.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	}
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = c.vol.Unlink(tmp)
		}
	}()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(f, in, *buf)
	if err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	err = c.vol.Rename(tmp, p)
	return err
}

func (c *gluster) Delete(key string) error {
	err := c.vol.Unlink(key)
	if os.IsNotExist(err) {
		err = nil
	}
	return err
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func (d *gluster) readDirSorted(dirname string) ([]*mEntry, error) {
	f, err := d.vol.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdir(0)
	mEntries := make([]*mEntry, len(entries))

	for i, e := range entries {
		if e.IsDir() {
			mEntries[i] = &mEntry{nil, e.Name() + dirSuffix, e, false}
		} else if !e.Mode().IsRegular() {
			// follow symlink
			fi, err := os.Stat(filepath.Join(dirname, e.Name()))
			if err != nil {
				mEntries[i] = &mEntry{nil, e.Name(), e, true}
				continue
			}
			name := e.Name()
			if fi.IsDir() {
				name = e.Name() + dirSuffix
			}
			mEntries[i] = &mEntry{nil, name, fi, true}
		} else {
			mEntries[i] = &mEntry{nil, e.Name(), e, false}
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].Name() < mEntries[j].Name() })
	return mEntries, err
}

func (d *gluster) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	if delimiter != "/" {
		return nil, notSupported
	}
	var dir string = prefix
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
	entries, err := readDirSorted(dir)
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
		key := p
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

func (d *gluster) Chmod(path string, mode os.FileMode) error {
	return d.vol.Chmod(path, mode)
}

func newGluster(endpoint, ak, sk, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("gluster://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ps := strings.Split(uri.Path, "/")
	if len(ps) == 1 {
		return nil, fmt.Errorf("no volume provided")
	}
	name := ps[1]
	v := &gfapi.Volume{}
	// TODO: support port in host
	err = v.Init(name, strings.Split(uri.Host, ",")...)
	if err != nil {
		return nil, fmt.Errorf("init %s: %s", name, err)
	}
	err = v.Mount()
	if err != nil {
		return nil, fmt.Errorf("mount %s: %s", name, err)
	}
	return &gluster{
		name: name,
		vol:  v,
	}, nil
}

func init() {
	Register("gluster", newGluster)
}
