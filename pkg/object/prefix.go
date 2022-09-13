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

func (s *withPrefix) Symlink(oldName, newName string) error {
	if w, ok := s.os.(SupportSymlink); ok {
		return w.Symlink(oldName, newName)
	}
	return notSupported
}

func (s *withPrefix) Readlink(name string) (string, error) {
	if w, ok := s.os.(SupportSymlink); ok {
		return w.Readlink(name)
	}
	return "", notSupported
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s%s", p.os, p.prefix)
}

func (p *withPrefix) Create() error {
	return p.os.Create()
}

func (p *withPrefix) Head(key string) (Object, error) {
	o, err := p.os.Head(p.prefix + key)
	if err != nil {
		return nil, err
	}
	switch po := o.(type) {
	case *obj:
		po.key = po.key[len(p.prefix):]
	case *file:
		po.key = po.key[len(p.prefix):]
	}
	return o, nil
}

func (p *withPrefix) Get(key string, off, limit int64) (io.ReadCloser, error) {
	return p.os.Get(p.prefix+key, off, limit)
}

func (p *withPrefix) Put(key string, in io.Reader) error {
	return p.os.Put(p.prefix+key, in)
}

func (p *withPrefix) Delete(key string) error {
	return p.os.Delete(p.prefix + key)
}

func (p *withPrefix) List(prefix, marker string, limit int64) ([]Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	objs, err := p.os.List(p.prefix+prefix, marker, limit)
	ln := len(p.prefix)
	for _, o := range objs {
		switch p := o.(type) {
		case *obj:
			p.key = p.key[ln:]
		case *file:
			p.key = p.key[ln:]
		}
	}
	return objs, err
}

func (p *withPrefix) ListAll(prefix, marker string) (<-chan Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	r, err := p.os.ListAll(p.prefix+prefix, marker)
	if err != nil {
		return r, err
	}
	r2 := make(chan Object, 10240)
	ln := len(p.prefix)
	go func() {
		for o := range r {
			if o != nil && o.Key() != "" {
				switch p := o.(type) {
				case *obj:
					p.key = p.key[ln:]
				case *file:
					p.key = p.key[ln:]
				}
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
