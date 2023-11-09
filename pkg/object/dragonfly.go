package object

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-http-utils/headers"
)

const (
	// AsyncWriteBack writes the object asynchronously to the backend.
	AsyncWriteBack = iota

	// WriteBack writes the object synchronously to the backend.
	WriteBack

	// Ephemeral only writes the object to the dfdaemon.
	// It is only provided for creating temporary objects between peers,
	// and users are not allowed to use this mode.
	Ephemeral
)

const (
	// HeaderDragonflyObjectMetaLastModifiedTime is used for last modified time of object storage.
	HeaderDragonflyObjectMetaLastModifiedTime = "X-Dragonfly-Object-Meta-Last-Modified-Time"

	// HeaderDragonflyObjectMetaStorageClass is used for storage class of object storage.
	HeaderDragonflyObjectMetaStorageClass = "X-Dragonfly-Object-Meta-Storage-Class"

	// HeaderDragonflyObjectOperation is used for object storage operation.
	HeaderDragonflyObjectOperation = "X-Dragonfly-Object-Operation"
)

const (
	// Upper limit of maxGetObjectMetadatas.
	MaxGetObjectMetadatasLimit = 1000

	// Upper limit of maxReplicas.
	MaxReplicasLimit = 100
)

const (
	// CopyOperation is the operation of copying object.
	CopyOperation = "copy"
)

const (
	FilterOSS = "Expires&Signature"
	FilterS3  = "X-Amz-Algorithm&X-Amz-Credential&X-Amz-Date&X-Amz-Expires&X-Amz-SignedHeaders&X-Amz-Signature"
	FilterOBS = "X-Amz-Algorithm&X-Amz-Credential&X-Amz-Date&X-Obs-Date&X-Amz-Expires&X-Amz-SignedHeaders&X-Amz-Signature"
)

type ObjectMetadatas struct {
	// CommonPrefixes are similar prefixes in object storage.
	CommonPrefixes []string `json:"CommonPrefixes"`

	// Metadatas are object metadata.
	Metadatas []*ObjectMetadata `json:"Metadatas"`
}

type ObjectMetadata struct {
	// Key is object key.
	Key string

	// ContentDisposition is Content-Disposition header.
	ContentDisposition string

	// ContentEncoding is Content-Encoding header.
	ContentEncoding string

	// ContentLanguage is Content-Language header.
	ContentLanguage string

	// ContentLanguage is Content-Length header.
	ContentLength int64

	// ContentType is Content-Type header.
	ContentType string

	// ETag is ETag header.
	ETag string

	// Digest is object digest.
	Digest string

	// LastModifiedTime is last modified time.
	LastModifiedTime time.Time

	// StorageClass is object storage class.
	StorageClass string
}

type ObjectStorageMetadata struct {
	// Name is object storage name of type, it can be s3, oss or obs.
	Name string

	// Region is storage region.
	Region string

	// Endpoint is datacenter endpoint.
	Endpoint string
}

type dragonfly struct {
	// DefaultObjectStorage is the default object storage.
	DefaultObjectStorage

	// Address of the object storage service.
	endpoint string

	// Filter is used to generate a unique Task ID by
	// filtering unnecessary query params in the URL,
	// it is separated by & character.
	filter string

	// Mode is the mode in which the backend is written,
	// including WriteBack and AsyncWriteBack.
	mode int

	// MaxReplicas is the maximum number of
	// replicas of an object cache in seed peers.
	maxReplicas int

	// ObjectStorage bucket name.
	bucket string

	// http client.
	client *http.Client
}

// String returns the string representation of the dragonfly.
func (d *dragonfly) String() string {
	return fmt.Sprintf("dragonfly://%s/", d.bucket)
}

// Create creates the object if it does not exist.
func (d *dragonfly) Create() error {
	if _, err := d.List("", "", "", 1, false); err == nil {
		return nil
	}

	// get create bucket request.
	u, err := url.Parse(d.endpoint)
	if err != nil {
		return err
	}

	u.Path = path.Join("buckets", d.bucket)

	query := u.Query()

	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)

	if err != nil && !isExists(err) {
		return err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("bad response status %s", resp.Status)
	}

	return nil
}

// Head returns the object metadata if it exists.
func (d *dragonfly) Head(key string) (Object, error) {
	// get get object metadata request.
	u, err := url.Parse(d.endpoint)
	if err != nil {
		return nil, err
	}

	u.Path = path.Join("buckets", d.bucket, "objects", key)

	if strings.HasSuffix(key, "/") {
		u.Path += "/"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Head object.
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		if resp.StatusCode == http.StatusNotFound {
			err = os.ErrNotExist
		}

		return nil, err
	}

	contentLength, err := strconv.ParseInt(resp.Header.Get(headers.ContentLength), 10, 64)
	if err != nil {
		return nil, err
	}

	lastModifiedTime, err := time.Parse(http.TimeFormat, resp.Header.Get(HeaderDragonflyObjectMetaLastModifiedTime))
	if err != nil {
		return nil, err
	}

	return &obj{
		key,
		int64(contentLength),
		lastModifiedTime,
		strings.HasSuffix(key, "/"),
		resp.Header.Get(HeaderDragonflyObjectMetaStorageClass),
	}, nil
}

// Get returns the object if it exists.
func (d *dragonfly) Get(key string, off, limit int64) (io.ReadCloser, error) {
	// get get object request.
	u, err := url.Parse(d.endpoint)
	if err != nil {
		return nil, err
	}

	u.Path = path.Join("buckets", d.bucket, "objects", key)

	if strings.HasSuffix(key, "/") {
		u.Path += "/"
	}

	query := u.Query()
	if d.filter != "" {
		query.Add("filter", d.filter)
	}

	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set(headers.Range, getRange(off, limit))

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bad response status %s", resp.Status)
	}

	return resp.Body, nil
}

// Put creates or replaces the object.
func (d *dragonfly) Put(key string, data io.Reader) error {
	// get put object request.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// AsyncWriteBack mode is used by default.
	if err := writer.WriteField("mode", fmt.Sprint(d.mode)); err != nil {
		return err
	}

	if d.filter != "" {
		if err := writer.WriteField("filter", d.filter); err != nil {
			return err
		}
	}

	if d.maxReplicas > 0 {
		if err := writer.WriteField("maxReplicas", fmt.Sprint(d.maxReplicas)); err != nil {
			return err
		}
	}

	part, err := writer.CreateFormFile("file", path.Base(key))
	if err != nil {
		return err
	}

	if _, err := io.Copy(part, data); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	u, err := url.Parse(d.endpoint)
	if err != nil {
		return err
	}

	u.Path = path.Join("buckets", d.bucket, "objects", key)

	if strings.HasSuffix(key, "/") {
		u.Path += "/"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Add(headers.ContentType, writer.FormDataContentType())

	// Put object.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("bad response status %s", resp.Status)
	}

	return nil
}

// Copy copies the object if it exists.
func (d *dragonfly) Copy(dst, src string) error {
	// get copy object request.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("source_object_key", src); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	u, err := url.Parse(d.endpoint)
	if err != nil {
		return err
	}

	u.Path = path.Join("buckets", d.bucket, "objects", dst)

	query := u.Query()

	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}

	req.Header.Add(headers.ContentType, writer.FormDataContentType())
	req.Header.Add(HeaderDragonflyObjectOperation, fmt.Sprint(CopyOperation))

	// copy object.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("bad response status %s", resp.Status)
	}

	return nil
}

// Delete deletes the object if it exists.
func (d *dragonfly) Delete(key string) error {
	// get delete object request.
	u, err := url.Parse(d.endpoint)
	if err != nil {
		return err
	}

	u.Path = path.Join("buckets", d.bucket, "objects", key)

	if strings.HasSuffix(key, "/") {
		u.Path += "/"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}

	// Delete object.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("bad response status %s", resp.Status)
	}

	return nil
}

// List lists the objects with the given prefix.
func (d *dragonfly) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if limit > MaxGetObjectMetadatasLimit {
		limit = MaxGetObjectMetadatasLimit
	}

	u, err := url.Parse(d.endpoint)
	if err != nil {
		return nil, err
	}

	u.Path = path.Join("buckets", d.bucket, "metadatas")

	query := u.Query()
	if prefix != "" {
		query.Set("prefix", prefix)
	}

	if marker != "" {
		query.Set("marker", marker)
	}

	if delimiter != "" {
		query.Set("delimiter", delimiter)
	}

	if limit != 0 {
		query.Set("limit", fmt.Sprint(limit))
	}

	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	// List object.
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bad response status %s", resp.Status)
	}

	var objectMetadatas ObjectMetadatas
	if err := json.NewDecoder(resp.Body).Decode(&objectMetadatas); err != nil {
		return nil, err
	}

	objs := make([]Object, 0, len(objectMetadatas.Metadatas))
	for _, meta := range objectMetadatas.Metadatas {
		objs = append(objs, &obj{
			meta.Key,
			meta.ContentLength,
			meta.LastModifiedTime,
			strings.HasSuffix(meta.Key, "/"),
			meta.StorageClass,
		})
	}

	if delimiter != "" {
		for _, o := range objectMetadatas.CommonPrefixes {
			objs = append(objs, &obj{o, 0, time.Unix(0, 0), true, ""})
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key() < objs[j].Key() })
	}

	return objs, err
}

// newDragonfly creates a new dragonfly object storage.
func newDragonfly(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	// Parse the endpoint.
	uri, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	var (
		mode        int = 1
		maxReplicas int
		filter      string
	)

	endpoint = uri.Scheme + "://" + uri.Host
	bucket := uri.Path

	if bucket == "" {
		return nil, fmt.Errorf("bucket name required")
	}

	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}

	// Initialize dfstore config.
	if value := uri.Query().Get("mode"); value != "" {
		mode, err = strconv.Atoi(value)
		if err != nil || (mode != WriteBack && mode != AsyncWriteBack) {
			return nil, fmt.Errorf("unexpected dragonfly mode: %s", value)
		}
	}

	if value := uri.Query().Get("maxReplicas"); value != "" {
		maxReplicas, err = strconv.Atoi(value)
		if err != nil || maxReplicas > MaxReplicasLimit || maxReplicas < 0 {
			return nil, fmt.Errorf("unexpected dragonfly max replicas: %s", value)
		}
	}

	objectStorageMetadata, err := getObjectStorageMetadata(endpoint)
	if err != nil {
		return nil, err
	}

	if objectStorageMetadata.Name == "s3" {
		filter = FilterS3
	} else if objectStorageMetadata.Name == "oss" {
		filter = FilterOSS
	} else {
		filter = FilterOBS
	}

	return &dragonfly{
		endpoint:    endpoint,
		filter:      filter,
		mode:        mode,
		maxReplicas: maxReplicas,
		bucket:      bucket,
		client:      httpClient,
	}, nil
}

func getObjectStorageMetadata(endpoint string) (*ObjectStorageMetadata, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil
	}

	u.Path = path.Join("metadata")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Get object storage Metadata.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bad response status %s", resp.Status)
	}

	var objectStorageMetadata ObjectStorageMetadata
	if err := json.NewDecoder(resp.Body).Decode(&objectStorageMetadata); err != nil {
		return nil, err
	}

	return &objectStorageMetadata, nil
}

// init registers the dragonfly object storage.
func init() {
	Register("dragonfly", newDragonfly)
}
