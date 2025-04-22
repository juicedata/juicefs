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

	"github.com/juicedata/juicefs/pkg/chunk"

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

type CacheFiller struct {
	conf  *Config
	meta  meta.Meta
	store chunk.ChunkStore
}

func NewCacheFiller(conf *Config, meta meta.Meta, store chunk.ChunkStore) *CacheFiller {
	return &CacheFiller{
		conf:  conf,
		meta:  meta,
		store: store,
	}
}

type token struct{}

func (c *CacheFiller) cacheFile(ctx meta.Context, action CacheAction, resp *CacheResponse, concurrent chan token, wg *sync.WaitGroup, f _file) {
	concurrent <- token{}
	wg.Add(1)
	go func() {
		defer func() {
			<-concurrent
			wg.Done()
		}()

		if f.ino == 0 {
			logger.Warnf("%s got inode 0", action)
			return
		}

		var handler sliceHandler
		switch action {
		case WarmupCache:
			handler = func(s meta.Slice) error {
				return c.store.FillCache(s.Id, s.Size)
			}

			if c.conf.Meta.OpenCache > 0 {
				if err := c.meta.Open(ctx, f.ino, syscall.O_RDONLY, &meta.Attr{}); err != 0 {
					logger.Errorf("Inode %d could be opened: %s", f.ino, err)
				}
				_ = c.meta.Close(ctx, f.ino)
			}
		case EvictCache:
			handler = func(s meta.Slice) error {
				return c.store.EvictCache(s.Id, s.Size)
			}
		case CheckCache:
			blockHandler := func(exists bool, loc string, size int) {
				if exists {
					resp.Lock()
					resp.Locations[loc] += uint64(size)
					resp.Unlock()
				} else {
					atomic.AddUint64(&resp.MissBytes, uint64(size))
				}
			}
			handler = func(s meta.Slice) error {
				return c.store.CheckCache(s.Id, s.Size, blockHandler)
			}
		}

		iter := newSliceIterator(ctx, c.meta, f.ino, f.size, resp)
		err := iter.Iterate(handler, concurrent)
		if err != nil {
			logger.Errorf("%s error : %s", action, err)
		}

		atomic.AddUint64(&resp.FileCount, 1)
	}()
}

func (c *CacheFiller) Cache(ctx meta.Context, action CacheAction, paths []string, threads int, resp *CacheResponse) {
	if resp == nil {
		resp = &CacheResponse{Locations: make(map[string]uint64)}
	}
	start := time.Now()
	todo := make(chan _file, 20*threads)

	concurrent := make(chan token, threads)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for f := range todo {
			if ctx.Canceled() {
				return
			}
			c.cacheFile(ctx, action, resp, concurrent, &wg, f)
		}
	}()

	var inode Ino
	var attr = &Attr{}
	for _, p := range paths {
		if st := c.resolve(ctx, p, &inode, attr); st != 0 {
			logger.Warnf("Failed to resolve path %s: %s", p, st)
			continue
		}
		logger.Debugf("path %s", p)
		if attr.Typ == meta.TypeDirectory {
			c.walkDir(ctx, inode, todo)
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

func (c *CacheFiller) resolve(ctx meta.Context, p string, inode *Ino, attr *Attr) syscall.Errno {
	var inodePrefix = "inode:"
	if strings.HasPrefix(p, inodePrefix) {
		i, err := strconv.ParseUint(p[len(inodePrefix):], 10, 64)
		if err == nil {
			*inode = meta.Ino(i)
			return c.meta.GetAttr(ctx, meta.Ino(i), attr)
		}
	}
	p = strings.Trim(p, "/")
	err := c.meta.Resolve(ctx, 1, p, inode, attr)
	if err != syscall.ENOTSUP {
		return err
	}

	// Fallback to the default implementation that calls `meta.Lookup` for each directory along the path.
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
			if err = c.meta.Access(ctx, parent, MODE_MASK_R|MODE_MASK_X, attr); err != 0 {
				return err
			}
		}
		if err = c.meta.Lookup(ctx, parent, name, inode, attr, false); err != 0 {
			return err
		}
		if attr.Typ == meta.TypeSymlink {
			var buf []byte
			if err = c.meta.ReadLink(ctx, *inode, &buf); err != 0 {
				return err
			}
			target := string(buf)
			if strings.HasPrefix(target, "/") || strings.Contains(target, "://") {
				return syscall.ENOTSUP
			}
			target = path.Join(strings.Join(ss[:i], "/"), target)
			if err = c.resolve(ctx, target, inode, attr); err != 0 {
				return err
			}
		}
		parent = *inode
	}
	if parent == meta.RootInode {
		*inode = parent
		if err = c.meta.GetAttr(ctx, *inode, attr); err != 0 {
			return err
		}
	}
	return 0
}

func (c *CacheFiller) walkDir(ctx meta.Context, inode Ino, todo chan _file) {
	pending := make([]Ino, 1)
	pending[0] = inode
	for len(pending) > 0 {
		l := len(pending)
		l--
		inode = pending[l]
		pending = pending[:l]
		var entries []*meta.Entry
		r := c.meta.Readdir(ctx, inode, 1, &entries)
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
	if iter.err != nil {
		logger.Error(iter.err)
		iter.err = nil
	}

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
			logger.Error(iter.err)
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

func (iter *sliceIterator) Iterate(handler sliceHandler, concurrent chan token) error {
	if handler == nil {
		return fmt.Errorf("handler not set")
	}
	var wg sync.WaitGroup
	for iter.hasNext() {
		s := iter.next()
		if s.Id == 0 {
			continue
		}
		atomic.AddUint64(&iter.stat.SliceCount, 1)
		atomic.AddUint64(&iter.stat.TotalBytes, uint64(s.Size))

		select {
		case concurrent <- token{}:
			wg.Add(1)
			go func() {
				defer func() {
					<-concurrent
					wg.Done()
				}()
				if err := handler(s); err != nil {
					iter.err = fmt.Errorf("inode %d slice %d : %w", iter.ino, s.Id, err)
				}
			}()
		default:
			if err := handler(s); err != nil {
				iter.err = fmt.Errorf("inode %d slice %d : %w", iter.ino, s.Id, err)
			}
		}
	}
	wg.Wait()
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
