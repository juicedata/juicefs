// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
)

type bosclient struct {
	defaultObjectStorage
	bucket string
	c      *bos.Client
}

func (q *bosclient) String() string {
	return fmt.Sprintf("bos://%s", q.bucket)
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

func (q *bosclient) Exists(key string) error {
	_, err := q.c.GetObjectMeta(q.bucket, key)
	return err
}

func (q *bosclient) Delete(key string) error {
	if err := q.Exists(key); err != nil {
		return err
	}
	return q.c.DeleteObject(q.bucket, key)
}

func (q *bosclient) List(prefix, marker string, limit int64) ([]*Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	limit_ := int(limit)
	out, err := q.c.SimpleListObjects(q.bucket, prefix, limit_, marker, "")
	if err != nil {
		return nil, err
	}
	n := len(out.Contents)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		k := out.Contents[i]
		println(k.LastModified)
		mod, _ := time.Parse("2006-01-02T15:04:05Z", k.LastModified)
		objs[i] = &Object{k.Key, int64(k.Size), mod}
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
	q.c.AbortMultipartUpload(q.bucket, key, uploadID)
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
	_, err := q.c.CompleteMultipartUploadFromStruct(q.bucket, key, uploadID, &ps, nil)
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

func newBOS(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	endpoint = fmt.Sprintf("https://%s", hostParts[1])
	bosClient, err := bos.NewClient(accessKey, secretKey, endpoint)
	return &bosclient{bucket: bucketName, c: bosClient}
}

func init() {
	register("bos", newBOS)
}
