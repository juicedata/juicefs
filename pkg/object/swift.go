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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/ncw/swift/v2"
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

func (s *swiftOSS) Create(ctx context.Context) error {
	// No error is returned if it already exists but the metadata if any will be updated.
	return s.conn.ContainerCreate(ctx, s.container, nil)
}

func (s *swiftOSS) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	headers := make(map[string]string)
	if off > 0 || limit > 0 {
		if limit > 0 {
			headers["Range"] = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			headers["Range"] = fmt.Sprintf("bytes=%d-", off)
		}
	}
	f, _, err := s.conn.ObjectOpen(ctx, s.container, key, true, headers)
	return f, err
}

func (s *swiftOSS) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	mimeType := utils.GuessMimeType(key)
	_, err := s.conn.ObjectPut(ctx, s.container, key, in, true, "", mimeType, nil)
	return err
}

func (s *swiftOSS) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	err := s.conn.ObjectDelete(ctx, s.container, key)
	if err != nil && errors.Is(err, swift.ObjectNotFound) {
		err = nil
	}
	return err
}

func (s *swiftOSS) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 10000 {
		limit = 10000
	}
	var delimiter_ rune
	if delimiter != "" {
		if len([]rune(delimiter)) == 1 {
			delimiter_ = []rune(delimiter)[0]
		} else {
			return nil, false, "", fmt.Errorf("delimiter should be a rune but now is %s", delimiter)
		}
	}
	objects, err := s.conn.Objects(ctx, s.container, &swift.ObjectsOpts{Prefix: prefix, Marker: marker, Delimiter: delimiter_, Limit: int(limit)})
	if err != nil {
		return nil, false, "", err
	}
	var objs = make([]Object, len(objects))
	for i, o := range objects {
		// https://docs.openstack.org/swift/latest/api/pseudo-hierarchical-folders-directories.html
		if delimiter != "" && o.PseudoDirectory {
			objs[i] = &obj{o.SubDir, 0, time.Unix(0, 0), true, "", ""}
		} else {
			objs[i] = &obj{o.Name, o.Bytes, o.LastModified, strings.HasSuffix(o.Name, "/"), "", ""}
		}
	}
	return generateListResult(objs, limit)
}

func (s *swiftOSS) Head(ctx context.Context, key string) (Object, error) {
	object, _, err := s.conn.Object(ctx, s.container, key)
	if err != nil {
		if err == swift.ObjectNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key,
		object.Bytes,
		object.LastModified,
		strings.HasSuffix(key, "/"),
		"",
		"",
	}, err
}

// parseSwiftEndpoint returns the container and host of an endpoint in either
// http://<container>.<host> or http://<host>/<container> form. Only the second
// can name a container containing dots or "@".
func parseSwiftEndpoint(uri *url.URL) (container, host string, err error) {
	if c := strings.Trim(uri.Path, "/"); c != "" {
		return c, uri.Host, nil
	}
	hostSlice := strings.SplitN(uri.Host, ".", 2)
	if len(hostSlice) != 2 {
		return "", "", fmt.Errorf("Invalid host: %s", uri.Host)
	}
	return hostSlice[0], hostSlice[1], nil
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

	container, host, err := parseSwiftEndpoint(uri)
	if err != nil {
		return nil, err
	}

	conn := swift.Connection{
		UserName:  username,
		ApiKey:    apiKey,
		AuthToken: token,
		UserAgent: UserAgent,
		Transport: httpClient.Transport.(*http.Transport),
	}
	if err = conn.ApplyEnvironment(); err != nil {
		return nil, fmt.Errorf("Invalid Swift environment: %s", err)
	}
	// The standard OS_* and ST_* variables can select Keystone authentication
	if conn.AuthUrl == "" {
		conn.AuthUrl = uri.Scheme + "://" + host + "/auth/v1.0"
	}
	err = conn.Authenticate(context.Background())
	if err != nil {
		return nil, fmt.Errorf("Auth: %s", err)
	}
	return &swiftOSS{DefaultObjectStorage{}, &conn, conn.Region, conn.StorageUrl, container}, nil
}

func init() {
	Register("swift", newSwiftOSS)
}
