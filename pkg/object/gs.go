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
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"github.com/pkg/errors"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

type gs struct {
	DefaultObjectStorage
	client    *storage.Client
	bucket    string
	region    string
	pageToken string
	sc        string
}

func (g *gs) String() string {
	return fmt.Sprintf("gs://%s/", g.bucket)
}

func (g *gs) Create() error {
	// check if the bucket is already exists
	if objs, err := g.List("", "", "", 1, true); err == nil && len(objs) > 0 {
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

	err := g.client.Bucket(g.bucket).Create(ctx, projectID, &storage.BucketAttrs{
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
	attrs, err := g.client.Bucket(g.bucket).Object(key).Attrs(ctx)
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
	reader, err := g.client.Bucket(g.bucket).Object(key).NewRangeReader(ctx, off, limit)
	if err != nil {
		return nil, err
	}
	// TODO fire another attr request to get the actual storage class
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(g.sc)
	return reader, nil
}

func (g *gs) Put(key string, data io.Reader, getters ...AttrGetter) error {
	writer := g.client.Bucket(g.bucket).Object(key).NewWriter(ctx)
	writer.StorageClass = g.sc
	_, err := io.Copy(writer, data)
	if err != nil {
		return err
	}
	attrs := applyGetters(getters...)
	attrs.SetStorageClass(g.sc)
	return writer.Close()
}

func (g *gs) Copy(dst, src string) error {
	srcObj := g.client.Bucket(g.bucket).Object(src)
	dstObj := g.client.Bucket(g.bucket).Object(dst)
	copier := dstObj.CopierFrom(srcObj)
	if g.sc != "" {
		copier.StorageClass = g.sc
	}
	_, err := copier.Run(ctx)
	return err
}

func (g *gs) Delete(key string, getters ...AttrGetter) error {
	if err := g.client.Bucket(g.bucket).Object(key).Delete(ctx); err != storage.ErrObjectNotExist {
		return err
	}
	return nil
}

func (g *gs) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if marker != "" && g.pageToken == "" {
		// last page
		return nil, nil
	}
	objectIterator := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix, Delimiter: delimiter})
	pager := iterator.NewPager(objectIterator, int(limit), g.pageToken)
	var entries []*storage.ObjectAttrs
	nextPageToken, err := pager.NextPage(&entries)
	if err != nil {
		return nil, err
	}
	g.pageToken = nextPageToken
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
	return objs, nil
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

	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &gs{client: client, bucket: bucket, region: region}, nil
}

func init() {
	Register("gs", newGS)
}
