// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"fmt"
	"io"
	"os"
)

type withPrefix struct {
	os     ObjectStorage
	prefix string
}

// WithPrefix retuns a object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) (ObjectStorage, error) {
	return &withPrefix{os, prefix}, nil
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s/%s", p.os, p.prefix)
}

func (p *withPrefix) Create() error {
	return p.os.Create()
}

func (p *withPrefix) Head(key string) (*Object, error) {
	obj, err := p.os.Head(p.prefix + key)
	if err != nil {
		return nil, err
	}
	obj.Key = obj.Key[len(p.prefix):]
	return obj, nil
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

func (p *withPrefix) List(prefix, marker string, limit int64) ([]*Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	objs, err := p.os.List(p.prefix+prefix, marker, limit)
	ln := len(p.prefix)
	for _, obj := range objs {
		obj.Key = obj.Key[ln:]
	}
	return objs, err
}

func (p *withPrefix) ListAll(prefix, marker string) (<-chan *Object, error) {
	if marker != "" {
		marker = p.prefix + marker
	}
	r, err := p.os.ListAll(p.prefix+prefix, marker)
	if err != nil {
		return r, err
	}
	r2 := make(chan *Object, 10240)
	ln := len(p.prefix)
	go func() {
		for o := range r {
			if o != nil {
				o.Key = o.Key[ln:]
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
	return nil
}

func (p *withPrefix) Chown(path string, owner, group string) error {
	if fs, ok := p.os.(FileSystem); ok {
		return fs.Chown(p.prefix+path, owner, group)
	}
	return nil
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
