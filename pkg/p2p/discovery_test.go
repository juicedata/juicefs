package p2p

import (
	"context"
	"testing"
	"time"
)

func TestParseStaticPeers(t *testing.T) {
	peers := parseStaticPeers("10.0.0.1:19090,10.0.0.2:19090")
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	if peers[0] != "10.0.0.1:19090" {
		t.Errorf("expected 10.0.0.1:19090, got %s", peers[0])
	}
	if peers[1] != "10.0.0.2:19090" {
		t.Errorf("expected 10.0.0.2:19090, got %s", peers[1])
	}
}

func TestParseStaticPeers_Empty(t *testing.T) {
	peers := parseStaticPeers("")
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}
}

func TestParseStaticPeers_Whitespace(t *testing.T) {
	peers := parseStaticPeers(" a , b ")
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	if peers[0] != "a" {
		t.Errorf("expected 'a', got %q", peers[0])
	}
	if peers[1] != "b" {
		t.Errorf("expected 'b', got %q", peers[1])
	}
}

func TestDiscovery_StaticOnly(t *testing.T) {
	d := NewPeerDiscovery("", "", "10.0.0.1:19090,10.0.0.2:19090", 19090)
	peers, err := d.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d: %v", len(peers), peers)
	}
}

func TestDiscovery_DeduplicatesAddrs(t *testing.T) {
	// Same address in static list twice → should deduplicate to 1
	d := NewPeerDiscovery("", "", "10.0.0.1:19090,10.0.0.1:19090", 19090)
	peers, err := d.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after dedup, got %d: %v", len(peers), peers)
	}
}

func TestDiscovery_PeersCachesLastResolve(t *testing.T) {
	d := NewPeerDiscovery("", "", "10.0.0.1:19090", 19090)

	// Before any Resolve, Peers() returns empty
	initial := d.Peers()
	if len(initial) != 0 {
		t.Fatalf("expected 0 peers before resolve, got %d", len(initial))
	}

	_, err := d.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cached := d.Peers()
	if len(cached) != 1 {
		t.Fatalf("expected 1 cached peer, got %d", len(cached))
	}
	if cached[0] != "10.0.0.1:19090" {
		t.Errorf("unexpected cached peer: %s", cached[0])
	}
}

func TestDiscovery_PeersReturnsCopy(t *testing.T) {
	d := NewPeerDiscovery("", "", "10.0.0.1:19090", 19090)
	_, _ = d.Resolve(context.Background())

	p1 := d.Peers()
	p1[0] = "mutated"

	p2 := d.Peers()
	if p2[0] == "mutated" {
		t.Error("Peers() should return a copy, not the internal slice")
	}
}

func TestDiscovery_RunLoop(t *testing.T) {
	d := NewPeerDiscovery("", "", "10.0.0.1:19090", 19090)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.RunLoop(ctx, 10*time.Millisecond)
	}()

	// Give the loop time to run at least once
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop did not exit after context cancellation")
	}

	// After the loop ran, the cache should be populated
	peers := d.Peers()
	if len(peers) == 0 {
		t.Error("expected cached peers after RunLoop, got none")
	}
}
