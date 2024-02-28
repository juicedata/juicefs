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

	bunnystorage "github.com/l0wl3vel/bunny-storage-go-sdk"
)

type bunnyClient struct {
	DefaultObjectStorage
	client   *bunnystorage.Client
	endpoint string
}

// Description of the object storage.
func (b *bunnyClient) String() string {
	return fmt.Sprintf("bunny://%v", b.endpoint)
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
	body, err := b.client.DownloadPartial(key, off, limit+off-1)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(body)), nil
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
// Requires a conditional retry, since deleting a directory or file called foo/bar requires two different calls to the Bunny API, which JuiceFS does not do
// Deleting a directory requires a trailing slash in the key to delete, which JuiceFS does not add to the path, leading to test case failures.
// We implement a conditional retry here to try deleting the directory if the delete for a key of the passed name fails.
// Deleting keys that do not exist are expected to not throw an error
func (b *bunnyClient) Delete(key string) error {
	if err := b.client.Delete(key, false); err != nil {
		if err.Error() == "Not Found" {
			// Retry delete the directory with the same name
			if errDirectoryDelete := b.client.Delete(key, true); err != nil {
				if err.Error() == "Not Found" {
					return nil
				} else {
					return errDirectoryDelete
				}
			}
		}
		return err
	}
	return nil
}

// ListAll returns all the objects as an channel.
func (b *bunnyClient) ListAll(prefix string, marker string, followLink bool) (<-chan Object, error) {

	out := make(chan Object)
	go func() {
		defer close(out)
		b.walkObjects(prefix, marker, out)
	}()

	return out, nil
}

func (b *bunnyClient) walkObjects(prefix string, marker string, out chan<- Object) {
	objects, err := b.client.List(prefix)
	if err != nil {
		return
	}

	objectsToProcess := objects

	// If no objects are returned list the parent directory and continue
	if len(objectsToProcess) == 0 {
		objectsToProcess, err = b.client.List(path.Dir(prefix))
		if err != nil {
			return
		}
	}

	markerEncountered := marker == ""

	for i := 0; i < len(objectsToProcess); i++ {
		o := objectsToProcess[i]
		normalizedPath := normalizedObjectNameWithinZone(o)
		if strings.HasPrefix(normalizedPath, path.Join(prefix)) {
			if normalizedPath == marker {
				markerEncountered = true
			}
			if markerEncountered {
				if o.IsDirectory {
					objects, err := b.client.List(normalizedPath)
					if err != nil {
						return
					}
					objectsToProcess = append(objectsToProcess, objects...)
				}
				out <- parseObjectMetadata(o)
			}
		}
	}
}

// The Object Path returned by the Bunny API contains the Storage Zone Name, which this function removes
func normalizedObjectNameWithinZone(o bunnystorage.Object) string {
	normalizedPath := path.Join(o.Path, o.ObjectName)
	if o.IsDirectory {
		normalizedPath = normalizedPath + "/" // Append a trailing slash to allow deletion of directories
	}
	return strings.TrimPrefix(normalizedPath, "/"+o.StorageZoneName+"/")
}

// Parse Bunnystorage API Object to JuiceFS Object
func parseObjectMetadata(object bunnystorage.Object) Object {
	lastChanged, _ := time.Parse("2006-01-02T15:04:05", object.LastChanged)
	return &obj{
		normalizedObjectNameWithinZone(object),
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
			return nil, os.ErrNotExist
		}
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
