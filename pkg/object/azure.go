// +build !noazure

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
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/storage"
)

type wasb struct {
	DefaultObjectStorage
	container *storage.Container
	marker    string
}

func (b *wasb) String() string {
	return fmt.Sprintf("wasb://%s/", b.container.Name)
}

func (b *wasb) Create() error {
	_, err := b.container.CreateIfNotExists(&storage.CreateContainerOptions{})
	return err
}

func (b *wasb) Head(key string) (Object, error) {
	blob := b.container.GetBlobReference(key)
	err := blob.GetProperties(nil)
	if err != nil {
		return nil, err
	}

	return &obj{
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
	return b.container.GetBlobReference(key).Delete(nil)
}

func (b *wasb) List(prefix, marker string, limit int64) ([]Object, error) {
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
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		blob := resp.Blobs[i]
		mtime := time.Time(blob.Properties.LastModified)
		objs[i] = &obj{
			blob.Name,
			int64(blob.Properties.ContentLength),
			mtime,
			strings.HasSuffix(blob.Name, "/"),
		}
	}
	return objs, nil
}

// TODO: support multipart upload

func autoWasbEndpoint(containerName, accountName, accountKey string, useHTTPS bool) (string, error) {
	baseURLs := []string{"core.windows.net", "core.chinacloudapi.cn"}
	endpoint := ""
	for _, baseURL := range baseURLs {
		client, err := storage.NewClient(accountName, accountKey, baseURL, "2017-04-17", useHTTPS)
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}
		blobService := client.GetBlobService()
		resp, err := blobService.ListContainers(storage.ListContainersParameters{Prefix: containerName, MaxResults: 1})
		if err != nil {
			logger.Debugf("Try to list containers at %s failed: %s", baseURL, err)
			continue
		}
		if len(resp.Containers) == 1 {
			endpoint = baseURL
			break
		}
	}

	if endpoint == "" {
		return "", fmt.Errorf("fail to get endpoint for container %s", containerName)
	}
	return endpoint, nil
}

func newWabs(endpoint, accountName, accountKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)

	scheme := ""
	domain := ""
	// Connection string support: DefaultEndpointsProtocol=[http|https];AccountName=***;AccountKey=***;EndpointSuffix=[core.windows.net|core.chinacloudapi.cn]
	if connString := os.Getenv("AZURE_STORAGE_CONNECTION_STRING"); connString != "" {
		items := strings.Split(connString, ";")
		for _, item := range items {
			if item = strings.TrimSpace(item); item == "" {
				continue
			}
			parts := strings.SplitN(item, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("Invalid connection string item: %s", item)
			}
			// Arguments from command line take precedence
			if parts[0] == "DefaultEndpointsProtocol" && scheme == "" {
				scheme = parts[1]
			} else if parts[0] == "AccountName" && accountName == "" {
				accountName = parts[1]
			} else if parts[0] == "AccountKey" && accountKey == "" {
				accountKey = parts[1]
			} else if parts[0] == "EndpointSuffix" && domain == "" {
				domain = parts[1]
			}
		}
	}
	if scheme == "" {
		scheme = "https"
	}
	name := hostParts[0]
	if len(hostParts) > 1 {
		// Arguments from command line take precedence
		domain = hostParts[1]
	} else if domain == "" {
		if domain, err = autoWasbEndpoint(name, accountName, accountKey, scheme == "https"); err != nil {
			return nil, fmt.Errorf("Unable to get endpoint of container %s: %s", name, err)
		}
	}

	client, err := storage.NewClient(accountName, accountKey, domain, "2017-04-17", scheme == "https")
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	service := client.GetBlobService()
	container := service.GetContainerReference(name)
	return &wasb{container: container}, nil
}

func init() {
	Register("wasb", newWabs)
}
