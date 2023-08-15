package object

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"d7y.io/dragonfly/v2/client/config"
	"d7y.io/dragonfly/v2/client/daemon/objectstorage"
	"d7y.io/dragonfly/v2/client/dfstore"
)

type dragonfly struct {
	DefaultObjectStorage
	client *dfstore.Dfstore
	*config.DfstoreConfig
	bucket   string
	endpoint string
}

func (d *dragonfly) String() string {
	return fmt.Sprintf("dragonfly://%s/", d.bucket)
}

func (d *dragonfly) Create() error {
	if _, err := d.List("", "", "", 1, false); err == nil {
		return nil
	}
	err := (*d.client).CreateBucketWithContext(ctx, &dfstore.CreateBucketInput{
		BucketName: d.bucket,
	})
	if err != nil && isExists(err) {
		err = nil
	}

	return err
}

func (d *dragonfly) Head(key string) (Object, error) {
	meta, err := (*d.client).GetObjectMetadataWithContext(ctx, &dfstore.GetObjectMetadataInput{
		BucketName: d.bucket,
		ObjectKey:  key,
	})
	if err != nil {
		if strings.Contains(err.Error(), strconv.Itoa(http.StatusNotFound)) {
			err = os.ErrNotExist
		}
		return nil, err
	}

	return &obj{
		key,
		meta.ContentLength,
		meta.LastModifiedTime,
		strings.HasSuffix(key, "/"),
		meta.StorageClass,
	}, nil
}

func (d *dragonfly) Get(key string, off, limit int64) (io.ReadCloser, error) {
	reader, err := (*d.client).GetObjectWithContext(ctx, &dfstore.GetObjectInput{
		BucketName: d.bucket,
		ObjectKey:  key,
		Range:      getRange(off, limit),
	})

	return reader, err
}

func (d *dragonfly) Put(key string, data io.Reader) error {
	err := (*d.client).PutObjectWithContext(ctx, &dfstore.PutObjectInput{
		BucketName:  d.bucket,
		ObjectKey:   key,
		Reader:      data,
		Mode:        d.Mode,
		MaxReplicas: d.MaxReplicas,
		Filter:      d.Filter,
	})
	return err
}

func (d *dragonfly) Copy(dst, src string) error {
	err := (*d.client).CopyObjectWithContext(ctx, &dfstore.CopyObjectInput{
		BucketName:           d.bucket,
		SourceObjectKey:      src,
		DestinationObjectKey: dst,
	})
	return err
}

func (d *dragonfly) Delete(key string) error {
	err := (*d.client).DeleteObjectWithContext(ctx, &dfstore.DeleteObjectInput{
		BucketName: d.bucket,
		ObjectKey:  key,
	})
	return err
}

func (d *dragonfly) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}

	resp, err := (*d.client).GetObjectMetadatasWithContext(ctx, &dfstore.GetObjectMetadatasInput{
		BucketName: d.bucket,
		Prefix:     prefix,
		Marker:     marker,
		Delimiter:  delimiter,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}

	n := len(resp.Metadatas)
	objs := make([]Object, 0, n)
	for _, meta := range resp.Metadatas {
		objs = append(objs, &obj{
			meta.Key,
			meta.ContentLength,
			meta.LastModifiedTime,
			strings.HasSuffix(meta.Key, "/"),
			meta.StorageClass,
		})
	}

	if delimiter != "" {
		for _, o := range resp.CommonPrefixes {
			objs = append(objs, &obj{o, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}

	return objs, err
}

func (d *dragonfly) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

// Not provided by Dragonfly yet.
func (d *dragonfly) SetStorageClass(sc string) {}

func (d *dragonfly) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return nil, notSupported
}

func (d *dragonfly) UploadPart(key string, uploadID string, num int, data []byte) (*Part, error) {
	return nil, notSupported
}

func (d *dragonfly) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

func (d *dragonfly) AbortUpload(key string, uploadID string) {
}

func (d *dragonfly) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return notSupported
}

func (d *dragonfly) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", notSupported
}

func newDragonfly(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	bucket := os.Getenv("DRAGONFLY_BUCKET")
	if bucket == "" {
		return nil, fmt.Errorf("environment variable DRAGONFLY_BUCKET is required")
	}

	dragonflyEndpoint := os.Getenv("DRAGONFLY_ENDPOINT")
	if dragonflyEndpoint == "" {
		defaultURL := url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", "127.0.0.1", config.DefaultObjectStorageStartPort),
		}
		dragonflyEndpoint = defaultURL.String()
	}
	if !strings.Contains(dragonflyEndpoint, "://") {
		dragonflyEndpoint = fmt.Sprintf("http://%s", dragonflyEndpoint)
	}
	client := dfstore.New(dragonflyEndpoint)

	cfg := &config.DfstoreConfig{
		Mode: objectstorage.WriteBack,
	}
	if modeVal := os.Getenv("DRAGONFLY_MODE"); modeVal != "" {
		mode, err := strconv.Atoi(modeVal)
		if err != nil {
			return nil, fmt.Errorf("unexpected dragonfly mode: %s", modeVal)
		}
		cfg.Mode = mode
	}

	if maxReplicasVal := os.Getenv("DRAGONFLY_MAX_REPLICAS"); maxReplicasVal != "" {
		maxReplicas, err := strconv.Atoi(maxReplicasVal)
		if err != nil {
			return nil, fmt.Errorf("unexpected dragonfly max replicas: %s", maxReplicasVal)
		}
		cfg.MaxReplicas = maxReplicas
	}
	cfg.Filter = os.Getenv("DRAGONFLY_FILTER")

	return &dragonfly{
		client:        &client,
		DfstoreConfig: cfg,
		bucket:        bucket,
		endpoint:      dragonflyEndpoint,
	}, nil
}

func init() {
	Register("dragonfly", newDragonfly)
}
