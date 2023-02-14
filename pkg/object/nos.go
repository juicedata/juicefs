//go:build !nonos
// +build !nonos

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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NetEase-Object-Storage/nos-golang-sdk/config"
	noslogger "github.com/NetEase-Object-Storage/nos-golang-sdk/logger"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/model"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/nosclient"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/noserror"
)

type nos struct {
	DefaultObjectStorage
	bucket string
	client *nosclient.NosClient
}

func (s *nos) String() string {
	return fmt.Sprintf("nos://%s/", s.bucket)
}

func (s *nos) Head(key string) (Object, error) {
	objectRequest := &model.ObjectRequest{
		Bucket: s.bucket,
		Object: key,
	}
	r, err := s.client.GetObjectMetaData(objectRequest)
	if err != nil {
		if e, ok := err.(*noserror.ServerError); ok && e.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}
		return nil, err
	}
	lastModified := r.Metadata["Last-Modified"]
	if lastModified == "" {
		return nil, fmt.Errorf("cannot get last modified time")
	}
	mtime, _ := time.Parse(time.RFC1123, lastModified)
	return &obj{
		key,
		r.ContentLength,
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *nos) Get(key string, off, limit int64) (io.ReadCloser, error) {
	params := &model.GetObjectRequest{Bucket: s.bucket, Object: key}
	if off > 0 || limit > 0 {
		var r string
		if limit > 0 {
			r = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			r = fmt.Sprintf("bytes=%d-", off)
		}
		params.ObjRange = r
	}
	resp, err := s.client.GetObject(params)
	if err != nil {
		logger.Error(err)
		return nil, err
	}
	return resp.Body, nil
}

func (s *nos) Put(key string, in io.Reader) error {
	var body io.ReadSeeker
	switch body.(type) {
	case io.ReadSeeker:
		body = in.(io.ReadSeeker)
	default:
		data, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	params := &model.PutObjectRequest{
		Bucket: s.bucket,
		Object: key,
		Body:   body,
	}
	_, err := s.client.PutObjectByStream(params)
	return err
}

func (s *nos) Copy(dst, src string) error {
	params := &model.CopyObjectRequest{
		SrcBucket:  s.bucket,
		SrcObject:  src,
		DestBucket: s.bucket,
		DestObject: dst,
	}
	return s.client.CopyObject(params)
}

func (s *nos) Delete(key string) error {
	param := model.ObjectRequest{
		Bucket: s.bucket,
		Object: key,
	}
	return s.client.DeleteObject(&param)
}

func (s *nos) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	param := model.ListObjectsRequest{
		Bucket:    s.bucket,
		Prefix:    prefix,
		Marker:    marker,
		MaxKeys:   int(limit),
		Delimiter: delimiter,
	}
	resp, err := s.client.ListObjects(&param)
	if err != nil {
		return nil, err
	}
	n := len(resp.Contents)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		o := resp.Contents[i]
		mtime, err := time.Parse("2006-01-02T15:04:05 +0800", o.LastModified)
		if err == nil {
			mtime = mtime.Add(-8 * time.Hour)
		}
		objs[i] = &obj{o.Key, o.Size, mtime, strings.HasSuffix(o.Key, "/")}
	}
	if delimiter != "" {
		for _, p := range resp.CommonPrefixes {
			objs = append(objs, &obj{p.Prefix, 0, time.Unix(0, 0), true})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, nil
}

func newNOS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	bucket := hostParts[0]

	conf := &config.Config{
		Endpoint:  hostParts[1],
		AccessKey: accessKey,
		SecretKey: secretKey,

		NosServiceConnectTimeout:    3,
		NosServiceReadWriteTimeout:  60,
		NosServiceMaxIdleConnection: 100,

		LogLevel: noslogger.LogLevel(noslogger.ERROR),
	}

	nosClient, _ := nosclient.New(conf)

	return &nos{bucket: bucket, client: nosClient}, nil
}

func init() {
	Register("nos", newNOS)
}
