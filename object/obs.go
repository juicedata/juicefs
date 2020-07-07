package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
)

type obsClient struct {
	bucket string
	c      *obs.ObsClient
}

func (s *obsClient) String() string {
	return fmt.Sprintf("obs://%s", s.bucket)
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

func (s *obsClient) Exists(key string) error {
	params := &obs.GetObjectMetadataInput{}
	params.Bucket = s.bucket
	params.Key = key
	_, err := s.c.GetObjectMetadata(params)
	return err
}

func (s *obsClient) Delete(key string) error {
	if err := s.Exists(key); err != nil {
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
		objs[i] = &Object{o.Key, o.Size, o.LastModified}
	}
	return objs, nil
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

func newObs(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, _ := url.ParseRequestURI(endpoint)
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucket := hostParts[0]
	endpoint = fmt.Sprintf("%s://%s", uri.Scheme, hostParts[1])
	c, err := obs.New(accessKey, secretKey, endpoint)
	if err != nil {
		logger.Fatalf(err.Error())
	}
	return &obsClient{bucket, c}
}

func init() {
	register("obs", newObs)
}
