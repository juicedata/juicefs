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
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/assert"
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

const extendQ = "?verify-checksum=extend"

func TestS3RangeChecksum(t *testing.T) {
	data := make([]byte, 3*ChunkChecksumSize+123)
	utils.RandRead(data)
	full := strconv.FormatUint(uint64(crc32.Checksum(data, crc32c)), 10)
	chunks := base64.StdEncoding.EncodeToString(chunkChecksums(data))
	var metadata string
	var corrupt int
	var legacy, wrongRange, truncate bool
	var requestRange string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestRange = r.Header.Get("Range")
		start, end := int64(0), int64(len(data))
		if requestRange != "" {
			last := end - 1
			_, _ = fmt.Sscanf(requestRange, "bytes=%d-%d", &start, &last)
			if last+1 < end {
				end = last + 1
			}
			cr := fmt.Sprintf("bytes %d-%d/%d", start, end-1, len(data))
			if wrongRange {
				cr = fmt.Sprintf("bytes %d-%d/%d", start+1, end, len(data))
			}
			w.Header().Set("Content-Range", cr)
		}
		if metadata != "" {
			w.Header().Set("x-amz-meta-crc32c-chunks", metadata)
		}
		if legacy {
			w.Header().Set("x-amz-meta-crc32c", full)
		}
		body := append([]byte(nil), data[start:end]...)
		if corrupt > 0 {
			body[corrupt-1] ^= 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if truncate {
			body = body[:len(body)-7]
		}
		if requestRange != "" {
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	for _, c := range []struct {
		name, query, chunks          string
		off, limit                   int64
		corrupt                      int // 1-based offset into the response body, 0 = none
		legacy, wrongRange, truncate bool
		wantErr                      bool
		wantRange                    string
	}{
		{name: "full chunks", query: "?verify-checksum=full", chunks: chunks, limit: -1},
		{name: "full partial unverified", query: "?verify-checksum=full", chunks: "invalid", off: 7, limit: 10, wantRange: "bytes=7-16"},
		{name: "extend partial", query: extendQ, chunks: chunks, off: ChunkChecksumSize + 7, limit: 10, wantRange: "bytes=32768-65535"},
		{name: "extend to EOF", query: extendQ, chunks: chunks, off: ChunkChecksumSize + 7, limit: -1, wantRange: "bytes=32768-"},
		{name: "extend tail clamped", query: extendQ, chunks: chunks, off: int64(len(data)) - 10, limit: 100, wantRange: "bytes=98304-131071"},
		{name: "absent chunks unverified", query: extendQ, off: 7, limit: 10, wantRange: "bytes=0-32767"},
		{name: "legacy full fallback", query: "?verify-checksum=full", legacy: true, limit: -1},
		{name: "corrupt payload", query: "?verify-checksum=full", chunks: chunks, corrupt: 1, limit: -1, wantErr: true},
		{name: "corrupt head padding", query: extendQ, chunks: chunks, off: 7, limit: 10, corrupt: 1, wantRange: "bytes=0-32767", wantErr: true},
		{name: "corrupt final partial chunk", query: extendQ, chunks: chunks, off: int64(len(data)) - 10, limit: 100, corrupt: 123, wantRange: "bytes=98304-131071", wantErr: true},
		{name: "truncated body", query: extendQ, chunks: chunks, off: 7, limit: 10, truncate: true, wantRange: "bytes=0-32767", wantErr: true},
		{name: "truncated table", query: extendQ, chunks: base64.StdEncoding.EncodeToString(chunkChecksums(data)[:4]), off: 7, limit: 10, wantRange: "bytes=0-32767", wantErr: true},
		{name: "wrong content range", query: extendQ, off: 7, limit: 10, wrongRange: true, wantRange: "bytes=0-32767", wantErr: true},
		{name: "checksum disabled", query: "?verify-checksum=none", chunks: "invalid", off: 7, limit: 10, wantRange: "bytes=7-16"},
	} {
		t.Run(c.name, func(t *testing.T) {
			metadata, corrupt, legacy, wrongRange, truncate = c.chunks, c.corrupt, c.legacy, c.wrongRange, c.truncate
			storage, err := newS3(server.URL+"/bucket"+c.query, "access", "secret", "")
			if err != nil {
				t.Fatal(err)
			}
			r, err := storage.Get(context.Background(), "key", c.off, c.limit)
			var got []byte
			if err == nil {
				got, err = io.ReadAll(r)
				_ = r.Close()
			}
			if (err != nil) != c.wantErr {
				t.Fatalf("error %v, wantErr %v", err, c.wantErr)
			}
			if requestRange != c.wantRange {
				t.Fatalf("Range %q, want %q", requestRange, c.wantRange)
			}
			if !c.wantErr {
				want := data[c.off:]
				if c.limit > 0 && c.limit < int64(len(want)) {
					want = want[:c.limit]
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("returned %d incorrect bytes", len(got))
				}
			}
		})
	}
	if _, err := newS3(server.URL+"/bucket?verify-checksum=bogus", "access", "secret", ""); err == nil {
		t.Fatal("invalid verify-checksum accepted")
	}
}

func TestS3ChecksumPut(t *testing.T) {
	small := []byte("checksum upload")
	huge := make([]byte, maxChunkChecksums*ChunkChecksumSize+1)
	utils.RandRead(huge)
	fullCRC := func(b []byte) string { return strconv.FormatUint(uint64(crc32.Checksum(b, crc32c)), 10) }
	for _, c := range []struct {
		name, query          string
		data                 []byte
		wantFull, wantChunks string
	}{
		{name: "full", query: "?verify-checksum=full", data: small, wantFull: fullCRC(small)},
		{name: "extend chunked", query: extendQ, data: small, wantChunks: base64.StdEncoding.EncodeToString(chunkChecksums(small))},
		{name: "none", query: "?verify-checksum=none", data: small},
		{name: "disable override", query: "?disable-checksum=true&verify-checksum=extend", data: small},
		{name: "empty falls back to full CRC", query: extendQ, data: nil, wantFull: fullCRC(nil)},
		{name: "oversized falls back to full CRC", query: extendQ, data: huge, wantFull: fullCRC(huge)},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(got, c.data) {
					t.Errorf("incorrect upload body: %d bytes, %v", len(got), err)
				}
				if r.Header.Get("x-amz-meta-crc32c") != c.wantFull || r.Header.Get("x-amz-meta-crc32c-chunks") != c.wantChunks {
					t.Errorf("incorrect checksum metadata: %v", r.Header)
				}
			}))
			defer server.Close()
			storage, err := newS3(server.URL+"/bucket"+c.query, "access", "secret", "")
			if err != nil {
				t.Fatal(err)
			}
			// cover both the bytes.Reader fast path and the streaming ReadSeeker path
			for _, in := range []io.Reader{bytes.NewReader(c.data), struct{ io.ReadSeeker }{bytes.NewReader(c.data)}} {
				if err = storage.Put(context.Background(), "key", in); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
