// Copyright (C) 2018-present Juicedata Inc.

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

func (s *speedy) String() string {
	uri, _ := url.ParseRequestURI(s.endpoint)
	return fmt.Sprintf("speedy://%s", uri.Host)
}

func (s *speedy) List(prefix, marker string, limit int64) ([]*Object, error) {
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
	objs := make([]*Object, 0)
	for _, item := range out.Contents {
		if strings.HasSuffix(item.Key, "/.speedycloud_dir_flag") {
			continue
		}
		objs = append(objs, &Object{item.Key, item.Size, item.LastModified})
	}
	return objs, nil
}

func newSpeedy(endpoint, accessKey, secretKey string) ObjectStorage {
	return &speedy{RestfulStorage{
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		signName:  "AWS",
		signer:    sign,
	}}
}

func init() {
	register("speedy", newSpeedy)
}
