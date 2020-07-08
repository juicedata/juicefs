package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
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

func (h *hdfsclient) path(key string) string {
	return "/" + key
}

func (h *hdfsclient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := h.c.Open(h.path(key))
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
	path := h.path(key)
	if strings.HasSuffix(path, dirSuffix) {
		return h.c.MkdirAll(path, os.FileMode(0755))
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	f, err := h.c.CreateFile(tmp, 3, 128<<20, 0755)
	if err != nil {
		if pe, ok := err.(*os.PathError); ok && pe.Err == os.ErrNotExist {
			h.c.MkdirAll(filepath.Dir(path), 0755)
			f, err = h.c.CreateFile(tmp, 3, 128<<20, 0755)
		}
		if pe, ok := err.(*os.PathError); ok && pe.Err == os.ErrExist {
			h.c.Remove(tmp)
			f, err = h.c.CreateFile(tmp, 3, 128<<20, 0755)
		}
		if err != nil {
			return err
		}
	}
	defer h.c.Remove(tmp)
	_, err = io.Copy(f, in)
	if err != nil {
		f.Close()
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}
	return h.c.Rename(tmp, path)
}

func (h *hdfsclient) Exists(key string) error {
	_, err := h.c.Stat(h.path(key))
	return err
}

func (h *hdfsclient) Delete(key string) error {
	return h.c.Remove(h.path(key))
}

func (h *hdfsclient) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, notSupported
}

func (h *hdfsclient) walk(path string, walkFn filepath.WalkFunc) error {
	file, err := h.c.Open(path)
	var info os.FileInfo
	if file != nil {
		info = file.Stat()
	}

	err = walkFn(path, info, err)
	if err != nil {
		if info != nil && info.IsDir() && err == filepath.SkipDir {
			return nil
		}

		return err
	}

	if info == nil || !info.IsDir() {
		return nil
	}

	infos, err := file.Readdir(0)
	if err != nil {
		return walkFn(path, info, err)
	}

	// make sure they are ordered in full path
	names := make([]string, len(infos))
	for i, info := range infos {
		if info.IsDir() {
			names[i] = info.Name() + "/"
		} else {
			names[i] = info.Name()
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if strings.HasSuffix(name, "/") {
			name = name[:len(name)-1]
		}
		err = h.walk(filepath.ToSlash(filepath.Join(path, name)), walkFn)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *hdfsclient) ListAll(prefix, marker string) (<-chan *Object, error) {
	listed := make(chan *Object, 10240)
	go func() {
		root := h.path(prefix)
		_, err := h.c.Stat(root)
		if err != nil && err.(*os.PathError).Err == os.ErrNotExist {
			root = filepath.Dir(root)
		}
		h.walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if err == io.EOF {
					err = nil // ignore
				} else {
					logger.Errorf("list %s: %s", path, err)
					listed <- nil
				}
				return err
			}
			key := path[1:]
			if !strings.HasPrefix(key, prefix) || key < marker {
				if info.IsDir() && !strings.HasPrefix(prefix, key) && !strings.HasPrefix(marker, key) {
					return filepath.SkipDir
				}
				return nil
			}
			hinfo := info.(*hdfs.FileInfo)
			f := &File{Object{key, info.Size(), info.ModTime()}, hinfo.Owner(), hinfo.OwnerGroup(), info.Mode()}
			// stickybit from HDFS is different than golang
			if f.Mode&01000 != 0 {
				f.Mode &= ^os.FileMode(01000)
				f.Mode |= os.ModeSticky
			}
			if info.IsDir() && (path != root || !strings.HasSuffix(root, "/")) {
				f.Key += "/"
				f.Size = 0
			}
			listed <- (*Object)(unsafe.Pointer(f))
			return nil
		})
		close(listed)
	}()
	return listed, nil
}

func (h *hdfsclient) Chtimes(key string, mtime time.Time) error {
	return h.c.Chtimes(h.path(key), mtime, mtime)
}

func (h *hdfsclient) Chmod(key string, mode os.FileMode) error {
	return h.c.Chmod(h.path(key), mode)
}

func (h *hdfsclient) Chown(key string, owner, group string) error {
	return h.c.Chown(h.path(key), owner, group)
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
