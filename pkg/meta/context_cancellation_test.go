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
	readdir    func(Context, Ino, *[]*Entry) syscall.Errno
	getDirStat func(Context, Ino) (*dirStat, syscall.Errno)
}

func (e summaryTestEngine) doGetAttr(_ Context, _ Ino, attr *Attr) syscall.Errno {
	attr.Typ = TypeDirectory
	attr.Nlink = 3
	return 0
}

func (e summaryTestEngine) doGetDirStat(ctx Context, ino Ino, _ bool) (*dirStat, syscall.Errno) {
	if e.getDirStat != nil {
		return e.getDirStat(ctx, ino)
	}
	return &dirStat{}, 0
}

func (e summaryTestEngine) doReaddir(ctx Context, ino Ino, _ uint8, entries *[]*Entry, _ int) syscall.Errno {
	return e.readdir(ctx, ino, entries)
}

func TestGetSummaryChildCancellation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		strict        bool
		childErr      syscall.Errno
		cancelCaller  bool
		timeoutCaller bool
		want          syscall.Errno
	}{
		{name: "child error strict", strict: true, childErr: syscall.EIO, want: syscall.EIO},
		{name: "child error dir stats", strict: false, childErr: syscall.EIO, want: syscall.EIO},
		{name: "caller cancellation", strict: true, childErr: syscall.EIO, cancelCaller: true, want: syscall.EINTR},
		{name: "caller timeout", strict: true, childErr: syscall.EIO, timeoutCaller: true, want: syscall.ETIMEDOUT},
		{name: "missing child", strict: true, childErr: syscall.ENOENT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newBaseMeta("", testConfig())
			defer m.of.close()
			m.setFormat(testFormat())
			synctest.Test(t, func(t *testing.T) {
				var caller context.Context
				var cancelCause context.CancelCauseFunc
				if tc.timeoutCaller {
					var cancel context.CancelFunc
					caller, cancel = context.WithTimeout(context.Background(), time.Second)
					defer cancel()
				} else {
					caller, cancelCause = context.WithCancelCause(context.Background())
					defer cancelCause(nil)
				}
				ctx := WrapWithCancel(caller, 0, 0, []uint32{0})
				siblingStarted := make(chan struct{})
				finishSibling := make(chan struct{})
				var siblingCanceled bool
				handleNode := func(ctx Context, ino Ino) (bool, syscall.Errno) {
					switch ino {
					case 3:
						close(siblingStarted)
						select {
						case <-ctx.Done():
							siblingCanceled = true
							<-finishSibling
							return true, syscall.EINTR
						case <-finishSibling:
							return true, 0
						}
					case 4:
						<-siblingStarted
						if tc.cancelCaller || tc.timeoutCaller {
							<-ctx.Done()
						}
						return true, tc.childErr
					default:
						return false, 0
					}
				}
				m.en = summaryTestEngine{
					getDirStat: func(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
						if !tc.strict {
							if handled, st := handleNode(ctx, ino); handled {
								if st != 0 {
									return nil, st
								}
								return &dirStat{}, 0
							}
						}
						return &dirStat{}, 0
					},
					readdir: func(ctx Context, ino Ino, entries *[]*Entry) syscall.Errno {
						switch ino {
						case RootInode:
							*entries = []*Entry{{Inode: 2, Attr: &Attr{Typ: TypeDirectory}}, {Inode: 3, Attr: &Attr{Typ: TypeDirectory}}}
							return 0
						case 2:
							*entries = []*Entry{{Inode: 4, Attr: &Attr{Typ: TypeDirectory}}}
							return 0
						}
						if tc.strict {
							if handled, st := handleNode(ctx, ino); handled {
								return st
							}
						}
						return 0
					},
				}

				result := make(chan syscall.Errno, 1)
				go func() { result <- m.GetSummary(ctx, RootInode, &Summary{}, true, tc.strict) }()
				synctest.Wait()
				if tc.cancelCaller {
					cancelCause(syscall.EPERM)
					synctest.Wait()
				} else if tc.timeoutCaller {
					time.Sleep(time.Second)
					synctest.Wait()
				}
				if want := tc.want != 0; siblingCanceled != want {
					t.Errorf("sibling canceled = %t, want %t", siblingCanceled, want)
				}
				select {
				case st := <-result:
					close(finishSibling)
					t.Fatalf("GetSummary returned %v before sibling exited", st)
				default:
				}
				close(finishSibling)
				if st := <-result; st != tc.want {
					t.Errorf("GetSummary returned %v, want %v", st, tc.want)
				}
				wantCallerCanceled := tc.cancelCaller || tc.timeoutCaller
				if ctx.Canceled() != wantCallerCanceled {
					t.Errorf("caller canceled = %t, want %t", ctx.Canceled(), wantCallerCanceled)
				}
			})
		})
	}
}

type summaryPollingContext struct {
	Context
	interrupted chan struct{}
}

func (ctx summaryPollingContext) Err() error { return syscall.EINTR }

func (ctx summaryPollingContext) Canceled() bool {
	select {
	case <-ctx.interrupted:
		return true
	default:
		return false
	}
}

func TestGetSummaryPollingCancellation(t *testing.T) {
	m := newBaseMeta("", testConfig())
	defer m.of.close()
	m.setFormat(testFormat())
	key := CtxKey("summary-test")
	caller := summaryPollingContext{
		Context:     newFuseDefaultCtx(100, 200).WithValue(key, true),
		interrupted: make(chan struct{}),
	}
	m.en = summaryTestEngine{readdir: func(ctx Context, _ Ino, _ *[]*Entry) syscall.Errno {
		if ctx.Uid() != caller.Uid() || ctx.Gid() != caller.Gid() || ctx.CheckPermission() || ctx.Value(key) != true {
			t.Error("scan context lost caller identity, permissions, or value")
		}
		if ctx.Canceled() || ctx.Err() != nil {
			t.Error("scan context canceled before caller interruption")
		}
		close(caller.interrupted)
		if !ctx.Canceled() {
			t.Error("scan context missed caller interruption")
		}
		return 0
	}}
	if st := m.GetSummary(caller, RootInode, &Summary{}, true, true); st != syscall.EINTR {
		t.Fatalf("GetSummary returned %v after caller interruption, want EINTR", st)
	}
}

func TestGetSummaryModes(t *testing.T) {
	m, err := newKVMeta("memkv", "jfs-summary-modes", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatal(err)
	}
	createSummaryTestTree(t, m, RootInode, 2, 3)
	m.FlushSession()

	for _, strict := range []bool{true, false} {
		for _, recursive := range []bool{true, false} {
			t.Run(fmt.Sprintf("strict=%t/recursive=%t", strict, recursive), func(t *testing.T) {
				want := Summary{Dirs: 3, Size: 3 * 4096}
				if recursive {
					want.Files = 6
					want.Size += 6 * 4096
				}
				var got Summary
				if st := m.GetSummary(Background(), RootInode, &got, recursive, strict); st != 0 || got != want {
					t.Fatalf("GetSummary returned %+v (%v), want %+v", got, st, want)
				}
			})
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
