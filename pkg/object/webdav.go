//go:build !nowebdav
// +build !nowebdav

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
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/studio-b12/gowebdav"
)

type webdav struct {
	DefaultObjectStorage
	endpoint *url.URL
	c        *gowebdav.Client
}

func (w *webdav) String() string {
	return fmt.Sprintf("webdav://%s/", w.endpoint.Host)
}

func (w *webdav) Create() error {
	return nil
}

func (w *webdav) Head(key string) (Object, error) {
	info, err := w.c.Stat(key)
	if err != nil {
		if gowebdav.IsErrNotFound(err) {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key,
		info.Size(),
		info.ModTime(),
		info.IsDir(),
	}, nil
}

// limitedReadCloser wraps a io.ReadCloser and limits the number of bytes that can be read from it.
type limitedReadCloser struct {
	rc        io.ReadCloser
	remaining int
}

func (l *limitedReadCloser) Read(buf []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, io.EOF
	}

	if len(buf) > l.remaining {
		buf = buf[0:l.remaining]
	}

	n, err := l.rc.Read(buf)
	l.remaining -= n

	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.rc.Close()
}

func (w *webdav) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off == 0 && limit <= 0 {
		return w.c.ReadStream(key)
	}
	url := &url.URL{
		Scheme: w.endpoint.Scheme,
		User:   w.endpoint.User,
		Host:   w.endpoint.Host,
		Path:   path.Join(w.endpoint.Path, key),
	}
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return nil, err
	}
	if limit > 0 {
		req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", off, off+limit-1))
	} else {
		req.Header.Add("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusPartialContent {
		// server supported partial content, return as-is.
		return resp.Body, nil
	}

	// server returned success, but did not support partial content, so we have the whole
	// stream in rs.Body
	if resp.StatusCode == 200 {
		if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, off)); err != nil {
			return nil, &os.PathError{Op: "ReadStreamRange", Path: key, Err: err}
		}
		// return a io.ReadCloser that is limited to `length` bytes.
		return &limitedReadCloser{resp.Body, int(limit)}, nil
	}
	_ = resp.Body.Close()
	return nil, &os.PathError{Op: "ReadStreamRange", Path: key, Err: err}
}

func (w *webdav) Put(key string, in io.Reader) error {
	if key == "" {
		return nil
	}
	if strings.HasSuffix(key, dirSuffix) {
		return w.c.MkdirAll(key, 0)
	}
	return w.c.WriteStream(key, in, 0)
}

func (w *webdav) Delete(key string) error {
	info, err := w.c.Stat(key)
	if gowebdav.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		infos, err := w.c.ReadDir(key)
		if err != nil {
			if gowebdav.IsErrNotFound(err) {
				return nil
			}
			return err
		}
		if len(infos) != 0 {
			return nil
		}
	}
	return w.c.Remove(key)
}

func (w *webdav) Copy(dst, src string) error {
	return w.c.Copy(src, dst, true)
}

type WebDAVWalkFunc func(path string, info fs.FileInfo, err error) error

type webDAVFile struct {
	name     string
	size     int64
	modified time.Time
	isdir    bool
}

func (f webDAVFile) Name() string {
	return f.name
}

func (f webDAVFile) Size() int64 {
	return f.size
}

func (f webDAVFile) Mode() os.FileMode {
	if f.isdir {
		return 0775 | os.ModeDir
	}
	return 0664
}

func (f webDAVFile) ModTime() time.Time {
	return f.modified
}

func (f webDAVFile) IsDir() bool {
	return f.isdir
}

func (f webDAVFile) Sys() interface{} {
	return nil
}

func webdavWalk(client *gowebdav.Client, path string, info fs.FileInfo, walkFn WebDAVWalkFunc) error {
	if !info.IsDir() {
		return walkFn(path, info, nil)
	}
	infos, err := client.ReadDir(path)
	sortedInfos := make([]os.FileInfo, len(infos))
	for idx, o := range infos {
		f := &webDAVFile{name: o.Name(), size: o.Size(), modified: o.ModTime(), isdir: o.IsDir()}
		if o.IsDir() {
			f.name = o.Name() + dirSuffix
		}
		sortedInfos[idx] = f
	}
	sort.Slice(sortedInfos, func(i, j int) bool {
		return sortedInfos[i].Name() < sortedInfos[j].Name()
	})
	err1 := walkFn(path, info, err)
	if err != nil || err1 != nil {
		return err1
	}

	for _, info := range sortedInfos {
		filename := filepath.Join(path, info.Name())
		if !strings.HasPrefix(filename, dirSuffix) {
			filename = dirSuffix + filename
		}
		err := webdavWalk(client, filename, info, walkFn)
		if err != nil {
			if !info.IsDir() || err != fs.SkipDir {
				return err
			}
		}
	}
	return nil
}

func (w *webdav) Walk(root string, fn WebDAVWalkFunc) error {
	info, err := w.c.Stat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = webdavWalk(w.c, root, info, fn)
	}
	if err == fs.SkipDir {
		return nil
	}
	return err
}

func (w *webdav) ListAll(prefix, marker string) (<-chan Object, error) {
	listed := make(chan Object, 10240)
	if !strings.HasPrefix(prefix, dirSuffix) {
		prefix = dirSuffix + prefix
	}
	if marker != "" && !strings.HasPrefix(marker, dirSuffix) {
		marker = dirSuffix + marker
	}
	go func() {
		var walkRoot string
		if strings.HasSuffix(prefix, dirSuffix) {
			walkRoot = prefix
		} else {
			// If the root is not ends with `/`, we'll list the directory root resides.
			walkRoot = path.Dir(prefix)
		}

		_ = w.Walk(walkRoot, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				if gowebdav.IsErrNotFound(err) {
					logger.Warnf("skip not exist file or directory: %s", path)
					return nil
				}
				listed <- nil
				logger.Errorf("list %s: %s", path, err)
				return nil
			}
			if info.IsDir() {
				if !strings.HasSuffix(path, dirSuffix) {
					path += dirSuffix
				}
				if strings.HasPrefix(prefix, path) || strings.HasPrefix(path, prefix) {
					return nil
				}
				return fs.SkipDir
			}
			if !strings.HasPrefix(path, prefix) || (marker != "" && path <= marker) {
				return nil
			}

			o := &obj{
				path[1:],
				info.Size(),
				info.ModTime(),
				info.IsDir(),
			}
			listed <- o
			return nil
		})
		close(listed)
	}()
	return listed, nil
}

func newWebDAV(endpoint, user, passwd, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	if uri.Path == "" {
		uri.Path = "/"
	}
	uri.User = url.UserPassword(user, passwd)
	c := gowebdav.NewClient(uri.String(), user, passwd)

	return &webdav{endpoint: uri, c: c}, nil
}

func init() {
	Register("webdav", newWebDAV)
}
