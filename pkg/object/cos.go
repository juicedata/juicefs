//go:build !nocos
// +build !nocos

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
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/tencentyun/cos-go-sdk-v5"
)

const (
	cosChecksumKey        = "x-cos-meta-" + checksumAlgr
	cosRequestIDKey       = "X-Cos-Request-Id"
	cosStorageClassHeader = "X-Cos-Storage-Class"
)

type COS struct {
	c        *cos.Client
	endpoint string
	sc       string
}

func (c *COS) String() string {
	return fmt.Sprintf("cos://%s/", strings.Split(c.endpoint, ".")[0])
}

func (c *COS) Create() error {
	_, err := c.c.Bucket.Put(ctx, nil)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (c *COS) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              1 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (c *COS) Head(key string) (Object, error) {
	resp, err := c.c.Object.Head(ctx, key, nil)
	if err != nil {
		if exist, err := c.c.Object.IsExist(ctx, key); err == nil && !exist {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	header := resp.Header
	var size int64
	if val, ok := header["Content-Length"]; ok {
		if length, err := strconv.ParseInt(val[0], 10, 64); err == nil {
			size = length
		}
	}
	var mtime time.Time
	if val, ok := header["Last-Modified"]; ok {
		mtime, _ = time.Parse(time.RFC1123, val[0])
	}
	var sc string
	if val := header.Get(cosStorageClassHeader); val != "" {
		sc = val
	} else {
		// https://cloud.tencent.com/document/product/436/7745
		// This header is returned only if the object is not STANDARD storage class.
		sc = "STANDARD"
	}
	return &obj{key, size, mtime, strings.HasSuffix(key, "/"), sc}, nil
}

func (c *COS) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	params := &cos.ObjectGetOptions{Range: getRange(off, limit)}
	resp, err := c.c.Object.Get(ctx, key, params)
	if err != nil {
		return nil, err
	}
	if err = checkGetStatus(resp.StatusCode, params.Range != ""); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	if off == 0 && limit == -1 {
		length, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
		if err != nil {
			length = -1
			logger.Warnf("failed to parse content-length %s: %s", resp.Header.Get("Content-Length"), err)
		}
		resp.Body = verifyChecksum(resp.Body, resp.Header.Get(cosChecksumKey), length)
	}
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(resp.Header.Get(cosRequestIDKey)).SetStorageClass(resp.Header.Get(cosStorageClassHeader))
	}
	return resp.Body, nil
}

func (c *COS) Put(key string, in io.Reader, getters ...AttrGetter) error {
	var options cos.ObjectPutOptions
	if ins, ok := in.(io.ReadSeeker); ok {
		header := http.Header(map[string][]string{
			cosChecksumKey: {generateChecksum(ins)},
		})
		options.ObjectPutHeaderOptions = &cos.ObjectPutHeaderOptions{XCosMetaXXX: &header}
	}
	if c.sc != "" {
		if options.ObjectPutHeaderOptions == nil {
			options.ObjectPutHeaderOptions = &cos.ObjectPutHeaderOptions{}
		}
		options.ObjectPutHeaderOptions.XCosStorageClass = c.sc
	}
	resp, err := c.c.Object.Put(ctx, key, in, &options)
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(resp.Header.Get(cosRequestIDKey)).SetStorageClass(c.sc)
	}
	return err
}

func (c *COS) Copy(dst, src string) error {
	var opt cos.ObjectCopyOptions
	if c.sc != "" {
		opt.ObjectCopyHeaderOptions = &cos.ObjectCopyHeaderOptions{XCosStorageClass: c.sc}
	}
	source := fmt.Sprintf("%s/%s", c.endpoint, src)
	_, _, err := c.c.Object.Copy(ctx, dst, source, &opt)
	return err
}

func (c *COS) Delete(key string, getters ...AttrGetter) error {
	resp, err := c.c.Object.Delete(ctx, key)
	if resp != nil {
		attrs := applyGetters(getters...)
		attrs.SetRequestID(resp.Header.Get(cosRequestIDKey))
	}
	return err
}

func (c *COS) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	param := cos.BucketGetOptions{
		Prefix:       prefix,
		Marker:       start,
		MaxKeys:      int(limit),
		Delimiter:    delimiter,
		EncodingType: "url",
	}
	resp, _, err := c.c.Bucket.Get(ctx, &param)
	if err != nil {
		return nil, false, "", err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		key, err := cos.DecodeURIComponent(o.Key)
		if err != nil {
			return nil, false, "", errors.WithMessagef(err, "failed to decode key %s", o.Key)
		}
		objs[i] = &obj{key, int64(o.Size), t, strings.HasSuffix(key, "/"), o.StorageClass}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			key, err := cos.DecodeURIComponent(p)
			if err != nil {
				return nil, false, "", errors.WithMessagef(err, "failed to decode commonPrefixes %s", p)
			}
			objs = append(objs, &obj{key, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, resp.IsTruncated, resp.NextMarker, nil
}

func (c *COS) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (c *COS) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	var options cos.InitiateMultipartUploadOptions
	if c.sc != "" {
		options.ObjectPutHeaderOptions = &cos.ObjectPutHeaderOptions{XCosStorageClass: c.sc}
	}
	resp, _, err := c.c.Object.InitiateMultipartUpload(ctx, key, &options)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: resp.UploadID, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (c *COS) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	resp, err := c.c.Object.UploadPart(ctx, key, uploadID, num, bytes.NewReader(body), nil)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: resp.Header.Get("Etag")}, nil
}

func (c *COS) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	result, _, err := c.c.Object.CopyPart(ctx, key, uploadID, num, c.endpoint+"/"+srcKey, &cos.ObjectCopyPartOptions{
		XCosCopySourceRange: fmt.Sprintf("bytes=%d-%d", off, off+size-1),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: result.ETag}, nil
}

func (c *COS) AbortUpload(key string, uploadID string) {
	_, _ = c.c.Object.AbortMultipartUpload(ctx, key, uploadID)
}

func (c *COS) CompleteUpload(key string, uploadID string, parts []*Part) error {
	var cosParts []cos.Object
	for i := range parts {
		cosParts = append(cosParts, cos.Object{ETag: parts[i].ETag, PartNumber: parts[i].Num})
	}
	_, _, err := c.c.Object.CompleteMultipartUpload(ctx, key, uploadID, &cos.CompleteMultipartUploadOptions{Parts: cosParts})
	return err
}

func (c *COS) ListUploads(marker string) ([]*PendingPart, string, error) {
	input := &cos.ListMultipartUploadsOptions{
		KeyMarker: marker,
	}
	result, _, err := c.c.Bucket.ListMultipartUploads(ctx, input)
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		t, _ := time.Parse(time.RFC3339, u.Initiated)
		parts[i] = &PendingPart{u.Key, u.UploadID, t}
	}
	return parts, result.NextKeyMarker, nil
}

func (c *COS) SetStorageClass(sc string) error {
	c.sc = sc
	return nil
}

func autoCOSEndpoint(bucketName, accessKey, secretKey, token string) (string, error) {
	client := cos.NewClient(nil, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:     accessKey,
			SecretKey:    secretKey,
			SessionToken: token,
		},
	})
	client.UserAgent = UserAgent
	s, _, err := client.Service.Get(ctx)
	if err != nil {
		return "", err
	}

	for _, b := range s.Buckets {
		// fmt.Printf("%#v\n", b)
		if b.Name == bucketName {
			return fmt.Sprintf("https://%s.cos.%s.myqcloud.com", b.Name, b.Region), nil
		}
	}

	return "", fmt.Errorf("bucket %q doesn't exist", bucketName)
}

func newCOS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)

	if accessKey == "" {
		accessKey = os.Getenv("COS_SECRETID")
		secretKey = os.Getenv("COS_SECRETKEY")
	}

	if len(hostParts) == 1 {
		if endpoint, err = autoCOSEndpoint(hostParts[0], accessKey, secretKey, token); err != nil {
			return nil, fmt.Errorf("Unable to get endpoint of bucket %s: %s", hostParts[0], err)
		}
		if uri, err = url.ParseRequestURI(endpoint); err != nil {
			return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
		}
		logger.Debugf("Use endpoint %q", endpoint)
	}

	b := &cos.BaseURL{BucketURL: uri}
	client := cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:     accessKey,
			SecretKey:    secretKey,
			SessionToken: token,
			Transport:    httpClient.Transport,
		},
	})
	client.UserAgent = UserAgent
	return &COS{c: client, endpoint: uri.Host}, nil
}

func init() {
	Register("cos", newCOS)
}
