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
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~jamesponddotco/bunnystorage-go"
	"github.com/juicedata/juicefs/pkg/version"
)


type bunnyClient struct	{
	DefaultObjectStorage
	client *bunnystorage.Client
	endpoint string
}

// Description of the object storage.
func (b bunnyClient) String() string {
	return fmt.Sprintf("bunny://%v", b.endpoint)
}

// Limits of the object storage.
func (b bunnyClient) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: false,
		IsSupportUploadPartCopy: false,
	}
}

// Get the data for the given object specified by key.
func (b bunnyClient) Get(key string, off int64, limit int64) (io.ReadCloser, error) {
	dir, file :=  filepath.Split(key)
	r, _, err := b.client.Download(context.Background(), dir, file)
	if limit != -1	{
		return io.NopCloser(bytes.NewReader(r[off:limit-1])), err
	} else	{
		return io.NopCloser(bytes.NewReader(r[off:])), err
	}
}

// Put data read from a reader to an object specified by key.
func (b bunnyClient) Put(key string, in io.Reader) error {
	dir, file :=  filepath.Split(key)
	_, err := b.client.Upload(context.Background(), dir, file, "", in )
	return err
}

// Delete a object.
func (b bunnyClient) Delete(key string) error {
	dir, file :=  filepath.Split(key)
	_, err := b.client.Delete(context.Background(), dir, file)
	return err
}

// ListAll returns all the objects as an channel.
func (b bunnyClient) ListAll(prefix string, marker string, followLink bool) (<-chan Object, error) {
	objects, _, err := b.client.List(context.Background(), prefix)
	if err != nil	{
		return nil , err
	}

	c := make(chan Object)

	go bunnyObjectsToJuiceObjects(objects, c)

	return c, nil
}

func bunnyObjectsToJuiceObjects(objects []*bunnystorage.Object, out chan<- Object)	{
	for o := range objects	{
		f := objects[o]
		lastChanged, _ := strconv.Atoi(f.LastChanged)
		out <- &obj{
			f.ObjectName,
			int64(f.Length),
			time.Unix(int64(lastChanged), 0),
			f.IsDirectory,
			"",
		}
	}
	close(out)
}

func newBunny(endpoint, accessKey, password, token string)	(ObjectStorage, error)	{

	split_endpoint := strings.SplitN(endpoint, ".", 2)

	zone_name := split_endpoint[0]
	bunny_endpoint := bunnystorage.Parse(split_endpoint[1])

	cfg := &bunnystorage.Config{
		Application: &bunnystorage.Application{
			Name: "JuiceFS",
			Version: version.Version(),
			Contact: "team@juicedata.io",
		},
		StorageZone: zone_name,
		Debug: false,
		Key: password,
		Endpoint: bunny_endpoint,
		Logger: &logger.Logger,
	}

	client, err := bunnystorage.NewClient(cfg)

	if err != nil	{
		return nil, fmt.Errorf("Unable to create Bunny client: %v", err)
	}
	return bunnyClient{client: client, endpoint: endpoint}, nil
}

func init()	{
	Register("bunny", newBunny)
}