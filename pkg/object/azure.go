//go:build !noazure
// +build !noazure

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
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

type wasb struct {
	DefaultObjectStorage
	container *azblob.ContainerClient
	sc        string
	cName     string
	marker    string
}

func (b *wasb) String() string {
	return fmt.Sprintf("wasb://%s/", b.cName)
}

func (b *wasb) Create() error {
	_, err := b.container.Create(ctx, nil)
	if err != nil && strings.Contains(err.Error(), string(azblob.StorageErrorCodeContainerAlreadyExists)) {
		return nil
	}
	return err
}

func (b *wasb) Head(key string) (Object, error) {
	client, err := b.container.NewBlobClient(key)
	if err != nil {
		return nil, err
	}
	properties, err := client.GetProperties(ctx, &azblob.BlobGetPropertiesOptions{})
	if err != nil {
		if strings.Contains(err.Error(), string(azblob.StorageErrorCodeBlobNotFound)) {
			err = os.ErrNotExist
		}
		return nil, err
	}

	return &obj{
		key,
		*properties.ContentLength,
		*properties.LastModified,
		strings.HasSuffix(key, "/"),
		*properties.AccessTier,
	}, nil
}

func (b *wasb) Get(key string, off, limit int64) (io.ReadCloser, error) {
	client, err := b.container.NewBlockBlobClient(key)
	if err != nil {
		return nil, err
	}
	download, err := client.Download(ctx, &azblob.BlobDownloadOptions{Offset: &off, Count: &limit})
	if err != nil {
		return nil, err
	}
	return download.RawResponse.Body, err
}

func (b *wasb) Put(key string, data io.Reader) error {
	client, err := b.container.NewBlockBlobClient(key)
	if err != nil {
		return err
	}
	options := azblob.UploadStreamOptions{}
	if b.sc != "" {
		options.AccessTier = azblob.AccessTier(b.sc).ToPtr()
	}
	_, err = client.UploadStream(ctx, data, options)
	return err
}

func (b *wasb) Copy(dst, src string) error {
	dstCli, err := b.container.NewBlockBlobClient(dst)
	if err != nil {
		return err
	}
	srcCli, err := b.container.NewBlockBlobClient(src)
	if err != nil {
		return err
	}
	options := &azblob.BlockBlobCopyFromURLOptions{}
	if b.sc != "" {
		options.Tier = azblob.AccessTier(b.sc).ToPtr()
	}
	_, err = dstCli.CopyFromURL(ctx, srcCli.URL(),
		options)
	return err
}

func (b *wasb) Delete(key string) error {
	client, err := b.container.NewBlockBlobClient(key)
	if err != nil {
		return err
	}
	_, err = client.Delete(ctx, &azblob.BlobDeleteOptions{})
	if err != nil && strings.Contains(err.Error(), string(azblob.StorageErrorCodeBlobNotFound)) {
		err = nil
	}
	return err
}

func (b *wasb) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	if delimiter != "" {
		return nil, notSupportedDelimiter
	}
	// todo
	if marker != "" {
		if b.marker == "" {
			// last page
			return nil, nil
		}
		marker = b.marker
	}

	limit32 := int32(limit)
	pager := b.container.ListBlobsFlat(&azblob.ContainerListBlobsFlatOptions{Prefix: &prefix, Marker: &marker, MaxResults: &(limit32)})
	if pager.Err() != nil {
		return nil, pager.Err()
	}
	if pager.NextPage(ctx) {
		b.marker = *pager.PageResponse().NextMarker
	} else {
		b.marker = ""
	}
	var n int
	if pager.PageResponse().Segment != nil {
		n = len(pager.PageResponse().Segment.BlobItems)
	}
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		blob := pager.PageResponse().Segment.BlobItems[i]
		mtime := blob.Properties.LastModified
		objs[i] = &obj{
			*blob.Name,
			*blob.Properties.ContentLength,
			*mtime,
			strings.HasSuffix(*blob.Name, "/"),
			string(*blob.Properties.AccessTier),
		}
	}
	return objs, nil
}

func (b *wasb) SetStorageClass(sc string) {
	b.sc = sc
}

func autoWasbEndpoint(containerName, accountName, scheme string, credential *azblob.SharedKeyCredential) (string, error) {
	baseURLs := []string{"blob.core.windows.net", "blob.core.chinacloudapi.cn"}
	endpoint := ""
	for _, baseURL := range baseURLs {
		if _, err := net.LookupIP(fmt.Sprintf("%s.%s", accountName, baseURL)); err != nil {
			logger.Debugf("Attempt to resolve domain name %s failed: %s", baseURL, err)
			continue
		}
		client, err := azblob.NewContainerClientWithSharedKey(fmt.Sprintf("%s://%s.%s/%s", scheme, accountName, baseURL, containerName), credential, nil)
		if err != nil {
			return "", err
		}
		if _, err = client.GetProperties(ctx, nil); err != nil {
			logger.Debugf("Try to get containers properties at %s failed: %s", baseURL, err)
			continue
		}
		endpoint = baseURL
		break
	}

	if endpoint == "" {
		return "", fmt.Errorf("fail to get endpoint for container %s", containerName)
	}
	return endpoint, nil
}

func newWasb(endpoint, accountName, accountKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint: %v, error: %v", endpoint, err)
	}
	hostParts := strings.SplitN(uri.Host, ".", 2)
	containerName := hostParts[0]
	// Connection string support: DefaultEndpointsProtocol=[http|https];AccountName=***;AccountKey=***;EndpointSuffix=[core.windows.net|core.chinacloudapi.cn]
	if connString := os.Getenv("AZURE_STORAGE_CONNECTION_STRING"); connString != "" {
		var client *azblob.ContainerClient
		if client, err = azblob.NewContainerClientFromConnectionString(connString, containerName, nil); err != nil {
			return nil, err
		}
		return &wasb{container: client, cName: containerName}, nil
	}

	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, err
	}
	var domain string
	if len(hostParts) > 1 {
		domain = hostParts[1]
		if !strings.HasPrefix(hostParts[1], "blob") {
			domain = fmt.Sprintf("blob.%s", hostParts[1])
		}
	} else if domain, err = autoWasbEndpoint(containerName, accountName, uri.Scheme, credential); err != nil {
		return nil, fmt.Errorf("Unable to get endpoint of container %s: %s", containerName, err)
	}

	client, err := azblob.NewContainerClientWithSharedKey(fmt.Sprintf("%s://%s.%s/%s", uri.Scheme, accountName, domain, containerName), credential, nil)
	if err != nil {
		return nil, err
	}

	return &wasb{container: client, cName: containerName}, nil
}

func init() {
	Register("wasb", newWasb)
}
