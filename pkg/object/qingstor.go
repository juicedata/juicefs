//go:build !noqingstore
// +build !noqingstore

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
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/qingstor/qingstor-sdk-go/v4/config"
	"github.com/qingstor/qingstor-sdk-go/v4/request/errors"
	qs "github.com/qingstor/qingstor-sdk-go/v4/service"
)

type qingstor struct {
	bucket *qs.Bucket
}

func (q *qingstor) String() string {
	return fmt.Sprintf("qingstor://%s/", *q.bucket.Properties.BucketName)
}

func (q *qingstor) Create() error {
	_, err := q.bucket.Put()
	if err != nil && strings.Contains(err.Error(), "bucket_already_exists") {
		err = nil
	}
	return err
}

func (q *qingstor) Head(key string) (Object, error) {
	r, err := q.bucket.HeadObject(key, nil)
	if err != nil {
		if e, ok := err.(*errors.QingStorError); ok && e.StatusCode == http.StatusNotFound {
			return nil, os.ErrNotExist
		}
	}
	return &obj{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (q *qingstor) Get(key string, off, limit int64) (io.ReadCloser, error) {
	input := &qs.GetObjectInput{}
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("bytes=%d-", off)
		}
		input.Range = &r
	}
	output, err := q.bucket.GetObject(key, input)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func findLen(in io.Reader) (io.Reader, int64, error) {
	var vlen int64
	switch v := in.(type) {
	case *bytes.Buffer:
		vlen = int64(v.Len())
	case *bytes.Reader:
		vlen = int64(v.Len())
	case *strings.Reader:
		vlen = int64(v.Len())
	case *os.File:
		st, err := v.Stat()
		if err != nil {
			return nil, 0, err
		}
		vlen = st.Size()
	case io.ReadSeeker:
		var err error
		vlen, err = v.Seek(0, 2)
		if err != nil {
			return nil, 0, err
		}
		if _, err = v.Seek(0, 0); err != nil {
			return nil, 0, err
		}
	default:
		d, err := ioutil.ReadAll(in)
		if err != nil {
			return nil, 0, err
		}
		vlen = int64(len(d))
		in = bytes.NewReader(d)
	}
	return in, vlen, nil
}

func (q *qingstor) Put(key string, in io.Reader) error {
	body, vlen, err := findLen(in)
	if err != nil {
		return err
	}
	mimeType := utils.GuessMimeType(key)
	input := &qs.PutObjectInput{
		Body:          body,
		ContentLength: &vlen,
		ContentType:   &mimeType,
	}
	out, err := q.bucket.PutObject(key, input)
	if err != nil {
		return err
	}
	if *out.StatusCode != 201 {
		return fmt.Errorf("unexpected code: %d", *out.StatusCode)
	}
	return nil
}

func (q *qingstor) Copy(dst, src string) error {
	source := fmt.Sprintf("/%s/%s", *q.bucket.Properties.BucketName, src)
	input := &qs.PutObjectInput{
		XQSCopySource: &source,
	}
	out, err := q.bucket.PutObject(dst, input)
	if err != nil {
		return err
	}
	if *out.StatusCode != 201 {
		return fmt.Errorf("unexpected code: %d", *out.StatusCode)
	}
	return nil
}

func (q *qingstor) Delete(key string) error {
	_, err := q.bucket.DeleteObject(key)
	return err
}

func (q *qingstor) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	limit_ := int(limit)
	input := &qs.ListObjectsInput{
		Prefix:    &prefix,
		Marker:    &marker,
		Limit:     &limit_,
		Delimiter: &delimiter,
	}
	out, err := q.bucket.ListObjects(input)
	if err != nil {
		return nil, err
	}
	n := len(out.Keys)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		k := out.Keys[i]
		objs[i] = &obj{
			*k.Key,
			*k.Size,
			time.Unix(int64(*k.Modified), 0),
			strings.HasSuffix(*k.Key, "/"),
		}
	}
	if delimiter != "" {
		for _, p := range out.CommonPrefixes {
			objs = append(objs, &obj{*p, 0, time.Unix(0, 0), true})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, nil
}

func (q *qingstor) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (q *qingstor) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	r, err := q.bucket.InitiateMultipartUpload(key, nil)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: *r.UploadID, MinPartSize: 4 << 20, MaxCount: 10000}, nil
}

func (q *qingstor) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	input := &qs.UploadMultipartInput{
		UploadID:   &uploadID,
		PartNumber: &num,
		Body:       bytes.NewReader(data),
	}
	r, err := q.bucket.UploadMultipart(key, input)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, Size: len(data), ETag: strings.Trim(*r.ETag, "\"")}, nil
}

func (q *qingstor) AbortUpload(key string, uploadID string) {
	input := &qs.AbortMultipartUploadInput{
		UploadID: &uploadID,
	}
	_, _ = q.bucket.AbortMultipartUpload(key, input)
}

func (q *qingstor) CompleteUpload(key string, uploadID string, parts []*Part) error {
	oparts := make([]*qs.ObjectPartType, len(parts))
	for i := range parts {
		oparts[i] = &qs.ObjectPartType{
			PartNumber: &parts[i].Num,
			Etag:       &parts[i].ETag,
		}
	}
	input := &qs.CompleteMultipartUploadInput{
		UploadID:    &uploadID,
		ObjectParts: oparts,
	}
	_, err := q.bucket.CompleteMultipartUpload(key, input)
	return err
}

func (q *qingstor) ListUploads(marker string) ([]*PendingPart, string, error) {
	input := &qs.ListMultipartUploadsInput{
		KeyMarker: &marker,
	}
	result, err := q.bucket.ListMultipartUploads(input)
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{*u.Key, *u.UploadID, *u.Created}
	}
	var nextMarker string
	if result.NextKeyMarker != nil {
		nextMarker = *result.NextKeyMarker
	}
	return parts, nextMarker, nil
}

func newQingStor(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	var bucketName, zone, host string
	if !strings.HasSuffix(uri.Host, "qingstor.com") {
		// support private cloud
		hostParts := strings.SplitN(uri.Host, ".", 2)
		bucketName, zone, host = hostParts[0], "", hostParts[1]
	} else {
		hostParts := strings.SplitN(uri.Host, ".", 3)
		bucketName, zone, host = hostParts[0], hostParts[1], hostParts[2]
	}
	conf, err := config.New(accessKey, secretKey)
	if err != nil {
		return nil, fmt.Errorf("Can't load config: %s", err.Error())
	}
	conf.Host = host
	conf.Protocol = uri.Scheme
	if uri.Scheme == "http" {
		conf.Port = 80
	} else {
		conf.Port = 443
	}
	conf.Connection = httpClient
	qsService, _ := qs.Init(conf)
	bucket, _ := qsService.Bucket(bucketName, zone)
	return &qingstor{bucket: bucket}, nil
}

func init() {
	Register("qingstor", newQingStor)
}
