//go:build !nos3 && !noqiniu && !noqingstore && !nodragonfly
// +build !nos3,!noqiniu,!noqingstore,!nodragonfly

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
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qiniu/go-sdk/v7/auth"
	qiniuclient "github.com/qiniu/go-sdk/v7/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userAgentTransport struct {
	requests chan *http.Request
	status   int
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests <- req.Clone(req.Context())
	return &http.Response{
		StatusCode: t.status,
		Status:     http.StatusText(t.status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func captureUserAgent(t *testing.T, status int, request func()) string {
	t.Helper()
	oldClient := httpClient
	transport := &userAgentTransport{requests: make(chan *http.Request, 32), status: status}
	httpClient = &http.Client{Transport: transport}
	t.Cleanup(func() { httpClient = oldClient })

	request()
	var req *http.Request
	select {
	case req = <-transport.requests:
	case <-time.After(time.Second):
		t.Fatal("no HTTP request was captured")
		return ""
	}
	for {
		select {
		case req = <-transport.requests:
		default:
			return req.Header.Get("User-Agent")
		}
	}
}

func TestS3CompatibleBackendsSendUserAgent(t *testing.T) {
	oldUserAgent := UserAgent
	UserAgent = "JuiceFS-test"
	t.Cleanup(func() { UserAgent = oldUserAgent })

	tests := []struct {
		name     string
		endpoint string
		create   Creator
	}{
		{"s3", "https://s3.example.com/bucket", newS3},
		{"minio", "https://minio.example.com/bucket", newMinio},
		{"wasabi", "https://bucket.s3.us-east-1.wasabisys.com", newWasabi},
		{"scw", "https://bucket.s3.fr-par.scw.cloud", newScw},
		{"oos", "https://bucket.oos-cn-beijing.ctyunapi.cn", newOOS},
		{"eos", "https://bucket.eos.example.com", newEos},
		{"space", "https://bucket.nyc3.digitaloceanspaces.com", newSpace},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userAgent := captureUserAgent(t, http.StatusNotFound, func() {
				store, err := test.create(test.endpoint, "access-key", "secret-key", "")
				require.NoError(t, err)
				_, _ = store.Head(context.Background(), "key")
			})
			assert.Contains(t, userAgent, UserAgent)
		})
	}
}

func TestS3RegionProbeDoesNotSendUserAgent(t *testing.T) {
	oldUserAgent := UserAgent
	UserAgent = "JuiceFS-test"
	t.Cleanup(func() { UserAgent = oldUserAgent })
	t.Setenv("AWS_DEFAULT_REGION", awsDefaultRegion)

	userAgent := captureUserAgent(t, http.StatusNotFound, func() {
		_, _ = autoS3Region("bucket", "access-key", "secret-key", "")
	})
	assert.NotContains(t, userAgent, UserAgent)
}

func TestQiniuPrivateDownloadSendsUserAgent(t *testing.T) {
	oldUserAgent := UserAgent
	UserAgent = "JuiceFS-test"
	t.Cleanup(func() { UserAgent = oldUserAgent })
	t.Setenv("QINIU_DOMAIN", "https://cdn.example.com")

	userAgent := captureUserAgent(t, http.StatusOK, func() {
		store := &qiniu{cred: auth.New("access-key", "secret-key")}
		body, err := store.download(context.Background(), "key", 0, -1)
		require.NoError(t, err)
		require.NoError(t, body.Close())
	})
	assert.Contains(t, userAgent, UserAgent)
}

func TestQiniuNativeClientSendsUserAgent(t *testing.T) {
	oldUserAgent := UserAgent
	oldQiniuUserAgent := qiniuclient.UserAgent
	UserAgent = "JuiceFS-test"
	t.Cleanup(func() {
		UserAgent = oldUserAgent
		qiniuclient.UserAgent = oldQiniuUserAgent
	})

	userAgent := captureUserAgent(t, http.StatusNotFound, func() {
		oldClient := qiniuclient.DefaultClient.Client
		qiniuclient.DefaultClient.Client = httpClient
		defer func() { qiniuclient.DefaultClient.Client = oldClient }()

		_, err := newQiniu("https://bucket.s3-cn-east-1.qiniucs.com", "access-key", "secret-key", "")
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://qiniu.example.com", nil)
		require.NoError(t, err)
		resp, err := qiniuclient.DefaultClient.Do(context.Background(), req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	})
	assert.Contains(t, userAgent, UserAgent)
}

func TestNativeHTTPBackendsSendUserAgent(t *testing.T) {
	oldUserAgent := UserAgent
	UserAgent = "JuiceFS-test"
	t.Cleanup(func() { UserAgent = oldUserAgent })

	tests := []struct {
		name     string
		endpoint string
		create   Creator
	}{
		{"qingstor", "https://bucket.zone.qingstor.com", newQingStor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userAgent := captureUserAgent(t, http.StatusNotFound, func() {
				store, err := test.create(test.endpoint, "access-key", "secret-key", "")
				require.NoError(t, err)
				_, _ = store.Head(context.Background(), "key")
			})
			assert.Contains(t, userAgent, UserAgent)
		})
	}

	t.Run("dragonfly", func(t *testing.T) {
		userAgent := captureUserAgent(t, http.StatusNotFound, func() {
			store := &dragonfly{endpoint: "https://dragonfly.example.com", bucket: "bucket", client: httpClient}
			_, _ = store.Head(context.Background(), "key")
		})
		assert.Equal(t, UserAgent, userAgent)
	})
}
