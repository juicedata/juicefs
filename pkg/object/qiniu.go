//go:build !noqiniu && !nos3
// +build !noqiniu,!nos3

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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/qiniu/go-sdk/v7/auth"
	"github.com/qiniu/go-sdk/v7/storage"
)

type qiniu struct {
	s3client
	bm     *storage.BucketManager
	cred   *auth.Credentials
	cfg    *storage.Config
	marker string
}

func (q *qiniu) String() string {
	return fmt.Sprintf("qiniu://%s/", q.bucket)
}

func (q *qiniu) SetStorageClass(_ string) error {
	return notSupported
}

func (q *qiniu) Limits() Limits {
	return Limits{}
}

func (q *qiniu) download(key string, off, limit int64) (io.ReadCloser, error) {
	deadline := time.Now().Add(time.Second * 3600).Unix()
	url := storage.MakePrivateURL(q.cred, os.Getenv("QINIU_DOMAIN"), key, deadline)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	if off > 0 || limit > 0 {
		if limit > 0 {
			req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", off, off+limit-1))
		} else {
			req.Header.Add("Range", fmt.Sprintf("bytes=%d-", off))
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, fmt.Errorf("Status code: %d", resp.StatusCode)
	}
	return resp.Body, nil
}

var notexist = "no such file or directory"

func (q *qiniu) Head(key string) (Object, error) {
	r, err := q.bm.Stat(q.bucket, key)
	if err != nil {
		if strings.Contains(err.Error(), notexist) {
			err = os.ErrNotExist
		}
		return nil, err
	}

	mtime := time.Unix(0, r.PutTime*100)
	return &obj{
		key,
		r.Fsize,
		mtime,
		strings.HasSuffix(key, "/"),
		"",
	}, nil
}

func (q *qiniu) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	// S3 SDK cannot get objects with prefix "/" in the key
	if strings.HasPrefix(key, "/") && os.Getenv("QINIU_DOMAIN") != "" {
		return q.download(key, off, limit)
	}
	for strings.HasPrefix(key, "/") {
		key = key[1:]
	}
	// S3ForcePathStyle = true
	return q.s3client.Get("/"+key, off, limit, getters...)
}

func (q *qiniu) Put(key string, in io.Reader, getters ...AttrGetter) error {
	body, vlen, err := findLen(in)
	if err != nil {
		return err
	}
	putPolicy := storage.PutPolicy{Scope: q.bucket + ":" + key}
	upToken := putPolicy.UploadToken(q.cred)
	formUploader := storage.NewFormUploader(q.cfg)
	var ret storage.PutRet
	return formUploader.Put(ctx, &ret, upToken, key, body, vlen, nil)
}

func (q *qiniu) Copy(dst, src string) error {
	return q.bm.Copy(q.bucket, src, q.bucket, dst, true)
}

func (q *qiniu) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return nil, notSupported
}

func (q *qiniu) Delete(key string, getters ...AttrGetter) error {
	err := q.bm.Delete(q.bucket, key)
	if err != nil && strings.Contains(err.Error(), notexist) {
		return nil
	}
	return err
}

func (q *qiniu) List(prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	entries, prefixes, markerOut, hasNext, err := q.bm.ListFiles(q.bucket, prefix, delimiter, token, int(limit))
	if len(entries) > 0 || err == io.EOF {
		// ignore error if returned something
		err = nil
	}
	if err != nil {
		return nil, false, "", err
	}
	n := len(entries)
	objs := make([]Object, 0, n)
	for i := 0; i < n; i++ {
		entry := entries[i]
		if entry.Key <= startAfter {
			continue
		}
		mtime := entry.PutTime / 10000000
		objs = append(objs, &obj{entry.Key, entry.Fsize, time.Unix(mtime, 0), strings.HasSuffix(entry.Key, "/"), ""})
	}
	if delimiter != "" {
		for _, p := range prefixes {
			if p <= startAfter {
				continue
			}
			objs = append(objs, &obj{p, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	if len(objs) == 0 {
		hasNext = false
		markerOut = ""
	}
	return objs, hasNext, markerOut, nil
}

func newQiniu(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucket := hostParts[0]
	endpoint = hostParts[1]
	var region string
	if strings.HasPrefix(endpoint, "s3") {
		// private region
		region = endpoint[strings.Index(endpoint, "-")+1 : strings.Index(endpoint, ".")]
	} else if strings.HasPrefix(endpoint, "qvm-") {
		region = "cn-east-1" // internal
	} else if strings.HasPrefix(endpoint, "qvm-z1") {
		region = "cn-north-1"
	} else {
		region = endpoint[:strings.LastIndex(endpoint, "-")]
	}
	awsConfig := &aws.Config{
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, token),
		Endpoint:         &endpoint,
		Region:           &region,
		DisableSSL:       aws.Bool(uri.Scheme == "http"),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       httpClient,
	}
	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("aws session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	s3client := s3client{bucket: bucket, s3: s3.New(ses), ses: ses}

	cfg := storage.Config{
		UseHTTPS: uri.Scheme == "https",
	}
	zone, err := storage.GetZone(accessKey, bucket)
	if err != nil {
		domain := strings.SplitN(endpoint, "-", 2)[1]
		zone = &storage.Zone{
			RsHost:     "rs-" + domain,
			RsfHost:    "rsf-" + domain,
			ApiHost:    "api-" + domain,
			IovipHost:  "io-" + domain,
			SrcUpHosts: []string{"up-" + domain},
		}
	} else if strings.HasPrefix(endpoint, "qvm-z1") {
		zone.SrcUpHosts = []string{"free-qvm-z1-zz.qiniup.com"}
	} else if strings.HasPrefix(endpoint, "qvm-") {
		zone.SrcUpHosts = []string{"free-qvm-z0-xs.qiniup.com"}
	}
	cfg.Zone = zone
	cred := auth.New(accessKey, secretKey)
	bucketManager := storage.NewBucketManager(cred, &cfg)
	return &qiniu{s3client, bucketManager, cred, &cfg, ""}, nil
}

func init() {
	Register("qiniu", newQiniu)
}
