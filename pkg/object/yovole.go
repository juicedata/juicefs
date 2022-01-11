/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	uuid "github.com/satori/go.uuid"
)

type yovole struct {
	RestfulStorage
}

func (u *yovole) String() string {
	uri, _ := url.ParseRequestURI(u.endpoint)
	return fmt.Sprintf("yovole://%s/", uri.Host)
}

func yovoleSigner(req *http.Request, accessKey, secretKey, signName string) {
	var headers = []string{"date", "nonce", "version"}
	nonce := uuid.NewV4()
	req.Header.Add("Nonce", nonce.String())
	req.Header.Add("Version", "2018-10-30")
	toSign := fmt.Sprintf("date:%s\nnonce:%s\nversion:2018-10-30\n", req.Header["Date"][0], nonce)
	h := hmac.New(sha1.New, []byte(secretKey))
	_, _ = h.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(h.Sum(nil))
	auth := fmt.Sprintf("YCS1-HMAC-SHA1 Credential=%s, SignedHeaders=%s, Signature=%s",
		accessKey, strings.Join(headers, ";"), sig)
	req.Header.Add("Authorization", auth)
}

func (u *yovole) Create() error {
	_, err := u.List("", "", 1)
	if err != nil {
		return fmt.Errorf("projectId needed")
	}
	return nil
}

// ListOutput presents output for ListObjects.
type ListResult struct {
	ObjectSummaries []ObjectSummaries
	BucketName      string
	Prefix          string
	MaxKeys         int
}

type ObjectSummaries struct {
	Key          string
	Size         int64
	LastModified int64
}

func (u *yovole) List(prefix, marker string, limit int64) ([]Object, error) {
	uri, _ := url.ParseRequestURI(u.endpoint)

	query := url.Values{}
	query.Add("prefix", prefix)
	query.Add("marker", marker)
	if limit > 100000 {
		limit = 100000
	}
	query.Add("maxKeys", strconv.Itoa(int(limit)))
	uri.RawQuery = query.Encode()
	uri.Path = "/"
	req, err := http.NewRequest("GET", uri.String(), nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	u.signer(req, u.accessKey, u.secretKey, u.signName)
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
	var out ListResult
	err = json.Unmarshal(data, &out)
	if err != nil {
		return nil, err
	}
	objs := make([]Object, 0)
	for _, item := range out.ObjectSummaries {
		objs = append(objs, &obj{item.Key, item.Size, time.Unix(item.LastModified, 0), strings.HasSuffix(item.Key, "/")})
	}
	return objs, nil
}

func newYovole(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	return &yovole{RestfulStorage{DefaultObjectStorage{}, endpoint, accessKey, secretKey, "YCS1", yovoleSigner}}, nil
}

func init() {
	Register("yovole", newYovole)
}
