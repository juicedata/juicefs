//go:build bunny
// +build bunny

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
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	bunny "github.com/l0wl3vel/bunny-storage-go-sdk"
)

type bunnyClient struct {
	DefaultObjectStorage
	client   *bunny.Client
	endpoint string
}

// Description of the object storage.
func (b *bunnyClient) String() string {
	return fmt.Sprintf("bunny://%v", b.endpoint)
}

// Get the data for the given object specified by key.
func (b *bunnyClient) Get(key string, off int64, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	var end int64
	if limit == -1 {
		end = math.MaxInt64
	} else {
		end = off + limit - 1
	}
	body, err := b.client.DownloadPartial(key, off, end)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// Put data read from a reader to an object specified by key.
func (b *bunnyClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	content, readErr := io.ReadAll(in)
	if readErr != nil {
		return readErr
	}
	return b.client.Upload(key, content, true)
}

// Delete a object.
func (b *bunnyClient) Delete(key string, getters ...AttrGetter) error {
	err := b.client.Delete(key, false)
	if err != nil && err.Error() == "Not Found" {
		err = nil
	}
	return err
}

func (b *bunnyClient) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", notSupported
	}
	var output []Object
	var dir = prefix
	if !strings.HasSuffix(dir, dirSuffix) { // If no Directory list in parent directory
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	}

	listedObjects, err := b.client.List(dir)
	if err != nil {
		if os.IsNotExist(err) {
			err = nil
		}
		return nil, false, "", err
	}

	for _, o := range listedObjects {
		normalizedPath := normalizedObjectNameWithinZone(o)
		if !strings.HasPrefix(normalizedPath, prefix) || (marker != "" && normalizedPath <= marker) {
			continue
		}
		output = append(output, parseObjectMetadata(o))
		if len(output) == int(limit) {
			break
		}
	}

	return generateListResult(output, limit)
}

// The Object Path returned by the Bunny API contains the Storage Zone Name, which this function removes
func normalizedObjectNameWithinZone(o bunny.Object) string {
	normalizedPath := path.Join(o.Path, o.ObjectName)
	if o.IsDirectory {
		normalizedPath = normalizedPath + "/" // Append a trailing slash to allow deletion of directories
	}
	return strings.TrimPrefix(normalizedPath, "/"+o.StorageZoneName+"/")
}

func parseObjectMetadata(object bunny.Object) Object {
	lastChanged, _ := time.Parse("2006-01-02T15:04:05", object.LastChanged)

	key := normalizedObjectNameWithinZone(object)
	if object.IsDirectory && !strings.HasSuffix(key, "/") {
		key = key + "/"
	}
	return &obj{
		key,
		int64(object.Length),
		lastChanged,
		object.IsDirectory,
		"",
	}
}

func (b *bunnyClient) Head(key string) (Object, error) {
	object, err := b.client.Describe(key)
	if err != nil {
		if err.Error() == "Not Found" {
			err = os.ErrNotExist
		}
		return nil, err
	}
	return parseObjectMetadata(object), nil
}

func newBunny(endpoint, accessKey, password, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	endpoint_url, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	client := bunny.NewClient(*endpoint_url, password)
	return &bunnyClient{client: &client, endpoint: endpoint}, nil
}

func init() {
	Register("bunny", newBunny)
}
