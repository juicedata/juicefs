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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/juicedata/gogfapi/gfapi"
)

type gluster struct {
	DefaultObjectStorage
	name string
	indx uint64
	vols []*gfapi.Volume
}

func (g *gluster) String() string {
	return fmt.Sprintf("gluster://%s/", g.name)
}

func (g *gluster) vol() *gfapi.Volume {
	if len(g.vols) == 1 {
		return g.vols[0]
	}
	n := atomic.AddUint64(&g.indx, 1)
	return g.vols[n%uint64(len(g.vols))]
}

func (g *gluster) Head(key string) (Object, error) {
	fi, err := g.vol().Stat(key)
	if err != nil {
		return nil, err
	}
	return g.toFile(key, fi, false), nil
}

func (g *gluster) toFile(key string, fi fs.FileInfo, isSymlink bool) *file {
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

func (g *gluster) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := g.vol().Open(key)
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

func (g *gluster) Put(key string, in io.Reader) error {
	v := g.vol()
	if strings.HasSuffix(key, dirSuffix) {
		return v.MkdirAll(key, os.FileMode(0777))
	}
	f, err := v.OpenFile(key, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil && os.IsNotExist(err) {
		if err := v.MkdirAll(filepath.Dir(key), os.FileMode(0777)); err != nil {
			return err
		}
		f, err = v.OpenFile(key, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	}
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Unlink(key)
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
	err = f.Close()
	return err
}

func (g *gluster) Delete(key string) error {
	v := g.vol()
	err := v.Unlink(key)
	if err != nil && strings.Contains(err.Error(), "is a directory") {
		err = v.Rmdir(key)
	}
	if os.IsNotExist(err) {
		err = nil
	}
	return err
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func (g *gluster) readDirSorted(dirname string, followLink bool) ([]*mEntry, error) {
	v := g.vol()
	f, err := v.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdir(0)
	if err != nil {
		return nil, err
	}

	mEntries := make([]*mEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		if e.IsDir() {
			mEntries = append(mEntries, &mEntry{nil, name + dirSuffix, e, false})
		} else if !e.Mode().IsRegular() && followLink {
			fi, err := v.Stat(filepath.Join(dirname, name))
			if err != nil {
				mEntries = append(mEntries, &mEntry{nil, name, e, true})
				continue
			}
			if fi.IsDir() {
				name += dirSuffix
			}
			mEntries = append(mEntries, &mEntry{nil, name, fi, false})
		} else {
			mEntries = append(mEntries, &mEntry{nil, name, e, !e.Mode().IsRegular()})
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].Name() < mEntries[j].Name() })
	return mEntries, err
}

func (g *gluster) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
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
		obj, err := g.Head(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		objs = append(objs, obj)
	}
	entries, err := g.readDirSorted(dir, followLink)
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
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			p = filepath.ToSlash(p + "/")
		}
		key := p
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		info := e.Info()
		f := g.toFile(key, info, e.isSymlink)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	return objs, nil
}

func (g *gluster) Chtimes(path string, mtime time.Time) error {
	return notSupported
}

func (g *gluster) Chmod(path string, mode os.FileMode) error {
	return g.vol().Chmod(path, mode)
}

func (g *gluster) Chown(path string, owner, group string) error {
	return notSupported
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
	// multiple clinets for possible performance improvement
	var size int
	if ssize := os.Getenv("JFS_NUM_GLUSTER_CLIENTS"); ssize != "" {
		size, _ = strconv.Atoi(ssize)
		if size > 8 {
			size = 8
		}
	}
	if size < 1 {
		size = 1
	}
	// logging
	level := gfapi.LogInfo
	if slevel := os.Getenv("JFS_GLUSTER_LOG_LEVEL"); slevel != "" {
		switch strings.ToUpper(slevel) {
		case "ERROR":
			level = gfapi.LogError
		case "WARN", "WARNING":
			level = gfapi.LogWarning
		case "INFO":
			level = gfapi.LogInfo
		case "DEBUG":
			level = gfapi.LogDebug
		case "TRACE":
			level = gfapi.LogTrace
		}
	}
	logPath := os.Getenv("JFS_GLUSTER_LOG_PATH")
	hosts := strings.Split(uri.Host, ",")
	pid := os.Getpid()
	ostore := gluster{
		name: name,
		vols: make([]*gfapi.Volume, size),
	}
	for i := range ostore.vols {
		v := &gfapi.Volume{}
		// TODO: support port in host
		err = v.Init(name, hosts...)
		if err != nil {
			return nil, fmt.Errorf("init %s: %s", name, err)
		}
		if logPath == "" {
			err = v.SetLogging(fmt.Sprintf("/var/log/glusterfs/%s-%s-%d-%d.log", hosts[0], name, pid, i), level)
		} else {
			err = v.SetLogging(logPath, level)
		}
		if err != nil {
			logger.Warnf("Set gluster logging for vol %s: %s", name, err)
		}
		err = v.Mount()
		if err != nil {
			return nil, fmt.Errorf("mount %s: %s", name, err)
		}
		ostore.vols[i] = v
	}
	return &ostore, nil
}

func init() {
	Register("gluster", newGluster)
}
