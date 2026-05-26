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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

func TestS3CopyPlanBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		size      int64
		wantParts int
		wantLast  int64 // expected size of the last part
	}{
		{name: "zero", size: 0, wantParts: 0},
		{name: "1 byte", size: 1, wantParts: 1, wantLast: 1},
		{name: "exact part", size: s3CopyPartSize, wantParts: 1, wantLast: s3CopyPartSize},
		{name: "just over CopyObject limit", size: s3CopyObjectMaxSize + 1, wantParts: 6, wantLast: 1},
		{name: "exactly 6 parts", size: 6 * s3CopyPartSize, wantParts: 6, wantLast: s3CopyPartSize},
		{name: "five tib (S3 max)", size: 5 << 40, wantParts: 5120, wantLast: s3CopyPartSize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := s3CopyPlan(tc.size)
			if len(plan) != tc.wantParts {
				t.Fatalf("len(plan)=%d, want %d", len(plan), tc.wantParts)
			}
			if tc.wantParts == 0 {
				return
			}
			// Plan must be contiguous and cover exactly [0, size).
			var covered int64
			for i, seg := range plan {
				if seg[0] != covered {
					t.Fatalf("part %d offset=%d, expected %d", i, seg[0], covered)
				}
				if seg[1] <= 0 || seg[1] > s3CopyPartSize {
					t.Fatalf("part %d size=%d out of range (0, %d]", i, seg[1], s3CopyPartSize)
				}
				covered += seg[1]
			}
			if covered != tc.size {
				t.Fatalf("plan covers %d bytes, expected %d", covered, tc.size)
			}
			if plan[len(plan)-1][1] != tc.wantLast {
				t.Fatalf("last part size=%d, want %d", plan[len(plan)-1][1], tc.wantLast)
			}
			// Part count must always stay under the S3 hard limit so
			// CompleteMultipartUpload doesn't reject it.
			if len(plan) > 10000 {
				t.Fatalf("plan has %d parts, exceeds S3 limit of 10000", len(plan))
			}
		})
	}
}

func TestS3CopyObjectMaxSizeBoundary(t *testing.T) {
	// Anything <= 5 GiB stays on CopyObject; anything strictly larger
	// goes through UploadPartCopy. Locks in the boundary so a future
	// refactor cannot silently shift it.
	if s3CopyObjectMaxSize != 5<<30 {
		t.Fatalf("s3CopyObjectMaxSize=%d, want %d (5 GiB)", s3CopyObjectMaxSize, int64(5<<30))
	}
}

func TestIsS3NotFound(t *testing.T) {
	// Typed not-found error from the v2 SDK.
	if !isS3NotFound(&types.NoSuchKey{}) {
		t.Errorf("types.NoSuchKey not recognised")
	}

	// Generic smithy APIError with the canonical code.
	if !isS3NotFound(&smithy.GenericAPIError{Code: "NoSuchKey", Message: "The specified key does not exist."}) {
		t.Errorf("smithy.GenericAPIError{Code: NoSuchKey} not recognised")
	}
	if !isS3NotFound(&smithy.GenericAPIError{Code: "NotFound"}) {
		t.Errorf("smithy.GenericAPIError{Code: NotFound} not recognised (e.g. HeadObject result)")
	}

	// Gateway 404 with no NoSuchKey code — the case the previous
	// substring check missed.
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      errors.New("gateway returned 404"),
	}
	if !isS3NotFound(resp404) {
		t.Errorf("smithyhttp.ResponseError with 404 status not recognised")
	}

	// Unrelated errors must NOT be flagged as not-found.
	if isS3NotFound(nil) {
		t.Errorf("nil error must not be flagged as not-found")
	}
	if isS3NotFound(errors.New("connection reset")) {
		t.Errorf("opaque non-API error flagged as not-found")
	}
	if isS3NotFound(&smithy.GenericAPIError{Code: "AccessDenied"}) {
		t.Errorf("AccessDenied incorrectly flagged as not-found")
	}
	resp500 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 500}},
		Err:      errors.New("internal"),
	}
	if isS3NotFound(resp500) {
		t.Errorf("500 status incorrectly flagged as not-found")
	}

	// Wrapped errors must still match — the whole point of using
	// errors.As over substring matching.
	wrapped := fmt.Errorf("delete object: %w", &types.NoSuchKey{})
	if !isS3NotFound(wrapped) {
		t.Errorf("wrapped types.NoSuchKey not recognised through errors.As")
	}
}

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
