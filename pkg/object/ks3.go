//go:build !nos3 && !noks3
// +build !nos3,!noks3

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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
)

const s3StorageClassHdr = "X-Amz-Storage-Class"

// ks3DerefTime returns *p when non-nil, else the zero time. The ks3 SDK does
// not ship a nil-safe pointer helper for time.Time.
func ks3DerefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// ks3DerefBool returns *p when non-nil, else false.
func ks3DerefBool(p *bool) bool {
	return p != nil && *p
}

type ks3 struct {
	bucket string
	s3     *s3.S3
	tierStorage
}

func (s *ks3) String() string {
	return fmt.Sprintf("ks3://%s/", s.bucket)
}

func (s *ks3) Create(ctx context.Context) error {
	_, err := s.s3.CreateBucketWithContext(ctx, &s3.CreateBucketInput{Bucket: &s.bucket})
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

func (s *ks3) Head(ctx context.Context, key string) (Object, error) {
	param := s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}

	r, err := s.s3.HeadObjectWithContext(ctx, &param)
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
		aws.ToLong(r.ContentLength),
		ks3DerefTime(r.LastModified),
		strings.HasSuffix(key, "/"),
		sc,
		aws.ToString(r.Restore),
	}, nil
}

func (s *ks3) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
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
	resp, err := s.s3.GetObjectWithContext(ctx, params)
	if resp != nil {
		attrs := ApplyGetters(getters...)
		attrs.SetRequestID(aws.ToString(resp.Metadata[s3RequestIDKey]))
		attrs.SetStorageClass(aws.ToString(resp.Metadata[s3StorageClassHdr]))
	}
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *ks3) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
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
	t := s.getRuntimeTier(ctx)
	if t.Sc != "" {
		params.StorageClass = aws.String(t.Sc)
	}
	if t.encodedTag != "" {
		params.Tagging = aws.String(t.encodedTag)
	}
	resp, err := s.s3.PutObjectWithContext(ctx, params)
	if resp != nil {
		attrs := ApplyGetters(getters...)
		attrs.SetRequestID(aws.ToString(resp.Metadata[s3RequestIDKey])).SetStorageClass(t.Sc)
	}
	return err
}
func (s *ks3) Copy(ctx context.Context, dst, src string) error {
	t := s.getRuntimeTier(ctx)
	sc := getOrDefaultScValue(t.Sc, s3.StorageClassStandard)
	src = s.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &s.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	params.StorageClass = aws.String(sc)
	if t.encodedTag != "" {
		params.Tagging = aws.String(t.encodedTag)
		params.TaggingDirective = aws.String("REPLACE")
	}
	_, err := s.s3.CopyObjectWithContext(ctx, params)
	return err
}

func (s *ks3) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	resp, err := s.s3.DeleteObjectWithContext(ctx, &param)
	if resp != nil {
		attrs := ApplyGetters(getters...)
		attrs.SetRequestID(aws.ToString(resp.Metadata[s3RequestIDKey]))
	}
	if e, ok := err.(awserr.RequestFailure); ok && e.StatusCode() == http.StatusNotFound {
		return nil
	}
	return err
}

func (s *ks3) List(ctx context.Context, prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
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
	resp, err := s.s3.ListObjectsWithContext(ctx, &param)
	if err != nil {
		return nil, false, "", err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		rawKey := aws.ToString(o.Key)
		oKey, err := decodeKey(rawKey, resp.EncodingType)
		if err != nil {
			return nil, false, "", errors.WithMessagef(err, "failed to decode key %s", rawKey)
		}
		objs[i] = &obj{
			oKey,
			aws.ToLong(o.Size),
			ks3DerefTime(o.LastModified),
			strings.HasSuffix(oKey, "/"),
			aws.ToString(o.StorageClass),
			"",
		}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			rawPrefix := aws.ToString(p.Prefix)
			prefix, err := decodeKey(rawPrefix, resp.EncodingType)
			if err != nil {
				return nil, false, "", errors.WithMessagef(err, "failed to decode commonPrefixes %s", rawPrefix)
			}
			objs = append(objs, &obj{prefix, 0, time.Unix(0, 0), true, "", ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, ks3DerefBool(resp.IsTruncated), aws.ToString(resp.NextMarker), nil
}

func (s *ks3) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *ks3) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	params := &s3.CreateMultipartUploadInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	if s.tiers[0].Sc != "" {
		params.StorageClass = aws.String(s.tiers[0].Sc)
	}
	resp, err := s.s3.CreateMultipartUploadWithContext(ctx, params)
	if err != nil {
		return nil, err
	}
	uploadID := aws.ToString(resp.UploadID)
	if uploadID == "" {
		return nil, fmt.Errorf("ks3: CreateMultipartUpload returned empty UploadID for %s", key)
	}
	return &MultipartUpload{UploadID: uploadID, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (s *ks3) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	n := int64(num)
	params := &s3.UploadPartInput{
		Bucket:     &s.bucket,
		Key:        &key,
		UploadID:   &uploadID,
		Body:       bytes.NewReader(body),
		PartNumber: &n,
	}
	resp, err := s.s3.UploadPartWithContext(ctx, params)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: aws.ToString(resp.ETag)}, nil
}

func (s *ks3) Restore(ctx context.Context, key string, days int32) error {
	_, err := s.s3.RestoreObject(&s3.RestoreObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		RestoreRequest: &s3.RestoreRequest{
			Days: aws.Long(int64(days)),
		},
	})
	return err
}

func (s *ks3) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	resp, err := s.s3.UploadPartCopyWithContext(ctx, &s3.UploadPartCopyInput{
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
	if resp.CopyPartResult == nil {
		return nil, fmt.Errorf("ks3: UploadPartCopy returned no CopyPartResult for %s part %d", key, num)
	}
	return &Part{Num: num, ETag: aws.ToString(resp.CopyPartResult.ETag)}, nil
}

func (s *ks3) AbortUpload(ctx context.Context, key string, uploadID string) {
	params := &s3.AbortMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadID: &uploadID,
	}
	_, _ = s.s3.AbortMultipartUploadWithContext(ctx, params)
}

func (s *ks3) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
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
	_, err := s.s3.CompleteMultipartUploadWithContext(ctx, params)
	return err
}

func (s *ks3) ListUploads(ctx context.Context, marker string) ([]*PendingPart, string, error) {
	input := &s3.ListMultipartUploadsInput{
		Bucket:    aws.String(s.bucket),
		KeyMarker: aws.String(marker),
	}
	// FIXME: parsing time "2018-08-23T12:23:26.046+08:00" as "2006-01-02T15:04:05Z"
	result, err := s.s3.ListMultipartUploadsWithContext(ctx, input)
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{aws.ToString(u.Key), aws.ToString(u.UploadID), ks3DerefTime(u.Initiated)}
	}
	return parts, aws.ToString(result.NextKeyMarker), nil
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
