/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package vfs

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

type _file struct {
	ino  Ino
	size uint64
}

type CacheAction uint8

func (act CacheAction) String() string {
	switch act {
	case WarmupCache:
		return "warmup cache"
	case EvictCache:
		return "evict cache"
	case CheckCache:
		return "check cache"
	}
	return "unknown operation"
}

const (
	WarmupCache CacheAction = iota
	EvictCache
	CheckCache = 2
)

func (v *VFS) cache(ctx meta.Context, action CacheAction, paths []string, concurrent int, resp *CacheResponse) {
	logger.Infof("start to %s %d paths with %d workers", action, len(paths), concurrent)

	if resp == nil {
		resp = &CacheResponse{}
	}
	start := time.Now()
	todo := make(chan _file, 10*concurrent)
	wg := sync.WaitGroup{}
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range todo {
				if ctx.Canceled() {
					return
				}

				if f.ino == 0 {
					logger.Warnf("%s got inode 0", action)
					continue
				}

				iter := newSliceIterator(ctx, v.Meta, f.ino, f.size, resp)
				var handler sliceHandler
				switch action {
				case WarmupCache:
					handler = func(s meta.Slice) error {
						return v.Store.FillCache(s.Id, s.Size)
					}

					if v.Conf.Meta.OpenCache > 0 {
						if err := v.Meta.Open(ctx, f.ino, syscall.O_RDONLY, &meta.Attr{}); err != 0 {
							logger.Errorf("Inode %d could be opened: %s", f.ino, err)
						}
						_ = v.Meta.Close(ctx, f.ino)
					}
				case EvictCache:
					handler = func(s meta.Slice) error {
						return v.Store.EvictCache(s.Id, s.Size)
					}
				case CheckCache:
					handler = func(s meta.Slice) error {
						missBytes, err := v.Store.CheckCache(s.Id, s.Size)
						if err != nil {
							return err
						}
						atomic.AddUint64(&resp.MissBytes, missBytes)
						return nil
					}
				}

				// log and skip error
				err := iter.Iterate(handler)
				if err != nil {
					logger.Errorf("%s error : %s", action, err)
				}

				atomic.AddUint64(&resp.FileCount, 1)
			}
		}()
	}

	var inode Ino
	var attr = &Attr{}
	for _, p := range paths {
		if st := v.resolve(ctx, p, &inode, attr); st != 0 {
			logger.Warnf("Failed to resolve path %s: %s", p, st)
			continue
		}
		logger.Debugf("Warming up path %s", p)
		if attr.Typ == meta.TypeDirectory {
			v.walkDir(ctx, inode, todo)
		} else if attr.Typ == meta.TypeFile {
			_ = sendFile(ctx, todo, _file{inode, attr.Length})
		}
		if ctx.Canceled() {
			break
		}
	}
	close(todo)
	wg.Wait()

	if ctx.Canceled() {
		logger.Infof("%s cancelled", action)
	}
	logger.Infof("%s %d paths in %s", action, len(paths), time.Since(start))
}

func sendFile(ctx meta.Context, todo chan _file, f _file) error {
	select {
	case todo <- f:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (v *VFS) resolve(ctx meta.Context, p string, inode *Ino, attr *Attr) syscall.Errno {
	var inodePrefix = "inode:"
	if strings.HasPrefix(p, inodePrefix) {
		i, err := strconv.ParseUint(p[len(inodePrefix):], 10, 64)
		if err == nil {
			*inode = meta.Ino(i)
			return v.Meta.GetAttr(ctx, meta.Ino(i), attr)
		}
	}
	p = strings.Trim(p, "/")
	err := v.Meta.Resolve(ctx, 1, p, inode, attr)
	if err != syscall.ENOTSUP {
		return err
	}

	// Fallback to the default implementation that calls `m.Lookup` for each directory along the path.
	// It might be slower for deep directories, but it works for every meta that implements `Lookup`.
	parent := Ino(1)
	ss := strings.Split(p, "/")
	for i, name := range ss {
		if len(name) == 0 {
			continue
		}
		if parent == meta.RootInode && i == len(ss)-1 && IsSpecialName(name) {
			*inode, attr = GetInternalNodeByName(name)
			parent = *inode
			break
		}
		if i > 0 {
			if err = v.Meta.Access(ctx, parent, MODE_MASK_R|MODE_MASK_X, attr); err != 0 {
				return err
			}
		}
		if err = v.Meta.Lookup(ctx, parent, name, inode, attr, false); err != 0 {
			return err
		}
		if attr.Typ == meta.TypeSymlink {
			var buf []byte
			if err = v.Meta.ReadLink(ctx, *inode, &buf); err != 0 {
				return err
			}
			target := string(buf)
			if strings.HasPrefix(target, "/") || strings.Contains(target, "://") {
				return syscall.ENOTSUP
			}
			target = path.Join(strings.Join(ss[:i], "/"), target)
			if err = v.resolve(ctx, target, inode, attr); err != 0 {
				return err
			}
		}
		parent = *inode
	}
	if parent == meta.RootInode {
		*inode = parent
		if err = v.Meta.GetAttr(ctx, *inode, attr); err != 0 {
			return err
		}
	}
	return 0
}

func (v *VFS) walkDir(ctx meta.Context, inode Ino, todo chan _file) {
	pending := make([]Ino, 1)
	pending[0] = inode
	for len(pending) > 0 {
		l := len(pending)
		l--
		inode = pending[l]
		pending = pending[:l]
		var entries []*meta.Entry
		r := v.Meta.Readdir(ctx, inode, 1, &entries)
		if r == 0 {
			for _, f := range entries {
				name := string(f.Name)
				if name == "." || name == ".." {
					continue
				}
				if f.Attr.Typ == meta.TypeDirectory {
					pending = append(pending, f.Inode)
				} else if f.Attr.Typ != meta.TypeSymlink {
					_ = sendFile(ctx, todo, _file{f.Inode, f.Attr.Length})
				}
				if ctx.Canceled() {
					return
				}
			}
		} else {
			logger.Warnf("readdir %d: %s", inode, r)
		}
	}
}

type sliceIterator struct {
	ctx      meta.Context
	mClient  meta.Meta
	ino      Ino
	chunkCnt uint32
	stat     *CacheResponse

	err            error
	nextChunkIndex uint32
	nextSliceIndex uint64
	slices         []meta.Slice
}

type sliceHandler func(s meta.Slice) error

func (iter *sliceIterator) hasNext() bool {
	if iter.ctx.Canceled() {
		iter.err = iter.ctx.Err()
		return false
	}

	for iter.nextSliceIndex >= uint64(len(iter.slices)) {
		if iter.nextChunkIndex >= iter.chunkCnt {
			return false
		}

		iter.slices = nil
		iter.nextSliceIndex = 0
		if st := iter.mClient.Read(iter.ctx, iter.ino, iter.nextChunkIndex, &iter.slices); st != 0 {
			iter.err = fmt.Errorf("get slices of inode %d index %d error: %d", iter.ino, iter.nextChunkIndex, st)
			return false
		}
		iter.nextChunkIndex++
	}

	return true
}

func (iter *sliceIterator) next() meta.Slice {
	s := iter.slices[iter.nextSliceIndex]
	iter.nextSliceIndex++
	return s
}

func (iter *sliceIterator) Iterate(handler sliceHandler) error {
	if handler == nil {
		return fmt.Errorf("handler not set")
	}
	for iter.hasNext() {
		s := iter.next()
		atomic.AddUint64(&iter.stat.SliceCount, 1)
		atomic.AddUint64(&iter.stat.TotalBytes, uint64(s.Size))
		if err := handler(s); err != nil {
			return fmt.Errorf("inode %d slice %d : %w", iter.ino, s.Id, err)
		}
	}
	return iter.err
}

func newSliceIterator(ctx meta.Context, mClient meta.Meta, ino Ino, size uint64, stat *CacheResponse) *sliceIterator {
	return &sliceIterator{
		ctx:     ctx,
		mClient: mClient,
		ino:     ino,
		stat:    stat,

		nextSliceIndex: 0,
		nextChunkIndex: 0,
		chunkCnt:       uint32((size + meta.ChunkSize - 1) / meta.ChunkSize),
	}
}
