// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/juicedata/juicesync/utils"
	"github.com/juicedata/juicesync/versioninfo"
)

var logger = utils.GetLogger("juicesync")

var UserAgent = fmt.Sprintf("juicesync/%s", versioninfo.Version())

type Object struct {
	Key   string
	Size  int64
	Mtime time.Time // Unix seconds
	IsDir bool
}

type MultipartUpload struct {
	MinPartSize int
	MaxCount    int
	UploadID    string
}

type Part struct {
	Num  int
	Size int
	ETag string
}

type PendingPart struct {
	Key      string
	UploadID string
	Created  time.Time
}

type ObjectStorage interface {
	String() string
	Create() error
	Head(key string) (*Object, error)
	Get(key string, off, limit int64) (io.ReadCloser, error)
	Put(key string, in io.Reader) error
	Delete(key string) error
	List(prefix, marker string, limit int64) ([]*Object, error)
	ListAll(prefix, marker string) (<-chan *Object, error)
	CreateMultipartUpload(key string) (*MultipartUpload, error)
	UploadPart(key string, uploadID string, num int, body []byte) (*Part, error)
	AbortUpload(key string, uploadID string)
	CompleteUpload(key string, uploadID string, parts []*Part) error
	ListUploads(marker string) ([]*PendingPart, string, error)
}

type MtimeChanger interface {
	Chtimes(path string, mtime time.Time) error
}
type File struct {
	Object
	Owner string
	Group string
	Mode  os.FileMode
}

type FileSystem interface {
	MtimeChanger
	Chmod(path string, mode os.FileMode) error
	Chown(path string, owner, group string) error
}

var notSupported = errors.New("not supported")

type DefaultObjectStorage struct{}

func (s DefaultObjectStorage) Create() error {
	return nil
}

func (s DefaultObjectStorage) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) AbortUpload(key string, uploadID string) {}

func (s DefaultObjectStorage) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return notSupported
}

func (s DefaultObjectStorage) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func (s DefaultObjectStorage) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) ListAll(prefix, marker string) (<-chan *Object, error) {
	return nil, notSupported
}

type Creator func(bucket, accessKey, secretKey string) (ObjectStorage, error)

var storages = make(map[string]Creator)

func Register(name string, register Creator) {
	storages[name] = register
}

func CreateStorage(name, endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	f, ok := storages[name]
	if ok {
		logger.Debugf("Creating %s storage at endpoint %s", name, endpoint)
		return f(endpoint, accessKey, secretKey)
	}
	return nil, fmt.Errorf("invalid storage: %s", name)
}
