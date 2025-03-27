//go:build !nooss
// +build !nooss

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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	openapicred "github.com/aliyun/credentials-go/credentials"
)

const ossDefaultRegionID = "cn-hangzhou"

type ossClient struct {
	client *oss.Client
	bucket string
	sc     string
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s/", o.bucket)
}

func (o *ossClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              int(oss.MinPartSize),
		MaxPartSize:              oss.MaxPartSize,
		MaxPartCount:             int(oss.MaxUploadParts),
	}
}

func (o *ossClient) Create() error {
	var configuration *oss.CreateBucketConfiguration
	if o.sc != "" {
		configuration = &oss.CreateBucketConfiguration{
			StorageClass: oss.StorageClassType(o.sc),
		}
	}
	_, err := o.client.PutBucket(ctx, &oss.PutBucketRequest{
		Bucket:                    &o.bucket,
		CreateBucketConfiguration: configuration,
	})
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (o *ossClient) Head(key string) (Object, error) {
	info, err := o.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: &o.bucket,
		Key:    &key,
	})
	if err != nil {
		var svcErr *oss.ServiceError
		if errors.As(err, &svcErr); svcErr.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key,
		info.ContentLength,
		oss.ToTime(info.LastModified),
		strings.HasSuffix(key, "/"),
		oss.ToString(info.StorageClass),
	}, nil
}

func (o *ossClient) Get(key string, off, limit int64, getters ...AttrGetter) (resp io.ReadCloser, err error) {
	var result *oss.GetObjectResult
	var reqId string
	var sc string
	result, err = o.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket:        &o.bucket,
		Key:           &key,
		Range:         oss.HTTPRange{Offset: off, Count: limit}.FormatHTTPRange(),
		RangeBehavior: oss.Ptr("standard"),
	})
	if err != nil {
		var svcErr *oss.ServiceError
		if errors.As(err, &svcErr) {
			reqId = svcErr.RequestID
		}
	} else {
		reqId = result.ResultCommon.Headers.Get(oss.HeaderOssRequestID)
		sc = oss.ToString(result.StorageClass)
		if off > 0 || limit > 0 {
			resp = result.Body
		} else {
			resp = verifyChecksum(result.Body,
				result.Headers.Get(oss.HeaderOssMetaPrefix+checksumAlgr),
				result.ContentLength)
		}
	}

	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqId)
	attrs.SetStorageClass(sc)
	return
}

func (o *ossClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	req := &oss.PutObjectRequest{
		Bucket:       &o.bucket,
		Key:          &key,
		StorageClass: oss.StorageClassType(o.sc),
		Body:         in,
	}
	if ins, ok := in.(io.ReadSeeker); ok {
		req.Metadata = make(map[string]string)
		req.Metadata[oss.HeaderOssMetaPrefix+checksumAlgr] = generateChecksum(ins)
	}
	var reqId string
	result, err := o.client.PutObject(ctx, req)
	if err != nil {
		var svcErr *oss.ServiceError
		if errors.As(err, &svcErr) {
			reqId = svcErr.RequestID
		}
	} else {
		reqId = result.Headers.Get(oss.HeaderOssRequestID)
	}
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqId).SetStorageClass(o.sc)
	return err
}

func (o *ossClient) Copy(dst, src string) error {
	var req = &oss.CopyObjectRequest{
		SourceBucket: &o.bucket,
		Bucket:       &o.bucket,
		SourceKey:    &src,
		Key:          &dst,
		StorageClass: oss.StorageClassType(o.sc),
	}
	_, err := o.client.CopyObject(ctx, req)
	return err
}

func (o *ossClient) Delete(key string, getters ...AttrGetter) error {
	result, err := o.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: &o.bucket,
		Key:    &key,
	})
	var reqId string
	if err != nil {
		var svcErr *oss.ServiceError
		if errors.As(err, &svcErr) {
			reqId = svcErr.RequestID
		}
	} else {
		reqId = result.Headers.Get(oss.HeaderOssRequestID)
	}
	attrs := applyGetters(getters...)
	attrs.SetRequestID(reqId)
	return err
}

func (o *ossClient) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	result, err := o.client.ListObjectsV2(ctx, &oss.ListObjectsV2Request{
		Bucket:            &o.bucket,
		Prefix:            &prefix,
		StartAfter:        &start,
		ContinuationToken: &token,
		Delimiter:         &delimiter,
		MaxKeys:           int32(limit),
	})
	if err != nil {
		return nil, false, "", err
	}
	n := len(result.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := result.Contents[i]
		objs[i] = &obj{oss.ToString(o.Key), o.Size, oss.ToTime(o.LastModified), strings.HasSuffix(oss.ToString(o.Key), "/"), oss.ToString(o.StorageClass)}
	}
	if delimiter != "" {
		for _, o := range result.CommonPrefixes {
			objs = append(objs, &obj{oss.ToString(o.Prefix), 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, result.IsTruncated, oss.ToString(result.NextContinuationToken), nil
}

func (o *ossClient) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (o *ossClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	result, err := o.client.InitiateMultipartUpload(ctx, &oss.InitiateMultipartUploadRequest{
		Bucket:       &o.bucket,
		Key:          &key,
		StorageClass: oss.StorageClassType(o.sc),
	})
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: oss.ToString(result.UploadId), MinPartSize: 4 << 20, MaxCount: 10000}, nil
}

func (o *ossClient) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	r, err := o.client.UploadPart(ctx, &oss.UploadPartRequest{
		Bucket:     &o.bucket,
		UploadId:   &uploadID,
		Key:        &key,
		Body:       bytes.NewReader(data),
		PartNumber: int32(num),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: oss.ToString(r.ETag)}, nil
}

func (o *ossClient) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	partCopy, err := o.client.UploadPartCopy(ctx, &oss.UploadPartCopyRequest{
		SourceBucket: &o.bucket,
		Bucket:       &o.bucket,
		SourceKey:    &srcKey,
		Key:          &key,
		UploadId:     &uploadID,
		PartNumber:   int32(num),
		Range:        oss.HTTPRange{Offset: off, Count: size}.FormatHTTPRange(),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: oss.ToString(partCopy.ETag)}, nil
}

func (o *ossClient) AbortUpload(key string, uploadID string) {
	_, _ = o.client.AbortMultipartUpload(ctx, &oss.AbortMultipartUploadRequest{
		Bucket:   &o.bucket,
		UploadId: &uploadID,
		Key:      &key,
	})
}

func (o *ossClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	oparts := make([]oss.UploadPart, len(parts))
	for i, p := range parts {
		oparts[i].PartNumber = int32(p.Num)
		oparts[i].ETag = &p.ETag
	}
	_, err := o.client.CompleteMultipartUpload(ctx, &oss.CompleteMultipartUploadRequest{
		Bucket:   &o.bucket,
		Key:      &key,
		UploadId: &uploadID,
		CompleteMultipartUpload: &oss.CompleteMultipartUpload{
			Parts: oparts,
		},
	})
	return err
}

func (o *ossClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	result, err := o.client.ListParts(ctx, &oss.ListPartsRequest{
		Bucket: &o.bucket,
		Key:    &marker,
	})
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Parts))
	for i, u := range result.Parts {
		parts[i] = &PendingPart{oss.ToString(result.Key), oss.ToString(result.UploadId), oss.ToTime(u.LastModified)}
	}
	return parts, string(result.NextPartNumberMarker), nil
}

func (o *ossClient) SetStorageClass(sc string) error {
	o.sc = sc
	return nil
}

func autoOSSEndpoint(bucketName string, provider credentials.CredentialsProvider) (string, error) {
	var err error
	regionID := ossDefaultRegionID
	if rid := os.Getenv("ALICLOUD_REGION_ID"); rid != "" {
		regionID = rid
	}
	config := oss.NewConfig()
	config.CredentialsProvider = provider
	config.Region = &regionID
	client := oss.NewClient(config)
	var info *oss.GetBucketInfoResult
	info, err = client.GetBucketInfo(ctx, &oss.GetBucketInfoRequest{
		Bucket: &bucketName,
	})
	if err != nil {
		return "", err
	}
	// try oss internal endpoint
	client2 := oss.NewClient(oss.NewConfig().
		WithEndpoint(oss.ToString(info.BucketInfo.IntranetEndpoint)).
		WithCredentialsProvider(provider).
		WithRegion(regionID))
	if _, err := client2.GetBucketInfo(ctx, &oss.GetBucketInfoRequest{Bucket: &bucketName}); err == nil {
		return "http://" + oss.ToString(info.BucketInfo.IntranetEndpoint), err
	}
	return "https://" + oss.ToString(info.BucketInfo.ExtranetEndpoint), nil
}

func newOSS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]

	var domain string
	if len(hostParts) > 1 {
		domain = uri.Scheme + "://" + hostParts[1]
	}
	// try environment variable
	if accessKey == "" {
		accessKey = os.Getenv("ALICLOUD_ACCESS_KEY_ID")
		secretKey = os.Getenv("ALICLOUD_ACCESS_KEY_SECRET")
		token = os.Getenv("SECURITY_TOKEN")
	}
	var provider credentials.CredentialsProvider
	if accessKey == "" {
		// use default credential chain https://github.com/aliyun/credentials-go?tab=readme-ov-file#credential-provider-chain
		defaultCred, _ := openapicred.NewCredential(nil)
		provider = credentials.CredentialsProviderFunc(func(ctx context.Context) (credentials.Credentials, error) {
			// return the old certificate before its expiration and obtain a new certificate when the old certificate expires
			cred, err := defaultCred.GetCredential()
			if err != nil {
				return credentials.Credentials{}, err
			}
			return credentials.Credentials{
				AccessKeyID:     *cred.AccessKeyId,
				AccessKeySecret: *cred.AccessKeySecret,
				SecurityToken:   *cred.SecurityToken,
			}, nil
		})
	} else {
		provider = credentials.NewStaticCredentialsProvider(accessKey, secretKey, token)
	}

	if domain == "" {
		if domain, err = autoOSSEndpoint(bucketName, provider); err != nil {
			return nil, fmt.Errorf("unable to get endpoint of bucket %s: %s", bucketName, err)
		}
		logger.Debugf("use endpoint %s", domain)
	}
	index := strings.Index(domain, ".")
	if index <= 0 {
		return nil, fmt.Errorf("invalid endpoint: %s", domain)
	}
	_, regionID, found := strings.Cut(domain[:index], "oss-")
	if !found {
		return nil, fmt.Errorf("invalid endpoint: %s", domain)
	}
	regionID, _ = strings.CutSuffix(regionID, "-internal")
	config := oss.LoadDefaultConfig()
	config.Endpoint = oss.Ptr(domain)
	config.Region = oss.Ptr(regionID)
	config.RetryMaxAttempts = oss.Ptr(1)
	config.ConnectTimeout = oss.Ptr(time.Second * 2)
	config.ReadWriteTimeout = oss.Ptr(time.Second * 5)
	config.DisableUploadCRC64Check = oss.Ptr(true)
	config.DisableDownloadCRC64Check = oss.Ptr(true)
	config.UserAgent = &UserAgent
	config.HttpClient = httpClient
	config.CredentialsProvider = provider
	client := oss.NewClient(config)
	o := &ossClient{client: client, bucket: bucketName}
	return o, nil
}

func init() {
	Register("oss", newOSS)
}
