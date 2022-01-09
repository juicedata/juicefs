//go:build !nogoplugin
// +build !nogoplugin

/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"errors"
	"io"
	"net/url"
	"plugin"
)

type PluginInterface interface {
	// Description of the object storage.
	String() string
	// Get the data for the given object specified by key.
	Get(key string, off, limit int64) (io.ReadCloser, error)
	// Put data read from a reader to an object specified by key.
	Put(key string, in io.Reader) error
	// Delete a object.
	Delete(key string) error
}

type pluginStorage struct {
	def               DefaultObjectStorage
	bucket            string
	accessKey         string
	secretKey         string
	rawInterface      interface{}
	requiredInterface PluginInterface
}

func init() {
	Register("goplugin", NewPlugin)
}

func NewPlugin(bucket, accessKey, secretKey string) (ObjectStorage, error) {
	u, err := url.Parse(bucket)
	if err != nil {
		return nil, err
	}
	p, err := plugin.Open(u.Path)
	if err != nil {
		return nil, err
	}
	v, err := p.Lookup("New")
	if err != nil {
		return nil, err
	}
	n, ok := v.(func(bucket, accessKey, secretKey string) (interface{}, error))
	if !ok {
		return nil, errors.New("new function not implemented")
	}
	r, err := n(bucket, accessKey, secretKey)
	if err != nil {
		return nil, err
	}
	i, ok := r.(PluginInterface)
	if !ok {
		return nil, errors.New("plugin interface not implemented")
	}
	return &pluginStorage{rawInterface: r, requiredInterface: i, bucket: bucket, accessKey: accessKey, secretKey: secretKey}, nil
}

func (storage *pluginStorage) Get(key string, off, limit int64) (io.ReadCloser, error) {
	return storage.requiredInterface.Get(key, off, limit)
}

func (storage *pluginStorage) Put(key string, r io.Reader) error {
	return storage.requiredInterface.Put(key, r)
}

func (storage *pluginStorage) Delete(key string) error {
	return storage.requiredInterface.Delete(key)
}

func (storage *pluginStorage) String() string {
	return "plugin://$plugin_filepath," + storage.requiredInterface.String()
}

func (storage *pluginStorage) Create() error {
	if i, ok := storage.rawInterface.(interface{ Create() error }); ok {
		return i.Create()
	}
	return storage.def.Create()
}

func (storage *pluginStorage) Head(key string) (Object, error) {
	if i, ok := storage.rawInterface.(interface {
		Head(key string) (Object, error)
	}); ok {
		return i.Head(key)
	}
	return storage.def.Head(key)
}

func (storage *pluginStorage) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	if i, ok := storage.rawInterface.(interface {
		CreateMultipartUpload(key string) (*MultipartUpload, error)
	}); ok {
		return i.CreateMultipartUpload(key)
	}
	return storage.def.CreateMultipartUpload(key)
}

func (storage *pluginStorage) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	if i, ok := storage.rawInterface.(interface {
		UploadPart(key string, uploadID string, num int, body []byte) (*Part, error)
	}); ok {
		return i.UploadPart(key, uploadID, num, body)
	}
	return storage.def.UploadPart(key, uploadID, num, body)
}

func (storage *pluginStorage) AbortUpload(key string, uploadID string) {
	if i, ok := storage.rawInterface.(interface {
		AbortUpload(key string, uploadID string)
	}); ok {
		i.AbortUpload(key, uploadID)
		return
	}
	storage.def.AbortUpload(key, uploadID)
}

func (storage *pluginStorage) CompleteUpload(key string, uploadID string, parts []*Part) error {
	if i, ok := storage.rawInterface.(interface {
		CompleteUpload(key string, uploadID string, parts []*Part) error
	}); ok {
		return i.CompleteUpload(key, uploadID, parts)
	}
	return storage.def.CompleteUpload(key, uploadID, parts)
}

func (storage *pluginStorage) ListUploads(marker string) ([]*PendingPart, string, error) {
	if i, ok := storage.rawInterface.(interface {
		ListUploads(marker string) ([]*PendingPart, string, error)
	}); ok {
		return i.ListUploads(marker)
	}
	return storage.def.ListUploads(marker)
}

func (storage *pluginStorage) List(prefix, marker string, limit int64) ([]Object, error) {
	if i, ok := storage.rawInterface.(interface {
		List(prefix, marker string, limit int64) ([]Object, error)
	}); ok {
		return i.List(prefix, marker, limit)
	}
	return storage.def.List(prefix, marker, limit)
}

func (storage *pluginStorage) ListAll(prefix, marker string) (<-chan Object, error) {
	if i, ok := storage.rawInterface.(interface {
		ListAll(prefix, marker string) (<-chan Object, error)
	}); ok {
		return i.ListAll(prefix, marker)
	}
	return storage.def.ListAll(prefix, marker)
}
