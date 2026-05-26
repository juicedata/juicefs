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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

// readerOnly hides any ReadSeeker / ReadCloser methods an underlying
// reader might satisfy so seekableBody is forced down its non-seeker
// branch. Used by the tests below.
type readerOnly struct{ r io.Reader }

func (r readerOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

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

func TestSeekableBodyFastPathForSeekers(t *testing.T) {
	// A reader that already satisfies io.ReadSeeker must be returned
	// unchanged (no buffering, no temp file). This is the hot path
	// for the typical JuiceFS Put where the body is a *bytes.Reader.
	src := bytes.NewReader([]byte("hello"))
	body, cleanup, err := seekableBody(src)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	defer cleanup()
	if body != src {
		t.Fatalf("expected the original *bytes.Reader to be returned, got a copy")
	}
}

func TestSeekableBodyInMemoryPath(t *testing.T) {
	// Body strictly smaller than the threshold stays in memory.
	want := bytes.Repeat([]byte{0xab}, s3SeekableBodyThreshold/2)
	body, cleanup, err := seekableBody(readerOnly{bytes.NewReader(want)})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	defer cleanup()
	if _, ok := body.(*os.File); ok {
		t.Fatalf("small body landed on disk; expected in-memory buffer")
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %s", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body content mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestSeekableBodyExactThreshold(t *testing.T) {
	// A body of exactly s3SeekableBodyThreshold bytes also stays in
	// memory (the +1 probe byte hits EOF). This is the boundary the
	// previous io.ReadAll path effectively had no upper bound on.
	want := bytes.Repeat([]byte{0xcd}, s3SeekableBodyThreshold)
	body, cleanup, err := seekableBody(readerOnly{bytes.NewReader(want)})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	defer cleanup()
	if _, ok := body.(*os.File); ok {
		t.Fatalf("at-threshold body landed on disk; expected in-memory buffer")
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %s", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body content mismatch")
	}
}

func TestSeekableBodyTempFilePath(t *testing.T) {
	// One byte over the threshold spills to disk. Verify (1) the
	// returned body is a temp file, (2) its content matches input
	// exactly, (3) Seek-back works (needed for both checksum re-read
	// and SDK retry), and (4) cleanup removes the temp file.
	want := bytes.Repeat([]byte{0xef}, s3SeekableBodyThreshold+1)
	body, cleanup, err := seekableBody(readerOnly{bytes.NewReader(want)})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	f, ok := body.(*os.File)
	if !ok {
		t.Fatalf("over-threshold body did not spill to disk; got %T", body)
	}
	tmpPath := f.Name()
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("temp file %q missing while cleanup is pending: %s", tmpPath, err)
	}

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %s", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body content mismatch: got %d bytes, want %d", len(got), len(want))
	}

	// Seek back and re-read — exercises the same path the SDK takes
	// on retry.
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek back: %s", err)
	}
	got2, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("re-read body: %s", err)
	}
	if !bytes.Equal(got2, want) {
		t.Fatalf("re-read content mismatch")
	}

	cleanup()
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove temp file %q: err=%v", tmpPath, err)
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
