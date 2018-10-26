// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type COS struct {
	RestfulStorage
}

func hmacWithSha1(key string, d []byte) string {
	h := hmac.New(sha1.New, []byte(key))
	h.Write(d)
	return hex.EncodeToString(h.Sum(nil))
}

func buildParams(req *http.Request) (string, string) {
	keys := make([]string, 0)
	query := req.URL.Query()
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	paramNames := strings.Join(keys, ";")
	values := make([]string, 0)
	for _, k := range keys {
		values = append(values, k+"="+url.QueryEscape(query[k][0]))
	}
	params := strings.Join(values, "&")
	return paramNames, params
}

func cosSigner(req *http.Request, accessKey, secretKey, signName string) {
	now := time.Now().Unix()
	signtime := fmt.Sprintf("%d;%d", now, now+3600)
	header := "host"

	signKey := hmacWithSha1(secretKey, []byte(signtime))
	headers := fmt.Sprintf("host=%s", req.Host)
	httpString := fmt.Sprintf("%s\n%s\n%s\n%s\n", strings.ToLower(req.Method), req.URL.Path,
		"", headers)
	h := sha1.New()
	h.Write([]byte(httpString))
	sha1Hex := hex.EncodeToString(h.Sum(nil))
	toSign := fmt.Sprintf("sha1\n%s\n%s\n", signtime, sha1Hex)
	sign := hmacWithSha1(signKey, []byte(toSign))
	auth := strings.Join([]string{
		"q-sign-algorithm=sha1",
		"q-ak=" + accessKey,
		"q-sign-time=" + signtime,
		"q-key-time=" + signtime,
		"q-header-list=" + header,
		"q-url-param-list=" + "",
		"q-signature=" + sign,
	}, "&")
	req.Header.Add("Authorization", auth)
}

func (c *COS) Create() error {
	resp, err := c.request("PUT", "", nil, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 201 && resp.StatusCode != 200 && resp.StatusCode != 409 {
		return parseError(resp)
	}
	return nil
}

func (c *COS) Copy(dst, src string) error {
	uri, _ := url.ParseRequestURI(c.endpoint)
	source := fmt.Sprintf("%s/%s", uri.Host, src)
	resp, err := c.request("PUT", dst, nil, map[string]string{
		"x-cos-copy-source": source,
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

func (c *COS) parseResult(resp *http.Response, out interface{}) error {
	defer resp.Body.Close()
	data := make([]byte, resp.ContentLength)
	if _, err := io.ReadFull(resp.Body, data); err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("status: %v, message: %s", resp.StatusCode, string(data))
	}
	err := xml.Unmarshal(data, out)
	if err != nil {
		return err
	}
	return nil
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
	CommonPrefixes string
}

func (c *COS) List(prefix, marker string, limit int64) ([]*Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	path := fmt.Sprintf("?prefix=%s&marker=%s&max-keys=%d", prefix, marker, limit)
	resp, err := c.request("GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	var out ListBucketResult
	if err := c.parseResult(resp, &out); err != nil {
		return nil, err
	}
	objs := make([]*Object, len(out.Contents))
	for i, item := range out.Contents {
		mtime := int(item.LastModified.Unix())
		objs[i] = &Object{item.Key, item.Size, mtime, mtime}
	}
	return objs, nil
}

type cosInitiateMultipartUploadResult struct {
	Bucket   string
	Key      string
	UploadId string
}

func (c *COS) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	resp, err := c.request("POST", key+"?uploads", nil, nil)
	if err != nil {
		return nil, err
	}
	var out cosInitiateMultipartUploadResult
	if err := c.parseResult(resp, &out); err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: out.UploadId, MinPartSize: 1 << 20, MaxCount: 10000}, nil
}

func (c *COS) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	path := fmt.Sprintf("%s?uploadId=%s&partNumber=%d", key, uploadID, num)
	resp, err := c.request("PUT", path, bytes.NewReader(data), nil)
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

func (c *COS) AbortUpload(key string, uploadID string) {
	c.request("DELETE", key+"?uploadId="+uploadID, nil, nil)
}

type cosPart struct {
	PartNumber int
	ETag       string
}

type CompleteMultipartUpload struct {
	Part []cosPart
}

func (c *COS) CompleteUpload(key string, uploadID string, parts []*Part) error {
	param := CompleteMultipartUpload{
		Part: make([]cosPart, len(parts)),
	}
	for i, p := range parts {
		param.Part[i] = cosPart{p.Num, p.ETag}
	}
	body, _ := xml.Marshal(param)
	resp, err := c.request("POST", key+"?uploadId="+uploadID, bytes.NewReader(body), nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return fmt.Errorf("CompleteMultipart: %s", parseError(resp).Error())
	}
	return nil
}

type cosUpload struct {
	Key       string
	UploadID  string
	Initiated time.Time
}

type cosListMultipartUploadsResult struct {
	Bucket        string
	KeyMarker     string
	NextKeyMarker string
	Upload        []cosUpload
}

func (c *COS) ListUploads(marker string) ([]*PendingPart, string, error) {
	resp, err := c.request("GET", "?uploads&key-marker="+marker, nil, nil)
	if err != nil {
		return nil, "", err
	}
	var out cosListMultipartUploadsResult
	if err := c.parseResult(resp, &out); err != nil {
		return nil, "", err
	}
	parts := make([]*PendingPart, len(out.Upload))
	for i, u := range out.Upload {
		parts[i] = &PendingPart{u.Key, u.UploadID, u.Initiated}
	}
	return parts, out.NextKeyMarker, nil
}

func newCOS(endpoint, accessKey, secretKey string) ObjectStorage {
	return &COS{RestfulStorage{
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		signer:    cosSigner,
	}}
}

func init() {
	register("cos", newCOS)
}
