/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"os"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/utils/extsort"
)

type unorderedListKey struct{}

// WithUnorderedList returns a context telling ListAll and List that the caller
// doesn't need the keys in order, so storages that can't list in order may skip
// sorting them in memory (e.g. Ceph) or checking their order (e.g. S3).
func WithUnorderedList(ctx context.Context) context.Context {
	return context.WithValue(ctx, unorderedListKey{}, true)
}

func isUnorderedList(ctx context.Context) bool {
	v, _ := ctx.Value(unorderedListKey{}).(bool)
	return v
}

var (
	// max memory ~= externalSortChunkSize * bytes per record * (4 * externalSortThreads + 3),
	// Current: ~0.7-1.9 GiB per sorter at 150-400 bytes/record
	externalSortThreads   = 4
	externalSortChunkSize = 1 << 18
)

var errListingFailed = errors.New("listing failed")

const (
	sortedObjectIsDir = 1 << iota
	sortedObjectIsFile
	sortedObjectIsSymlink
)

const sortedObjectHeaderSize = 33 // flags(1) + size(8) + mtime(12) + mode(4) + lengths(8)

// record: header owner group key
func encodeSortedObject(o Object) ([]byte, error) {
	var flags byte
	if o.IsDir() {
		flags |= sortedObjectIsDir
	}
	var mode os.FileMode
	var owner, group string
	if f, ok := o.(File); ok {
		flags |= sortedObjectIsFile
		if f.IsSymlink() {
			flags |= sortedObjectIsSymlink
		}
		mode, owner, group = f.Mode(), f.Owner(), f.Group()
	}
	wb := utils.NewBuffer(uint32(sortedObjectHeaderSize + len(owner) + len(group) + len(o.Key())))
	wb.Put8(flags)
	wb.Put64(uint64(o.Size()))
	mtime := o.Mtime()
	wb.Put64(uint64(mtime.Unix()))
	wb.Put32(uint32(mtime.Nanosecond()))
	wb.Put32(uint32(mode))
	wb.Put32(uint32(len(owner)))
	wb.Put32(uint32(len(group)))
	wb.Put([]byte(owner))
	wb.Put([]byte(group))
	wb.Put([]byte(o.Key()))
	return wb.Bytes(), nil
}

func decodeSortedObject(b []byte) (Object, error) {
	invalid := fmt.Errorf("invalid sorted object record of %d bytes", len(b))
	if len(b) < sortedObjectHeaderSize {
		return nil, invalid
	}
	rb := utils.FromBuffer(b)
	flags := rb.Get8()
	o := obj{
		size:  int64(rb.Get64()),
		mtime: time.Unix(int64(rb.Get64()), int64(rb.Get32())).UTC(),
		isDir: flags&sortedObjectIsDir != 0,
	}
	mode := os.FileMode(rb.Get32())
	ownerLen, groupLen := rb.Get32(), rb.Get32()
	if uint64(ownerLen)+uint64(groupLen) > uint64(rb.Left()) {
		return nil, invalid
	}
	owner, group := string(rb.Get(int(ownerLen))), string(rb.Get(int(groupLen)))
	o.key = string(rb.Get(rb.Left()))
	if flags&sortedObjectIsFile == 0 {
		return &o, nil
	}
	return &file{obj: o, mode: mode, owner: owner, group: group, isSymlink: flags&sortedObjectIsSymlink != 0}, nil
}

func compareSortedObject(a, b Object) int {
	return strings.Compare(a.Key(), b.Key())
}

// SortObjects returns the objects from in sorted by key, dropping duplicated keys.
// They are sorted in memory if they fit in one chunk, otherwise on disk under dir.
// A nil from in marks a failed listing: the output then contains only a nil, so
// partial results are never returned. Nothing is returned before in is closed.
// The output is closed when done or ctx is cancelled, and in is always drained.
func SortObjects(ctx context.Context, dir string, in <-chan Object) <-chan Object {
	out := make(chan Object, 1000)
	go func() {
		defer close(out)
		defer func() {
			go func() {
				for range in {
				}
			}()
		}()
		send := func(o Object) bool {
			select {
			case out <- o:
				return true
			case <-ctx.Done():
				return false
			}
		}
		fail := func(err error) {
			if ctx.Err() == nil {
				logger.Errorf("Sort objects: %s", err)
				send(nil)
			}
		}

		sortCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		sorter, err := extsort.New(sortCtx, extsort.Config{
			WorkDir:   dir,
			Name:      "sort-objects",
			Threads:   externalSortThreads,
			ChunkSize: externalSortChunkSize,
			Checksum:  true,
		}, extsort.Codec[Object]{
			FromBytes: decodeSortedObject,
			ToBytes:   encodeSortedObject,
			Compare:   compareSortedObject,
		})
		if err != nil {
			fail(err)
			return
		}

		inputDone := make(chan struct{})
		go func() {
			defer close(inputDone)
			for {
				select {
				case o, ok := <-in:
					if !ok {
						sorter.CloseInput()
						return
					}
					if o == nil {
						cancel(errListingFailed)
						return
					}
					select {
					case sorter.Input() <- o:
					case <-sortCtx.Done():
						return
					}
				case <-sortCtx.Done():
					return
				}
			}
		}()
		defer func() {
			cancel(nil)
			<-inputDone
			// wait for the sorter to stop and release its temporary files
			for {
				if _, ok, err := sorter.Next(context.Background()); !ok || err != nil {
					break
				}
			}
		}()

		var last string
		first := true
		for {
			o, ok, err := sorter.Next(sortCtx)
			if err != nil {
				if cause := context.Cause(sortCtx); cause != nil {
					err = cause
				}
				fail(err)
				return
			}
			if !ok {
				return
			}
			if !first && o.Key() == last {
				continue
			}
			first, last = false, o.Key()
			if !send(o) {
				return
			}
		}
	}()
	return out
}
