//go:build !nobos
// +build !nobos

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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/baidubce/bce-sdk-go/bce"
)

func TestIsBosNotFound(t *testing.T) {
	// Canonical BOS not-found: HTTP 404 from the typed service error.
	if !isBosNotFound(&bce.BceServiceError{StatusCode: http.StatusNotFound, Code: "NoSuchKey"}) {
		t.Errorf("BceServiceError 404 / NoSuchKey not recognised")
	}
	// HTTP-only signal (gateway translated 404 with a different code).
	if !isBosNotFound(&bce.BceServiceError{StatusCode: http.StatusNotFound, Code: "Whatever"}) {
		t.Errorf("BceServiceError 404 with other Code not recognised")
	}
	// Code-only signal (a non-standard proxy that drops status but keeps the code).
	if !isBosNotFound(&bce.BceServiceError{StatusCode: 0, Code: "NoSuchKey"}) {
		t.Errorf("BceServiceError NoSuchKey without status not recognised")
	}

	// Negative cases.
	if isBosNotFound(nil) {
		t.Errorf("nil flagged as not-found")
	}
	if isBosNotFound(errors.New("network reset")) {
		t.Errorf("opaque error flagged as not-found")
	}
	if isBosNotFound(&bce.BceServiceError{StatusCode: 500, Code: "InternalError"}) {
		t.Errorf("500 InternalError flagged as not-found")
	}

	// Wrapped error must still match — the whole point of using errors.As.
	wrapped := fmt.Errorf("delete %q: %w", "key",
		&bce.BceServiceError{StatusCode: http.StatusNotFound})
	if !isBosNotFound(wrapped) {
		t.Errorf("wrapped BceServiceError not recognised through errors.As")
	}
}
