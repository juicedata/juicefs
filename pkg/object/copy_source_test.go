//go:build !nos3 && !noks3 && !noibmcos

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

	ibmaws "github.com/IBM/ibm-cos-sdk-go/aws"
	ibmcredentials "github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	ibmsession "github.com/IBM/ibm-cos-sdk-go/aws/session"
	ibms3 "github.com/IBM/ibm-cos-sdk-go/service/s3"
	ksaws "github.com/ks3sdklib/aws-sdk-go/aws"
	kscredentials "github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	kss3 "github.com/ks3sdklib/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/require"
)

type copySourceTransport struct {
	header string
}

func (tr *copySourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The KS3 V2 signer lowercases header map keys.
	for name, values := range req.Header {
		if strings.EqualFold(name, "X-Amz-Copy-Source") && len(values) > 0 {
			tr.header = values[0]
		}
	}
	body := `<CopyObjectResult><ETag>"etag"</ETag></CopyObjectResult>`
	if req.URL.Query().Get("uploadId") != "" {
		body = `<CopyPartResult><ETag>"etag"</ETag></CopyPartResult>`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestOtherSDKCopySourceEncoding(t *testing.T) {
	for _, backend := range []string{"ks3", "ibmcos"} {
		for _, tc := range []struct {
			key  string
			want string
		}{
			{"dir/a-b_c.d~e", "bucket/dir/a-b_c.d~e"},
			{"dir/pct%41", "bucket/dir/pct%2541"},
			{"dir/pct%2F", "bucket/dir/pct%252F"},
			{"dir/q?x", "bucket/dir/q%3Fx"},
			{"dir/a b+c#d&e=f", "bucket/dir/a%20b%2Bc%23d%26e%3Df"},
			{"dir/中文", "bucket/dir/%E4%B8%AD%E6%96%87"},
		} {
			t.Run(backend+"/"+tc.key, func(t *testing.T) {
				tr := &copySourceTransport{}
				hc := &http.Client{Transport: tr}
				var store ObjectStorage
				if backend == "ks3" {
					store = &ks3{bucket: "bucket", s3: kss3.New(&ksaws.Config{
						Region: "us-east-1", Endpoint: "localhost", DisableSSL: true,
						S3ForcePathStyle: true, HTTPClient: hc,
						Credentials: kscredentials.NewStaticCredentials("key", "secret", ""),
					})}
				} else {
					sess := ibmsession.Must(ibmsession.NewSession(&ibmaws.Config{
						Region: ibmaws.String("us-east-1"), Endpoint: ibmaws.String("http://localhost"),
						S3ForcePathStyle: ibmaws.Bool(true), HTTPClient: hc,
						Credentials: ibmcredentials.NewStaticCredentials("key", "secret", ""),
					}))
					store = &ibmcos{bucket: "bucket", s3: ibms3.New(sess)}
				}
				t.Run("Copy", func(t *testing.T) {
					err := store.Copy(context.Background(), "destination", tc.key)
					require.NoError(t, err)
					require.Equal(t, tc.want, tr.header)
				})
				if backend == "ks3" {
					t.Run("UploadPartCopy", func(t *testing.T) {
						tr.header = ""
						part, err := store.UploadPartCopy(context.Background(), "destination", "upload", 1, tc.key, 0, 1)
						require.NoError(t, err)
						require.Equal(t, tc.want, tr.header)
						require.Equal(t, "\"etag\"", part.ETag)
					})
				}
			})
		}
	}
}
