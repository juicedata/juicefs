package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juju/ratelimit"
)

// FetchStats counts blocks by source. FromCache means the block was already
// on disk with the expected size and the fetch was skipped.
type FetchStats struct {
	FromPeers   atomic.Int64
	FromStorage atomic.Int64
	FromCache   atomic.Int64
	Failed      atomic.Int64
	BytesPeer   atomic.Int64
	BytesStore  atomic.Int64
}

// Fetcher retrieves blocks from the best available source (cache > peer >
// object storage) and writes them to the local disk cache.
type Fetcher struct {
	storage      object.ObjectStorage
	cacheDir     string
	blockSize    int
	httpClient   *http.Client
	availability *AvailabilityTracker
	stats        FetchStats
	downLimit    *ratelimit.Bucket   // object storage download throttle; nil = unlimited
	compressor   compress.Compressor // decompresses storage payloads; noOp for uncompressed volumes
	// True for noOp compressor: return io.ReadAll's buffer directly
	// instead of decompressing into a fresh allocation.
	skipDecompress bool
}

// NewFetcher constructs a Fetcher. compressor must match the volume's
// format.Compression so FetchFromStorage produces the layout mount expects
// on disk; nil is treated as noOp for uncompressed volumes.
func NewFetcher(storage object.ObjectStorage, cacheDir string, blockSize int, client *http.Client, compressor compress.Compressor) *Fetcher {
	if client == nil {
		client = http.DefaultClient
	}
	if compressor == nil {
		compressor = compress.NewCompressor("")
	}
	return &Fetcher{
		storage:        storage,
		cacheDir:       cacheDir,
		blockSize:      blockSize,
		httpClient:     client,
		compressor:     compressor,
		skipDecompress: compressor.Name() == "Noop",
	}
}

// SetAvailability wires the tracker used by FetchBlock to find peers.
func (f *Fetcher) SetAvailability(at *AvailabilityTracker) {
	f.availability = at
}

// SetDownloadLimit throttles object-storage reads to the bucket's fill rate.
// Pass nil to disable.
func (f *Fetcher) SetDownloadLimit(b *ratelimit.Bucket) {
	f.downLimit = b
}

// FetchFromPeer issues GET http://{peerAddr}/block/{key} and returns the
// response body. The caller is responsible for validating length/integrity.
func (f *Fetcher) FetchFromPeer(ctx context.Context, peerAddr, key string) ([]byte, error) {
	url := fmt.Sprintf("http://%s/block/%s", peerAddr, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer fetch %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("peer fetch %s: read body: %w", url, err)
	}
	return data, nil
}

// FetchFromStorage downloads key and decompresses to the logical size mount
// expects on disk. The download limiter accounts raw network bytes
// (compressed when applicable), not the decompressed output.
func (f *Fetcher) FetchFromStorage(ctx context.Context, key string, size int) ([]byte, error) {
	rc, err := f.storage.Get(ctx, key, 0, -1)
	if err != nil {
		return nil, fmt.Errorf("storage get %q: %w", key, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("storage read %q: %w", key, err)
	}
	if f.downLimit != nil && len(raw) > 0 {
		f.downLimit.Wait(int64(len(raw)))
	}

	if f.skipDecompress {
		if len(raw) != size {
			return nil, fmt.Errorf("storage object %q size %d != expected logical size %d (corrupt or truncated)", key, len(raw), size)
		}
		return raw, nil
	}

	out := make([]byte, size)
	n, err := f.compressor.Decompress(out, raw)
	if err != nil {
		return nil, fmt.Errorf("decompress %q: %w", key, err)
	}
	if n != size {
		return nil, fmt.Errorf("decompress %q: produced %d bytes, expected %d (corrupt or truncated object)", key, n, size)
	}
	return out, nil
}

// CacheBlock writes data to {cacheDir}/raw/{key}, creating parent directories
// as needed.
func (f *Fetcher) CacheBlock(key string, data []byte) error {
	path := filepath.Join(f.cacheDir, "raw", key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write block %q: %w", path, err)
	}
	return nil
}

// FetchBlock fetches a block (cache > peer > storage) and writes it to the
// disk cache. fromPeer is true when the data came from a peer.
func (f *Fetcher) FetchBlock(ctx context.Context, block *Block) (fromPeer bool, err error) {
	// Cache-hit fast path: skip fetch if file exists on disk with expected size.
	cachePath := filepath.Join(f.cacheDir, "raw", block.Key)
	if fi, statErr := os.Stat(cachePath); statErr == nil && fi.Size() == int64(block.Size) {
		f.stats.FromCache.Add(1)
		return false, nil
	}

	var data []byte

	// Attempt peer download if we have an availability tracker.
	if f.availability != nil {
		if peerAddr := f.availability.FindPeerWith(block.Key); peerAddr != "" {
			data, err = f.FetchFromPeer(ctx, peerAddr, block.Key)
			if err == nil {
				fromPeer = true
			}
			// On peer failure fall through to storage.
		}
	}

	if !fromPeer {
		data, err = f.FetchFromStorage(ctx, block.Key, block.Size)
		if err != nil {
			return false, err
		}
	}

	// Write to local disk cache.
	if cacheErr := f.CacheBlock(block.Key, data); cacheErr != nil {
		return fromPeer, cacheErr
	}

	// Update counters.
	n := int64(len(data))
	if fromPeer {
		f.stats.FromPeers.Add(1)
		f.stats.BytesPeer.Add(n)
	} else {
		f.stats.FromStorage.Add(1)
		f.stats.BytesStore.Add(n)
	}
	return fromPeer, nil
}

// MaxBlockAttempts bounds retries for blocks that fail in non-NoSuchKey ways
// (NoSuchKey is short-circuited by isStorageNotFound). 5 absorbs transient
// peer/HTTP failures — worst case ~25s with the 5s peer timeout — while
// guaranteeing the warmup terminates.
const MaxBlockAttempts = 5

// isStorageNotFound reports whether the object is missing from storage.
// Permanent — the worker abandons immediately rather than burning retries.
// String-matching mirrors pkg/chunk/cached_store.go since backends share no
// typed "not found" error.
func isStorageNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "NoSuchKey") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "No such file")
}

// RunWorkers spawns numWorkers goroutines that pop blocks, claim them via
// CAS Pending→Downloading, and fetch. On success: MarkDone + MarkLocal. On
// failure: re-push until MaxBlockAttempts, then MarkFailed so the warmup can
// complete even with unfetchable blocks. The returned WaitGroup signals when
// the queue is closed and drained.
func (f *Fetcher) RunWorkers(ctx context.Context, queue *FetchQueue, tracker *AvailabilityTracker, numWorkers int) *sync.WaitGroup {
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				block := queue.WaitAndPop()
				if block == nil {
					// Queue closed and empty.
					return
				}

				// Claim the block atomically.
				if !block.TryDownload() {
					// Another worker already claimed it.
					continue
				}

				_, err := f.FetchBlock(ctx, block)
				if err != nil {
					if isStorageNotFound(err) {
						// Permanent: object is gone (e.g. JuiceFS slice
						// compaction during warmup). Don't burn retries.
						block.MarkFailed()
						f.stats.Failed.Add(1)
						logger.Warnf("block %q abandoned: not found in object storage (likely deleted by compaction): %v", block.Key, err)
						continue
					}
					attempts := block.IncrAttempts()
					if attempts >= MaxBlockAttempts {
						block.MarkFailed()
						f.stats.Failed.Add(1)
						logger.Warnf("block %q abandoned after %d attempts: %v", block.Key, attempts, err)
						continue
					}
					block.ResetToPending()
					queue.Push(block)
					continue
				}

				block.MarkDone()
				if tracker != nil {
					tracker.MarkLocal(block.Key)
				}
			}
		}()
	}
	return &wg
}

// pollResponse is the JSON structure returned by a peer's /available endpoint.
type pollResponse struct {
	Blocks    []string `json:"blocks"`
	UpdatedAt int64    `json:"updated_at"`
}

// PollAvailability queries the peer's /available?since={sinceMs}, updates the
// tracker for peerAddr, and returns the response's updated_at timestamp.
func (f *Fetcher) PollAvailability(ctx context.Context, peerAddr string, sinceMs int64) (int64, error) {
	url := fmt.Sprintf("http://%s/available?since=%d", peerAddr, sinceMs)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("poll availability %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("poll availability %s: status %d", url, resp.StatusCode)
	}

	var pr pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, fmt.Errorf("poll availability %s: decode: %w", url, err)
	}

	if f.availability != nil && len(pr.Blocks) > 0 {
		f.availability.UpdateRemote(peerAddr, pr.Blocks)
	}
	return pr.UpdatedAt, nil
}

// Stats returns a snapshot of the current fetch statistics.
func (f *Fetcher) Stats() (fromPeers, fromStorage, fromCache, failed, bytesPeer, bytesStorage int64) {
	return f.stats.FromPeers.Load(),
		f.stats.FromStorage.Load(),
		f.stats.FromCache.Load(),
		f.stats.Failed.Load(),
		f.stats.BytesPeer.Load(),
		f.stats.BytesStore.Load()
}
