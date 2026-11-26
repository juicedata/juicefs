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
	"io"
	"os"
	"time"
)

// invariant describes storage semantics that an optional capability requires
// wrapper layers to preserve when unwrapping.
type invariant uint8

const (
	invNamespace invariant = 1 << iota // 1:1 file path and directory semantics
	invPayload                         // raw, untransformed object byte stream

	invNone invariant = 0
)

type storageUnwrapper interface {
	Unwrap() ObjectStorage
}

// layeredStorage is implemented by wrappers that transform keys or payloads,
// reporting the inner storage, the mapped key for this layer, and the
// invariants preserved across it.
type layeredStorage interface {
	unwrapLayer(key string) (inner ObjectStorage, mappedKey string, preserved invariant)
}

// resolve returns the outermost layer implementing T and the key mapped down
// to that layer. Checking T before unwrapping lets a wrapper's own capability
// take precedence over the storage it wraps.
func resolve[T any](o ObjectStorage, key string, need invariant) (impl T, mappedKey string, ok bool) {
	for o != nil {
		if impl, ok = o.(T); ok {
			return impl, key, true
		}
		switch t := o.(type) {
		case layeredStorage:
			var preserved invariant
			o, key, preserved = t.unwrapLayer(key)
			if need&^preserved != 0 {
				return impl, "", false
			}
		case storageUnwrapper:
			o = t.Unwrap()
		default:
			return impl, "", false
		}
	}
	return impl, "", false
}

// StorageController accesses optional capabilities of an [ObjectStorage]
// across wrapper layers, modeled after [net/http.ResponseController].
//
// Passthrough wrappers that do not alter keys or payload layout should
// implement Unwrap() ObjectStorage so StorageController can reach the
// underlying storage without combinatorial wrapper types.
//
// Unsupported operations return [utils.ErrNotSUP].
type StorageController struct {
	os ObjectStorage
}

// NewStorageController creates a [StorageController] for o.
func NewStorageController(o ObjectStorage) *StorageController {
	return &StorageController{os: o}
}

// Shutdown releases resources held by the underlying storage.
func (c *StorageController) Shutdown() {
	if s, _, ok := resolve[Shutdownable](c.os, "", invNone); ok {
		s.Shutdown()
	}
}

// InitTiers configures storage tiers on the underlying storage.
func (c *StorageController) InitTiers(init Tiers) error {
	if t, _, ok := resolve[SupportTier](c.os, "", invNone); ok {
		return t.InitTiers(init)
	}
	return notSupported
}

// GetTier returns the runtime tier of the underlying storage for ctx.
func (c *StorageController) GetTier(ctx context.Context) Tier {
	if t, _, ok := resolve[SupportTier](c.os, "", invNone); ok {
		return t.GetTier(ctx)
	}
	return Tier{}
}

// IsFileSystem reports whether the storage supports [FileSystem] operations.
func (c *StorageController) IsFileSystem() bool {
	_, _, ok := resolve[FileSystem](c.os, "", invNamespace)
	return ok
}

// Chmod changes the mode of path on the underlying [FileSystem].
func (c *StorageController) Chmod(path string, mode os.FileMode) error {
	if fs, key, ok := resolve[FileSystem](c.os, path, invNamespace); ok {
		return fs.Chmod(key, mode)
	}
	return notSupported
}

// Chown changes the owner and group of path on the underlying [FileSystem].
func (c *StorageController) Chown(path, owner, group string) error {
	if fs, key, ok := resolve[FileSystem](c.os, path, invNamespace); ok {
		return fs.Chown(key, owner, group)
	}
	return notSupported
}

// Chtimes updates the modification time of path on the underlying [MtimeChanger].
func (c *StorageController) Chtimes(path string, mtime time.Time) error {
	if mc, key, ok := resolve[MtimeChanger](c.os, path, invNamespace); ok {
		return mc.Chtimes(key, mtime)
	}
	return notSupported
}

// SupportSymlink reports whether the storage supports [SupportSymlink] operations.
func (c *StorageController) SupportSymlink() bool {
	_, _, ok := resolve[SupportSymlink](c.os, "", invNamespace)
	return ok
}

// Symlink creates newName as a symbolic link to oldName.
func (c *StorageController) Symlink(oldName, newName string) error {
	if s, key, ok := resolve[SupportSymlink](c.os, newName, invNamespace); ok {
		return s.Symlink(oldName, key)
	}
	return notSupported
}

// Readlink returns the destination of the named symbolic link.
func (c *StorageController) Readlink(name string) (string, error) {
	if s, key, ok := resolve[SupportSymlink](c.os, name, invNamespace); ok {
		return s.Readlink(key)
	}
	return "", notSupported
}

// UploadPartStream uploads a multipart part from a reader when supported.
func (c *StorageController) UploadPartStream(key string, uploadID string, num int, in io.Reader) (*Part, error) {
	if ups, mappedKey, ok := resolve[SupportUploadPartStream](c.os, key, invNamespace|invPayload); ok {
		return ups.UploadPartStream(mappedKey, uploadID, num, in)
	}
	return nil, notSupported
}
