//go:build !tos

/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/codes"
)

type tosClient struct {
	bucket string
	client *tos.ClientV2
}

func (t tosClient) String() string {
	return fmt.Sprintf("tos://%s/", t.bucket)
}

func (t tosClient) Create() error {
	_, err := t.client.CreateBucketV2(context.Background(), &tos.CreateBucketV2Input{Bucket: t.bucket})
	if e, ok := err.(*tos.TosServerError); ok {
		if e.Code == codes.BucketAlreadyOwnedByYou || e.Code == codes.BucketAlreadyExists {
			return nil
		}
	}
	return err
}

func (t tosClient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	var rangeEnd int64
	if limit > 0 {
		rangeEnd = off + limit - 1
	} else {
		rangeEnd = 0
	}
	resp, err := t.client.GetObjectV2(context.Background(), &tos.GetObjectV2Input{
		Bucket:     t.bucket,
		Key:        key,
		RangeStart: off,
		RangeEnd:   rangeEnd,
	})
	if err != nil {
		return nil, err
	}
	return resp.Content, nil
}

func (t tosClient) Put(key string, in io.Reader) error {
	_, err := t.client.PutObjectV2(context.Background(), &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket: t.bucket,
			Key:    key,
		},
		Content: in,
	})
	return err
}

func (t tosClient) Delete(key string) error {
	_, err := t.client.DeleteObjectV2(context.Background(), &tos.DeleteObjectV2Input{
		Bucket: t.bucket,
		Key:    key,
	})
	return err
}

func (t tosClient) Head(key string) (Object, error) {
	head, err := t.client.HeadObjectV2(context.Background(),
		&tos.HeadObjectV2Input{Bucket: t.bucket, Key: key})
	if err != nil {
		if e, ok := err.(*tos.TosServerError); ok {
			if e.StatusCode == http.StatusNotFound {
				err = os.ErrNotExist
			}
		}
		return nil, err
	}
	return &obj{
		key,
		head.ContentLength,
		head.LastModified,
		strings.HasSuffix(key, "/"),
	}, err
}

func (t tosClient) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	resp, err := t.client.ListObjectsV2(context.Background(), &tos.ListObjectsV2Input{
		Bucket: t.bucket,
		ListObjectsInput: tos.ListObjectsInput{
			Delimiter: delimiter,
			Prefix:    prefix,
			Marker:    marker,
			MaxKeys:   int(limit),
		},
	})
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		if !strings.HasPrefix(o.Key, prefix) || o.Key < marker {
			return nil, fmt.Errorf("found invalid key %s from List, prefix: %s, marker: %s", o.Key, prefix, marker)
		}
		objs[i] = &obj{
			o.Key,
			o.Size,
			o.LastModified,
			strings.HasSuffix(o.Key, "/"),
		}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			objs = append(objs, &obj{p.Prefix, 0, time.Unix(0, 0), true})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, nil
}

func (t tosClient) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (t tosClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	resp, err := t.client.CreateMultipartUploadV2(context.Background(), &tos.CreateMultipartUploadV2Input{
		Bucket: t.bucket,
		Key:    key,
	})
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: resp.UploadID, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

func (t tosClient) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	resp, err := t.client.UploadPartV2(context.Background(), &tos.UploadPartV2Input{
		UploadPartBasicInput: tos.UploadPartBasicInput{
			Bucket:     t.bucket,
			Key:        key,
			UploadID:   uploadID,
			PartNumber: num,
		},
		Content: bytes.NewReader(body),
	})
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: resp.ETag}, nil
}

func (t tosClient) AbortUpload(key string, uploadID string) {
	_, _ = t.client.AbortMultipartUpload(context.Background(), &tos.AbortMultipartUploadInput{
		Bucket:   t.bucket,
		Key:      key,
		UploadID: uploadID,
	})
}

func (t tosClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	var tosParts []tos.UploadedPartV2
	for i := range parts {
		tosParts = append(tosParts, tos.UploadedPartV2{ETag: parts[i].ETag, PartNumber: parts[i].Num})
	}
	_, err := t.client.CompleteMultipartUploadV2(context.Background(), &tos.CompleteMultipartUploadV2Input{
		Bucket:   t.bucket,
		Key:      key,
		UploadID: uploadID,
		Parts:    tosParts,
	})
	return err
}

func (t tosClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	result, err := t.client.ListMultipartUploadsV2(context.Background(),
		&tos.ListMultipartUploadsV2Input{Bucket: t.bucket})
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{u.Key, u.UploadID, u.Initiated}
	}
	var nextMarker string
	if result.NextKeyMarker != "" {
		nextMarker = result.NextKeyMarker
	}
	return parts, nextMarker, nil
}

func (t tosClient) Copy(dst, src string) error {
	_, err := t.client.CopyObject(context.Background(), &tos.CopyObjectInput{
		SrcBucket: t.bucket,
		Bucket:    t.bucket,
		SrcKey:    src,
		Key:       dst,
	})
	return err
}

func newTOS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 3)
	credentials := tos.NewStaticCredentials(accessKey, secretKey)
	credentials.WithSecurityToken(token)
	cli, err := tos.NewClientV2(
		hostParts[1]+"."+hostParts[2],
		tos.WithRegion(strings.TrimSuffix(hostParts[1], "tos-")),
		tos.WithCredentials(credentials))
	if err != nil {
		return nil, err
	}
	return &tosClient{bucket: hostParts[0], client: cli}, nil
}

func init() {
	Register("tos", newTOS)
}
