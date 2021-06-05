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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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
	CommonPrefixes string
}

type mss struct {
	RestfulStorage
}

func (u *mss) String() string {
	uri, _ := url.ParseRequestURI(u.endpoint)
	return fmt.Sprintf("mss://%s/", uri.Host)
}

var awskeys []string = []string{"x-amz-copy-source"}

// RequestURL is fully url of api request
func mssSigner(req *http.Request, accessKey, secretKey, signName string) {
	toSign := req.Method + "\n"
	for _, n := range HEADER_NAMES {
		toSign += req.Header.Get(n) + "\n"
	}
	for _, k := range awskeys {
		if req.Header.Get(k) != "" {
			toSign += k + ":" + req.Header.Get(k) + "\n"
		}
	}
	bucket := strings.Split(req.URL.Host, ".")[0]
	if req.Method == "GET" {
		toSign += "/" + bucket
	}
	toSign += req.URL.Path
	h := hmac.New(sha1.New, []byte(secretKey))
	_, _ = h.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(h.Sum(nil))
	token := signName + " " + accessKey + ":" + sig
	req.Header.Add("Authorization", token)
}

func (c *mss) Copy(dst, src string) error {
	uri, _ := url.ParseRequestURI(c.endpoint)
	bucket := strings.Split(uri.Host, ".")[0]
	source := fmt.Sprintf("%s/%s", bucket, src)
	resp, err := c.request("PUT", dst, nil, map[string]string{
		"x-amz-copy-source": source,
	})
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return parseError(resp)
	}
	return nil
}

func (c *mss) List(prefix, marker string, limit int64) ([]Object, error) {
	uri, _ := url.ParseRequestURI(c.endpoint)

	query := url.Values{}
	query.Add("prefix", prefix)
	query.Add("marker", marker)
	if limit > 1000 {
		limit = 1000
	}
	query.Add("max-keys", strconv.Itoa(int(limit)))
	uri.RawQuery = query.Encode()
	uri.Path = "/"
	req, err := http.NewRequest("GET", uri.String(), nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	mssSigner(req, c.accessKey, c.secretKey, c.signName)
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
	objs := make([]Object, len(out.Contents))
	for i, item := range out.Contents {
		objs[i] = &obj{
			item.Key,
			item.Size,
			item.LastModified,
			strings.HasSuffix(item.Key, "/"),
		}
	}
	return objs, nil
}

func newMSS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	qs := &mss{RestfulStorage{
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		signName:  "AWS",
		signer:    mssSigner,
	}}
	return qs, nil
}

func init() {
	Register("mss", newMSS)
}
