//go:build !noqiniu && !nos3
// +build !noqiniu,!nos3

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

	qiniuclient "github.com/qiniu/go-sdk/v7/client"
)

func TestIsQiniuNotFound(t *testing.T) {
	// Qiniu's canonical "no such entry" status is HTTP 612.
	if !isQiniuNotFound(&qiniuclient.ErrorInfo{Code: 612, Err: "no such file or directory"}) {
		t.Errorf("ErrorInfo Code=612 not recognised")
	}
	// 404 also accepted for compatible gateways that normalise to standard HTTP.
	if !isQiniuNotFound(&qiniuclient.ErrorInfo{Code: http.StatusNotFound}) {
		t.Errorf("ErrorInfo Code=404 not recognised")
	}

	// Negative cases.
	if isQiniuNotFound(nil) {
		t.Errorf("nil flagged as not-found")
	}
	if isQiniuNotFound(errors.New("network reset")) {
		t.Errorf("opaque error flagged as not-found")
	}
	if isQiniuNotFound(&qiniuclient.ErrorInfo{Code: 500}) {
		t.Errorf("500 flagged as not-found")
	}
	// The pre-fix code substring-matched on "no such file or directory"
	// in the error text. We must NOT be tricked by other errors that
	// happen to contain that phrase (the typed check guards against it).
	if isQiniuNotFound(errors.New("connection: no such file or directory")) {
		t.Errorf("opaque error with confusing message flagged as not-found")
	}

	// Wrapped error must still match.
	wrapped := fmt.Errorf("stat %q: %w", "key", &qiniuclient.ErrorInfo{Code: 612})
	if !isQiniuNotFound(wrapped) {
		t.Errorf("wrapped ErrorInfo not recognised through errors.As")
	}
}
