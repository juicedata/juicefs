// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/juicedata/juicesync/utils"
)

var logger = utils.GetLogger("juicesync")

type Object struct {
	Key   string
	Size  int64
	Mtime time.Time // Unix seconds
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
	Get(key string, off, limit int64) (io.ReadCloser, error)
	Put(key string, in io.Reader) error
	Exists(key string) error
	Delete(key string) error
	List(prefix, marker string, limit int64) ([]*Object, error)
	CreateMultipartUpload(key string) (*MultipartUpload, error)
	UploadPart(key string, uploadID string, num int, body []byte) (*Part, error)
	AbortUpload(key string, uploadID string)
	CompleteUpload(key string, uploadID string, parts []*Part) error
	ListUploads(marker string) ([]*PendingPart, string, error)
}

type MtimeChanger interface {
	Chtimes(path string, mtime time.Time) error
}

var notSupported = errors.New("not supported")

type defaultObjectStorage struct{}

func (s defaultObjectStorage) Create() error {
	return nil
}

func (s defaultObjectStorage) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return nil, notSupported
}

func (s defaultObjectStorage) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return nil, notSupported
}

func (s defaultObjectStorage) AbortUpload(key string, uploadID string) {}

func (s defaultObjectStorage) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return notSupported
}

func (s defaultObjectStorage) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func (s defaultObjectStorage) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, notSupported
}

type Register func(endpoint, accessKey, secretKey string) ObjectStorage

var storages = make(map[string]Register)

func register(name string, register Register) {
	storages[name] = register
}

func CreateStorage(name, endpoint, accessKey, secretKey string) ObjectStorage {
	f, ok := storages[name]
	if ok {
		logger.Debugf("Creating %s storage at endpoint %s", name, endpoint)
		return f(endpoint, accessKey, secretKey)
	}
	panic(fmt.Sprintf("invalid storage: %s", name))
}
