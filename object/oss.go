// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type ossClient struct {
	client *oss.Client
	bucket *oss.Bucket
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s", o.bucket.BucketName)
}

func (o *ossClient) Create() error {
	// no error if bucket is already created
	return o.bucket.Client.CreateBucket(o.bucket.BucketName)
}

func (o *ossClient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("%d-", off)
		}
		return o.bucket.GetObject(key, oss.NormalizedRange(r))
	}
	return o.bucket.GetObject(key)
}

func (o *ossClient) Put(key string, in io.Reader) error {
	return o.bucket.PutObject(key, in)
}

func (o *ossClient) Copy(dst, src string) error {
	_, err := o.bucket.CopyObject(src, dst)
	return err
}

func (o *ossClient) Exists(key string) error {
	_, err := o.bucket.GetObjectDetailedMeta(key)
	return err
}

func (o *ossClient) Delete(key string) error {
	if err := o.Exists(key); err != nil {
		return err
	}
	return o.bucket.DeleteObject(key)
}

func (o *ossClient) List(prefix, marker string, limit int64) ([]*Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	result, err := o.bucket.ListObjects(oss.Prefix(prefix),
		oss.Marker(marker), oss.MaxKeys(int(limit)))
	if err != nil {
		return nil, err
	}
	n := len(result.Objects)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		o := result.Objects[i]
		mtime := int(o.LastModified.Unix())
		// TODO: ctime
		objs[i] = &Object{o.Key, o.Size, mtime, mtime}
	}
	return objs, nil
}

func (o *ossClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	r, err := o.bucket.InitiateMultipartUpload(key)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: r.UploadID, MinPartSize: 4 << 20, MaxCount: 10000}, nil
}

func (o *ossClient) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	initResult := oss.InitiateMultipartUploadResult{
		Key:      key,
		UploadID: uploadID,
	}
	r, err := o.bucket.UploadPart(initResult, bytes.NewReader(data), int64(len(data)), num)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: r.ETag}, nil
}

func (o *ossClient) AbortUpload(key string, uploadID string) {
	initResult := oss.InitiateMultipartUploadResult{
		Key:      key,
		UploadID: uploadID,
	}
	o.bucket.AbortMultipartUpload(initResult)
}

func (o *ossClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	initResult := oss.InitiateMultipartUploadResult{
		Key:      key,
		UploadID: uploadID,
	}
	oparts := make([]oss.UploadPart, len(parts))
	for i, p := range parts {
		oparts[i].PartNumber = p.Num
		oparts[i].ETag = p.ETag
	}
	_, err := o.bucket.CompleteMultipartUpload(initResult, oparts)
	return err
}

func (o *ossClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	result, err := o.bucket.ListMultipartUploads(oss.KeyMarker(marker))
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{u.Key, u.UploadID, u.Initiated}
	}
	return parts, result.NextKeyMarker, nil
}

func newOSS(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	domain := uri.Scheme + "://" + hostParts[1]

	client, err := oss.New(domain, accessKey, secretKey)
	if err != nil {
		logger.Fatalf("Cannot create OSS client with endpoint %s: %s", endpoint, err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		logger.Fatalf("Cannot create bucket %s: %s", bucketName, err)
	}
	return &ossClient{client: client, bucket: bucket}
}

func init() {
	register("oss", newOSS)
}
