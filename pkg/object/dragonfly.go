package object

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"d7y.io/dragonfly/v2/client/config"
	"d7y.io/dragonfly/v2/client/daemon/objectstorage"
	"d7y.io/dragonfly/v2/client/dfstore"
)

// defaultDragonflyEndpoint is the default endpoint to connect to a local dragonfly.
var defaultDragonflyEndpoint = fmt.Sprintf("http://127.0.0.1:%d", config.DefaultObjectStorageStartPort)

type dragonfly struct {
	DefaultObjectStorage
	config *config.DfstoreConfig
	client dfstore.Dfstore
	bucket string
}

func (d *dragonfly) String() string {
	return fmt.Sprintf("dragonfly://%s/", d.bucket)
}

func (d *dragonfly) Create() error {
	if _, err := d.List("", "", "", 1, false); err == nil {
		return nil
	}

	if err := d.client.CreateBucketWithContext(ctx, &dfstore.CreateBucketInput{
		BucketName: d.bucket,
	}); err != nil && !isExists(err) {
		return err
	}

	return nil
}

func (d *dragonfly) Head(key string) (Object, error) {
	meta, err := d.client.GetObjectMetadataWithContext(ctx, &dfstore.GetObjectMetadataInput{
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
	return d.client.GetObjectWithContext(ctx, &dfstore.GetObjectInput{
		BucketName: d.bucket,
		ObjectKey:  key,
		Range:      getRange(off, limit),
	})
}

func (d *dragonfly) Put(key string, data io.Reader) error {
	return d.client.PutObjectWithContext(ctx, &dfstore.PutObjectInput{
		BucketName:  d.bucket,
		ObjectKey:   key,
		Reader:      data,
		Mode:        d.config.Mode,
		MaxReplicas: d.config.MaxReplicas,
		Filter:      d.config.Filter,
	})
}

func (d *dragonfly) Copy(dst, src string) error {
	return d.client.CopyObjectWithContext(ctx, &dfstore.CopyObjectInput{
		BucketName:           d.bucket,
		SourceObjectKey:      src,
		DestinationObjectKey: dst,
	})
}

func (d *dragonfly) Delete(key string) error {
	return d.client.DeleteObjectWithContext(ctx, &dfstore.DeleteObjectInput{
		BucketName: d.bucket,
		ObjectKey:  key,
	})
}

func (d *dragonfly) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	resp, err := d.client.GetObjectMetadatasWithContext(ctx, &dfstore.GetObjectMetadatasInput{
		BucketName: d.bucket,
		Prefix:     prefix,
		Marker:     marker,
		Delimiter:  delimiter,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}

	objs := make([]Object, 0, len(resp.Metadatas))
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

func newDragonfly(_endpoint, _accessKey, _secretKey, _token string) (ObjectStorage, error) {
	// Get endpoint from environment variable.
	endpoint, exists := os.LookupEnv("DRAGONFLY_ENDPOINT")
	if !exists {
		endpoint = defaultDragonflyEndpoint
		logger.Infof("DRAGONFLY_ENDPOINT is not defined, using default endpoint %s", endpoint)
	}

	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}

	// Initialize dfstore client.
	client := dfstore.New(endpoint)

	// Get bucket from environment variable.
	bucket, exists := os.LookupEnv("DRAGONFLY_BUCKET")
	if !exists {
		return nil, fmt.Errorf("environment variable DRAGONFLY_BUCKET is required")
	}

	// Initialize dfstore config.
	cfg := &config.DfstoreConfig{
		Mode: objectstorage.WriteBack,
	}
	if value, exists := os.LookupEnv("DRAGONFLY_MODE"); exists {
		mode, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("unexpected dragonfly mode: %s", value)
		}

		cfg.Mode = mode
	}

	if value, exists := os.LookupEnv("DRAGONFLY_MAX_REPLICAS"); exists {
		maxReplicas, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("unexpected dragonfly max replicas: %s", value)
		}

		cfg.MaxReplicas = maxReplicas
	}

	if value, exists := os.LookupEnv("DRAGONFLY_FILTER"); exists {
		cfg.Filter = value
	}

	return &dragonfly{
		config: cfg,
		client: client,
		bucket: bucket,
	}, nil
}

func init() {
	Register("dragonfly", newDragonfly)
}
