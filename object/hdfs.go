// Copyright (C) 2020-present Juicedata Inc.

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"github.com/colinmarc/hdfs/v2"
	"github.com/colinmarc/hdfs/v2/hadoopconf"
	krb "github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
)

var superuser = "hdfs"
var supergroup = "supergroup"

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

func (h *hdfsclient) Head(key string) (*Object, error) {
	info, err := h.c.Stat(h.path(key))
	if err != nil {
		return nil, err
	}

	hinfo := info.(*hdfs.FileInfo)
	f := &File{
		Object{
			key,
			info.Size(),
			info.ModTime(),
			info.IsDir(),
		},
		hinfo.Owner(),
		hinfo.OwnerGroup(),
		info.Mode(),
	}
	if f.Owner == superuser {
		f.Owner = "root"
	}
	if f.Group == supergroup {
		f.Group = "root"
	}
	// stickybit from HDFS is different than golang
	if f.Mode&01000 != 0 {
		f.Mode &= ^os.FileMode(01000)
		f.Mode |= os.ModeSticky
	}
	if info.IsDir() {
		f.Size = 0
		if !strings.HasSuffix(f.Key, "/") {
			f.Key += "/"
		}
	}
	return (*Object)(unsafe.Pointer(f)), nil
}

type withCloser struct {
	io.Reader
	io.Closer
}

func (h *hdfsclient) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := h.c.Open(h.path(key))
	if err != nil {
		return nil, err
	}

	finfo := f.Stat()
	if finfo.IsDir() {
		return ioutil.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}
	if limit > 0 {
		return withCloser{io.LimitReader(f, limit), f}, nil
	}
	return f, nil
}

const abcException = "org.apache.hadoop.hdfs.protocol.AlreadyBeingCreatedException"

func (h *hdfsclient) Put(key string, in io.Reader) error {
	path := h.path(key)
	if strings.HasSuffix(path, dirSuffix) {
		return h.c.MkdirAll(path, os.FileMode(0755))
	}
	tmp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.tmp.%d", filepath.Base(path), rand.Int()))
	f, err := h.c.CreateFile(tmp, 3, 128<<20, 0755)
	defer h.c.Remove(tmp)
	if err != nil {
		if pe, ok := err.(*os.PathError); ok && pe.Err == os.ErrNotExist {
			h.c.MkdirAll(filepath.Dir(path), 0755)
			f, err = h.c.CreateFile(tmp, 3, 128<<20, 0755)
		}
		if pe, ok := err.(*os.PathError); ok {
			if remoteErr, ok := pe.Err.(hdfs.Error); ok && remoteErr.Exception() == abcException {
				pe.Err = os.ErrExist
			}
			if pe.Err == os.ErrExist {
				h.c.Remove(tmp)
				f, err = h.c.CreateFile(tmp, 3, 128<<20, 0755)
			}
		}
		if err != nil {
			return err
		}
	}
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
	root := h.path(prefix)
	_, err := h.c.Stat(root)
	if err != nil && err.(*os.PathError).Err == os.ErrNotExist && !strings.HasSuffix(prefix, "/") {
		root = filepath.Dir(root)
	}
	_, err = h.c.Stat(root)
	if err != nil && err.(*os.PathError).Err == os.ErrNotExist {
		close(listed)
		return listed, nil // return empty list
	}
	go func() {
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
			f := &File{
				Object{
					key,
					info.Size(),
					info.ModTime(),
					info.IsDir(),
				},
				hinfo.Owner(),
				hinfo.OwnerGroup(),
				info.Mode(),
			}
			if f.Owner == superuser {
				f.Owner = "root"
			}
			if f.Group == supergroup {
				f.Group = "root"
			}
			// stickybit from HDFS is different than golang
			if f.Mode&01000 != 0 {
				f.Mode &= ^os.FileMode(01000)
				f.Mode |= os.ModeSticky
			}
			if info.IsDir() {
				f.Size = 0
				if path != root || !strings.HasSuffix(root, "/") {
					f.Key += "/"
				}
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
	if owner == "root" {
		owner = superuser
	}
	if group == "root" {
		group = supergroup
	}
	return h.c.Chown(h.path(key), owner, group)
}

// borrowed from https://github.com/colinmarc/hdfs/blob/master/cmd/hdfs/kerberos.go
func getKerberosClient() (*krb.Client, error) {
	configPath := os.Getenv("KRB5_CONFIG")
	if configPath == "" {
		configPath = "/etc/krb5.conf"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Determine the ccache location from the environment, falling back to the
	// default location.
	ccachePath := os.Getenv("KRB5CCNAME")
	if strings.Contains(ccachePath, ":") {
		if strings.HasPrefix(ccachePath, "FILE:") {
			ccachePath = strings.SplitN(ccachePath, ":", 2)[1]
		} else {
			return nil, fmt.Errorf("unusable ccache: %s", ccachePath)
		}
	} else if ccachePath == "" {
		u, err := user.Current()
		if err != nil {
			return nil, err
		}

		ccachePath = fmt.Sprintf("/tmp/krb5cc_%s", u.Uid)
	}

	ccache, err := credentials.LoadCCache(ccachePath)
	if err != nil {
		return nil, err
	}

	client, err := krb.NewFromCCache(ccache, cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func newHDFS(addr, username, sk string) ObjectStorage {
	conf, err := hadoopconf.LoadFromEnvironment()
	if err != nil {
		logger.Fatalf("Problem loading configuration: %s", err)
	}

	options := hdfs.ClientOptionsFromConf(conf)
	if addr != "" {
		options.Addresses = strings.Split(addr, ",")
	}

	if options.KerberosClient != nil {
		options.KerberosClient, err = getKerberosClient()
		if err != nil {
			logger.Fatalf("Problem with kerberos authentication: %s", err)
		}
	} else {
		if username == "" {
			username = os.Getenv("HADOOP_USER_NAME")
		}
		if username == "" {
			current, err := user.Current()
			if err != nil {
				logger.Fatalf("get current user: %s", err)
			}
			username = current.Username
		}
		options.User = username
	}

	c, err := hdfs.NewClient(options)
	if err != nil {
		logger.Fatalf("new HDFS client %s: %s", addr, err)
	}
	if os.Getenv("HADOOP_SUPER_USER") != "" {
		superuser = os.Getenv("HADOOP_SUPER_USER")
	}
	if os.Getenv("HADOOP_SUPER_GROUP") != "" {
		supergroup = os.Getenv("HADOOP_SUPER_GROUP")
	}

	return &hdfsclient{addr: addr, c: c}
}

func init() {
	register("hdfs", newHDFS)
}
