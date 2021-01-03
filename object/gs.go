// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/storage/v1"
)

var ctx = context.Background()

type gs struct {
	DefaultObjectStorage
	service   *storage.Service
	bucket    string
	region    string
	pageToken string
}

func (g *gs) String() string {
	return fmt.Sprintf("gs://%s", g.bucket)
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
		log.Fatalf("GOOGLE_CLOUD_PROJECT environment variable must be set")
	}

	_, err := g.service.Buckets.Insert(projectID, &storage.Bucket{
		Id:           g.bucket,
		StorageClass: "regional",
		Location:     g.region,
	}).Do()
	if err != nil && strings.Contains(err.Error(), "You already own this bucket") {
		return nil
	}
	return err
}

func (g *gs) Head(key string) (*Object, error) {
	req := g.service.Objects.Get(g.bucket, key)
	obj, err := req.Do()
	if err != nil {
		return nil, err
	}

	mtime, _ := time.Parse(time.RFC3339, obj.Updated)
	return &Object{
		key,
		int64(obj.Size),
		mtime,
		strings.HasSuffix(key, "/"),
	}, nil
}

func (g *gs) Get(key string, off, limit int64) (io.ReadCloser, error) {
	req := g.service.Objects.Get(g.bucket, key)
	header := req.Header()
	if off > 0 || limit > 0 {
		if limit > 0 {
			header.Add("Range", fmt.Sprintf("bytes=%d-%d", off, off+limit-1))
		} else {
			header.Add("Range", fmt.Sprintf("bytes=%d-", off))
		}
	}
	resp, err := req.Download()
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (g *gs) Put(key string, data io.Reader) error {
	obj := &storage.Object{Name: key}
	_, err := g.service.Objects.Insert(g.bucket, obj).Media(data).Do()
	return err
}

func (g *gs) Copy(dst, src string) error {
	_, err := g.service.Objects.Copy(g.bucket, src, g.bucket, dst, nil).Do()
	return err
}

func (g *gs) Delete(key string) error {
	return g.service.Objects.Delete(g.bucket, key).Do()
}

func (g *gs) List(prefix, marker string, limit int64) ([]*Object, error) {
	call := g.service.Objects.List(g.bucket).Prefix(prefix).MaxResults(limit)
	if marker != "" {
		if g.pageToken == "" {
			// last page
			return nil, nil
		}
		call.PageToken(g.pageToken)
	}
	objects, err := call.Do()
	if err != nil {
		g.pageToken = ""
		return nil, err
	}
	g.pageToken = objects.NextPageToken
	n := len(objects.Items)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		item := objects.Items[i]
		mtime, _ := time.Parse(time.RFC3339, item.Updated)
		objs[i] = &Object{item.Name, int64(item.Size), mtime, strings.HasSuffix(item.Name, "/")}
	}
	return objs, nil
}

func newGS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[1]
	client, err := google.DefaultClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	service, err := storage.New(client)
	if err != nil {
		log.Fatalf("Failed to create service: %v", err)
	}
	return &gs{service: service, bucket: bucket, region: region}, nil
}

func init() {
	Register("gs", newGS)
}
