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

package fuse

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/juicedata/juicefs/pkg/vfs"
)

// passthroughState manages per-open backing files used for FUSE passthrough
// write acceleration. EXPERIMENTAL: scoped to the write path. A file opened
// for write gets a local staging file (on a non-stacked fs) that the kernel
// reads/writes directly via FUSE_PASSTHROUGH, bypassing the daemon per-op. On
// release the staging file is reconciled into JuiceFS slices via the normal
// writer path. Durability is therefore deferred to release (commit-style).
type passthroughState struct {
	server *fuse.Server
	dir    string

	mu      sync.Mutex
	files   map[uint64]*ptFile // keyed by fh
	warnOne sync.Once
}

type ptFile struct {
	ino       Ino
	fh        uint64
	path      string
	f         *os.File
	backingID int32
}

func newPassthroughState(server *fuse.Server, dir string) *passthroughState {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "juicefs-passthrough")
	}
	_ = os.MkdirAll(dir, 0700)
	return &passthroughState{server: server, dir: dir, files: make(map[uint64]*ptFile)}
}

func isWriteOpen(flags uint32) bool {
	acc := flags & uint32(syscall.O_ACCMODE)
	return acc == syscall.O_WRONLY || acc == syscall.O_RDWR
}

// tryOpen sets up passthrough for a write-opened file. It returns the kernel
// backing ID and true on success; callers then set FOPEN_PASSTHROUGH and
// OpenOut.BackingID. On any failure it returns false and the caller falls back
// to the normal (daemon) path — passthrough is purely an optimization.
func (p *passthroughState) tryOpen(ino Ino, fh uint64, flags uint32) (int32, bool) {
	if p == nil || !isWriteOpen(flags) {
		return 0, false
	}
	if !p.server.SupportsPassthrough() {
		p.warnOne.Do(func() {
			logger.Warnf("FUSE passthrough requested but not supported by the kernel; falling back")
		})
		return 0, false
	}
	path := filepath.Join(p.dir, fmt.Sprintf("%d-%d.tmp", ino, fh))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		logger.Warnf("passthrough: open staging %s: %s", path, err)
		return 0, false
	}
	id, errno := p.server.RegisterBackingFd(&fuse.BackingMap{Fd: int32(f.Fd())})
	if errno != 0 {
		logger.Warnf("passthrough: RegisterBackingFd(%s): %s", path, errno)
		_ = f.Close()
		_ = os.Remove(path)
		return 0, false
	}
	p.mu.Lock()
	p.files[fh] = &ptFile{ino: ino, fh: fh, path: path, f: f, backingID: id}
	p.mu.Unlock()
	return id, true
}

// reconcile flushes a passthrough staging file back into JuiceFS slices, then
// tears down the backing registration. Called on release, before vfs.Release.
func (p *passthroughState) reconcile(ctx vfs.Context, v *vfs.VFS, fh uint64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	pf := p.files[fh]
	if pf != nil {
		delete(p.files, fh)
	}
	p.mu.Unlock()
	if pf == nil {
		return
	}
	// Stop kernel passthrough first so no further direct writes land in the
	// backing file, and close the registered fd, then re-open the staging file
	// by path for a clean sequential read (reading via the registered backing
	// fd can return stale/partial data).
	if errno := p.server.UnregisterBackingFd(pf.backingID); errno != 0 {
		logger.Warnf("passthrough: UnregisterBackingFd(%d): %s", pf.backingID, errno)
	}
	_ = pf.f.Close()
	defer func() { _ = os.Remove(pf.path) }()

	rf, err := os.Open(pf.path)
	if err != nil {
		logger.Errorf("passthrough: reopen staging %s: %s", pf.path, err)
		return
	}
	defer rf.Close()
	buf := make([]byte, 4<<20)
	var off uint64
	for {
		n, err := rf.Read(buf)
		if n > 0 {
			// vfs.Write may retain the buffer until flush; give each chunk its
			// own backing array so the next Read doesn't corrupt a pending slice.
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if e := v.Write(ctx, pf.ino, chunk, off, fh); e != 0 {
				logger.Errorf("passthrough: reconcile write ino %d off %d: %s", pf.ino, off, e)
				return
			}
			off += uint64(n)
		}
		if err != nil {
			break // io.EOF or read error
		}
	}
	if e := v.Flush(ctx, pf.ino, fh, 0); e != 0 {
		logger.Errorf("passthrough: reconcile flush ino %d: %s", pf.ino, e)
	}
	// Passthrough writes bypassed the daemon, so the kernel's cached size and
	// page data for this inode are stale (size is still 0 from the empty
	// create). Now that the slices + metadata are committed, invalidate both so
	// readers in this mount session see the reconciled file (read-your-writes).
	p.server.InodeNotify(uint64(pf.ino), -1, 0)         // attributes (size/mtime)
	p.server.InodeNotify(uint64(pf.ino), 0, int64(off)) // data range
}
