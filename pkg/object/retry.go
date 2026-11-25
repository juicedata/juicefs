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
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

const maxRetries = 3

var (
	retryMinBackoff = 100 * time.Millisecond
	retryMaxBackoff = time.Second

	retryCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "object_request_retries",
		Help: "retried requests to object store",
	}, []string{"method"})
)

func InitMetrics(registerer prometheus.Registerer) {
	if registerer == nil {
		return
	}
	registerer.MustRegister(retryCounter)
}

type withRetry struct {
	ObjectStorage
}

type withRetryFS struct {
	*withRetry
	FileSystem
}

type withRetryFSSymlink struct {
	*withRetryFS
	SupportSymlink
}

// WithRetry returns an object storage that retries requests failed with transient errors.
// FileSystem and SupportSymlink of o are kept, their methods are not retried.
func WithRetry(o ObjectStorage) ObjectStorage {
	if _, ok := unwrapRetry(o); ok {
		return o
	}
	r := &withRetry{o}
	inner := o
	for p, ok := inner.(*withPrefix); ok; p, ok = inner.(*withPrefix) {
		inner = p.os
	}
	if _, ok := inner.(FileSystem); ok {
		rfs := &withRetryFS{r, o.(FileSystem)}
		if _, ok := inner.(SupportSymlink); ok {
			return &withRetryFSSymlink{rfs, o.(SupportSymlink)}
		}
		return rfs
	}
	return r
}

func unwrapRetry(o ObjectStorage) (*withRetry, bool) {
	switch t := o.(type) {
	case *withRetry:
		return t, true
	case *withRetryFS:
		return t.withRetry, true
	case *withRetryFSSymlink:
		return t.withRetry, true
	}
	return nil, false
}

func shouldRetry(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, utils.ErrNotSUP) || errors.Is(err, utils.ErrUnsafeKey) {
		return false
	}
	var code int
	var s1 interface{ HTTPStatusCode() int } // aws-sdk-go-v2 (smithy)
	var s2 interface{ HttpStatusCode() int } // alibabacloud-oss-go-sdk-v2
	if errors.As(err, &s1) {
		code = s1.HTTPStatusCode()
	} else if errors.As(err, &s2) {
		code = s2.HttpStatusCode()
	}
	if code >= 400 {
		return code == 408 || code == 429 || code >= 500 && code != 501
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var eno syscall.Errno
	if errors.As(err, &eno) {
		switch eno {
		case syscall.EAGAIN, syscall.EINTR, syscall.EBUSY, syscall.ETIMEDOUT, syscall.EIO,
			syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED, syscall.EPIPE:
			return true
		default:
			return false
		}
	}
	return true
}

func (r *withRetry) run(ctx context.Context, method, key string, reqID *string, f func() error) error {
	for i := 0; ; i++ {
		err := f()
		if i >= maxRetries || ctx.Err() != nil || !shouldRetry(err) {
			return err
		}
		d := min(retryMinBackoff<<i, retryMaxBackoff)
		d = d/2 + rand.N(d/2+1)
		var id string
		if reqID != nil {
			id = *reqID
		}
		logger.Warnf("%s %s (req_id: %q): %s, retry %d/%d after %s", method, key, id, err, i+1, maxRetries, d)
		retryCounter.WithLabelValues(method).Inc()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (r *withRetry) Create(ctx context.Context) error {
	return r.run(ctx, "CREATE", "", nil, func() error { return r.ObjectStorage.Create(ctx) })
}

func (r *withRetry) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (in io.ReadCloser, err error) {
	err = r.run(ctx, "GET", key, ApplyGetters(getters...).requestID, func() (err error) {
		in, err = r.ObjectStorage.Get(ctx, key, off, limit, getters...)
		return
	})
	return
}

// Put retries only if the reader can be rewound to where it was at the first attempt.
func (r *withRetry) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	s, ok := in.(io.Seeker)
	if !ok {
		return r.ObjectStorage.Put(ctx, key, in, getters...)
	}
	start, err := s.Seek(0, io.SeekCurrent)
	if err != nil {
		return r.ObjectStorage.Put(ctx, key, in, getters...)
	}
	var tried bool
	return r.run(ctx, "PUT", key, ApplyGetters(getters...).requestID, func() error {
		if tried {
			if _, err := s.Seek(start, io.SeekStart); err != nil {
				return err
			}
		}
		tried = true
		return r.ObjectStorage.Put(ctx, key, in, getters...)
	})
}

func (r *withRetry) Copy(ctx context.Context, dst, src string) error {
	return r.run(ctx, "COPY", dst, nil, func() error { return r.ObjectStorage.Copy(ctx, dst, src) })
}

func (r *withRetry) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	return r.run(ctx, "DELETE", key, ApplyGetters(getters...).requestID, func() error {
		return r.ObjectStorage.Delete(ctx, key, getters...)
	})
}

func (r *withRetry) Head(ctx context.Context, key string) (o Object, err error) {
	err = r.run(ctx, "HEAD", key, nil, func() (err error) {
		o, err = r.ObjectStorage.Head(ctx, key)
		return
	})
	return
}

func (r *withRetry) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) (objs []Object, hasMore bool, nextToken string, err error) {
	err = r.run(ctx, "LIST", prefix, nil, func() (err error) {
		objs, hasMore, nextToken, err = r.ObjectStorage.List(ctx, prefix, startAfter, token, delimiter, limit, followLink)
		return
	})
	return
}

// ListAll only retries starting the listing; errors during it are reported through the channel.
func (r *withRetry) ListAll(ctx context.Context, prefix, marker string, followLink bool) (ch <-chan Object, err error) {
	err = r.run(ctx, "LIST", prefix, nil, func() (err error) {
		ch, err = r.ObjectStorage.ListAll(ctx, prefix, marker, followLink)
		return
	})
	return
}

func (r *withRetry) CreateMultipartUpload(ctx context.Context, key string) (mu *MultipartUpload, err error) {
	err = r.run(ctx, "CREATE_MPU", key, nil, func() (err error) {
		mu, err = r.ObjectStorage.CreateMultipartUpload(ctx, key)
		return
	})
	return
}

func (r *withRetry) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (p *Part, err error) {
	err = r.run(ctx, "UPLOAD_PART", key, nil, func() (err error) {
		p, err = r.ObjectStorage.UploadPart(ctx, key, uploadID, num, body)
		return
	})
	return
}

func (r *withRetry) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (p *Part, err error) {
	err = r.run(ctx, "UPLOAD_PART_COPY", key, nil, func() (err error) {
		p, err = r.ObjectStorage.UploadPartCopy(ctx, key, uploadID, num, srcKey, off, size)
		return
	})
	return
}

func (r *withRetry) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
	return r.run(ctx, "COMPLETE_MPU", key, nil, func() error {
		return r.ObjectStorage.CompleteUpload(ctx, key, uploadID, parts)
	})
}

func (r *withRetry) ListUploads(ctx context.Context, marker string) (parts []*PendingPart, nextMarker string, err error) {
	err = r.run(ctx, "LIST_MPU", marker, nil, func() (err error) {
		parts, nextMarker, err = r.ObjectStorage.ListUploads(ctx, marker)
		return
	})
	return
}

func (r *withRetry) Restore(ctx context.Context, key string, days int32) error {
	return r.run(ctx, "RESTORE", key, nil, func() error { return r.ObjectStorage.Restore(ctx, key, days) })
}

func (r *withRetry) InitTiers(init Tiers) error {
	if o, ok := r.ObjectStorage.(SupportTier); ok {
		return o.InitTiers(init)
	}
	return notSupported
}

func (r *withRetry) GetTier(ctx context.Context) Tier {
	if o, ok := r.ObjectStorage.(SupportTier); ok {
		return o.GetTier(ctx)
	}
	return Tier{}
}

var _ ObjectStorage = (*withRetry)(nil)
var _ SupportTier = (*withRetry)(nil)
