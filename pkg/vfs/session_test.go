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
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/stretchr/testify/require"
)

type vfsSessionMeta struct {
	meta.Meta
	lost       atomic.Bool
	writes     atomic.Int64
	lease      meta.SessionLease
	newSlice   func(meta.Context, *uint64) syscall.Errno
	afterWrite func() syscall.Errno
}

func (m *vfsSessionMeta) CheckSession() syscall.Errno {
	if m.lost.Load() {
		return syscall.EIO
	}
	return m.Meta.CheckSession()
}

func (m *vfsSessionMeta) ExportSessionLease() (meta.SessionLease, error) {
	if m.CheckSession() != 0 {
		return meta.SessionLease{}, meta.ErrSessionLost
	}
	return m.lease, nil
}

func (m *vfsSessionMeta) NewSlice(ctx meta.Context, id *uint64) syscall.Errno {
	if m.newSlice != nil {
		return m.newSlice(ctx, id)
	}
	return m.Meta.NewSlice(ctx, id)
}

func (m *vfsSessionMeta) Write(ctx meta.Context, inode Ino, indx, off uint32, slice meta.Slice, mtime time.Time) syscall.Errno {
	m.writes.Add(1)
	if st := m.Meta.Write(ctx, inode, indx, off, slice, mtime); st != 0 {
		return st
	}
	if m.afterWrite != nil {
		return m.afterWrite()
	}
	return 0
}

func newSessionTestVFS() (*VFS, *vfsSessionMeta) {
	conf := meta.DefaultConf()
	conf.MountPoint = "/jfs"
	m := &vfsSessionMeta{Meta: meta.NewClient("memkv://", conf)}
	v, _ := newTestVFS(m, conf)
	return v, m
}

func TestVFSSessionLost(t *testing.T) {
	v, m := newSessionTestVFS()
	ctx := NewLogContext(meta.Background())
	entry, fh, st := v.Create(ctx, 1, "buffered", 0644, 0, syscall.O_RDWR)
	require.Zero(t, st)
	require.Zero(t, v.Write(ctx, entry.Inode, []byte("buffered"), 0, fh))
	dirfh, st := v.Opendir(ctx, 1, 0)
	require.Zero(t, st)
	m.lost.Store(true)

	for _, tc := range []struct {
		name string
		call func() syscall.Errno
	}{
		{"lookup internal", func() syscall.Errno { _, e := v.Lookup(ctx, 1, internalNodes[0].name); return e }},
		{"getattr internal", func() syscall.Errno { _, e := v.GetAttr(ctx, ConfigInode, 0); return e }},
		{"mknod", func() syscall.Errno { _, e := v.Mknod(ctx, 1, "node", 0644, 0, 0); return e }},
		{"mkdir", func() syscall.Errno { _, e := v.Mkdir(ctx, 1, "dir", 0755, 0); return e }},
		{"unlink", func() syscall.Errno { return v.Unlink(ctx, 1, "buffered") }},
		{"rmdir", func() syscall.Errno { return v.Rmdir(ctx, 1, "dir") }},
		{"symlink", func() syscall.Errno { _, e := v.Symlink(ctx, "buffered", 1, "link"); return e }},
		{"readlink", func() syscall.Errno { _, e := v.Readlink(ctx, entry.Inode); return e }},
		{"rename", func() syscall.Errno { return v.Rename(ctx, 1, "buffered", 1, "new", 0) }},
		{"link", func() syscall.Errno { _, e := v.Link(ctx, entry.Inode, 1, "link"); return e }},
		{"opendir", func() syscall.Errno { _, e := v.Opendir(ctx, 1, 0); return e }},
		{"readdir", func() syscall.Errno { _, _, e := v.Readdir(ctx, 1, 0, 0, dirfh, false); return e }},
		{"create", func() syscall.Errno { _, _, e := v.Create(ctx, 1, "new", 0644, 0, syscall.O_RDWR); return e }},
		{"open internal", func() syscall.Errno { _, _, e := v.Open(ctx, ConfigInode, syscall.O_RDONLY); return e }},
		{"truncate", func() syscall.Errno { return v.Truncate(ctx, entry.Inode, 0, fh, nil) }},
		{"read empty", func() syscall.Errno { _, e := v.Read(ctx, entry.Inode, nil, 0, fh); return e }},
		{"read EOF", func() syscall.Errno { _, e := v.Read(ctx, entry.Inode, make([]byte, 1), 100, fh); return e }},
		{"write empty", func() syscall.Errno { return v.Write(ctx, entry.Inode, nil, 0, fh) }},
		{"fallocate", func() syscall.Errno { return v.Fallocate(ctx, entry.Inode, 0, 0, 10, fh) }},
		{"copy empty", func() syscall.Errno {
			_, e := v.CopyFileRange(ctx, entry.Inode, fh, 0, entry.Inode, fh, 10, 0, 0)
			return e
		}},
		{"flush", func() syscall.Errno { return v.Flush(ctx, entry.Inode, fh, 0) }},
		{"fsync", func() syscall.Errno { return v.Fsync(ctx, entry.Inode, 0, fh) }},
		{"setxattr", func() syscall.Errno { return v.SetXattr(ctx, entry.Inode, "user.a", nil, 0) }},
		{"getxattr internal", func() syscall.Errno { _, e := v.GetXattr(ctx, ConfigInode, "user.a", 0); return e }},
		{"listxattr internal", func() syscall.Errno { _, e := v.ListXattr(ctx, ConfigInode, 0); return e }},
		{"removexattr", func() syscall.Errno { return v.RemoveXattr(ctx, entry.Inode, "user.a") }},
		{"statfs", func() syscall.Errno { _, e := v.StatFS(ctx, 1); return e }},
		{"access internal", func() syscall.Errno { return v.Access(ctx, ConfigInode, 0) }},
		{"setattr", func() syscall.Errno {
			_, e := v.SetAttr(ctx, entry.Inode, 0, fh, 0, 0, 0, 0, 0, 0, 0, 0)
			return e
		}},
		{"getlk", func() syscall.Errno {
			start, length, typ, pid := uint64(0), uint64(1), uint32(syscall.F_WRLCK), uint32(0)
			return v.Getlk(ctx, entry.Inode, fh, 1, &start, &length, &typ, &pid)
		}},
		{"setlk", func() syscall.Errno { return v.Setlk(ctx, entry.Inode, fh, 1, 0, 1, syscall.F_WRLCK, 0, false) }},
		{"flock", func() syscall.Errno { return v.Flock(ctx, entry.Inode, fh, 1, syscall.F_WRLCK, false) }},
		{"ioctl", func() syscall.Errno { return v.Ioctl(ctx, entry.Inode, 0, 0, nil, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, syscall.EIO, tc.call()) })
	}
	h := v.findHandle(entry.Inode, fh)
	_, st = h.reader.Read(ctx, 100, nil)
	require.Equal(t, syscall.EIO, st)
	require.Equal(t, syscall.EIO, h.writer.Write(ctx, 0, nil))
	require.Equal(t, syscall.EIO, h.writer.Flush(ctx))
	require.Equal(t, syscall.EIO, v.writer.Flush(ctx, 10000))
	require.Equal(t, syscall.EIO, v.writer.FlushAll())
	require.Equal(t, syscall.EIO, v.FlushAll(""))
	v.Release(ctx, entry.Inode, fh)
	require.Equal(t, int(syscall.EIO), v.Releasedir(ctx, 1, dirfh))
	w := v.writer.(*dataWriter)
	require.Eventually(t, func() bool {
		w.Lock()
		defer w.Unlock()
		return len(w.files) == 0
	}, 2*time.Second, time.Millisecond*10)
	require.Zero(t, m.writes.Load(), "lost session must not commit buffered slices")
	var attr meta.Attr
	require.Zero(t, m.Meta.GetAttr(ctx, entry.Inode, &attr))
	require.Zero(t, attr.Length)
}

func TestVFSReadSessionLost(t *testing.T) {
	v, m := newSessionTestVFS()
	ctx := NewLogContext(meta.Background())
	entry, fh, st := v.Create(ctx, 1, "cached", 0644, 0, syscall.O_RDWR)
	require.Zero(t, st)
	require.Zero(t, v.Write(ctx, entry.Inode, []byte("hello"), 0, fh))
	require.Zero(t, v.Flush(ctx, entry.Inode, fh, 0))
	h := v.findHandle(entry.Inode, fh)
	buf := make([]byte, 5)
	n, st := h.reader.Read(ctx, 0, buf)
	require.Zero(t, st)
	require.Equal(t, 5, n)
	m.lost.Store(true)
	n, st = h.reader.Read(ctx, 0, buf)
	require.Equal(t, syscall.EIO, st)
	require.Zero(t, n)
	v.Release(ctx, entry.Inode, fh)
}

func TestVFSWriteWaitSessionLost(t *testing.T) {
	v, m := newSessionTestVFS()
	f := v.writer.Open(1, 0, 0).(*fileWriter)
	f.Lock()
	f.flushwaiting++
	f.Unlock()
	done := make(chan syscall.Errno, 1)
	go func() { done <- f.Write(meta.Background(), 0, []byte("hello")) }()
	require.Eventually(t, func() bool {
		f.Lock()
		defer f.Unlock()
		return f.writewaiting > 0
	}, time.Second, time.Millisecond)
	m.lost.Store(true)
	select {
	case st := <-done:
		require.Equal(t, syscall.EIO, st)
	case <-time.After(time.Second):
		t.Fatal("write remains blocked after session loss")
	}
	f.Lock()
	f.flushwaiting--
	f.Unlock()
	require.Equal(t, syscall.EIO, f.Close(meta.Background()))
}

func TestVFSPrepareIDSessionLost(t *testing.T) {
	v, m := newSessionTestVFS()
	called := make(chan struct{}, 1)
	m.newSlice = func(meta.Context, *uint64) syscall.Errno {
		select {
		case called <- struct{}{}:
		default:
		}
		return syscall.EIO
	}
	f := v.writer.Open(1, 0, 0).(*fileWriter)
	s := &sliceWriter{chunk: &chunkWriter{file: f}}
	done := make(chan struct{})
	go func() { s.prepareID(meta.Background(), true); close(done) }()
	<-called
	m.lost.Store(true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prepareID continues retrying after session loss")
	}
	require.Equal(t, syscall.EIO, s.err)
	require.Equal(t, syscall.EIO, f.Close(meta.Background()))
}

type sessionRemoveStore struct {
	chunk.ChunkStore
	removed chan uint64
}

func (s *sessionRemoveStore) Remove(id uint64, length int) error {
	err := s.ChunkStore.Remove(id, length)
	s.removed <- id
	return err
}

func TestVFSSessionLostUncertainCommit(t *testing.T) {
	conf := meta.DefaultConf()
	conf.MountPoint = "/jfs"
	m := &vfsSessionMeta{Meta: meta.NewClient("memkv://", conf)}
	m.afterWrite = func() syscall.Errno { m.lost.Store(true); return syscall.EIO }
	v, _ := newTestVFS(m, conf)
	w := v.writer.(*dataWriter)
	store := &sessionRemoveStore{ChunkStore: w.store, removed: make(chan uint64, 10)}
	w.store = store
	ctx := NewLogContext(meta.Background())
	entry, fh, st := v.Create(ctx, 1, "uncertain", 0644, 0, syscall.O_RDWR)
	require.Zero(t, st)
	require.Zero(t, v.Write(ctx, entry.Inode, []byte("hello"), 0, fh))
	require.Equal(t, syscall.EIO, v.Flush(ctx, entry.Inode, fh, 0))
	var slices []meta.Slice
	require.Zero(t, m.Meta.Read(ctx, entry.Inode, 0, &slices))
	require.Len(t, slices, 1)
	require.NotZero(t, slices[0].Id)
	select {
	case id := <-store.removed:
		t.Fatalf("removed slice %d after an uncertain metadata commit", id)
	case <-time.After(time.Millisecond * 100):
	}
	v.Release(ctx, entry.Inode, fh)
}

func TestVFSHandleSessionLease(t *testing.T) {
	v, m := newSessionTestVFS()
	m.lease = meta.SessionLease{Sid: 9, Heartbeat: time.Second * 12, LastRefresh: time.Now().UnixNano()}
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, v.dumpAllHandles(path))
	lease, err := LoadSessionLease(path)
	require.NoError(t, err)
	require.Equal(t, &m.lease, lease)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	v.Conf.Meta.SessionLease = &meta.SessionLease{Sid: m.lease.Sid + 1}
	require.Equal(t, meta.ErrSessionLost, v.loadAllHandles(path), "takeover state must match the validated lease")
	v.Conf.Meta.SessionLease = nil
	m.lost.Store(true)
	require.Equal(t, syscall.EIO, v.dumpAllHandles(path))
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, saved, unchanged)
	require.Equal(t, syscall.EIO, v.loadAllHandles(path))
	require.NoError(t, os.WriteFile(path, []byte(`{"Handler":{},"NextFh":1}`), 0600))
	lease, err = LoadSessionLease(path)
	require.NoError(t, err)
	require.Nil(t, lease)
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0600))
	_, err = LoadSessionLease(path)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"SessionLease":{},"Handler":"invalid"}`), 0600))
	_, err = LoadSessionLease(path)
	require.Error(t, err, "the lease header must not hide invalid handle state")

	m.lost.Store(false)
	v.Conf.Meta.Sid = m.lease.Sid + 1
	require.NoError(t, os.WriteFile(path, saved, 0600))
	t.Setenv("_FUSE_STATE_PATH", path)
	fresh := NewVFS(v.Conf, m, v.Store, nil, v.registry)
	require.NotNil(t, fresh)
	require.Empty(t, fresh.handleIno, "a fresh SID must ignore stale state files")
	v.Conf.Meta.SessionLease = &m.lease
	t.Setenv("_FUSE_STATE_PATH", path+".missing")
	require.Nil(t, NewVFS(v.Conf, m, v.Store, nil, v.registry), "failed protected takeover must not serve empty handles")
}
