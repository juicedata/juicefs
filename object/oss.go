// Copyright (C) 2018-present Juicedata Inc.

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

const (
	ossDefaultRegionID = "cn-hangzhou"
)

type ossClient struct {
	client *oss.Client
	bucket *oss.Bucket
}

func (o *ossClient) String() string {
	return fmt.Sprintf("oss://%s", o.bucket.BucketName)
}

func (o *ossClient) Head(key string) (*Object, error) {
	r, err := o.bucket.GetObjectMeta(key)
	if err != nil {
		return nil, err
	}

	lastModified := r.Get("Last-Modified")
	if lastModified == "" {
		return nil, fmt.Errorf("cannot get last modified time")
	}
	contentLength := r.Get("Content-Length")
	mtime, _ := time.Parse(time.RFC1123, lastModified)
	size, _ := strconv.ParseInt(contentLength, 10, 64)
	return &Object{
		key,
		size,
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (o *ossClient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("%d-", off)
		}
		return o.bucket.GetObject(key, oss.NormalizedRange(r), oss.RangeBehavior("standard"))
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

func (o *ossClient) Delete(key string) error {
	if _, err := o.Head(key); err != nil {
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

func autoEndpoint(bucketName, accessKey, secretKey, securityToken string) (string, error) {
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

func newOSS(endpoint, accessKey, secretKey string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
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
				logger.Fatalf("No credential provided for OSS")
			} else {
				accessKey = cred.AccessKeyId
				secretKey = cred.AccessKeySecret
				securityToken = cred.SecurityToken
			}
		}
	}

	if domain == "" {
		if domain, err = autoEndpoint(bucketName, accessKey, secretKey, securityToken); err != nil {
			logger.Fatalf("Unable to get endpoint of bucket %s: %s", bucketName, err)
		}
		logger.Debugf("Use endpoint %q", domain)
	}

	var client *oss.Client
	if securityToken == "" {
		client, err = oss.New(domain, accessKey, secretKey)
	} else {
		client, err = oss.New(domain, accessKey, secretKey, oss.SecurityToken(securityToken))
		go func() {
			for {
				cred, err := fetchStsToken()
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
