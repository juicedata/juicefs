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
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	gowebdav "github.com/emersion/go-webdav"
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
		return nil, err
	}
	return &obj{
		key,
		info.Size,
		info.ModTime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (w *webdav) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off == 0 && limit <= 0 {
		return w.c.Open(key)
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
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, parseError(resp)
	}
	return resp.Body, nil
}

func (w *webdav) mkdirs(p string) error {
	err := w.c.Mkdir(p)
	if err != nil && w.isNotExist(path.Dir(p)) {
		if w.mkdirs(path.Dir(p)) == nil {
			err = w.c.Mkdir(p)
		}
	}
	return err
}

func (w *webdav) isNotExist(key string) bool {
	if _, err := w.c.Stat(key); err != nil {
		return strings.Contains(strings.ToLower(err.Error()), "not found")
	}
	return false
}

func (w *webdav) Put(key string, in io.Reader) error {
	var buf = bytes.NewBuffer(nil)
	in = io.TeeReader(in, buf)
	out, err := w.c.Create(key)
	if err != nil {
		return err
	}
	wbuf := bufPool.Get().(*[]byte)
	defer bufPool.Put(wbuf)
	_, err = io.CopyBuffer(out, in, *wbuf)
	if err != nil {
		return err
	}
	err = out.Close()
	if err != nil && w.isNotExist(path.Dir(key)) {
		if w.mkdirs(path.Dir(key)) == nil {
			return w.Put(key, bytes.NewReader(buf.Bytes()))
		}
	}
	return err
}

func (w *webdav) Delete(key string) error {
	err := w.c.RemoveAll(key)
	if err != nil && w.isNotExist(key) {
		err = nil
	}
	return err
}

func (w *webdav) Copy(dst, src string) error {
	return w.c.CopyAll(src, dst, true)
}

func (w *webdav) ListAll(prefix, marker string) (<-chan Object, error) {
	listed := make(chan Object, 10240)
	var walkRoot string
	if strings.HasSuffix(prefix, dirSuffix) {
		walkRoot = prefix
	} else {
		// If the root is not ends with `/`, we'll list the directory root resides.
		walkRoot = path.Dir(prefix)
	}
	infos, err := w.c.Readdir(walkRoot, true)
	if err != nil {
		return nil, err
	}
	go func() {
		for _, info := range infos {
			key := info.Path[len(w.endpoint.Path):]
			if info.IsDir || !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
				continue
			}
			o := &obj{
				key,
				info.Size,
				info.ModTime,
				info.IsDir,
			}
			listed <- o
		}
		close(listed)
	}()
	return listed, nil
}

func newWebDAV(endpoint, user, passwd string) (ObjectStorage, error) {
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
	c, err := gowebdav.NewClient(httpClient, uri.String())
	if err != nil {
		return nil, fmt.Errorf("create client for %s: %s", uri, err)
	}
	return &webdav{endpoint: uri, c: c}, nil
}

func init() {
	Register("webdav", newWebDAV)
}
