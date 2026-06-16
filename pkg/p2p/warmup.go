package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juju/ratelimit"
)

// Warmup orchestrates the P2P warmup: resolve paths to blocks, discover
// peers, schedule and run fetches, serve blocks to other peers.
type Warmup struct {
	config       WarmupConfig
	meta         meta.Meta
	storage      object.ObjectStorage
	uuid         string
	discovery    *PeerDiscovery
	availability *AvailabilityTracker
	server       *Server
	fetcher      *Fetcher
	resolver     *MetaResolver
	httpServer   *http.Server
	listenAddr   string
	externalAddr string // address other peers see us as; populated by filterSelf via UUID match
}

// NewWarmup creates a Warmup instance with all sub-components wired together.
func NewWarmup(config WarmupConfig, m meta.Meta, storage object.ObjectStorage) *Warmup {
	uid := uuid.New().String()

	availability := NewAvailabilityTracker()

	// Extract port from config.ListenAddr for DNS A discovery default.
	defaultPort := 0
	if _, portStr, err := net.SplitHostPort(config.ListenAddr); err == nil {
		if p, err2 := net.LookupPort("tcp", portStr); err2 == nil {
			defaultPort = p
		}
	}

	discovery := NewPeerDiscovery(config.PeersDNSSRV, config.PeersDNSA, config.PeersList, defaultPort)

	server := NewServer(uid, availability, config.CacheDir)

	// 5s peer-fetch timeout: enough for a 4 MiB block on slow LANs, but
	// ~6x faster failure detection than the 30s default — storage fallback
	// is cheaper than waiting on a dead peer.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	// NewCompressor returns nil for unknown algos. Abort loudly: silently
	// using noOp would write compressed bytes to cache and mount would
	// later reject every block.
	compressor := compress.NewCompressor(config.Compress)
	if compressor == nil {
		logger.Fatalf("unsupported compression algorithm %q (expected lz4, zstd, or none)", config.Compress)
	}
	fetcher := NewFetcher(storage, config.CacheDir, config.BlockSize, httpClient, compressor)
	fetcher.SetAvailability(availability)
	if config.CacheSize > 0 {
		fetcher.SetCacheSize(config.CacheSize)
	}
	if config.DownloadLimit > 0 {
		// 0.85 dampening / 10% burst — same convention as pkg/chunk and
		// pkg/sync, keeps observed rate close to the cap without large bursts.
		fill := float64(config.DownloadLimit) * 0.85
		burst := config.DownloadLimit / 10
		if burst < 1 {
			burst = 1
		}
		fetcher.SetDownloadLimit(ratelimit.NewBucketWithRate(fill, burst))
	}

	var resolver *MetaResolver
	if m != nil {
		resolver = NewMetaResolver(m, config.BlockSize, config.HashPrefix)
	}

	return &Warmup{
		config:       config,
		meta:         m,
		storage:      storage,
		uuid:         uid,
		discovery:    discovery,
		availability: availability,
		server:       server,
		fetcher:      fetcher,
		resolver:     resolver,
	}
}

// Run executes the full warmup flow and optionally enters keep-alive to keep
// serving blocks to peers after the local fetch finishes.
func (w *Warmup) Run(ctx context.Context, paths []string) error {
	startTime := time.Now()

	// 1. Start HTTP server.
	ln, err := net.Listen("tcp", w.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", w.config.ListenAddr, err)
	}
	w.listenAddr = ln.Addr().String()
	w.httpServer = &http.Server{Handler: w.server.Handler()}

	go func() {
		if err := w.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Error("HTTP server error")
		}
	}()
	logger.Infof("P2P server listening on %s (uuid=%s)", w.listenAddr, w.uuid)
	if w.config.CacheSize > 0 {
		logger.Infof("cache-size cap: %s (pass --cache-size 0 for unlimited)", humanize.IBytes(uint64(w.config.CacheSize)))
	} else {
		logger.Warnf("cache-size cap disabled (--cache-size 0); warmup will keep writing until the disk fills")
	}

	// 1b. Announce pre-cached blocks via /available and skip them in the
	// fetch. The byte total seeds the fetcher's cache-size accounting so the
	// cap applies to total on-disk bytes, not just bytes this run wrote.
	if n, bytes := w.scanExistingCache(); n > 0 {
		logger.Infof("found %d pre-cached blocks (%s) in %s", n, humanize.IBytes(uint64(bytes)), w.config.CacheDir)
		w.fetcher.AddCacheUsed(bytes)
	}

	// 2. Initial peer discovery + filter self. MinPeers counts total peers
	// INCLUDING self, so we wait until len(peers) reaches MinPeers-1. More
	// than the threshold proceeds immediately; late joiners use the
	// manifest-reuse fast-path.
	peers, err := w.discovery.Resolve(ctx)
	if err != nil {
		logger.WithError(err).Warn("initial peer discovery failed")
	}
	peers = w.filterSelf(ctx, peers)

	if w.config.MinPeers > 1 && len(peers) < w.config.MinPeers-1 {
		logger.Infof("waiting for at least %d total peers (currently %d others discovered)...", w.config.MinPeers, len(peers))
		peers = w.waitForPeers(ctx, w.config.MinPeers-1)
	}
	logger.Infof("discovered %d peers (excluding self)", len(peers))

	// DNS discovery without --min-peers >= 2 disables manifest sharing —
	// each peer scans meta independently. Likely a misconfig.
	if w.config.MinPeers <= 1 && (w.config.PeersDNSSRV != "" || w.config.PeersDNSA != "") {
		logger.Warn("DNS peer discovery configured without --min-peers (>= 2); manifest sharing disabled. Each peer will independently scan metadata, increasing meta-engine load. Set --min-peers to enable coordinated warmup.")
	}

	// Multi-peer mode + self not found in peer list → effectiveAddr falls
	// back to listenAddr. Wildcards ("[::]:port", "0.0.0.0:port") then make
	// every peer's identity collide and election cannot agree.
	if w.config.MinPeers > 1 && w.externalAddr == "" {
		logger.Warnf("self external address not discovered; using listen address %q for election. If listen is a wildcard, leader election and partition assignment will not agree across peers — pass --listen <self-IP>:port or ensure self is reachable via the configured discovery source.", w.listenAddr)
	}

	// --keep-alive-timeout only applies inside the keep-alive phase; with
	// --keep-alive=off, step 11 is skipped and the timer never starts.
	if w.config.keepAliveTimeoutIgnored() {
		logger.Warnf("--keep-alive-timeout=%s is set but --keep-alive=off; the timeout will be ignored and the process will exit immediately after warmup. Use --keep-alive=peers or =forever to enable post-warmup serving.", w.config.KeepAliveTimeout)
	}

	// Publish the leader role + initial peer count to /status (peers is refreshed later by the availability poll).
	manifestSharing := w.config.MinPeers > 1 && !w.config.DisableManifestSharing
	role := "leader"
	if manifestSharing && !isLeader(peers, w.effectiveAddr()) {
		role = "follower"
	}
	w.server.SetRole(role)
	w.server.SetPeers(len(peers))

	// 3. Resolve metadata. With manifest sharing on, only the leader scans
	// meta; followers download the leader's manifest from object storage —
	// collapsing meta-engine load from O(N peers × M files) to O(M files).
	blocks, err := w.resolveOrDownloadManifest(ctx, paths, peers)
	if err != nil {
		w.shutdownHTTP()
		return err
	}
	total := len(blocks)
	if total == 0 {
		logger.Info("no blocks to warm up")
		w.shutdownHTTP()
		return nil
	}
	w.server.SetProgress(total, 0)
	fetchStart := time.Now()

	// 4. Start background discovery loop.
	var bgWg sync.WaitGroup
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()

	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		w.discovery.RunLoop(bgCtx, w.config.DiscoveryInterval)
	}()

	// 5. Start availability polling loop.
	bgWg.Add(1)
	go w.runAvailabilityPolling(bgCtx, &bgWg)

	// 6. Build the fetch queue. Each peer orders the full block set by
	// hash(key, self): every peer holds every block (no orphaned ownership
	// when a peer dies), but the queue fronts are statistically disjoint, so
	// initial concurrent storage hits land on different blocks until
	// availability polling propagates.
	scheduler := NewScheduler(blocks)
	queue := NewFetchQueue(total)
	queue.PushAll(scheduler.OrderForPeer(w.effectiveAddr()))

	// 7. Start fetch workers.
	workersWg := w.fetcher.RunWorkers(ctx, queue, w.availability, w.config.Threads)

	// 8. Progress monitor. Cancelled at completion so the keep-alive phase
	// doesn't keep emitting "600/600 done, 0 B/s" lines.
	monitorCtx, monitorCancel := context.WithCancel(bgCtx)
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		w.monitorProgress(monitorCtx, blocks, queue)
	}()

	// 9. Wait for completion: poll until all blocks are done, then close the queue.
	w.waitForCompletion(ctx, blocks, queue)
	workersWg.Wait()
	monitorCancel()

	// 10. Print final stats.
	fromPeers, fromStorage, fromCache, skippedFull, failed, bytesPeer, bytesStorage := w.fetcher.Stats()
	// Push the terminal snapshot: the progress monitor stops at monitorCancel
	// above, so without this the keep-alive-phase /status would report the
	// last 5s-tick numbers instead of the final totals.
	w.server.SetStats(fromPeers, fromStorage, fromCache, skippedFull, failed, bytesPeer, bytesStorage)
	logger.Infof("warmup complete: %d blocks (%d from peers, %d from storage, %d from cache, %d skipped, %d failed)",
		total, fromPeers, fromStorage, fromCache, skippedFull, failed)
	if skippedFull > 0 {
		logger.Warnf("%d blocks not cached: --cache-size cap reached; raise it or shrink the warmup path set to cover more", skippedFull)
	}
	logger.Infof("bytes transferred: %s from peers, %s from storage",
		humanize.IBytes(uint64(bytesPeer)), humanize.IBytes(uint64(bytesStorage)))
	elapsed := time.Since(startTime)
	fetchElapsed := time.Since(fetchStart)
	var avgRate, fetchRate uint64
	if secs := elapsed.Seconds(); secs > 0 {
		avgRate = uint64(float64(bytesPeer+bytesStorage) / secs)
	}
	if secs := fetchElapsed.Seconds(); secs > 0 {
		fetchRate = uint64(float64(bytesPeer+bytesStorage) / secs)
	}
	logger.Infof("elapsed: %s total, %s fetch; average throughput: %s/s total, %s/s fetch",
		elapsed.Round(time.Millisecond), fetchElapsed.Round(time.Millisecond),
		humanize.IBytes(avgRate), humanize.IBytes(fetchRate))

	// 11. Keep-alive if configured.
	switch w.config.KeepAlive {
	case KeepAlivePeers:
		logger.Info("keep-alive=peers: serving blocks until all peers complete")
		w.keepAliveUntilPeersDone(bgCtx)
	case KeepAliveForever:
		logger.Info("keep-alive=forever: serving blocks to peers")
		w.keepAlive(bgCtx)
	}

	// 12. Graceful shutdown.
	bgCancel()
	bgWg.Wait()
	w.shutdownHTTP()
	return nil
}

const (
	defaultLeaderTimeout = 10 * time.Minute
	manifestPollInterval = 5 * time.Second
)

// resolveOrDownloadManifest selects the block list source:
//   - solo / sharing disabled → resolve via meta engine.
//   - sharing on + valid manifest exists → reuse (covers scale-out where a
//     new low-address peer would otherwise mis-elect and re-scan meta).
//   - sharing on, leader → resolve + upload manifest.
//   - sharing on, follower → poll until the leader's manifest appears.
func (w *Warmup) resolveOrDownloadManifest(ctx context.Context, paths, peers []string) ([]*Block, error) {
	// MinPeers counts total peers including self, so values <= 1 mean
	// solo (no manifest sharing needed even if other addresses leak in).
	manifestSharing := w.config.MinPeers > 1 && !w.config.DisableManifestSharing

	// Manifest-reuse fast-path: skip meta scan and follower polling whenever
	// a valid manifest is already published — both roles try this first.
	if manifestSharing {
		if m := w.tryFetchValidManifest(ctx, paths); m != nil {
			logger.Infof("found existing manifest: %d blocks (%d bytes total); skipping resolve",
				m.TotalBlocks, m.TotalBytes)
			return ManifestToBlocks(m), nil
		}
	}

	if manifestSharing && !isLeader(peers, w.effectiveAddr()) {
		timeout := w.config.LeaderTimeout
		if timeout <= 0 {
			timeout = defaultLeaderTimeout
		}
		logger.Infof("follower role; waiting for leader's manifest (timeout=%v)", timeout)
		manifest, err := w.downloadManifest(ctx, paths, timeout, manifestPollInterval)
		if err != nil {
			return nil, fmt.Errorf("download manifest: %w", err)
		}
		if err := manifest.Validate(paths, w.config.BlockSize, w.config.HashPrefix); err != nil {
			return nil, fmt.Errorf("manifest content mismatch (another warmup may be using --manifest-name=%q): %w", w.config.ManifestName, err)
		}
		blocks := ManifestToBlocks(manifest)
		logger.Infof("loaded manifest: %d blocks (%d bytes total)", manifest.TotalBlocks, manifest.TotalBytes)
		return blocks, nil
	}

	// Leader or solo: resolve via meta engine.
	if w.resolver == nil {
		return nil, fmt.Errorf("meta resolver not available (nil meta engine)")
	}
	// Wrap the caller's ctx so SIGTERM/SIGINT cancels the meta scan instead
	// of leaving it to run to completion on the deadlineless background ctx.
	mctx := meta.WrapContext(ctx)
	blocks, err := w.resolver.Resolve(mctx, paths)
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}
	logger.Infof("resolved %d blocks from %d paths", len(blocks), len(paths))

	if manifestSharing {
		logger.Info("leader role; uploading manifest for followers")
		if upErr := w.uploadManifest(ctx, paths, blocks); upErr != nil {
			// Don't abort the leader's own warmup; followers will time out.
			logger.WithError(upErr).Warn("upload manifest failed; followers may time out")
		}
	}
	return blocks, nil
}

// isLeader reports whether selfAddr wins the leader election (lexicographically
// smallest address among peers ∪ self). peers must exclude selfAddr. The rule
// is deterministic, so all peers with the same view agree without coordination.
func isLeader(peers []string, selfAddr string) bool {
	for _, p := range peers {
		if p < selfAddr {
			return false
		}
	}
	return true
}

// countTerminalBlocks returns Done + Failed counts. Both are terminal, so
// the completion check uses their sum — a Failed block won't become Done and
// blocking on it would hang the warmup.
func countTerminalBlocks(blocks []*Block) (done int, failed int) {
	for _, b := range blocks {
		switch b.State() {
		case BlockDone:
			done++
		case BlockFailed:
			failed++
		}
	}
	return
}

// cacheChecksumBlock matches pkg/chunk/disk_cache.go csBlock — the granularity
// at which mount writes 4-byte CRC32C checksums when CacheChecksum is enabled.
const cacheChecksumBlock = 32 << 10

// cacheTierIDLength matches pkg/chunk/disk_cache.go tierIDLength — the
// single-byte tier marker mount may append to cache files when multi-tier
// storage is configured.
const cacheTierIDLength = 1

// parseLogicalSize extracts the logical block size from a key like
// "chunks/0/0/100_0_4194304" (mirrors pkg/chunk/cached_store.go's
// parseObjOrigSize). Returns 0 for malformed keys so callers can reject them.
func parseLogicalSize(key string) int {
	p := strings.LastIndexByte(key, '_')
	if p < 0 {
		return 0
	}
	n, err := strconv.Atoi(key[p+1:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// validCacheFileSize accepts the four layouts pkg/chunk/disk_cache.go's
// openCacheFile accepts. Mount may append an optional checksum trailer and/or
// a 1-byte tier marker, so the on-disk size minus the logical size must equal
// one of:
//
//	① 0                            (no checksum, no tier)
//	② cacheTierIDLength            (no checksum, tier)
//	③ checksumLen                  (checksum, no tier)
//	④ checksumLen + cacheTierIDLength (checksum, tier)
//
// Anything else is corrupt or partial and must not be announced.
//
// Keep this in lock-step with openCacheFile — every layout mount accepts on
// read must also count as a cache hit here, otherwise p2p-warmup re-downloads
// blocks the mount considers fine.
func validCacheFileSize(onDisk int64, logical int) bool {
	if logical <= 0 {
		return false
	}
	delta := onDisk - int64(logical)
	if delta == 0 || delta == cacheTierIDLength {
		return true
	}
	checksumLen := int64((logical-1)/cacheChecksumBlock+1) * 4
	return delta == checksumLen || delta == checksumLen+cacheTierIDLength
}

// maxCacheFileSize is the largest layout openCacheFile will accept for the
// given logical size: logical + full checksum trailer + tier byte. Used by
// FetchFromPeer to cap peer-served bodies — without a cap, a misbehaving
// peer could stream arbitrary bytes into memory.
func maxCacheFileSize(logical int) int64 {
	if logical <= 0 {
		return 0
	}
	checksumLen := int64((logical-1)/cacheChecksumBlock+1) * 4
	return int64(logical) + checksumLen + cacheTierIDLength
}

// scanExistingCache walks {cacheDir}/raw and announces well-formed block
// files via the availability tracker, returning the count and total bytes of
// accepted files. Missing roots are treated as empty. Files failing
// validCacheFileSize are skipped — primarily truncated cache files left by a
// SIGKILL/OOM mid-write (CacheBlock's os.WriteFile is not atomic), which
// would otherwise mislead peers into fetching unusable bytes. The byte
// total seeds Fetcher.cacheUsed so --cache-size accounts for files
// pre-existing on disk, not just what this run writes.
func (w *Warmup) scanExistingCache() (int, int64) {
	root := filepath.Join(w.config.CacheDir, "raw")
	count := 0
	var bytes int64
	skipped := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		key := filepath.ToSlash(rel)

		fi, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		logical := parseLogicalSize(key)
		if !validCacheFileSize(fi.Size(), logical) {
			skipped++
			logger.Debugf("scanExistingCache: skipping %q (size %d, logical %d)", key, fi.Size(), logical)
			return nil
		}

		w.availability.MarkLocal(key)
		count++
		bytes += fi.Size()
		return nil
	})
	if err != nil {
		logger.Debugf("scanExistingCache: %v", err)
	}
	if skipped > 0 {
		logger.Infof("scanExistingCache: skipped %d cache files with unexpected size", skipped)
	}
	return count, bytes
}

// filterSelf removes our own address from peers via /uuid probe and records
// the matching address as externalAddr — the identity peers see — used by
// leader election and partition assignment.
func (w *Warmup) filterSelf(ctx context.Context, peers []string) []string {
	var filtered []string
	for _, addr := range peers {
		peerUUID, err := w.fetchUUID(ctx, addr)
		if err != nil {
			logger.WithError(err).Debugf("skipping unreachable peer %s", addr)
			continue
		}
		if peerUUID == w.uuid {
			if w.externalAddr == "" {
				w.externalAddr = addr
				logger.Infof("self external address: %s", addr)
			}
			continue
		}
		filtered = append(filtered, addr)
	}
	return filtered
}

// effectiveAddr is the identity used for leader election and partition
// assignment. Prefers externalAddr; falls back to listenAddr when filterSelf
// failed to find self (Run warns when this fallback is taken in multi-peer
// mode, since wildcard listen addresses break election agreement).
func (w *Warmup) effectiveAddr() string {
	if w.externalAddr != "" {
		return w.externalAddr
	}
	return w.listenAddr
}

// fetchUUID issues GET http://{addr}/uuid and returns the peer's UUID.
func (w *Warmup) fetchUUID(ctx context.Context, addr string) (string, error) {
	url := fmt.Sprintf("http://%s/uuid", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	var body struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode uuid from %s: %w", url, err)
	}
	return body.UUID, nil
}

// maxConsecutivePollFailures is the threshold for dropping a peer from the
// tracker. Beyond this, FindPeerWith would keep returning a dead address and
// every routed fetch would burn the 5s peer timeout before storage fallback.
const maxConsecutivePollFailures = 3

// runAvailabilityPolling polls each peer for block availability and removes
// peers that disappear from discovery or fail too many consecutive polls,
// preventing FindPeerWith from routing fetches to dead peers.
func (w *Warmup) runAvailabilityPolling(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	lastSeen := make(map[string]int64)
	failures := make(map[string]int)
	known := make(map[string]struct{})

	ticker := time.NewTicker(w.config.AvailabilityInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pollAvailabilityOnce(ctx, lastSeen, failures, known)
		}
	}
}

// pollAvailabilityOnce runs a single sweep. Extracted so tests can exercise
// the failure-threshold and discovery-diff branches without driving a ticker.
func (w *Warmup) pollAvailabilityOnce(ctx context.Context, lastSeen map[string]int64, failures map[string]int, known map[string]struct{}) {
	self := w.effectiveAddr()
	current := make(map[string]struct{})
	for _, addr := range w.discovery.Peers() {
		if addr == self {
			continue
		}
		current[addr] = struct{}{}
	}

	// Discovery dropped these peers (DNS removal, scaled-down replicas, ...).
	for addr := range known {
		if _, ok := current[addr]; !ok {
			logger.Infof("peer %s left discovery; clearing availability", addr)
			w.availability.RemovePeer(addr)
			delete(lastSeen, addr)
			delete(failures, addr)
			delete(known, addr)
		}
	}
	for addr := range current {
		known[addr] = struct{}{}
	}

	// Refresh /status with the current live (self-excluded) peer count.
	w.server.SetPeers(len(current))

	for addr := range current {
		sinceMs := lastSeen[addr]
		updatedAt, err := w.fetcher.PollAvailability(ctx, addr, sinceMs)
		if err != nil {
			failures[addr]++
			logger.WithError(err).Debugf("availability poll failed for %s (%d/%d)", addr, failures[addr], maxConsecutivePollFailures)
			if failures[addr] >= maxConsecutivePollFailures {
				logger.Warnf("peer %s failed %d consecutive polls; clearing availability", addr, failures[addr])
				w.availability.RemovePeer(addr)
				delete(lastSeen, addr)
				delete(failures, addr)
			}
			continue
		}
		failures[addr] = 0
		if updatedAt > sinceMs {
			lastSeen[addr] = updatedAt
		}
	}
}

// monitorProgress logs progress every 5 seconds until ctx is cancelled.
func (w *Warmup) monitorProgress(ctx context.Context, blocks []*Block, queue *FetchQueue) {
	const interval = 5 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	total := len(blocks)
	var prevPeer, prevStore int64
	prevTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			done, failed := countTerminalBlocks(blocks)
			w.server.SetProgress(total, done)
			pending := queue.Len()
			fromPeers, fromStorage, fromCache, skippedFull, failedFetch, bytesPeer, bytesStore := w.fetcher.Stats()
			w.server.SetStats(fromPeers, fromStorage, fromCache, skippedFull, failedFetch, bytesPeer, bytesStore)
			elapsed := now.Sub(prevTime).Seconds()
			var peerRate, storeRate uint64
			if elapsed > 0 {
				peerRate = uint64(float64(bytesPeer-prevPeer) / elapsed)
				storeRate = uint64(float64(bytesStore-prevStore) / elapsed)
			}
			prevPeer, prevStore, prevTime = bytesPeer, bytesStore, now
			logger.Infof("progress: %d/%d blocks done, %d failed, %d pending; transferred %s peers (%s/s), %s storage (%s/s)",
				done, total, failed, pending,
				humanize.IBytes(uint64(bytesPeer)), humanize.IBytes(peerRate),
				humanize.IBytes(uint64(bytesStore)), humanize.IBytes(storeRate))
		}
	}
}

// waitForCompletion polls until every block is terminal (Done or Failed),
// then closes the queue. Failed counts toward completion so an unfetchable
// block (e.g. deleted by slice compaction mid-warmup) cannot hang the run.
func (w *Warmup) waitForCompletion(ctx context.Context, blocks []*Block, queue *FetchQueue) {
	total := len(blocks)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			queue.Close()
			return
		case <-ticker.C:
			done, failed := countTerminalBlocks(blocks)
			w.server.SetProgress(total, done)
			if done+failed >= total {
				queue.Close()
				return
			}
		}
	}
}

// peersAllComplete polls each live (self-excluded) peer's /status and reports
// whether every live peer is done warming. An unreachable peer (left
// discovery, crashed, or already exited) is treated as complete — otherwise a
// single dead peer would block exit forever. Returns true when no live peers
// remain.
func (w *Warmup) peersAllComplete(ctx context.Context) bool {
	self := w.effectiveAddr()
	for _, addr := range w.discovery.Peers() {
		if addr == self {
			continue
		}
		done, err := w.fetcher.PollCompleted(ctx, addr)
		if err != nil {
			logger.WithError(err).Debugf("status poll failed for %s; treating as complete", addr)
			continue
		}
		if !done {
			return false
		}
	}
	return true
}

// keepAliveUntilPeersDone keeps serving blocks until every live peer reports
// completion, then returns. It also returns early on SIGTERM/SIGINT, ctx
// cancellation, or — if set — KeepAliveTimeout. Peers that leave discovery are
// no longer awaited (see peersAllComplete), so a crashed peer cannot wedge the
// process; KeepAliveTimeout is a belt-and-suspenders upper bound.
func (w *Warmup) keepAliveUntilPeersDone(ctx context.Context) {
	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigStop()

	var timeoutCh <-chan time.Time
	if w.config.KeepAliveTimeout > 0 {
		timer := time.NewTimer(w.config.KeepAliveTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	// Check once up front so an already-finished cluster exits immediately
	// instead of waiting a full tick.
	if w.peersAllComplete(sigCtx) {
		logger.Info("all peers complete; shutting down")
		return
	}

	ticker := time.NewTicker(w.config.AvailabilityInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCtx.Done():
			logger.Info("received signal, shutting down")
			return
		case <-timeoutCh:
			logger.Info("keep-alive timeout reached")
			return
		case <-ticker.C:
			if w.peersAllComplete(sigCtx) {
				logger.Info("all peers complete; shutting down")
				return
			}
		}
	}
}

// keepAlive blocks until KeepAliveTimeout elapses, or — if no timeout is set —
// until SIGTERM/SIGINT or ctx cancellation.
func (w *Warmup) keepAlive(ctx context.Context) {
	if w.config.KeepAliveTimeout > 0 {
		timer := time.NewTimer(w.config.KeepAliveTimeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			logger.Info("keep-alive timeout reached")
			return
		}
	}

	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigStop()
	<-sigCtx.Done()
	logger.Info("received signal, shutting down")
}

// waitForPeers polls discovery until the expected number of peers is reached or ctx is cancelled.
func (w *Warmup) waitForPeers(ctx context.Context, expected int) []string {
	ticker := time.NewTicker(w.config.DiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return w.filterSelf(ctx, w.discovery.Peers())
		case <-ticker.C:
			resolved, _ := w.discovery.Resolve(ctx)
			peers := w.filterSelf(ctx, resolved)
			logger.Infof("peer discovery: %d/%d peers found", len(peers), expected)
			if len(peers) >= expected {
				return peers
			}
		}
	}
}

// shutdownHTTP gracefully shuts down the HTTP server with a 10-second timeout.
func (w *Warmup) shutdownHTTP() {
	if w.httpServer == nil {
		return
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.httpServer.Shutdown(shutCtx); err != nil {
		logger.WithError(err).Warn("HTTP server shutdown error")
	}
}
