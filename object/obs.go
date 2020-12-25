// Copyright (C) 2019-present Juicedata Inc.

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
)

const obsDefaultRegion = "cn-north-1"

type obsClient struct {
	bucket string
	region string
	c      *obs.ObsClient
}

func (s *obsClient) String() string {
	return fmt.Sprintf("obs://%s", s.bucket)
}

func (s *obsClient) Create() error {
	params := &obs.CreateBucketInput{}
	params.Bucket = s.bucket
	params.Location = s.region
	_, err := s.c.CreateBucket(params)
	if err != nil {
		if obsError, ok := err.(obs.ObsError); ok && obsError.Code == "BucketAlreadyOwnedByYou" {
			err = nil
		}
	}
	return err
}

func (s *obsClient) Head(key string) (*Object, error) {
	params := &obs.GetObjectMetadataInput{
		Bucket: s.bucket,
		Key:    key,
	}
	r, err := s.c.GetObjectMetadata(params)
	if err != nil {
		return nil, err
	}
	return &Object{
		key,
		r.ContentLength,
		r.LastModified,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *obsClient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	params := &obs.GetObjectInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.RangeStart = off
	if limit > 0 {
		params.RangeEnd = off + limit - 1
	}
	resp, err := s.c.GetObject(params)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *obsClient) Put(key string, in io.Reader) error {
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
	params := &obs.PutObjectInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.Body = body

	_, err := s.c.PutObject(params)
	return err
}

func (s *obsClient) Copy(dst, src string) error {
	params := &obs.CopyObjectInput{}
	params.Bucket = s.bucket
	params.Key = dst
	params.CopySourceBucket = s.bucket
	params.CopySourceKey = src
	_, err := s.c.CopyObject(params)
	return err
}

func (s *obsClient) Delete(key string) error {
	if _, err := s.Head(key); err != nil {
		return err
	}
	params := obs.DeleteObjectInput{}
	params.Bucket = s.bucket
	params.Key = key
	_, err := s.c.DeleteObject(&params)
	return err
}

func (s *obsClient) List(prefix, marker string, limit int64) ([]*Object, error) {
	input := &obs.ListObjectsInput{
		Bucket: s.bucket,
		Marker: marker,
	}
	input.Prefix = prefix
	input.MaxKeys = int(limit)
	resp, err := s.c.ListObjects(input)
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		objs[i] = &Object{o.Key, o.Size, o.LastModified, strings.HasSuffix(o.Key, "/")}
	}
	return objs, nil
}

func (s *obsClient) ListAll(prefix, marker string) (<-chan *Object, error) {
	return nil, notSupported
}

func (s *obsClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	params := &obs.InitiateMultipartUploadInput{}
	params.Bucket = s.bucket
	params.Key = key
	resp, err := s.c.InitiateMultipartUpload(params)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: resp.UploadId, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (s *obsClient) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	params := &obs.UploadPartInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.UploadId = uploadID
	params.Body = bytes.NewReader(body)
	params.PartNumber = num
	resp, err := s.c.UploadPart(params)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: resp.ETag}, nil
}

func (s *obsClient) AbortUpload(key string, uploadID string) {
	params := &obs.AbortMultipartUploadInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.UploadId = uploadID
	s.c.AbortMultipartUpload(params)
}

func (s *obsClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	params := &obs.CompleteMultipartUploadInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.UploadId = uploadID
	for i := range parts {
		params.Parts = append(params.Parts, obs.Part{ETag: parts[i].ETag, PartNumber: parts[i].Num})
	}
	_, err := s.c.CompleteMultipartUpload(params)
	return err
}

func (s *obsClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	input := &obs.ListMultipartUploadsInput{
		Bucket:    s.bucket,
		KeyMarker: marker,
	}

	result, err := s.c.ListMultipartUploads(input)
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{u.Key, u.UploadId, u.Initiated}
	}
	var nextMarker string
	if result.NextKeyMarker != "" {
		nextMarker = result.NextKeyMarker
	}
	return parts, nextMarker, nil
}

func autoOBSEndpoint(bucketName, accessKey, secretKey string) (string, error) {
	region := obsDefaultRegion
	if r := os.Getenv("HWCLOUD_DEFAULT_REGION"); r != "" {
		region = r
	}
	endpoint := fmt.Sprintf("https://obs.%s.myhuaweicloud.com", region)

	obsCli, err := obs.New(accessKey, secretKey, endpoint)
	if err != nil {
		return "", err
	}
	defer obsCli.Close()

	result, err := obsCli.ListBuckets(&obs.ListBucketsInput{QueryLocation: true})
	if err != nil {
		return "", err
	}
	for _, bucket := range result.Buckets {
		if bucket.Name == bucketName {
			logger.Debugf("Get location of bucket %q: %s", bucketName, bucket.Location)
			return fmt.Sprintf("obs.%s.myhuaweicloud.com", bucket.Location), nil
		}
	}
	return "", fmt.Errorf("bucket %q does not exist", bucketName)
}

func newOBS(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint %s: %s", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	if len(hostParts) > 1 {
		endpoint = fmt.Sprintf("%s://%s", uri.Scheme, hostParts[1])
	}

	if accessKey == "" {
		accessKey = os.Getenv("HWCLOUD_ACCESS_KEY")
		secretKey = os.Getenv("HWCLOUD_SECRET_KEY")
	}

	var region string
	if len(hostParts) == 1 {
		if endpoint, err = autoOBSEndpoint(bucketName, accessKey, secretKey); err != nil {
			logger.Fatalf("Cannot get location of bucket %q: %s", bucketName, err)
		}
		if !strings.HasPrefix(endpoint, "http") {
			endpoint = fmt.Sprintf("%s://%s", uri.Scheme, endpoint)
		}
	} else {
		region = strings.Split(hostParts[1], ".")[1]
	}

	c, err := obs.New(accessKey, secretKey, endpoint)
	if err != nil {
		logger.Fatalf("Fail to initialize OBS: %s", err)
	}
	return &obsClient{bucketName, region, c}
}

func init() {
	register("obs", newOBS)
}
