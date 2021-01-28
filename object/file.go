// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"jfs/mount/storage/utils"
)

type filestore struct {
	dir string
}

func (d *filestore) String() string {
	return "file://" + d.dir
}

func (d *filestore) Create() error {
	return os.MkdirAll(d.dir, os.FileMode(0700))
}

func (d *filestore) path(key string) string {
	return filepath.Join(d.dir, key)
}

func (d *filestore) Get(key string, off, limit int64) (io.ReadCloser, error) {
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

func (d *filestore) Put(key string, in io.Reader) error {
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

func (d *filestore) Copy(dst, src string) error {
	r, err := d.Get(src, 0, -1)
	if err != nil {
		return err
	}
	return d.Put(dst, r)
}

func (d *filestore) Exists(key string) error {
	if utils.Exists(d.path(key)) {
		return nil
	}
	return errors.New("not exists")
}

func (d *filestore) Delete(key string) error {
	if d.Exists(key) != nil {
		return errors.New("not exists")
	}
	return os.Remove(d.path(key))
}

// walk recursively descends path, calling w.
func walk(path string, info os.FileInfo, walkFn filepath.WalkFunc) error {
	err := walkFn(path, info, nil)
	if err != nil {
		if info.IsDir() && err == filepath.SkipDir {
			return nil
		}
		return err
	}

	if !info.IsDir() {
		return nil
	}

	names, err := readDirNames(path)
	if err != nil {
		return walkFn(path, info, err)
	}

	for _, name := range names {
		filename := filepath.Join(path, name)
		fileInfo, err := os.Lstat(filename)
		if err != nil {
			if err := walkFn(filename, fileInfo, err); err != nil && err != filepath.SkipDir {
				return err
			}
		} else {
			err = walk(filename, fileInfo, walkFn)
			if err != nil {
				if !fileInfo.IsDir() || err != filepath.SkipDir {
					return err
				}
			}
		}
	}
	return nil
}

// Walk walks the file tree rooted at root, calling walkFn for each file or
// directory in the tree, including root. All errors that arise visiting files
// and directories are filtered by walkFn. The files are walked in lexical
// order, which makes the output deterministic but means that for very
// large directories Walk can be inefficient.
// Walk does not follow symbolic links.
func Walk(root string, walkFn filepath.WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		err = walkFn(root, nil, err)
	} else {
		err = walk(root, info, walkFn)
	}
	if err == filepath.SkipDir {
		return nil
	}
	return err
}

// readDirNames reads the directory named by dirname and returns
// a sorted list of directory entries.
func readDirNames(dirname string) ([]string, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(fi))
	for i := range fi {
		if fi[i].IsDir() {
			names[i] = fi[i].Name() + "/"
		} else {
			names[i] = fi[i].Name()
		}
	}
	sort.Strings(names)
	return names, nil
}

func (d *filestore) List(prefix, marker string, limit int64) ([]*Object, error) {
	var objs []*Object
	Walk(d.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Size() > 0 {
			key := path[len(d.dir):]
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

func (d *filestore) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	dir, err := ioutil.TempDir("", "multipart")
	return &MultipartUpload{UploadID: dir, MinPartSize: 1 << 20, MaxCount: 1000}, err
}

func (d *filestore) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	path := filepath.Join(uploadID, strconv.Itoa(num))
	return &Part{Num: num, ETag: path}, ioutil.WriteFile(path, body, os.FileMode(0700))
}

func (d *filestore) AbortUpload(key string, uploadID string) {
	fs, err := ioutil.ReadDir(uploadID)
	if err == nil {
		for _, f := range fs {
			os.Remove(filepath.Join(uploadID, f.Name()))
		}
	}
}

func (d *filestore) CompleteUpload(key string, uploadID string, parts []*Part) error {
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

func (d *filestore) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func newDisk(endpoint, accesskey, secretkey string) ObjectStorage {
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	store := &filestore{dir: endpoint}
	return store
}

func init() {
	register("file", newDisk)
}
