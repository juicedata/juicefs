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

package vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/require"
)

const readerRefreshSize = 64 << 10

type readerRefreshMeta struct {
	meta.Meta
	updated atomic.Bool
}

func (m *readerRefreshMeta) Read(ctx meta.Context, ino meta.Ino, index uint32, slices *[]meta.Slice) syscall.Errno {
	id := uint64(1)
	if ino == 10 && m.updated.Load() {
		id = 2
	}
	*slices = []meta.Slice{{Id: id, Size: readerRefreshSize, Len: readerRefreshSize}}
	return 0
}

type readerRefreshStorage struct {
	object.ObjectStorage
	entered chan struct{}
	resume  chan struct{}
	oldGets atomic.Int32
}

func (s *readerRefreshStorage) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	if key == "chunks/0/0/1_0_65536" && s.oldGets.Add(1) == 1 {
		close(s.entered)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

// Inodes 10 and 11 initially share slice 1, as after clone/copy_file_range.
// Updating inode 10's metadata while its old GET is in flight forces VFS to
// refresh the in-flight reader. Inode 11 must retain 'a'.
func TestReaderRefreshKeepsSharedSliceUnchanged(t *testing.T) {
	blob, err := object.CreateStorage("mem", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	oldData := bytes.Repeat([]byte{'a'}, readerRefreshSize)
	newData := bytes.Repeat([]byte{'b'}, readerRefreshSize)
	ctx := context.Background()
	for key, data := range map[string][]byte{
		"chunks/0/0/1_0_65536": oldData,
		"chunks/0/0/2_0_65536": newData,
	} {
		if err := blob.Put(ctx, key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	gate := &readerRefreshStorage{ObjectStorage: blob, entered: make(chan struct{}), resume: make(chan struct{})}
	m := &readerRefreshMeta{}
	conf := &Config{Meta: meta.DefaultConf(), Chunk: &chunk.Config{
		BlockSize: 4 << 20, Compress: "none", MaxUpload: 2, MaxDownload: 16,
		BufferSize: 30 << 20, CacheSize: 1 << 20, CacheDir: "memory", CacheFullBlock: true,
	}}
	store := chunk.NewCachedStore(gate, *conf.Chunk, nil)
	r := &dataReader{m: m, store: store, files: make(map[Ino]*fileReader),
		blockSize: uint64(conf.Chunk.BlockSize), bufferSize: int64(conf.Chunk.BufferSize),
		maxRequests: 1, maxRetries: 3}
	a := r.Open(10, readerRefreshSize)
	b := r.Open(11, readerRefreshSize)
	type result struct {
		n   int
		err syscall.Errno
	}
	gotA := make([]byte, readerRefreshSize)
	done := make(chan result, 1)
	readDone := make(chan struct{})
	t.Cleanup(func() {
		defer store.EvictCache(1, readerRefreshSize, nil)
		defer store.EvictCache(2, readerRefreshSize, nil)
		select {
		case <-gate.resume:
		default:
			close(gate.resume)
		}
		a.Close(meta.Background())
		b.Close(meta.Background())
		awaitReaderTest(t, readDone)
	})
	go func() {
		defer close(readDone)
		n, err := a.Read(meta.Background(), 0, gotA)
		done <- result{n, err}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old slice GET did not start")
	}
	m.updated.Store(true)
	r.Invalidate(10, 0, readerRefreshSize)
	close(gate.resume)
	select {
	case result := <-done:
		if result.err != 0 || result.n != readerRefreshSize || !bytes.Equal(gotA, newData) {
			t.Fatalf("refreshed source: n=%d err=%s matches new data=%v", result.n, result.err, bytes.Equal(gotA, newData))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refreshed source read did not complete")
	}
	gotB := make([]byte, readerRefreshSize)
	n, eno := b.Read(meta.Background(), 0, gotB)
	if eno != 0 || n != readerRefreshSize {
		t.Fatalf("shared file read: n=%d err=%s", n, eno)
	}
	if !bytes.Equal(gotB, oldData) {
		t.Fatalf("shared file expected old 'a', got first byte %q; old object GETs=%d", gotB[0], gate.oldGets.Load())
	}
	if gate.oldGets.Load() != 1 {
		t.Fatalf("expected shared file to use cached old slice, GETs=%d", gate.oldGets.Load())
	}
}

type readerTestMeta struct {
	meta.Meta
	read        func() ([]meta.Slice, syscall.Errno)
	invalidates atomic.Int32
}

func (m *readerTestMeta) Read(_ meta.Context, _ meta.Ino, _ uint32, slices *[]meta.Slice) syscall.Errno {
	var err syscall.Errno
	*slices, err = m.read()
	return err
}

func (m *readerTestMeta) InvalidateChunkCache(meta.Context, meta.Ino, uint32) syscall.Errno {
	m.invalidates.Add(1)
	return 0
}

type readerTestStore struct {
	chunk.ChunkStore
	read func(*chunk.Page, int) (int, error)
}

func (s *readerTestStore) NewReader(uint64, int) chunk.Reader { return s }

func (s *readerTestStore) ReadAt(_ context.Context, p *chunk.Page, off int) (int, error) {
	return s.read(p, off)
}

type readerTestGate struct {
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func newReaderTestGate(t *testing.T) *readerTestGate {
	g := &readerTestGate{entered: make(chan struct{}), resume: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *readerTestGate) block() {
	close(g.entered)
	<-g.resume
}

func (g *readerTestGate) open() { g.once.Do(func() { close(g.resume) }) }

func awaitReaderTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reader")
	}
}

const readerTestSize = 4096

func readerTestSlices() []meta.Slice {
	return []meta.Slice{{Id: 1, Size: readerTestSize, Len: readerTestSize}}
}

func newReaderTestFile(t *testing.T, m *readerTestMeta, store chunk.ChunkStore, length uint64) (*fileReader, func()) {
	t.Helper()
	r := &dataReader{m: m, store: store, files: make(map[Ino]*fileReader),
		blockSize: readerTestSize, bufferSize: 64 << 20, maxRequests: 1, maxRetries: 3}
	f := r.Open(10, length).(*fileReader)
	var once sync.Once
	closeFile := func() { once.Do(func() { f.Close(meta.Background()) }) }
	t.Cleanup(closeFile)
	return f, closeFile
}

func startReaderTestSlice(f *fileReader, off uint64) (*sliceReader, *chunk.Page) {
	f.Lock()
	defer f.Unlock()
	s := f.newSlice(&frange{off, readerTestSize})
	return s, s.page
}

func awaitReaderTestSlice(t *testing.T, f *fileReader, s *sliceReader) {
	t.Helper()
	f.Lock()
	defer f.Unlock()
	for s.state != READY {
		require.Zero(t, f.err)
		if s.cond.WaitWithTimeout(5*time.Second) && s.state != READY {
			t.Fatalf("reader did not become ready: %s", s.state)
		}
	}
}

func TestReaderMetadataRetryKeepsPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		used    bool
		refresh bool
	}{
		{name: "metadata error"},
		{name: "metadata invalidation", refresh: true},
		{name: "metadata error after data read", used: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, retry := newReaderTestGate(t), newReaderTestGate(t)
			var metaCalls, dataCalls atomic.Int32
			failAt := int32(1)
			if tc.used {
				failAt++
			}
			m := &readerTestMeta{read: func() ([]meta.Slice, syscall.Errno) {
				n := metaCalls.Add(1)
				if n == failAt {
					if tc.refresh {
						first.block()
					} else {
						return nil, syscall.EIO
					}
				} else if n == failAt+1 {
					retry.block()
				}
				return readerTestSlices(), 0
			}}
			store := &readerTestStore{read: func(p *chunk.Page, _ int) (int, error) {
				dataCalls.Add(1)
				copy(p.Data, bytes.Repeat([]byte{'a'}, len(p.Data)))
				return len(p.Data), nil
			}}
			f, _ := newReaderTestFile(t, m, store, readerTestSize)
			s, initial := startReaderTestSlice(f, 0)
			if tc.used {
				awaitReaderTestSlice(t, f, s)
				f.Lock()
				s.refs++
				s.invalidate()
				f.Unlock()
				t.Cleanup(func() { f.Lock(); s.refs--; f.Unlock() })
			}
			if tc.refresh {
				awaitReaderTest(t, first.entered)
				f.r.Invalidate(f.inode, 0, readerTestSize)
				first.open()
			}
			awaitReaderTest(t, retry.entered)
			f.Lock()
			beforeData := s.page
			f.Unlock()
			require.Same(t, initial, beforeData, "metadata work must not replace the data page")
			require.Equal(t, failAt-1, dataCalls.Load())
			retry.open()
			awaitReaderTestSlice(t, f, s)
			f.Lock()
			current := s.page
			f.Unlock()
			if tc.used {
				require.NotSame(t, initial, current)
			} else {
				require.Same(t, initial, current, "the first data read must use the initial allocation")
			}
		})
	}
}

func TestReaderDataRetryReplacesPage(t *testing.T) {
	for _, mode := range []string{"error", "partial", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var failed atomic.Bool
			var retained atomic.Pointer[chunk.Page]
			late := newReaderTestGate(t)
			lateDone := make(chan struct{})
			t.Cleanup(func() {
				late.open()
				if mode == "timeout" && failed.Load() {
					awaitReaderTest(t, lateDone)
				}
				if p := retained.Swap(nil); p != nil {
					p.Release()
				}
			})
			m := &readerTestMeta{read: func() ([]meta.Slice, syscall.Errno) { return readerTestSlices(), 0 }}
			store := &readerTestStore{read: func(p *chunk.Page, off int) (int, error) {
				if failed.CompareAndSwap(false, true) {
					copy(p.Data, bytes.Repeat([]byte{'a'}, len(p.Data)))
					p.Acquire() // Stand in for another segment cached before a read failure.
					retained.Store(p)
					if mode == "timeout" {
						p.Acquire() // A backend can keep writing after its timeout returns.
						go func() {
							late.block()
							copy(p.Data, bytes.Repeat([]byte{'c'}, len(p.Data)))
							p.Release()
							close(lateDone)
						}()
						return 0, utils.ErrFuncTimeout
					}
					if mode == "partial" {
						return len(p.Data) / 2, io.ErrUnexpectedEOF
					}
					return 0, io.ErrUnexpectedEOF
				}
				if off > 0 {
					return 0, io.ErrUnexpectedEOF
				}
				copy(p.Data, bytes.Repeat([]byte{'b'}, len(p.Data)))
				return len(p.Data), nil
			}}
			before := readBufferUsed.Load()
			allocated := utils.AllocMemory()
			f, closeFile := newReaderTestFile(t, m, store, readerTestSize)
			s, initial := startReaderTestSlice(f, 0)
			awaitReaderTestSlice(t, f, s)
			f.Lock()
			current := s.page
			f.Unlock()
			require.NotSame(t, initial, current)
			require.Len(t, current.Data, readerTestSize)
			require.Equal(t, readerTestSize, cap(current.Data))
			require.Equal(t, before+readerTestSize, readBufferUsed.Load())
			require.Equal(t, int32(1), m.invalidates.Load())
			require.Equal(t, bytes.Repeat([]byte{'a'}, readerTestSize), retained.Load().Data)
			if mode == "timeout" {
				late.open()
				awaitReaderTest(t, lateDone)
			}
			require.Equal(t, bytes.Repeat([]byte{'b'}, readerTestSize), current.Data)
			closeFile()
			require.Equal(t, before, readBufferUsed.Load())
			retained.Swap(nil).Release()
			require.Eventually(t, func() bool { return utils.AllocMemory() == allocated }, time.Second, time.Millisecond,
				"both pages must be released after their last borrowers")
		})
	}
}

func TestReaderReadyInvalidationWaitsForRefresh(t *testing.T) {
	refresh := newReaderTestGate(t)
	var calls atomic.Int32
	m := &readerTestMeta{read: func() ([]meta.Slice, syscall.Errno) { return readerTestSlices(), 0 }}
	store := &readerTestStore{read: func(p *chunk.Page, _ int) (int, error) {
		value := byte('a')
		call := calls.Add(1)
		if call == 2 {
			refresh.block()
		}
		if call > 1 {
			value = 'b'
		}
		copy(p.Data, bytes.Repeat([]byte{value}, len(p.Data)))
		return len(p.Data), nil
	}}
	f, _ := newReaderTestFile(t, m, store, readerTestSize)
	s, initial := startReaderTestSlice(f, 0)
	awaitReaderTestSlice(t, f, s)
	f.Lock()
	s.refs++
	s.invalidate()
	progress := s.currentPos
	f.Unlock()
	t.Cleanup(func() { f.Lock(); s.refs--; f.Unlock() })
	require.Zero(t, progress, "the old progress must not expose an unfinished refresh")
	awaitReaderTest(t, refresh.entered)
	buf := make([]byte, readerTestSize)
	started, done := make(chan struct{}), make(chan struct{})
	var n int
	var err syscall.Errno
	go func() {
		f.Lock()
		close(started)
		n, err = f.waitForIO(meta.Background(), []*req{{frange{0, readerTestSize}, s}}, buf)
		close(done)
		f.Unlock()
	}()
	awaitReaderTest(t, started)
	f.Lock() // waitForIO must release this lock while waiting for the blocked refresh.
	select {
	case <-done:
		t.Error("waitForIO returned before the refresh completed")
	default:
	}
	f.Unlock()
	refresh.open()
	awaitReaderTest(t, done)
	require.Zero(t, err)
	require.Equal(t, readerTestSize, n)
	require.Equal(t, bytes.Repeat([]byte{'b'}, readerTestSize), buf)
	f.Lock()
	current := s.page
	f.Unlock()
	require.NotSame(t, initial, current)
	used := readBufferUsed.Load()
	f.r.Truncate(f.inode, readerTestSize/2)
	awaitReaderTestSlice(t, f, s)
	f.Lock()
	shortened, size := s.page, s.block.len
	f.Unlock()
	require.NotSame(t, current, shortened)
	require.Equal(t, uint64(readerTestSize/2), size)
	require.Len(t, shortened.Data, readerTestSize)
	require.Equal(t, readerTestSize, cap(shortened.Data))
	require.Equal(t, used, readBufferUsed.Load())
	f.r.Truncate(f.inode, 0)
	awaitReaderTestSlice(t, f, s)
	f.Lock()
	empty, size, progress := s.page, s.block.len, s.currentPos
	f.Unlock()
	require.Same(t, shortened, empty, "a zero-length refresh must not allocate another page")
	require.Zero(t, size)
	require.Zero(t, progress)
	require.Equal(t, int32(3), calls.Load())
}

func TestReaderCloseReleasesBorrowedPage(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprint(refresh), func(t *testing.T) {
			ioGate := newReaderTestGate(t)
			var retained atomic.Pointer[chunk.Page]
			done := make(chan struct{})
			t.Cleanup(func() {
				ioGate.open()
				awaitReaderTest(t, done)
				if p := retained.Swap(nil); p != nil {
					p.Release()
				}
			})
			m := &readerTestMeta{read: func() ([]meta.Slice, syscall.Errno) { return readerTestSlices(), 0 }}
			store := &readerTestStore{read: func(p *chunk.Page, _ int) (int, error) {
				p.Acquire()
				retained.Store(p)
				ioGate.block()
				copy(p.Data, bytes.Repeat([]byte{'a'}, len(p.Data)))
				return len(p.Data), nil
			}}
			before := readBufferUsed.Load()
			allocated := utils.AllocMemory()
			f, closeFile := newReaderTestFile(t, m, store, readerTestSize)
			go func() {
				_, _ = f.Read(meta.Background(), 0, make([]byte, readerTestSize))
				close(done)
			}()
			awaitReaderTest(t, ioGate.entered)
			f.Lock()
			initial := f.slices.page
			f.Unlock()
			if refresh {
				f.r.Invalidate(f.inode, 0, readerTestSize)
			}
			closeFile()
			ioGate.open()
			awaitReaderTest(t, done)
			require.NotNil(t, initial.Data, "the outstanding borrower must retain the closed reader's page")
			retained.Swap(nil).Release()
			require.Eventually(t, func() bool { return utils.AllocMemory() == allocated }, time.Second, time.Millisecond)
			require.Equal(t, before, readBufferUsed.Load())
			f.r.Lock()
			files := len(f.r.files)
			f.r.Unlock()
			require.Zero(t, files)
		})
	}
}

func TestReaderTruncateBeforeDataRead(t *testing.T) {
	for _, length := range []uint64{0, readerTestSize} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			metadata := newReaderTestGate(t)
			var metaCalls, dataCalls atomic.Int32
			m := &readerTestMeta{read: func() ([]meta.Slice, syscall.Errno) {
				if metaCalls.Add(1) == 1 {
					metadata.block()
				}
				return readerTestSlices(), 0
			}}
			store := &readerTestStore{read: func(p *chunk.Page, _ int) (int, error) {
				dataCalls.Add(1)
				return len(p.Data), nil
			}}
			before := readBufferUsed.Load()
			f, closeFile := newReaderTestFile(t, m, store, 2*readerTestSize)
			s, initial := startReaderTestSlice(f, readerTestSize)
			awaitReaderTest(t, metadata.entered)
			f.r.Truncate(f.inode, length)
			metadata.open()
			awaitReaderTestSlice(t, f, s)
			f.Lock()
			current, size, progress := s.page, s.block.len, s.currentPos
			f.Unlock()
			require.Same(t, initial, current)
			require.Zero(t, size)
			require.Zero(t, progress)
			require.Zero(t, dataCalls.Load())
			closeFile()
			require.Nil(t, initial.Data)
			require.Equal(t, before, readBufferUsed.Load())
		})
	}
}
