package p2p

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestWarmupWithStorage(t *testing.T, storage *trackingStorage) *Warmup {
	t.Helper()
	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
		HashPrefix:           false,
	}
	return NewWarmup(config, nil, storage)
}

func TestWarmup_UploadManifest_PutsGzippedJSON(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)

	paths := []string{"/models/llama"}
	blocks := []*Block{
		{Key: "chunks/0/0/1_0_100", Size: 100},
		{Key: "chunks/0/0/1_1_200", Size: 200},
	}

	if err := w.uploadManifest(context.Background(), paths, blocks); err != nil {
		t.Fatalf("uploadManifest: %v", err)
	}

	wantKey := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	storage.mu.Lock()
	data, ok := storage.data[wantKey]
	storage.mu.Unlock()
	if !ok {
		t.Fatalf("manifest not stored at expected key %q", wantKey)
	}

	got, err := UnmarshalManifest(data)
	if err != nil {
		t.Fatalf("stored bytes are not valid manifest: %v", err)
	}
	if got.TotalBlocks != 2 {
		t.Errorf("uploaded manifest TotalBlocks = %d, want 2", got.TotalBlocks)
	}
	if got.TotalBytes != 300 {
		t.Errorf("uploaded manifest TotalBytes = %d, want 300", got.TotalBytes)
	}
}

func TestWarmup_UploadManifest_SkipsWhenContentMatches(t *testing.T) {
	// Crash-recovery path: leader restarts, finds its own manifest from the
	// previous run, and proceeds without a redundant PUT.
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.ManifestName = "llama-7b"

	paths := []string{"/p"}
	blocks := []*Block{{Key: "chunks/0/0/1_0_100", Size: 100}}

	matching := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, blocks)
	matchingData, _ := matching.Marshal()
	key := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	storage.data[key] = matchingData

	if err := w.uploadManifest(context.Background(), paths, blocks); err != nil {
		t.Fatalf("uploadManifest should skip on content match: %v", err)
	}

	// The original bytes must still be there — Put was skipped.
	storage.mu.Lock()
	stored := storage.data[key]
	storage.mu.Unlock()
	if !reflect.DeepEqual(stored, matchingData) {
		t.Errorf("matching content path should have skipped Put; stored bytes changed")
	}
}

func TestWarmup_UploadManifest_RefusesWhenContentDiffers(t *testing.T) {
	// True collision: another warmup uploaded a manifest under the same name
	// with different paths. Leader must refuse to overwrite.
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.ManifestName = "shared"

	requestedPaths := []string{"/wanted"}
	otherPaths := []string{"/different"}
	other := BuildManifest(otherPaths, w.config.BlockSize, w.config.HashPrefix, []*Block{
		{Key: "chunks/0/0/9_0_42", Size: 42},
	})
	otherData, _ := other.Marshal()
	key := ManifestKey(requestedPaths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	storage.data[key] = otherData

	err := w.uploadManifest(context.Background(), requestedPaths, []*Block{
		{Key: "chunks/0/0/1_0_100", Size: 100},
	})
	if err == nil {
		t.Fatal("expected error when existing manifest has different content")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("error should announce the refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("error should mention the conflicting --manifest-name: %v", err)
	}

	storage.mu.Lock()
	stored := storage.data[key]
	storage.mu.Unlock()
	if !reflect.DeepEqual(stored, otherData) {
		t.Errorf("refusal must not modify the existing manifest")
	}
}

func TestWarmup_UploadManifest_OverwritesUnparseable(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)

	paths := []string{"/p"}
	key := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	storage.data[key] = []byte("not-a-gzipped-json")

	blocks := []*Block{{Key: "chunks/0/0/1_0_100", Size: 100}}
	if err := w.uploadManifest(context.Background(), paths, blocks); err != nil {
		t.Fatalf("uploadManifest should overwrite corrupt object: %v", err)
	}

	storage.mu.Lock()
	stored := storage.data[key]
	storage.mu.Unlock()
	got, err := UnmarshalManifest(stored)
	if err != nil {
		t.Fatalf("stored bytes are not a valid manifest after overwrite: %v", err)
	}
	if got.TotalBlocks != 1 {
		t.Errorf("overwrote manifest TotalBlocks = %d, want 1", got.TotalBlocks)
	}
}

func TestWarmup_DownloadManifest_ReturnsParsed(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)

	paths := []string{"/models/llama"}
	original := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, []*Block{
		{Key: "chunks/0/0/1_0_100", Size: 100},
	})
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	storage.data[ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)] = data

	got, err := w.downloadManifest(context.Background(), paths, 1*time.Second, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("downloadManifest: %v", err)
	}
	if !reflect.DeepEqual(got.Blocks, original.Blocks) {
		t.Errorf("downloaded blocks differ:\n got: %+v\nwant: %+v", got.Blocks, original.Blocks)
	}
}

func TestWarmup_DownloadManifest_PollsUntilAvailable(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)

	paths := []string{"/p"}

	// Inject the manifest after 150ms — downloadManifest should be polling.
	go func() {
		time.Sleep(150 * time.Millisecond)
		m := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, []*Block{
			{Key: "chunks/0/0/1_0_50", Size: 50},
		})
		data, _ := m.Marshal()
		storage.mu.Lock()
		storage.data[ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)] = data
		storage.mu.Unlock()
	}()

	start := time.Now()
	got, err := w.downloadManifest(context.Background(), paths, 2*time.Second, 50*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("downloadManifest: %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("returned too quickly: %v (manifest was injected after 150ms)", elapsed)
	}
	if got.TotalBlocks != 1 {
		t.Errorf("TotalBlocks = %d, want 1", got.TotalBlocks)
	}
}

func TestWarmup_ResolveOrDownload_FollowerPathLoadsManifest(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 3
	w.config.LeaderTimeout = 1 * time.Second
	w.listenAddr = "127.0.0.1:20002" // self is NOT lowest

	paths := []string{"/p"}
	leaderManifest := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, []*Block{
		{Key: "chunks/0/0/1_0_42", Size: 42},
		{Key: "chunks/0/0/1_1_99", Size: 99},
	})
	data, _ := leaderManifest.Marshal()
	storage.data[ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)] = data

	peers := []string{"127.0.0.1:20001", "127.0.0.1:20003"} // 20001 is leader

	blocks, err := w.resolveOrDownloadManifest(context.Background(), paths, peers)
	if err != nil {
		t.Fatalf("resolveOrDownloadManifest: %v", err)
	}
	if len(blocks) != 2 {
		t.Errorf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].Key != "chunks/0/0/1_0_42" {
		t.Errorf("blocks[0].Key = %q", blocks[0].Key)
	}
	// Follower must NOT call resolver, so storage.Get for manifest is the
	// only Get; no other meta-engine traffic happens here.
}

func TestWarmup_ResolveOrDownload_ReusesManifestEvenForSelfPerceivedLeader(t *testing.T) {
	// Scale-out scenario: a new peer with the smallest address joins a
	// cluster whose manifest was already uploaded by a different leader.
	// Without the manifest-reuse fast-path, this peer would mis-elect itself
	// leader and run a redundant meta-engine scan. With the fast-path, it
	// reuses the existing manifest and never touches the meta engine.
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 3
	// w.resolver stays nil — if the fast-path fails and the leader branch
	// runs, resolveOrDownloadManifest will error out, surfacing the bug.
	w.listenAddr = "127.0.0.1:10000" // smallest among peers below

	paths := []string{"/p"}
	existing := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, []*Block{
		{Key: "chunks/0/0/1_0_42", Size: 42},
	})
	data, _ := existing.Marshal()
	storage.data[ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)] = data

	peers := []string{"127.0.0.1:20001", "127.0.0.1:20002"} // both larger than self

	blocks, err := w.resolveOrDownloadManifest(context.Background(), paths, peers)
	if err != nil {
		t.Fatalf("resolveOrDownloadManifest: %v (fast-path should have skipped resolve)", err)
	}
	if len(blocks) != 1 {
		t.Errorf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Key != "chunks/0/0/1_0_42" {
		t.Errorf("blocks[0].Key = %q", blocks[0].Key)
	}
}

func TestWarmup_ResolveOrDownload_FastPathSkippedWhenNoManifest(t *testing.T) {
	// Cold start: storage is empty. Self-perceived leader (smallest address)
	// must fall through the fast-path and resolve via meta — verified
	// indirectly by the resolver=nil error path.
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 3
	w.listenAddr = "127.0.0.1:10000"

	peers := []string{"127.0.0.1:20001", "127.0.0.1:20002"}

	_, err := w.resolveOrDownloadManifest(context.Background(), []string{"/p"}, peers)
	if err == nil {
		t.Fatal("expected error when manifest is absent and resolver is nil")
	}
	if !strings.Contains(err.Error(), "meta resolver not available") {
		t.Errorf("expected meta-resolver error, got: %v", err)
	}
}

func TestWarmup_ResolveOrDownload_FollowerRejectsCollidingManifest(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 3
	w.config.LeaderTimeout = 1 * time.Second
	w.config.ManifestName = "shared" // operator-supplied name → collision-prone
	w.listenAddr = "127.0.0.1:20002"

	// Simulate another warmup having uploaded a manifest under the same name
	// but for completely different paths.
	requestedPaths := []string{"/wanted/here"}
	otherPaths := []string{"/totally/different"}
	otherManifest := BuildManifest(otherPaths, w.config.BlockSize, w.config.HashPrefix, []*Block{
		{Key: "chunks/0/0/9_0_42", Size: 42},
	})
	data, _ := otherManifest.Marshal()
	storage.data[ManifestKey(requestedPaths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)] = data

	peers := []string{"127.0.0.1:20001", "127.0.0.1:20003"}

	_, err := w.resolveOrDownloadManifest(context.Background(), requestedPaths, peers)
	if err == nil {
		t.Fatal("expected error on manifest content mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "manifest content mismatch") {
		t.Errorf("error should describe content mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("error should include the conflicting --manifest-name: %v", err)
	}
}

func TestWarmup_ResolveOrDownload_MinPeersOneIsSolo(t *testing.T) {
	// New semantic: MinPeers counts INCLUDING self.
	// MinPeers=1 means "just me", so manifest sharing should be off
	// even if other addresses appear in the peers list (e.g. stale DNS).
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 1
	w.config.LeaderTimeout = 200 * time.Millisecond
	w.listenAddr = "127.0.0.1:20002"

	// peers list contains a lower address — under old semantic (gate `> 0`)
	// this would route us to the follower path and downloadManifest would
	// be called (and would time out since no manifest exists).
	// Under new semantic (gate `> 1`), MinPeers=1 is solo: the call
	// should hit the nil-resolver error from the leader/solo branch.
	peers := []string{"127.0.0.1:20001"}

	_, err := w.resolveOrDownloadManifest(context.Background(), []string{"/p"}, peers)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "meta resolver not available") {
		t.Errorf("expected solo-path error (nil resolver), got: %v", err)
	}
	// Confirm no follower behaviour: storage.Get must NOT have been called
	// (which would happen if we tried to download a manifest).
	if storage.getCalls.Load() != 0 {
		t.Errorf("storage.Get called %d times; expected 0 (solo mode)", storage.getCalls.Load())
	}
}

func TestWarmup_ResolveOrDownload_DisableForcesLocalResolve(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)
	w.config.MinPeers = 3
	w.config.DisableManifestSharing = true
	w.listenAddr = "127.0.0.1:20002" // would be follower if sharing enabled

	peers := []string{"127.0.0.1:20001", "127.0.0.1:20003"}

	// resolver is nil (no meta), so the local-resolve path returns the
	// nil-resolver error. This proves the dispatch chose the resolve branch
	// rather than the download branch.
	_, err := w.resolveOrDownloadManifest(context.Background(), []string{"/p"}, peers)
	if err == nil {
		t.Fatal("expected nil-resolver error, got nil")
	}
	if !strings.Contains(err.Error(), "meta resolver not available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWarmup_DownloadManifest_TimesOut(t *testing.T) {
	storage := newTrackingStorage()
	w := newTestWarmupWithStorage(t, storage)

	paths := []string{"/never-exists"}

	_, err := w.downloadManifest(context.Background(), paths, 200*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("downloadManifest should have timed out")
	}
}
