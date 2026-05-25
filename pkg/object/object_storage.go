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
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
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

type File interface {
	Object
	Owner() string
	Group() string
	Mode() os.FileMode
}

type onlyWriter struct {
	io.Writer
}

type file struct {
	obj
	owner     string
	group     string
	mode      os.FileMode
	isSymlink bool
}

func (f *file) Owner() string     { return f.owner }
func (f *file) Group() string     { return f.group }
func (f *file) Mode() os.FileMode { return f.mode }
func (f *file) IsSymlink() bool   { return f.isSymlink }

func MarshalObject(o Object) map[string]interface{} {
	m := make(map[string]interface{})
	m["key"] = o.Key()
	m["size"] = o.Size()
	m["mtime"] = strconv.FormatInt(o.Mtime().UnixNano(), 10)
	m["isdir"] = o.IsDir()
	if f, ok := o.(File); ok {
		m["mode"] = f.Mode()
		m["owner"] = f.Owner()
		m["group"] = f.Group()
		m["isSymlink"] = f.IsSymlink()
	}
	return m
}

func UnmarshalObject(m map[string]interface{}) Object {
	mtime_int64, _ := strconv.ParseInt(m["mtime"].(string), 10, 64)
	mtime := time.Unix(0, mtime_int64)
	o := obj{
		key:   m["key"].(string),
		size:  int64(m["size"].(float64)),
		mtime: mtime,
		isDir: m["isdir"].(bool)}
	if _, ok := m["mode"]; ok {
		f := file{o, m["owner"].(string), m["group"].(string), os.FileMode(m["mode"].(float64)), m["isSymlink"].(bool)}
		return &f
	}
	return &o
}

type FileSystem interface {
	MtimeChanger
	Chmod(path string, mode os.FileMode) error
	Chown(path string, owner, group string) error
}

var notSupported = utils.ErrNotSUP

type DefaultObjectStorage struct{}

func (s DefaultObjectStorage) Create(ctx context.Context) error {
	return nil
}

func (s DefaultObjectStorage) Limits() Limits {
	return Limits{IsSupportMultipartUpload: false, IsSupportUploadPartCopy: false}
}

func (s DefaultObjectStorage) Head(key string) (Object, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) Copy(ctx context.Context, dst, src string) error {
	return notSupported
}

func (s DefaultObjectStorage) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) AbortUpload(ctx context.Context, key string, uploadID string) {}

func (s DefaultObjectStorage) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
	return notSupported
}

func (s DefaultObjectStorage) ListUploads(ctx context.Context, marker string) ([]*PendingPart, string, error) {
	return nil, "", nil
}

func (s DefaultObjectStorage) List(ctx context.Context, prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	return nil, false, "", notSupported
}

func (s DefaultObjectStorage) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s DefaultObjectStorage) Restore(ctx context.Context, key string, days int32) error {
	return notSupported
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
	}
	return nil, fmt.Errorf("invalid storage: %s", name)
}

var bufPool = sync.Pool{
	New: func() interface{} {
		// Default io.Copy uses 32KB buffer, here we choose a larger one (1MiB io-size increases throughput by ~20%)
		buf := make([]byte, 1<<20)
		return &buf
	},
}

func ListAllWithDelimiter(ctx context.Context, store ObjectStorage, prefix, start, end string, followLink bool) (<-chan Object, error) {
	marker := start
	if start != "" && strings.HasPrefix(start, prefix) {
		remaining := start[len(prefix):]
		if idx := strings.Index(remaining, "/"); idx >= 0 {
			marker = prefix + remaining[:idx]
		}
	}
	entries, _, _, err := store.List(ctx, prefix, marker, "", "/", 1e9, followLink)
	if err != nil {
		logger.Errorf("list %s: %s", prefix, err)
		return nil, err
	}

	listed := make(chan Object, 10240)

	// prefetch is one worker's pre-fetched child-listing for a single entry.
	type prefetch struct {
		entries   []Object
		hasMore   bool
		nextToken string
		err       error
	}

	var walk func(ctx context.Context, prefix string, entries []Object) error
	walk = func(ctx context.Context, prefix string, entries []Object) error {
		if len(entries) == 0 {
			return nil
		}
		concurrent := 10
		if concurrent > len(entries) {
			concurrent = len(entries)
		}

		// One capacity-1 channel per worker preserves the producer's
		// in-order consumption (it reads produce[i%concurrent] for entry i)
		// while letting each worker stay one step ahead.
		produce := make([]chan prefetch, concurrent)
		for c := range produce {
			produce[c] = make(chan prefetch, 1)
		}

		// walkCtx is cancelled when the producer exits — either on error,
		// because an entry is past `end`, or because every entry has been
		// consumed. The explicit cancel before g.Wait below unblocks any
		// workers that are otherwise stuck trying to deliver into produce[c].
		walkCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		g, gctx := errgroup.WithContext(walkCtx)

		for c := 0; c < concurrent; c++ {
			c := c
			g.Go(func() error {
				defer close(produce[c])
				for i := c; i < len(entries); i += concurrent {
					key := entries[i].Key()
					if end != "" && key >= end {
						return nil
					}
					if key < start && !strings.HasPrefix(start, key) {
						continue
					}
					if !entries[i].IsDir() || key == prefix {
						continue
					}
					var p prefetch
					p.entries, p.hasMore, p.nextToken, p.err = store.List(gctx, key, "\x00", "", "/", 1000, followLink)
					select {
					case produce[c] <- p:
					case <-gctx.Done():
						return gctx.Err()
					}
				}
				return nil
			})
		}

		var walkErr error
	loop:
		for i, e := range entries {
			key := e.Key()
			if end != "" && key >= end {
				break
			}
			if key >= start {
				select {
				case listed <- e:
				case <-gctx.Done():
					walkErr = gctx.Err()
					break loop
				}
			} else if !strings.HasPrefix(start, key) {
				continue
			}
			if !e.IsDir() || key == prefix {
				continue
			}

			var p prefetch
			select {
			case got, ok := <-produce[i%concurrent]:
				if !ok {
					walkErr = errors.New("list prefetcher closed before delivering result")
					break loop
				}
				p = got
			case <-gctx.Done():
				walkErr = gctx.Err()
				break loop
			}
			if p.err != nil {
				walkErr = p.err
				break loop
			}
			children := p.entries
			for p.hasMore {
				if len(children) == 0 {
					walkErr = fmt.Errorf("List(%s) returned hasMore=true with no entries", key)
					break loop
				}
				var more []Object
				startAfter := children[len(children)-1].Key()
				more, p.hasMore, p.nextToken, p.err = store.List(gctx, key, startAfter, p.nextToken, "/", 1e9, followLink)
				if p.err != nil {
					walkErr = p.err
					break loop
				}
				children = append(children, more...)
			}
			if cerr := walk(gctx, key, children); cerr != nil {
				walkErr = cerr
				break loop
			}
		}

		// Cancel before Wait so any worker blocked on produce[c] <- p exits
		// promptly instead of waiting for the parent ctx.
		cancel()
		if werr := g.Wait(); werr != nil && walkErr == nil && !errors.Is(werr, context.Canceled) {
			walkErr = werr
		}
		return walkErr
	}

	go func() {
		defer close(listed)
		if err := walk(ctx, prefix, entries); err != nil {
			listed <- nil
		}
	}()
	return listed, nil
}

func generateListResult(objs []Object, limit int64) ([]Object, bool, string, error) {
	var nextMarker string
	if len(objs) > 0 {
		nextMarker = objs[len(objs)-1].Key()
	}
	return objs, len(objs) == int(limit), nextMarker, nil
}

func decodeKey(value string, typ *string) (string, error) {
	if typ != nil && *typ == "url" {
		return url.QueryUnescape(value)
	}
	return value, nil
}

func TmpFilePath(parent, name string) string {
	return filepath.Join(filepath.Dir(parent), ".jfs."+name+".tmp."+strconv.Itoa(rand.Int()))
}

type TierKey struct{}

const DefaultRestoreDays = 3

type SupportTier interface {
	SetTier(init Tiers) error
	GetStorageClass(ctx context.Context) string
}

type tierStorage struct {
	tiers map[uint8]Tier
}

func (b *tierStorage) GetStorageClass(ctx context.Context) string {
	sc := b.tiers[0].Sc
	if id, ok := ctx.Value(TierKey{}).(uint8); ok {
		if t, ok := b.tiers[id]; ok {
			sc = t.Sc
		} else {
			logger.Warnf("invalid tier id: %d", id)
		}
	}
	return sc
}

func (b *tierStorage) SetTier(init Tiers) error {
	if init == nil {
		init = NewTiers("")
	}
	b.tiers = init
	return nil
}

type Tier struct {
	ID uint8  `json:"ID"`
	Sc string `json:"StorageClass"`
}
type Tiers map[uint8]Tier

func (t Tiers) GetID(sc string) (uint8, bool) {
	for k, v := range t {
		if v.Sc == sc {
			return k, true
		}
	}
	return 0, false
}

func (t Tiers) GetSc(id uint8) (string, bool) {
	tInfo, ok := t[id]
	return tInfo.Sc, ok
}

func NewTiers(defaultSc string) Tiers {
	t := make(Tiers)
	t[0] = Tier{
		ID: 0,
		Sc: defaultSc,
	}
	return t
}

func getOrDefaultScValue(v, defaultValue string) string {
	if v == "" {
		return defaultValue
	}
	return v
}
