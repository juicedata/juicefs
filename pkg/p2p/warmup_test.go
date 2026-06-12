package p2p

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestWarmup_NewAndClose(t *testing.T) {
	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              4,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)
	if w == nil {
		t.Fatal("NewWarmup returned nil")
	}
	if w.uuid == "" {
		t.Fatal("UUID should be set")
	}
	if w.availability == nil {
		t.Fatal("availability tracker should be set")
	}
	if w.discovery == nil {
		t.Fatal("discovery should be set")
	}
	if w.server == nil {
		t.Fatal("server should be set")
	}
	if w.fetcher == nil {
		t.Fatal("fetcher should be set")
	}
	// resolver should be nil when meta is nil
	if w.resolver != nil {
		t.Fatal("resolver should be nil when meta is nil")
	}
}

func TestWarmup_UUIDUniqueness(t *testing.T) {
	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
	}
	w1 := NewWarmup(config, nil, nil)
	w2 := NewWarmup(config, nil, nil)
	if w1.uuid == w2.uuid {
		t.Fatalf("two Warmup instances should have different UUIDs, both got %s", w1.uuid)
	}
}

func TestWarmup_ScanExistingCache_PopulatesTracker(t *testing.T) {
	cacheDir := t.TempDir()
	// Each file's bytes count must equal the logical size encoded in the
	// last "_<n>" suffix; otherwise scanExistingCache treats the file as
	// corrupt or partial and skips it (matching mount's openCacheFile
	// validation contract).
	writeRawBlock(t, cacheDir, "chunks/0/0/1_0_100", make([]byte, 100))
	writeRawBlock(t, cacheDir, "chunks/0/0/1_1_100", make([]byte, 100))
	writeRawBlock(t, cacheDir, "chunks/5/7/9999_0_2048", make([]byte, 2048))

	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             cacheDir,
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)

	n, _ := w.scanExistingCache()
	if n != 3 {
		t.Errorf("scanExistingCache returned %d, want 3", n)
	}
	if got := w.availability.LocalDoneCount(); got != 3 {
		t.Errorf("tracker LocalDoneCount = %d, want 3", got)
	}

	keys, _ := w.availability.LocalBlocksSince(0)
	sort.Strings(keys)
	want := []string{
		"chunks/0/0/1_0_100",
		"chunks/0/0/1_1_100",
		"chunks/5/7/9999_0_2048",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("tracker keys = %v, want %v", keys, want)
	}
}

func TestWarmup_ScanExistingCache_RejectsWrongSizedFiles(t *testing.T) {
	// Mix of valid and invalid cache files. Invalid ones (size != logical
	// from key, and not logical+checksumTrailer) must NOT be announced
	// — they would mislead other peers into fetching unusable bytes.
	cacheDir := t.TempDir()

	// Valid: bytes count matches logical size suffix.
	writeRawBlock(t, cacheDir, "chunks/0/0/1_0_100", make([]byte, 100))
	// Valid: with checksum trailer (logical=100, csBlock=32k → trailer = 4 bytes).
	writeRawBlock(t, cacheDir, "chunks/0/0/1_1_100", make([]byte, 100+4))
	// Invalid: file size disagrees with the logical size encoded in the key
	// (e.g. produced by an external tool, or an unforeseen write bug).
	writeRawBlock(t, cacheDir, "chunks/0/0/2_0_4194304", make([]byte, 1024))
	// Invalid: partial write (e.g. killed mid-flight).
	writeRawBlock(t, cacheDir, "chunks/0/0/3_0_2048", make([]byte, 1500))
	// Invalid: missing logical-size suffix entirely.
	writeRawBlock(t, cacheDir, "chunks/0/0/garbage_no_size_suffix", make([]byte, 50))

	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             cacheDir,
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)

	n, _ := w.scanExistingCache()
	if n != 2 {
		t.Errorf("scanExistingCache returned %d, want 2 (only the two well-formed files)", n)
	}

	keys, _ := w.availability.LocalBlocksSince(0)
	sort.Strings(keys)
	want := []string{"chunks/0/0/1_0_100", "chunks/0/0/1_1_100"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("tracker keys = %v, want %v (corrupt/partial files must be skipped)", keys, want)
	}
}

func TestParseLogicalSize(t *testing.T) {
	cases := []struct {
		key  string
		want int
	}{
		{"chunks/0/0/100_0_4194304", 4194304},
		{"chunks/5/7/9999_3_512", 512},
		{"chunks/0/0/1_0_100", 100},
		{"missing_suffix", 0},                    // last "_" yields non-numeric "suffix"
		{"chunks/0/0/garbage_no_size_suffix", 0}, // trailing "suffix" is non-numeric
		{"no-underscores", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseLogicalSize(c.key); got != c.want {
			t.Errorf("parseLogicalSize(%q) = %d, want %d", c.key, got, c.want)
		}
	}
}

func TestValidCacheFileSize(t *testing.T) {
	// Mirror every layout pkg/chunk/disk_cache.go's openCacheFile accepts:
	// logical, logical+tier, logical+checksum, logical+checksum+tier.
	cases := []struct {
		name    string
		onDisk  int64
		logical int
		ok      bool
	}{
		// Accepted (mount accepts these on read).
		{"exact match", 100, 100, true},
		{"with tier only", 100 + 1, 100, true},
		{"with checksum trailer (1 csBlock)", 100 + 4, 100, true},
		{"with checksum + tier (1 csBlock)", 100 + 4 + 1, 100, true},
		{"larger logical with checksum (2 csBlocks)", 40*1024 + 8, 40 * 1024, true},
		{"larger logical with checksum + tier (2 csBlocks)", 40*1024 + 8 + 1, 40 * 1024, true},
		// Rejected.
		{"compressed-too-small", 50, 100, false},
		{"random extra bytes", 102, 100, false},
		{"checksum minus one byte", 100 + 3, 100, false},
		{"zero logical (parse failure upstream)", 0, 0, false},
		{"negative logical (defensive)", 100, -1, false},
	}
	for _, c := range cases {
		if got := validCacheFileSize(c.onDisk, c.logical); got != c.ok {
			t.Errorf("%s: got %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestWarmup_ScanExistingCache_MissingDir(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "does-not-exist")

	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             cacheDir,
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)

	n, _ := w.scanExistingCache()
	if n != 0 {
		t.Errorf("scanExistingCache on missing dir: got %d, want 0", n)
	}
	if got := w.availability.LocalDoneCount(); got != 0 {
		t.Errorf("tracker should be empty on missing dir, got %d entries", got)
	}
}

// TestWarmup_ScanExistingCache_AccumulatesBytes locks in the byte total used
// to seed Fetcher.cacheUsed. Without an accurate seed, --cache-size only
// caps NEW writes — leaving a run that inherits a near-full cache free to
// double actual usage before the cap kicks in.
func TestWarmup_ScanExistingCache_AccumulatesBytes(t *testing.T) {
	cacheDir := t.TempDir()
	writeRawBlock(t, cacheDir, "chunks/0/0/1_0_100", make([]byte, 100))
	writeRawBlock(t, cacheDir, "chunks/0/0/1_1_100", make([]byte, 100+4)) // checksum trailer
	writeRawBlock(t, cacheDir, "chunks/5/7/9999_0_2048", make([]byte, 2048))
	// Corrupt file — must not contribute to the byte total.
	writeRawBlock(t, cacheDir, "chunks/0/0/2_0_4194304", make([]byte, 1024))

	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             cacheDir,
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)

	n, bytes := w.scanExistingCache()
	if n != 3 {
		t.Errorf("scanExistingCache count = %d, want 3", n)
	}
	const wantBytes = int64(100 + (100 + 4) + 2048)
	if bytes != wantBytes {
		t.Errorf("scanExistingCache bytes = %d, want %d (corrupt file excluded)", bytes, wantBytes)
	}
}

func TestIsLeader_SoloPeer(t *testing.T) {
	if !isLeader(nil, "10.0.0.1:19090") {
		t.Error("solo peer should be leader")
	}
	if !isLeader([]string{}, "10.0.0.1:19090") {
		t.Error("empty peer list: self should be leader")
	}
}

func TestIsLeader_LowestAddressWins(t *testing.T) {
	peers := []string{"10.0.0.2:19090", "10.0.0.3:19090"}
	if !isLeader(peers, "10.0.0.1:19090") {
		t.Error("self with lowest address should be leader")
	}
}

func TestIsLeader_NotLowest(t *testing.T) {
	peers := []string{"10.0.0.1:19090", "10.0.0.3:19090"}
	if isLeader(peers, "10.0.0.2:19090") {
		t.Error("self with non-lowest address should NOT be leader")
	}
}

func TestIsLeader_OrderIndependent(t *testing.T) {
	a := isLeader([]string{"a", "b", "c"}, "z")
	b := isLeader([]string{"c", "a", "b"}, "z")
	if a != b {
		t.Error("isLeader result must not depend on input order")
	}
}

func TestCountTerminalBlocks(t *testing.T) {
	blocks := []*Block{
		{Key: "a"}, // pending
		{Key: "b"},
		{Key: "c"}, // pending
		{Key: "d"},
		{Key: "e"}, // failed
	}
	blocks[1].MarkDone()
	blocks[3].MarkDone()
	blocks[4].MarkFailed()

	done, failed := countTerminalBlocks(blocks)
	if done != 2 {
		t.Errorf("done = %d, want 2", done)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

func TestKeepAliveTimeoutIgnored(t *testing.T) {
	cases := []struct {
		name string
		ka   bool
		kat  time.Duration
		want bool
	}{
		{"both set", true, time.Hour, false},
		{"timeout only — ignored", false, time.Hour, true},
		{"keep-alive only — manual", true, 0, false},
		{"neither", false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := WarmupConfig{KeepAlive: tc.ka, KeepAliveTimeout: tc.kat}
			if got := c.keepAliveTimeoutIgnored(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectiveAddr_PrefersExternal(t *testing.T) {
	w := &Warmup{listenAddr: "[::]:19090", externalAddr: "10.0.0.5:19090"}
	if got := w.effectiveAddr(); got != "10.0.0.5:19090" {
		t.Errorf("effectiveAddr = %q, want externalAddr", got)
	}
}

func TestEffectiveAddr_FallsBackToListen(t *testing.T) {
	w := &Warmup{listenAddr: "[::]:19090"}
	if got := w.effectiveAddr(); got != "[::]:19090" {
		t.Errorf("effectiveAddr = %q, want listenAddr fallback", got)
	}
}

// TestFilterSelf_DiscoversExternalAddr verifies the wildcard-listen bug fix:
// when listenAddr is something like "[::]:port" but discovery returns concrete
// IPs, filterSelf must record the matching peer's address as externalAddr so
// that leader election sees a consistent identity across all peers.
func TestFilterSelf_DiscoversExternalAddr(t *testing.T) {
	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              1,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
	}
	self := NewWarmup(config, nil, nil)
	other := NewWarmup(config, nil, nil)

	selfSrv := httptest.NewServer(self.server.Handler())
	defer selfSrv.Close()
	otherSrv := httptest.NewServer(other.server.Handler())
	defer otherSrv.Close()

	// Simulate the wildcard-listen scenario: listenAddr is unrelated to the
	// addresses peers see.
	self.listenAddr = "[::]:19090"

	selfExternal := strings.TrimPrefix(selfSrv.URL, "http://")
	otherExternal := strings.TrimPrefix(otherSrv.URL, "http://")

	filtered := self.filterSelf(context.Background(), []string{selfExternal, otherExternal})

	if len(filtered) != 1 || filtered[0] != otherExternal {
		t.Errorf("filtered = %v, want [%s]", filtered, otherExternal)
	}
	if self.externalAddr != selfExternal {
		t.Errorf("externalAddr = %q, want %q", self.externalAddr, selfExternal)
	}
	if got := self.effectiveAddr(); got != selfExternal {
		t.Errorf("effectiveAddr = %q, want %q (not the wildcard listenAddr)", got, selfExternal)
	}
}

// pollTestWarmup builds a Warmup wired with a controllable discovery cache
// and an externalAddr sentinel that won't match the test peers. Tests can
// then drive pollAvailabilityOnce directly without spinning up the
// background ticker goroutine.
func pollTestWarmup(t *testing.T) *Warmup {
	t.Helper()
	config := WarmupConfig{
		ListenAddr:           ":0",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              1,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)
	w.externalAddr = "self-sentinel:0"
	return w
}

func setDiscoveryPeers(w *Warmup, peers []string) {
	w.discovery.mu.Lock()
	w.discovery.peers = peers
	w.discovery.mu.Unlock()
}

func TestPolling_RemovesPeerOnConsecutiveFailures(t *testing.T) {
	// Boot a server, capture its address, then close it. Connections to
	// that address now refuse fast — perfect for exercising the failure
	// threshold without slow timeouts.
	srv := httptest.NewServer(http.NotFoundHandler())
	deadAddr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	w := pollTestWarmup(t)
	setDiscoveryPeers(w, []string{deadAddr})
	w.availability.UpdateRemote(deadAddr, []string{"chunks/x"})
	if !w.availability.PeerHas(deadAddr, "chunks/x") {
		t.Fatal("setup: tracker should hold dead peer")
	}

	lastSeen := make(map[string]int64)
	failures := make(map[string]int)
	known := make(map[string]struct{})

	for i := 1; i < maxConsecutivePollFailures; i++ {
		w.pollAvailabilityOnce(context.Background(), lastSeen, failures, known)
		if !w.availability.PeerHas(deadAddr, "chunks/x") {
			t.Fatalf("after %d failures, tracker entry cleared early (threshold %d)", i, maxConsecutivePollFailures)
		}
	}
	w.pollAvailabilityOnce(context.Background(), lastSeen, failures, known)
	if w.availability.PeerHas(deadAddr, "chunks/x") {
		t.Errorf("after %d consecutive failures, tracker still holds dead peer", maxConsecutivePollFailures)
	}
}

func TestPolling_RemovesPeerOnDiscoveryDrop(t *testing.T) {
	// Live server replying to /available; we simulate the peer leaving
	// discovery (DNS removal, scaled-down replica) by clearing the peer
	// list between sweeps. The tracker entry must be cleared as soon as
	// the peer disappears, before any failure threshold accrues.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/available" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"blocks":[],"updated_at":0}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	w := pollTestWarmup(t)
	setDiscoveryPeers(w, []string{addr})
	w.availability.UpdateRemote(addr, []string{"chunks/x"})

	lastSeen := make(map[string]int64)
	failures := make(map[string]int)
	known := make(map[string]struct{})

	w.pollAvailabilityOnce(context.Background(), lastSeen, failures, known)
	if !w.availability.PeerHas(addr, "chunks/x") {
		t.Fatal("first sweep cleared a healthy peer")
	}

	setDiscoveryPeers(w, nil)

	w.pollAvailabilityOnce(context.Background(), lastSeen, failures, known)
	if w.availability.PeerHas(addr, "chunks/x") {
		t.Errorf("after discovery drop, tracker still holds %s", addr)
	}
}

func TestPolling_SkipsSelf(t *testing.T) {
	// pollAvailabilityOnce must not poll itself; doing so wastes a
	// roundtrip and could lead to recursive announcements.
	w := pollTestWarmup(t)
	setDiscoveryPeers(w, []string{w.externalAddr})

	lastSeen := make(map[string]int64)
	failures := make(map[string]int)
	known := make(map[string]struct{})
	w.pollAvailabilityOnce(context.Background(), lastSeen, failures, known)

	if _, ok := known[w.externalAddr]; ok {
		t.Errorf("self-address %q ended up in known set", w.externalAddr)
	}
}

func TestWarmup_PortExtraction(t *testing.T) {
	config := WarmupConfig{
		ListenAddr:           ":8080",
		DiscoveryInterval:    time.Second,
		AvailabilityInterval: time.Second,
		Threads:              2,
		CacheDir:             t.TempDir(),
		BlockSize:            4 * 1024 * 1024,
	}
	w := NewWarmup(config, nil, nil)
	if w == nil {
		t.Fatal("NewWarmup returned nil")
	}
	// The discovery should have been created (we can't directly check defaultPort
	// but we verify construction didn't panic).
}
