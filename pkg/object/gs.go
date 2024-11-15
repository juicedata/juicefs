//go:build !nogs
// +build !nogs

/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"github.com/pkg/errors"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

type gs struct {
	DefaultObjectStorage
	clients []*storage.Client
	index   uint64
	bucket  string
	region  string
	sc      string
}

func (g *gs) String() string {
	return fmt.Sprintf("gs://%s/", g.bucket)
}

func (g *gs) getClient() *storage.Client {
	if len(g.clients) == 1 {
		return g.clients[0]
	}
	n := atomic.AddUint64(&g.index, 1)
	return g.clients[n%(uint64(len(g.clients)))]
}

func (g *gs) Create() error {
	// check if the bucket is already exists
	if objs, _, _, err := g.List("", "", "", "", 1, true); err == nil && len(objs) > 0 {
		return nil
	}
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID, _ = metadata.ProjectID()
	}
	if projectID == "" {
		cred, err := google.FindDefaultCredentials(context.Background())
		if err == nil {
			projectID = cred.ProjectID
		}
	}
	if projectID == "" {
		return errors.New("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}
	// Guess region when region is not provided
	if g.region == "" {
		zone, err := metadata.Zone()
		if err == nil && len(zone) > 2 {
			g.region = zone[:len(zone)-2]
		}
	}

	err := g.getClient().Bucket(g.bucket).Create(ctx, projectID, &storage.BucketAttrs{
		Name:         g.bucket,
		StorageClass: g.sc,
		Location:     g.region,
	})
	if err != nil && strings.Contains(err.Error(), "You already own this bucket") {
		return nil
	}
	return err
}

func (g *gs) Head(key string) (Object, error) {
	attrs, err := g.getClient().Bucket(g.bucket).Object(key).Attrs(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			err = os.ErrNotExist
		}
		return nil, err
	}

	return &obj{
		key,
		attrs.Size,
		attrs.Updated,
		strings.HasSuffix(key, "/"),
		attrs.StorageClass,
	}, nil
}

func (g *gs) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	reader, err := g.getClient().Bucket(g.bucket).Object(key).NewRangeReader(ctx, off, limit)
	if err != nil {
		return nil, err
	}
	// TODO fire another attr request to get the actual storage class
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(g.sc)
	return reader, nil
}

func (g *gs) Put(key string, data io.Reader, getters ...AttrGetter) error {
	writer := g.getClient().Bucket(g.bucket).Object(key).NewWriter(ctx)
	writer.StorageClass = g.sc

	// If you upload small objects (< 16MiB), you should set ChunkSize
	// to a value slightly larger than the objects' sizes to avoid memory bloat.
	// This is especially important if you are uploading many small objects concurrently.
	writer.ChunkSize = 5 << 20

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err := io.CopyBuffer(writer, data, *buf)
	if err != nil {
		return err
	}
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(g.sc)
	return writer.Close()
}

func (g *gs) Copy(dst, src string) error {
	client := g.getClient()
	srcObj := client.Bucket(g.bucket).Object(src)
	dstObj := client.Bucket(g.bucket).Object(dst)
	copier := dstObj.CopierFrom(srcObj)
	if g.sc != "" {
		copier.StorageClass = g.sc
	}
	_, err := copier.Run(ctx)
	return err
}

func (g *gs) Delete(key string, getters ...AttrGetter) error {
	if err := g.getClient().Bucket(g.bucket).Object(key).Delete(ctx); err != storage.ErrObjectNotExist {
		return err
	}
	return nil
}

func (g *gs) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	objectIterator := g.getClient().Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix, Delimiter: delimiter, StartOffset: start})
	pager := iterator.NewPager(objectIterator, int(limit), token)
	var entries []*storage.ObjectAttrs
	nextPageToken, err := pager.NextPage(&entries)
	if err != nil {
		return nil, false, "", err
	}
	n := len(entries)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		item := entries[i]
		if delimiter != "" && item.Prefix != "" {
			objs[i] = &obj{item.Prefix, 0, time.Unix(0, 0), true, item.StorageClass}
		} else {
			objs[i] = &obj{item.Name, item.Size, item.Updated, strings.HasSuffix(item.Name, "/"), item.StorageClass}
		}
	}
	if delimiter != "" {
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}
	return objs, nextPageToken != "", nextPageToken, nil
}

func (g *gs) SetStorageClass(sc string) error {
	g.sc = sc
	return nil
}

func newGS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("gs://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, errors.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	var region string
	if len(hostParts) > 1 {
		region = hostParts[1]
	}

	var size int
	if ssize := os.Getenv("JFS_NUM_GOOGLE_CLIENTS"); ssize != "" {
		if size, err = strconv.Atoi(ssize); err != nil {
			return nil, err
		}
	}
	if size < 1 {
		size = 5
	}
	clis := make([]*storage.Client, size)
	for i := 0; i < size; i++ {
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, err
		}
		clis[i] = client
	}

	return &gs{clients: clis, bucket: bucket, region: region}, nil
}

func init() {
	Register("gs", newGS)
}
