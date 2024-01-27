/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"io"
	"math"
	"net/url"
	"time"

	bunnystorage "github.com/l0wl3vel/bunny-storage-go-sdk"
)

type bunnyClient struct {
	DefaultObjectStorage
	client   *bunnystorage.Client
	endpoint string
}

// Description of the object storage.
func (b *bunnyClient) String() string {
	return b.endpoint
}

// Limits of the object storage.
func (b *bunnyClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: false,
		IsSupportUploadPartCopy:  false,
	}
}

// Get the data for the given object specified by key.
func (b *bunnyClient) Get(key string, off int64, limit int64) (io.ReadCloser, error) {
	if limit == -1 {
		limit = math.MaxInt64
	}
	body, err := b.client.DownloadPartialWithReaderCloser(key, off, limit+off-1)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Put data read from a reader to an object specified by key.
func (b *bunnyClient) Put(key string, in io.Reader) error {
	content, readErr := io.ReadAll(in)
	if readErr != nil {
		return readErr
	}
	return b.client.Upload(key, content, true)
}

// Delete a object.
func (b *bunnyClient) Delete(key string) error {
	return b.client.Delete(key, false)
}

// ListAll returns all the objects as an channel.
func (b *bunnyClient) ListAll(prefix string, marker string, followLink bool) (<-chan Object, error) {
	objects, err := b.client.List(prefix)
	if err != nil {
		return nil, err
	}

	c := make(chan Object)

	go bunnyObjectsToJuiceObjects(objects, c)

	return c, nil
}

func bunnyObjectsToJuiceObjects(objects []bunnystorage.Object, out chan<- Object) {
	for o := range objects {
		f := objects[o]
		out <- parseObjectMetadata(f)
	}
	close(out)
}

// Parse Bunnystorage API Object to JuiceFS Object
func parseObjectMetadata(object bunnystorage.Object) Object {
	lastChanged, _ := time.Parse("2006-01-02T15:04:05", object.LastChanged)
	return &obj{
		object.ObjectName,
		int64(object.Length),
		lastChanged,
		object.IsDirectory,
		"",
	}
}

func (b *bunnyClient) Head(key string) (Object, error) {
	logger.Debug(key)
	object, err := b.client.Describe(key)
	logger.Debug(object)
	if err != nil {
		return nil, err
	}
	return parseObjectMetadata(object), nil
}

func newBunny(endpoint, accessKey, password, token string) (ObjectStorage, error) {
	endpoint_url, err := url.Parse(endpoint)

	if err != nil {
		return nil, err
	}

	client := bunnystorage.NewClient(*endpoint_url, password)

	return &bunnyClient{client: &client, endpoint: endpoint}, nil
}

func init() {
	Register("bunny", newBunny)
}
