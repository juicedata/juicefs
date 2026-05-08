package p2p

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_TwoPeers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Setup two temp cache directories.
	cache1, cache2 := t.TempDir(), t.TempDir()

	// Write fake blocks to each cache directory.
	writeBlock(t, cache1, "chunks/0/0/1_0_100", "data-block-1")
	writeBlock(t, cache2, "chunks/0/0/1_1_100", "data-block-2")

	// Start peer1 server with block1 marked as local.
	at1 := NewAvailabilityTracker()
	at1.MarkLocal("chunks/0/0/1_0_100")
	srv1 := NewServer("uuid-1", at1, cache1)
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen peer1: %v", err)
	}
	defer ln1.Close()
	go http.Serve(ln1, srv1.Handler()) //nolint:errcheck

	// Start peer2 server with block2 marked as local.
	at2 := NewAvailabilityTracker()
	at2.MarkLocal("chunks/0/0/1_1_100")
	srv2 := NewServer("uuid-2", at2, cache2)
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen peer2: %v", err)
	}
	defer ln2.Close()
	go http.Serve(ln2, srv2.Handler()) //nolint:errcheck

	client := &http.Client{Timeout: 5 * time.Second}
	peer1Addr := ln1.Addr().String()
	peer2Addr := ln2.Addr().String()

	// --- Test: peer2 fetches block1 from peer1 ---
	fetcher2 := NewFetcher(nil, cache2, 4*1024*1024, client, nil)
	data, err := fetcher2.FetchFromPeer(context.Background(), peer1Addr, "chunks/0/0/1_0_100")
	if err != nil {
		t.Fatalf("peer2.FetchFromPeer block1: %v", err)
	}
	if string(data) != "data-block-1" {
		t.Errorf("peer2 fetched block1: got %q, want %q", data, "data-block-1")
	}

	// --- Test: CacheBlock writes block1 to peer2's cache, verify on disk ---
	if err := fetcher2.CacheBlock("chunks/0/0/1_0_100", data); err != nil {
		t.Fatalf("peer2.CacheBlock: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(cache2, "raw", "chunks/0/0/1_0_100"))
	if err != nil {
		t.Fatalf("read cached block on disk: %v", err)
	}
	if string(onDisk) != "data-block-1" {
		t.Errorf("cached block on disk: got %q, want %q", onDisk, "data-block-1")
	}

	// --- Test: peer1 fetches block2 from peer2 ---
	fetcher1 := NewFetcher(nil, cache1, 4*1024*1024, client, nil)
	data2, err := fetcher1.FetchFromPeer(context.Background(), peer2Addr, "chunks/0/0/1_1_100")
	if err != nil {
		t.Fatalf("peer1.FetchFromPeer block2: %v", err)
	}
	if string(data2) != "data-block-2" {
		t.Errorf("peer1 fetched block2: got %q, want %q", data2, "data-block-2")
	}

	// --- Test: availability polling from peer2's fetcher to peer1 ---
	at3 := NewAvailabilityTracker()
	fetcher2.SetAvailability(at3)
	ts, err := fetcher2.PollAvailability(context.Background(), peer1Addr, 0)
	if err != nil {
		t.Fatalf("PollAvailability: %v", err)
	}
	if ts <= 0 {
		t.Errorf("PollAvailability: updated_at = %d, want > 0", ts)
	}
	if !at3.PeerHas(peer1Addr, "chunks/0/0/1_0_100") {
		t.Errorf("at3 should know peer1 has block1 after polling")
	}

	// --- Test: /uuid endpoint returns correct UUID for peer1 ---
	resp, err := client.Get("http://" + peer1Addr + "/uuid")
	if err != nil {
		t.Fatalf("GET /uuid peer1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /uuid peer1: status %d", resp.StatusCode)
	}
	var uuidBody struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uuidBody); err != nil {
		t.Fatalf("decode /uuid: %v", err)
	}
	if uuidBody.UUID != "uuid-1" {
		t.Errorf("/uuid: got %q, want %q", uuidBody.UUID, "uuid-1")
	}

	// --- Test: /status endpoint returns correct progress ---
	srv1.SetProgress(10, 5)
	resp2, err := client.Get("http://" + peer1Addr + "/status")
	if err != nil {
		t.Fatalf("GET /status peer1: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /status peer1: status %d", resp2.StatusCode)
	}
	var statusBody struct {
		Completed bool `json:"completed"`
		Progress  struct {
			Total int64 `json:"total"`
			Done  int64 `json:"done"`
		} `json:"progress"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decode /status: %v", err)
	}
	if statusBody.Completed {
		t.Error("/status: expected completed=false when done(5) < total(10)")
	}
	if statusBody.Progress.Total != 10 {
		t.Errorf("/status: total = %d, want 10", statusBody.Progress.Total)
	}
	if statusBody.Progress.Done != 5 {
		t.Errorf("/status: done = %d, want 5", statusBody.Progress.Done)
	}
}

func TestIntegration_PreCachedBlocksServedAfterScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Peer1 simulates a restart with pre-existing cached blocks on disk.
	cache1 := t.TempDir()
	writeBlock(t, cache1, "chunks/0/0/1_0_18", "pre-cached-content")

	config1 := WarmupConfig{
		ListenAddr:           "127.0.0.1:0",
		DiscoveryInterval:    time.Hour,
		AvailabilityInterval: time.Hour,
		Threads:              1,
		CacheDir:             cache1,
		BlockSize:            4 * 1024 * 1024,
	}
	w1 := NewWarmup(config1, nil, nil)

	if n := w1.scanExistingCache(); n != 1 {
		t.Fatalf("scanExistingCache: got %d, want 1", n)
	}
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen peer1: %v", err)
	}
	defer ln1.Close()
	go http.Serve(ln1, w1.server.Handler()) //nolint:errcheck
	peer1Addr := ln1.Addr().String()

	// Peer2 is cold and must discover peer1's pre-cached block via /available.
	cache2 := t.TempDir()
	tracker2 := NewAvailabilityTracker()
	fetcher2 := NewFetcher(nil, cache2, 4*1024*1024, &http.Client{Timeout: 5 * time.Second}, nil)
	fetcher2.SetAvailability(tracker2)

	if _, err := fetcher2.PollAvailability(context.Background(), peer1Addr, 0); err != nil {
		t.Fatalf("PollAvailability: %v", err)
	}
	if !tracker2.PeerHas(peer1Addr, "chunks/0/0/1_0_18") {
		t.Fatal("peer2 should see pre-cached block on peer1 after scan; /available did not report it")
	}

	data, err := fetcher2.FetchFromPeer(context.Background(), peer1Addr, "chunks/0/0/1_0_18")
	if err != nil {
		t.Fatalf("FetchFromPeer: %v", err)
	}
	if string(data) != "pre-cached-content" {
		t.Errorf("fetched content: got %q, want %q", data, "pre-cached-content")
	}
}

// writeBlock writes data to {cacheDir}/raw/{key}, creating parent directories
// as needed. It is a test helper and calls t.Fatal on any error.
func writeBlock(t *testing.T, cacheDir, key, data string) {
	t.Helper()
	path := filepath.Join(cacheDir, "raw", key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("writeBlock MkdirAll %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("writeBlock WriteFile %q: %v", path, err)
	}
}
