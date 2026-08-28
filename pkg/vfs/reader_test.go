package vfs

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

// blockingStore is a no-op chunk store so fileReader can be opened/closed in
// unit tests without touching real object storage.
type blockingStore struct{}

func (s *blockingStore) NewReader(id uint64, length int) chunk.Reader   { return nil }
func (s *blockingStore) NewWriter(id uint64, tierID uint8) chunk.Writer { return nil }
func (s *blockingStore) Remove(id uint64, length int) error             { return nil }
func (s *blockingStore) FillCache(id uint64, size uint32, parts []chunk.Range) error {
	return nil
}
func (s *blockingStore) EvictCache(id uint64, size uint32, parts []chunk.Range) error {
	return nil
}
func (s *blockingStore) CheckCache(id uint64, size uint32, parts []chunk.Range, handler func(bool, string, int)) error {
	return nil
}
func (s *blockingStore) UsedMemory() int64                  { return 0 }
func (s *blockingStore) UpdateLimit(upload, download int64) {}
func (s *blockingStore) BlobStorage() object.ObjectStorage  { return nil }

func newReaderForCheck(t *testing.T) *dataReader {
	t.Helper()
	metaConf := meta.DefaultConf()
	metaConf.MountPoint = "/jfs"
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
	conf := &Config{
		Meta:   metaConf,
		Format: *format,
		Chunk: &chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			BufferSize: 4 << 20,
			Readahead:  1 << 20,
		},
		FuseOpts: &FuseOptions{},
	}
	return NewDataReader(conf, m, &blockingStore{}).(*dataReader)
}

// TestCheckReadBufferConcurrentMutation reproduces the panic fixed by
// checkReadBuffer: concurrently mutating r.files (via Open/Close) while
// tickReadBuffer snaps and releases idle buffers. The old code iterated
// r.files while temporarily releasing the lock, which panicked with
// "concurrent map iteration and map write". tickReadBuffer must snapshot the
// linked list under the lock and release buffers after unlocking.
func TestCheckReadBufferConcurrentMutation(t *testing.T) {
	dr := newReaderForCheck(t)

	const inodes = 32
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: keep opening and closing readers so r.files keeps changing.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ino := Ino(id*1000 + (id % inodes))
				fr := dr.Open(ino, 1<<20)
				fr.Close(meta.Background())
			}
		}(i)
	}

	// Checker: repeatedly run the fixed scan/release step.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			dr.tickReadBuffer()
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
