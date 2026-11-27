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

package fs

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type storageHolder struct {
	storage atomic.Pointer[storageSnapshot]
	config  object.VolumeConfig
}

type storageSnapshot struct {
	object.ObjectStorage
}

func (h *storageHolder) current() object.ObjectStorage {
	return h.storage.Load().ObjectStorage
}

func (h *storageHolder) replace(storage object.ObjectStorage) {
	h.storage.Store(&storageSnapshot{storage})
}

func (h *storageHolder) String() string {
	return h.current().String()
}

func (h *storageHolder) Limits() object.Limits {
	return h.current().Limits()
}

func (h *storageHolder) Create(ctx context.Context) error {
	return h.current().Create(ctx)
}

func (h *storageHolder) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	return h.current().Get(ctx, key, off, limit, getters...)
}

func (h *storageHolder) Put(ctx context.Context, key string, in io.Reader, getters ...object.AttrGetter) error {
	return h.current().Put(ctx, key, in, getters...)
}

func (h *storageHolder) Copy(ctx context.Context, dst, src string) error {
	return h.current().Copy(ctx, dst, src)
}

func (h *storageHolder) Delete(ctx context.Context, key string, getters ...object.AttrGetter) error {
	return h.current().Delete(ctx, key, getters...)
}

func (h *storageHolder) Head(ctx context.Context, key string) (object.Object, error) {
	return h.current().Head(ctx, key)
}

func (h *storageHolder) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]object.Object, bool, string, error) {
	return h.current().List(ctx, prefix, startAfter, token, delimiter, limit, followLink)
}

func (h *storageHolder) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan object.Object, error) {
	return h.current().ListAll(ctx, prefix, marker, followLink)
}

func (h *storageHolder) CreateMultipartUpload(ctx context.Context, key string) (*object.MultipartUpload, error) {
	return h.current().CreateMultipartUpload(ctx, key)
}

func (h *storageHolder) UploadPart(ctx context.Context, key, uploadID string, num int, body []byte) (*object.Part, error) {
	return h.current().UploadPart(ctx, key, uploadID, num, body)
}

func (h *storageHolder) UploadPartCopy(ctx context.Context, key, uploadID string, num int, srcKey string, off, size int64) (*object.Part, error) {
	return h.current().UploadPartCopy(ctx, key, uploadID, num, srcKey, off, size)
}

func (h *storageHolder) AbortUpload(ctx context.Context, key, uploadID string) {
	h.current().AbortUpload(ctx, key, uploadID)
}

func (h *storageHolder) CompleteUpload(ctx context.Context, key, uploadID string, parts []*object.Part) error {
	return h.current().CompleteUpload(ctx, key, uploadID, parts)
}

func (h *storageHolder) ListUploads(ctx context.Context, marker string) ([]*object.PendingPart, string, error) {
	return h.current().ListUploads(ctx, marker)
}

func (h *storageHolder) Restore(ctx context.Context, key string, days int32) error {
	return h.current().Restore(ctx, key, days)
}

func (h *storageHolder) Shutdown() {
	object.Shutdown(h.current())
}

// NewReloadableStorage creates object storage and recreates it when storage settings change on reload.
func NewReloadableStorage(format *meta.Format, m meta.Meta, patch func(*meta.Format)) (object.ObjectStorage, error) {
	format = format.Clone()
	if patch != nil {
		patch(format)
	}
	config, err := format.VolumeConfig()
	if err != nil {
		return nil, err
	}
	blob, err := object.CreateVolumeStorage(config)
	if err != nil {
		return nil, err
	}
	holder := &storageHolder{config: config}
	holder.replace(blob)
	m.OnReload(func(format *meta.Format) error {
		if patch != nil {
			patch(format)
		}
		config, err := format.VolumeConfig()
		if err != nil {
			return fmt.Errorf("object storage: %w", err)
		}
		old := &holder.config
		if config.Storage == old.Storage && config.Bucket == old.Bucket && config.AccessKey == old.AccessKey &&
			config.SecretKey == old.SecretKey && config.SessionToken == old.SessionToken && reflect.DeepEqual(config.Tiers, old.Tiers) {
			return nil
		}
		logger.Infof("found new configuration: storage=%q bucket=%q ak=%q storageClass=%q tiers=%v", config.Storage, config.Bucket, config.AccessKey, config.Tiers[0].Sc, config.Tiers)
		blob, err := object.CreateVolumeStorage(config)
		if err != nil {
			return fmt.Errorf("object storage: %w", err)
		}
		holder.replace(blob)
		holder.config = config
		return nil
	})
	return holder, nil
}
