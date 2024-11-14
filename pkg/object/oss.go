//go:build !nooss
// +build !nooss

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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

const ossDefaultRegionID = "cn-hangzhou"

type ossClient struct {
	client *oss.Client
	bucket *oss.Bucket
	sc     string
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s/", o.bucket.BucketName)
}

func (o *ossClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              100 << 10,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (o *ossClient) Create() error {
	var option []oss.Option
	if o.sc != "" {
		option = append(option, oss.StorageClass(oss.StorageClassType(o.sc)))
	}
	err := o.bucket.Client.CreateBucket(o.bucket.BucketName, option...)
	if err != nil && isExists(err) {
		err = nil
	}
	return err
}

func (o *ossClient) checkError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "InvalidAccessKeyId") || strings.Contains(msg, "SecurityTokenExpired") {
		logger.Warnf("refresh security token: %s", err)
		go o.refreshToken()
	}
	return err
}

func (o *ossClient) Head(key string) (Object, error) {
	var r http.Header
	var err error
	if o.sc != "" {
		r, err = o.bucket.GetObjectDetailedMeta(key)
	} else {
		r, err = o.bucket.GetObjectMeta(key)
	}
	if o.checkError(err) != nil {
		if e, ok := err.(oss.ServiceError); ok && e.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}

	lastModified := r.Get("Last-Modified")
	if lastModified == "" {
		return nil, fmt.Errorf("cannot get last modified time")
	}
	contentLength := r.Get("Content-Length")
	mtime, _ := time.Parse(time.RFC1123, lastModified)
	size, _ := strconv.ParseInt(contentLength, 10, 64)
	return &obj{
		key,
		size,
		mtime,
		strings.HasSuffix(key, "/"),
		r.Get(oss.HTTPHeaderOssStorageClass),
	}, nil
}

func (o *ossClient) Get(key string, off, limit int64, getters ...AttrGetter) (resp io.ReadCloser, err error) {
	var respHeader http.Header
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("%d-", off)
		}
		resp, err = o.bucket.GetObject(key, oss.NormalizedRange(r), oss.RangeBehavior("standard"), oss.GetResponseHeader(&respHeader))
	} else {
		resp, err = o.bucket.GetObject(key, oss.GetResponseHeader(&respHeader))
		if err == nil {
			length, err := strconv.ParseInt(resp.(*oss.Response).Headers.Get(oss.HTTPHeaderContentLength), 10, 64)
			if err != nil {
				length = -1
				logger.Warnf("failed to parse content-length %s: %s", resp.(*oss.Response).Headers.Get(oss.HTTPHeaderContentLength), err)
			}
			resp = verifyChecksum(resp,
				resp.(*oss.Response).Headers.Get(oss.HTTPHeaderOssMetaPrefix+checksumAlgr),
				length)
		}
	}
	attrs := applyGetters(getters...)
	attrs.SetRequestID(respHeader.Get(oss.HTTPHeaderOssRequestID))
	attrs.SetStorageClass(respHeader.Get(oss.HTTPHeaderOssStorageClass))
	err = o.checkError(err)
	return
}

func (o *ossClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	var option []oss.Option
	if ins, ok := in.(io.ReadSeeker); ok {
		option = append(option, oss.Meta(checksumAlgr, generateChecksum(ins)))
	}
	if o.sc != "" {
		option = append(option, oss.ObjectStorageClass(oss.StorageClassType(o.sc)))
	}
	var respHeader http.Header
	option = append(option, oss.GetResponseHeader(&respHeader))
	err := o.bucket.PutObject(key, in, option...)
	attrs := applyGetters(getters...)
	attrs.SetRequestID(respHeader.Get(oss.HTTPHeaderOssRequestID)).SetStorageClass(o.sc)
	return o.checkError(err)
}

func (o *ossClient) Copy(dst, src string) error {
	var option []oss.Option
	if o.sc != "" {
		option = append(option, oss.ObjectStorageClass(oss.StorageClassType(o.sc)))
	}
	_, err := o.bucket.CopyObject(src, dst, option...)
	return o.checkError(err)
}

func (o *ossClient) Delete(key string, getters ...AttrGetter) error {
	var respHeader http.Header
	err := o.bucket.DeleteObject(key, oss.GetResponseHeader(&respHeader))
	attrs := applyGetters(getters...)
	attrs.SetRequestID(respHeader.Get(oss.HTTPHeaderOssRequestID))
	return o.checkError(err)
}

func (o *ossClient) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	result, err := o.bucket.ListObjectsV2(
		oss.Prefix(prefix),
		oss.StartAfter(start),
		oss.ContinuationToken(token),
		oss.Delimiter(delimiter),
		oss.MaxKeys(int(limit)))
	if o.checkError(err) != nil {
		return nil, false, "", err
	}
	n := len(result.Objects)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := result.Objects[i]
		objs[i] = &obj{o.Key, o.Size, o.LastModified, strings.HasSuffix(o.Key, "/"), o.StorageClass}
	}
	if delimiter != "" {
		for _, o := range result.CommonPrefixes {
			objs = append(objs, &obj{o, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, result.IsTruncated, result.NextContinuationToken, nil
}

func (o *ossClient) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (o *ossClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	var option []oss.Option
	if o.sc != "" {
		option = append(option, oss.ObjectStorageClass(oss.StorageClassType(o.sc)))
	}
	r, err := o.bucket.InitiateMultipartUpload(key, option...)
	if o.checkError(err) != nil {
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
	if o.checkError(err) != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: r.ETag}, nil
}

func (o *ossClient) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	initMultipartResult := oss.InitiateMultipartUploadResult{Bucket: o.bucket.BucketName, Key: key, UploadID: uploadID}
	partCopy, err := o.bucket.UploadPartCopy(initMultipartResult, o.bucket.BucketName, srcKey, off, size, num)
	if o.checkError(err) != nil {
		return nil, err
	}
	return &Part{Num: num, ETag: partCopy.ETag}, nil
}

func (o *ossClient) AbortUpload(key string, uploadID string) {
	initResult := oss.InitiateMultipartUploadResult{
		Key:      key,
		UploadID: uploadID,
	}
	_ = o.bucket.AbortMultipartUpload(initResult)
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
	return o.checkError(err)
}

func (o *ossClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	result, err := o.bucket.ListMultipartUploads(oss.KeyMarker(marker))
	if o.checkError(err) != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(result.Uploads))
	for i, u := range result.Uploads {
		parts[i] = &PendingPart{u.Key, u.UploadID, u.Initiated}
	}
	return parts, result.NextKeyMarker, nil
}

func (o *ossClient) SetStorageClass(sc string) error {
	o.sc = sc
	return nil
}

type stsCred struct {
	AccessKeyId     string
	AccessKeySecret string
	Expiration      string
	SecurityToken   string
	LastUpdated     string
	Code            string
}

func fetch(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

func fetchStsToken() (*stsCred, error) {
	if cred, err := fetchStsCred(); err == nil {
		return cred, nil
	}

	// EMR MetaService: https://help.aliyun.com/document_detail/43966.html
	url := "http://127.0.0.1:10011/"
	token, err := fetch(url + "role-security-token")
	if err != nil {
		return nil, err
	}
	accessKey, err := fetch(url + "role-access-key-id")
	if err != nil {
		return nil, err
	}
	secretKey, err := fetch(url + "role-access-key-secret")
	if err != nil {
		return nil, err
	}
	return &stsCred{
		SecurityToken:   string(token),
		AccessKeyId:     string(accessKey),
		AccessKeySecret: string(secretKey),
		Expiration:      time.Now().Add(time.Hour * 24 * 100).Format("2006-01-02T15:04:05Z"),
	}, nil
}

func fetchStsCred() (*stsCred, error) {
	url := "http://100.100.100.200/latest/meta-data/Ram/security-credentials/"
	role, err := fetch(url)
	if err != nil {
		return nil, err
	}
	d, err := fetch(url + string(role))
	if err != nil {
		return nil, err
	}
	var cred stsCred
	err = json.Unmarshal(d, &cred)
	return &cred, err
}

func (o *ossClient) refreshToken() time.Time {
	cred, err := fetchStsToken()
	if err != nil {
		logger.Errorf("refresh token: %s", err)
		return time.Now().Add(time.Second)
	}
	o.client.Config.AccessKeyID = cred.AccessKeyId
	o.client.Config.AccessKeySecret = cred.AccessKeySecret
	o.client.Config.SecurityToken = cred.SecurityToken
	logger.Debugf("Refreshed STS, will be expired at %s", cred.Expiration)
	expire, err := time.Parse("2006-01-02T15:04:05Z", cred.Expiration)
	if err != nil {
		logger.Errorf("invalid expiration: %s, %s", cred.Expiration, err)
		return time.Now().Add(time.Minute)
	}
	return expire
}

func autoOSSEndpoint(bucketName, accessKey, secretKey, securityToken string) (string, error) {
	var client *oss.Client
	var err error

	regionID := ossDefaultRegionID
	if rid := os.Getenv("ALICLOUD_REGION_ID"); rid != "" {
		regionID = rid
	}
	defaultEndpoint := fmt.Sprintf("https://oss-%s.aliyuncs.com", regionID)

	if securityToken == "" {
		if client, err = oss.New(defaultEndpoint, accessKey, secretKey); err != nil {
			return "", err
		}
	} else {
		if client, err = oss.New(defaultEndpoint, accessKey, secretKey,
			oss.SecurityToken(securityToken)); err != nil {
			return "", err
		}
	}

	result, err := client.ListBuckets(oss.Prefix(bucketName), oss.MaxKeys(1))
	if err != nil {
		return "", err
	}
	if len(result.Buckets) == 0 {
		return "", fmt.Errorf("cannot list bucket %q using endpoint %q", bucketName, defaultEndpoint)
	}

	bucketLocation := result.Buckets[0].Location
	// try oss internal endpoint
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s-internal.aliyuncs.com:http",
		bucketLocation), time.Second*3); err == nil {
		_ = conn.Close()
		return fmt.Sprintf("http://%s-internal.aliyuncs.com", bucketLocation), nil
	}

	return fmt.Sprintf("https://%s.aliyuncs.com", bucketLocation), nil
}

func newOSS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucketName := hostParts[0]

	var domain string
	if len(hostParts) > 1 {
		domain = uri.Scheme + "://" + hostParts[1]
	}

	var refresh bool
	if accessKey == "" {
		// try environment variable
		accessKey = os.Getenv("ALICLOUD_ACCESS_KEY_ID")
		secretKey = os.Getenv("ALICLOUD_ACCESS_KEY_SECRET")
		token = os.Getenv("SECURITY_TOKEN")

		if accessKey == "" {
			var err error
			var cred *stsCred
			maxRetry := 4
			for i := 0; i < maxRetry; i++ {
				time.Sleep(time.Second * time.Duration(i))
				if cred, err = fetchStsToken(); err != nil {
					logger.Warnf("Fetch STS Token try %d: %s", i+1, err)
				} else {
					accessKey = cred.AccessKeyId
					secretKey = cred.AccessKeySecret
					token = cred.SecurityToken
					refresh = true
					break
				}
			}
			if err != nil {
				return nil, fmt.Errorf("No credential provided for OSS: %s", err)
			}
		}
	}

	if domain == "" {
		if domain, err = autoOSSEndpoint(bucketName, accessKey, secretKey, token); err != nil {
			return nil, fmt.Errorf("Unable to get endpoint of bucket %s: %s", bucketName, err)
		}
		logger.Debugf("Use endpoint %q", domain)
	}

	client, err := oss.New(domain, accessKey, secretKey, oss.SecurityToken(token), oss.HTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("Cannot create OSS client with endpoint %s: %s", endpoint, err)
	}

	client.Config.Timeout = 10
	client.Config.RetryTimes = 1
	client.Config.HTTPTimeout.ConnectTimeout = time.Second * 2   // 30s
	client.Config.HTTPTimeout.ReadWriteTimeout = time.Second * 5 // 60s
	client.Config.HTTPTimeout.HeaderTimeout = time.Second * 5    // 60s
	client.Config.HTTPTimeout.LongTimeout = time.Second * 30     // 300s
	client.Config.IsEnableCRC = false                            // CRC64ECMA is much slower than CRC32C
	client.Config.UserAgent = UserAgent

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("Cannot create bucket %s: %s", bucketName, err)
	}

	o := &ossClient{client: client, bucket: bucket}
	if token != "" && refresh {
		go func() {
			for {
				next := o.refreshToken()
				time.Sleep(time.Until(next) / 2)
			}
		}()
	}
	return o, nil
}

func init() {
	Register("oss", newOSS)
}
