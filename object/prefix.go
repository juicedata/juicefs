// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"fmt"
	"io"
)

type withPrefix struct {
	os     ObjectStorage
	prefix string
}

// WithPrefix retuns a object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) ObjectStorage {
	return &withPrefix{os, prefix}
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s/%s", p.os, p.prefix)
}

func (p *withPrefix) Get(key string, off, limit int64) (io.ReadCloser, error) {
	return p.os.Get(p.prefix+key, off, limit)
}

func (p *withPrefix) Put(key string, in io.Reader) error {
	return p.os.Put(p.prefix+key, in)
}

func (p *withPrefix) Exists(key string) error {
	return p.os.Exists(p.prefix + key)
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
