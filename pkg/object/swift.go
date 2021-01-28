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
	return fmt.Sprintf("swift://%s", s.container)
}

func (s *swiftOSS) Create() error {
	/* Connection.ContainerCreate():
	 *     No error is returned if it already exists but the metadata if any will be updated.
	 */
	err := s.conn.ContainerCreate(s.container, nil)
	return err
}

func (s *swiftOSS) Get(key string, off, limit int64) (io.ReadCloser, error) {
	objOpenFile, _, err := s.conn.ObjectOpen(s.container, key, false, nil)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		_, err := objOpenFile.Seek(off, 0)
		if err != nil {
			objOpenFile.Close()
			return nil, err
		}
	}
	if limit > 0 {
		defer objOpenFile.Close()
		buf := make([]byte, limit)
		if n, err := objOpenFile.Read(buf); err != nil {
			return nil, err
		} else {
			return ioutil.NopCloser(bytes.NewBuffer(buf[:n])), nil
		}
	}
	return objOpenFile, err
}

func (s *swiftOSS) Put(key string, in io.Reader) error {
	_, err := s.conn.ObjectPut(s.container, key, in, false, "", "", nil)
	return err
}

func (s *swiftOSS) Delete(key string) error {
	err := s.conn.ObjectDelete(s.container, key)
	return err
}

func newSwiftOSS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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
		UserName: accessKey,
		ApiKey:   secretKey,
		AuthUrl:  authURL,
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
