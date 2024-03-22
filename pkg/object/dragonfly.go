//go:build !nodragonfly
// +build !nodragonfly

/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/mohae/deepcopy"
)

const (
	// BackendStorageS3 is the backend storage of s3.
	BackendStorageS3 = "s3"

	// BackendStorageOSS is the backend storage of oss.
	BackendStorageOSS = "oss"

	// BackendStorageOBS is the backend storage of obs.
	BackendStorageOBS = "obs"
)

const (
	// FilterHeaderKey is the filter header key of dragonfly.
	FilterHeaderKey = "X-Dragonfly-Filter"
)

const (
	// FilterHeaderS3 is the filter of s3 url for generating task id.
	FilterHeaderS3 = "X-Amz-Algorithm&X-Amz-Credential&X-Amz-Date&X-Amz-Expires&X-Amz-SignedHeaders&X-Amz-Signature"

	// FilterHeaderOSS is the filter of oss url for generating task id.
	FilterHeaderOSS = "Expires&Signature"

	// FilterHeaderOBS is the filter of obs url for generating task id.
	FilterHeaderOBS = "X-Amz-Algorithm&X-Amz-Credential&X-Amz-Date&X-Obs-Date&X-Amz-Expires&X-Amz-SignedHeaders&X-Amz-Signature"
)

// DefaultSignedURLExpire is the default expire time of signed url.
var DefaultSignedURLExpire = 5 * time.Minute

// dragonfly is the dragonfly object storage.
type dragonfly struct {
	// Object storage interface.
	ObjectStorage

	// filterHeader is the filter header of dragonfly.
	filterHeader string

	// http client with dragonfly proxy.
	httpClient *http.Client
}

// Get returns the object if it exists.
func (d *dragonfly) Get(key string, off, limit int64) (io.ReadCloser, error) {
	ss, ok := d.ObjectStorage.(SupportSignedURL)
	if !ok {
		return nil, fmt.Errorf("not support signed url")
	}

	// Parse the signed url.
	signedURL, err := ss.SignedURL(key, DefaultSignedURLExpire)
	if err != nil {
		return nil, err
	}

	url, err := url.Parse(signedURL)
	if err != nil {
		return nil, err
	}
	url.Scheme = "http"

	// Create the request.
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return nil, err
	}

	// Add the filter header.
	req.Header.Add(FilterHeaderKey, d.filterHeader)

	// Add the range header.
	if off > 0 || limit > 0 {
		req.Header.Add("Range", getRange(off, limit))
	}

	// Add the date header.
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Add("Date", now)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		// If Proxy is down, the error is "connection refused" and
		// try to access the backend storage directly.
		if errors.Is(err, syscall.ECONNREFUSED) {
			return d.ObjectStorage.Get(key, off, limit)
		}

		return nil, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, parseError(resp)
	}

	if err = checkGetStatus(resp.StatusCode, req.Header.Get("Range") != ""); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}

	return resp.Body, nil
}

// newDragonfly creates a new dragonfly object storage.
func newDragonfly(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	// Parse the endpoint.
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}

	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	endpoint = endpointURL.Scheme + "://" + endpointURL.Host

	// Parse the dragonfly proxy.
	proxy := endpointURL.Query().Get("proxy")
	if !strings.Contains(proxy, "://") {
		proxy = fmt.Sprintf("http://%s", proxy)
	}

	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, err
	}

	// Parse the backend storage.
	backendStorageName := endpointURL.Query().Get("backendStorage")
	if backendStorageName == "" {
		return nil, fmt.Errorf("backend storage is required")
	}

	// Initialize the backend storage and filter header.
	var (
		backendStorage ObjectStorage
		filterHeader   string
	)
	switch backendStorageName {
	case BackendStorageS3:
		filterHeader = FilterHeaderS3
		backendStorage, err = newS3(endpoint, accessKey, secretKey, token)
		if err != nil {
			return nil, err
		}
	case BackendStorageOSS:
		filterHeader = FilterHeaderOSS
		backendStorage, err = newOSS(endpoint, accessKey, secretKey, token)
		if err != nil {
			return nil, err
		}
	case BackendStorageOBS:
		filterHeader = FilterHeaderOBS
		backendStorage, err = newOBS(endpoint, accessKey, secretKey, token)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("not supported backend storage: %s", backendStorage)
	}

	// Create the http client with dragonfly proxy.
	httpClient := deepcopy.Copy(httpClient).(*http.Client)
	httpClient.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)

	return &dragonfly{
		ObjectStorage: backendStorage,
		filterHeader:  filterHeader,
		httpClient:    httpClient,
	}, nil
}

// init registers the dragonfly object storage.
func init() {
	Register("dragonfly", newDragonfly)
}
