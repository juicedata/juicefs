// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"

	"github.com/kurin/blazer/b2"
)

type b2client struct {
	defaultObjectStorage
	client *b2.Client
	bucket *b2.Bucket
	cursor *b2.Cursor
}

func (c *b2client) String() string {
	return fmt.Sprintf("b2://%s", c.bucket.Name())
}

func (c *b2client) Create() error {
	return nil
}

func (c *b2client) Get(key string, off, limit int64) (io.ReadCloser, error) {
	obj := c.bucket.Object(key)
	if _, err := obj.Attrs(ctx); err != nil {
		return nil, err
	}
	return obj.NewRangeReader(ctx, off, limit), nil
}

func (c *b2client) Put(key string, data io.Reader) error {
	w := c.bucket.Object(key).NewWriter(ctx)
	if _, err := w.ReadFrom(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// TODO: support multipart upload

func (c *b2client) Copy(dst, src string) error {
	in, err := c.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer in.Close()
	return c.Put(dst, in)
}

func (c *b2client) Exists(key string) error {
	_, err := c.bucket.Object(key).Attrs(ctx)
	return err
}

func (c *b2client) Delete(key string) error {
	if err := c.Exists(key); err != nil {
		return err
	}
	return c.bucket.Object(key).Delete(ctx)
}

func (c *b2client) List(prefix, marker string, limit int64) ([]*Object, error) {
	var cursor *b2.Cursor
	if marker != "" {
		cursor = c.cursor
	} else {
		cursor = &b2.Cursor{Prefix: prefix}
	}
	c.cursor = nil
	objects, nc, err := c.bucket.ListCurrentObjects(ctx, int(limit), cursor)
	if err != nil && err != io.EOF {
		return nil, err
	}
	c.cursor = nc

	n := len(objects)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		attr, err := objects[i].Attrs(ctx)
		if err == nil {
			// attr.LastModified is not correct
			objs[i] = &Object{attr.Name, attr.Size, int(attr.UploadTimestamp.Unix()), int(attr.UploadTimestamp.Unix())}
		}
	}
	return objs, nil
}

func newB2(endpoint, account, key string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		logger.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.Split(uri.Host, ".")
	bucketName := hostParts[0]
	client, err := b2.NewClient(ctx, account, key, b2.Transport(httpClient.Transport))
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	bucket, err := client.Bucket(ctx, bucketName)
	if err != nil {
		bucket, err = client.NewBucket(ctx, bucketName, &b2.BucketAttrs{
			Type: "allPrivate",
		})
		if err != nil {
			log.Fatalf("Failed to create bucket: %v", err)
		}
	}
	return &b2client{client: client, bucket: bucket}
}

func init() {
	register("b2", newB2)
}
