/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package object

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// trackedPool wraps sync.Pool's Get/Put with explicit borrow tracking
// so the test can assert exactly one Put per Get, no buffer being Put
// twice, and zero live borrows at the end. Each Get returns a freshly
// allocated buffer (no reuse) so identity equality is a reliable
// double-Put signal.
type trackedPool struct {
	size     int
	mu       sync.Mutex
	live     map[*[]byte]bool
	gets     atomic.Int64
	puts     atomic.Int64
	doublePu atomic.Int64
	alien    atomic.Int64
}

func newTrackedPool(size int) *trackedPool {
	return &trackedPool{size: size, live: map[*[]byte]bool{}}
}

func (p *trackedPool) Get() any {
	p.gets.Add(1)
	buf := make([]byte, p.size)
	pb := &buf
	p.mu.Lock()
	p.live[pb] = true
	p.mu.Unlock()
	return pb
}

func (p *trackedPool) Put(x any) {
	p.puts.Add(1)
	pb := x.(*[]byte)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.live[pb] {
		// Either a double-Put (already removed) or an alien buffer.
		// Bookkeeping is enough; the test fails the assertion.
		p.doublePu.Add(1)
		return
	}
	delete(p.live, pb)
}

func (p *trackedPool) liveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.live)
}

// TestChunkDecryptReaderPoolBalance is the regression guard for the
// audit's "future maintainer will get it wrong" concern: every
// Read/Close codepath in chunkDecryptReader must Put exactly one
// buffer for every Get. We exercise the partial-consume path
// (one-byte reads), the full-consume path (read a whole chunk at
// once), and the Close-before-drain path.
func TestChunkDecryptReaderPoolBalance(t *testing.T) {
	ctx := context.Background()
	memStore, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)

	// Build a chunkedEncrypted just like NewChunkedEncrypted does, but
	// with our tracked pool so we can audit every Get/Put.
	overhead := dc.MaxOverhead()
	encSize := plainChunkSize + chunkHeaderSize + int64(overhead)
	pool := newTrackedPool(int(encSize))
	ce := &chunkedEncrypted{
		ObjectStorage: memStore,
		enc:           dc,
		overhead:      overhead,
		encChunkSize:  encSize,
	}
	ce.plainPool = sync.Pool{New: func() any { buf := make([]byte, plainChunkSize); return &buf }}
	// encChunkPool is unused by writes; Put uses plainPool. For reads we
	// inject our tracked pool directly into the decrypt reader below.

	// A body that spans multiple chunks plus a partial tail — exercises
	// both full-chunk and partial-chunk paths.
	const bodySize = plainChunkSize*2 + 1024
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte((i * 7) % 251)
	}
	require.NoError(t, ce.Put(ctx, "k", bytes.NewReader(body)))

	openDecryptReader := func() *chunkDecryptReader {
		// Fetch the encrypted payload from the wrapped storage and wire
		// up a chunkDecryptReader backed by the tracked pool.
		rc, err := memStore.Get(ctx, "k", 0, -1)
		require.NoError(t, err)
		return &chunkDecryptReader{r: rc, enc: dc, pool: pool}
	}

	check := func(label string) {
		if got := pool.doublePu.Load(); got != 0 {
			t.Fatalf("%s: %d double-Put detected", label, got)
		}
		if got := pool.alien.Load(); got != 0 {
			t.Fatalf("%s: %d alien-buf Put detected", label, got)
		}
		if got := pool.liveCount(); got != 0 {
			t.Fatalf("%s: %d buffer(s) leaked from the pool", label, got)
		}
		if g, p := pool.gets.Load(), pool.puts.Load(); g != p {
			t.Fatalf("%s: Gets=%d Puts=%d (must be equal)", label, g, p)
		}
	}

	// (1) Partial-consume: drain one byte at a time so the leftover-buf
	// branch is exercised heavily.
	dr := openDecryptReader()
	got := make([]byte, 0, bodySize)
	one := make([]byte, 1)
	for {
		n, err := dr.Read(one)
		if n > 0 {
			got = append(got, one[:n]...)
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, body, got)
	require.NoError(t, dr.Close())
	check("partial-consume")

	// (2) Full-consume: a buffer big enough to drain a whole chunk in
	// one Read exercises the immediate-release branch.
	dr = openDecryptReader()
	big := make([]byte, plainChunkSize+1)
	got = got[:0]
	for {
		n, err := dr.Read(big)
		if n > 0 {
			got = append(got, big[:n]...)
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, body, got)
	require.NoError(t, dr.Close())
	check("full-consume")

	// (3) Close-before-drain: read partway, then close. The leftover
	// chunk must be returned to the pool by Close, not leaked.
	dr = openDecryptReader()
	mid := make([]byte, 1024)
	_, err = dr.Read(mid)
	require.NoError(t, err)
	require.NoError(t, dr.Close())
	check("close-before-drain")
}

func TestChunkedEncryptedConcurrentGet(t *testing.T) {
	ctx := context.Background()
	s, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	store := NewChunkedEncrypted(s, dc)

	const dataSize = 1024
	want := make([]byte, dataSize)
	for i := range want {
		want[i] = byte(i % 251)
	}
	require.NoError(t, store.Put(ctx, "key", bytes.NewReader(want)))

	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.Get(ctx, "key", 0, -1)
			require.NoError(t, err)
			defer r.Close()

			// Read one byte at a time to maximise the r.buf aliasing window.
			buf := make([]byte, 1)
			var got []byte
			for {
				n, readErr := r.Read(buf)
				if n > 0 {
					got = append(got, buf[:n]...)
				}
				if readErr == io.EOF {
					break
				}
				require.NoError(t, readErr)
			}
			require.Equal(t, want, got)
		}()
	}
	wg.Wait()
}
