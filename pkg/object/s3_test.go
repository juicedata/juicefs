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
	"testing"

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
