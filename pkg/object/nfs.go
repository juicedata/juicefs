//go:build !nonfs
// +build !nonfs

/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/rpc"
)

var _ ObjectStorage = &nfsStore{}

type nfsStore struct {
	DefaultObjectStorage
	username string
	host     string
	root     string

	target *nfs.Target
}

type nfsEntry struct {
	*nfs.EntryPlus
	name string
}

func (n *nfsStore) String() string {
	return fmt.Sprintf("nfs://%s@%s:%s", n.username, n.host, n.root)
}

func (n *nfsStore) path(key string) string {
	return n.root + key
}

func (n *nfsStore) Head(key string) (Object, error) {
	fi, _, err := n.target.Lookup(n.path(key))
	if err != nil {
		return nil, err
	}
	return n.fileInfo(key, fi), nil
}

func (n *nfsStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	p := n.path(key)
	ff, err := n.target.Open(p)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(p, "/") {
		_ = ff.Close()
		return io.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	if limit > 0 {
		return &SectionReaderCloser{
			SectionReader: io.NewSectionReader(ff, off, limit),
			Closer:        ff,
		}, nil
	}
	return ff, err
}

func (n *nfsStore) mkdirAll(path string, perm fs.FileMode) error {
	path = strings.TrimSuffix(path, "/")
	fi, _, err := n.target.Lookup(path)
	if err == nil {
		if fi.IsDir() {
			logger.Tracef("nfs mkdir: path %s already exists", path)
			return nil
		} else {
			return syscall.ENOTDIR
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dir, _ := filepath.Split(path)
	if dir != "." {
		if err = n.mkdirAll(dir, perm); err != nil {
			return err
		}
	}
	_, err = n.target.Mkdir(path, perm)
	return err
}

func (n *nfsStore) Put(key string, in io.Reader) error {
	p := n.path(key)
	if strings.HasSuffix(p, dirSuffix) {
		return n.mkdirAll(p, 0777)
	}
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp")
	_, err := n.target.Create(tmp, 0777)
	if os.IsNotExist(err) {
		_ = n.mkdirAll(filepath.Dir(p), 0777)
		_, err = n.target.Create(tmp, 0777)
	}
	if err != nil {
		return err
	}
	ff, err := n.target.Open(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = n.target.Remove(tmp) }()
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(ff, in, *buf)
	if err != nil {
		_ = ff.Close()
		return err
	}
	err = ff.Close()
	if err != nil {
		return err
	}
	// _ = n.target.Remove(p)
	return n.target.Rename(tmp, p)
}

func (n *nfsStore) Delete(key string) error {
	err := n.target.Remove(strings.TrimRight(n.path(key), dirSuffix))
	if err != nil && os.IsNotExist(err) {
		err = nil
	}
	return err
}

func (n *nfsStore) fileInfo(key string, fi os.FileInfo) Object {
	owner, group := getOwnerGroup(fi)
	isSymlink := !fi.Mode().IsDir() && !fi.Mode().IsRegular()
	ff := &file{
		obj{key, fi.Size(), fi.ModTime(), fi.IsDir(), ""},
		owner,
		group,
		fi.Mode(),
		isSymlink,
	}
	if fi.IsDir() {
		if key != "" && !strings.HasSuffix(key, "/") {
			ff.key += "/"
		}
		ff.size = 0
	}
	return ff
}

func (n *nfsStore) readDirSorted(dirname string) ([]*nfsEntry, error) {
	entries, err := n.target.ReadDirPlus(dirname)
	if err != nil {
		return nil, errors.Wrapf(err, "readdir %s", dirname)
	}
	nfsEntries := make([]*nfsEntry, len(entries))

	for i, e := range entries {
		if e.IsDir() {
			nfsEntries[i] = &nfsEntry{e, e.Name() + dirSuffix}
		} else {
			nfsEntries[i] = &nfsEntry{e, e.Name()}
		}
	}
	sort.Slice(nfsEntries, func(i, j int) bool { return nfsEntries[i].Name() < nfsEntries[j].Name() })
	return nfsEntries, err
}

func (d *nfsStore) toFile(key string, e *nfsEntry) *file {
	size := e.Size()
	if e.IsDir() {
		size = 0
	}
	owner, group := getOwnerGroup(e)
	return &file{
		obj{
			key,
			size,
			e.ModTime(),
			e.IsDir(),
			"",
		},
		owner,
		group,
		e.Mode(),
		false,
	}
}

func (n *nfsStore) List(prefix, marker, delimiter string, limit int64) ([]Object, error) {
	if delimiter != "/" {
		return nil, notSupported
	}
	var dir string = n.root + prefix
	var objs []Object
	if !strings.HasSuffix(dir, dirSuffix) {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := n.Head(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		objs = append(objs, obj)
	}
	entries, err := n.readDirSorted(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			p = filepath.ToSlash(p + "/")
		}
		if !strings.HasPrefix(p, n.root) {
			continue
		}
		key := p[len(n.root):]
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		f := n.toFile(key, e)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	return objs, nil
}

func (n *nfsStore) ListAll(prefix, marker string) (<-chan Object, error) {
	return nil, notSupported
}

func newNFSStore(addr, username, pass, token string) (ObjectStorage, error) {
	if strings.Contains(addr, "@") {
		ps := strings.Split(addr, "@")
		username = ps[0]
		addr = ps[1]
	} else {
		u, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("current user: %s", err)
		}
		username = u.Username
	}
	b := strings.Split(addr, ":")
	host := b[0]
	path := b[1]
	mount, err := nfs.DialMount(host, time.Second*3)
	if err != nil {
		return nil, fmt.Errorf("unable to dial MOUNT service %s: %v", addr, err)
	}
	auth := rpc.NewAuthUnix(username, uint32(os.Getuid()), uint32(os.Getgid()))
	target, err := mount.Mount(path, auth.Auth())
	if err != nil {
		return nil, fmt.Errorf("unable to mount %s: %v", addr, err)
	}
	return &nfsStore{DefaultObjectStorage{}, username, host, path, target}, nil

}

func init() {
	Register("nfs", newNFSStore)
}
