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
	"io"
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
	busy     map[Ino]int        // inodes with a passthrough open or pending reconcile
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
	base := dir
	if base == "" {
		base = filepath.Join(os.TempDir(), "juicefs-passthrough")
	}
	_ = os.MkdirAll(base, 0700)
	// Isolate this mount's staging in a per-process subdir. Several juicefs
	// mounts (multiple volumes on a node, or a graceful-restart successor)
	// can share `base`; a flat shared dir would let one mount's pool-N.tmp
	// collide with another's, and — worse — a startup sweep of the shared dir
	// would unlink another live mount's in-use staging files, silently losing
	// the data of files whose close(2) already returned. A private subdir
	// removes both hazards, so we deliberately do NOT garbage-collect the
	// shared root here (a crashed predecessor's subdir is inert and can be
	// reaped out of band).
	sub, err := os.MkdirTemp(base, fmt.Sprintf("m%d-", os.Getpid()))
	if err != nil {
		logger.Warnf("passthrough: per-process staging dir under %s: %s; using base", base, err)
		sub = base
	}
	return &passthroughState{
		server: server,
		dir:    sub,
		files:  make(map[uint64]*ptFile),
		busy:   make(map[Ino]int),
	}
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
	// O_EXCL: never open a staging file that already exists. The dir is
	// per-process and seq is monotonic, so a collision would signal a bug
	// (or a shared-dir fallback), and truncating a pre-existing file could
	// clobber another mount's in-use backing.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
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

// tryOpen sets up passthrough for a write-opened file that is EMPTY at open.
// It returns the kernel backing ID and true on success; callers then set
// FOPEN_PASSTHROUGH and OpenOut.BackingID. On any failure it returns false and
// the caller falls back to the normal (daemon) path — passthrough is purely an
// optimization.
//
// emptyAtOpen MUST be true only when the file has no pre-existing content the
// open can observe or extend: a fresh Create, or an Open with O_TRUNC, or a
// zero-length file. This is a correctness gate, not a heuristic. The backing
// staging file always starts empty and FOPEN_PASSTHROUGH diverts reads, writes
// AND mmap to it, so enabling it on a non-empty file would (a) serve reads as
// zeros/EOF instead of real content (read-modify-write corruption), and (b)
// make reconcile — which copies staging linearly from offset 0 — overwrite the
// file's real prefix with holes for O_APPEND / sparse / seek-write patterns.
func (p *passthroughState) tryOpen(ino Ino, fh uint64, flags uint32, emptyAtOpen bool) (int32, bool) {
	if p == nil || !isWriteOpen(flags) || !emptyAtOpen {
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
	// One passthrough writer per inode at a time. A second write-open while an
	// earlier one is still open — or while its reconcile is in flight (during
	// which the metadata size still reads 0, so emptyAtOpen looks true again) —
	// would get its own empty backing whose linear reconcile overwrites the
	// first's data. Reserve the inode; overlapping opens fall to the daemon
	// path, which serializes correctly.
	p.mu.Lock()
	if p.disabled || p.busy[ino] > 0 {
		p.mu.Unlock()
		return 0, false
	}
	p.busy[ino]++
	p.mu.Unlock()

	b, ok := p.checkout()
	if !ok {
		p.mu.Lock()
		p.releaseBusyLocked(ino)
		p.mu.Unlock()
		return 0, false
	}
	p.mu.Lock()
	p.files[fh] = &ptFile{ino: ino, fh: fh, b: b}
	p.mu.Unlock()
	return b.backingID, true
}

// releaseBusyLocked drops one busy reference for ino. Caller holds p.mu.
func (p *passthroughState) releaseBusyLocked(ino Ino) {
	if n := p.busy[ino]; n <= 1 {
		delete(p.busy, ino)
	} else {
		p.busy[ino] = n - 1
	}
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
	// Free the inode for a new passthrough open only once its data has fully
	// landed (or been preserved on failure) — held across the whole reconcile.
	defer func() {
		p.mu.Lock()
		p.releaseBusyLocked(pf.ino)
		p.mu.Unlock()
	}()
	// Fence for consistency points now that we know this is a passthrough
	// release with data to land: the application's close(2) has already
	// returned, but the data still lives only in the staging file until the
	// copy below finishes. A checkpoint or commit that ran concurrently would
	// flush+snapshot without these writes and publish a mid-copy (short) file.
	// The external-flush counter lets those paths wait for in-flight
	// reconciles first. Scoped past the pf==nil check so plain (non-
	// passthrough) releases don't add spurious fence contention.
	v.BeginExternalFlush()
	defer v.EndExternalFlush()
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
		if !done {
			// Reconcile failed: the staging file is the ONLY copy of data whose
			// close(2) already returned 0, so do NOT delete it — preserve it as
			// an .orphan sibling for manual recovery and log loudly. Drop the
			// kernel registration and the live fd so the backing isn't reused
			// with stale data.
			if errno := p.server.UnregisterBackingFd(b.backingID); errno != 0 {
				logger.Warnf("passthrough: UnregisterBackingFd(%d): %s", b.backingID, errno)
			}
			_ = b.f.Close()
			orphan := b.path + fmt.Sprintf(".orphan-%d", pf.ino)
			if err := os.Rename(b.path, orphan); err != nil {
				logger.Errorf("passthrough: reconcile of ino %d FAILED and staging %s could not be preserved: %s", pf.ino, b.path, err)
			} else {
				logger.Errorf("passthrough: reconcile of ino %d FAILED; unreconciled data preserved at %s", pf.ino, orphan)
			}
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
			if err != io.EOF {
				// A read error mid-copy must NOT be mistaken for end-of-file:
				// that would flush+commit a truncated file as if complete. Bail
				// so the defer preserves the staging and the file is not sealed
				// short.
				logger.Errorf("passthrough: read staging %s at off %d: %s", b.path, off, err)
				return
			}
			break
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
