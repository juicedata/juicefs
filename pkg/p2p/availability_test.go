package p2p

import (
	"testing"
	"time"
)

func TestAvailabilityTracker_MarkLocal(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.MarkLocal("key1")

	keys, ts := tr.LocalBlocksSince(0)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "key1" {
		t.Errorf("expected key1, got %s", keys[0])
	}
	if ts <= 0 {
		t.Errorf("expected ts > 0, got %d", ts)
	}
}

func TestAvailabilityTracker_IncrementalLocal(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.MarkLocal("key1")

	_, ts1 := tr.LocalBlocksSince(0)

	time.Sleep(2 * time.Millisecond)

	tr.MarkLocal("key2")

	keys, _ := tr.LocalBlocksSince(ts1)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after ts1, got %d: %v", len(keys), keys)
	}
	if keys[0] != "key2" {
		t.Errorf("expected key2, got %s", keys[0])
	}
}

func TestAvailabilityTracker_UpdateRemote(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.UpdateRemote("peer1", []string{"key1", "key2"})

	if !tr.PeerHas("peer1", "key1") {
		t.Error("expected peer1 to have key1")
	}
	if !tr.PeerHas("peer1", "key2") {
		t.Error("expected peer1 to have key2")
	}
	if tr.PeerHas("peer1", "key3") {
		t.Error("expected peer1 NOT to have key3")
	}
}

func TestAvailabilityTracker_FindPeerWith(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.UpdateRemote("peer1", []string{"key1"})
	tr.UpdateRemote("peer2", []string{"key1", "key2"})

	found := tr.FindPeerWith("key1")
	if found != "peer1" && found != "peer2" {
		t.Errorf("expected peer1 or peer2, got %q", found)
	}

	notFound := tr.FindPeerWith("key3")
	if notFound != "" {
		t.Errorf("expected empty string for missing key, got %q", notFound)
	}
}

func TestAvailabilityTracker_RemovePeer(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.UpdateRemote("peer1", []string{"key1"})

	if !tr.PeerHas("peer1", "key1") {
		t.Fatal("expected peer1 to have key1 before removal")
	}

	tr.RemovePeer("peer1")

	if tr.PeerHas("peer1", "key1") {
		t.Error("expected peer1 NOT to have key1 after removal")
	}
}

func TestAvailabilityTracker_LocalDoneCount(t *testing.T) {
	tr := NewAvailabilityTracker()
	tr.MarkLocal("key1")
	tr.MarkLocal("key2")

	if count := tr.LocalDoneCount(); count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}
