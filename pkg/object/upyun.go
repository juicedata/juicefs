//go:build !noupyun
// +build !noupyun

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"net/url"
	"os"
	"strings"

	"github.com/upyun/go-sdk/v3/upyun"
)

type up struct {
	DefaultObjectStorage
	c       *upyun.UpYun
	listing chan *upyun.FileInfo
	err     error
}

func (u *up) String() string {
	return fmt.Sprintf("upyun://%s/", u.c.Bucket)
}

func (u *up) Create() error {
	return nil
}

func (u *up) Head(key string) (Object, error) {
	info, err := u.c.GetInfo("/" + key)
	if err != nil {
		if upyun.IsNotExist(err) {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key,
		info.Size,
		info.Time,
		strings.HasSuffix(key, "/"),
		"",
	}, nil
}

func (u *up) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	w := bytes.NewBuffer(nil)
	_, err := u.c.Get(&upyun.GetObjectConfig{
		Path:   "/" + key,
		Writer: w,
	})
	if err != nil {
		return nil, err
	}
	data := w.Bytes()[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (u *up) Put(key string, in io.Reader, getters ...AttrGetter) error {
	return u.c.Put(&upyun.PutObjectConfig{
		Path:   "/" + key,
		Reader: in,
	})
}

func (u *up) Delete(key string, getters ...AttrGetter) error {
	return u.c.Delete(&upyun.DeleteObjectConfig{
		Path: "/" + key,
	})
}

func (u *up) Copy(dst, src string) error {
	return u.c.Copy(&upyun.CopyObjectConfig{
		SrcPath:  "/" + src,
		DestPath: "/" + dst,
	})
}

func (u *up) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "" {
		return nil, false, "", notSupported
	}
	if u.listing == nil {
		listing := make(chan *upyun.FileInfo, limit)
		go func() {
			u.err = u.c.List(&upyun.GetObjectsConfig{
				Path:         "/" + prefix,
				ObjectsChan:  listing,
				MaxListTries: 10,
				MaxListLevel: -1,
			})
		}()
		u.listing = listing
	}
	objs := make([]Object, 0, limit)
	for len(objs) < int(limit) {
		fi, ok := <-u.listing
		if !ok {
			break
		}
		key := prefix + "/" + fi.Name
		if !fi.IsDir && key > marker {
			objs = append(objs, &obj{key, fi.Size, fi.Time, strings.HasSuffix(key, "/"), ""})
		}
	}
	if len(objs) == 0 {
		u.listing = nil
	}
	return generateListResult(objs, limit)
}

func newUpyun(endpoint, user, passwd, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	bucket := strings.Split(uri.Host, ".")[0]
	cfg := &upyun.UpYunConfig{
		Bucket:    bucket,
		Operator:  user,
		Password:  passwd,
		UserAgent: UserAgent,
		Hosts:     make(map[string]string),
	}
	if strings.Contains(uri.Host, ".") {
		cfg.Hosts["v0.api.upyun.com"] = strings.SplitN(uri.Host, ".", 2)[1]
	}
	upYun := upyun.NewUpYun(cfg)
	upYun.SetHTTPClient(httpClient)
	return &up{c: upYun}, nil
}

func init() {
	Register("upyun", newUpyun)
}
