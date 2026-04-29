package meta

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func createSummaryTestTree(t *testing.T, m Meta, parent Ino, dirs, filesPerDir int) {
	t.Helper()
	ctx := Background()
	for i := 0; i < dirs; i++ {
		name := fmt.Sprintf("dir-%d", i)
		var inode Ino
		var attr Attr
		if st := m.Mkdir(ctx, parent, name, 0755, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("mkdir %s: %s", name, st)
		}
		for j := 0; j < filesPerDir; j++ {
			fname := fmt.Sprintf("file-%d", j)
			var fileInode Ino
			if st := m.Create(ctx, inode, fname, 0644, 0, 0, &fileInode, &attr); st != 0 && st != syscall.EEXIST {
				t.Fatalf("create %s/%s: %s", name, fname, st)
			}
		}
	}
}

func TestGetTreeSummaryCanceledByProgressCallback(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-cancel-summary", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}

	ctx := NewContext(1, 0, []uint32{0})
	createSummaryTestTree(t, m, RootInode, 50, 20)

	tree := &TreeSummary{Inode: RootInode}
	updates := 0
	st := m.GetTreeSummary(ctx, tree, 8, 10, true, func(count uint64, bytes uint64) {
		updates++
		if updates >= 10 {
			ctx.Cancel()
		}
	})
	if st != syscall.EINTR {
		t.Fatalf("expected EINTR, got %s", st)
	}
}

func TestKVTxnReturnsEINTRWhenContextAlreadyCanceled(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-cancel-txn", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}
	km, ok := m.(*kvMeta)
	if !ok {
		t.Fatalf("unexpected meta type: %T", m)
	}

	ctx := NewContext(1, 0, []uint32{0})
	ctx.Cancel()
	err = km.txn(ctx, func(tx *kvTxn) error {
		return nil
	})
	if err != syscall.EINTR {
		t.Fatalf("expected EINTR, got %v", err)
	}
}

func TestCleanupTrashBeforeReturnsEINTRWhenContextAlreadyCanceled(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-cancel-trash", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}
	bm, ok := m.(*kvMeta)
	if !ok {
		t.Fatalf("unexpected meta type: %T", m)
	}

	ctx := NewContext(1, 0, []uint32{0})
	ctx.Cancel()
	st := bm.CleanupTrashBefore(ctx, time.Now(), nil, nil)
	if st != syscall.EINTR && st != 0 {
		t.Fatalf("expected EINTR or 0 for empty trash fast path, got %s", st)
	}
}

func TestCleanupDelayedSlicesReturnsCanceledWhenContextCanceled(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-cancel-delayed", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}
	km, ok := m.(*kvMeta)
	if !ok {
		t.Fatalf("unexpected meta type: %T", m)
	}

	now := time.Now().Unix()
	err = km.txn(Background(), func(tx *kvTxn) error {
		for i := 0; i < 128; i++ {
			tx.set(km.delSliceKey(now-1, uint64(i+1)), []byte{1})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prepare delayed slices: %v", err)
	}

	ctx := NewContext(1, 0, []uint32{0})
	ctx.Cancel()
	_, err = km.doCleanupDelayedSlices(ctx, now+1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRemoveEmptyDirReturnsEINTRWhenCanceled(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-cancel-emptydir", testConfig())
	if err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}

	ctx := Background()
	var root Ino
	var attr Attr
	if st := m.Mkdir(ctx, RootInode, "test-dir", 0755, 0, 0, &root, &attr); st != 0 {
		t.Fatalf("mkdir test-dir: %s", st)
	}

	var count uint64
	cctx := NewContext(1, 0, []uint32{0})
	cctx.Cancel()
	st := m.Remove(cctx, RootInode, "test-dir", true, 16, &count)
	if st != syscall.EINTR {
		t.Fatalf("expected EINTR, got %s", st)
	}
}

func TestBadgerKVTxnReturnsEINTRWhenContextAlreadyCanceled(t *testing.T) {
	metaURL := "badger://" + filepath.Join(t.TempDir(), "jfs-cancel-tkv-badger")
	m := NewClient(metaURL, testConfig())
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("init format: %v", err)
	}
	km, ok := m.(*kvMeta)
	if !ok {
		t.Fatalf("unexpected meta type: %T", m)
	}

	ctx := NewContext(1, 0, []uint32{0})
	ctx.Cancel()
	err := km.txn(ctx, func(tx *kvTxn) error { return nil })
	if err != syscall.EINTR {
		t.Fatalf("expected EINTR, got %v", err)
	}
}
