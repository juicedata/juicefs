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
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// flakyStore wraps an ObjectStorage and forces List to fail the first
// failuresBeforeSuccess invocations with err.
type flakyStore struct {
	ObjectStorage
	failuresBeforeSuccess int32
	calls                 atomic.Int32
	err                   error
}

func (f *flakyStore) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	n := f.calls.Add(1)
	if n <= f.failuresBeforeSuccess {
		return nil, false, "", f.err
	}
	return f.ObjectStorage.List(ctx, prefix, marker, token, delimiter, limit, followLink)
}

func TestListWithRetryEventualSuccess(t *testing.T) {
	base, _ := CreateStorage("mem", "", "", "", "")
	if err := base.Put(context.Background(), "a", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("seed: %s", err)
	}
	fs := &flakyStore{ObjectStorage: base, failuresBeforeSuccess: 3, err: errors.New("transient")}

	objs, _, _, err := listWithRetry(context.Background(), fs, "", "", "", 100, true)
	if err != nil {
		t.Fatalf("expected eventual success, got %v after %d calls", err, fs.calls.Load())
	}
	if len(objs) == 0 {
		t.Fatalf("expected at least one object after retry")
	}
	if got := fs.calls.Load(); got != 4 {
		t.Fatalf("expected 4 List calls (3 failures + 1 success), got %d", got)
	}
}

func TestListWithRetryExhaustion(t *testing.T) {
	base, _ := CreateStorage("mem", "", "", "", "")
	wantErr := errors.New("perma fail")
	fs := &flakyStore{ObjectStorage: base, failuresBeforeSuccess: 1 << 30, err: wantErr}

	start := time.Now()
	_, _, _, err := listWithRetry(context.Background(), fs, "", "", "", 100, true)
	elapsed := time.Since(start)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr after exhaustion, got %v", err)
	}
	if got := fs.calls.Load(); got != int32(listMaxAttempts) {
		t.Fatalf("expected exactly %d attempts, got %d", listMaxAttempts, got)
	}
	// Sanity: even with full-jitter, the total sleep must be bounded by the
	// sum of doubling caps (100ms + 200ms + ... up to 30s) ~~ 58s. We just
	// assert we didn't sleep forever like the old code.
	if elapsed > 2*time.Minute {
		t.Fatalf("retry took too long: %s", elapsed)
	}
}

func TestListWithRetryContextCanceled(t *testing.T) {
	base, _ := CreateStorage("mem", "", "", "", "")
	fs := &flakyStore{ObjectStorage: base, failuresBeforeSuccess: 1 << 30, err: errors.New("perma fail")}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after the first failed attempt.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, _, err := listWithRetry(ctx, fs, "", "", "", 100, true)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("did not honour cancel promptly: %s", elapsed)
	}
}

func TestConfigureS3RetryerInstallsMultiAttemptRetryer(t *testing.T) {
	var opts s3.Options
	configureS3Retryer(&opts)
	if opts.RetryMaxAttempts <= 1 {
		t.Fatalf("expected RetryMaxAttempts > 1, got %d", opts.RetryMaxAttempts)
	}
	if opts.Retryer == nil {
		t.Fatalf("expected non-nil Retryer")
	}
	// The installed retryer must classify a canonical S3 throttle error as
	// retryable; otherwise the helper would be silently ineffective for the
	// exact failure modes the audit cares about.
	throttle := &smithy.GenericAPIError{Code: "SlowDown", Message: "Please reduce your request rate."}
	if !opts.Retryer.IsErrorRetryable(throttle) {
		t.Fatalf("retryer did not flag SlowDown as retryable")
	}
}

func TestListWithRetrySkipsContextErrors(t *testing.T) {
	base, _ := CreateStorage("mem", "", "", "", "")
	// Return context.Canceled directly — must not be retried.
	fs := &flakyStore{ObjectStorage: base, failuresBeforeSuccess: 1 << 30, err: fmt.Errorf("wrapped: %w", context.Canceled)}

	_, _, _, err := listWithRetry(context.Background(), fs, "", "", "", 100, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled passthrough, got %v", err)
	}
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("context errors must not trigger retry, got %d calls", got)
	}
}
