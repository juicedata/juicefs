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
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	blob2 "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
)

type wasb struct {
	DefaultObjectStorage
	container     *container.Client
	azblobCli     *azblob.Client
	sc            string
	cName         string
	sharedKeyCred *azblob.SharedKeyCredential // nil if using managed identity
}

func (b *wasb) String() string {
	return fmt.Sprintf("wasb://%s/", b.cName)
}

func (b *wasb) Create(ctx context.Context) error {
	_, err := b.container.Create(ctx, nil)
	if err != nil {
		if e, ok := err.(*azcore.ResponseError); ok && e.ErrorCode == string(bloberror.ContainerAlreadyExists) {
			return nil
		}
	}
	return err
}

func (b *wasb) Head(ctx context.Context, key string) (Object, error) {
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

func (b *wasb) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	download, err := b.container.NewBlobClient(key).DownloadStream(ctx, &azblob.DownloadStreamOptions{Range: blob2.HTTPRange{Offset: off, Count: limit}})
	if err != nil {
		return nil, err
	}
	attrs := ApplyGetters(getters...)
	// TODO fire another property request to get the actual storage class
	attrs.SetRequestID(aws.ToString(download.RequestID)).SetStorageClass(b.sc)
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

func (b *wasb) Put(ctx context.Context, key string, data io.Reader, getters ...AttrGetter) error {
	options := azblob.UploadStreamOptions{}
	if b.sc != "" {
		options.AccessTier = str2Tier(b.sc)
	}
	resp, err := b.azblobCli.UploadStream(ctx, b.cName, key, data, &options)
	attrs := ApplyGetters(getters...)
	attrs.SetRequestID(aws.ToString(resp.RequestID)).SetStorageClass(b.sc)
	return err
}

func (b *wasb) Copy(ctx context.Context, dst, src string) error {
	dstCli := b.container.NewBlobClient(dst)
	srcCli := b.container.NewBlobClient(src)
	options := &blob2.CopyFromURLOptions{}
	if b.sc != "" {
		options.Tier = str2Tier(b.sc)
	}

	var srcURL string
	if b.sharedKeyCred != nil {
		// Use SAS token for shared key authentication
		srcSASUrl, err := srcCli.GetSASURL(sas.BlobPermissions{Read: true}, time.Now().Add(10*time.Second), nil)
		if err != nil {
			return err
		}
		srcURL = srcSASUrl
	} else {
		// For managed identity, use the direct blob URL
		// The copy operation will use the same credential that authenticated the client
		srcURL = srcCli.URL()
	}

	_, err := dstCli.CopyFromURL(ctx, srcURL, options)
	return err
}

func (b *wasb) Delete(ctx context.Context, key string, getters ...AttrGetter) error {
	resp, err := b.container.NewBlobClient(key).Delete(ctx, nil)
	if err != nil {
		if e, ok := err.(*azcore.ResponseError); ok && e.ErrorCode == string(bloberror.BlobNotFound) {
			err = nil
		}
	}
	attrs := ApplyGetters(getters...)
	attrs.SetRequestID(aws.ToString(resp.RequestID))
	return err
}

func (b *wasb) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "" {
		return nil, false, "", notSupported
	}

	limit32 := int32(limit)
	pager := b.azblobCli.NewListBlobsFlatPager(b.cName, &azblob.ListBlobsFlatOptions{Prefix: &prefix, Marker: &token, MaxResults: &limit32})
	page, err := pager.NextPage(ctx)
	if err != nil {
		return nil, false, "", err
	}
	var n int
	if page.Segment != nil {
		n = len(page.Segment.BlobItems)
	}
	objs := make([]Object, 0, n)
	for i := 0; i < n; i++ {
		blob := page.Segment.BlobItems[i]
		if *blob.Name <= startAfter {
			continue
		}
		mtime := blob.Properties.LastModified
		objs = append(objs, &obj{
			*blob.Name,
			*blob.Properties.ContentLength,
			*mtime,
			strings.HasSuffix(*blob.Name, "/"),
			string(*blob.Properties.AccessTier),
		})
	}

	var nextMarker string
	if pager.More() {
		nextMarker = *page.NextMarker
	}
	return objs, pager.More(), nextMarker, nil
}

func (b *wasb) SetStorageClass(sc string) error {
	b.sc = sc
	return nil
}

// createAzureCredential creates a credential for Azure authentication using managed identity or other methods.
// It supports both system-assigned and user-assigned managed identities via AZURE_CLIENT_ID environment variable.
// Falls back to DefaultAzureCredential which includes: Environment, Workload Identity, Managed Identity, Azure CLI, etc.
func createAzureCredential() (azcore.TokenCredential, error) {
	// Check if user wants to use a specific user-assigned managed identity
	if clientID := os.Getenv("AZURE_CLIENT_ID"); clientID != "" {
		logger.Debugf("Attempting authentication with user-assigned managed identity (client ID: %s)", clientID)
		opts := &azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(clientID),
		}
		cred, err := azidentity.NewManagedIdentityCredential(opts)
		if err != nil {
			logger.Debugf("Failed to create user-assigned managed identity credential: %v", err)
			return nil, err
		}
		return cred, nil
	}

	// Use DefaultAzureCredential for flexible authentication
	// This tries: Environment -> Workload Identity -> Managed Identity -> Azure CLI -> Azure Developer CLI
	logger.Debugf("Attempting authentication with DefaultAzureCredential (managed identity, Azure CLI, etc.)")
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Debugf("Failed to create DefaultAzureCredential: %v", err)
		return nil, err
	}
	return cred, nil
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

	// Priority 1: Connection string support
	// DefaultEndpointsProtocol=[http|https];AccountName=***;AccountKey=***;EndpointSuffix=[core.windows.net|core.chinacloudapi.cn]
	if connString := os.Getenv("AZURE_STORAGE_CONNECTION_STRING"); connString != "" {
		logger.Debugf("Using Azure connection string authentication")
		var client *azblob.Client
		if client, err = azblob.NewClientFromConnectionString(connString, nil); err != nil {
			return nil, err
		}
		// Connection string uses shared key internally, but we don't have direct access to it
		return &wasb{container: client.ServiceClient().NewContainerClient(containerName), azblobCli: client, cName: containerName, sharedKeyCred: nil}, nil
	}

	// Priority 2: Try managed identity / token-based authentication if no account key provided
	if accountKey == "" {
		logger.Debugf("No account key provided, attempting token-based authentication (managed identity, Azure CLI, etc.)")
		tokenCred, err := createAzureCredential()
		if err != nil {
			return nil, fmt.Errorf("Failed to create Azure credential (managed identity/Azure CLI): %v", err)
		}

		// Determine the domain/endpoint for managed identity
		var domain string
		if len(hostParts) > 1 {
			domain = hostParts[1]
			if !strings.HasPrefix(hostParts[1], "blob") {
				domain = fmt.Sprintf("blob.%s", hostParts[1])
			}
		} else {
			// Default to public Azure cloud when using managed identity
			domain = "blob.core.windows.net"
		}

		serviceURL := fmt.Sprintf("%s://%s.%s", uri.Scheme, accountName, domain)
		client, err := azblob.NewClient(serviceURL, tokenCred, nil)
		if err != nil {
			return nil, fmt.Errorf("Failed to create Azure blob client with token credential: %v", err)
		}
		logger.Debugf("Successfully authenticated using token-based credential")
		return &wasb{container: client.ServiceClient().NewContainerClient(containerName), azblobCli: client, cName: containerName, sharedKeyCred: nil}, nil
	}

	// Priority 3: Shared key authentication (existing behavior)
	logger.Debugf("Using Azure shared key authentication")
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

	serviceURL := fmt.Sprintf("%s://%s.%s", uri.Scheme, accountName, domain)
	client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, credential, nil)
	if err != nil {
		return nil, err
	}
	return &wasb{container: client.ServiceClient().NewContainerClient(containerName), azblobCli: client, cName: containerName, sharedKeyCred: credential}, nil
}

func init() {
	Register("wasb", newWasb)
}
