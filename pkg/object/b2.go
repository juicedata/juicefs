/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"gopkg.in/kothar/go-backblaze.v0"
)

type b2client struct {
	DefaultObjectStorage
	bucket     *backblaze.Bucket
	nextMarker string
}

func (c *b2client) String() string {
	return fmt.Sprintf("b2://%s/", c.bucket.Name)
}

func (c *b2client) Create() error {
	return nil
}

func (c *b2client) getFileInfo(key string) (*backblaze.File, error) {
	f, r, err := c.bucket.DownloadFileRangeByName(key, &backblaze.FileRange{Start: 0, End: 1})
	if err != nil {
		return nil, err
	}
	var buf [2]byte
	_, _ = r.Read(buf[:])
	r.Close()
	return f, nil
}

func (c *b2client) Head(key string) (Object, error) {
	f, err := c.getFileInfo(key)
	if err != nil {
		return nil, err
	}
	return &obj{
		f.Name,
		f.ContentLength,
		time.Unix(f.UploadTimestamp/1000, 0),
		strings.HasSuffix(f.Name, "/"),
	}, nil
}

func (c *b2client) Get(key string, off, limit int64) (io.ReadCloser, error) {
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

func (c *b2client) Put(key string, data io.Reader) error {
	_, err := c.bucket.UploadFile(key, nil, data)
	return err
}

func (c *b2client) Copy(dst, src string) error {
	f, err := c.getFileInfo(src)
	if err != nil {
		return err
	}
	_, err = c.bucket.CopyFile(f.ID, dst, "", backblaze.FileMetaDirectiveCopy)
	return err
}

func (c *b2client) Delete(key string) error {
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

func (c *b2client) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	if marker == "" && c.nextMarker != "" {
		marker = c.nextMarker
		c.nextMarker = ""
	}
	resp, err := c.bucket.ListFileNamesWithPrefix(marker, int(limit), prefix, "")
	if err != nil {
		return nil, err
	}

	n := len(resp.Files)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		f := resp.Files[i]
		objs[i] = &obj{
			f.Name,
			f.ContentLength,
			time.Unix(f.UploadTimestamp/1000, 0),
			strings.HasSuffix(f.Name, "/"),
		}
	}
	c.nextMarker = resp.NextFileName
	return objs, nil
}

// TODO: support multipart upload using S3 client

func newB2(endpoint, keyID, applicationKey string) (ObjectStorage, error) {
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
		bucket, err = client.CreateBucket(name, "allPrivate")
		if err != nil {
			return nil, fmt.Errorf("create bucket %s: %s", name, err)
		}
	}
	return &b2client{bucket: bucket}, nil
}

func init() {
	Register("b2", newB2)
}
