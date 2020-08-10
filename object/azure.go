// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/storage"
)

type wasb struct {
	defaultObjectStorage
	container *storage.Container
	marker    string
}

func (b *wasb) String() string {
	return fmt.Sprintf("wasb://%s", b.container.Name)
}

func (b *wasb) Head(key string) (*Object, error) {
	blob := b.container.GetBlobReference(key)
	err := blob.GetProperties(nil)
	if err != nil {
		return nil, err
	}

	return &Object{
		blob.Name,
		blob.Properties.ContentLength,
		time.Time(blob.Properties.LastModified),
		strings.HasSuffix(blob.Name, "/"),
	}, nil
}

func (b *wasb) Get(key string, off, limit int64) (io.ReadCloser, error) {
	blob := b.container.GetBlobReference(key)
	var end int64
	if limit > 0 {
		end = off + limit - 1
	}
	return blob.GetRange(&storage.GetBlobRangeOptions{
		Range: &storage.BlobRange{
			Start: uint64(off),
			End:   uint64(end),
		},
	})
}

func (b *wasb) Put(key string, data io.Reader) error {
	return b.container.GetBlobReference(key).CreateBlockBlobFromReader(data, nil)
}

func (b *wasb) Copy(dst, src string) error {
	uri := b.container.GetBlobReference(src).GetURL()
	return b.container.GetBlobReference(dst).Copy(uri, nil)
}

func (b *wasb) Delete(key string) error {
	ok, err := b.container.GetBlobReference(key).DeleteIfExists(nil)
	if !ok {
		err = errors.New("Not existed")
	}
	return err
}

func (b *wasb) List(prefix, marker string, limit int64) ([]*Object, error) {
	if marker != "" {
		if b.marker == "" {
			// last page
			return nil, nil
		}
		marker = b.marker
	}
	resp, err := b.container.ListBlobs(storage.ListBlobsParameters{
		Prefix:     prefix,
		Marker:     marker,
		MaxResults: uint(limit),
	})
	if err != nil {
		b.marker = ""
		return nil, err
	}
	b.marker = resp.NextMarker
	n := len(resp.Blobs)
	objs := make([]*Object, n)
	for i := 0; i < n; i++ {
		blob := resp.Blobs[i]
		mtime := time.Time(blob.Properties.LastModified)
		objs[i] = &Object{
			blob.Name,
			int64(blob.Properties.ContentLength),
			mtime,
			strings.HasSuffix(blob.Name, "/"),
		}
	}
	return objs, nil
}

// TODO: support multipart upload

func newWabs(endpoint, account, key string) ObjectStorage {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		log.Fatalf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	name := hostParts[0]
	client, err := storage.NewClient(account, key, hostParts[1], "2017-04-17", true)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	service := client.GetBlobService()
	container := service.GetContainerReference(name)
	return &wasb{container: container}
}

func init() {
	register("wasb", newWabs)
}
