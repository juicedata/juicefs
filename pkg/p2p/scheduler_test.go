package p2p

import (
	"fmt"
	"sync"
	"testing"
)

// makeBlocks creates n blocks with sequential indices for testing.
func makeBlocks(n int) []*Block {
	blocks := make([]*Block, n)
	for i := 0; i < n; i++ {
		blocks[i] = &Block{
			Key:   fmt.Sprintf("chunk/%d", i),
			Index: i,
		}
	}
	return blocks
}

func TestOrderForPeer_DeterministicAndCovers(t *testing.T) {
	// Same peer + same blocks must produce the same order on every call,
	// and the order must be a permutation of the pending set (no drops,
	// no duplicates). This is what lets every peer carry every block
	// without any inter-peer coordination.
	blocks := makeBlocks(500)
	s := NewScheduler(blocks)

	a := s.OrderForPeer("10.0.0.1:19090")
	b := s.OrderForPeer("10.0.0.1:19090")

	if len(a) != len(blocks) || len(b) != len(blocks) {
		t.Fatalf("expected %d blocks, got a=%d b=%d", len(blocks), len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("non-deterministic order at index %d: %q vs %q", i, a[i].Key, b[i].Key)
		}
	}

	seen := make(map[string]struct{}, len(a))
	for _, blk := range a {
		if _, dup := seen[blk.Key]; dup {
			t.Errorf("duplicate key in ordering: %q", blk.Key)
		}
		seen[blk.Key] = struct{}{}
	}
	if len(seen) != len(blocks) {
		t.Errorf("ordering covers %d distinct keys, want %d", len(seen), len(blocks))
	}
}

func TestOrderForPeer_DiffersPerPeer(t *testing.T) {
	// Different peers should see substantially different orders so that
	// the front of each peer's queue is statistically distinct — that's
	// what spreads concurrent storage hits across distinct blocks.
	blocks := makeBlocks(1000)
	s := NewScheduler(blocks)

	a := s.OrderForPeer("10.0.0.1:19090")
	b := s.OrderForPeer("10.0.0.2:19090")

	// Count agreement in the first 100 positions.
	agree := 0
	for i := 0; i < 100; i++ {
		if a[i].Key == b[i].Key {
			agree++
		}
	}
	// With independent hashes, expected agreement ≈ 100/1000 = 10%. A
	// hard ceiling of 25 catches a regression where peer addr is ignored
	// while leaving plenty of room for statistical variance.
	if agree > 25 {
		t.Errorf("orders too similar: %d/100 head positions agree (peer addr ignored?)", agree)
	}
}

func TestOrderForPeer_AvoidsIndexSkew(t *testing.T) {
	// Regression for the previous Index%N partition: with every block's
	// Index=0, that scheme collapsed every block to peer 0. The hashed
	// peer-aware order must not exhibit that — different peers must see
	// different blocks at the front of their queues.
	const numBlocks = 600
	blocks := make([]*Block, numBlocks)
	for i := 0; i < numBlocks; i++ {
		blocks[i] = &Block{Key: fmt.Sprintf("chunks/0/0/%d_0_4194304", i), Index: 0}
	}
	s := NewScheduler(blocks)

	a := s.OrderForPeer("peer0")
	b := s.OrderForPeer("peer1")
	c := s.OrderForPeer("peer2")
	if a[0].Key == b[0].Key && b[0].Key == c[0].Key {
		t.Errorf("all peers see the same head block; ordering ignored peerAddr")
	}
}

func TestOrderForPeer_OnlyPendingBlocks(t *testing.T) {
	blocks := makeBlocks(5)
	blocks[2].TryDownload() // Downloading
	blocks[3].TryDownload()
	blocks[3].MarkDone() // Done

	s := NewScheduler(blocks)
	out := s.OrderForPeer("peer0")
	if len(out) != 3 {
		t.Fatalf("expected 3 pending blocks, got %d", len(out))
	}
	for _, b := range out {
		if b.State() != BlockPending {
			t.Errorf("non-pending block %q in ordering (state=%d)", b.Key, b.State())
		}
	}
}

func TestFetchQueue_PopAndPush(t *testing.T) {
	q := NewFetchQueue(10)

	// Pop on empty -> nil
	if b := q.Pop(); b != nil {
		t.Errorf("expected nil on empty Pop, got %v", b)
	}

	block := &Block{Key: "test", Index: 0}
	q.Push(block)

	// Pop returns the block
	got := q.Pop()
	if got == nil {
		t.Fatal("expected block, got nil")
	}
	if got.Key != "test" {
		t.Errorf("expected block key 'test', got %q", got.Key)
	}

	// Pop again -> nil (queue empty)
	if b := q.Pop(); b != nil {
		t.Errorf("expected nil after consuming single block, got %v", b)
	}
}

func TestFetchQueue_FIFO(t *testing.T) {
	q := NewFetchQueue(10)
	blocks := makeBlocks(3)
	q.PushAll(blocks)

	for i := 0; i < 3; i++ {
		b := q.Pop()
		if b == nil {
			t.Fatalf("expected block %d, got nil", i)
		}
		if b.Index != i {
			t.Errorf("expected FIFO order: block %d, got %d", i, b.Index)
		}
	}
}

func TestFetchQueue_WaitAndPop(t *testing.T) {
	q := NewFetchQueue(10)
	block := &Block{Key: "async", Index: 99}

	var wg sync.WaitGroup
	wg.Add(1)

	var got *Block
	go func() {
		defer wg.Done()
		got = q.WaitAndPop()
	}()

	// Push from this goroutine after a tiny delay
	// Use a channel to synchronize without sleep
	ready := make(chan struct{})
	go func() {
		close(ready)
		q.Push(block)
	}()
	<-ready

	wg.Wait()

	if got == nil {
		t.Fatal("WaitAndPop returned nil, expected block")
	}
	if got.Key != "async" {
		t.Errorf("expected key 'async', got %q", got.Key)
	}
}

func TestFetchQueue_Close(t *testing.T) {
	q := NewFetchQueue(10)

	var wg sync.WaitGroup
	wg.Add(1)

	var got *Block
	go func() {
		defer wg.Done()
		got = q.WaitAndPop()
	}()

	// Close the queue — WaitAndPop should return nil
	q.Close()
	wg.Wait()

	if got != nil {
		t.Errorf("expected nil from WaitAndPop after Close, got %v", got)
	}
}

func TestFetchQueue_Len(t *testing.T) {
	q := NewFetchQueue(5)

	if q.Len() != 0 {
		t.Errorf("expected Len 0, got %d", q.Len())
	}

	blocks := makeBlocks(3)
	q.PushAll(blocks)

	if q.Len() != 3 {
		t.Errorf("expected Len 3, got %d", q.Len())
	}

	q.Pop()
	if q.Len() != 2 {
		t.Errorf("expected Len 2 after Pop, got %d", q.Len())
	}
}
