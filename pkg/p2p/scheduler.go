package p2p

import (
	"hash/fnv"
	"sort"
	"sync"
)

// Scheduler orders pending blocks for a single peer's fetch queue.
type Scheduler struct {
	blocks []*Block
}

// NewScheduler creates a Scheduler bound to the given block list.
func NewScheduler(blocks []*Block) *Scheduler {
	return &Scheduler{blocks: blocks}
}

// pendingBlocks returns all blocks currently in the Pending state.
func (s *Scheduler) pendingBlocks() []*Block {
	var out []*Block
	for _, b := range s.blocks {
		if b.State() == BlockPending {
			out = append(out, b)
		}
	}
	return out
}

// OrderForPeer sorts the Pending blocks into a peer-specific order. Two
// goals at once:
//
//   - Spread initial storage hits: each peer sorts by hash(key, myAddr),
//     so different peers see different fronts. While availability polling
//     ramps up, concurrent storage fetches land on disjoint blocks instead
//     of dogpiling the same one.
//   - No orphaned ownership: every peer's queue still contains every block,
//     so a peer dying mid-warmup does not leave any block uncovered.
//
// The order is deterministic per (block-set, peer), so the same peer always
// produces the same queue from the same input. This is a Rendezvous-Hashing
// approximation.
func (s *Scheduler) OrderForPeer(myAddr string) []*Block {
	pending := s.pendingBlocks()
	sort.Slice(pending, func(i, j int) bool {
		return hashKeyForPeer(pending[i].Key, myAddr) < hashKeyForPeer(pending[j].Key, myAddr)
	})
	return pending
}

// hashKeyForPeer derives a 64-bit ordering key from (peerAddr, key).
// FNV-1a is chosen for cross-Go-version stability and good spread on short
// strings.
//
// Peer-first ordering is intentional. FNV mixes weakly on trailing-byte
// changes, so peers with similar names ("peer0", "peer1") fed last would
// produce correlated orderings — defeating the spread goal. Feeding the
// peer first diverges the hash state before the shared key bytes mix in.
//
// The 0x00 separator prevents (peer=AB, key=C) from colliding with
// (peer=A, key=BC).
func hashKeyForPeer(key, peerAddr string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(peerAddr))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// FetchQueue is a thread-safe FIFO queue of *Block values.
type FetchQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*Block
	closed bool
}

// NewFetchQueue creates a FetchQueue with the given initial capacity hint.
func NewFetchQueue(capacity int) *FetchQueue {
	q := &FetchQueue{
		items: make([]*Block, 0, capacity),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push appends b to the queue and signals one waiter.
func (q *FetchQueue) Push(b *Block) {
	q.mu.Lock()
	q.items = append(q.items, b)
	q.cond.Signal()
	q.mu.Unlock()
}

// PushAll appends all blocks to the queue and broadcasts to all waiters.
func (q *FetchQueue) PushAll(blocks []*Block) {
	q.mu.Lock()
	q.items = append(q.items, blocks...)
	q.cond.Broadcast()
	q.mu.Unlock()
}

// Pop returns and removes the front block without blocking. Returns nil if
// the queue is empty.
func (q *FetchQueue) Pop() *Block {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	b := q.items[0]
	q.items = q.items[1:]
	return b
}

// WaitAndPop blocks until a block is available or the queue is closed. Returns
// nil when the queue is closed and empty.
func (q *FetchQueue) WaitAndPop() *Block {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil
	}
	b := q.items[0]
	q.items = q.items[1:]
	return b
}

// Close marks the queue as closed and wakes all blocked WaitAndPop callers.
func (q *FetchQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// Len returns the current number of items in the queue.
func (q *FetchQueue) Len() int {
	q.mu.Lock()
	n := len(q.items)
	q.mu.Unlock()
	return n
}
