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
