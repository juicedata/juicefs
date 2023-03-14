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

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
)

var skipDir syscall.Errno = 100000
var dirSuffix = "/"

func toError(eno syscall.Errno) error {
	if eno == 0 {
		return nil
	}
	return eno
}

type juiceFS struct {
	object.DefaultObjectStorage
	name string
	jfs  *fs.FileSystem
}

func (j *juiceFS) String() string {
	return fmt.Sprintf("jfs://%s/", j.name)
}

func (j *juiceFS) Create() error {
	return nil
}

func (j *juiceFS) path(key string) string {
	return dirSuffix + key
}

type jFile struct {
	f     *fs.File
	limit int64
}

func (f *jFile) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if f.limit <= 0 {
		return 0, io.EOF
	}
	if len(buf) > int(f.limit) {
		buf = buf[:f.limit]
	}
	n, err := f.f.Read(ctx, buf)
	f.limit -= int64(n)
	return n, err
}

func (f *jFile) Write(buf []byte) (int, error) {
	n, eno := f.f.Write(ctx, buf)
	return n, toError(eno)
}

func (f *jFile) Close() error {
	return toError(f.f.Close(ctx))
}

func (j *juiceFS) Get(key string, off, limit int64) (io.ReadCloser, error) {
	f, err := j.jfs.Open(ctx, j.path(key), 0)
	if err != 0 {
		return nil, err
	}
	if off > 0 {
		_, _ = f.Seek(ctx, off, io.SeekStart)
	}
	if limit <= 0 {
		limit = 1 << 62
	}
	return &jFile{f, limit}, nil
}

var bufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 128<<10)
		return &buf
	},
}

func (j *juiceFS) Put(key string, in io.Reader) error {
	p := j.path(key)
	if strings.HasSuffix(p, "/") {
		eno := j.jfs.MkdirAll(ctx, p, 0755)
		return toError(eno)
	}
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp"+strconv.Itoa(rand.Int()))
	f, eno := j.jfs.Create(ctx, tmp, 0755)
	if eno == syscall.ENOENT {
		_ = j.jfs.MkdirAll(ctx, filepath.Dir(tmp), 0755)
		f, eno = j.jfs.Create(ctx, tmp, 0755)
	}
	if eno != 0 {
		return eno
	}
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err := io.CopyBuffer(&jFile{f, 0}, in, *buf)
	if err != nil {
		_ = j.jfs.Delete(ctx, tmp)
		return err
	}
	eno = f.Close(ctx)
	if eno != 0 {
		_ = j.jfs.Delete(ctx, tmp)
		return eno
	}
	eno = j.jfs.Rename(ctx, tmp, p, 0)
	if eno != 0 {
		_ = j.jfs.Delete(ctx, tmp)
		return eno
	}
	return nil
}

func (j *juiceFS) Delete(key string) error {
	if key == "" {
		return nil
	}
	p := strings.TrimSuffix(j.path(key), dirSuffix)
	eno := j.jfs.Delete(ctx, p)
	if eno == syscall.ENOENT {
		eno = 0
	}
	return toError(eno)
}

type jObj struct {
	key string
	fi  *fs.FileStat
}

func (o *jObj) Key() string { return o.key }
func (o *jObj) Size() int64 {
	if o.fi.IsDir() {
		return 0
	}
	return o.fi.Size()
}
func (o *jObj) Mtime() time.Time  { return o.fi.ModTime() }
func (o *jObj) IsDir() bool       { return o.fi.IsDir() }
func (o *jObj) IsSymlink() bool   { return o.fi.IsSymlink() }
func (o *jObj) Owner() string     { return utils.UserName(o.fi.Uid()) }
func (o *jObj) Group() string     { return utils.GroupName(o.fi.Gid()) }
func (o *jObj) Mode() os.FileMode { return o.fi.Mode() }

func (j *juiceFS) Head(key string) (object.Object, error) {
	fi, eno := j.jfs.Stat(ctx, j.path(key))
	if eno == syscall.ENOENT {
		return nil, os.ErrNotExist
	}
	if eno != 0 {
		return nil, eno
	}
	return &jObj{key, fi}, nil
}

func (j *juiceFS) List(prefix, marker, delimiter string, limit int64) ([]object.Object, error) {
	return nil, utils.ENOTSUP
}

// walk recursively descends path, calling w.
func (j *juiceFS) walk(path string, info *fs.FileStat, isSymlink bool, walkFn WalkFunc) syscall.Errno {
	err := walkFn(path, info, isSymlink, 0)
	if err != 0 {
		if info.IsDir() && err == skipDir {
			return 0
		}
		return err
	}

	if !info.IsDir() {
		return 0
	}

	entries, err := j.readDirSorted(path)
	if err != 0 {
		return walkFn(path, info, isSymlink, err)
	}

	for _, e := range entries {
		p := path + e.name
		err = j.walk(p, e.fi, e.isSymlink, walkFn)
		if err != 0 && err != skipDir && err != syscall.ENOENT {
			return err
		}
	}
	return 0
}

func (j *juiceFS) walkRoot(root string, walkFn WalkFunc) syscall.Errno {
	var err syscall.Errno
	var lstat, info *fs.FileStat
	lstat, err = j.jfs.Lstat(ctx, root)
	if err != 0 {
		err = walkFn(root, nil, false, err)
	} else {
		isSymlink := lstat.IsSymlink()
		info, err = j.jfs.Stat(ctx, root)
		if err != 0 {
			// root is a broken link
			err = walkFn(root, lstat, isSymlink, 0)
		} else {
			err = j.walk(root, info, isSymlink, walkFn)
		}
	}

	if err == skipDir {
		return 0
	}
	return err
}

type mEntry struct {
	fi        *fs.FileStat
	name      string
	isSymlink bool
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func (j *juiceFS) readDirSorted(dirname string) ([]*mEntry, syscall.Errno) {
	f, err := j.jfs.Open(ctx, dirname, 0)
	if err != 0 {
		return nil, err
	}
	defer f.Close(ctx)
	entries, err := f.ReaddirPlus(ctx, 0)
	if err != 0 {
		return nil, err
	}
	mEntries := make([]*mEntry, len(entries))
	for i, e := range entries {
		fi := fs.AttrToFileInfo(e.Inode, e.Attr)
		if fi.IsDir() {
			mEntries[i] = &mEntry{fi, string(e.Name) + dirSuffix, false}
		} else if fi.IsSymlink() {
			// follow symlink
			fi2, err := j.jfs.Stat(ctx, filepath.Join(dirname, string(e.Name)))
			if err != 0 {
				mEntries[i] = &mEntry{fi, string(e.Name), true}
				continue
			}
			name := string(e.Name)
			if fi2.IsDir() {
				name += dirSuffix
			}
			mEntries[i] = &mEntry{fi2, name, true}
		} else {
			mEntries[i] = &mEntry{fi, string(e.Name), false}
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].name < mEntries[j].name })
	return mEntries, err
}

type WalkFunc func(path string, info *fs.FileStat, isSymlink bool, err syscall.Errno) syscall.Errno

func (d *juiceFS) ListAll(prefix, marker string) (<-chan object.Object, error) {
	listed := make(chan object.Object, 10240)
	var walkRoot string
	if strings.HasSuffix(prefix, dirSuffix) {
		walkRoot = prefix
	} else {
		// If the root is not ends with `/`, we'll list the directory root resides.
		walkRoot = path.Dir(prefix) + dirSuffix
	}
	if walkRoot == "./" {
		walkRoot = ""
	}
	go func() {
		_ = d.walkRoot(dirSuffix+walkRoot, func(path string, info *fs.FileStat, isSymlink bool, err syscall.Errno) syscall.Errno {
			if len(path) > 0 {
				path = path[1:]
			}
			if err != 0 {
				if err == syscall.ENOENT {
					logger.Warnf("skip not exist file or directory: %s", path)
					return 0
				}
				listed <- nil
				logger.Errorf("list %s: %s", path, err)
				return 0
			}

			if !strings.HasPrefix(path, prefix) {
				if info.IsDir() && path != walkRoot {
					return skipDir
				}
				return 0
			}

			key := path
			if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
				if info.IsDir() && !strings.HasPrefix(prefix, key) && !strings.HasPrefix(marker, key) {
					return skipDir
				}
				return 0
			}
			f := &jObj{key, info}
			listed <- f
			return 0
		})
		close(listed)
	}()
	return listed, nil
}

func (j *juiceFS) Chtimes(key string, mtime time.Time) error {
	f, err := j.jfs.Open(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Utime(ctx, -1, mtime.UnixNano()/1e6))
}

func (j *juiceFS) Chmod(key string, mode os.FileMode) error {
	f, err := j.jfs.Open(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Chmod(ctx, uint16(mode.Perm())))
}

func (j *juiceFS) Chown(key string, owner, group string) error {
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	f, err := j.jfs.Open(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Chown(ctx, uint32(uid), uint32(gid)))
}

func (d *juiceFS) Symlink(oldName, newName string) error {
	p := d.path(newName)
	err := d.jfs.Symlink(ctx, oldName, p)
	if err == syscall.ENOENT {
		_ = d.jfs.MkdirAll(ctx, filepath.Dir(p), 0755)
		err = d.jfs.Symlink(ctx, oldName, p)
	}
	return toError(err)
}

func (j *juiceFS) Readlink(name string) (string, error) {
	target, err := j.jfs.Readlink(ctx, j.path(name))
	return string(target), toError(err)
}

func getDefaultChunkConf(format *meta.Format) *chunk.Config {
	chunkConf := &chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		HashPrefix: format.HashPrefix,
		GetTimeout: time.Minute,
		PutTimeout: time.Minute,
		MaxUpload:  50,
		MaxRetries: 10,
		BufferSize: 300 << 20,
	}
	chunkConf.SelfCheck(format.UUID)
	return chunkConf
}

func newJFS(endpoint, accessKey, secretKey, token string) (object.ObjectStorage, error) {
	metaUrl := os.Getenv(endpoint)
	if metaUrl == "" {
		metaUrl = endpoint
	}
	metaConf := meta.DefaultConf()
	metaConf.MaxDeletes = 10
	metaConf.NoBGJob = true
	metaCli := meta.NewClient(metaUrl, metaConf)
	format, err := metaCli.Load(true)
	if err != nil {
		return nil, fmt.Errorf("load setting: %s", err)
	}
	blob, err := NewReloadableStorage(format, metaCli, nil)
	if err != nil {
		return nil, fmt.Errorf("object storage: %s", err)
	}
	chunkConf := getDefaultChunkConf(format)
	store := chunk.NewCachedStore(blob, *chunkConf, nil)
	registerMetaMsg(metaCli, store, chunkConf)
	err = metaCli.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %s", err)
	}

	vfsConf := &vfs.Config{
		Meta:            metaConf,
		Format:          *format,
		Version:         version.Version(),
		Chunk:           chunkConf,
		AttrTimeout:     time.Second,
		DirEntryTimeout: time.Second,
	}

	vfsConf.Format.RemoveSecret()
	d, _ := json.MarshalIndent(vfsConf, "  ", "")
	logger.Debugf("Config: %s", string(d))

	jfs, err := fs.NewFileSystem(vfsConf, metaCli, store)
	if err != nil {
		return nil, fmt.Errorf("Initialize: %s", err)
	}
	return &juiceFS{object.DefaultObjectStorage{}, format.Name, jfs}, nil
}

func init() {
	object.Register("jfs", newJFS)
}
