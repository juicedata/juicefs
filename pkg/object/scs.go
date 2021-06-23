// +build !noscs

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
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Arvintian/scs-go-sdk/scs"
)

type scsClient struct {
	bucket string
	c      *scs.SCS
	b      scs.Bucket
	marker string
}

func (s *scsClient) String() string {
	return fmt.Sprintf("scs://%s/", s.bucket)
}

func (s *scsClient) Create() error {
	err := s.c.PutBucket(s.bucket, scs.ACLPrivate)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (s *scsClient) Head(key string) (Object, error) {
	om, err := s.b.Head(key)
	if err != nil {
		return nil, err
	}
	mtime, err := time.Parse(time.RFC1123, om.LastModified)
	if err != nil {
		return nil, err
	}
	return &obj{key: key, size: om.ContentLength, mtime: mtime, isDir: strings.HasSuffix(key, "/")}, nil
}

func (s *scsClient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("%d-", off)
		}
		return s.b.Get(key, r)
	}
	return s.b.Get(key, "")
}

func (s *scsClient) Put(key string, in io.Reader) error {
	return s.b.Put(key, map[string]string{}, in)
}

func (s *scsClient) Delete(key string) error {
	return s.b.Delete(key)
}

func (s *scsClient) List(prefix, marker string, limit int64) ([]Object, error) {
	if marker != "" {
		if s.marker == "" {
			// last page
			return nil, nil
		}
		marker = s.marker
	}
	list, err := s.b.List("", prefix, marker, limit)
	if err != nil {
		s.marker = ""
		return nil, err
	}
	s.marker = list.NextMarker
	n := len(list.Contents)
	// Message from scs technical support, the api not guarantee contents is ordered, but marker is work.
	// So we sort contents at here, can work both contents is ordered or not ordered.
	// https://scs.sinacloud.com/doc/scs/api#get_bucket
	sort.Slice(list.Contents, func(i, j int) bool { return list.Contents[i].Name < list.Contents[j].Name })
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		ob := list.Contents[i]
		mtime, _ := time.Parse(time.RFC1123, ob.LastModified)
		objs[i] = &obj{
			key:   ob.Name,
			size:  ob.Size,
			mtime: mtime,
			isDir: strings.HasSuffix(ob.Name, "/"),
		}
	}
	return objs, nil
}

func (s *scsClient) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (s *scsClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	mu, err := s.b.InitiateMultipartUpload(key, map[string]string{})
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{
		MinPartSize: 5 << 20,
		MaxCount:    2048,
		UploadID:    mu.UploadID,
	}, nil
}

func (s *scsClient) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	p, err := s.b.UploadPart(key, uploadID, num, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return &Part{
		Num:  p.PartNumber,
		Size: p.Size,
		ETag: p.ETag,
	}, nil
}

func (s *scsClient) AbortUpload(key string, uploadID string) {}

func (s *scsClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	ps := make([]scs.Part, len(parts))
	for i := 0; i < len(parts); i++ {
		ps[i] = scs.Part{
			PartNumber: parts[i].Num,
			ETag:       parts[i].ETag,
		}
	}
	return s.b.CompleteMultipartUpload(key, uploadID, ps)
}

func (s *scsClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", notSupported
}

func newSCS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	var domain string
	if len(hostParts) > 1 {
		domain = uri.Scheme + "://" + hostParts[1]
	}
	c, err := scs.NewSCS(accessKey, secretKey, domain)
	if err != nil {
		return nil, err
	}
	b, err := c.GetBucket(bucketName)
	if err != nil {
		return nil, err
	}
	return &scsClient{bucket: bucketName, c: c, b: b, marker: ""}, nil
}

func init() {
	Register("scs", newSCS)
}
