//go:build !nob2
// +build !nob2

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
	"strings"
	"time"

	"gopkg.in/kothar/go-backblaze.v0"
)

type b2client struct {
	DefaultObjectStorage
	bucket *backblaze.Bucket
}

func (c *b2client) String() string {
	return fmt.Sprintf("b2://%s/", c.bucket.Name)
}

func (c *b2client) Create() error {
	return nil
}

func (c *b2client) getFileInfo(key string) (*backblaze.File, error) {
	var f *backblaze.File
	var r io.ReadCloser
	var err error
	f, r, err = c.bucket.DownloadFileRangeByName(key, &backblaze.FileRange{Start: 0, End: 1})
	if err != nil {
		//	get empty file info
		if e, ok := err.(*backblaze.B2Error); ok && e.Status == http.StatusRequestedRangeNotSatisfiable {
			f, r, err = c.bucket.DownloadFileRangeByName(key, nil)
		}
	}
	if err != nil {
		return nil, err
	}
	var buf [2]byte
	_, _ = r.Read(buf[:])
	_ = r.Close()
	return f, nil
}

func (c *b2client) Head(key string) (Object, error) {
	f, err := c.getFileInfo(key)
	if err != nil {
		if e, ok := err.(*backblaze.B2Error); ok && e.Status == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		f.Name,
		f.ContentLength,
		time.Unix(f.UploadTimestamp/1000, 0),
		strings.HasSuffix(f.Name, "/"),
		"",
	}, nil
}

func (c *b2client) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	if off == 0 && limit == -1 {
		_, r, err := c.bucket.DownloadFileByName(key)
		return r, err
	}
	if limit == -1 {
		limit = 1 << 50
	}
	rang := &backblaze.FileRange{Start: off, End: off + limit - 1}
	_, r, err := c.bucket.DownloadFileRangeByName(key, rang)
	return r, err
}

func (c *b2client) Put(key string, data io.Reader, getters ...AttrGetter) error {
	_, err := c.bucket.UploadFile(key, nil, data)
	return err
}

func (c *b2client) Copy(dst, src string) error {
	f, err := c.getFileInfo(src)
	if err != nil {
		return err
	}
	// destinationBucketId must be set,otherwise it will return 400 Bad destinationBucketId
	_, err = c.bucket.CopyFile(f.ID, dst, c.bucket.ID, backblaze.FileMetaDirectiveCopy)
	return err
}

func (c *b2client) Delete(key string, getters ...AttrGetter) error {
	f, err := c.getFileInfo(key)
	if err != nil {
		if strings.HasPrefix(err.Error(), "not_found") {
			return nil
		}
		return err
	}
	_, err = c.bucket.DeleteFileVersion(key, f.ID)
	return err
}

func (c *b2client) List(prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}

	resp, err := c.bucket.ListFileNamesWithPrefix(startAfter, int(limit), prefix, delimiter)
	if err != nil {
		return nil, false, "", err
	}

	n := len(resp.Files)
	objs := make([]Object, 0, n)
	for i := 0; i < n; i++ {
		if resp.Files[i].Name <= startAfter {
			continue
		}
		f := resp.Files[i]
		objs = append(objs, &obj{
			f.Name,
			f.ContentLength,
			time.Unix(f.UploadTimestamp/1000, 0),
			strings.HasSuffix(f.Name, "/"),
			"",
		})
	}
	return objs, resp.NextFileName != "", resp.NextFileName, nil
}

// TODO: support multipart upload using S3 client

func newB2(endpoint, keyID, applicationKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.Split(uri.Host, ".")
	name := hostParts[0]
	client, err := backblaze.NewB2(backblaze.Credentials{
		KeyID:          keyID,
		ApplicationKey: applicationKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create B2 client: %s", err)
	}
	client.MaxIdleUploads = 20
	bucket, err := client.Bucket(name)
	if err != nil {
		logger.Warnf("access bucket %s: %s", name, err)
	}
	if err == nil && bucket == nil {
		bucket, err = client.CreateBucket(name, "allPrivate")
		if err != nil {
			return nil, fmt.Errorf("create bucket %s: %s", name, err)
		}
	}
	if bucket == nil {
		return nil, fmt.Errorf("can't find bucket %s with provided Key ID", name)
	}
	return &b2client{bucket: bucket}, nil
}

func init() {
	Register("b2", newB2)
}
