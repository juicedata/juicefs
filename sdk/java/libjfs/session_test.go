/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package main

import (
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
)

type terminalSDKMeta struct {
	meta.Meta
	lost      atomic.Bool
	closed    atomic.Int32
	shutdowns atomic.Int32
	reloads   atomic.Int32
}

func (m *terminalSDKMeta) CheckSession() syscall.Errno {
	if m.lost.Load() {
		return syscall.EIO
	}
	return 0
}
func (m *terminalSDKMeta) Name() string                      { return "memkv" }
func (m *terminalSDKMeta) GetFormat() meta.Format            { return meta.Format{} }
func (m *terminalSDKMeta) FlushSession()                     {}
func (m *terminalSDKMeta) OnReload(func(*meta.Format) error) { m.reloads.Add(1) }
func (m *terminalSDKMeta) CloseSession() error {
	m.closed.Add(1)
	if m.lost.Load() {
		return meta.ErrSessionLost
	}
	return nil
}

func (m *terminalSDKMeta) Shutdown() error {
	m.shutdowns.Add(1)
	return nil
}

func newSessionTestFS(t *testing.T, m *terminalSDKMeta) *fs.FileSystem {
	t.Helper()
	conf := &vfs.Config{
		Meta:  meta.DefaultConf(),
		Chunk: &chunk.Config{BlockSize: 4 << 20, BufferSize: 8 << 20},
	}
	jfs, err := fs.NewFileSystem(conf, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return jfs
}

func TestSDKLostSessionCache(t *testing.T) {
	name := t.Name()
	formats[name] = &meta.Format{KerbConf: "configured"}
	t.Cleanup(func() { delete(formats, name) })
	oldMeta := &terminalSDKMeta{}
	newMeta := &terminalSDKMeta{}
	oldFS := newSessionTestFS(t, oldMeta)
	created := 0
	factory := func() *fs.FileSystem {
		created++
		if created == 1 {
			return oldFS
		}
		return newSessionTestFS(t, newMeta)
	}
	init := func() int64 { return getOrCreate(name, "root", "root", "root", "root", javaConf{}, factory) }
	h1, h2 := init(), init()
	if h1 == 0 || h2 == 0 || created != 1 {
		t.Fatalf("shared init: %d, %d, created %d", h1, h2, created)
	}
	if oldMeta.reloads.Load() != 1 {
		t.Fatalf("shared client reload registrations: %d", oldMeta.reloads.Load())
	}
	f, st := oldFS.Open(meta.Background(), "/.config", 0)
	if st != 0 {
		t.Fatal(st)
	}
	oldFD := nextFileHandle(f, F(h1))
	oldMeta.lost.Store(true)
	if h := init(); h != 0 || created != 1 {
		t.Fatalf("reused terminal filesystem: handle %d, created %d", h, created)
	}
	if st := jfs_statvfs(1, h1, 0); st != errno(syscall.EIO) {
		t.Fatalf("statvfs: %d", st)
	}
	if st := jfs_lseek(1, oldFD, 0, 0); st != int64(errno(syscall.EIO)) {
		t.Fatalf("lseek: %d", st)
	}
	if st := jfs_term(1, h1, 0); st != errno(syscall.EIO) {
		t.Fatalf("first term: %d", st)
	}
	if h := init(); h != 0 || created != 1 {
		t.Fatalf("replaced filesystem while an old wrapper remains: %d", h)
	}
	if oldMeta.closed.Load() != 0 {
		t.Fatal("closed filesystem still used by another wrapper")
	}
	if st := jfs_term(1, h2, 0); st != errno(syscall.EIO) {
		t.Fatalf("last term: %d", st)
	}
	if oldMeta.closed.Load() != 1 {
		t.Fatalf("last term did not close terminal filesystem: %d", oldMeta.closed.Load())
	}
	if oldMeta.shutdowns.Load() != 1 {
		t.Fatalf("last term did not release metadata resources: %d", oldMeta.shutdowns.Load())
	}
	h3 := init()
	if h3 == 0 || created != 2 || F(h3).FileSystem == oldFS {
		t.Fatalf("did not create fresh filesystem: %d, created %d", h3, created)
	}
	if newMeta.reloads.Load() != 1 {
		t.Fatal("replacement client did not register its security configuration reload")
	}
	if _, ok := openFiles[oldFD]; ok {
		t.Fatal("old fd survived filesystem termination")
	}
	if st := jfs_read(1, oldFD, 0, 0); st != EINVAL {
		t.Fatalf("old fd mapped to a new filesystem: %d", st)
	}
	if st := jfs_term(1, h3, 1); st != 0 {
		t.Fatalf("fresh term: %d", st)
	}
}

func TestSDKLostIdleSessionCache(t *testing.T) {
	name := t.Name()
	m := &terminalSDKMeta{}
	jfs := newSessionTestFS(t, m)
	created := 0
	init := func() int64 {
		return getOrCreate(name, "root", "root", "root", "root", javaConf{}, func() *fs.FileSystem {
			created++
			if created == 1 {
				return jfs
			}
			return newSessionTestFS(t, &terminalSDKMeta{})
		})
	}
	h := init()
	if st := jfs_term(1, h, 0); st != 0 {
		t.Fatalf("idle term: %d", st)
	}
	if m.closed.Load() != 0 {
		t.Fatal("healthy idle filesystem was closed")
	}
	m.lost.Store(true)
	h = init()
	if h == 0 || created != 2 || m.closed.Load() != 1 || m.shutdowns.Load() != 1 {
		t.Fatalf("did not discard failed idle cache: handle %d, created %d, closed %d", h, created, m.closed.Load())
	}
	if st := jfs_term(1, h, 1); st != 0 {
		t.Fatalf("fresh term: %d", st)
	}
}
