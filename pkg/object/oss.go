// +build !nooss

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
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

const ossDefaultRegionID = "cn-hangzhou"

type ossClient struct {
	client *oss.Client
	bucket *oss.Bucket
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s/", o.bucket.BucketName)
}

func (o *ossClient) Create() error {
	err := o.bucket.Client.CreateBucket(o.bucket.BucketName)
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
	r, err := o.bucket.GetObjectMeta(key)
	if o.checkError(err) != nil {
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
	}, nil
}

func (o *ossClient) Get(key string, off, limit int64) (resp io.ReadCloser, err error) {
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("%d-", off)
		}
		resp, err = o.bucket.GetObject(key, oss.NormalizedRange(r), oss.RangeBehavior("standard"))
	} else {
		resp, err = o.bucket.GetObject(key)
		if err == nil {
			resp = verifyChecksum(resp,
				resp.(*oss.Response).Headers.Get(oss.HTTPHeaderOssMetaPrefix+checksumAlgr))
		}
	}
	err = o.checkError(err)
	return
}

func (o *ossClient) Put(key string, in io.Reader) error {
	if ins, ok := in.(io.ReadSeeker); ok {
		option := oss.Meta(checksumAlgr, generateChecksum(ins))
		return o.checkError(o.bucket.PutObject(key, in, option))
	}
	return o.checkError(o.bucket.PutObject(key, in))
}

func (o *ossClient) Copy(dst, src string) error {
	_, err := o.bucket.CopyObject(src, dst)
	return o.checkError(err)
}

func (o *ossClient) Delete(key string) error {
	return o.checkError(o.bucket.DeleteObject(key))
}

func (o *ossClient) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	result, err := o.bucket.ListObjects(oss.Prefix(prefix),
		oss.Marker(marker), oss.MaxKeys(int(limit)))
	if o.checkError(err) != nil {
		return nil, err
	}
	n := len(result.Objects)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := result.Objects[i]
		objs[i] = &obj{o.Key, o.Size, o.LastModified, strings.HasSuffix(o.Key, "/")}
	}
	return objs, nil
}

func (o *ossClient) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func (o *ossClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	r, err := o.bucket.InitiateMultipartUpload(key)
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
	return ioutil.ReadAll(resp.Body)
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
		conn.Close()
		return fmt.Sprintf("http://%s-internal.aliyuncs.com", bucketLocation), nil
	}

	return fmt.Sprintf("https://%s.aliyuncs.com", bucketLocation), nil
}

func newOSS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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

	securityToken := ""
	if accessKey == "" {
		// try environment variable
		accessKey = os.Getenv("ALICLOUD_ACCESS_KEY_ID")
		secretKey = os.Getenv("ALICLOUD_ACCESS_KEY_SECRET")
		securityToken = os.Getenv("SECURITY_TOKEN")

		if accessKey == "" {
			if cred, err := fetchStsToken(); err != nil {
				return nil, fmt.Errorf("No credential provided for OSS")
			} else {
				accessKey = cred.AccessKeyId
				secretKey = cred.AccessKeySecret
				securityToken = cred.SecurityToken
			}
		}
	}

	if domain == "" {
		if domain, err = autoOSSEndpoint(bucketName, accessKey, secretKey, securityToken); err != nil {
			return nil, fmt.Errorf("Unable to get endpoint of bucket %s: %s", bucketName, err)
		}
		logger.Debugf("Use endpoint %q", domain)
	}

	var client *oss.Client
	if securityToken == "" {
		client, err = oss.New(domain, accessKey, secretKey)
	} else {
		client, err = oss.New(domain, accessKey, secretKey, oss.SecurityToken(securityToken))
	}
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

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("Cannot create bucket %s: %s", bucketName, err)
	}

	o := &ossClient{client: client, bucket: bucket}
	if securityToken != "" {
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
