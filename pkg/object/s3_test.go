//go:build !nos3
// +build !nos3

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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_s3client_full_string(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
	}{
		{endpoint: "s3.compatible.site/bucket", want: "s3://s3.compatible.site/bucket/"},
		{endpoint: "http://s3.compatible.site/bucket", want: "s3://s3.compatible.site/bucket/"},
		{endpoint: "s3://s3.compatible.site/bucket", want: "s3://s3.compatible.site/bucket/"},
		{endpoint: "https://mybucket.s3.us-east-2.amazonaws.com", want: "s3://mybucket/"},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			stor, err := newS3(tt.endpoint, "", "", "")
			if err != nil {
				t.Fatalf("newS3() error = %v", err)
			}
			assert.Equalf(t, tt.want, stor.String(), "Display full address of s3 compatible object storage")
		})
	}
}

func TestS3OCIRegion(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      string
		wantBucket    string
		wantRegion    string
		wantPathStyle bool
	}{
		{
			name:          "legacy endpoint",
			endpoint:      "bucket.namespace.compat.objectstorage.ap-singapore-1.oraclecloud.com",
			wantBucket:    "bucket",
			wantRegion:    "ap-singapore-1",
			wantPathStyle: true,
		},
		{
			name:          "dedicated endpoint",
			endpoint:      "https://prod-sandbox-juicefs.axywvpcvts33.compat.objectstorage.us-sanjose-1.oci.customer-oci.com",
			wantBucket:    "prod-sandbox-juicefs",
			wantRegion:    "us-sanjose-1",
			wantPathStyle: true,
		},
		{
			name:          "path style endpoint",
			endpoint:      "https://axywvpcvts33.compat.objectstorage.us-sanjose-1.oci.customer-oci.com/prod-sandbox-juicefs",
			wantBucket:    "prod-sandbox-juicefs",
			wantRegion:    "us-sanjose-1",
			wantPathStyle: true,
		},
		{
			name:          "virtual hosted style endpoint",
			endpoint:      "https://prod-sandbox-juicefs.vhcompat.objectstorage.us-sanjose-1.oci.customer-oci.com",
			wantBucket:    "prod-sandbox-juicefs",
			wantRegion:    "us-sanjose-1",
			wantPathStyle: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_REGION", "eu-frankfurt-1")
			t.Setenv("AWS_DEFAULT_REGION", "eu-frankfurt-1")
			stor, err := newS3(tt.endpoint, "", "", "")
			if err != nil {
				t.Fatalf("newS3() error = %v", err)
			}
			client := stor.(*s3client)
			assert.Equal(t, tt.wantBucket, client.bucket)
			assert.Equal(t, tt.wantRegion, client.region)
			assert.Equal(t, tt.wantPathStyle, client.s3.Options().UsePathStyle)
		})
	}
}

func Test_s3client_copy_source_encoding(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("X-Amz-Copy-Source")
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("uploadId") != "" {
			_, _ = w.Write([]byte(`<CopyPartResult><ETag>"etag"</ETag></CopyPartResult>`))
		} else {
			_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"etag"</ETag></CopyObjectResult>`))
		}
	}))
	defer srv.Close()

	stor, err := newS3(srv.URL+"/bucket", "ak", "sk", "")
	require.NoError(t, err)
	s3c := stor.(*s3client)
	ctx := context.Background()

	tests := []struct {
		key  string
		want string
	}{
		{key: "jfs/chunks/0/0/1_0_4194304", want: "bucket/jfs/chunks/0/0/1_0_4194304"},
		{key: "dir/a-b_c.d~e", want: "bucket/dir/a-b_c.d~e"},
		{key: "dir/a b+c%d?e#f&g=h", want: "bucket/dir/a%20b%2Bc%25d%3Fe%23f%26g%3Dh"},
		{key: "dir/café.txt", want: "bucket/dir/caf%C3%A9.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			require.NoError(t, s3c.Copy(ctx, "dst", tt.key))
			assert.Equal(t, tt.want, <-got, "Copy")

			_, err := s3c.UploadPartCopy(ctx, "dst", "upload", 1, tt.key, 0, 1)
			require.NoError(t, err)
			assert.Equal(t, tt.want, <-got, "UploadPartCopy")
		})
	}
}
