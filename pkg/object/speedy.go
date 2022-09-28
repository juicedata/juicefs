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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type speedy struct {
	RestfulStorage
}

type Contents struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ListObjectsOutput presents output for ListObjects.
type ListBucketResult struct {
	Contents       []*Contents
	IsTruncated    bool
	Prefix         string
	Marker         string
	MaxKeys        string
	NextMarker     string
	CommonPrefixes []string
}

func (s *speedy) String() string {
	uri, _ := url.ParseRequestURI(s.endpoint)
	return fmt.Sprintf("speedy://%s/", uri.Host)
}

func (s *speedy) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	if delimiter != "" {
		return nil, notSupportedDelimiter
	}
	uri, _ := url.ParseRequestURI(s.endpoint)

	query := url.Values{}
	query.Add("prefix", prefix)
	query.Add("marker", marker)
	if limit > 100000 {
		limit = 100000
	}
	query.Add("max-keys", strconv.Itoa(int(limit)+1))
	uri.RawQuery = query.Encode()
	uri.Path = "/"
	req, err := http.NewRequest("GET", uri.String(), nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	s.signer(req, s.accessKey, s.secretKey, s.signName)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return nil, parseError(resp)
	}
	if resp.ContentLength <= 0 || resp.ContentLength > (1<<31) {
		return nil, fmt.Errorf("invalid content length: %d", resp.ContentLength)
	}
	data := make([]byte, resp.ContentLength)
	if _, err := io.ReadFull(resp.Body, data); err != nil {
		return nil, err
	}
	var out ListBucketResult
	err = xml.Unmarshal(data, &out)
	if err != nil {
		return nil, err
	}
	objs := make([]Object, 0)
	for _, item := range out.Contents {
		if strings.HasSuffix(item.Key, "/.speedycloud_dir_flag") {
			continue
		}
		objs = append(objs, &obj{item.Key, item.Size, item.LastModified, strings.HasSuffix(item.Key, "/")})
	}
	return objs, nil
}

func newSpeedy(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	return &speedy{RestfulStorage{
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		signName:  "AWS",
		signer:    sign,
	}}, nil
}

func init() {
	Register("speedy", newSpeedy)
}
