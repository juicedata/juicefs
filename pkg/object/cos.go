// +build !nocos

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
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

const cosChecksumKey = "x-cos-meta-" + checksumAlgr

type COS struct {
	c        *cos.Client
	endpoint string
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

func (c *COS) Head(key string) (Object, error) {
	resp, err := c.c.Object.Head(ctx, key, nil)
	if err != nil {
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

	return &obj{key, size, mtime, strings.HasSuffix(key, "/")}, nil
}

func (c *COS) Get(key string, off, limit int64) (io.ReadCloser, error) {
	params := &cos.ObjectGetOptions{}
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("bytes=%d-", off)
		}
		params.Range = r
	}
	resp, err := c.c.Object.Get(ctx, key, params)
	if err != nil {
		return nil, err
	}
	if off == 0 && limit == -1 {
		resp.Body = verifyChecksum(resp.Body, resp.Header.Get(cosChecksumKey))
	}
	return resp.Body, nil
}

func (c *COS) Put(key string, in io.Reader) error {
	var options *cos.ObjectPutOptions
	if ins, ok := in.(io.ReadSeeker); ok {
		header := http.Header(map[string][]string{
			cosChecksumKey: {generateChecksum(ins)},
		})
		options = &cos.ObjectPutOptions{ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{XCosMetaXXX: &header}}
	}
	_, err := c.c.Object.Put(ctx, key, in, options)
	return err
}

func (c *COS) Copy(dst, src string) error {
	source := fmt.Sprintf("%s/%s", c.endpoint, src)
	_, _, err := c.c.Object.Copy(ctx, dst, source, nil)
	return err
}

func (c *COS) Delete(key string) error {
	_, err := c.c.Object.Delete(ctx, key)
	return err
}

func (c *COS) List(prefix, marker string, limit int64) ([]Object, error) {
	param := cos.BucketGetOptions{
		Prefix:  prefix,
		Marker:  marker,
		MaxKeys: int(limit),
	}
	resp, _, err := c.c.Bucket.Get(ctx, &param)
	for err == nil && len(resp.Contents) == 0 && resp.IsTruncated {
		param.Marker = resp.NextMarker
		resp, _, err = c.c.Bucket.Get(ctx, &param)
	}
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		objs[i] = &obj{o.Key, int64(o.Size), t, strings.HasSuffix(o.Key, "/")}
	}
	return objs, nil
}

func (c *COS) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (c *COS) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	resp, _, err := c.c.Object.InitiateMultipartUpload(ctx, key, nil)
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

func (c *COS) AbortUpload(key string, uploadID string) {
	_, _ = c.c.Object.AbortMultipartUpload(ctx, key, uploadID)
}

func (c *COS) CompleteUpload(key string, uploadID string, parts []*Part) error {
	var cosParts []cos.Object
	for i := range parts {
		cosParts = append(cosParts, cos.Object{Key: key, ETag: parts[i].ETag, PartNumber: parts[i].Num})
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

func autoCOSEndpoint(bucketName, accessKey, secretKey string) (string, error) {
	client := cos.NewClient(nil, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  accessKey,
			SecretKey: secretKey,
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

	return "", fmt.Errorf("bucket %q doesnot exist", bucketName)
}

func newCOS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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
		if endpoint, err = autoCOSEndpoint(hostParts[0], accessKey, secretKey); err != nil {
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
			SecretID:  accessKey,
			SecretKey: secretKey,
			Transport: httpClient.Transport,
		},
	})
	client.UserAgent = UserAgent
	return &COS{client, uri.Host}, nil
}

func init() {
	Register("cos", newCOS)
}
