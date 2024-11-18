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
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

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
		"",
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

func (w *webdav) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
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

func (w *webdav) Put(key string, in io.Reader, getters ...AttrGetter) error {
	if key == "" {
		return nil
	}
	if strings.HasSuffix(key, dirSuffix) {
		return w.c.MkdirAll(key, 0)
	}
	return w.c.WriteStream(key, in, 0)
}

func (w *webdav) Delete(key string, getters ...AttrGetter) error {
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
			return fmt.Errorf("%s is non-empty directory", key)
		}
	}
	return w.c.Remove(key)
}

func (w *webdav) Copy(dst, src string) error {
	return w.c.Copy(src, dst, true)
}

type webDAVFile struct {
	os.FileInfo
	name string
}

func (w webDAVFile) Name() string {
	return w.name
}

func (w *webdav) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", notSupported
	}

	root := "/" + prefix
	var objs []Object
	if !strings.HasSuffix(root, dirSuffix) {
		// If the root is not ends with `/`, we'll list the directory root resides.
		root = path.Dir(root)
		if !strings.HasSuffix(root, dirSuffix) {
			root += dirSuffix
		}
	}

	infos, err := w.c.ReadDir(root)
	if err != nil {
		if gowebdav.IsErrCode(err, http.StatusForbidden) {
			logger.Warnf("skip %s: %s", root, err)
			return nil, false, "", nil
		}
		if gowebdav.IsErrNotFound(err) {
			return nil, false, "", nil
		}
		return nil, false, "", err
	}
	sortedInfos := make([]os.FileInfo, len(infos))
	for idx, o := range infos {
		if o.IsDir() {
			sortedInfos[idx] = &webDAVFile{name: o.Name() + dirSuffix, FileInfo: o}
		} else {
			sortedInfos[idx] = o
		}
	}
	sort.Slice(sortedInfos, func(i, j int) bool {
		return sortedInfos[i].Name() < sortedInfos[j].Name()
	})
	for _, info := range sortedInfos {
		key := root[1:] + info.Name()
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		objs = append(objs, &obj{
			key,
			info.Size(),
			info.ModTime(),
			info.IsDir(),
			"",
		})
		if len(objs) == int(limit) {
			break
		}
	}
	return generateListResult(objs, limit)
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
	c.SetTransport(httpClient.Transport)
	return &webdav{endpoint: uri, c: c}, nil
}

func init() {
	Register("webdav", newWebDAV)
}
