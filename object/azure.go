package object

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/storage"
)

type abs struct {
	defaultObjectStorage
	container *storage.Container
	marker    string
}

func (b *abs) String() string {
	return fmt.Sprintf("wasb://%s", b.container.Name)
}

func (b *abs) Create() error {
	_, err := b.container.CreateIfNotExists(&storage.CreateContainerOptions{})
	return err
}

func (b *abs) Get(key string, off, limit int64) (io.ReadCloser, error) {
	blob := b.container.GetBlobReference(key)
	if limit > 0 {
		return blob.GetRange(&storage.GetBlobRangeOptions{
			Range: &storage.BlobRange{
				Start: uint64(off),
				End:   uint64(off + limit - 1),
			},
		})
	}
	r, err := blob.Get(nil)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		io.CopyN(ioutil.Discard, r, off)
	}
	return r, nil
}

func (b *abs) Put(key string, data io.Reader) error {
	return b.container.GetBlobReference(key).CreateBlockBlobFromReader(data, nil)
}

func (b *abs) Copy(dst, src string) error {
	uri := b.container.GetBlobReference(src).GetURL()
	return b.container.GetBlobReference(dst).Copy(uri, nil)
}

func (b *abs) Exists(key string) error {
	ok, err := b.container.GetBlobReference(key).Exists()
	if !ok {
		err = errors.New("Not existed")
	}
	return err
}

func (b *abs) Delete(key string) error {
	ok, err := b.container.GetBlobReference(key).DeleteIfExists(nil)
	if !ok {
		err = errors.New("Not existed")
	}
	return err
}

func (b *abs) List(prefix, marker string, limit int64) ([]*Object, error) {
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
		objs[i] = &Object{blob.Name, int64(blob.Properties.ContentLength), int(mtime.Unix()), int(mtime.Unix())}
	}
	return objs, nil
}

func newAbs(endpoint, account, key string) ObjectStorage {
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
	return &abs{container: container}
}

func init() {
	RegisterStorage("wasb", newAbs)
}
