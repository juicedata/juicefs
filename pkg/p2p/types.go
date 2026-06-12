package p2p

import (
	"sync/atomic"
	"time"
)

type BlockState int32

const (
	BlockPending     BlockState = 0
	BlockDownloading BlockState = 1
	BlockDone        BlockState = 2
	BlockFailed      BlockState = 3 // terminal: exhausted retry budget
)

type Block struct {
	Key      string // Object storage key (e.g., "chunks/0/0/100_0_4194304")
	SliceID  uint64
	Index    int
	Size     int
	state    atomic.Int32
	doneAt   atomic.Int64
	attempts atomic.Int32 // count of failed fetch attempts so far
}

// State returns the current block state.
func (b *Block) State() BlockState {
	return BlockState(b.state.Load())
}

// IsTerminal reports whether the block is in a state that contributes to
// completion (Done or Failed). Pending/Downloading do not count.
func (b *Block) IsTerminal() bool {
	s := b.State()
	return s == BlockDone || s == BlockFailed
}

// TryDownload atomically transitions Pending -> Downloading (CAS). Returns true if succeeded.
func (b *Block) TryDownload() bool {
	return b.state.CompareAndSwap(int32(BlockPending), int32(BlockDownloading))
}

// MarkDone transitions Downloading -> Done, records timestamp.
func (b *Block) MarkDone() {
	b.doneAt.Store(time.Now().UnixMilli())
	b.state.Store(int32(BlockDone))
}

// MarkFailed transitions to Failed (terminal). Used when the retry budget is
// exhausted, e.g. the block was deleted by slice compaction mid-warmup.
func (b *Block) MarkFailed() {
	b.state.Store(int32(BlockFailed))
}

// ResetToPending transitions Downloading -> Pending (on failure).
func (b *Block) ResetToPending() {
	b.state.CompareAndSwap(int32(BlockDownloading), int32(BlockPending))
}

// IncrAttempts atomically increments the failed-attempt counter and returns
// the new value. The fetcher uses this to decide whether to retry or abandon.
func (b *Block) IncrAttempts() int32 {
	return b.attempts.Add(1)
}

// DoneAt returns unix millis when marked Done, or 0.
func (b *Block) DoneAt() int64 {
	return b.doneAt.Load()
}

type Peer struct {
	Addr    string // "host:port"
	UUID    string
	Healthy bool
}

type WarmupConfig struct {
	// P2P settings
	ListenAddr           string
	DiscoveryInterval    time.Duration
	AvailabilityInterval time.Duration
	Threads              int
	KeepAlive            bool
	KeepAliveTimeout     time.Duration
	// MinPeers is the minimum total peer count (INCLUDING self) to wait for.
	// Acts as a synchronization barrier, not a fixed cluster size — more
	// peers proceed immediately. 0 or 1 = solo (manifest sharing disabled).
	MinPeers int

	// Manifest sharing (active when MinPeers > 1 and not Disabled): the
	// lexicographically smallest peer scans meta and uploads a manifest;
	// followers download it, avoiding redundant meta-engine load.
	DisableManifestSharing bool
	LeaderTimeout          time.Duration // follower max wait for leader's manifest (0=use default)
	// ManifestName is the manifest's object-storage basename. Empty = use a
	// content-addressable hash. All peers must pass the same value to agree
	// on the key.
	ManifestName string

	// Discovery sources
	PeersDNSSRV string
	PeersDNSA   string
	PeersList   string

	// JuiceFS cache settings
	CacheDir   string
	BlockSize  int
	HashPrefix bool
	// Compress is the volume's block compression algorithm ("lz4", "zstd",
	// "none" or empty), sourced from format.Compression. Storage holds
	// compressed bytes but the on-disk cache holds decompressed bytes; the
	// Fetcher decompresses at the storage-fetch boundary so cache files
	// match the layout mount's openCacheFile expects. Mismatched values
	// cause mount to reject every cached block.
	Compress string

	// DownloadLimit is the object-storage download cap in bytes/second
	// (0 = unlimited). Peer-to-peer transfers are not throttled.
	DownloadLimit int64

	// CacheSize is the hard cap on cumulative cached bytes (0 = unlimited).
	// Counted as: bytes from pre-existing files at scan + bytes written
	// during this run. Workers race the load-check-add window so peak
	// usage may exceed CacheSize by up to Threads * BlockSize.
	CacheSize int64
}

// keepAliveTimeoutIgnored reports whether KeepAliveTimeout is set without
// KeepAlive — Run() skips the keep-alive phase, so the timer never starts.
func (c WarmupConfig) keepAliveTimeoutIgnored() bool {
	return !c.KeepAlive && c.KeepAliveTimeout > 0
}
