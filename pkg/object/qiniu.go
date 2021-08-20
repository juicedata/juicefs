// +build !noqiniu,!nos3

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
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/qiniu/api.v7/v7/auth/qbox"
	"github.com/qiniu/api.v7/v7/storage"
)

type qiniu struct {
	s3client
	bm     *storage.BucketManager
	mac    *qbox.Mac
	cfg    *storage.Config
	marker string
}

func (q *qiniu) String() string {
	return fmt.Sprintf("qiniu://%s/", q.bucket)
}

func (q *qiniu) download(key string, off, limit int64) (io.ReadCloser, error) {
	deadline := time.Now().Add(time.Second * 3600).Unix()
	url := storage.MakePrivateURL(q.mac, os.Getenv("QINIU_DOMAIN"), key, deadline)
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

func (q *qiniu) Head(key string) (Object, error) {
	r, err := q.bm.Stat(q.bucket, key)
	if err != nil {
		return nil, err
	}

	mtime := time.Unix(0, r.PutTime*100)
	return &obj{
		key,
		r.Fsize,
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (q *qiniu) Get(key string, off, limit int64) (io.ReadCloser, error) {
	// S3 SDK cannot get objects with prefix "/" in the key
	if strings.HasPrefix(key, "/") && os.Getenv("QINIU_DOMAIN") != "" {
		return q.download(key, off, limit)
	}
	for strings.HasPrefix(key, "/") {
		key = key[1:]
	}
	// S3ForcePathStyle = true
	return q.s3client.Get("/"+key, off, limit)
}

func (q *qiniu) Put(key string, in io.Reader) error {
	body, vlen, err := findLen(in)
	if err != nil {
		return err
	}
	putPolicy := storage.PutPolicy{Scope: q.bucket + ":" + key}
	upToken := putPolicy.UploadToken(q.mac)
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

func (q *qiniu) Delete(key string) error {
	return q.bm.Delete(q.bucket, key)
}

func (q *qiniu) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	if marker == "" {
		q.marker = ""
	} else if q.marker == "" {
		// last page
		return nil, nil
	}
	entries, _, markerOut, hasNext, err := q.bm.ListFiles(q.bucket, prefix, "", q.marker, int(limit))
	for err == nil && len(entries) == 0 && hasNext {
		entries, _, markerOut, hasNext, err = q.bm.ListFiles(q.bucket, prefix, "", markerOut, int(limit))
	}
	q.marker = markerOut
	if len(entries) > 0 || err == io.EOF {
		// ignore error if returned something
		err = nil
	}
	if err != nil {
		return nil, err
	}
	n := len(entries)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		entry := entries[i]
		mtime := entry.PutTime / 10000000
		objs[i] = &obj{entry.Key, entry.Fsize, time.Unix(mtime, 0), strings.HasSuffix(entry.Key, "/")}
	}
	return objs, nil
}

var publicRegions = map[string]*storage.Zone{
	"cn-east-1":      &storage.ZoneHuadong,
	"cn-north-1":     &storage.ZoneHuabei,
	"cn-south-1":     &storage.ZoneHuanan,
	"us-west-1":      &storage.ZoneBeimei,
	"ap-southeast-1": &storage.ZoneXinjiapo,
}

func newQiniu(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
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
	s3client := s3client{bucket, s3.New(ses), ses}

	cfg := storage.Config{
		UseHTTPS: uri.Scheme == "https",
	}
	zone, ok := publicRegions[region]
	if !ok {
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
	mac := qbox.NewMac(accessKey, secretKey)
	bucketManager := storage.NewBucketManager(mac, &cfg)
	return &qiniu{s3client, bucketManager, mac, &cfg, ""}, nil
}

func init() {
	Register("qiniu", newQiniu)
}
