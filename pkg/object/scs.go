//go:build !noscs
// +build !noscs

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
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Arvintian/scs-go-sdk/pkg/client"
	"github.com/Arvintian/scs-go-sdk/scs"
)

type scsClient struct {
	DefaultObjectStorage
	bucket string
	c      *scs.SCS
	b      scs.Bucket
	marker string
}

func (s *scsClient) String() string {
	return fmt.Sprintf("scs://%s/", s.bucket)
}

func (s *scsClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30, // guess
		MaxPartCount:             2048,
	}
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
		if e, ok := err.(*client.Error); ok && e.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	mtime, err := time.Parse(time.RFC1123, om.LastModified)
	if err != nil {
		return nil, err
	}
	return &obj{key: key, size: om.ContentLength, mtime: mtime, isDir: strings.HasSuffix(key, "/")}, nil
}

func (s *scsClient) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
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

func (s *scsClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	return s.b.Put(key, map[string]string{}, in)
}

func (s *scsClient) Delete(key string, getters ...AttrGetter) error {
	return s.b.Delete(key)
}

func (s *scsClient) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if marker != "" {
		if s.marker == "" {
			// last page
			return nil, false, "", nil
		}
		marker = s.marker
	}
	list, err := s.b.List(delimiter, prefix, marker, limit)
	if err != nil {
		s.marker = ""
		return nil, false, "", err
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
	if delimiter != "" {
		for _, p := range list.CommonPrefixes {
			objs = append(objs, &obj{p.Prefix, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return generateListResult(objs, limit)
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

func newSCS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
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
