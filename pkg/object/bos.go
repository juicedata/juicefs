// +build !nobos

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
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
)

const bosDefaultRegion = "bj"

type bosclient struct {
	DefaultObjectStorage
	bucket string
	c      *bos.Client
}

func (q *bosclient) String() string {
	return fmt.Sprintf("bos://%s/", q.bucket)
}

func (q *bosclient) Create() error {
	_, err := q.c.PutBucket(q.bucket)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (q *bosclient) Head(key string) (Object, error) {
	r, err := q.c.GetObjectMeta(q.bucket, key)
	if err != nil {
		return nil, err
	}
	mtime, _ := time.Parse(time.RFC1123, r.LastModified)
	return &obj{
		key,
		r.ContentLength,
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (q *bosclient) Get(key string, off, limit int64) (io.ReadCloser, error) {
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
	return r.Body, nil
}

func (q *bosclient) Put(key string, in io.Reader) error {
	b, vlen, err := findLen(in)
	if err != nil {
		return err
	}
	body, err := bce.NewBodyFromSizedReader(b, vlen)
	if err != nil {
		return err
	}
	_, err = q.c.BasicPutObject(q.bucket, key, body)
	return err
}

func (q *bosclient) Copy(dst, src string) error {
	_, err := q.c.BasicCopyObject(q.bucket, dst, q.bucket, src)
	return err
}

func (q *bosclient) Delete(key string) error {
	return q.c.DeleteObject(q.bucket, key)
}

func (q *bosclient) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	limit_ := int(limit)
	out, err := q.c.SimpleListObjects(q.bucket, prefix, limit_, marker, "")
	if err != nil {
		return nil, err
	}
	n := len(out.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		k := out.Contents[i]
		mod, _ := time.Parse("2006-01-02T15:04:05Z", k.LastModified)
		objs[i] = &obj{k.Key, int64(k.Size), mod, strings.HasSuffix(k.Key, "/")}
	}
	return objs, nil
}

func (q *bosclient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	r, err := q.c.BasicInitiateMultipartUpload(q.bucket, key)
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
	region := bosDefaultRegion
	if r := os.Getenv("BDCLOUD_DEFAULT_REGION"); r != "" {
		region = r
	}

	endpoint := fmt.Sprintf("https://%s.bcebos.com", region)
	bosCli, err := bos.NewClient(accessKey, secretKey, endpoint)
	if err != nil {
		return "", err
	}

	if location, err := bosCli.GetBucketLocation(bucketName); err != nil {
		return "", err
	} else {
		return fmt.Sprintf("%s.bcebos.com", location), nil
	}
}

func newBOS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	if len(hostParts) > 1 {
		endpoint = fmt.Sprintf("https://%s", hostParts[1])
	}

	if accessKey == "" {
		accessKey = os.Getenv("BDCLOUD_ACCESS_KEY")
		secretKey = os.Getenv("BDCLOUD_SECRET_KEY")
	}

	if len(hostParts) == 1 {
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
