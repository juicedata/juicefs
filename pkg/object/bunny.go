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
	"bytes"
	"io"
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
func (b bunnyClient) String() string {
	return b.endpoint
}

// Limits of the object storage.
func (b bunnyClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: false,
		IsSupportUploadPartCopy:  false,
	}
}

// Get the data for the given object specified by key.
func (b bunnyClient) Get(key string, off int64, limit int64) (io.ReadCloser, error) {
	body, err := b.client.Download(key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(io.NewSectionReader(bytes.NewReader(body), off, limit)), err
}

// Put data read from a reader to an object specified by key.
func (b bunnyClient) Put(key string, in io.Reader) error {
	content, readErr := io.ReadAll(in)
	if readErr != nil {
		return readErr
	}
	err := b.client.Upload(key, content, true)
	return err
}

// Delete a object.
func (b bunnyClient) Delete(key string) error {
	return b.client.Delete(key, false)
}

// ListAll returns all the objects as an channel.
func (b bunnyClient) ListAll(prefix string, marker string, followLink bool) (<-chan Object, error) {
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
		lastChanged, _ := time.Parse("2006-01-02T15:04:05", f.LastChanged)
		out <- &obj{
			f.ObjectName,
			int64(f.Length),
			lastChanged,
			f.IsDirectory,
			"",
		}
	}
	close(out)
}

func newBunny(endpoint, accessKey, password, token string) (ObjectStorage, error) {

	endpoint_url, err := url.Parse(endpoint)

	if err != nil {
		return nil, err
	}

	client := bunnystorage.NewClient(*endpoint_url, password)

	return bunnyClient{client: &client, endpoint: endpoint}, nil
}

func init() {
	Register("bunny", newBunny)
}
