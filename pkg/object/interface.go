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
	"io"
	"time"
)

type Object struct {
	Key   string
	Size  int64
	Mtime time.Time // Unix seconds
	IsDir bool
}

type MultipartUpload struct {
	MinPartSize int
	MaxCount    int
	UploadID    string
}

type Part struct {
	Num  int
	Size int
	ETag string
}

type PendingPart struct {
	Key      string
	UploadID string
	Created  time.Time
}

// ObjectStorage is the interface for object storage.
// all of these API should be idempotent.
type ObjectStorage interface {
	// Description of the object storage.
	String() string
	// Create the bucket if not existed.
	Create() error
	// Get the data for the given object specified by key.
	Get(key string, off, limit int64) (io.ReadCloser, error)
	// Put data read from a reader to an object specified by key.
	Put(key string, in io.Reader) error
	// Delete a object.
	Delete(key string) error

	Head(key string) (*Object, error)
	List(prefix, marker string, limit int64) ([]*Object, error)
	ListAll(prefix, marker string) (<-chan *Object, error)
	CreateMultipartUpload(key string) (*MultipartUpload, error)
	UploadPart(key string, uploadID string, num int, body []byte) (*Part, error)
	AbortUpload(key string, uploadID string)
	CompleteUpload(key string, uploadID string, parts []*Part) error
	ListUploads(marker string) ([]*PendingPart, string, error)
}
