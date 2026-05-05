package vfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

type blockingChunkReader struct {
	once     sync.Once
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (r *blockingChunkReader) ReadAt(ctx context.Context, p *chunk.Page, off int) (int, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-ctx.Done():
		close(r.canceled)
		return 0, ctx.Err()
	case <-r.release:
		copy(p.Data, []byte("data"))
		return len(p.Data), nil
	}
}

type blockingChunkStore struct {
	reader *blockingChunkReader
}

func (s *blockingChunkStore) NewReader(id uint64, length int) chunk.Reader   { return s.reader }
func (s *blockingChunkStore) NewWriter(id uint64, tierID uint8) chunk.Writer { return nil }
func (s *blockingChunkStore) Remove(id uint64, length int) error             { return nil }
func (s *blockingChunkStore) FillCache(id uint64, length uint32) error       { return nil }
func (s *blockingChunkStore) EvictCache(id uint64, length uint32) error      { return nil }
func (s *blockingChunkStore) CheckCache(id uint64, length uint32, handler func(bool, string, int)) error {
	return nil
}
func (s *blockingChunkStore) UsedMemory() int64                  { return 0 }
func (s *blockingChunkStore) UpdateLimit(upload, download int64) {}
func (s *blockingChunkStore) BlobStorage() object.ObjectStorage  { return nil }

func createCancellationTestReader(t *testing.T, store chunk.ChunkStore) (*dataReader, Ino) {
	t.Helper()
	mp := "/jfs"
	metaConf := meta.DefaultConf()
	metaConf.MountPoint = mp
	m := meta.NewClient("memkv://", metaConf)
	format := &meta.Format{
		Name:        "test-" + uuid.New().String(),
		UUID:        uuid.New().String(),
		Storage:     "mem",
		BlockSize:   4096,
		Compression: "none",
		DirStats:    true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %v", err)
	}

	ctx := meta.Background()
	var inode meta.Ino
	var attr meta.Attr
	if st := m.Create(ctx, meta.RootInode, "file", 0644, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("create file: %s", st)
	}
	var sliceID uint64
	if st := m.NewSlice(ctx, &sliceID); st != 0 {
		t.Fatalf("new slice: %s", st)
	}
	if st := m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceID, Size: 4, Len: 4}, time.Now()); st != 0 {
		t.Fatalf("write slice: %s", st)
	}

	conf := &Config{
		Meta: metaConf,
		Format: meta.Format{
			Name:      format.Name,
			UUID:      format.UUID,
			Storage:   format.Storage,
			BlockSize: format.BlockSize,
		},
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			BufferSize: 4 << 20,
			Readahead:  1 << 20,
		},
		FuseOpts: &FuseOptions{},
	}
	return NewDataReader(conf, m, store).(*dataReader), Ino(inode)
}

func TestFileReaderCloseCancelsOngoingRead(t *testing.T) {
	reader := &blockingChunkReader{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	store := &blockingChunkStore{reader: reader}
	dr, inode := createCancellationTestReader(t, store)
	fr := dr.Open(inode, 4)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4)
		_, _ = fr.Read(meta.Background(), 0, buf)
	}()

	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read to start")
	}

	fr.Close(meta.Background())

	select {
	case <-reader.canceled:
		t.Fatal("unexpected cancellation: close should not cancel ongoing BUSY read immediately")
	case <-time.After(200 * time.Millisecond):
	}
	close(reader.release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read goroutine to exit")
	}
}

func TestDataReaderInvalidateShouldCancelOngoingRead(t *testing.T) {
	reader := &blockingChunkReader{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	store := &blockingChunkStore{reader: reader}
	dr, inode := createCancellationTestReader(t, store)
	fr := dr.Open(inode, 4)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4)
		_, _ = fr.Read(meta.Background(), 0, buf)
	}()

	<-reader.started
	dr.Invalidate(inode, 0, 4)

	select {
	case <-reader.canceled:
		t.Fatal("unexpected cancellation: invalidate should not cancel ongoing BUSY read immediately")
	case <-time.After(200 * time.Millisecond):
	}
	close(reader.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read goroutine to exit")
	}
}

func decodeControlOutput(data []byte) ([]byte, syscall.Errno) {
	for p := 0; p < len(data); {
		if len(data)-p == 1 {
			return nil, syscall.Errno(data[p])
		}
		if len(data)-p >= 17 && data[p] == meta.CPROGRESS {
			p += 17
			continue
		}
		if len(data)-p >= 5 && data[p] == meta.CDATA {
			sz := binary.BigEndian.Uint32(data[p+1 : p+5])
			if p+5+int(sz) > len(data) {
				return nil, syscall.EIO
			}
			return data[p+5 : p+5+int(sz)], 0
		}
		return nil, syscall.EIO
	}
	return nil, syscall.EIO
}

func runInternalControlWithCancel(t *testing.T, v *VFS, cmd uint32, payload []byte) ([]byte, syscall.Errno) {
	t.Helper()
	ctx := meta.NewContext(10, 1, []uint32{1})
	out := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		v.handleInternalMsg(ctx, cmd, utils.FromBuffer(payload), out)
		close(done)
	}()
	ctx.Cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for internal control handler to finish")
	}
	return decodeControlOutput(out.Bytes())
}

func buildTestTreeForControlCancel(t *testing.T, v *VFS, ctx Context, parent Ino, dirs, filesPerDir int) {
	t.Helper()
	for i := 0; i < dirs; i++ {
		dname := fmt.Sprintf("sum-%03d", i)
		de, st := v.Mkdir(ctx, parent, dname, 0755, 0)
		if st != 0 {
			t.Fatalf("mkdir %s: %s", dname, st)
		}
		for j := 0; j < filesPerDir; j++ {
			fname := fmt.Sprintf("f-%03d", j)
			fe, fh, st := v.Create(ctx, de.Inode, fname, 0644, 0, syscall.O_RDWR)
			if st != 0 {
				t.Fatalf("create %s/%s: %s", dname, fname, st)
			}
			_ = v.Flush(ctx, fe.Inode, fh, 0)
			v.Release(ctx, fe.Inode, fh)
		}
	}
}

func TestControlInfoV2Cancellation(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	buildTestTreeForControlCancel(t, v, ctx, 1, 2, 5)
	payload := make([]byte, 8+1+1+1)
	w := utils.FromBuffer(payload)
	w.Put64(1)
	w.Put8(1)
	w.Put8(0)
	w.Put8(1)

	data, eno := runInternalControlWithCancel(t, v, meta.InfoV2, w.Bytes())
	if eno == syscall.EINTR {
		return
	}
	if eno != 0 {
		t.Fatalf("info v2 returned unexpected errno: %s", eno)
	}

	var resp InfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal info response: %v", err)
	}
	if !resp.Failed {
		t.Fatalf("expected failed response when canceled, got success")
	}
	if resp.Reason == "" {
		t.Fatalf("expected non-empty failure reason")
	}
}

func TestControlSummaryCancellation(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.Background())
	buildTestTreeForControlCancel(t, v, ctx, 1, 2, 5)
	payload := make([]byte, 8+1+1+1)
	w := utils.FromBuffer(payload)
	w.Put64(1)
	w.Put8(5)
	w.Put8(10)
	w.Put8(1)

	data, eno := runInternalControlWithCancel(t, v, meta.OpSummary, w.Bytes())
	if eno == syscall.EINTR {
		return
	}
	if eno != 0 {
		t.Fatalf("summary returned unexpected errno: %s", eno)
	}

	var resp SummaryReponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal summary response: %v", err)
	}
	if resp.Errno != syscall.EINTR {
		t.Fatalf("expected EINTR in summary response, got %s", resp.Errno)
	}
}
