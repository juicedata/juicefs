// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viki-org/dnscache"
)

var resolver = dnscache.New(time.Minute)
var httpClient *http.Client

func init() {
	httpClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   time.Second * 20,
			ResponseHeaderTimeout: time.Second * 30,
			IdleConnTimeout:       time.Second * 60,
			MaxIdleConnsPerHost:   100,
			Dial: func(network string, address string) (net.Conn, error) {
				separator := strings.LastIndex(address, ":")
				host := address[:separator]
				ips, err := resolver.Fetch(host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("No such host: %s", host)
				}
				ip := ips[rand.Intn(len(ips))]
				dialer := &net.Dialer{Timeout: time.Second * 10}
				return dialer.Dial("tcp", ip.String()+address[separator:])
			},
			DisableCompression: true,
		},
		Timeout: time.Hour,
	}
}

func cleanup(response *http.Response) {
	if response != nil && response.Body != nil {
		ioutil.ReadAll(response.Body)
		response.Body.Close()
	}
}

type RestfulStorage struct {
	DefaultObjectStorage
	endpoint  string
	accessKey string
	secretKey string
	signName  string
	signer    func(*http.Request, string, string, string)
}

func (s *RestfulStorage) String() string {
	return s.endpoint
}

var HEADER_NAMES = []string{"Content-MD5", "Content-Type", "Date"}

// RequestURL is fully url of api request
func sign(req *http.Request, accessKey, secretKey, signName string) {
	if accessKey == "" {
		return
	}
	toSign := req.Method + "\n"
	for _, n := range HEADER_NAMES {
		toSign += req.Header.Get(n) + "\n"
	}
	bucket := strings.Split(req.URL.Host, ".")[0]
	toSign += "/" + bucket + req.URL.Path
	h := hmac.New(sha1.New, []byte(secretKey))
	h.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(h.Sum(nil))
	token := signName + " " + accessKey + ":" + sig
	req.Header.Add("Authorization", token)
}

func (s *RestfulStorage) request(method, key string, body io.Reader, headers map[string]string) (*http.Response, error) {
	uri := s.endpoint + "/" + key
	req, err := http.NewRequest(method, uri, body)
	if err != nil {
		return nil, err
	}
	if f, ok := body.(*os.File); ok {
		st, err := f.Stat()
		if err == nil {
			req.ContentLength = st.Size()
		}
	}
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)
	for key := range headers {
		req.Header.Add(key, headers[key])
	}
	s.signer(req, s.accessKey, s.secretKey, s.signName)
	return httpClient.Do(req)
}

func parseError(resp *http.Response) error {
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("request failed: %s", err)
	}
	return fmt.Errorf("status: %v, message: %s", resp.StatusCode, string(data))
}

func (s *RestfulStorage) Head(key string) (*Object, error) {
	resp, err := s.request("HEAD", key, nil, nil)
	if err != nil {
		return nil, err
	}
	defer cleanup(resp)
	if resp.StatusCode != 200 {
		return nil, parseError(resp)
	}

	lastModified := resp.Header.Get("Last-Modified")
	if lastModified == "" {
		return nil, fmt.Errorf("cannot get last modified time")
	}
	mtime, _ := time.Parse(time.RFC1123, lastModified)
	return &Object{
		key,
		resp.ContentLength,
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (s *RestfulStorage) Get(key string, off, limit int64) (io.ReadCloser, error) {
	headers := make(map[string]string)
	if off > 0 || limit > 0 {
		if limit > 0 {
			headers["Range"] = fmt.Sprintf("bytes=%d-%d", off, off+limit-1)
		} else {
			headers["Range"] = fmt.Sprintf("bytes=%d-", off)
		}
	}
	resp, err := s.request("GET", key, nil, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, parseError(resp)
	}
	return resp.Body, nil
}

func (u *RestfulStorage) Put(key string, body io.Reader) error {
	resp, err := u.request("PUT", key, body, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) Copy(dst, src string) error {
	in, err := s.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer in.Close()
	d, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	return s.Put(dst, bytes.NewReader(d))
}

func (s *RestfulStorage) Delete(key string) error {
	resp, err := s.request("DELETE", key, nil, nil)
	if err != nil {
		return err
	}
	defer cleanup(resp)
	if resp.StatusCode != 204 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, errors.New("Not implemented")
}

var _ ObjectStorage = &RestfulStorage{}
