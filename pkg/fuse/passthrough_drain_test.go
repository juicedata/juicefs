//go:build linux

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 * Licensed under the Apache License, Version 2.0 (the "License").
 */

package fuse

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A paused state (drain for smooth-upgrade handover) must refuse new
// passthrough opens before touching the server — handing out a backing
// during a handover would strand its data in a staging file the successor
// has no record of.
func TestTryOpenRefusedWhilePaused(t *testing.T) {
	p := &passthroughState{dir: t.TempDir(), files: make(map[uint64]*ptFile), busy: make(map[Ino]int), paused: true}
	// nil server: reaching SupportsPassthrough/checkout would panic, proving
	// the pause gate short-circuits first.
	if id, ok := p.tryOpen(Ino(2), 1, 0x8002 /* O_RDWR|... */, true); ok || id != 0 {
		t.Fatalf("tryOpen while paused = (%d,%v), want (0,false)", id, ok)
	}
	if len(p.busy) != 0 {
		t.Fatalf("paused tryOpen leaked a busy reservation: %v", p.busy)
	}
}

// drain returns true immediately when nothing is in flight and leaves the
// state paused (the caller is about to hand the session over); with a live
// open it must time out, RE-ENABLE passthrough, and return false so the
// caller refuses the handover instead of losing the open's staging data.
func TestDrainForHandover(t *testing.T) {
	p := &passthroughState{dir: t.TempDir(), files: make(map[uint64]*ptFile), busy: make(map[Ino]int)}
	if !p.drain(time.Millisecond) {
		t.Fatalf("drain with no passthrough opens should succeed")
	}
	if !p.paused {
		t.Fatalf("successful drain must leave the state paused for the handover")
	}

	p2 := &passthroughState{dir: t.TempDir(), files: make(map[uint64]*ptFile), busy: map[Ino]int{Ino(7): 1}}
	start := time.Now()
	if p2.drain(50 * time.Millisecond) {
		t.Fatalf("drain with a live passthrough open must fail")
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatalf("drain returned before its deadline")
	}
	if p2.paused {
		t.Fatalf("failed drain must re-enable passthrough (handover was refused)")
	}
}

// truncate must mirror a SETATTR size change onto the inode's live backing:
// the kernel never diverts SETATTR to the backing, so without the mirror the
// staging keeps its old length and the release-time reconcile would undo the
// truncate (and reads through the passthrough fd would see stale bytes).
func TestTruncateMirrorsOntoBacking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pool-1.tmp")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	p := &passthroughState{dir: dir, files: make(map[uint64]*ptFile), busy: make(map[Ino]int)}
	pf := &ptFile{ino: Ino(42), fh: 9, b: &ptBacking{path: path, f: f}}
	p.files[9] = pf

	p.truncate(Ino(42), 4) // shrink
	if st, _ := os.Stat(path); st.Size() != 4 {
		t.Fatalf("backing size after shrink = %d, want 4", st.Size())
	}
	p.truncate(Ino(42), 16) // extend (sparse zeros)
	if st, _ := os.Stat(path); st.Size() != 16 {
		t.Fatalf("backing size after extend = %d, want 16", st.Size())
	}
	// A size change for an inode with no live passthrough open is a no-op.
	p.truncate(Ino(43), 1)
	if st, _ := os.Stat(path); st.Size() != 16 {
		t.Fatalf("truncate of unrelated inode disturbed the backing: %d", st.Size())
	}
}

// fsync on a non-passthrough fh is not handled (the caller proceeds with the
// plain vfs path), including on a nil state.
func TestFsyncPassesThroughUnknownFh(t *testing.T) {
	var nilState *passthroughState
	if handled, _ := nilState.fsync(nil, nil, 1); handled {
		t.Fatalf("nil state must not handle fsync")
	}
	p := &passthroughState{dir: t.TempDir(), files: make(map[uint64]*ptFile), busy: make(map[Ino]int)}
	if handled, _ := p.fsync(nil, nil, 99); handled {
		t.Fatalf("unknown fh must not be handled")
	}
}
