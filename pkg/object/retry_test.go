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
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/juicedata/juicefs/pkg/utils"
)

type flakyStore struct {
	ObjectStorage
	fails int
	err   error
	calls int
}

func (f *flakyStore) fail() error {
	f.calls++
	if f.fails > 0 {
		f.fails--
		return f.err
	}
	return nil
}

func (f *flakyStore) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return f.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

func (f *flakyStore) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	if err := f.fail(); err != nil {
		_, _ = io.CopyN(io.Discard, in, 3) // consume part of the body before failing
		return err
	}
	return f.ObjectStorage.Put(ctx, key, in, getters...)
}

func (f *flakyStore) Head(ctx context.Context, key string) (Object, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return f.ObjectStorage.Head(ctx, key)
}

func (f *flakyStore) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	if err := f.fail(); err != nil {
		return err
	}
	return f.ObjectStorage.Delete(ctx, key, getters...)
}

func httpStatusErr(code int) error {
	return &smithy.OperationError{ServiceID: "S3", OperationName: "GetObject", Err: &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: code}},
		Err:      errors.New("api error"),
	}}
}

func TestShouldRetry(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.Canceled, false},
		{os.ErrNotExist, false},
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}, false},
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, false},
		{&os.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC}, false},
		{fmt.Errorf("list: %w", utils.ErrNotSUP), false},
		{fmt.Errorf("put: %w", utils.ErrUnsafeKey), false},
		{httpStatusErr(403), false},
		{httpStatusErr(404), false},
		{httpStatusErr(501), false},
		{httpStatusErr(408), true},
		{httpStatusErr(429), true},
		{httpStatusErr(500), true},
		{httpStatusErr(503), true},
		{httpStatusErr(200), true}, // e.g. S3 returns InternalError with 200 for CompleteMultipartUpload
		{&net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}, true},
		{&os.PathError{Op: "read", Path: "/x", Err: syscall.EIO}, true},
		{context.DeadlineExceeded, true},
		{io.ErrUnexpectedEOF, true},
		{errors.New("unknown"), true},
	}
	for _, c := range cases {
		if got := shouldRetry(c.err); got != c.want {
			t.Errorf("shouldRetry(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestWithRetry(t *testing.T) {
	defer func(min, max time.Duration) { retryMinBackoff, retryMaxBackoff = min, max }(retryMinBackoff, retryMaxBackoff)
	retryMinBackoff, retryMaxBackoff = time.Millisecond, 2*time.Millisecond

	mem, _ := CreateStorage("mem", "", "", "", "")
	transient := httpStatusErr(503)
	ctx := context.Background()

	t.Run("recover", func(t *testing.T) {
		f := &flakyStore{ObjectStorage: mem, fails: 3, err: transient}
		s := WithRetry(f)
		data := []byte("hello world")
		if err := s.Put(ctx, "k", bytes.NewReader(data)); err != nil {
			t.Fatalf("put: %s", err)
		}
		if f.calls != 4 {
			t.Fatalf("put calls %d, want 4", f.calls)
		}
		f.calls, f.fails = 0, 2
		in, err := s.Get(ctx, "k", 0, -1)
		if err != nil {
			t.Fatalf("get: %s", err)
		}
		got, _ := io.ReadAll(in)
		_ = in.Close()
		// the body must be rewound before retrying, otherwise the object would be truncated
		if !bytes.Equal(got, data) || f.calls != 3 {
			t.Fatalf("get %q with %d calls, want %q with 3 calls", got, f.calls, data)
		}
		f.calls, f.fails = 0, 1
		if _, err = s.Head(ctx, "k"); err != nil || f.calls != 2 {
			t.Fatalf("head: %v with %d calls", err, f.calls)
		}
		f.calls, f.fails = 0, 1
		if err = s.Delete(ctx, "k"); err != nil || f.calls != 2 {
			t.Fatalf("delete: %v with %d calls", err, f.calls)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		f := &flakyStore{ObjectStorage: mem, fails: 10, err: transient}
		if _, err := WithRetry(f).Head(ctx, "k"); !errors.Is(err, transient) || f.calls != maxRetries+1 {
			t.Fatalf("head: %v with %d calls, want %v with %d calls", err, f.calls, transient, maxRetries+1)
		}
	})

	t.Run("permanent", func(t *testing.T) {
		f := &flakyStore{ObjectStorage: mem, fails: 10, err: httpStatusErr(403)}
		if _, err := WithRetry(f).Head(ctx, "k"); err == nil || f.calls != 1 {
			t.Fatalf("head: %v with %d calls, want 1 call", err, f.calls)
		}
		f = &flakyStore{ObjectStorage: mem}
		if _, err := WithRetry(f).Head(ctx, "nonexistent"); !os.IsNotExist(err) || f.calls != 1 {
			t.Fatalf("head: %v with %d calls, want not exist with 1 call", err, f.calls)
		}
	})

	t.Run("unseekable body", func(t *testing.T) {
		f := &flakyStore{ObjectStorage: mem, fails: 1, err: transient}
		err := WithRetry(f).Put(ctx, "k2", io.MultiReader(strings.NewReader("hello")))
		if !errors.Is(err, transient) || f.calls != 1 {
			t.Fatalf("put: %v with %d calls, want no retry", err, f.calls)
		}
	})

	t.Run("capabilities", func(t *testing.T) {
		s := WithRetry(mem)
		if _, ok := s.(FileSystem); ok {
			t.Fatalf("mem should not be a file system")
		}
		if _, ok := s.(SupportTier); !ok {
			t.Fatalf("SupportTier should be kept")
		}
		file, _ := CreateStorage("file", t.TempDir()+"/", "", "", "")
		s = WithRetry(file)
		if _, ok := s.(SupportSymlink); !ok || !IsFileSystem(s) {
			t.Fatalf("FileSystem and SupportSymlink should be kept")
		}
		if _, ok := unwrapRetry(s); !ok {
			t.Fatalf("file store should be retried")
		}
		Shutdown(s)
	})

	t.Run("canceled", func(t *testing.T) {
		retryMinBackoff, retryMaxBackoff = time.Minute, time.Minute
		defer func() { retryMinBackoff, retryMaxBackoff = time.Millisecond, 2*time.Millisecond }()
		f := &flakyStore{ObjectStorage: mem, fails: 10, err: transient}
		ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := WithRetry(f).Head(ctx, "k")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second || f.calls != 1 {
			t.Fatalf("head: %v with %d calls after %s", err, f.calls, time.Since(start))
		}
	})
}
