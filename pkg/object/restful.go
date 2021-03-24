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
	rand.Seed(time.Now().Unix())
	httpClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   time.Second * 20,
			ResponseHeaderTimeout: time.Second * 30,
			IdleConnTimeout:       time.Second * 300,
			MaxIdleConnsPerHost:   500,
			Dial: func(network string, address string) (net.Conn, error) {
				separator := strings.LastIndex(address, ":")
				host := address[:separator]
				port := address[separator:]
				ips, err := resolver.Fetch(host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("No such host: %s", host)
				}
				var conn net.Conn
				n := len(ips)
				first := rand.Intn(n)
				dialer := &net.Dialer{Timeout: time.Second * 10}
				for i := 0; i < n; i++ {
					ip := ips[(first+i)%n]
					address = ip.String()
					if port != "" {
						address = net.JoinHostPort(address, port[1:])
					}
					conn, err = dialer.Dial(network, address)
					if err == nil {
						return conn, nil
					}
				}
				return nil, err
			},
			DisableCompression: true,
		},
		Timeout: time.Hour,
	}
}

func cleanup(response *http.Response) {
	if response != nil && response.Body != nil {
		_, _ = ioutil.ReadAll(response.Body)
		_ = response.Body.Close()
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
	_, _ = h.Write([]byte(toSign))
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

func (s *RestfulStorage) Head(key string) (Object, error) {
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
	return &obj{
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
	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		return parseError(resp)
	}
	return nil
}

func (s *RestfulStorage) List(prefix, marker string, limit int64) ([]Object, error) {
	return nil, errors.New("Not implemented")
}

var _ ObjectStorage = &RestfulStorage{}
