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

package vfs

import (
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/stretchr/testify/require"
)

func TestHandleRemoveOpClearsTail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		index int
	}{
		{"only", 1, 0},
		{"first", 3, 0},
		{"middle", 3, 1},
		{"last", 3, 2},
		{"absent", 3, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &handle{}
			var want []Context
			ctx := NewLogContext(meta.Background())
			for i := 0; i < tc.count; i++ {
				op := NewLogContext(meta.Background())
				h.addOp(op)
				if i == tc.index {
					ctx = op
				} else {
					want = append(want, op)
				}
			}
			h.removeOp(ctx)
			require.ElementsMatch(t, want, h.ops)
			for _, op := range h.ops[len(h.ops):cap(h.ops)] {
				require.Nil(t, op, "unused slot must not retain a context")
			}
		})
	}
}

func TestReleaseHandleClearsTail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		index int
	}{
		{"only", 1, 0},
		{"first", 3, 0},
		{"middle", 3, 1},
		{"last", 3, 2},
		{"absent", 3, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &VFS{handles: make(map[Ino][]*handle), handleIno: make(map[uint64]Ino)}
			const ino Ino = 2
			var want, original []*handle
			fh := ^uint64(0)
			for i := 0; i < tc.count; i++ {
				h := v.newHandle(ino, false, 0)
				original = append(original, h)
				if i == tc.index {
					fh = h.fh
				} else {
					want = append(want, h)
				}
			}
			// Truncate can still be using this snapshot when Release runs.
			snapshot := v.findAllHandles(ino)
			v.releaseHandle(ino, fh)
			require.ElementsMatch(t, want, v.handles[ino])
			for i, h := range original {
				require.Same(t, h, snapshot[i], "release must not invalidate a snapshot")
			}
			hs, exists := v.handles[ino]
			if len(want) == 0 {
				require.False(t, exists)
			}
			for _, h := range hs[len(hs):cap(hs)] {
				require.Nil(t, h, "unused slot must not retain a released handle")
			}
		})
	}
}

func TestHandleSnapshotAfterAppend(t *testing.T) {
	const ino Ino = 2
	v := &VFS{handles: map[Ino][]*handle{ino: make([]*handle, 0, 2)}, handleIno: make(map[uint64]Ino)}
	first := v.newHandle(ino, false, 0)
	snapshot := v.findAllHandles(ino)
	// Reuse the backing array, then replace its first slot during removal.
	v.newHandle(ino, false, 0)
	v.releaseHandle(ino, first.fh)
	require.Len(t, snapshot, 1)
	require.Same(t, first, snapshot[0], "opening and releasing handles must not modify an existing snapshot")
}

type blockingDirHandler struct {
	meta.DirHandler
	entered chan struct{}
	resume  chan struct{}
}

func (d *blockingDirHandler) Insert(Ino, string, *Attr) {
	close(d.entered)
	<-d.resume
}

func TestInvalidateDirHandleConcurrentRelease(t *testing.T) {
	v := &VFS{handles: make(map[Ino][]*handle), handleIno: make(map[uint64]Ino)}
	const ino Ino = 2
	first := v.newHandle(ino, false, 0)
	second := v.newHandle(ino, false, 0)
	d := &blockingDirHandler{entered: make(chan struct{}), resume: make(chan struct{})}
	first.dirHandler = d
	done := make(chan interface{}, 1)
	go func() {
		defer func() { done <- recover() }()
		v.invalidateDirHandle(ino, "entry", 3, &Attr{})
	}()
	<-d.entered
	// Release the next handle while invalidation is using its snapshot.
	v.releaseHandle(ino, second.fh)
	close(d.resume)
	require.Nil(t, <-done, "concurrent release must not invalidate the slice being traversed")
}

func TestTruncateReleasesContexts(t *testing.T) {
	for _, name := range []string{"path", "handle", "bad_handle", "read_only"} {
		t.Run(name, func(t *testing.T) {
			v, _ := createTestVFS(nil, "")
			t.Cleanup(func() { require.NoError(t, v.Meta.Shutdown()) })
			ctx := NewLogContext(meta.Background())
			entry, fh, st := v.Create(ctx, 1, "truncate-contexts", 0600, 0, syscall.O_RDWR)
			require.Zero(t, st)
			_, readFH, st := v.Open(ctx, entry.Inode, syscall.O_RDONLY)
			require.Zero(t, st)
			for _, f := range []uint64{fh, readFH} {
				t.Cleanup(func() {
					require.Zero(t, v.Meta.Close(ctx, entry.Inode))
					v.ReleaseHandler(entry.Inode, f)
				})
			}
			var want syscall.Errno
			switch name {
			case "path":
				fh = 0
			case "bad_handle":
				fh = ^uint64(1) // Even handles are not recovered by findHandle.
				want = syscall.EBADF
			case "read_only":
				fh = readFH
				want = syscall.EACCES
			}
			// Keep both handles open while repeatedly truncating the same file.
			for i := 0; i < 256; i++ {
				op := NewLogContext(meta.Background())
				require.Equal(t, want, v.Truncate(op, entry.Inode, 0, fh, &Attr{}))
			}
			for _, h := range v.findAllHandles(entry.Inode) {
				require.Zero(t, len(h.ops), "completed truncates must not accumulate on fh %d", h.fh)
				require.Zero(t, h.writing)
				for _, op := range h.ops[:cap(h.ops)] {
					require.Nil(t, op, "completed contexts must not remain in the backing array")
				}
			}
		})
	}
}

func TestTruncateCanceledLockReleasesContexts(t *testing.T) {
	v := &VFS{handles: make(map[Ino][]*handle), handleIno: make(map[uint64]Ino)}
	const ino Ino = 2
	first := v.newHandle(ino, false, 0)
	second := v.newHandle(ino, false, 0)
	blocker := NewLogContext(meta.Background())
	require.True(t, second.Wlock(blocker))
	defer func() {
		second.removeOp(blocker)
		second.Wunlock()
	}()
	ctx := NewLogContext(meta.Background())
	ctx.Cancel()
	require.Equal(t, syscall.EINTR, v.Truncate(ctx, ino, 0, 0, &Attr{}))
	require.Zero(t, len(first.ops), "the previously acquired handle must be cleaned up")
	require.Zero(t, first.writing)
	require.Equal(t, []Context{blocker}, second.ops, "the existing operation must remain registered")
	require.EqualValues(t, 1, second.writing)
}
