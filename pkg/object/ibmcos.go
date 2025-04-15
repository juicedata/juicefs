//go:build !noibmcos
// +build !noibmcos

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam"
	"github.com/IBM/ibm-cos-sdk-go/aws/request"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
)

type ibmcos struct {
	bucket string
	s3     *s3.S3
	sc     string
}

func (s *ibmcos) String() string {
	return fmt.Sprintf("ibmcos://%s/", s.bucket)
}

func (s *ibmcos) Create() error {
	input := &s3.CreateBucketInput{Bucket: &s.bucket}
	// https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-classes&code=go
	if s.sc != "" {
		input.CreateBucketConfiguration = &s3.CreateBucketConfiguration{
			LocationConstraint: &s.sc,
		}
	}
	_, err := s.s3.CreateBucket(input)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (s *ibmcos) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (s *ibmcos) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	params := &s3.GetObjectInput{Bucket: &s.bucket, Key: &key}
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("bytes=%d-", off)
		}
		params.Range = &r
	}
	var reqID string
	resp, err := s.s3.GetObjectWithContext(ctx, params, request.WithGetResponseHeader(s3RequestIDKey, &reqID))
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqID)
	if err != nil {
		return nil, err
	}
	if resp.StorageClass != nil {
		attrs.SetStorageClass(*resp.StorageClass)
	}
	return resp.Body, nil
}

func (s *ibmcos) Put(key string, in io.Reader, getters ...AttrGetter) error {
	var body io.ReadSeeker
	if b, ok := in.(io.ReadSeeker); ok {
		body = b
	} else {
		data, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	mimeType := utils.GuessMimeType(key)
	params := &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &mimeType,
	}
	if s.sc != "" {
		params.SetStorageClass(s.sc)
	}
	var reqID string
	_, err := s.s3.PutObjectWithContext(ctx, params, request.WithGetResponseHeader(s3RequestIDKey, &reqID))
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqID).SetStorageClass(s.sc)
	return err
}

func (s *ibmcos) Copy(dst, src string) error {
	src = s.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &s.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	if s.sc != "" {
		params.SetStorageClass(s.sc)
	}
	_, err := s.s3.CopyObject(params)
	return err
}

func (s *ibmcos) Head(key string) (Object, error) {
	param := s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	r, err := s.s3.HeadObject(&param)
	if err != nil {
		if e, ok := err.(awserr.RequestFailure); ok && e.StatusCode() == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
		*r.StorageClass,
	}, nil
}

func (s *ibmcos) Delete(key string, getters ...AttrGetter) error {
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	var reqID string
	_, err := s.s3.DeleteObjectWithContext(ctx, &param, request.WithGetResponseHeader(s3RequestIDKey, &reqID))
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqID)
	return err
}

func (s *ibmcos) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	param := s3.ListObjectsInput{
		Bucket:       &s.bucket,
		Prefix:       &prefix,
		Marker:       &start,
		MaxKeys:      &limit,
		EncodingType: aws.String("url"),
	}
	if delimiter != "" {
		param.Delimiter = &delimiter
	}
	resp, err := s.s3.ListObjects(&param)
	if err != nil {
		return nil, false, "", err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		oKey, err := url.QueryUnescape(*o.Key)
		if err != nil {
			return nil, false, "", errors.WithMessagef(err, "failed to decode key %s", *o.Key)
		}
		objs[i] = &obj{oKey, *o.Size, *o.LastModified, strings.HasSuffix(oKey, "/"), *o.StorageClass}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			prefix, err := url.QueryUnescape(*p.Prefix)
			if err != nil {
				return nil, false, "", errors.WithMessagef(err, "failed to decode commonPrefixes %s", *p.Prefix)
			}
			objs = append(objs, &obj{prefix, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, *resp.IsTruncated, *resp.NextMarker, nil
}

func (s *ibmcos) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *ibmcos) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	params := &s3.CreateMultipartUploadInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	if s.sc != "" {
		params.SetStorageClass(s.sc)
	}
	resp, err := s.s3.CreateMultipartUpload(params)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: *resp.UploadId, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (s *ibmcos) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	n := int64(num)
	params := &s3.UploadPartInput{
		Bucket:     &s.bucket,
		Key:        &key,
		UploadId:   &uploadID,
		Body:       bytes.NewReader(body),
		PartNumber: &n,
	}
	resp, err := s.s3.UploadPart(params)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: *resp.ETag}, nil
}

func (s *ibmcos) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

func (s *ibmcos) AbortUpload(key string, uploadID string) {
	params := &s3.AbortMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadId: &uploadID,
	}
	_, _ = s.s3.AbortMultipartUpload(params)
}

func (s *ibmcos) CompleteUpload(key string, uploadID string, parts []*Part) error {
	var s3Parts []*s3.CompletedPart
	for i := range parts {
		n := new(int64)
		*n = int64(parts[i].Num)
		s3Parts = append(s3Parts, &s3.CompletedPart{ETag: &parts[i].ETag, PartNumber: n})
	}
	params := &s3.CompleteMultipartUploadInput{
		Bucket:          &s.bucket,
		Key:             &key,
		UploadId:        &uploadID,
		MultipartUpload: &s3.CompletedMultipartUpload{Parts: s3Parts},
	}
	_, err := s.s3.CompleteMultipartUpload(params)
	return err
}

func (s *ibmcos) ListUploads(marker string) ([]*PendingPart, string, error) {
	input := &s3.ListMultipartUploadsInput{
		Bucket:    aws.String(s.bucket),
		KeyMarker: aws.String(marker),
	}
	// FIXME: parsing time "2018-08-23T12:23:26.046+08:00" as "2006-01-02T15:04:05Z"
	result, err := s.s3.ListMultipartUploads(input)
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{*u.Key, *u.UploadId, *u.Initiated}
	}
	var nextMarker string
	if result.NextKeyMarker != nil {
		nextMarker = *result.NextKeyMarker
	}
	return parts, nextMarker, nil
}

func (s *ibmcos) SetStorageClass(sc string) error {
	s.sc = sc
	return nil
}

func newIBMCOS(endpoint, apiKey, serviceInstanceID, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, _ := url.ParseRequestURI(endpoint)
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[2]
	authEndpoint := "https://iam.cloud.ibm.com/identity/token"
	serviceEndpoint := "https://" + strings.SplitN(uri.Host, ".", 2)[1]
	conf := aws.NewConfig().
		WithRegion(region).
		WithEndpoint(serviceEndpoint).
		WithCredentials(ibmiam.NewStaticCredentials(aws.NewConfig(),
			authEndpoint, apiKey, serviceInstanceID)).
		WithS3ForcePathStyle(defaultPathStyle())
	sess := session.Must(session.NewSession())
	client := s3.New(sess, conf)
	return &ibmcos{bucket: bucket, s3: client}, nil
}

func init() {
	Register("ibmcos", newIBMCOS)
}
