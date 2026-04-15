//go:build !nostorj
// +build !nostorj

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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"storj.io/uplink"
)

type storjClient struct {
	DefaultObjectStorage
	project *uplink.Project
	bucket  string
}

var _ ObjectStorage = (*storjClient)(nil)

// storjBackoff retries fn on uplink.ErrTooManyRequests with exponential
// backoff (500 ms → 1 s → 2 s → 4 s).  All other errors are returned
// immediately without retry.
//
// Unlike the AWS SDK, which handles HTTP 429 internally before the error
// reaches the caller, the Storj uplink library surfaces ErrTooManyRequests
// directly.  This helper bridges that gap so callers behave consistently.
func storjBackoff(ctx context.Context, fn func() error) error {
	const maxRetries = 4
	delay := 500 * time.Millisecond
	var err error
	for i := 0; i <= maxRetries; i++ {
		err = fn()
		if err == nil || !errors.Is(err, uplink.ErrTooManyRequests) {
			return err
		}
		if i == maxRetries {
			break
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		delay *= 2
	}
	return err
}

func (s *storjClient) String() string {
	return fmt.Sprintf("storj://%s/", s.bucket)
}

func (s *storjClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  false,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (s *storjClient) Shutdown() {
	_ = s.project.Close()
}

func (s *storjClient) Create(ctx context.Context) error {
	return storjBackoff(ctx, func() error {
		_, err := s.project.EnsureBucket(ctx, s.bucket)
		if err != nil {
			return fmt.Errorf("ensure bucket %s: %w", s.bucket, err)
		}
		return nil
	})
}

func (s *storjClient) Head(ctx context.Context, key string) (Object, error) {
	var object *uplink.Object
	err := storjBackoff(ctx, func() error {
		var e error
		object, e = s.project.StatObject(ctx, s.bucket, key)
		return e
	})
	if err != nil {
		if errors.Is(err, uplink.ErrObjectNotFound) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return &obj{
		key:   object.Key,
		size:  object.System.ContentLength,
		mtime: object.System.Created,
		isDir: strings.HasSuffix(object.Key, "/"),
	}, nil
}

func (s *storjClient) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	opts := &uplink.DownloadOptions{Offset: off, Length: -1}
	if limit > 0 {
		opts.Length = limit
	}
	var download *uplink.Download
	err := storjBackoff(ctx, func() error {
		var e error
		download, e = s.project.DownloadObject(ctx, s.bucket, key, opts)
		return e
	})
	if err != nil {
		return nil, err
	}
	return download, nil
}

func (s *storjClient) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	// Retry is only safe when the reader is seekable: rewinding lets us restart
	// the upload from the beginning after a rate-limit response.
	rs, seekable := in.(io.ReadSeeker)
	if !seekable {
		return s.putOnce(ctx, key, in)
	}
	startPos, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("capture read position %s: %w", key, err)
	}
	return storjBackoff(ctx, func() error {
		if _, err := rs.Seek(startPos, io.SeekStart); err != nil {
			return err
		}
		return s.putOnce(ctx, key, rs)
	})
}

func (s *storjClient) putOnce(ctx context.Context, key string, in io.Reader) error {
	upload, err := s.project.UploadObject(ctx, s.bucket, key, nil)
	if err != nil {
		return fmt.Errorf("begin upload %s: %w", key, err)
	}
	if _, err = io.Copy(upload, in); err != nil {
		_ = upload.Abort()
		return fmt.Errorf("upload %s: %w", key, err)
	}
	if err = upload.Commit(); err != nil {
		_ = upload.Abort()
		return fmt.Errorf("commit upload %s: %w", key, err)
	}
	return nil
}

func (s *storjClient) Copy(ctx context.Context, dst, src string) error {
	return storjBackoff(ctx, func() error {
		_, err := s.project.CopyObject(ctx, s.bucket, src, s.bucket, dst, nil)
		return err
	})
}

func (s *storjClient) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	return storjBackoff(ctx, func() error {
		_, err := s.project.DeleteObject(ctx, s.bucket, key)
		if errors.Is(err, uplink.ErrObjectNotFound) {
			return nil
		}
		return err
	})
}

// List returns up to limit objects whose keys start with prefix and sort after
// startAfter.  The full page is collected, client-sorted, then truncated,
// because the uplink API does not guarantee lexicographic ordering.
//
// token is intentionally ignored: Storj paginates via a cursor relative to the
// prefix (mapped from startAfter), so a separate continuation token is
// unnecessary and not exposed by the uplink API.
func (s *storjClient) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	// The uplink Prefix option works at the directory level, so strip the
	// last path component if it doesn't end with '/'.  When the prefix
	// contains no '/' at all (e.g. "a"), storjPrefix falls back to "" and
	// the post-filter on line below (HasPrefix) ensures correctness.
	storjPrefix := prefix
	if storjPrefix != "" && !strings.HasSuffix(storjPrefix, "/") {
		if idx := strings.LastIndex(storjPrefix, "/"); idx >= 0 {
			storjPrefix = storjPrefix[:idx+1]
		} else {
			storjPrefix = ""
		}
	}
	// Cursor is relative to Prefix per the uplink API; strip the common part.
	cursor := strings.TrimPrefix(startAfter, storjPrefix)

	var objs []Object
	err := storjBackoff(ctx, func() error {
		objs = nil
		seen := make(map[string]struct{})
		iter := s.project.ListObjects(ctx, s.bucket, &uplink.ListObjectsOptions{
			Prefix:    storjPrefix,
			Cursor:    cursor,
			Recursive: delimiter == "",
			System:    true,
		})
		for iter.Next() {
			item := iter.Item()
			if item.Key <= startAfter || !strings.HasPrefix(item.Key, prefix) {
				continue
			}
			if item.IsPrefix {
				if _, ok := seen[item.Key]; ok {
					continue
				}
				seen[item.Key] = struct{}{}
				objs = append(objs, &obj{key: item.Key, isDir: true})
			} else {
				objs = append(objs, &obj{
					key:   item.Key,
					size:  item.System.ContentLength,
					mtime: item.System.Created,
					isDir: strings.HasSuffix(item.Key, "/"),
				})
			}
		}
		return iter.Err()
	})
	if err != nil {
		return nil, false, "", err
	}
	slices.SortFunc(objs, func(a, b Object) int { return cmp.Compare(a.Key(), b.Key()) })
	if int64(len(objs)) > limit {
		objs = objs[:limit]
	}
	return generateListResult(objs, limit)
}

func (s *storjClient) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *storjClient) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	var info uplink.UploadInfo
	if err := storjBackoff(ctx, func() error {
		var e error
		info, e = s.project.BeginUpload(ctx, s.bucket, key, nil)
		return e
	}); err != nil {
		return nil, err
	}
	return &MultipartUpload{UploadID: info.UploadID, MinPartSize: 5 << 20, MaxCount: 10000}, nil
}

// UploadPart uploads body as part num of the multipart upload identified by
// uploadID.  It uses io.Copy(pu, bytes.NewReader(body)) rather than a single
// pu.Write(body) call so that short writes (permitted by the io.Writer
// contract) are handled correctly by the copy loop.  Retrying from a []byte
// is always safe — a fresh bytes.Reader is constructed on each attempt.
func (s *storjClient) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	var info *uplink.Part
	if err := storjBackoff(ctx, func() error {
		pu, err := s.project.UploadPart(ctx, s.bucket, key, uploadID, uint32(num))
		if err != nil {
			return err
		}
		if _, err = io.Copy(pu, bytes.NewReader(body)); err != nil {
			_ = pu.Abort()
			return err
		}
		if err = pu.Commit(); err != nil {
			_ = pu.Abort()
			return err
		}
		info = pu.Info()
		return nil
	}); err != nil {
		return nil, err
	}
	return &Part{Num: num, Size: len(body), ETag: string(info.ETag)}, nil
}

func (s *storjClient) AbortUpload(ctx context.Context, key string, uploadID string) {
	_ = s.project.AbortUpload(ctx, s.bucket, key, uploadID)
}

// CompleteUpload finalises the multipart upload.  The parts slice is not
// forwarded to CommitUpload because the Storj API assembles parts by uploadID
// internally; CommitUpload does not accept a part list.
func (s *storjClient) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
	return storjBackoff(ctx, func() error {
		_, err := s.project.CommitUpload(ctx, s.bucket, key, uploadID, nil)
		return err
	})
}

// ListUploads returns all in-progress multipart uploads.
func (s *storjClient) ListUploads(ctx context.Context, marker string) ([]*PendingPart, string, error) {
	var pending []*PendingPart
	err := storjBackoff(ctx, func() error {
		pending = nil
		iter := s.project.ListUploads(ctx, s.bucket, &uplink.ListUploadsOptions{System: true})
		for iter.Next() {
			item := iter.Item()
			if item.Key <= marker {
				continue
			}
			pending = append(pending, &PendingPart{
				Key:      item.Key,
				UploadID: item.UploadID,
				Created:  item.System.Created,
			})
		}
		return iter.Err()
	})
	if err != nil {
		return nil, "", err
	}
	return pending, "", nil
}

func newStorj(endpoint, accessGrant, _, _ string) (ObjectStorage, error) {
	if accessGrant == "" {
		return nil, fmt.Errorf("storj: access grant is required (pass via --access-key)")
	}
	access, err := uplink.ParseAccess(accessGrant)
	if err != nil {
		return nil, fmt.Errorf("storj: parse access grant: %w", err)
	}
	bucket := strings.Trim(strings.TrimPrefix(endpoint, "storj://"), "/")
	if bucket == "" {
		return nil, fmt.Errorf("storj: bucket name is required (set via --bucket)")
	}

	cfg := uplink.Config{UserAgent: UserAgent}

	ctx := context.Background()
	project, err := cfg.OpenProject(ctx, access)
	if err != nil {
		return nil, fmt.Errorf("storj: open project: %w", err)
	}
	return &storjClient{project: project, bucket: bucket}, nil
}

func init() {
	Register("storj", newStorj)
}
