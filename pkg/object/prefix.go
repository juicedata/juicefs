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
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

type withPrefix struct {
	os     ObjectStorage
	prefix string
}

func (s *withPrefix) InitTiers(init Tiers) error {
	if o, ok := s.os.(SupportTier); ok {
		return o.InitTiers(init)
	}
	return notSupported
}

func (s *withPrefix) GetTier(ctx context.Context) Tier {
	if o, ok := s.os.(SupportTier); ok {
		return o.GetTier(ctx)
	}
	return Tier{}
}

// WithPrefix return an object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) ObjectStorage {
	return &withPrefix{os, prefix}
}

// DirStorage returns an ObjectStorage representing the parent directory of s.
// If s already represents a directory (String() ends with "/"), it is returned unchanged.
// For file-like storages, returns a new storage rooted at the parent directory.
func DirStorage(s ObjectStorage) ObjectStorage {
	switch t := s.(type) {
	case *encrypted:
		return &encrypted{ObjectStorage: DirStorage(t.ObjectStorage), enc: t.enc}
	case *chunkedEncrypted:
		return NewChunkedEncrypted(DirStorage(t.ObjectStorage), t.enc)
	case *chunkedEncryptedFS:
		return NewChunkedEncrypted(DirStorage(t.chunkedEncrypted.ObjectStorage), t.enc)
	case *chunkedEncryptedFSSymlink:
		return NewChunkedEncrypted(DirStorage(t.chunkedEncryptedFS.chunkedEncrypted.ObjectStorage), t.enc)
	}
	if strings.HasSuffix(s.String(), "/") {
		return s
	}
	switch t := s.(type) {
	case *withPrefix:
		dir := path.Clean(path.Dir(t.prefix))
		if dir == "." {
			return t.os
		}
		return &withPrefix{os: t.os, prefix: dir + "/"}
	case *filestore:
		dir := path.Clean(path.Dir(t.root))
		return &filestore{root: dir + "/"}
	}
	return s
}

// key returns the key in the underlying storage. For file systems, keys must
// not escape the prefix, which is usually the target directory of sync. The key
// is not cleaned here to keep the prefix of returned keys, the underlying file
// system will clean the whole path.
func (s *withPrefix) key(key string) (string, error) {
	var err error
	switch underlyingStorage(s.os).(type) {
	case backslashSeparated:
		_, err = safeBackslashPath(s.prefix, key)
	case FileSystem:
		_, err = safePath(s.prefix, key)
	}
	if err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

// underlyingStorage returns the storage wrapped by (nested) withPrefix, since
// withPrefix itself always implements FileSystem.
func underlyingStorage(s ObjectStorage) ObjectStorage {
	for {
		p, ok := s.(*withPrefix)
		if !ok {
			return s
		}
		s = p.os
	}
}

func (s *withPrefix) Symlink(oldName, newName string) error {
	if w, ok := s.os.(SupportSymlink); ok {
		k, err := s.key(newName)
		if err != nil {
			return err
		}
		return w.Symlink(oldName, k)
	}
	return notSupported
}

func (s *withPrefix) UploadPartStream(key string, uploadID string, num int, in io.Reader) (*Part, error) {
	if w, ok := s.os.(SupportUploadPartStream); ok {
		k, err := s.key(key)
		if err != nil {
			return nil, err
		}
		return w.UploadPartStream(k, uploadID, num, in)
	}
	return nil, notSupported
}

func (s *withPrefix) Readlink(name string) (string, error) {
	if w, ok := s.os.(SupportSymlink); ok {
		k, err := s.key(name)
		if err != nil {
			return "", err
		}
		return w.Readlink(k)
	}
	return "", notSupported
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s%s", p.os, p.prefix)
}

func (p *withPrefix) Limits() Limits {
	return p.os.Limits()
}

func (p *withPrefix) Create(ctx context.Context) error {
	return p.os.Create(ctx)
}

type withSys interface {
	Sys() any
}

type withFile struct {
	File
	key string
}

func (f *withFile) Key() string { return f.key }

func (f *withFile) Sys() any {
	if s, ok := f.File.(withSys); ok {
		return s.Sys()
	}
	return nil
}

type withObj struct {
	Object
	key string
}

func (o *withObj) Key() string { return o.key }

func (p *withPrefix) updateKey(o Object) Object {
	key := o.Key()
	if len(key) < len(p.prefix) {
		return o
	}
	key = key[len(p.prefix):]
	switch po := o.(type) {
	case *obj:
		po.key = key
	case *file:
		po.key = key
	case File:
		o = &withFile{po, key}
	case Object:
		o = &withObj{po, key}
	}
	return o
}

func (p *withPrefix) Head(ctx context.Context, key string) (Object, error) {
	k, err := p.key(key)
	if err != nil {
		return nil, err
	}
	o, err := p.os.Head(ctx, k)
	if err != nil {
		return nil, err
	}
	return p.updateKey(o), nil
}

func (p *withPrefix) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	if off > 0 && limit < 0 {
		return nil, fmt.Errorf("invalid range: %d-%d", off, limit)
	}
	k, err := p.key(key)
	if err != nil {
		return nil, err
	}
	return p.os.Get(ctx, k, off, limit, getters...)
}

func (p *withPrefix) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.os.Put(ctx, k, in, getters...)
}

func (p *withPrefix) Copy(ctx context.Context, dst, src string) error {
	return p.os.Copy(ctx, dst, src)
}

func (p *withPrefix) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.os.Delete(ctx, k, getters...)
}

func (p *withPrefix) List(ctx context.Context, prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	k, err := p.key(prefix)
	if err != nil {
		return nil, false, "", err
	}
	if start != "" {
		start = p.prefix + start
	}
	objs, hasMore, nextMarker, err := p.os.List(ctx, k, start, token, delimiter, limit, followLink)
	for i, o := range objs {
		objs[i] = p.updateKey(o)
	}
	return objs, hasMore, nextMarker, err
}

func (p *withPrefix) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	k, err := p.key(prefix)
	if err != nil {
		return nil, err
	}
	if marker != "" {
		marker = p.prefix + marker
	}
	r, err := p.os.ListAll(ctx, k, marker, followLink)
	if err != nil {
		return r, err
	}
	r2 := make(chan Object, 10240)
	go func() {
		for o := range r {
			if o != nil && o.Key() != "" {
				o = p.updateKey(o)
			}
			r2 <- o
		}
		close(r2)
	}()
	return r2, nil
}

func (p *withPrefix) Chmod(path string, mode os.FileMode) error {
	if fs, ok := p.os.(FileSystem); ok {
		k, err := p.key(path)
		if err != nil {
			return err
		}
		return fs.Chmod(k, mode)
	}
	return notSupported
}

func (p *withPrefix) Chown(path string, owner, group string) error {
	if fs, ok := p.os.(FileSystem); ok {
		k, err := p.key(path)
		if err != nil {
			return err
		}
		return fs.Chown(k, owner, group)
	}
	return notSupported
}

func (p *withPrefix) Chtimes(key string, mtime time.Time) error {
	if fs, ok := p.os.(FileSystem); ok {
		k, err := p.key(key)
		if err != nil {
			return err
		}
		return fs.Chtimes(k, mtime)
	}
	return notSupported
}

func (p *withPrefix) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	k, err := p.key(key)
	if err != nil {
		return nil, err
	}
	return p.os.CreateMultipartUpload(ctx, k)
}

func (p *withPrefix) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	k, err := p.key(key)
	if err != nil {
		return nil, err
	}
	return p.os.UploadPart(ctx, k, uploadID, num, body)
}

func (s *withPrefix) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, err
	}
	src, err := s.key(srcKey)
	if err != nil {
		return nil, err
	}
	return s.os.UploadPartCopy(ctx, k, uploadID, num, src, off, size)
}

func (p *withPrefix) AbortUpload(ctx context.Context, key string, uploadID string) {
	if k, err := p.key(key); err == nil {
		p.os.AbortUpload(ctx, k, uploadID)
	}
}

func (p *withPrefix) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.os.CompleteUpload(ctx, k, uploadID, parts)
}

func (p *withPrefix) ListUploads(ctx context.Context, marker string) ([]*PendingPart, string, error) {
	parts, nextMarker, err := p.os.ListUploads(ctx, marker)
	for _, part := range parts {
		part.Key = part.Key[len(p.prefix):]
	}
	return parts, nextMarker, err
}

func (p *withPrefix) Restore(ctx context.Context, key string, days int32) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.os.Restore(ctx, k, days)
}

var _ ObjectStorage = (*withPrefix)(nil)
var _ SupportTier = (*withPrefix)(nil)

func IsFileSystem(object ObjectStorage) bool {
	_, ok := underlyingStorage(object).(FileSystem)
	return ok
}
