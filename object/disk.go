package object

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"jfs/mount/storage/utils"
)

type diskStore struct {
	dir string
}

func (d *diskStore) String() string {
	return "file://" + d.dir
}

func (d *diskStore) Create() error {
	return os.MkdirAll(d.dir, os.FileMode(0700))
}

func (d *diskStore) path(key string) string {
	return filepath.Join(d.dir, key)
}

func (d *diskStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	p := d.path(key)
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if _, err := f.Seek(off, 0); err != nil {
			f.Close()
			return nil, err
		}
	}
	if limit > 0 {
		buf := make([]byte, limit)
		if _, err = f.Read(buf); err != nil {
			return nil, err
		}
		return ioutil.NopCloser(bytes.NewBuffer(buf)), nil
	}
	return f, err
}

func (d *diskStore) Put(key string, in io.Reader) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), os.FileMode(0700)); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, in)
	return err
}

func (d *diskStore) Copy(dst, src string) error {
	r, err := d.Get(src, 0, -1)
	if err != nil {
		return err
	}
	return d.Put(dst, r)
}

func (d *diskStore) Exists(key string) error {
	if utils.Exists(d.path(key)) {
		return nil
	}
	return errors.New("not exists")
}

func (d *diskStore) Delete(key string) error {
	if d.Exists(key) != nil {
		return errors.New("not exists")
	}
	return os.Remove(d.path(key))
}

func (d *diskStore) List(prefix, marker string, limit int64) ([]*Object, error) {
	var objs []*Object
	filepath.Walk(d.dir, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			key := path[len(d.dir)+1:]
			if key > marker && strings.HasPrefix(key, prefix) {
				t := int(info.ModTime().Unix())
				objs = append(objs, &Object{key, info.Size(), t, t})
				if len(objs) == int(limit) {
					return errors.New("enough")
				}
			}
		}
		return nil
	})
	return objs, nil
}

func (d *diskStore) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	dir, err := ioutil.TempDir("", "multipart")
	return &MultipartUpload{UploadID: dir, MinPartSize: 1 << 20, MaxCount: 1000}, err
}

func (d *diskStore) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	path := filepath.Join(uploadID, strconv.Itoa(num))
	return &Part{Num: num, ETag: path}, ioutil.WriteFile(path, body, os.FileMode(0700))
}

func (d *diskStore) AbortUpload(key string, uploadID string) {
	fs, err := ioutil.ReadDir(uploadID)
	if err == nil {
		for _, f := range fs {
			os.Remove(filepath.Join(uploadID, f.Name()))
		}
	}
}

func (d *diskStore) CompleteUpload(key string, uploadID string, parts []*Part) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), os.FileMode(0700)); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	for i, p := range parts {
		if i+1 != int(p.Num) {
			return fmt.Errorf("unexpected num %d", p.Num)
		}
		r, e := os.Open(p.ETag)
		if e != nil {
			return e
		}
		defer r.Close()
		d, e := ioutil.ReadAll(r)
		if e != nil {
			return e
		}
		if _, e := f.Write(d); e != nil {
			return e
		}
	}
	return nil
}

func (d *diskStore) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func newDisk(endpoint, accesskey, secretkey string) ObjectStorage {
	store := &diskStore{dir: endpoint}
	return store
}

func init() {
	RegisterStorage("file", newDisk)
}
