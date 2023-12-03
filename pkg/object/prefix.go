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

type WithPrefixObj struct {
	Os     ObjectStorage
	prefix string
}

// WithPrefix return an object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) ObjectStorage {
	return &WithPrefixObj{os, prefix}
}

func (s *WithPrefixObj) SetStorageClass(sc string) {
	if o, ok := s.Os.(SupportStorageClass); ok {
		o.SetStorageClass(sc)
	}
}

func (s *WithPrefixObj) Symlink(oldName, newName string) error {
	if w, ok := s.Os.(SupportSymlink); ok {
		return w.Symlink(oldName, s.prefix+newName)
	}
	return notSupported
}

func (s *WithPrefixObj) Readlink(name string) (string, error) {
	if w, ok := s.Os.(SupportSymlink); ok {
		return w.Readlink(s.prefix + name)
	}
	return "", notSupported
}

func (p *WithPrefixObj) String() string {
	return fmt.Sprintf("%s%s", p.Os, p.prefix)
}

func (p *WithPrefixObj) Limits() Limits {
	return p.Os.Limits()
}

func (p *WithPrefixObj) Create() error {
	return p.Os.Create()
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

func (p *WithPrefixObj) updateKey(o Object) Object {
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

func (p *WithPrefixObj) Head(key string) (Object, error) {
	o, err := p.Os.Head(p.prefix + key)
	if err != nil {
		return nil, err
	}
	return p.updateKey(o), nil
}

func (p *WithPrefixObj) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if off > 0 && limit < 0 {
		return nil, fmt.Errorf("invalid range: %d-%d", off, limit)
	}
	return p.Os.Get(p.prefix+key, off, limit)
}

func (p *WithPrefixObj) Put(key string, in io.Reader) error {
	return p.Os.Put(p.prefix+key, in)
}

func (p *WithPrefixObj) Copy(dst, src string) error {
	return p.Os.Copy(dst, src)
}

func (p *WithPrefixObj) Delete(key string) error {
	return p.Os.Delete(p.prefix + key)
}

func (p *WithPrefixObj) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	objs, err := p.Os.List(p.prefix+prefix, marker, delimiter, limit, followLink)
	for i, o := range objs {
		objs[i] = p.updateKey(o)
	}
	return objs, err
}

func (p *WithPrefixObj) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	r, err := p.Os.ListAll(p.prefix+prefix, marker, followLink)
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

func (p *WithPrefixObj) Chmod(path string, mode os.FileMode) error {
	if fs, ok := p.Os.(FileSystem); ok {
		return fs.Chmod(p.prefix+path, mode)
	}
	return notSupported
}

func (p *WithPrefixObj) Chown(path string, owner, group string) error {
	if fs, ok := p.Os.(FileSystem); ok {
		return fs.Chown(p.prefix+path, owner, group)
	}
	return notSupported
}

func (p *WithPrefixObj) Chtimes(key string, mtime time.Time) error {
	if fs, ok := p.Os.(FileSystem); ok {
		return fs.Chtimes(p.prefix+key, mtime)
	}
	return notSupported
}

func (p *WithPrefixObj) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return p.Os.CreateMultipartUpload(p.prefix + key)
}

func (p *WithPrefixObj) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return p.Os.UploadPart(p.prefix+key, uploadID, num, body)
}

func (s *WithPrefixObj) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return s.Os.UploadPartCopy(s.prefix+key, uploadID, num, s.prefix+srcKey, off, size)
}

func (p *WithPrefixObj) AbortUpload(key string, uploadID string) {
	p.Os.AbortUpload(p.prefix+key, uploadID)
}

func (p *WithPrefixObj) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return p.Os.CompleteUpload(p.prefix+key, uploadID, parts)
}

func (p *WithPrefixObj) ListUploads(marker string) ([]*PendingPart, string, error) {
	parts, nextMarker, err := p.Os.ListUploads(marker)
	for _, part := range parts {
		part.Key = part.Key[len(p.prefix):]
	}
	return parts, nextMarker, err
}

var _ ObjectStorage = &WithPrefixObj{}
