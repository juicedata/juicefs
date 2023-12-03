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
	"math/rand"
	"os"
	"os/user"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
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
	name      string
	fi        os.FileInfo
	isSymlink bool
}

func (e *nfsEntry) Name() string {
	return e.name
}

func (e *nfsEntry) Size() int64 {
	if e.fi != nil {
		return e.fi.Size()
	}
	return e.EntryPlus.Size()
}

func (e *nfsEntry) Info() (os.FileInfo, error) {
	if e.fi != nil {
		return e.fi, nil
	}
	return e.EntryPlus, nil
}

func (e *nfsEntry) IsDir() bool {
	if e.fi != nil {
		return e.fi.IsDir()
	}
	return e.EntryPlus.IsDir()
}

func (n *nfsStore) String() string {
	return fmt.Sprintf("nfs://%s@%s:%s", n.username, n.host, n.root)
}

func (n *nfsStore) path(key string) string {
	if key == "" {
		return "./"
	}
	return key
}

func (n *nfsStore) Head(key string) (Object, error) {
	p := n.path(key)
	fi, _, err := n.target.Lookup(p)
	if err != nil {
		return nil, err
	}
	if attr, ok := fi.(*nfs.Fattr); ok && attr.Type == nfs.NF3Lnk {
		src, err := n.Readlink(p)
		if err != nil {
			return nil, err
		}
		dir, _ := path.Split(p)
		return n.Head(path.Join(dir, src))
	}
	return n.fileInfo(key, fi), nil
}

func (n *nfsStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	p := n.path(key)
	if strings.HasSuffix(p, "/") {
		return io.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	ff, err := n.target.Open(p)
	if err != nil {
		return nil, errors.Wrapf(err, "open %s", p)
	}

	if limit > 0 {
		return &SectionReaderCloser{
			SectionReader: io.NewSectionReader(ff, off, limit),
			Closer:        ff,
		}, nil
	}
	return ff, err
}

func (n *nfsStore) mkdirAll(p string, perm fs.FileMode) error {
	p = strings.TrimSuffix(p, "/")
	fi, _, err := n.target.Lookup(p)
	if err == nil {
		if fi.IsDir() {
			logger.Tracef("nfs mkdir: path %s already exists", p)
			return nil
		} else {
			return syscall.ENOTDIR
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dir, _ := path.Split(p)
	if dir != "." {
		if err = n.mkdirAll(dir, perm); err != nil {
			return err
		}
	}
	_, err = n.target.Mkdir(p, perm)
	return err
}

func (n *nfsStore) Put(key string, in io.Reader) (err error) {
	p := n.path(key)
	if strings.HasSuffix(p, dirSuffix) {
		return n.mkdirAll(p, 0777)
	}
	var tmp string
	if PutInplace {
		tmp = p
	} else {
		name := path.Base(p)
		if len(name) > 200 {
			name = name[:200]
		}
		tmp = path.Join(path.Dir(p), fmt.Sprintf(".%s.tmp.%d", name, rand.Int()))
		defer func() {
			if err != nil {
				_ = n.target.Remove(tmp)
			}
		}()
	}
	_, err = n.target.Create(tmp, 0777)
	if os.IsNotExist(err) {
		_ = n.mkdirAll(path.Dir(p), 0777)
		_, err = n.target.Create(tmp, 0777)
	}
	if os.IsExist(err) {
		_ = n.target.Remove(tmp)
		_, err = n.target.Create(tmp, 0777)
	}
	if err != nil {
		return errors.Wrapf(err, "create %s", tmp)
	}
	ff, err := n.target.Open(tmp)
	if err != nil {
		return err
	}

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
	if !PutInplace {
		// overwrite dst
		err = n.target.Rename(tmp, p)
	}
	return err
}

func (n *nfsStore) Delete(key string) error {
	path := n.path(key)
	if path == "./" {
		return nil
	}
	fi, _, err := n.target.Lookup(path)
	if err != nil {
		if nfs.IsNotDirError(err) || os.IsNotExist(err) {
			return nil
		}
		return err
	}
	p := strings.TrimSuffix(path, "/")
	if fi.IsDir() {
		err = n.target.RmDir(p)
	} else {
		err = n.target.Remove(p)
	}
	if err != nil && os.IsNotExist(err) {
		err = nil
	}
	return err
}

func (n *nfsStore) fileInfo(key string, fi os.FileInfo) Object {
	owner, group := n.getOwnerGroup(fi)
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

func (n *nfsStore) readDirSorted(dir string, followLink bool) ([]*nfsEntry, error) {
	o, err := n.Head(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return nil, err
	}
	dirname := o.Key()
	entries, err := n.target.ReadDirPlus(dirname)
	if err != nil {
		return nil, errors.Wrapf(err, "readdir %s", dirname)
	}
	nfsEntries := make([]*nfsEntry, len(entries))
	for i, e := range entries {
		if e.IsDir() {
			nfsEntries[i] = &nfsEntry{e, e.Name() + dirSuffix, nil, false}
		} else if e.Attr.Attr.Type == nfs.NF3Lnk && followLink {
			// follow symlink
			nfsEntries[i] = &nfsEntry{e, e.Name(), nil, true}
			src, err := n.Readlink(path.Join(dirname, e.Name()))
			if err != nil {
				logger.Errorf("readlink %s: %s", e.Name(), err)
				continue
			}
			srcPath := path.Clean(path.Join(dirname, src))
			fi, _, err := n.target.Lookup(srcPath)
			if err != nil {
				logger.Warnf("follow link `%s`: lookup `%s`: %s", path.Join(dirname, e.Name()), srcPath, err)
				continue
			}
			name := e.Name()
			if fi.IsDir() {
				name = e.Name() + dirSuffix
			}
			nfsEntries[i] = &nfsEntry{e, name, fi, false}
		} else {
			nfsEntries[i] = &nfsEntry{e, e.Name(), nil, e.Attr.Attr.Type == nfs.NF3Lnk}
		}
	}
	sort.Slice(nfsEntries, func(i, j int) bool { return nfsEntries[i].Name() < nfsEntries[j].Name() })
	return nfsEntries, err
}

func (n *nfsStore) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	if delimiter != "/" {
		return nil, notSupported
	}
	dir := prefix
	var objs []Object
	if dir != "" && !strings.HasSuffix(dir, dirSuffix) {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := n.Head(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		objs = append(objs, obj)
	}
	entries, err := n.readDirSorted(dir, followLink)
	if err != nil {
		if os.IsPermission(err) || errors.Is(err, nfs.NFS3Error(nfs.NFS3ErrAcces)) {
			logger.Warnf("skip %s: %s", dir, err)
			return nil, nil
		}
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		if e.IsDir() && !e.isSymlink {
			p = p + "/"
		}
		if !strings.HasPrefix(p, prefix) || (marker != "" && p <= marker) {
			continue
		}
		f := toFile(p, e, e.isSymlink, n.getOwnerGroup)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	return objs, nil
}

func (n *nfsStore) setAttr(path string, attrSet func(attr *nfs.Fattr) nfs.Sattr3) error {
	p := n.path(path)
	fi, fh, err := n.target.Lookup(p)
	if err != nil {
		return err
	}
	fattr := fi.(*nfs.Fattr)
	_, err = n.target.SetAttr(fh, attrSet(fattr))
	return err
}

func (n *nfsStore) Chtimes(path string, mtime time.Time) error {
	return n.setAttr(path, func(attr *nfs.Fattr) nfs.Sattr3 {
		return nfs.Sattr3{
			Mtime: nfs.SetTime{
				SetIt: nfs.SetToClientTime,
				Time: nfs.NFS3Time{
					Seconds:  uint32(mtime.Unix()),
					Nseconds: uint32(mtime.Nanosecond()),
				},
			},
		}
	})
}

func (n *nfsStore) Chmod(path string, mode os.FileMode) error {
	return n.setAttr(path, func(attr *nfs.Fattr) nfs.Sattr3 {
		return nfs.Sattr3{
			Mode: nfs.SetMode{
				SetIt: true,
				Mode:  uint32(mode),
			},
		}
	})
}

func (n *nfsStore) Chown(path string, owner, group string) error {
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	return n.setAttr(path, func(attr *nfs.Fattr) nfs.Sattr3 {
		return nfs.Sattr3{
			UID: nfs.SetUID{
				SetIt: true,
				UID:   uint32(uid),
			},
			GID: nfs.SetUID{
				SetIt: true,
				UID:   uint32(gid),
			},
		}
	})
}

func (n *nfsStore) Symlink(oldName, newName string) error {
	newName = strings.TrimRight(newName, "/")
	p := n.path(newName)
	dir := path.Dir(p)
	if _, _, err := n.target.Lookup(dir); err != nil && os.IsNotExist(err) {
		if _, err := n.target.Mkdir(dir, os.FileMode(0777)); err != nil && !os.IsExist(err) {
			return errors.Wrapf(err, "mkdir %s", dir)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return n.target.Symlink(n.path(oldName), n.path(newName))
}

func (n *nfsStore) Readlink(name string) (string, error) {
	f, err := n.target.Open(n.path(name))
	if err != nil {
		return "", errors.Wrapf(err, "open %s", name)
	}
	return f.Readlink()
}

func (n *nfsStore) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (n *nfsStore) findOwnerGroup(attr *nfs.Fattr) (string, string) {
	return utils.UserName(int(attr.UID)), utils.GroupName(int(attr.GID))
}

func (n *nfsStore) getOwnerGroup(info os.FileInfo) (string, string) {
	if st, match := info.(*nfs.Fattr); match {
		return n.findOwnerGroup(st)
	}
	if st, match := info.Sys().(*nfs.Fattr); match {
		return n.findOwnerGroup(st)
	}
	return "", ""
}

func newNFSStore(addr, username, pass, token string) (ObjectStorage, error) {
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("current user: %s", err)
		}
		username = u.Username
	}
	b := strings.Split(addr, ":")
	if len(b) != 2 {
		return nil, fmt.Errorf("invalid NFS address %s", addr)
	}
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
