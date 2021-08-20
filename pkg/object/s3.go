// +build !nos3

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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const awsDefaultRegion = "us-east-1"

var disableSha256Func = func(r *request.Request) {
	if op := r.Operation.Name; r.ClientInfo.ServiceID != "S3" || !(op == "PutObject" || op == "UploadPart") {
		return
	}
	if len(r.HTTPRequest.Header.Get("X-Amz-Content-Sha256")) != 0 {
		return
	}
	r.HTTPRequest.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
}

type s3client struct {
	bucket string
	s3     *s3.S3
	ses    *session.Session
}

func (s *s3client) String() string {
	return fmt.Sprintf("s3://%s/", s.bucket)
}

func isExists(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, s3.ErrCodeBucketAlreadyExists) || strings.Contains(msg, s3.ErrCodeBucketAlreadyOwnedByYou)
}

func (s *s3client) Create() error {
	if _, err := s.List("", "", 1); err == nil {
		return nil
	}
	_, err := s.s3.CreateBucket(&s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (s *s3client) Head(key string) (Object, error) {
	param := s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	r, err := s.s3.HeadObject(&param)
	if err != nil {
		return nil, err
	}
	return &obj{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *s3client) Get(key string, off, limit int64) (io.ReadCloser, error) {
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
	if off == 0 && limit == -1 {
		cs := resp.Metadata[checksumAlgr]
		if cs != nil {
			resp.Body = verifyChecksum(resp.Body, *cs)
		}
	}
	return resp.Body, nil
}

func (s *s3client) Put(key string, in io.Reader) error {
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
	checksum := generateChecksum(body)
	params := &s3.PutObjectInput{
		Bucket:   &s.bucket,
		Key:      &key,
		Body:     body,
		Metadata: map[string]*string{checksumAlgr: &checksum},
	}
	_, err := s.s3.PutObject(params)
	return err
}

func (s *s3client) Copy(dst, src string) error {
	src = s.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &s.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	_, err := s.s3.CopyObject(params)
	return err
}

func (s *s3client) Delete(key string) error {
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	_, err := s.s3.DeleteObject(&param)
	return err
}

func (s *s3client) List(prefix, marker string, limit int64) ([]Object, error) {
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
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		objs[i] = &obj{
			*o.Key,
			*o.Size,
			*o.LastModified,
			strings.HasSuffix(*o.Key, "/"),
		}
	}
	return objs, nil
}

func (s *s3client) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (s *s3client) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	params := &s3.CreateMultipartUploadInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	resp, err := s.s3.CreateMultipartUpload(params)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: *resp.UploadId, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (s *s3client) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
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

func (s *s3client) AbortUpload(key string, uploadID string) {
	params := &s3.AbortMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadId: &uploadID,
	}
	_, _ = s.s3.AbortMultipartUpload(params)
}

func (s *s3client) CompleteUpload(key string, uploadID string, parts []*Part) error {
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

func (s *s3client) ListUploads(marker string) ([]*PendingPart, string, error) {
	input := &s3.ListMultipartUploadsInput{
		Bucket:    aws.String(s.bucket),
		KeyMarker: aws.String(marker),
	}

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

func autoS3Region(bucketName, accessKey, secretKey string) (string, error) {
	awsConfig := &aws.Config{
		HTTPClient: httpClient,
	}
	if accessKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, "")
	}

	var regions []string
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		regions = []string{r}
	} else {
		regions = []string{awsDefaultRegion, "cn-north-1"}
	}

	var (
		err     error
		ses     *session.Session
		service *s3.S3
		result  *s3.GetBucketLocationOutput
	)
	for _, r := range regions {
		// try to get bucket location
		awsConfig.Region = aws.String(r)
		ses, err = session.NewSession(awsConfig)
		if err != nil {
			return "", fmt.Errorf("fail to create aws session: %s", err)
		}
		ses.Handlers.Build.PushFront(disableSha256Func)
		service = s3.New(ses)
		result, err = service.GetBucketLocation(&s3.GetBucketLocationInput{
			Bucket: aws.String(bucketName),
		})
		if err == nil {
			logger.Debugf("Get location of bucket %q from region %q endpoint success: %s",
				bucketName, r, *result.LocationConstraint)
			return *result.LocationConstraint, nil
		}
		if err1, ok := err.(awserr.Error); ok {
			// continue to try other regions if the credentials are invalid, otherwise stop trying.
			if errCode := err1.Code(); errCode != "InvalidAccessKeyId" && errCode != "InvalidToken" {
				return "", err
			}
		}
		logger.Debugf("Fail to get location of bucket %q from region %q endpoint: %s", bucketName, r, err)
	}
	return "", err
}

func parseRegion(endpoint string) string {
	if strings.HasPrefix(endpoint, "s3-") || strings.HasPrefix(endpoint, "s3.") {
		endpoint = endpoint[3:]
	}
	if strings.HasPrefix(endpoint, "dualstack") {
		endpoint = endpoint[len("dualstack."):]
	}
	if endpoint == "amazonaws.com" {
		endpoint = awsDefaultRegion + "." + endpoint
	}
	region := strings.Split(endpoint, ".")[0]
	if region == "external-1" {
		region = awsDefaultRegion
	}
	return region
}

func newS3(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	endpoint = strings.Trim(endpoint, "/")
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err.Error())
	}

	var (
		bucketName string
		region     string
		ep         string
	)

	if uri.Path != "" {
		// [ENDPOINT]/[BUCKET]
		pathParts := strings.Split(uri.Path, "/")
		bucketName = pathParts[1]
		if strings.Contains(uri.Host, ".amazonaws.com") {
			// standard s3
			// s3-[REGION].[REST_OF_ENDPOINT]/[BUCKET]
			// s3.[REGION].amazonaws.com[.cn]/[BUCKET]
			endpoint = uri.Host
			region = parseRegion(endpoint)
		} else {
			// compatible s3
			ep = uri.Host
			region = awsDefaultRegion
		}
	} else {
		// [BUCKET].[ENDPOINT]
		hostParts := strings.SplitN(uri.Host, ".", 2)
		if len(hostParts) == 1 {
			// take endpoint as bucketname
			bucketName = hostParts[0]
			if region, err = autoS3Region(bucketName, accessKey, secretKey); err != nil {
				return nil, fmt.Errorf("Can't guess your region for bucket %s: %s", bucketName, err)
			}
		} else {
			// get region or endpoint
			if strings.Contains(uri.Host, ".amazonaws.com") {
				// standard s3
				// [BUCKET].s3-[REGION].[REST_OF_ENDPOINT]
				// [BUCKET].s3.[REGION].amazonaws.com[.cn]
				hostParts = strings.SplitN(uri.Host, ".s3", 2)
				bucketName = hostParts[0]
				endpoint = "s3" + hostParts[1]
				region = parseRegion(endpoint)
			} else {
				// compatible s3
				bucketName = hostParts[0]
				ep = hostParts[1]
				region = awsDefaultRegion
			}
		}
	}

	ssl := strings.ToLower(uri.Scheme) == "https"
	awsConfig := &aws.Config{
		Region:     aws.String(region),
		DisableSSL: aws.Bool(!ssl),
		HTTPClient: httpClient,
	}
	if accessKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, "")
	}
	if ep != "" {
		awsConfig.Endpoint = aws.String(ep)
		awsConfig.S3ForcePathStyle = aws.Bool(true)
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("Fail to create aws session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &s3client{bucketName, s3.New(ses), ses}, nil
}

func init() {
	Register("s3", newS3)
}
