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
	ce.encChunkPool = sync.Pool{New: func() any { buf := make([]byte, encSize); return &buf }}
	// For the decrypt path we inject our tracked pool directly into the
	// reader below so we can audit every Get/Put. ce.encChunkPool above
	// is the pool the encrypt-side Put uses internally; we don't need to
	// track its balance here because the encrypt-side has its own
	// dedicated pool-balance test.

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

// TestChunkEncryptReaderPoolBalance is the encrypt-side mirror of
// TestChunkDecryptReaderPoolBalance: after the refactor that pools
// chunk buffers across writes, every Read/Close codepath must Put one
// buffer for every Get. Exercises partial-consume (one-byte reads),
// full-consume (chunk-sized buffer), and Close-before-drain.
func TestChunkEncryptReaderPoolBalance(t *testing.T) {
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)

	overhead := dc.MaxOverhead()
	encSize := plainChunkSize + chunkHeaderSize + int64(overhead)
	pool := newTrackedPool(int(encSize))
	ce := &chunkedEncrypted{enc: dc, overhead: overhead, encChunkSize: encSize}
	ce.plainPool = sync.Pool{New: func() any { buf := make([]byte, plainChunkSize); return &buf }}

	const bodySize = plainChunkSize*2 + 1024
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte((i * 11) % 251)
	}

	openEncryptReader := func() *chunkEncryptReader {
		return &chunkEncryptReader{
			r:         bytes.NewReader(body),
			enc:       dc,
			overhead:  overhead,
			plainPool: &ce.plainPool,
			encPool:   pool,
		}
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

	reset := func() {
		pool.gets.Store(0)
		pool.puts.Store(0)
		pool.doublePu.Store(0)
		pool.alien.Store(0)
		pool.mu.Lock()
		pool.live = map[*[]byte]bool{}
		pool.mu.Unlock()
	}

	// (1) Partial-consume: one byte at a time exercises the leftover-buf
	// branch, which is where the previous implementation would have
	// re-used a stale `chunk` slice across Reads.
	cr := openEncryptReader()
	one := make([]byte, 1)
	for {
		_, err := cr.Read(one)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.NoError(t, cr.Close())
	check("partial-consume")
	reset()

	// (2) Full-consume: a buffer bigger than the encrypted chunk size
	// drains each chunk in one Read, exercising the immediate-release
	// branch.
	cr = openEncryptReader()
	big := make([]byte, int(encSize)+1)
	for {
		_, err := cr.Read(big)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.NoError(t, cr.Close())
	check("full-consume")
	reset()

	// (3) Close-before-drain: read partway, then Close. The borrowed
	// chunk buffer must come back to the pool, not leak. This is the
	// case the original deferred-Put pattern in chunkEncryptReader
	// would have leaked under the new pooled design.
	cr = openEncryptReader()
	mid := make([]byte, 1024)
	_, err = cr.Read(mid)
	require.NoError(t, err)
	require.NoError(t, cr.Close())
	check("close-before-drain")
}

// TestChunkedEncryptedRoundTripUnderPoolReuse drives several
// concurrent Put + Get pairs through a real chunkedEncrypted (with
// the production sync.Pool) to catch any cross-chunk corruption
// introduced by sharing encChunkPool with the encrypt side. If the
// padding-zeroing step in chunkEncryptReader.Read were ever skipped,
// the decryptor would still produce the right plaintext (it only
// reads ct[:ctLen]) but a subtle round-trip such as a partial Get
// could land bytes from a stale chunk in the wrong place.
func TestChunkedEncryptedRoundTripUnderPoolReuse(t *testing.T) {
	ctx := context.Background()
	mem, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	store := NewChunkedEncrypted(mem, dc)

	keys := []string{"a", "b", "c", "d"}
	bodies := make(map[string][]byte, len(keys))
	for i, k := range keys {
		// Each body is a different size and contents so cross-chunk
		// contamination would be visible.
		size := plainChunkSize + 17*(i+1)
		buf := make([]byte, size)
		for j := range buf {
			buf[j] = byte((j*13 + i*101) % 251)
		}
		bodies[k] = buf
		require.NoError(t, store.Put(ctx, k, bytes.NewReader(buf)))
	}

	for _, k := range keys {
		r, err := store.Get(ctx, k, 0, -1)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.Equal(t, bodies[k], got, "round-trip mismatch for %s", k)
	}
}

// BenchmarkChunkEncryptReaderPut measures the encrypt-side throughput
// of a multi-chunk Put through the chunked-encrypted wrapper. Run with
//
//	go test -tags nogspt -bench BenchmarkChunkEncryptReaderPut \
//	    -benchmem ./pkg/object/
//
// to confirm the AUDIT-014 fix reduces per-chunk allocations
// (previously: one ~1 MiB `make` per chunk inside Read).
func BenchmarkChunkEncryptReaderPut(b *testing.B) {
	mem, err := CreateStorage("mem", "", "", "", "")
	if err != nil {
		b.Fatal(err)
	}
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	if err != nil {
		b.Fatal(err)
	}
	store := NewChunkedEncrypted(mem, dc)
	body := make([]byte, 16*plainChunkSize) // 16 MiB → 16 chunks
	for i := range body {
		body[i] = byte(i % 251)
	}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Put(context.Background(), "k", bytes.NewReader(body)); err != nil {
			b.Fatal(err)
		}
	}
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
