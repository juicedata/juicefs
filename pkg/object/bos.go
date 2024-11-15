//go:build !nobos
// +build !nobos

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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
)

type bosclient struct {
	DefaultObjectStorage
	bucket string
	sc     string
	c      *bos.Client
}

func (q *bosclient) String() string {
	return fmt.Sprintf("bos://%s/", q.bucket)
}

func (q *bosclient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              100 << 10,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (q *bosclient) SetStorageClass(sc string) error {
	q.sc = sc
	return nil
}

func (q *bosclient) Create() error {
	_, err := q.c.PutBucket(q.bucket)
	if err == nil && q.sc != "" {
		if err := q.c.PutBucketStorageclass(q.bucket, q.sc); err != nil {
			logger.Warnf("failed to set storage class: %v", err)
		}
	}
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (q *bosclient) Head(key string) (Object, error) {
	r, err := q.c.GetObjectMeta(q.bucket, key)
	if err != nil {
		if e, ok := err.(*bce.BceServiceError); ok && e.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	mtime, _ := time.Parse(time.RFC1123, r.LastModified)
	return &obj{
		key,
		r.ContentLength,
		mtime,
		strings.HasSuffix(key, "/"),
		r.StorageClass,
	}, nil
}

func (q *bosclient) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	var r *api.GetObjectResult
	var err error
	if limit > 0 {
		r, err = q.c.GetObject(q.bucket, key, nil, off, off+limit-1)
	} else if off > 0 {
		r, err = q.c.GetObject(q.bucket, key, nil, off)
	} else {
		r, err = q.c.GetObject(q.bucket, key, nil)
	}
	if err != nil {
		return nil, err
	}
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(r.StorageClass)
	return r.Body, nil
}

func (q *bosclient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	b, vlen, err := findLen(in)
	if err != nil {
		return err
	}
	body, err := bce.NewBodyFromSizedReader(b, vlen)
	if err != nil {
		return err
	}
	args := new(api.PutObjectArgs)
	if q.sc != "" {
		args.StorageClass = q.sc
	}
	_, err = q.c.PutObject(q.bucket, key, body, args)
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(q.sc)
	return err
}

func (q *bosclient) Copy(dst, src string) error {
	var args *api.CopyObjectArgs
	if q.sc != "" {
		args = &api.CopyObjectArgs{ObjectMeta: api.ObjectMeta{StorageClass: q.sc}}
	}
	_, err := q.c.CopyObject(q.bucket, dst, q.bucket, src, args)
	return err
}

func (q *bosclient) Delete(key string, getters ...AttrGetter) error {
	err := q.c.DeleteObject(q.bucket, key)
	if err != nil && strings.Contains(err.Error(), "NoSuchKey") {
		err = nil
	}
	return err
}

func (q *bosclient) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	limit_ := int(limit)
	out, err := q.c.SimpleListObjects(q.bucket, prefix, limit_, start, delimiter)
	if err != nil {
		return nil, false, "", err
	}
	n := len(out.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		k := out.Contents[i]
		mod, _ := time.Parse("2006-01-02T15:04:05Z", k.LastModified)
		objs[i] = &obj{k.Key, int64(k.Size), mod, strings.HasSuffix(k.Key, "/"), k.StorageClass}
	}
	if delimiter != "" {
		for _, p := range out.CommonPrefixes {
			objs = append(objs, &obj{p.Prefix, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, out.IsTruncated, out.NextMarker, nil
}

func (q *bosclient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	args := new(api.InitiateMultipartUploadArgs)
	if q.sc != "" {
		args.StorageClass = q.sc
	}
	r, err := q.c.InitiateMultipartUpload(q.bucket, key, "", args)
	if err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: r.UploadId, MinPartSize: 4 << 20, MaxCount: 10000}, nil
}

func (q *bosclient) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	body, _ := bce.NewBodyFromBytes(data)
	etag, err := q.c.BasicUploadPart(q.bucket, key, uploadID, num, body)
	if err != nil {
		return nil, err
	}
	return &Part{Num: num, Size: len(data), ETag: etag}, nil
}

func (q *bosclient) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	result, err := q.c.UploadPartCopy(q.bucket, key, q.bucket, srcKey, uploadID, num,
		&api.UploadPartCopyArgs{SourceRange: fmt.Sprintf("bytes=%d-%d", off, off+size-1)})

	if err != nil {
		return nil, err
	}
	return &Part{Num: num, Size: int(size), ETag: result.ETag}, nil
}

func (q *bosclient) AbortUpload(key string, uploadID string) {
	_ = q.c.AbortMultipartUpload(q.bucket, key, uploadID)
}

func (q *bosclient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	oparts := make([]api.UploadInfoType, len(parts))
	for i := range parts {
		oparts[i] = api.UploadInfoType{
			PartNumber: parts[i].Num,
			ETag:       parts[i].ETag,
		}
	}
	ps := api.CompleteMultipartUploadArgs{Parts: oparts}
	_, err := q.c.CompleteMultipartUploadFromStruct(q.bucket, key, uploadID, &ps)
	return err
}

func (q *bosclient) ListUploads(marker string) ([]*PendingPart, string, error) {
	result, err := q.c.ListMultipartUploads(q.bucket, &api.ListMultipartUploadsArgs{
		MaxUploads: 1000,
		KeyMarker:  marker,
	})
	if err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{u.Key, u.UploadId, time.Time{}}
	}
	return parts, result.NextKeyMarker, nil
}

func autoBOSEndpoint(bucketName, accessKey, secretKey string) (string, error) {
	region := bce.DEFAULT_REGION
	if r := os.Getenv("BDCLOUD_DEFAULT_REGION"); r != "" {
		region = r
	}

	endpoint := fmt.Sprintf("https://%s.%s.bcebos.com", bucketName, region)
	bosCli, err := bos.NewClient(accessKey, secretKey, endpoint)
	if err != nil {
		return "", err
	}

	if location, err := bosCli.GetBucketLocation(bucketName); err != nil {
		return "", err
	} else {
		return fmt.Sprintf("%s.%s.bcebos.com", bucketName, location), nil
	}
}

func newBOS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	if len(hostParts) != 2 {
		return nil, fmt.Errorf("Invalid endpoint: %v", endpoint)
	}
	bucketName := hostParts[0]
	if accessKey == "" {
		accessKey = os.Getenv("BDCLOUD_ACCESS_KEY")
		secretKey = os.Getenv("BDCLOUD_SECRET_KEY")
	}

	if hostParts[1] == "bcebos.com" {
		if endpoint, err = autoBOSEndpoint(bucketName, accessKey, secretKey); err != nil {
			return nil, fmt.Errorf("Fail to get location of bucket %q: %s", bucketName, err)
		}
		if !strings.HasPrefix(endpoint, "http") {
			endpoint = fmt.Sprintf("%s://%s", uri.Scheme, endpoint)
		}
		logger.Debugf("Use endpoint: %s", endpoint)
	}

	bosClient, err := bos.NewClient(accessKey, secretKey, endpoint)
	if err != nil {
		return nil, err
	}
	return &bosclient{bucket: bucketName, c: bosClient}, nil
}

func init() {
	Register("bos", newBOS)
}
