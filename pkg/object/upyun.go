// +build !noupyun

/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
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
		return nil, err
	}
	return &obj{
		key,
		info.Size,
		info.Time,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (u *up) Get(key string, off, limit int64) (io.ReadCloser, error) {
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
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (u *up) Put(key string, in io.Reader) error {
	return u.c.Put(&upyun.PutObjectConfig{
		Path:   "/" + key,
		Reader: in,
	})
}

func (u *up) Delete(key string) error {
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

func (u *up) List(prefix, marker string, limit int64) ([]Object, error) {
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
			objs = append(objs, &obj{key, fi.Size, fi.Time, strings.HasSuffix(key, "/")})
		}
	}
	if len(objs) > 0 {
		return objs, nil
	}
	u.listing = nil
	return nil, u.err
}

func newUpyun(endpoint, user, passwd string) (ObjectStorage, error) {
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
	return &up{c: upyun.NewUpYun(cfg)}, nil
}

func init() {
	Register("upyun", newUpyun)
}
