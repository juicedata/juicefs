package meta

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"
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

type summaryTestEngine struct {
	engine
	readdir func(Context, Ino, *[]*Entry) syscall.Errno
}

func (e summaryTestEngine) doGetAttr(_ Context, _ Ino, attr *Attr) syscall.Errno {
	attr.Typ = TypeDirectory
	return 0
}

func (e summaryTestEngine) doReaddir(ctx Context, ino Ino, _ uint8, entries *[]*Entry, _ int) syscall.Errno {
	return e.readdir(ctx, ino, entries)
}

func newSummaryTestMeta(t *testing.T, readdir func(Context, Ino, *[]*Entry) syscall.Errno) *baseMeta {
	t.Helper()
	m := newBaseMeta("", testConfig())
	t.Cleanup(m.of.close)
	m.setFormat(testFormat())
	m.en = summaryTestEngine{readdir: readdir}
	return m
}

func TestGetSummaryChildCancellation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		childErr, want syscall.Errno
	}{
		{"child error", syscall.EACCES, syscall.EACCES},
		{"missing child", syscall.ENOENT, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const branch, sibling, failing = Ino(2), Ino(3), Ino(4)
				caller := Background()
				defer caller.Cancel()
				started := make(chan struct{})
				release := make(chan struct{})
				var siblingCanceled bool
				m := newSummaryTestMeta(t, func(ctx Context, ino Ino, entries *[]*Entry) syscall.Errno {
					switch ino {
					case RootInode:
						*entries = []*Entry{{Inode: branch, Attr: &Attr{Typ: TypeDirectory}}, {Inode: sibling, Attr: &Attr{Typ: TypeDirectory}}}
					case branch:
						*entries = []*Entry{{Inode: failing, Attr: &Attr{Typ: TypeDirectory}}}
					case failing:
						<-started
						return tc.childErr
					case sibling:
						close(started)
						select {
						case <-ctx.Done():
							siblingCanceled = true
							<-release
							return syscall.EINTR
						case <-release:
						}
					}
					return 0
				})

				result := make(chan syscall.Errno, 1)
				go func() { result <- m.GetSummary(caller, RootInode, &Summary{}, true, true) }()
				synctest.Wait()
				if siblingCanceled != (tc.want != 0) {
					t.Errorf("sibling canceled = %t, want %t", siblingCanceled, tc.want != 0)
				}
				select {
				case st := <-result:
					close(release)
					t.Fatalf("GetSummary returned %v before sibling exited", st)
				default:
				}
				close(release)
				if st := <-result; st != tc.want {
					t.Errorf("GetSummary returned %v, want %v", st, tc.want)
				}
				if caller.Canceled() {
					t.Error("GetSummary canceled caller context")
				}
			})
		})
	}
}

func TestGetSummaryCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		want       syscall.Errno
	}{
		{"cancel", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancelCause(context.Background())
			return ctx, func() { cancel(syscall.EPERM) }
		}, syscall.EINTR},
		{"timeout", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, syscall.ETIMEDOUT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := tc.newContext()
				defer cancel()
				caller := WrapWithCancel(parent, 0, 0, []uint32{0})
				defer caller.Cancel()
				m := newSummaryTestMeta(t, func(ctx Context, ino Ino, entries *[]*Entry) syscall.Errno {
					if ino == RootInode {
						*entries = []*Entry{{Inode: 2, Attr: &Attr{Typ: TypeDirectory}}}
						return 0
					}
					if tc.want == syscall.EINTR {
						cancel()
					}
					<-ctx.Done()
					return syscall.EACCES
				})
				if st := m.GetSummary(caller, RootInode, &Summary{}, true, true); st != tc.want {
					t.Errorf("GetSummary returned %v, want %v", st, tc.want)
				}
			})
		})
	}
}

type summaryPollingContext struct {
	Context
	interrupted bool
}

func (ctx *summaryPollingContext) Err() error     { return syscall.EINTR }
func (ctx *summaryPollingContext) Canceled() bool { return ctx.interrupted }

func TestGetSummaryPollingCancellation(t *testing.T) {
	caller := &summaryPollingContext{Context: newFuseDefaultCtx(100, 200)}
	m := newSummaryTestMeta(t, func(ctx Context, _ Ino, _ *[]*Entry) syscall.Errno {
		if ctx.Canceled() || ctx.Err() != nil {
			t.Error("scan context canceled before caller interruption")
		}
		caller.interrupted = true
		if !ctx.Canceled() {
			t.Error("scan context missed caller interruption")
		}
		select {
		case <-ctx.Done():
		default:
			t.Error("scan context Done is still open")
		}
		return 0
	})
	if st := m.GetSummary(caller, RootInode, &Summary{}, true, true); st != syscall.EINTR {
		t.Fatalf("GetSummary returned %v after caller interruption, want EINTR", st)
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

func TestWrapContextEmptyGids(t *testing.T) {
	for name, ctx := range map[string]Context{
		"NewContext nil":          NewContext(1, 100, nil),
		"WrapWithCancel empty":    WrapWithCancel(context.Background(), 1, 100, []uint32{}),
		"WrapWithoutCancel empty": WrapWithoutCancel(context.Background(), 1, 100, nil),
	} {
		t.Run(name, func(t *testing.T) {
			defer ctx.Cancel()
			if got := ctx.Gid(); got != 0 {
				t.Fatalf("Gid() = %d, want 0", got)
			}
		})
	}
}
