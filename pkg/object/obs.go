//go:build !noobs
// +build !noobs

/*
 * JuiceFS, Copyright 2019 Juicedata, Inc.
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
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/net/http/httpproxy"
)

const obsDefaultRegion = "cn-north-1"

type obsClient struct {
	bucket string
	region string
	c      *obs.ObsClient
}

func (s *obsClient) String() string {
	return fmt.Sprintf("obs://%s/", s.bucket)
}

func (s *obsClient) Create() error {
	params := &obs.CreateBucketInput{}
	params.Bucket = s.bucket
	params.Location = s.region
	params.AvailableZone = "3az"
	_, err := s.c.CreateBucket(params)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (s *obsClient) Head(key string) (Object, error) {
	params := &obs.GetObjectMetadataInput{
		Bucket: s.bucket,
		Key:    key,
	}
	r, err := s.c.GetObjectMetadata(params)
	if err != nil {
		if e, ok := err.(obs.ObsError); ok && e.BaseModel.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
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
	var vlen int64
	var sum []byte
	if b, ok := in.(io.ReadSeeker); ok {
		var err error
		h := md5.New()
		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		vlen, err = io.CopyBuffer(h, in, *buf)
		if err != nil {
			return err
		}
		_, err = b.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		sum = h.Sum(nil)
		body = b
	} else {
		data, err := ioutil.ReadAll(in)
		if err != nil {
			return err
		}
		vlen = int64(len(data))
		s := md5.Sum(data)
		sum = s[:]
		body = bytes.NewReader(data)
	}
	mimeType := utils.GuessMimeType(key)
	params := &obs.PutObjectInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.Body = body
	params.ContentLength = vlen
	params.ContentMD5 = base64.StdEncoding.EncodeToString(sum[:])
	params.ContentType = mimeType
	resp, err := s.c.PutObject(params)
	if err == nil && strings.Trim(resp.ETag, "\"") != obs.Hex(sum) {
		err = fmt.Errorf("unexpected ETag: %s != %s", strings.Trim(resp.ETag, "\""), obs.Hex(sum))
	}
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
	params := obs.DeleteObjectInput{}
	params.Bucket = s.bucket
	params.Key = key
	_, err := s.c.DeleteObject(&params)
	return err
}

func (s *obsClient) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	input := &obs.ListObjectsInput{
		Bucket: s.bucket,
		Marker: marker,
	}
	input.Prefix = prefix
	input.MaxKeys = int(limit)
	input.Delimiter = delimiter
	resp, err := s.c.ListObjects(input)
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		objs[i] = &obj{o.Key, o.Size, o.LastModified, strings.HasSuffix(o.Key, "/")}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			objs = append(objs, &obj{p, 0, time.Unix(0, 0), true})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, nil
}

func (s *obsClient) ListAll(prefix, marker string) (<-chan Object, error) {
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
	params.PartSize = int64(len(body))
	sum := md5.Sum(body)
	params.ContentMD5 = base64.StdEncoding.EncodeToString(sum[:])
	resp, err := s.c.UploadPart(params)
	if err == nil && strings.Trim(resp.ETag, "\"") != obs.Hex(sum[:]) {
		err = fmt.Errorf("unexpected ETag: %s != %s", strings.Trim(resp.ETag, "\""), obs.Hex(sum[:]))
	}
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: resp.ETag}, err
}

func (s *obsClient) AbortUpload(key string, uploadID string) {
	params := &obs.AbortMultipartUploadInput{}
	params.Bucket = s.bucket
	params.Key = key
	params.UploadId = uploadID
	_, _ = s.c.AbortMultipartUpload(params)
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

func autoOBSEndpoint(bucketName, accessKey, secretKey, token string) (string, error) {
	region := obsDefaultRegion
	if r := os.Getenv("HWCLOUD_DEFAULT_REGION"); r != "" {
		region = r
	}
	endpoint := fmt.Sprintf("https://obs.%s.myhuaweicloud.com", region)

	obsCli, err := obs.New(accessKey, secretKey, endpoint, obs.WithSecurityToken(token))
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

func newOBS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %s: %q", endpoint, err)
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
		if endpoint, err = autoOBSEndpoint(bucketName, accessKey, secretKey, token); err != nil {
			return nil, fmt.Errorf("cannot get location of bucket %s: %q", bucketName, err)
		}
		if !strings.HasPrefix(endpoint, "http") {
			endpoint = fmt.Sprintf("%s://%s", uri.Scheme, endpoint)
		}
	} else {
		region = strings.Split(hostParts[1], ".")[1]
	}

	// Use proxy setting from environment variables: HTTP_PROXY, HTTPS_PROXY, NO_PROXY
	if uri, err = url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("invalid endpoint %s: %q", endpoint, err)
	}
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(uri)
	if err != nil {
		return nil, fmt.Errorf("get proxy url for endpoint: %s error: %q", endpoint, err)
	}
	var urlString string
	if proxyURL != nil {
		urlString = proxyURL.String()
	}

	// Empty proxy url string has no effect
	// there is a bug in the retry of PUT (did not call Seek(0,0) before retry), so disable the retry here
	c, err := obs.New(accessKey, secretKey, endpoint, obs.WithSecurityToken(token),
		obs.WithProxyUrl(urlString), obs.WithMaxRetryCount(0))
	if err != nil {
		return nil, fmt.Errorf("fail to initialize OBS: %q", err)
	}
	return &obsClient{bucketName, region, c}, nil
}

func init() {
	Register("obs", newOBS)
}
