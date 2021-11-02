// +build !nogs

/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/pkg/errors"

	"google.golang.org/api/iterator"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
)

type gs struct {
	DefaultObjectStorage
	client    *storage.Client
	bucket    string
	region    string
	pageToken string
}

func (g *gs) String() string {
	return fmt.Sprintf("gs://%s/", g.bucket)
}

func (g *gs) Create() error {
	// check if the bucket is already exists
	if objs, err := g.List("", "", 1); err == nil && len(objs) > 0 {
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
		if g.region == "" {
			return errors.New("Could not guess region to create bucket")
		}
	}

	err := g.client.Bucket(g.bucket).Create(ctx, projectID, &storage.BucketAttrs{
		Name:         g.bucket,
		StorageClass: "regional",
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
		return nil, err
	}

	return &obj{
		key,
		attrs.Size,
		attrs.Updated,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (g *gs) Get(key string, off, limit int64) (io.ReadCloser, error) {
	reader, err := g.client.Bucket(g.bucket).Object(key).NewRangeReader(ctx, off, limit)
	if err != nil {
		return nil, err
	}
	return reader, nil
}

func (g *gs) Put(key string, data io.Reader) error {
	writer := g.client.Bucket(g.bucket).Object(key).NewWriter(ctx)
	_, err := io.Copy(writer, data)
	if err != nil {
		return err
	}
	return writer.Close()
}

func (g *gs) Copy(dst, src string) error {
	srcObj := g.client.Bucket(g.bucket).Object(src)
	dstObj := g.client.Bucket(g.bucket).Object(dst)
	_, err := dstObj.CopierFrom(srcObj).Run(ctx)
	return err
}

func (g *gs) Delete(key string) error {
	if err := g.client.Bucket(g.bucket).Object(key).Delete(ctx); err != storage.ErrObjectNotExist {
		return err
	}
	return nil
}

func (g *gs) List(prefix, marker string, limit int64) ([]Object, error) {
	if marker != "" && g.pageToken == "" {
		// last page
		return nil, nil
	}
	objectIterator := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix})
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
		objs[i] = &obj{item.Name, item.Size, item.Updated, strings.HasSuffix(item.Name, "/")}
	}
	return objs, nil
}

func newGS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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
