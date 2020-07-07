package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/colinmarc/hdfs"
)

type hdfsclient struct {
	defaultObjectStorage
	addr string
	c    *hdfs.Client
}

func (h *hdfsclient) String() string {
	return fmt.Sprintf("hdfs://%s", h.addr)
}

func (h *hdfsclient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := h.c.Open("/" + key)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}
	if limit > 0 {
		defer f.Close()
		buf := make([]byte, limit)
		if n, err := f.Read(buf); err != nil {
			return nil, err
		} else {
			return ioutil.NopCloser(bytes.NewBuffer(buf[:n])), nil
		}
	}
	return f, nil
}

func (h *hdfsclient) Put(key string, in io.Reader) error {
	path := "/" + key
	f, err := h.c.CreateFile(path, 3, 128<<20, 0755)
	if err != nil {
		if pe, ok := err.(*os.PathError); ok && pe.Err == os.ErrNotExist {
			h.c.MkdirAll(filepath.Dir(path), 0755)
			f, err = h.c.CreateFile(path, 3, 128<<20, 0755)
		}
		if pe, ok := err.(*os.PathError); ok && pe.Err == os.ErrExist {
			h.c.Remove(path)
			f, err = h.c.CreateFile(path, 3, 128<<20, 0755)
		}
		if err != nil {
			return err
		}
	}
	f.Write(nil)
	_, err = io.Copy(f, in)
	if err != nil && err != io.EOF {
		f.Close()
		h.c.Remove(path)
		return err
	}
	err = f.Close()
	if err != nil {
		h.c.Remove(path)
	}
	return err
}

func (h *hdfsclient) Exists(key string) error {
	_, err := h.c.Stat("/" + key)
	return err
}

func (h *hdfsclient) Delete(key string) error {
	return h.c.Remove("/" + key)
}

func (h *hdfsclient) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, notSupported
}

func (h *hdfsclient) ListAll(prefix, marker string) (<-chan *Object, error) {
	listed := make(chan *Object, 10240)
	go func() {
		root := "/" + prefix
		_, err := h.c.Stat(root)
		if err != nil && err.(*os.PathError).Err == os.ErrNotExist {
			root = filepath.Dir(root)
		}
		prefix = "/" + prefix
		h.c.Walk(root, func(path string, info os.FileInfo, err error) error {
			if info == nil {
				return nil // workaround
			}
			if err != nil {
				logger.Errorf("list %s: %s", path, err)
				listed <- nil
				return err
			}
			if !strings.HasPrefix(path, prefix) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			key := path[1:]
			if key < marker {
				return nil
			}
			if !info.IsDir() {
				hinfo := info.(*hdfs.FileInfo)
				f := &File{Object{key, info.Size(), info.ModTime()}, hinfo.Owner(), hinfo.OwnerGroup(), info.Mode()}
				listed <- (*Object)(unsafe.Pointer(f))
			}
			return nil
		})
		close(listed)
	}()
	return listed, nil
}

func (h *hdfsclient) Chtimes(path string, mtime time.Time) error {
	return h.c.Chtimes(path, mtime, mtime)
}

func (h *hdfsclient) Chmod(path string, mode os.FileMode) error {
	return h.c.Chmod(path, mode)
}

func (h *hdfsclient) Chown(path string, owner, group string) error {
	return h.c.Chown(path, owner, group)
}

// TODO: multipart upload

func newHDFS(addr, user, sk string) ObjectStorage {
	c, err := hdfs.NewForUser(addr, user)
	if err != nil {
		logger.Fatalf("new HDFS client %s: %s", addr, err)
	}
	return &hdfsclient{addr: addr, c: c}
}

func init() {
	register("hdfs", newHDFS)
}
