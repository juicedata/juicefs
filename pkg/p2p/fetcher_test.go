package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juju/ratelimit"
)

// trackingStorage is a test double for object.ObjectStorage that records Get
// calls and can serve predefined block data. Methods not overridden fall
// through to DefaultObjectStorage.
type trackingStorage struct {
	object.DefaultObjectStorage
	mu       sync.Mutex
	data     map[string][]byte
	getCalls atomic.Int32
}

func newTrackingStorage() *trackingStorage {
	return &trackingStorage{data: make(map[string][]byte)}
}

func (s *trackingStorage) String() string { return "tracking" }

func (s *trackingStorage) Get(_ context.Context, key string, off, limit int64, _ ...object.AttrGetter) (io.ReadCloser, error) {
	s.getCalls.Add(1)
	s.mu.Lock()
	data, ok := s.data[key]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *trackingStorage) Put(_ context.Context, key string, in io.Reader, _ ...object.AttrGetter) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.data[key] = data
	s.mu.Unlock()
	return nil
}

func (s *trackingStorage) Delete(_ context.Context, key string, _ ...object.AttrGetter) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}

// Head explicit override — DefaultObjectStorage.Head still uses the legacy
// signature so the embedded version does not satisfy the interface.
func (s *trackingStorage) Head(_ context.Context, _ string) (object.Object, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestFetcher_FetchFromPeer(t *testing.T) {
	wantData := []byte("block-data-from-peer")

	// Mock peer HTTP server returning fixed block data.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/block/chunks/0/0/100_0_4194304" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wantData)
	}))
	defer peer.Close()

	f := NewFetcher(nil, t.TempDir(), 4194304, peer.Client(), nil)

	// peer.Listener.Addr().String() gives "127.0.0.1:<port>" — strip the scheme
	// that httptest adds so we can build the URL ourselves.
	peerAddr := peer.Listener.Addr().String()

	// Override the HTTP client to use the test server's transport but speak to
	// the real address (the server uses http://, so we just pass the addr).
	got, err := f.FetchFromPeer(context.Background(), peerAddr, "chunks/0/0/100_0_4194304", len(wantData))
	if err != nil {
		t.Fatalf("FetchFromPeer: unexpected error: %v", err)
	}
	if string(got) != string(wantData) {
		t.Errorf("FetchFromPeer: got %q, want %q", got, wantData)
	}
}

// TestFetcher_FetchFromPeer_RejectsOversizedBody confirms a peer cannot
// drown the fetcher in memory by sending a body bigger than the largest
// layout mount accepts for the requested logical size.
func TestFetcher_FetchFromPeer_RejectsOversizedBody(t *testing.T) {
	const logical = 100
	// One byte past the largest accepted layout (logical + checksum + tier).
	checksumLen := ((logical-1)/(32<<10) + 1) * 4
	tooBig := make([]byte, logical+checksumLen+int(cacheTierIDLength)+1)

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(tooBig)
	}))
	defer peer.Close()

	f := NewFetcher(nil, t.TempDir(), 4194304, peer.Client(), nil)
	_, err := f.FetchFromPeer(context.Background(), peer.Listener.Addr().String(), "chunks/0/0/k_0_100", logical)
	if err == nil {
		t.Fatal("expected error on oversized body")
	}
}

// TestFetcher_FetchFromPeer_RejectsInvalidLength confirms a body whose
// length is not one of the four valid cache layouts is rejected, so the
// caller falls through to storage rather than caching bytes mount would
// later refuse to open.
func TestFetcher_FetchFromPeer_RejectsInvalidLength(t *testing.T) {
	const logical = 100
	// logical + 3: not 0, not tier(1), not checksum(4), not checksum+tier(5).
	body := make([]byte, logical+3)

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer peer.Close()

	f := NewFetcher(nil, t.TempDir(), 4194304, peer.Client(), nil)
	_, err := f.FetchFromPeer(context.Background(), peer.Listener.Addr().String(), "chunks/0/0/k_0_100", logical)
	if err == nil {
		t.Fatal("expected error on body whose length is not a valid cache layout")
	}
}

// TestFetcher_FetchFromPeer_AcceptsValidLayouts confirms each of the four
// layouts mount accepts (logical / +tier / +checksum / +checksum+tier)
// passes through unchanged.
func TestFetcher_FetchFromPeer_AcceptsValidLayouts(t *testing.T) {
	const logical = 100
	checksumLen := ((logical-1)/(32<<10) + 1) * 4
	cases := []struct {
		name string
		n    int
	}{
		{"logical only", logical},
		{"logical + tier", logical + 1},
		{"logical + checksum", logical + checksumLen},
		{"logical + checksum + tier", logical + checksumLen + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := make([]byte, c.n)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(body)
			}))
			defer peer.Close()

			f := NewFetcher(nil, t.TempDir(), 4194304, peer.Client(), nil)
			got, err := f.FetchFromPeer(context.Background(), peer.Listener.Addr().String(), "chunks/0/0/k_0_100", logical)
			if err != nil {
				t.Fatalf("FetchFromPeer: %v", err)
			}
			if len(got) != c.n {
				t.Errorf("got %d bytes, want %d", len(got), c.n)
			}
		})
	}
}

func TestFetcher_FetchFromStorage_DecompressesLz4(t *testing.T) {
	// Storage holds lz4-compressed bytes (matching how JuiceFS mount writes
	// them when format.Compression="lz4"). Fetcher must decompress so the
	// returned payload matches the logical block layout mount expects.
	logical := []byte("the quick brown fox jumps over the lazy dog repeatedly")
	c := compress.NewCompressor("lz4")
	if c == nil {
		t.Fatal("lz4 compressor unavailable")
	}
	buf := make([]byte, c.CompressBound(len(logical)))
	n, err := c.Compress(buf, logical)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	compressed := buf[:n]

	storage := newTrackingStorage()
	storage.data["k"] = compressed

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, c)
	got, err := f.FetchFromStorage(context.Background(), "k", len(logical))
	if err != nil {
		t.Fatalf("FetchFromStorage: %v", err)
	}
	if !bytes.Equal(got, logical) {
		t.Errorf("got %q, want %q", got, logical)
	}
}

func TestFetcher_FetchFromStorage_NoOpPassthrough(t *testing.T) {
	// Empty Compress → noOp compressor → bytes returned verbatim.
	logical := []byte("uncompressed-payload")
	storage := newTrackingStorage()
	storage.data["k"] = logical

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, compress.NewCompressor(""))
	got, err := f.FetchFromStorage(context.Background(), "k", len(logical))
	if err != nil {
		t.Fatalf("FetchFromStorage: %v", err)
	}
	if !bytes.Equal(got, logical) {
		t.Errorf("got %q, want %q", got, logical)
	}
}

func TestFetcher_FetchFromStorage_NoOpRejectsWrongSize(t *testing.T) {
	// Uncompressed volume but storage object size disagrees with the
	// expected logical size — corrupt or truncated. The zero-copy fast-path
	// must surface this rather than silently caching wrong-sized bytes.
	storage := newTrackingStorage()
	storage.data["short"] = make([]byte, 50)
	storage.data["long"] = make([]byte, 200)

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, compress.NewCompressor(""))

	if _, err := f.FetchFromStorage(context.Background(), "short", 100); err == nil {
		t.Error("expected error when storage object is shorter than expected")
	}
	if _, err := f.FetchFromStorage(context.Background(), "long", 100); err == nil {
		t.Error("expected error when storage object is longer than expected")
	}
}

func TestFetcher_FetchFromStorage_RejectsWrongDecompressedSize(t *testing.T) {
	// Storage object decompresses to fewer bytes than expected — corrupt or
	// truncated. Fetcher must surface an error rather than silently writing
	// a short cache file (which mount would later reject).
	logical := []byte("only-50-bytes-of-actual-data!!!!!!!!!!!!!!!!!!!!!!")
	c := compress.NewCompressor("lz4")
	if c == nil {
		t.Fatal("lz4 compressor unavailable")
	}
	buf := make([]byte, c.CompressBound(len(logical)))
	n, err := c.Compress(buf, logical)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	storage := newTrackingStorage()
	storage.data["k"] = buf[:n]

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, c)
	_, err = f.FetchFromStorage(context.Background(), "k", len(logical)+1)
	if err == nil {
		t.Fatal("expected error when decompressed size mismatches expected logical size")
	}
}

// TestFetcher_DownloadLimit_ThrottlesStorageReads exercises the bandwidth cap
// path: with an 8 KB/s bucket and 1 KB burst, an 8 KB read must wait roughly
// (8KB-1KB)/8KB ≈ 875ms after the read for tokens to refill.
func TestFetcher_DownloadLimit_ThrottlesStorageReads(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["k"] = make([]byte, 8192)

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, nil)
	f.SetDownloadLimit(ratelimit.NewBucketWithRate(8192, 1024))

	start := time.Now()
	if _, err := f.FetchFromStorage(context.Background(), "k", 8192); err != nil {
		t.Fatalf("FetchFromStorage: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("expected throttling to delay read by ~875ms, got %v", elapsed)
	}
}

// TestFetcher_DownloadLimit_NilIsUnlimited makes sure a nil limiter does not
// introduce any wait or panic.
func TestFetcher_DownloadLimit_NilIsUnlimited(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["k"] = make([]byte, 8192)

	f := NewFetcher(storage, t.TempDir(), 4194304, nil, nil)
	f.SetDownloadLimit(nil)

	start := time.Now()
	if _, err := f.FetchFromStorage(context.Background(), "k", 8192); err != nil {
		t.Fatalf("FetchFromStorage: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("nil limiter should not delay; elapsed=%v", elapsed)
	}
}

func TestFetcher_CacheBlock(t *testing.T) {
	cacheDir := t.TempDir()
	f := NewFetcher(nil, cacheDir, 4194304, http.DefaultClient, nil)

	key := "chunks/0/0/42_0_1024"
	data := []byte("cached-block-content")

	if err := f.CacheBlock(key, data); err != nil {
		t.Fatalf("CacheBlock: unexpected error: %v", err)
	}

	// Verify the file exists at the expected path.
	path := filepath.Join(cacheDir, "raw", key)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %q: %v", path, err)
	}
	if string(onDisk) != string(data) {
		t.Errorf("on-disk content: got %q, want %q", onDisk, data)
	}
}

// writeRawBlock writes data to {cacheDir}/raw/{key} for tests that need a
// pre-existing cached block. Mirrors the helper in integration_test.go but is
// kept here to keep each test file self-contained.
func writeRawBlock(t *testing.T, cacheDir, key string, data []byte) {
	t.Helper()
	path := filepath.Join(cacheDir, "raw", key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile %q: %v", path, err)
	}
}

func TestFetcher_FetchBlock_SkipsExistingWithMatchingSize(t *testing.T) {
	cacheDir := t.TempDir()
	key := "chunks/0/0/42_0_22"
	data := []byte("already-cached-content") // 22 bytes
	writeRawBlock(t, cacheDir, key, data)

	storage := newTrackingStorage()
	f := NewFetcher(storage, cacheDir, 4*1024*1024, http.DefaultClient, nil)

	block := &Block{Key: key, Size: len(data)}
	fromPeer, written, err := f.FetchBlock(context.Background(), block)
	if err != nil {
		t.Fatalf("FetchBlock: unexpected error: %v", err)
	}
	if fromPeer {
		t.Error("FetchBlock: fromPeer should be false on cache hit")
	}
	if !written {
		t.Error("FetchBlock: written should be true on cache hit")
	}
	if got := storage.getCalls.Load(); got != 0 {
		t.Errorf("storage.Get called %d times on cache hit, want 0", got)
	}
	if got := f.stats.FromCache.Load(); got != 1 {
		t.Errorf("FromCache = %d, want 1", got)
	}
	if got := f.stats.FromStorage.Load(); got != 0 {
		t.Errorf("FromStorage = %d, want 0", got)
	}
	if got := f.stats.FromPeers.Load(); got != 0 {
		t.Errorf("FromPeers = %d, want 0", got)
	}
}

// TestFetcher_FetchBlock_CacheHitForTrailerLayouts confirms the fast-path
// recognises files mount may have written with a checksum and/or tier
// trailer — without this, p2p-warmup re-downloads every block on a peer
// that runs `--verify-cache-checksum=extend` or multi-tier caching.
func TestFetcher_FetchBlock_CacheHitForTrailerLayouts(t *testing.T) {
	cases := []struct {
		name    string
		logical int
		onDisk  int // total bytes to write
	}{
		{"logical only", 100, 100},
		{"logical + tier", 100, 100 + 1},
		{"logical + checksum (1 csBlock)", 100, 100 + 4},
		{"logical + checksum + tier", 100, 100 + 4 + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			key := "chunks/0/0/42_0_" + strconv.Itoa(c.logical)
			writeRawBlock(t, cacheDir, key, make([]byte, c.onDisk))

			storage := newTrackingStorage()
			f := NewFetcher(storage, cacheDir, 4*1024*1024, http.DefaultClient, nil)

			block := &Block{Key: key, Size: c.logical}
			if _, _, err := f.FetchBlock(context.Background(), block); err != nil {
				t.Fatalf("FetchBlock: %v", err)
			}
			if got := storage.getCalls.Load(); got != 0 {
				t.Errorf("storage.Get called %d times on cache hit, want 0", got)
			}
			if got := f.stats.FromCache.Load(); got != 1 {
				t.Errorf("FromCache = %d, want 1", got)
			}
		})
	}
}

func TestFetcher_FetchBlock_RefetchesWhenSizeMismatches(t *testing.T) {
	cacheDir := t.TempDir()
	key := "chunks/0/0/42_0_24"
	stale := []byte("wrong-size")                // 10 bytes
	fresh := []byte("correct-size-content-ok!!") // 25 bytes
	writeRawBlock(t, cacheDir, key, stale)

	storage := newTrackingStorage()
	storage.data[key] = fresh
	f := NewFetcher(storage, cacheDir, 4*1024*1024, http.DefaultClient, nil)

	block := &Block{Key: key, Size: len(fresh)}
	if _, _, err := f.FetchBlock(context.Background(), block); err != nil {
		t.Fatalf("FetchBlock: unexpected error: %v", err)
	}
	if got := storage.getCalls.Load(); got != 1 {
		t.Errorf("storage.Get called %d times, want 1", got)
	}
	onDisk, err := os.ReadFile(filepath.Join(cacheDir, "raw", key))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if !bytes.Equal(onDisk, fresh) {
		t.Errorf("cache content: got %q, want %q", onDisk, fresh)
	}
	if got := f.stats.FromCache.Load(); got != 0 {
		t.Errorf("FromCache = %d, want 0 on size mismatch", got)
	}
}

func TestFetcher_PollPeerAvailability(t *testing.T) {
	// Mock peer returning a JSON availability response.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/available" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"blocks":     []string{"key1", "key2"},
			"updated_at": int64(1000),
		})
	}))
	defer peer.Close()

	tracker := NewAvailabilityTracker()
	f := NewFetcher(nil, t.TempDir(), 4194304, peer.Client(), nil)
	f.SetAvailability(tracker)

	peerAddr := peer.Listener.Addr().String()
	updatedAt, err := f.PollAvailability(context.Background(), peerAddr, 0)
	if err != nil {
		t.Fatalf("PollAvailability: unexpected error: %v", err)
	}
	if updatedAt != 1000 {
		t.Errorf("PollAvailability: updatedAt = %d, want 1000", updatedAt)
	}

	// Verify the tracker was updated with the two keys.
	if !tracker.PeerHas(peerAddr, "key1") {
		t.Errorf("tracker does not have key1 for peer %s", peerAddr)
	}
	if !tracker.PeerHas(peerAddr, "key2") {
		t.Errorf("tracker does not have key2 for peer %s", peerAddr)
	}
}

func TestRunWorkers_AbandonsImmediatelyOnNotFound(t *testing.T) {
	// Storage returns "not found" (the canonical permanent failure). Worker
	// must abandon the block on the FIRST attempt — retrying won't bring
	// the object back, and burning the full retry budget would just delay
	// completion of an entire warmup that should fail-fast on this block.
	storage := newTrackingStorage() // empty → Get returns "not found: <key>"

	f := NewFetcher(storage, t.TempDir(), 4194304, http.DefaultClient, nil)
	tracker := NewAvailabilityTracker()
	f.SetAvailability(tracker)

	block := &Block{Key: "missing-block", Size: 100}
	queue := NewFetchQueue(1)
	queue.PushAll([]*Block{block})

	wg := f.RunWorkers(context.Background(), queue, tracker, 1)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if block.IsTerminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("block did not reach terminal state in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if block.State() != BlockFailed {
		t.Errorf("block state = %d, want BlockFailed", block.State())
	}
	// attempts is incremented only for transient failures; not-found
	// short-circuits before IncrAttempts.
	if got := block.attempts.Load(); got != 0 {
		t.Errorf("attempts = %d, want 0 (not-found should not increment retry counter)", got)
	}
	if got := storage.getCalls.Load(); got != 1 {
		t.Errorf("storage.Get calls = %d, want 1 (no retries on not-found)", got)
	}

	queue.Close()
	wg.Wait()

	_, _, _, _, failed, _, _ := f.Stats()
	if failed != 1 {
		t.Errorf("Failed counter = %d, want 1", failed)
	}
}

func TestRunWorkers_AbandonsAfterMaxAttempts(t *testing.T) {
	// Storage returns a transient (non-not-found) error every time. Worker
	// retries up to MaxBlockAttempts before abandoning. Distinct from the
	// not-found path because transient errors might recover.
	var attempts atomic.Int32
	storage := &flakyStorage{
		fail:     1 << 30, // never succeeds
		attempts: &attempts,
	}

	f := NewFetcher(storage, t.TempDir(), 4194304, http.DefaultClient, nil)
	tracker := NewAvailabilityTracker()
	f.SetAvailability(tracker)

	block := &Block{Key: "transient-fail-block", Size: 100}
	queue := NewFetchQueue(1)
	queue.PushAll([]*Block{block})

	wg := f.RunWorkers(context.Background(), queue, tracker, 1)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if block.IsTerminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("block did not reach terminal state in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if block.State() != BlockFailed {
		t.Errorf("block state = %d, want BlockFailed", block.State())
	}
	if got := block.attempts.Load(); got != MaxBlockAttempts {
		t.Errorf("attempts = %d, want %d", got, MaxBlockAttempts)
	}

	queue.Close()
	wg.Wait()
}

func TestRunWorkers_TransientFailureRetries(t *testing.T) {
	// Storage fails the first 2 times then succeeds. Block should retry and
	// finish in BlockDone, not BlockFailed.
	var attempts atomic.Int32
	storage := &flakyStorage{
		fail:     2, // first 2 attempts fail
		attempts: &attempts,
		data:     []byte("hello"),
	}

	f := NewFetcher(storage, t.TempDir(), 4194304, http.DefaultClient, nil)
	tracker := NewAvailabilityTracker()
	f.SetAvailability(tracker)

	block := &Block{Key: "flaky-block", Size: 5}
	queue := NewFetchQueue(1)
	queue.PushAll([]*Block{block})

	wg := f.RunWorkers(context.Background(), queue, tracker, 1)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if block.IsTerminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("block did not reach terminal state in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if block.State() != BlockDone {
		t.Errorf("block state = %d, want BlockDone", block.State())
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("Get attempts = %d, want 3 (2 fail + 1 success)", got)
	}

	queue.Close()
	wg.Wait()
}

// flakyStorage fails the first `fail` Gets, then succeeds with `data`.
type flakyStorage struct {
	object.DefaultObjectStorage
	fail     int32
	attempts *atomic.Int32
	data     []byte
}

func (s *flakyStorage) String() string { return "flaky" }

func (s *flakyStorage) Get(_ context.Context, key string, off, limit int64, _ ...object.AttrGetter) (io.ReadCloser, error) {
	n := s.attempts.Add(1)
	if n <= s.fail {
		return nil, fmt.Errorf("flaky failure %d", n)
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *flakyStorage) Put(_ context.Context, key string, in io.Reader, _ ...object.AttrGetter) error {
	return nil
}

func (s *flakyStorage) Delete(_ context.Context, key string, _ ...object.AttrGetter) error {
	return nil
}

func (s *flakyStorage) Head(_ context.Context, _ string) (object.Object, error) {
	return nil, fmt.Errorf("not implemented")
}

// TestFetcher_FetchBlock_CacheHitBypassesCacheSizeCap asserts that the
// cache-hit fast path runs before the cap check. A block already on disk
// costs zero new bytes, so guarding it would force a phantom skip on every
// worker pass over an already-warmed block — defeating warmup completion
// when the cap is set tight relative to existing cache.
func TestFetcher_FetchBlock_CacheHitBypassesCacheSizeCap(t *testing.T) {
	cacheDir := t.TempDir()
	key := "chunks/0/0/42_0_22"
	data := []byte("already-cached-content") // 22 bytes
	writeRawBlock(t, cacheDir, key, data)

	storage := newTrackingStorage()
	f := NewFetcher(storage, cacheDir, 4*1024*1024, http.DefaultClient, nil)
	f.SetCacheSize(1) // intentionally tighter than data size

	block := &Block{Key: key, Size: len(data)}
	_, written, err := f.FetchBlock(context.Background(), block)
	if err != nil {
		t.Fatalf("FetchBlock: unexpected error: %v", err)
	}
	if !written {
		t.Error("written = false on cache hit, want true")
	}
	if got := f.stats.FromCache.Load(); got != 1 {
		t.Errorf("FromCache = %d, want 1", got)
	}
	if got := f.stats.SkippedFull.Load(); got != 0 {
		t.Errorf("SkippedFull = %d, want 0 when fast-path satisfied", got)
	}
}

// TestRunWorkers_SkippedBlocksDoNotAnnounce asserts that blocks skipped by
// the cache-size cap are marked terminal (warmup completes) but are NOT
// announced via MarkLocal — announcing would make peers fetch from a node
// that does not actually hold the block, returning 404 and burning a round
// trip before they fall back to storage.
func TestRunWorkers_SkippedBlocksDoNotAnnounce(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["skip-me"] = []byte("payload")

	f := NewFetcher(storage, t.TempDir(), 4194304, http.DefaultClient, nil)
	tracker := NewAvailabilityTracker()
	f.SetAvailability(tracker)
	f.SetCacheSize(1) // any 7-byte block exceeds the cap

	block := &Block{Key: "skip-me", Size: 7}
	queue := NewFetchQueue(1)
	queue.PushAll([]*Block{block})

	wg := f.RunWorkers(context.Background(), queue, tracker, 1)

	deadline := time.Now().Add(2 * time.Second)
	for !block.IsTerminal() {
		if time.Now().After(deadline) {
			t.Fatalf("block did not reach terminal state in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	queue.Close()
	wg.Wait()

	if block.State() != BlockDone {
		t.Errorf("block state = %d, want BlockDone (skipped blocks complete the warmup)", block.State())
	}
	if got := tracker.LocalDoneCount(); got != 0 {
		t.Errorf("LocalDoneCount = %d, want 0 (skipped blocks must not be announced)", got)
	}
	if got := f.stats.SkippedFull.Load(); got != 1 {
		t.Errorf("SkippedFull = %d, want 1", got)
	}
}

// TestFetcher_FetchBlock_CacheSizeCapStopsWritesAfterLimit verifies that
// once cumulative successful writes reach the configured cacheSize, further
// blocks are skipped without I/O. The cap is the *only* protection against
// the cache subtree growing unbounded — without it, a user pointing warmup
// at a multi-TB dataset has no way to opt into a smaller working set.
func TestFetcher_FetchBlock_CacheSizeCapStopsWritesAfterLimit(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["a"] = make([]byte, 60)
	storage.data["b"] = make([]byte, 60)

	f := NewFetcher(storage, t.TempDir(), 4*1024*1024, http.DefaultClient, nil)
	f.SetCacheSize(100) // first 60-byte block fits, second pushes past 100

	for _, key := range []string{"a", "b"} {
		_, _, err := f.FetchBlock(context.Background(), &Block{Key: key, Size: 60})
		if err != nil {
			t.Fatalf("FetchBlock %q: %v", key, err)
		}
	}

	if got := f.stats.FromStorage.Load(); got != 1 {
		t.Errorf("FromStorage = %d, want 1 (only the first block fits)", got)
	}
	if got := f.stats.SkippedFull.Load(); got != 1 {
		t.Errorf("SkippedFull = %d, want 1 (second block exceeds cap)", got)
	}
}

// TestFetcher_FetchBlock_CacheSizeZeroIsUnlimited locks in the contract that
// zero (or unset) cacheSize disables the cap. A regression here would silently
// brick existing deployments that rely on the unbounded default.
func TestFetcher_FetchBlock_CacheSizeZeroIsUnlimited(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["big"] = make([]byte, 1<<20) // 1 MiB

	f := NewFetcher(storage, t.TempDir(), 4*1024*1024, http.DefaultClient, nil)
	// SetCacheSize NOT called — defaults to 0 (unlimited).

	_, written, err := f.FetchBlock(context.Background(), &Block{Key: "big", Size: 1 << 20})
	if err != nil {
		t.Fatalf("FetchBlock: %v", err)
	}
	if !written {
		t.Error("written=false despite unlimited cap")
	}
	if got := f.stats.SkippedFull.Load(); got != 0 {
		t.Errorf("SkippedFull = %d, want 0 with cacheSize=0", got)
	}
}

// TestFetcher_AddCacheUsed_SeedsCounter exercises the path where the warmup
// scans pre-existing cache files at startup and pre-populates the byte
// counter so the cap accounts for files this run did not write. Without
// seeding, a run reusing a near-full cache from a prior session would let
// the warmup write up to (cacheSize) more bytes on top — doubling actual
// usage.
func TestFetcher_AddCacheUsed_SeedsCounter(t *testing.T) {
	storage := newTrackingStorage()
	storage.data["new"] = make([]byte, 20)

	f := NewFetcher(storage, t.TempDir(), 4*1024*1024, http.DefaultClient, nil)
	f.SetCacheSize(100)
	f.AddCacheUsed(95) // simulate scan finding 95 bytes pre-cached

	// 95 + 20 = 115 > 100 → must skip.
	_, written, err := f.FetchBlock(context.Background(), &Block{Key: "new", Size: 20})
	if err != nil {
		t.Fatalf("FetchBlock: %v", err)
	}
	if written {
		t.Error("written=true despite seeded counter exceeding cap")
	}
	if got := f.stats.SkippedFull.Load(); got != 1 {
		t.Errorf("SkippedFull = %d, want 1", got)
	}
}

func TestIsStorageNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"S3-style NoSuchKey", fmt.Errorf("s3 GET failed: NoSuchKey: The specified key does not exist"), true},
		{"generic not found", fmt.Errorf("storage get %q: not found: foo", "k"), true},
		{"file backend No such file", fmt.Errorf("open /var/x: No such file or directory"), true},
		{"timeout (transient)", fmt.Errorf("read tcp 127.0.0.1:80: i/o timeout"), false},
		{"connection refused (transient)", fmt.Errorf("dial tcp: connect: connection refused"), false},
		{"permission denied (permanent but not not-found)", fmt.Errorf("AccessDenied: forbidden"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isStorageNotFound(c.err); got != c.want {
				t.Errorf("isStorageNotFound(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
