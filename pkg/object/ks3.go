//go:build !nos3
// +build !nos3

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
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"

	aws2 "github.com/aws/aws-sdk-go/aws"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
)

const s3StorageClassHdr = "X-Amz-Storage-Class"

type ks3 struct {
	bucket string
	s3     *s3.S3
	sc     string
}

func (s *ks3) String() string {
	return fmt.Sprintf("ks3://%s/", s.bucket)
}

func (s *ks3) Create() error {
	_, err := s.s3.CreateBucket(&s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (s *ks3) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (s *ks3) Head(key string) (Object, error) {
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

	var sc string
	if val, ok := r.Metadata[s3StorageClassHdr]; ok {
		sc = *val
	} else {
		sc = "STANDARD"
	}
	return &obj{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
		sc,
	}, nil
}

func (s *ks3) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
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
	resp, err := s.s3.GetObject(params)
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(aws2.StringValue(resp.Metadata[s3RequestIDKey]))
		attrs.SetStorageClass(aws2.StringValue(resp.Metadata[s3StorageClassHdr]))
	}
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *ks3) Put(key string, in io.Reader, getters ...AttrGetter) error {
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
		params.StorageClass = aws.String(s.sc)
	}
	resp, err := s.s3.PutObject(params)
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(aws2.StringValue(resp.Metadata[s3RequestIDKey])).SetStorageClass(s.sc)
	}
	return err
}
func (s *ks3) Copy(dst, src string) error {
	src = s.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &s.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	if s.sc != "" {
		params.StorageClass = aws.String(s.sc)
	}
	_, err := s.s3.CopyObject(params)
	return err
}

func (s *ks3) Delete(key string, getters ...AttrGetter) error {
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	resp, err := s.s3.DeleteObject(&param)
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(aws2.StringValue(resp.Metadata[s3RequestIDKey]))
	}
	if e, ok := err.(awserr.RequestFailure); ok && e.StatusCode() == http.StatusNotFound {
		return nil
	}
	return err
}

func (s *ks3) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
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
	var nextMarker string
	if resp.NextMarker != nil {
		nextMarker = *resp.NextMarker
	}
	return objs, *resp.IsTruncated, nextMarker, nil
}

func (s *ks3) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *ks3) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	params := &s3.CreateMultipartUploadInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	if s.sc != "" {
		params.StorageClass = aws.String(s.sc)
	}
	resp, err := s.s3.CreateMultipartUpload(params)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: *resp.UploadID, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (s *ks3) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	n := int64(num)
	params := &s3.UploadPartInput{
		Bucket:     &s.bucket,
		Key:        &key,
		UploadID:   &uploadID,
		Body:       bytes.NewReader(body),
		PartNumber: &n,
	}
	resp, err := s.s3.UploadPart(params)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: *resp.ETag}, nil
}

func (s *ks3) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	resp, err := s.s3.UploadPartCopy(&s3.UploadPartCopyInput{
		Bucket:          aws.String(s.bucket),
		CopySource:      aws.String(s.bucket + "/" + srcKey),
		CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", off, off+size-1)),
		Key:             aws.String(key),
		PartNumber:      aws.Long(int64(num)),
		UploadID:        aws.String(uploadID),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: *resp.CopyPartResult.ETag}, nil
}

func (s *ks3) AbortUpload(key string, uploadID string) {
	params := &s3.AbortMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadID: &uploadID,
	}
	_, _ = s.s3.AbortMultipartUpload(params)
}

func (s *ks3) CompleteUpload(key string, uploadID string, parts []*Part) error {
	var s3Parts []*s3.CompletedPart
	for i := range parts {
		n := new(int64)
		*n = int64(parts[i].Num)
		s3Parts = append(s3Parts, &s3.CompletedPart{ETag: &parts[i].ETag, PartNumber: n})
	}
	params := &s3.CompleteMultipartUploadInput{
		Bucket:          &s.bucket,
		Key:             &key,
		UploadID:        &uploadID,
		MultipartUpload: &s3.CompletedMultipartUpload{Parts: s3Parts},
	}
	_, err := s.s3.CompleteMultipartUpload(params)
	return err
}

func (s *ks3) ListUploads(marker string) ([]*PendingPart, string, error) {
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
		parts[i] = &PendingPart{*u.Key, *u.UploadID, *u.Initiated}
	}
	var nextMarker string
	if result.NextKeyMarker != nil {
		nextMarker = *result.NextKeyMarker
	}
	return parts, nextMarker, nil
}

func (s *ks3) SetStorageClass(sc string) error {
	s.sc = sc
	return nil
}

var ks3Regions = map[string]string{
	"cn-beijing":   "BEIJING",
	"cn-shanghai":  "SHANGHAI",
	"cn-guangzhou": "GUANGZHOU",
	"cn-qingdao":   "QINGDAO",
	"jr-beijing":   "JR_BEIJING",
	"jr-shanghai":  "JR_SHANGHAI",
	"":             "HANGZHOU",
	"cn-hk-1":      "HONGKONG",
	"rus":          "RUSSIA",
	"sgp":          "SINGAPORE",
}

func newKS3(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, _ := url.ParseRequestURI(endpoint)
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	if len(hostParts) < 2 {
		return nil, fmt.Errorf("invalid endpoint: %s", endpoint)
	}
	bucket := hostParts[0]
	region := hostParts[1][3:]
	region = strings.TrimLeft(region, "-")
	var pathStyle bool = defaultPathStyle()
	if strings.HasSuffix(uri.Host, "ksyun.com") || strings.HasSuffix(uri.Host, "ksyuncs.com") {
		region = strings.TrimSuffix(region, "-internal")
		region = ks3Regions[region]
		pathStyle = false
	} else if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
		region = envRegion
	}
	if region == "" {
		region = "us-east-1"
	}

	var err error
	accessKey, err = url.PathUnescape(accessKey)
	if err != nil {
		return nil, fmt.Errorf("unescape access key: %s", err)
	}
	secretKey, err = url.PathUnescape(secretKey)
	if err != nil {
		return nil, fmt.Errorf("unescape secret key: %s", err)
	}
	awsConfig := &aws.Config{
		Region:           region,
		Endpoint:         strings.SplitN(uri.Host, ".", 2)[1],
		DisableSSL:       !ssl,
		HTTPClient:       httpClient,
		S3ForcePathStyle: pathStyle,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, token),
	}

	return &ks3{bucket: bucket, s3: s3.New(awsConfig)}, nil
}

func init() {
	Register("ks3", newKS3)
}
