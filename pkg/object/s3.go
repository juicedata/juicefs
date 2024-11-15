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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/juicedata/juicefs/pkg/utils"
)

const awsDefaultRegion = "us-east-1"
const s3RequestIDKey = "X-Amz-Request-Id"

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
	bucket          string
	sc              string
	s3              *s3.S3
	ses             *session.Session
	disableChecksum bool
}

func (s *s3client) String() string {
	return fmt.Sprintf("s3://%s/", s.bucket)
}

func (s *s3client) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func isExists(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, s3.ErrCodeBucketAlreadyExists) || strings.Contains(msg, s3.ErrCodeBucketAlreadyOwnedByYou)
}

func (s *s3client) Create() error {
	if _, _, _, err := s.List("", "", "", "", 1, true); err == nil {
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
		if e, ok := err.(awserr.RequestFailure); ok && e.StatusCode() == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	var sc = DefaultStorageClass
	if r.StorageClass != nil {
		sc = *r.StorageClass
	}
	return &obj{
		key,
		*r.ContentLength,
		*r.LastModified,
		strings.HasSuffix(key, "/"),
		sc,
	}, nil
}

func (s *s3client) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
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
	if off == 0 && limit == -1 {
		cs := resp.Metadata[checksumAlgr]
		var length int64 = -1
		if resp.ContentLength != nil {
			length = *resp.ContentLength
		}
		if cs != nil {
			resp.Body = verifyChecksum(resp.Body, *cs, length)
		}
	}
	if resp.StorageClass != nil {
		attrs.SetStorageClass(*resp.StorageClass)
	}
	return resp.Body, nil
}

func (s *s3client) Put(key string, in io.Reader, getters ...AttrGetter) error {
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
	if !s.disableChecksum {
		checksum := generateChecksum(body)
		params.Metadata = map[string]*string{checksumAlgr: &checksum}
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

func (s *s3client) Copy(dst, src string) error {
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

func (s *s3client) Delete(key string, getters ...AttrGetter) error {
	param := s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}
	var reqID string
	_, err := s.s3.DeleteObjectWithContext(ctx, &param, request.WithGetResponseHeader(s3RequestIDKey, &reqID))
	if err != nil && strings.Contains(err.Error(), "NoSuchKey") {
		err = nil
	}
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqID)
	return err
}

func (s *s3client) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	param := s3.ListObjectsV2Input{
		Bucket:       &s.bucket,
		Prefix:       &prefix,
		MaxKeys:      &limit,
		EncodingType: aws.String("url"),
	}
	if start != "" {
		param.StartAfter = aws.String(start)
	}
	if token != "" {
		param.ContinuationToken = aws.String(token)
	}
	if delimiter != "" {
		param.Delimiter = aws.String(delimiter)
	}
	resp, err := s.s3.ListObjectsV2(&param)
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
		if !strings.HasPrefix(oKey, prefix) || oKey < start {
			return nil, false, "", fmt.Errorf("found invalid key %s from List, prefix: %s, marker: %s", oKey, prefix, start)
		}
		var sc = DefaultStorageClass
		if o.StorageClass != nil {
			sc = *o.StorageClass
		}
		objs[i] = &obj{
			oKey,
			*o.Size,
			*o.LastModified,
			strings.HasSuffix(oKey, "/"),
			sc,
		}
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
	var isTruncated bool
	if resp.IsTruncated != nil {
		isTruncated = *resp.IsTruncated
	}
	var nextMarker string
	if resp.NextContinuationToken != nil {
		nextMarker = *resp.NextContinuationToken
	}
	return objs, isTruncated, nextMarker, nil
}

func (s *s3client) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *s3client) CreateMultipartUpload(key string) (*MultipartUpload, error) {
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

func (s *s3client) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	resp, err := s.s3.UploadPartCopy(&s3.UploadPartCopyInput{
		Bucket:          aws.String(s.bucket),
		CopySource:      aws.String(s.bucket + "/" + srcKey),
		CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", off, off+size-1)),
		Key:             aws.String(key),
		PartNumber:      aws.Int64(int64(num)),
		UploadId:        aws.String(uploadID),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: *resp.CopyPartResult.ETag}, nil
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

func (s *s3client) SetStorageClass(sc string) error {
	s.sc = sc
	return nil
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

func defaultPathStyle() bool {
	v := os.Getenv("JFS_S3_VHOST_STYLE")
	return v == "" || v == "0" || v == "false"
}

var oracleCompileRegexp = `.*\.compat.objectstorage\.(.*)\.oraclecloud\.com`
var OVHCompileRegexp = `^s3\.(\w*)(\.\w*)?\.cloud\.ovh\.net$`

func newS3(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		if len(strings.Split(endpoint, ".")) > 1 && !strings.HasSuffix(endpoint, ".amazonaws.com") {
			endpoint = fmt.Sprintf("http://%s", endpoint)
		} else {
			endpoint = fmt.Sprintf("https://%s", endpoint)
		}
	}
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
				vpcCompile := regexp.MustCompile(`^.*\.(.*)\.vpce\.amazonaws\.com`)
				//vpc link
				if vpcCompile.MatchString(uri.Host) {
					bucketName = hostParts[0]
					ep = hostParts[1]
					if submatch := vpcCompile.FindStringSubmatch(uri.Host); len(submatch) == 2 {
						region = submatch[1]
					}
				} else {
					// standard s3
					// [BUCKET].s3-[REGION].[REST_OF_ENDPOINT]
					// [BUCKET].s3.[REGION].amazonaws.com[.cn]
					hostParts = strings.SplitN(uri.Host, ".s3", 2)
					bucketName = hostParts[0]
					endpoint = "s3" + hostParts[1]
					region = parseRegion(endpoint)
				}
			} else {
				// compatible s3
				bucketName = hostParts[0]
				ep = hostParts[1]

				for _, compileRegexp := range []string{oracleCompileRegexp, OVHCompileRegexp} {
					compile := regexp.MustCompile(compileRegexp)
					if compile.MatchString(ep) {
						if submatch := compile.FindStringSubmatch(ep); len(submatch) >= 2 {
							region = submatch[1]
							break
						}
					}
				}
			}
		}
	}
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = awsDefaultRegion
	}

	ssl := strings.ToLower(uri.Scheme) == "https"
	awsConfig := &aws.Config{
		Region:     &region,
		DisableSSL: aws.Bool(!ssl),
	}

	disable100Continue := strings.EqualFold(uri.Query().Get("disable-100-continue"), "true")
	if disable100Continue {
		logger.Infof("HTTP header 100-Continue is disabled")
		awsConfig.S3Disable100Continue = aws.Bool(true)
	}
	disableMD5 := strings.EqualFold(uri.Query().Get("disable-content-md5"), "true")
	if disableMD5 {
		logger.Infof("HTTP header Content-MD5 is disabled")
		awsConfig.S3DisableContentMD5Validation = &disableMD5
	}
	disableChecksum := strings.EqualFold(uri.Query().Get("disable-checksum"), "true")
	if disableChecksum {
		logger.Infof("CRC checksum is disabled")
	}

	if accessKey == "anonymous" {
		awsConfig.Credentials = credentials.AnonymousCredentials
	} else if accessKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, token)
	}
	if ep != "" {
		awsConfig.Endpoint = aws.String(ep)
		awsConfig.S3ForcePathStyle = aws.Bool(defaultPathStyle())
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("Fail to create aws session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &s3client{bucket: bucketName, s3: s3.New(ses, aws.NewConfig().WithHTTPClient(httpClient)), ses: ses, disableChecksum: disableChecksum}, nil
}

func init() {
	Register("s3", newS3)
}
