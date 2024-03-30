/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

const reqIDExample = "c30c0107cd3a073f6607cd3a-ac103aa8-1rqU4w-PuO-cs-tos-front-azc-2"

func apiCall(getters ...AttrGetter) {
	attrs := applyGetters(getters...)
	attrs.SetStorageClass("STANDARD")
	attrs.SetRequestID(reqIDExample)
	return
}

func Test_api_call(t *testing.T) {
	var reqID, sc string

	apiCall(WithRequestID(&reqID), WithStorageClass(&sc))
	assert.Equalf(t, reqIDExample, reqID, "expected %q, got %q", reqIDExample, reqID)
	assert.Equalf(t, "STANDARD", sc, "expected %q, got %q", "STANDARD", sc)

	attrs := applyGetters(WithStorageClass(&sc))
	attrs.SetStorageClass("") // Won't overwrite by empty string
	assert.Equalf(t, "STANDARD", sc, "expected %q, got %q", "STANDARD", sc)
}
