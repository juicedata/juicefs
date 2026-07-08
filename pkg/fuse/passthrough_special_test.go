//go:build linux

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 * Licensed under the Apache License, Version 2.0 (the "License").
 */

package fuse

import (
	"testing"

	"github.com/juicedata/juicefs/pkg/vfs"
)

// tryOpen must refuse passthrough for JuiceFS internal/control inodes: a
// backing file on .control would divert the control protocol (e.g. the
// checkpoint verb) and hang. A nil passthroughState short-circuits before
// any kernel calls, so this exercises the special-node guard in isolation.
func TestTryOpenSkipsSpecialNodes(t *testing.T) {
	var p *passthroughState // nil: guard order still returns (0,false)
	// Sanity: control inode is classified special.
	control := vfs.Ino(0x7FFFFFFF00000002)
	if !vfs.IsSpecialNode(control) {
		t.Fatalf("expected %d to be a special node", control)
	}
	if id, ok := p.tryOpen(control, 1, 0x8002, true); ok || id != 0 {
		t.Fatalf("tryOpen(special) = (%d,%v), want (0,false)", id, ok)
	}
	// A regular inode with a read-only open is also refused (not a write).
	if _, ok := p.tryOpen(vfs.Ino(2), 1, 0, true); ok {
		t.Fatalf("tryOpen(regular, read-only) should be false")
	}
}
