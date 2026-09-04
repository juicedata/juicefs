package p2p

import (
	"sync"
	"time"
)

type localEntry struct {
	key       string
	timestamp int64 // Unix millis
}

// AvailabilityTracker tracks which blocks this node has locally and which
// blocks each remote peer has. The local list is append-only and ordered by
// timestamp so that incremental updates can be served efficiently via
// LocalBlocksSince. The remote map is populated by availability polling and
// consumed by the fetcher to decide whether to pull from a peer or object
// storage.
//
// All methods are safe for concurrent use.
type AvailabilityTracker struct {
	mu     sync.RWMutex
	local  []localEntry                   // append-only, ordered by timestamp
	remote map[string]map[string]struct{} // peerAddr -> set of block keys
}

// NewAvailabilityTracker returns a ready-to-use AvailabilityTracker.
func NewAvailabilityTracker() *AvailabilityTracker {
	return &AvailabilityTracker{
		remote: make(map[string]map[string]struct{}),
	}
}

// MarkLocal records that the local node has completed the given block key.
func (t *AvailabilityTracker) MarkLocal(key string) {
	now := time.Now().UnixMilli()
	t.mu.Lock()
	t.local = append(t.local, localEntry{key: key, timestamp: now})
	t.mu.Unlock()
}

// LocalBlocksSince returns all block keys whose timestamp is strictly greater
// than sinceMs, along with the timestamp of the newest entry seen (0 if the
// local list is empty). Callers should store the returned timestamp and pass
// it as sinceMs on the next call to receive only new blocks.
func (t *AvailabilityTracker) LocalBlocksSince(sinceMs int64) ([]string, int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var latestTs int64
	if n := len(t.local); n > 0 {
		latestTs = t.local[n-1].timestamp
	}

	// Binary search for the first entry with timestamp > sinceMs.
	// local is ordered by insertion time which corresponds to monotonically
	// non-decreasing timestamps (same-millisecond entries keep their order).
	lo, hi := 0, len(t.local)
	for lo < hi {
		mid := (lo + hi) / 2
		if t.local[mid].timestamp <= sinceMs {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	entries := t.local[lo:]
	if len(entries) == 0 {
		return nil, latestTs
	}

	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.key
	}
	return keys, latestTs
}

// LocalDoneCount returns the total number of blocks recorded via MarkLocal.
func (t *AvailabilityTracker) LocalDoneCount() int {
	t.mu.RLock()
	n := len(t.local)
	t.mu.RUnlock()
	return n
}

// UpdateRemote adds keys to the set of blocks known to be held by peerAddr.
// The update is additive — existing keys are never removed (call RemovePeer to
// clear all state for a peer).
func (t *AvailabilityTracker) UpdateRemote(peerAddr string, keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	set, ok := t.remote[peerAddr]
	if !ok {
		set = make(map[string]struct{}, len(keys))
		t.remote[peerAddr] = set
	}
	for _, k := range keys {
		set[k] = struct{}{}
	}
}

// PeerHas reports whether peerAddr is known to hold the given block key.
func (t *AvailabilityTracker) PeerHas(peerAddr, key string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	set, ok := t.remote[peerAddr]
	if !ok {
		return false
	}
	_, has := set[key]
	return has
}

// FindPeerWith returns the address of the first peer that is known to hold
// key, or "" if no such peer exists. The order of iteration over the internal
// map is not guaranteed; callers that need determinism should post-process the
// full list themselves.
func (t *AvailabilityTracker) FindPeerWith(key string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for addr, set := range t.remote {
		if _, ok := set[key]; ok {
			return addr
		}
	}
	return ""
}

// RemovePeer deletes all availability data for the given peer address.
func (t *AvailabilityTracker) RemovePeer(peerAddr string) {
	t.mu.Lock()
	delete(t.remote, peerAddr)
	t.mu.Unlock()
}
