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

	obj "github.com/juicedata/juicesync/object"
	"github.com/juicedata/juicesync/utils"
)

var logger = utils.GetLogger("juicefs")

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
}

var storages = make(map[string]Creator)

type Creator func(bucket, accessKey, secretKey string) (ObjectStorage, error)

func register(name string, creator Creator) {
	storages[name] = creator
}

func CreateStorage(name, endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	f, ok := storages[name]
	if ok {
		logger.Debugf("Creating %s storage at endpoint %s", name, endpoint)
		return f(endpoint, accessKey, secretKey)
	}
	// look for implementation in juicesync
	return obj.CreateStorage(name, endpoint, accessKey, secretKey)
}
