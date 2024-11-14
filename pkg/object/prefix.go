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
	"fmt"
	"io"
	"os"
	"time"
)

type withPrefix struct {
	os     ObjectStorage
	prefix string
}

// WithPrefix return an object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) ObjectStorage {
	return &withPrefix{os, prefix}
}

func (s *withPrefix) SetStorageClass(sc string) error {
	if o, ok := s.os.(SupportStorageClass); ok {
		return o.SetStorageClass(sc)
	}
	return notSupported
}

func (s *withPrefix) Symlink(oldName, newName string) error {
	if w, ok := s.os.(SupportSymlink); ok {
		return w.Symlink(oldName, s.prefix+newName)
	}
	return notSupported
}

func (s *withPrefix) Readlink(name string) (string, error) {
	if w, ok := s.os.(SupportSymlink); ok {
		return w.Readlink(s.prefix + name)
	}
	return "", notSupported
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s%s", p.os, p.prefix)
}

func (p *withPrefix) Limits() Limits {
	return p.os.Limits()
}

func (p *withPrefix) Create() error {
	return p.os.Create()
}

type withFile struct {
	File
	key string
}

func (f *withFile) Key() string { return f.key }

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

func (p *withPrefix) Head(key string) (Object, error) {
	o, err := p.os.Head(p.prefix + key)
	if err != nil {
		return nil, err
	}
	return p.updateKey(o), nil
}

func (p *withPrefix) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	if off > 0 && limit < 0 {
		return nil, fmt.Errorf("invalid range: %d-%d", off, limit)
	}
	return p.os.Get(p.prefix+key, off, limit, getters...)
}

func (p *withPrefix) Put(key string, in io.Reader, getters ...AttrGetter) error {
	return p.os.Put(p.prefix+key, in, getters...)
}

func (p *withPrefix) Copy(dst, src string) error {
	return p.os.Copy(dst, src)
}

func (p *withPrefix) Delete(key string, getters ...AttrGetter) error {
	return p.os.Delete(p.prefix+key, getters...)
}

func (p *withPrefix) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if start != "" {
		start = p.prefix + start
	}
	objs, hasMore, nextMarker, err := p.os.List(p.prefix+prefix, start, token, delimiter, limit, followLink)
	for i, o := range objs {
		objs[i] = p.updateKey(o)
	}
	return objs, hasMore, nextMarker, err
}

func (p *withPrefix) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	r, err := p.os.ListAll(p.prefix+prefix, marker, followLink)
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
		return fs.Chmod(p.prefix+path, mode)
	}
	return notSupported
}

func (p *withPrefix) Chown(path string, owner, group string) error {
	if fs, ok := p.os.(FileSystem); ok {
		return fs.Chown(p.prefix+path, owner, group)
	}
	return notSupported
}

func (p *withPrefix) Chtimes(key string, mtime time.Time) error {
	if fs, ok := p.os.(FileSystem); ok {
		return fs.Chtimes(p.prefix+key, mtime)
	}
	return notSupported
}

func (p *withPrefix) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return p.os.CreateMultipartUpload(p.prefix + key)
}

func (p *withPrefix) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return p.os.UploadPart(p.prefix+key, uploadID, num, body)
}

func (s *withPrefix) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return s.os.UploadPartCopy(s.prefix+key, uploadID, num, s.prefix+srcKey, off, size)
}

func (p *withPrefix) AbortUpload(key string, uploadID string) {
	p.os.AbortUpload(p.prefix+key, uploadID)
}

func (p *withPrefix) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return p.os.CompleteUpload(p.prefix+key, uploadID, parts)
}

func (p *withPrefix) ListUploads(marker string) ([]*PendingPart, string, error) {
	parts, nextMarker, err := p.os.ListUploads(marker)
	for _, part := range parts {
		part.Key = part.Key[len(p.prefix):]
	}
	return parts, nextMarker, err
}

var _ ObjectStorage = &withPrefix{}

func IsFileSystem(object ObjectStorage) bool {
	if o, ok := object.(*withPrefix); ok {
		object = o.os
	}
	_, ok := object.(FileSystem)
	return ok
}
