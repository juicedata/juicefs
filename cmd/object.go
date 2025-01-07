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
	"sort"
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

var dirSuffix = "/"

func toError(eno syscall.Errno) error {
	if eno == 0 {
		return nil
	}
	return eno
}

type juiceFS struct {
	object.DefaultObjectStorage
	name  string
	umask uint16
	jfs   *fs.FileSystem
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

func (j *juiceFS) Get(key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	f, err := j.jfs.Open(ctx, j.path(key), vfs.MODE_MASK_R)
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

func (j *juiceFS) Put(key string, in io.Reader, getters ...object.AttrGetter) (err error) {
	if vfs.IsSpecialName(key) {
		return fmt.Errorf("skip special file %s for jfs: %w", key, utils.ErrSkipped)
	}
	p := j.path(key)
	if strings.HasSuffix(p, "/") {
		eno := j.jfs.MkdirAll(ctx, p, 0777, j.umask)
		return toError(eno)
	}
	var tmp string
	if object.PutInplace {
		tmp = p
	} else {
		name := path.Base(p)
		if len(name) > 200 {
			name = name[:200]
		}
		tmp = path.Join(path.Dir(p), fmt.Sprintf(".%s.tmp.%d", name, rand.Int()))
		defer func() {
			if err != nil {
				if e := j.jfs.Delete(ctx, tmp); e != 0 {
					logger.Warnf("Failed to delete %s: %s", tmp, e)
				}
			}
		}()
	}
	f, eno := j.jfs.Open(ctx, tmp, vfs.MODE_MASK_W)
	if eno == syscall.ENOENT {
		_ = j.jfs.MkdirAll(ctx, path.Dir(tmp), 0777, j.umask)
		f, eno = j.jfs.Create(ctx, tmp, 0666, j.umask)
	}

	if eno == syscall.EEXIST {
		_ = j.jfs.Delete(ctx, path.Dir(tmp))
		f, eno = j.jfs.Create(ctx, tmp, 0666, j.umask)
	}

	if eno != 0 {
		return toError(eno)
	}
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(&jFile{f, 0}, in, *buf)
	if err != nil {
		return
	}
	eno = f.Close(ctx)
	if eno != 0 {
		return toError(eno)
	}
	if !object.PutInplace {
		if eno = j.jfs.Rename(ctx, tmp, p, 0); eno != 0 {
			return toError(eno)
		}
	}
	return nil
}

func (j *juiceFS) Delete(key string, getters ...object.AttrGetter) error {
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
func (o *jObj) Mtime() time.Time     { return o.fi.ModTime() }
func (o *jObj) IsDir() bool          { return o.fi.IsDir() }
func (o *jObj) IsSymlink() bool      { return o.fi.IsSymlink() }
func (o *jObj) Owner() string        { return utils.UserName(o.fi.Uid()) }
func (o *jObj) Group() string        { return utils.GroupName(o.fi.Gid()) }
func (o *jObj) Mode() os.FileMode    { return o.fi.Mode() }
func (o *jObj) StorageClass() string { return "" }

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

func (j *juiceFS) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]object.Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", utils.ENOTSUP
	}
	dir := j.path(prefix)
	var objs []object.Object
	if !strings.HasSuffix(dir, dirSuffix) {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := j.Head(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, "", nil
			}
			return nil, false, "", err
		}
		objs = append(objs, obj)
	}
	entries, err := j.readDirSorted(dir, followLink)
	if err != 0 {
		if err == syscall.ENOENT {
			return nil, false, "", nil
		}
		return nil, false, "", err
	}
	for _, e := range entries {
		key := dir[1:] + e.name
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}
		f := &jObj{key, e.fi}
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}
	var nextMarker string
	if len(objs) > 0 {
		nextMarker = objs[len(objs)-1].Key()
	}
	return objs, len(objs) == int(limit), nextMarker, nil
}

type mEntry struct {
	fi        *fs.FileStat
	name      string
	isSymlink bool
}

// readDirSorted reads the directory named by dirname and returns
// a sorted list of directory entries.
func (j *juiceFS) readDirSorted(dirname string, followLink bool) ([]*mEntry, syscall.Errno) {
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
		} else if fi.IsSymlink() && followLink {
			fi2, err := j.jfs.Stat(ctx, path.Join(dirname, string(e.Name)))
			if err != 0 {
				mEntries[i] = &mEntry{fi, string(e.Name), true}
				continue
			}
			name := string(e.Name)
			if fi2.IsDir() {
				name += dirSuffix
			}
			mEntries[i] = &mEntry{fi2, name, false}
		} else {
			mEntries[i] = &mEntry{fi, string(e.Name), fi.IsSymlink()}
		}
	}
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].name < mEntries[j].name })
	return mEntries, err
}

func (j *juiceFS) Chtimes(key string, mtime time.Time) error {
	f, err := j.jfs.Lopen(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Utime(ctx, -1, mtime.UnixNano()/1e6))
}

// syscallMode returns the syscall-specific mode bits from Go's portable mode bits.
func syscallMode(i os.FileMode) (o uint32) {
	o |= uint32(i.Perm())
	if i&os.ModeSetuid != 0 {
		o |= syscall.S_ISUID
	}
	if i&os.ModeSetgid != 0 {
		o |= syscall.S_ISGID
	}
	if i&os.ModeSticky != 0 {
		o |= syscall.S_ISVTX
	}
	// No mapping for Go's ModeTemporary (plan9 only).
	return
}

func (j *juiceFS) Chmod(key string, mode os.FileMode) error {
	f, err := j.jfs.Open(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Chmod(ctx, uint16(syscallMode(mode))))
}

func (j *juiceFS) Chown(key string, owner, group string) error {
	uid := utils.LookupUser(owner)
	gid := utils.LookupGroup(group)
	f, err := j.jfs.Lopen(ctx, j.path(key), 0)
	if err != 0 {
		return err
	}
	defer f.Close(ctx)
	return toError(f.Chown(ctx, uint32(uid), uint32(gid)))
}

func (j *juiceFS) Symlink(oldName, newName string) error {
	p := j.path(newName)
	err := j.jfs.Symlink(ctx, oldName, p)
	if err == syscall.ENOENT {
		_ = j.jfs.MkdirAll(ctx, path.Dir(p), 0777, j.umask)
		err = j.jfs.Symlink(ctx, oldName, p)
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

func (j *juiceFS) Shutdown() {
	_ = j.jfs.Meta().CloseSession()
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
	err = metaCli.NewSession(false)
	if err != nil {
		return nil, fmt.Errorf("new session: %s", err)
	}
	metaCli.OnReload(func(fmt *meta.Format) {
		store.UpdateLimit(fmt.UploadLimit, fmt.DownloadLimit)
	})

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
	return &juiceFS{object.DefaultObjectStorage{}, format.Name, uint16(utils.GetUmask()), jfs}, nil
}

func init() {
	object.Register("jfs", newJFS)
}
