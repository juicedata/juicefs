package meta

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
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

type summaryCancelTestEngine struct {
	engine
	failInode  Ino
	waitInode  Ino
	failErr    syscall.Errno
	started    chan struct{}
	canceled   chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
	active     atomic.Int32
}

func (e *summaryCancelTestEngine) waitForSibling(ctx Context, inode Ino) bool {
	switch inode {
	case e.failInode:
		select {
		case <-e.started:
			return true
		case <-time.After(time.Second):
			return false
		}
	case e.waitInode:
		e.startOnce.Do(func() { close(e.started) })
		select {
		case <-ctx.Done():
			e.cancelOnce.Do(func() { close(e.canceled) })
			return true
		case <-time.After(time.Second):
			return false
		}
	default:
		return false
	}
}

func (e *summaryCancelTestEngine) doReaddir(ctx Context, inode Ino, plus uint8, entries *[]*Entry, limit int) syscall.Errno {
	if inode == e.failInode || inode == e.waitInode {
		e.active.Add(1)
		defer e.active.Add(-1)
	}
	if e.waitForSibling(ctx, inode) {
		if inode == e.failInode {
			return e.failure()
		}
		return syscall.EINTR
	}
	return e.engine.doReaddir(ctx, inode, plus, entries, limit)
}

func (e *summaryCancelTestEngine) doGetDirStat(ctx Context, inode Ino, trySync bool) (*dirStat, syscall.Errno) {
	if inode == e.failInode || inode == e.waitInode {
		e.active.Add(1)
		defer e.active.Add(-1)
	}
	if e.waitForSibling(ctx, inode) {
		if inode == e.failInode {
			return nil, e.failure()
		}
		return nil, syscall.EINTR
	}
	return e.engine.doGetDirStat(ctx, inode, trySync)
}

func (e *summaryCancelTestEngine) failure() syscall.Errno {
	if e.failErr == 0 {
		return syscall.EIO
	}
	return e.failErr
}

func TestGetSummaryCancelsSiblingsOnChildError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		strict bool
	}{
		{name: "strict", strict: true},
		{name: "dir stats", strict: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMemKV(t, "getsummary-cancel-"+tc.name, testFormat())
			ctx := NewContext(1, 0, []uint32{0})
			defer ctx.Cancel()

			var failInode, waitInode Ino
			for _, name := range []string{"fail", "wait"} {
				var inode Ino
				var attr Attr
				if st := m.Mkdir(Background(), RootInode, name, 0755, 0, 0, &inode, &attr); st != 0 {
					t.Fatalf("mkdir %s: %s", name, st)
				}
				if name == "fail" {
					failInode = inode
				} else {
					waitInode = inode
				}
			}

			base := m.getBase()
			wrapped := &summaryCancelTestEngine{
				engine:    base.en,
				failInode: failInode,
				waitInode: waitInode,
				started:   make(chan struct{}),
				canceled:  make(chan struct{}),
			}
			base.en = wrapped
			defer func() { base.en = wrapped.engine }()

			var summary Summary
			if st := m.GetSummary(ctx, RootInode, &summary, true, tc.strict); st != syscall.EIO {
				t.Fatalf("GetSummary returned %s, want %s", st, syscall.EIO)
			}
			select {
			case <-wrapped.canceled:
			default:
				t.Fatal("sibling scan did not observe cancellation")
			}
			if ctx.Err() != nil {
				t.Fatalf("caller context was canceled: %v", ctx.Err())
			}
			if active := wrapped.active.Load(); active != 0 {
				t.Fatalf("%d sibling scans still active after GetSummary returned", active)
			}
		})
	}
}

func TestGetSummaryScanStateStopsDispatchAfterError(t *testing.T) {
	parent := Background()
	defer parent.Cancel()
	scanCtx := WrapWithCancel(parent, parent.Pid(), parent.Uid(), parent.Gids())
	defer scanCtx.Cancel()
	scan := &summaryScanState{ctx: scanCtx, parent: parent}

	if !scan.begin() {
		t.Fatal("scan should allow work before an error")
	}
	started := 0
	if !scan.start(func() { started++ }) {
		t.Fatal("scan should dispatch work before an error")
	}
	if got := scan.fail(syscall.EIO); got != syscall.EIO {
		t.Fatalf("recorded error = %s, want %s", got, syscall.EIO)
	}
	if scan.begin() {
		t.Fatal("scan allowed synchronous work after an error")
	}
	if scan.start(func() { started++ }) {
		t.Fatal("scan allowed asynchronous work after an error")
	}
	if started != 1 {
		t.Fatalf("dispatched work count = %d, want 1", started)
	}
}

func TestGetSummaryCallerCancellation(t *testing.T) {
	m := newTestMemKV(t, "getsummary-caller-cancel", testFormat())
	var dirInode Ino
	var attr Attr
	if st := m.Mkdir(Background(), RootInode, "wait", 0755, 0, 0, &dirInode, &attr); st != 0 {
		t.Fatalf("mkdir wait: %s", st)
	}

	base := m.getBase()
	wrapped := &summaryCancelTestEngine{
		engine:    base.en,
		waitInode: dirInode,
		started:   make(chan struct{}),
		canceled:  make(chan struct{}),
	}
	base.en = wrapped
	defer func() { base.en = wrapped.engine }()

	ctx := NewContext(1, 0, []uint32{0})
	defer ctx.Cancel()
	result := make(chan syscall.Errno, 1)
	go func() {
		var summary Summary
		result <- m.GetSummary(ctx, RootInode, &summary, true, true)
	}()
	select {
	case <-wrapped.started:
	case <-time.After(time.Second):
		t.Fatal("child scan did not start")
	}
	ctx.Cancel()
	select {
	case st := <-result:
		if st != syscall.EINTR {
			t.Fatalf("GetSummary returned %s, want %s", st, syscall.EINTR)
		}
	case <-time.After(time.Second):
		t.Fatal("GetSummary did not return after caller cancellation")
	}
	select {
	case <-wrapped.canceled:
	default:
		t.Fatal("child scan did not observe caller cancellation")
	}
	if active := wrapped.active.Load(); active != 0 {
		t.Fatalf("%d child scans still active after GetSummary returned", active)
	}
}

func TestGetSummaryIgnoresENOENTFromChild(t *testing.T) {
	m := newTestMemKV(t, "getsummary-child-enoent", testFormat())
	var failInode, waitInode Ino
	for _, name := range []string{"fail", "wait"} {
		var inode Ino
		var attr Attr
		if st := m.Mkdir(Background(), RootInode, name, 0755, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("mkdir %s: %s", name, st)
		}
		if name == "fail" {
			failInode = inode
		} else {
			waitInode = inode
		}
	}

	base := m.getBase()
	wrapped := &summaryCancelTestEngine{
		engine:    base.en,
		failInode: failInode,
		waitInode: waitInode,
		failErr:   syscall.ENOENT,
		started:   make(chan struct{}),
		canceled:  make(chan struct{}),
	}
	base.en = wrapped
	defer func() { base.en = wrapped.engine }()

	ctx := NewContext(1, 0, []uint32{0})
	defer ctx.Cancel()
	var summary Summary
	if st := m.GetSummary(ctx, RootInode, &summary, true, true); st != 0 {
		t.Fatalf("GetSummary returned %s, want success", st)
	}
	select {
	case <-wrapped.canceled:
		t.Fatal("ENOENT canceled a sibling scan")
	default:
	}
	if ctx.Err() != nil {
		t.Fatalf("caller context was canceled: %v", ctx.Err())
	}
	if summary.Dirs != 3 {
		t.Fatalf("summary dirs = %d, want 3", summary.Dirs)
	}
}

func TestGetSummaryModesKeepExistingResults(t *testing.T) {
	m := newTestMemKV(t, "getsummary-modes", testFormat())
	createSummaryTestTree(t, m, RootInode, 2, 1)
	ctx := Background()

	// DirStats has existing size semantics that can differ from strict traversal.
	for _, tc := range []struct {
		name      string
		recursive bool
		strict    bool
		want      Summary
	}{
		{name: "non-recursive strict", recursive: false, strict: true, want: Summary{Size: 3 * 4096, Dirs: 3}},
		{name: "non-recursive dir stats", recursive: false, strict: false, want: Summary{Size: 5 * 4096, Dirs: 3}},
		{name: "recursive strict", recursive: true, strict: true, want: Summary{Size: 5 * 4096, Files: 2, Dirs: 3}},
		{name: "recursive dir stats", recursive: true, strict: false, want: Summary{Size: 7 * 4096, Files: 2, Dirs: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var summary Summary
			if st := m.GetSummary(ctx, RootInode, &summary, tc.recursive, tc.strict); st != 0 {
				t.Fatalf("GetSummary returned %s", st)
			}
			if summary != tc.want {
				t.Fatalf("GetSummary = %+v, want %+v", summary, tc.want)
			}
		})
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
	st := m.GetTreeSummary(ctx, tree, 8, 10, true, SortBySize, func(count uint64, bytes uint64) {
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
