// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type ossClient struct {
	client *oss.Client
	bucket *oss.Bucket
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s", o.bucket.BucketName)
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
		objs[i] = &Object{o.Key, o.Size, o.LastModified, strings.HasSuffix(o.Key, "/")}
	}
	return objs, nil
}

func (o *ossClient) ListAll(prefix, marker string) (<-chan *Object, error) {
	return nil, notSupported
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

type stsCred struct {
	AccessKeyId     string
	AccessKeySecret string
	Expiration      string
	SecurityToken   string
	LastUpdated     string
	Code            string
}

func fetchStsCred() (*stsCred, error) {
	url := "http://100.100.100.200/latest/meta-data/Ram/security-credentials/"
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	d, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	role := string(d)
	req, err = http.NewRequest("GET", url+role, nil)
	if err != nil {
		return nil, err
	}
	resp, err = httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	d, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var cred stsCred
	err = json.Unmarshal(d, &cred)
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

func newOSS(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]
	domain := uri.Scheme + "://" + hostParts[1]

	var client *oss.Client
	if accessKey != "" {
		client, err = oss.New(domain, accessKey, secretKey)
	} else {
		cred, err := fetchStsCred()
		if err != nil {
			logger.Fatalf("No credential provided for OSS")
		}
		client, err = oss.New(domain, cred.AccessKeyId, cred.AccessKeySecret,
			oss.SecurityToken(cred.SecurityToken))
		go func() {
			for {
				cred, err := fetchStsCred()
				if err == nil {
					client.Config.AccessKeyID = cred.AccessKeyId
					client.Config.AccessKeySecret = cred.AccessKeySecret
					client.Config.SecurityToken = cred.SecurityToken
					logger.Debugf("Refreshed STS, will be expired at %s", cred.Expiration)
					expire, err := time.Parse("2006-01-02T15:04:05Z", cred.Expiration)
					if err == nil {
						time.Sleep(expire.Sub(time.Now()) / 2)
					}
				}
			}
		}()
	}
	if err != nil {
		logger.Fatalf("Cannot create OSS client with endpoint %s: %s", endpoint, err)
	}

	client.Config.Timeout = 10
	client.Config.RetryTimes = 1
	client.Config.HTTPTimeout.ConnectTimeout = time.Second * 2   // 30s
	client.Config.HTTPTimeout.ReadWriteTimeout = time.Second * 5 // 60s
	client.Config.HTTPTimeout.HeaderTimeout = time.Second * 5    // 60s
	client.Config.HTTPTimeout.LongTimeout = time.Second * 30     // 300s

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		logger.Fatalf("Cannot create bucket %s: %s", bucketName, err)
	}

	return &ossClient{client: client, bucket: bucket}
}

func init() {
	register("oss", newOSS)
}
