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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	blob2 "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/aws/aws-sdk-go/aws"
)

type wasb struct {
	DefaultObjectStorage
	container *container.Client
	azblobCli *azblob.Client
	sc        string
	cName     string
	marker    string
}

func (b *wasb) String() string {
	return fmt.Sprintf("wasb://%s/", b.cName)
}

func (b *wasb) Create() error {
	_, err := b.container.Create(ctx, nil)
	if err != nil {
		if e, ok := err.(*azcore.ResponseError); ok && e.ErrorCode == string(bloberror.ContainerAlreadyExists) {
			return nil
		}
	}
	return err
}

func (b *wasb) Head(key string) (Object, error) {
	properties, err := b.container.NewBlobClient(key).GetProperties(ctx, nil)
	if err != nil {
		if e, ok := err.(*azcore.ResponseError); ok && e.ErrorCode == string(bloberror.BlobNotFound) {
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
	download, err := b.container.NewBlobClient(key).DownloadStream(ctx, &azblob.DownloadStreamOptions{Range: blob2.HTTPRange{Offset: off, Count: limit}})
	if err != nil {
		return nil, err
	}
	ReqIDCache.put(key, aws.StringValue(download.RequestID))
	return download.Body, err
}

func str2Tier(tier string) *blob2.AccessTier {
	for _, v := range blob2.PossibleAccessTierValues() {
		if string(v) == tier {
			return &v
		}
	}
	return nil
}

func (b *wasb) Put(key string, data io.Reader) error {
	options := azblob.UploadStreamOptions{}
	if b.sc != "" {
		options.AccessTier = str2Tier(b.sc)
	}
	resp, err := b.azblobCli.UploadStream(ctx, b.cName, key, data, &options)
	ReqIDCache.put(key, aws.StringValue(resp.RequestID))
	return err
}

func (b *wasb) Copy(dst, src string) error {
	dstCli := b.container.NewBlobClient(dst)
	srcCli := b.container.NewBlobClient(src)
	options := &blob2.CopyFromURLOptions{}
	if b.sc != "" {
		options.Tier = str2Tier(b.sc)
	}
	srcSASUrl, err := srcCli.GetSASURL(sas.BlobPermissions{Read: true}, time.Now().Add(10*time.Second), nil)
	if err != nil {
		return err
	}
	_, err = dstCli.CopyFromURL(ctx, srcSASUrl, options)
	return err
}

func (b *wasb) Delete(key string) error {
	resp, err := b.container.NewBlobClient(key).Delete(ctx, nil)
	if err != nil {
		if e, ok := err.(*azcore.ResponseError); ok && e.ErrorCode == string(bloberror.BlobNotFound) {
			err = nil
		}
	}
	ReqIDCache.put(key, aws.StringValue(resp.RequestID))
	return err
}

func (b *wasb) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if delimiter != "" {
		return nil, notSupported
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

	pager := b.azblobCli.NewListBlobsFlatPager(b.cName, &azblob.ListBlobsFlatOptions{Prefix: &prefix, Marker: &marker, MaxResults: &(limit32)})
	page, err := pager.NextPage(ctx)
	if err != nil {
		return nil, err
	}
	if pager.More() {
		b.marker = *page.NextMarker
	} else {
		b.marker = ""
	}
	var n int
	if page.Segment != nil {
		n = len(page.Segment.BlobItems)
	}
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		blob := page.Segment.BlobItems[i]
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
		client, err := azblob.NewClientWithSharedKeyCredential(fmt.Sprintf("%s://%s.%s", scheme, accountName, baseURL), credential, nil)
		if err != nil {
			return "", err
		}
		if _, err = client.ServiceClient().GetProperties(ctx, nil); err != nil {
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
		var client *azblob.Client
		if client, err = azblob.NewClientFromConnectionString(connString, nil); err != nil {
			return nil, err
		}
		return &wasb{container: client.ServiceClient().NewContainerClient(containerName), azblobCli: client, cName: containerName}, nil
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

	client, err := azblob.NewClientWithSharedKeyCredential(fmt.Sprintf("%s://%s.%s", uri.Scheme, accountName, domain), credential, nil)
	if err != nil {
		return nil, err
	}
	return &wasb{container: client.ServiceClient().NewContainerClient(containerName), azblobCli: client, cName: containerName}, nil
}

func init() {
	Register("wasb", newWasb)
}
