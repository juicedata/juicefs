//go:build !noswift
// +build !noswift

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
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/ncw/swift"
)

type swiftOSS struct {
	DefaultObjectStorage
	conn       *swift.Connection
	region     string
	storageUrl string
	container  string
}

func (s *swiftOSS) String() string {
	return fmt.Sprintf("swift://%s/", s.container)
}

func (s *swiftOSS) Create() error {
	// No error is returned if it already exists but the metadata if any will be updated.
	return s.conn.ContainerCreate(s.container, nil)
}

func (s *swiftOSS) Get(key string, off, limit int64) (io.ReadCloser, error) {
	headers := make(map[string]string)
	if off > 0 || limit > 0 {
		if limit > 0 {
			headers["Range"] = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			headers["Range"] = fmt.Sprintf("bytes=%d-", off)
		}
	}
	f, _, err := s.conn.ObjectOpen(s.container, key, true, headers)
	return f, err
}

func (s *swiftOSS) Put(key string, in io.Reader) error {
	mimeType := utils.GuessMimeType(key)
	_, err := s.conn.ObjectPut(s.container, key, in, true, "", mimeType, nil)
	return err
}

func (s *swiftOSS) Delete(key string) error {
	err := s.conn.ObjectDelete(s.container, key)
	if err != nil && errors.Is(err, swift.ObjectNotFound) {
		err = nil
	}
	return err
}

func (s *swiftOSS) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 10000 {
		limit = 10000
	}
	objects, err := s.conn.Objects(s.container, &swift.ObjectsOpts{Prefix: prefix, Marker: marker, Limit: int(limit)})
	if err != nil {
		return nil, err
	}
	var objs = make([]Object, len(objects))
	for i, o := range objects {
		objs[i] = &obj{o.Name, o.Bytes, o.LastModified, strings.HasSuffix(o.Name, "/")}
	}
	return objs, nil
}

func (s *swiftOSS) Head(key string) (Object, error) {
	object, _, err := s.conn.Object(s.container, key)
	if err == swift.ObjectNotFound {
		err = os.ErrNotExist
	}
	return &obj{
		key,
		object.Bytes,
		object.LastModified,
		strings.HasSuffix(key, "/"),
	}, err
}

func newSwiftOSS(endpoint, username, apiKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	if uri.Scheme != "http" && uri.Scheme != "https" {
		return nil, fmt.Errorf("Invalid uri.Scheme: %s", uri.Scheme)
	}

	hostSlice := strings.SplitN(uri.Host, ".", 2)
	if len(hostSlice) != 2 {
		return nil, fmt.Errorf("Invalid host: %s", uri.Host)
	}
	container := hostSlice[0]
	host := hostSlice[1]

	// current only support V1 authentication
	authURL := uri.Scheme + "://" + host + "/auth/v1.0"

	conn := swift.Connection{
		UserName:  username,
		ApiKey:    apiKey,
		AuthToken: token,
		AuthUrl:   authURL,
	}
	err = conn.Authenticate()
	if err != nil {
		return nil, fmt.Errorf("Auth: %s", err)
	}
	return &swiftOSS{DefaultObjectStorage{}, &conn, conn.Region, conn.StorageUrl, container}, nil
}

func init() {
	Register("swift", newSwiftOSS)
}
