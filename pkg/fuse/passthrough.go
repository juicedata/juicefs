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
//
// Backing registrations are POOLED: registering a backing fd costs an ioctl
// (or, on unprivileged broker mounts, an RPC round trip to the node broker)
// plus a staging-file create, once per write-open. Small-file workloads open
// thousands of times, so instead of register-at-open/unregister-at-release,
// reconciled staging files are truncated to zero and parked for the next
// open; after warm-up a small-file loop performs no registrations at all
// (ENG26-869). A backing is never attached to two live opens at once:
// checkout is exclusive, and a backing returns to the pool only after its
// reconcile finished (data copied out, file truncated).
type passthroughState struct {
	server *fuse.Server
	dir    string

	mu       sync.Mutex
	files    map[uint64]*ptFile // keyed by fh
	pool     []*ptBacking       // idle registered backings, truncated to 0
	poolSeq  int
	disabled bool // registration failed with a permanent error; stop trying
	warnOne  sync.Once
}

// ptPoolCap bounds the idle registered backings kept per mount. Each entry
// pins one kernel backing registration and one empty staging file; the cap
// only needs to cover the plausible number of concurrent write-opens.
const ptPoolCap = 64

// ptBacking is one registered kernel backing: a staging file plus the
// backing ID the kernel handed back for it. It outlives individual opens.
type ptBacking struct {
	path      string
	f         *os.File
	backingID int32
}

type ptFile struct {
	ino Ino
	fh  uint64
	b   *ptBacking
}

func newPassthroughState(server *fuse.Server, dir string) *passthroughState {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "juicefs-passthrough")
	}
	_ = os.MkdirAll(dir, 0700)
	// Best-effort: drop staging files a crashed predecessor left behind.
	if stale, err := filepath.Glob(filepath.Join(dir, "*.tmp")); err == nil {
		for _, p := range stale {
			_ = os.Remove(p)
		}
	}
	return &passthroughState{server: server, dir: dir, files: make(map[uint64]*ptFile)}
}

// checkout returns an idle registered backing, or registers a fresh one.
func (p *passthroughState) checkout() (*ptBacking, bool) {
	p.mu.Lock()
	if p.disabled {
		p.mu.Unlock()
		return nil, false
	}
	if n := len(p.pool); n > 0 {
		b := p.pool[n-1]
		p.pool = p.pool[:n-1]
		p.mu.Unlock()
		return b, true
	}
	p.poolSeq++
	seq := p.poolSeq
	p.mu.Unlock()

	path := filepath.Join(p.dir, fmt.Sprintf("pool-%d.tmp", seq))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		logger.Warnf("passthrough: open staging %s: %s", path, err)
		return nil, false
	}
	id, errno := p.server.RegisterBackingFd(&fuse.BackingMap{Fd: int32(f.Fd())})
	if errno != 0 {
		_ = f.Close()
		_ = os.Remove(path)
		// EPERM is permanent: the backing-registration ioctl needs
		// CAP_SYS_ADMIN in the init user namespace, and a process that
		// lacks it now will lack it for every open (e.g. a non-root
		// container whose added capabilities are bounding-set only).
		// Without this latch every write-open pays a doomed ioctl, a
		// staging create/remove, and a warning line (ENG26-869 saw 2000+
		// per run). Other errnos may be transient; keep trying those.
		if errno == syscall.EPERM {
			p.mu.Lock()
			p.disabled = true
			p.mu.Unlock()
			logger.Warnf("passthrough: RegisterBackingFd(%s): %s; disabling passthrough for this mount "+
				"(the ioctl needs CAP_SYS_ADMIN in the init user namespace — run the mount as root, "+
				"or use a mount broker that performs registrations node-side)", path, errno)
			return nil, false
		}
		logger.Warnf("passthrough: RegisterBackingFd(%s): %s", path, errno)
		return nil, false
	}
	return &ptBacking{path: path, f: f, backingID: id}, true
}

// checkin parks a reconciled backing for reuse, or retires it when the pool
// is full. The staging file MUST already be truncated to zero.
func (p *passthroughState) checkin(b *ptBacking) {
	p.mu.Lock()
	if len(p.pool) < ptPoolCap {
		p.pool = append(p.pool, b)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	if errno := p.server.UnregisterBackingFd(b.backingID); errno != 0 {
		logger.Warnf("passthrough: UnregisterBackingFd(%d): %s", b.backingID, errno)
	}
	_ = b.f.Close()
	_ = os.Remove(b.path)
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
	// Never hand JuiceFS's internal/control files (.control, .stats, .config,
	// ...) to the kernel via a backing file. Those inodes are served by the
	// daemon's in-process handlers; passthrough would silently divert their
	// I/O to a plain temp file, breaking the control protocol — e.g. the
	// `juicefs checkpoint` verb, whose write/read on .control would go to the
	// backing file and never reach the handler, hanging the command.
	if vfs.IsSpecialNode(ino) {
		return 0, false
	}
	if !p.server.SupportsPassthrough() {
		p.warnOne.Do(func() {
			logger.Warnf("FUSE passthrough requested but not supported by the kernel; falling back")
		})
		return 0, false
	}
	b, ok := p.checkout()
	if !ok {
		return 0, false
	}
	p.mu.Lock()
	p.files[fh] = &ptFile{ino: ino, fh: fh, b: b}
	p.mu.Unlock()
	return b.backingID, true
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
	// The kernel stops issuing passthrough I/O for this open once its release
	// is processed, and reconcile runs from the RELEASE handler — after the
	// application's last close, so no writes are in flight. The registration
	// itself is kept alive for reuse (see checkin); read the staging content
	// through a fresh path-open fd for a clean sequential pass (reading via
	// the registered backing fd can return stale/partial data), then truncate
	// and park the backing for the next open.
	b := pf.b
	done := false
	defer func() {
		if !done { // reconcile failed: don't reuse a backing with stale data
			if errno := p.server.UnregisterBackingFd(b.backingID); errno != 0 {
				logger.Warnf("passthrough: UnregisterBackingFd(%d): %s", b.backingID, errno)
			}
			_ = b.f.Close()
			_ = os.Remove(b.path)
		}
	}()

	rf, err := os.Open(b.path)
	if err != nil {
		logger.Errorf("passthrough: reopen staging %s: %s", b.path, err)
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
		return
	}
	// Passthrough writes bypassed the daemon, so the kernel's cached size and
	// page data for this inode are stale (size is still 0 from the empty
	// create). Now that the slices + metadata are committed, invalidate both so
	// readers in this mount session see the reconciled file (read-your-writes).
	p.server.InodeNotify(uint64(pf.ino), -1, 0)         // attributes (size/mtime)
	p.server.InodeNotify(uint64(pf.ino), 0, int64(off)) // data range
	// Data is safely in JuiceFS: recycle the registration for the next open.
	if err := b.f.Truncate(0); err != nil {
		logger.Warnf("passthrough: truncate staging %s: %s", b.path, err)
		return // defer retires it
	}
	done = true
	p.checkin(b)
}
