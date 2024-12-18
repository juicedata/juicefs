//go:build !noufile
// +build !noufile

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ufile struct {
	RestfulStorage
}

func (u *ufile) String() string {
	uri, _ := url.ParseRequestURI(u.endpoint)
	return fmt.Sprintf("ufile://%s/", uri.Host)
}

func (u *ufile) Limits() Limits {
	// only support 4MB part size and max object size: 5TB
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  false,
		MinPartSize:              4 << 20,
		MaxPartSize:              4 << 20,
		MaxPartCount:             1310720,
	}
}

func ufileSigner(req *http.Request, accessKey, secretKey, signName string) {
	if accessKey == "" {
		return
	}
	toSign := req.Method + "\n"
	for _, n := range HEADER_NAMES {
		toSign += req.Header.Get(n) + "\n"
	}
	bucket := strings.Split(req.URL.Host, ".")[0]
	key := req.URL.Path
	// Hack for UploadHit
	if len(req.URL.RawQuery) > 0 {
		vs, _ := url.ParseQuery(req.URL.RawQuery)
		if _, ok := vs["FileName"]; ok {
			key = "/" + vs.Get("FileName")
		}
	}
	toSign += "/" + bucket + key
	h := hmac.New(sha1.New, []byte(secretKey))
	_, _ = h.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(h.Sum(nil))
	token := signName + " " + accessKey + ":" + sig
	req.Header.Add("Authorization", token)
}

func (u *ufile) Create() error {
	uri, _ := url.ParseRequestURI(u.endpoint)
	parts := strings.Split(uri.Host, ".")
	name := parts[0]
	region := parts[1] // www.cn-bj.ufileos.com
	if region == "ufile" {
		region = parts[2] // www.ufile.cn-north-02.ucloud.cn
	}
	if strings.HasPrefix(region, "internal") {
		// www.internal-hk-01.ufileos.cn
		// www.internal-cn-gd-02.ufileos.cn
		ps := strings.Split(region, "-")
		region = strings.Join(ps[1:len(ps)-1], "-")
	}

	query := url.Values{}
	query.Add("Action", "CreateBucket")
	query.Add("BucketName", name)
	query.Add("PublicKey", u.accessKey)
	query.Add("Region", region)

	// generate signature
	toSign := fmt.Sprintf("ActionCreateBucketBucketName%sPublicKey%sRegion%s",
		name, u.accessKey, region)

	sum := sha1.Sum([]byte(toSign + u.secretKey))
	sig := hex.EncodeToString(sum[:])
	query.Add("Signature", sig)

	req, err := http.NewRequest("GET", "https://api.ucloud.cn/?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	err = parseError(resp)
	if strings.Contains(err.Error(), "duplicate bucket name") ||
		strings.Contains(err.Error(), "CreateBucketResponse") {
		err = nil
	}
	return err
}

func (u *ufile) parseResp(resp *http.Response, out interface{}) error {
	defer resp.Body.Close()
	var data []byte
	if resp.ContentLength <= 0 || resp.ContentLength > (1<<31) {
		d, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		data = d
	} else {
		data = make([]byte, resp.ContentLength)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			return err
		}
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("status: %v, message: %s", resp.StatusCode, string(data))
	}
	err := json.Unmarshal(data, out)
	if err != nil {
		return err
	}
	return nil
}

func copyObj(store ObjectStorage, dst, src string) error {
	in, err := store.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer in.Close()
	d, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return store.Put(dst, bytes.NewReader(d))
}

func (u *ufile) Copy(dst, src string) error {
	resp, err := u.request("HEAD", src, nil, nil)
	if err != nil {
		return copyObj(u, dst, src)
	}
	if resp.StatusCode != 200 {
		return copyObj(u, dst, src)
	}

	etag := resp.Header["Etag"]
	if len(etag) < 1 {
		return copyObj(u, dst, src)
	}
	hash := etag[0][1 : len(etag[0])-1]
	lens := resp.Header["Content-Length"]
	if len(lens) < 1 {
		return copyObj(u, dst, src)
	}
	uri := fmt.Sprintf("uploadhit?Hash=%s&FileName=%s&FileSize=%s", hash, dst, lens[0])
	resp, err = u.request("POST", uri, nil, nil)
	if err != nil {
		return copyObj(u, dst, src)
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return copyObj(u, dst, src)
	}
	return nil
}

type ContentsItem struct {
	Key          string
	Size         string
	LastModified int
	CreateTime   int
	StorageClass string
	ETag         string
}

type CommonPrefixesItem struct {
	Prefix string
}

// uFileListObjectsOutput presents output for ListObjects.
type uFileListObjectsOutput struct {
	Maxkeys     string `json:"MaxKeys,omitempty"`
	Delimiter   string `json:"Delimiter,omitempty"`
	NextMarker  string `json:"NextMarker,omitempty"`
	IsTruncated bool   `json:"IsTruncated,omitempty"`

	// Object keys
	Contents       []*ContentsItem       `json:"Contents,omitempty"`
	CommonPrefixes []*CommonPrefixesItem `json:"CommonPrefixes,omitempty"`
}

func (u *ufile) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "" {
		return nil, false, "", notSupported
	}
	query := url.Values{}
	query.Add("prefix", prefix)
	query.Add("marker", start)
	query.Add("delimiter", delimiter)
	if limit > 1000 {
		limit = 1000
	}
	query.Add("max-keys", strconv.Itoa(int(limit)))
	resp, err := u.request("GET", "?listobjects&"+query.Encode(), nil, nil)
	if err != nil {
		return nil, false, "", err
	}

	var out uFileListObjectsOutput
	if err := u.parseResp(resp, &out); err != nil {
		return nil, false, "", err
	}
	objs := make([]Object, len(out.Contents))
	for i, item := range out.Contents {
		size_, _ := strconv.ParseInt(item.Size, 10, 64)
		objs[i] = &obj{item.Key, size_, time.Unix(int64(item.LastModified), 0), strings.HasSuffix(item.Key, "/"), ""}
	}
	if delimiter != "" {
		for _, item := range out.CommonPrefixes {
			objs = append(objs, &obj{item.Prefix, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	// This is a bug in ufile, NextMarker is not the last one after sorting.
	var lastKey string
	if len(objs) > 0 {
		lastKey = objs[len(objs)-1].Key()
	}
	return objs, out.IsTruncated, lastKey, nil
}

type ufileCreateMultipartUploadResult struct {
	UploadId string
	BlkSize  int
	Bucket   string
	Key      string
}

func (u *ufile) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	resp, err := u.request("POST", key+"?uploads", nil, nil)
	if err != nil {
		return nil, err
	}
	var out ufileCreateMultipartUploadResult
	if err := u.parseResp(resp, &out); err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: out.UploadId, MinPartSize: out.BlkSize, MaxCount: 1000000}, nil
}

func (u *ufile) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	// UFile require the PartNumber to start from 0 (continuous)
	num--
	path := fmt.Sprintf("%s?uploadId=%s&partNumber=%d", key, uploadID, num)
	resp, err := u.request("PUT", path, bytes.NewReader(data), nil)
	if err != nil {
		return nil, err
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("UploadPart: %s", parseError(resp).Error())
	}
	etags := resp.Header["Etag"]
	if len(etags) < 1 {
		return nil, errors.New("No ETag")
	}
	return &Part{Num: num, Size: len(data), ETag: strings.Trim(etags[0], "\"")}, nil
}

func (u *ufile) AbortUpload(key string, uploadID string) {
	_, _ = u.request("DELETE", key+"?uploads="+uploadID, nil, nil)
}

func (u *ufile) CompleteUpload(key string, uploadID string, parts []*Part) error {
	etags := make([]string, len(parts))
	for i, p := range parts {
		etags[i] = p.ETag
	}
	resp, err := u.request("POST", key+"?uploadId="+uploadID, bytes.NewReader([]byte(strings.Join(etags, ","))), nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return fmt.Errorf("CompleteMultipart: %s", parseError(resp).Error())
	}
	return nil
}

type ufileUpload struct {
	FileName  string
	UploadId  string
	StartTime int
}

type ufileListMultipartUploadsResult struct {
	RetCode    int
	ErrMsg     string
	NextMarker string
	DataSet    []*ufileUpload
}

func (u *ufile) ListUploads(marker string) ([]*PendingPart, string, error) {
	query := url.Values{}
	query.Add("muploadid", "")
	query.Add("prefix", "")
	query.Add("marker", marker)
	query.Add("limit", strconv.Itoa(1000))
	resp, err := u.request("GET", "?"+query.Encode(), nil, nil)
	if err != nil {
		return nil, "", err
	}
	var out ufileListMultipartUploadsResult
	// FIXME: invalid auth
	if err := u.parseResp(resp, &out); err != nil {
		return nil, "", err
	}
	if out.RetCode != 0 {
		return nil, "", errors.New(out.ErrMsg)
	}
	parts := make([]*PendingPart, len(out.DataSet))
	for i, u := range out.DataSet {
		parts[i] = &PendingPart{u.FileName, u.UploadId, time.Unix(int64(u.StartTime), 0)}
	}
	return parts, out.NextMarker, nil
}

func newUFile(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	return &ufile{RestfulStorage{DefaultObjectStorage{}, endpoint, accessKey, secretKey, "UCloud", ufileSigner}}, nil
}

func init() {
	Register("ufile", newUFile)
}
