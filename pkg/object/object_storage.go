/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"context"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"io"
	"os"
	"path"
	"plugin"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

var ctx = context.Background()
var logger = utils.GetLogger("juicefs")

var UserAgent = "JuiceFS"

type MtimeChanger interface {
	Chtimes(path string, mtime time.Time) error
}

type SupportSymlink interface {
	// Symlink create a symbolic link
	Symlink(oldName, newName string) error
	// Readlink read a symbolic link
	Readlink(name string) (string, error)
}

type FileItf interface {
	Object
	Owner() string
	Group() string
	Mode() os.FileMode
}

type onlyWriter struct {
	io.Writer
}

type File struct {
	Obj
	Owner_     string
	Group_     string
	Mode_      os.FileMode
	IsSymlink_ bool
}

func (f *File) Owner() string     { return f.Owner_ }
func (f *File) Group() string     { return f.Group_ }
func (f *File) Mode() os.FileMode { return f.Mode_ }
func (f *File) IsSymlink() bool   { return f.IsSymlink_ }

func MarshalObject(o Object) map[string]interface{} {
	m := make(map[string]interface{})
	m["key"] = o.Key()
	m["size"] = o.Size()
	m["mtime"] = o.Mtime().UnixNano()
	m["isdir"] = o.IsDir()
	if f, ok := o.(FileItf); ok {
		m["mode"] = f.Mode()
		m["owner"] = f.Owner()
		m["group"] = f.Group()
		m["isSymlink"] = f.IsSymlink()
	}
	return m
}

func UnmarshalObject(m map[string]interface{}) Object {
	mtime := time.Unix(0, int64(m["mtime"].(float64)))
	o := Obj{
		Key_:   m["key"].(string),
		Size_:  int64(m["size"].(float64)),
		Mtime_: mtime,
		IsDir_: m["isdir"].(bool)}
	if _, ok := m["mode"]; ok {
		f := File{o, m["owner"].(string), m["group"].(string), os.FileMode(m["mode"].(float64)), m["isSymlink"].(bool)}
		return &f
	}
	return &o
}

type FileSystem interface {
	MtimeChanger
	Chmod(path string, mode os.FileMode) error
	Chown(path string, owner, group string) error
}

var NotSupported = utils.ENOTSUP

type DefaultObjectStorage struct{}

func (s DefaultObjectStorage) Create() error {
	return nil
}

func (s DefaultObjectStorage) Limits() Limits {
	return Limits{IsSupportMultipartUpload: false, IsSupportUploadPartCopy: false}
}

func (s DefaultObjectStorage) Head(key string) (Object, error) {
	return nil, NotSupported
}

func (s DefaultObjectStorage) Copy(dst, src string) error {
	return NotSupported
}

func (s DefaultObjectStorage) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	return nil, NotSupported
}

func (s DefaultObjectStorage) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	return nil, NotSupported
}

func (s DefaultObjectStorage) UploadPartCopy(key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, NotSupported
}

func (s DefaultObjectStorage) AbortUpload(key string, uploadID string) {}

func (s DefaultObjectStorage) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return NotSupported
}

func (s DefaultObjectStorage) ListUploads(marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func (s DefaultObjectStorage) List(prefix, marker, delimiter string, limit int64, followLink bool) ([]Object, error) {
	return nil, NotSupported
}

func (s DefaultObjectStorage) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, NotSupported
}

type Creator func(bucket, accessKey, secretKey, token string) (ObjectStorage, error)

var storages = make(map[string]Creator)

func Register(name string, register Creator) {
	storages[name] = register
}

func CreateStorage(name, endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	f, ok := storages[name]
	if ok {
		logger.Debugf("Creating %s storage at endpoint %s", name, endpoint)
		return f(endpoint, accessKey, secretKey, token)
	} else {
		loadDir := os.Getenv("JUICEFS_PLUGIN_LOAD_PATH")
		if loadDir == "" {
			return nil, fmt.Errorf("invalid storage: %s", name)
		}
		soPath := path.Join(loadDir, fmt.Sprintf("%s_plugin.so", name))
		logger.Infof("Loading plugin %s", soPath)
		p, err := plugin.Open(soPath)
		if err != nil {
			logger.Errorf("open plugin %s: %s", soPath, err)
			return nil, err
		}
		newFunc, err := p.Lookup(fmt.Sprintf("New%s", cases.Title(language.English).String(name)))
		if err != nil {
			logger.Errorf("lookup NewStorage in %s: %s", soPath, err)
			return nil, err
		}
		return newFunc.(func(string, string, string, string) (ObjectStorage, error))(endpoint, accessKey, secretKey, token)
	}
}

var BufPool = sync.Pool{
	New: func() interface{} {
		// Default io.Copy uses 32KB buffer, here we choose a larger one (1MiB io-size increases throughput by ~20%)
		buf := make([]byte, 1<<20)
		return &buf
	},
}

type listThread struct {
	sync.Mutex
	cond    *utils.Cond
	ready   bool
	err     error
	entries []Object
}

func ListAllWithDelimiter(store ObjectStorage, prefix, start, end string, followLink bool) (<-chan Object, error) {
	entries, err := store.List(prefix, "", "/", 1e9, followLink)
	if err != nil {
		logger.Errorf("list %s: %s", prefix, err)
		return nil, err
	}

	listed := make(chan Object, 10240)
	var walk func(string, []Object) error
	walk = func(prefix string, entries []Object) error {
		var concurrent = 10
		var err error
		threads := make([]listThread, concurrent)
		for c := 0; c < concurrent; c++ {
			t := &threads[c]
			t.cond = utils.NewCond(t)
			go func(c int) {
				for i := c; i < len(entries); i += concurrent {
					key := entries[i].Key()
					if end != "" && key >= end {
						break
					}
					if key < start && !strings.HasPrefix(start, key) {
						continue
					}
					if !entries[i].IsDir() || key == prefix {
						continue
					}

					t.entries, t.err = store.List(key, "\x00", "/", 1e9, followLink) // exclude itself
					t.Lock()
					t.ready = true
					t.cond.Signal()
					for t.ready {
						t.cond.WaitWithTimeout(time.Second)
						if err != nil {
							t.Unlock()
							return
						}
					}
					t.Unlock()
				}
			}(c)
		}

		for i, e := range entries {
			key := e.Key()
			if end != "" && key >= end {
				return nil
			}
			if key >= start {
				listed <- e
			} else if !strings.HasPrefix(start, key) {
				continue
			}
			if !e.IsDir() || key == prefix {
				continue
			}

			t := &threads[i%concurrent]
			t.Lock()
			for !t.ready {
				t.cond.WaitWithTimeout(time.Millisecond * 10)
			}
			if t.err != nil {
				err = t.err
				t.Unlock()
				return err
			}
			t.ready = false
			t.cond.Signal()
			children := t.entries
			t.Unlock()

			err = walk(key, children)
			if err != nil {
				return err
			}
		}
		return nil
	}

	go func() {
		defer close(listed)
		err := walk(prefix, entries)
		if err != nil {
			listed <- nil
		}
	}()
	return listed, nil
}
