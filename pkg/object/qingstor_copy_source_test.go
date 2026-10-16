//go:build !noqingstor

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

	"github.com/qingstor/qingstor-sdk-go/v4/config"
	qs "github.com/qingstor/qingstor-sdk-go/v4/service"
	"github.com/stretchr/testify/require"
)

type qingstorCopyTransport struct{ header string }

func (tr *qingstorCopyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.header = req.Header.Get("X-QS-Copy-Source")
	return &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}, "Etag": {"\"etag\""}},
		Body:       io.NopCloser(strings.NewReader("{}")), Request: req,
	}, nil
}

func TestQingStorCopySourceEncoding(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"dir/a-b_c.d~e", "/bucket/dir/a-b_c.d~e"},
		{"dir/pct%41", "/bucket/dir/pct%2541"},
		{"dir/pct%2F", "/bucket/dir/pct%252F"},
		{"dir/q?x", "/bucket/dir/q%3Fx"},
		{"dir/a b+c#d&e=f", "/bucket/dir/a%20b%2Bc%23d%26e%3Df"},
		{"dir/中文", "/bucket/dir/%E4%B8%AD%E6%96%87"},
		{"dir/中文%41?x", "/bucket/dir/%E4%B8%AD%E6%96%87%2541%3Fx"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			tr := &qingstorCopyTransport{}
			conf, err := config.New("key", "secret")
			require.NoError(t, err)
			conf.Connection = &http.Client{Transport: tr}
			service, err := qs.Init(conf)
			require.NoError(t, err)
			bucket, err := service.Bucket("bucket", "pek3a")
			require.NoError(t, err)
			store := &qingstor{bucket: bucket}
			t.Run("Copy", func(t *testing.T) {
				require.NoError(t, store.Copy(context.Background(), "destination", tc.key))
				require.Equal(t, tc.want, tr.header)
			})
			t.Run("UploadPartCopy", func(t *testing.T) {
				tr.header = ""
				part, err := store.UploadPartCopy(context.Background(), "destination", "upload", 1, tc.key, 0, 1)
				require.NoError(t, err)
				require.Equal(t, tc.want, tr.header)
				require.Equal(t, "etag", part.ETag)
			})
		})
	}
}
