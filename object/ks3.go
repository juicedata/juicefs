// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
)

type ks3 struct {
	bucket string
	s3     *s3.S3
	ses    *session.Session
}

func (s *ks3) String() string {
	return fmt.Sprintf("ks3://%s", s.bucket)
}
func (s *ks3) Create() error {
	_, err := s.s3.CreateBucket(&s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case awss3.ErrCodeBucketAlreadyExists:
				err = nil
			case awss3.ErrCodeBucketAlreadyOwnedByYou:
				err = nil
			}
		}
	}
	return err
}

func (s *ks3) Head(key string) (*Object, error) {
	param := s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}

	r, err := s.s3.HeadObject(&param)
	if err != nil {
		return nil, err
	}

	return &Object{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *ks3) Get(key string, off, limit int64) (io.ReadCloser, error) {
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
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *ks3) Put(key string, in io.Reader) error {
	var body io.ReadSeeker
	if b, ok := in.(io.ReadSeeker); ok {
		body = b
	} else {
		data, err := ioutil.ReadAll(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	params := &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
		Body:   body,
	}
	_, err := s.s3.PutObject(params)
	return err
}
func (s *ks3) Copy(dst, src string) error {
	src = s.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &s.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	_, err := s.s3.CopyObject(params)
	return err
}

func (s *ks3) Delete(key string) error {
	if _, err := s.Head(key); err != nil {
		return err
	}
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	_, err := s.s3.DeleteObject(&param)
	return err
}

func (s *ks3) List(prefix, marker string, limit int64) ([]*Object, error) {
	param := s3.ListObjectsInput{
		Bucket:  &s.bucket,
		Prefix:  &prefix,
		Marker:  &marker,
		MaxKeys: &limit,
	}
	resp, err := s.s3.ListObjects(&param)
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		objs[i] = &Object{*o.Key, *o.Size, *o.LastModified, strings.HasSuffix(*o.Key, "/")}
	}
	return objs, nil
}

func (s *ks3) ListAll(prefix, marker string) (<-chan *Object, error) {
	return nil, notSupported
}

func (s *ks3) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	params := &s3.CreateMultipartUploadInput{
		Bucket: &s.bucket,
		Key:    &key,
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

func (s *ks3) AbortUpload(key string, uploadID string) {
	params := &s3.AbortMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadID: &uploadID,
	}
	s.s3.AbortMultipartUpload(params)
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
	return parts, *result.NextKeyMarker, nil
}

var ks3Regions = map[string]string{
	"cn-beijing":  "BEIJING",
	"cn-shanghai": "SHANGHAI",
	"":            "HANGZHOU",
	"cn-hk-1":     "HONGKONG",
	"rus":         "RUSSIA",
	"sgp":         "SINGAPORE",
}

func newKS3(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, _ := url.ParseRequestURI(endpoint)
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[1][3:]
	if strings.HasPrefix(region, "-") {
		region = region[1:]
	}
	if strings.HasSuffix(region, "-internal") {
		region = region[:len(region)-len("-internal")]
	}
	region, _ = ks3Regions[region]

	awsConfig := &aws.Config{
		Region:      region,
		Endpoint:    strings.SplitN(uri.Host, ".", 2)[1],
		DisableSSL:  !ssl,
		HTTPClient:  httpClient,
		Credentials: credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	return &ks3{bucket, s3.New(awsConfig), nil}
}

func init() {
	register("ks3", newKS3)
}
