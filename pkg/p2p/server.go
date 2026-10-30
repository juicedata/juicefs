package p2p

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// Server serves block data and availability info to peers via HTTP.
type Server struct {
	uuid         string
	availability *AvailabilityTracker
	cacheDir     string
	totalBlocks  atomic.Int64
	doneBlocks   atomic.Int64

	// Additive /status fields, pushed by the orchestrator like SetProgress.
	// Lock-free: slow probe reads never contend with the per-tick writers.
	role         atomic.Value // string; "" until election decides
	peers        atomic.Int64 // currently alive discovered peers (excluding self)
	fromPeers    atomic.Int64
	fromStorage  atomic.Int64
	fromCache    atomic.Int64
	skipped      atomic.Int64
	failed       atomic.Int64
	bytesPeer    atomic.Int64
	bytesStorage atomic.Int64
}

// NewServer creates a Server with the given identity, tracker, and cache directory.
func NewServer(uuid string, availability *AvailabilityTracker, cacheDir string) *Server {
	return &Server{
		uuid:         uuid,
		availability: availability,
		cacheDir:     cacheDir,
	}
}

// SetProgress updates the total and done block counters atomically.
func (s *Server) SetProgress(total, done int) {
	s.totalBlocks.Store(int64(total))
	s.doneBlocks.Store(int64(done))
}

// SetRole records this node's leader-election role ("leader" or "follower").
// Set once, after election decides.
func (s *Server) SetRole(role string) {
	s.role.Store(role)
}

// SetPeers records the number of currently alive discovered peers (excluding
// self). Pushed periodically as discovery adds and drops peers.
func (s *Server) SetPeers(n int) {
	s.peers.Store(int64(n))
}

// SetStats records a snapshot of Fetcher.Stats() for /status. Argument order
// matches Fetcher.Stats's return order so the orchestrator can forward it
// directly.
func (s *Server) SetStats(fromPeers, fromStorage, fromCache, skipped, failed, bytesPeer, bytesStorage int64) {
	s.fromPeers.Store(fromPeers)
	s.fromStorage.Store(fromStorage)
	s.fromCache.Store(fromCache)
	s.skipped.Store(skipped)
	s.failed.Store(failed)
	s.bytesPeer.Store(bytesPeer)
	s.bytesStorage.Store(bytesStorage)
}

// Handler returns an http.ServeMux wired with all server routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/uuid", s.handleUUID)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/available", s.handleAvailable)
	mux.HandleFunc("/block/", s.handleBlock)
	return mux
}

// handleUUID responds with this node's UUID.
// GET /uuid → {"uuid": "..."}
func (s *Server) handleUUID(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"uuid": s.uuid})
}

// handleStatus reports overall download progress plus role, live peer count,
// and per-source block/byte counts, letting consumers read warmup state
// directly instead of parsing logs. Bytes are raw int64; consumers format them.
// GET /status → {"completed", "progress":{"total","done"}, "role", "peers",
//                "sources":{...}, "bytes":{...}}
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total := s.totalBlocks.Load()
	done := s.doneBlocks.Load()
	completed := total > 0 && done >= total

	// atomic.Value.Load is nil until the first SetRole; report "" so the field
	// is always present and parseable.
	role, _ := s.role.Load().(string)

	type progress struct {
		Total int64 `json:"total"`
		Done  int64 `json:"done"`
	}
	type sources struct {
		FromPeers   int64 `json:"from_peers"`
		FromStorage int64 `json:"from_storage"`
		FromCache   int64 `json:"from_cache"`
		Skipped     int64 `json:"skipped"`
		Failed      int64 `json:"failed"`
	}
	type bytesXfer struct {
		FromPeers   int64 `json:"from_peers"`
		FromStorage int64 `json:"from_storage"`
	}
	writeJSON(w, map[string]interface{}{
		"completed": completed,
		"progress": progress{
			Total: total,
			Done:  done,
		},
		"role":  role,
		"peers": s.peers.Load(),
		"sources": sources{
			FromPeers:   s.fromPeers.Load(),
			FromStorage: s.fromStorage.Load(),
			FromCache:   s.fromCache.Load(),
			Skipped:     s.skipped.Load(),
			Failed:      s.failed.Load(),
		},
		"bytes": bytesXfer{
			FromPeers:   s.bytesPeer.Load(),
			FromStorage: s.bytesStorage.Load(),
		},
	})
}

// handleAvailable returns locally available block keys, optionally filtered by
// a ?since=<unix-millis> query parameter.
// GET /available          → {"blocks": [...], "updated_at": ts}
// GET /available?since=ts → incremental subset
func (s *Server) handleAvailable(w http.ResponseWriter, r *http.Request) {
	var sinceMs int64
	if v := r.URL.Query().Get("since"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid since parameter", http.StatusBadRequest)
			return
		}
		sinceMs = parsed
	}

	keys, updatedAt := s.availability.LocalBlocksSince(sinceMs)
	if keys == nil {
		keys = []string{}
	}

	writeJSON(w, map[string]interface{}{
		"blocks":     keys,
		"updated_at": updatedAt,
	})
}

// validBlockKey rejects URL-derived keys whose shape could trigger filesystem
// traversal. JuiceFS cache keys are of the form "chunks/<dirs>/<id>_<idx>_<size>"
// — all segments are alphanumeric, so a key that path.Clean rewrites or that
// contains absolute-path or parent-dir markers is never legitimate.
func validBlockKey(key string) bool {
	if key == "" {
		return false
	}
	// Reject absolute paths (POSIX leading "/", Windows leading "\" or drive
	// letter). filepath.Join silently overrides the prefix on Windows when
	// given a path with a drive letter, and a leading "/" is suspicious
	// regardless.
	if key[0] == '/' || key[0] == '\\' {
		return false
	}
	if len(key) >= 2 && key[1] == ':' { // Windows drive letter like "C:"
		return false
	}
	// Reject any "." or ".." segment that path.Clean would collapse.
	for _, seg := range strings.Split(key, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	// Final check: anything that path.Clean rewrites (duplicate slashes,
	// trailing slash, embedded "./", etc.) is rejected so the on-wire key
	// matches the cached filename exactly.
	if path.Clean(key) != key {
		return false
	}
	return true
}

// handleBlock serves raw block data from the local disk cache.
// GET /block/<key> → binary file contents, or 404
// The key is everything after "/block/" in the URL path.
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/block/")
	if !validBlockKey(key) {
		http.NotFound(w, r)
		return
	}

	rawRoot := filepath.Join(s.cacheDir, "raw")
	blockPath := filepath.Join(rawRoot, key)

	// Defense in depth: even after the shape check, confirm the resolved
	// path is still under {cacheDir}/raw. filepath.Rel returns a path
	// starting with ".." when the target escapes the root.
	rel, err := filepath.Rel(rawRoot, blockPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(blockPath)
	if err != nil {
		// Treat every read error as 404 so the response does not leak
		// whether the path exists but is unreadable (e.g. permission
		// denied); log unexpected errors for the operator.
		if !os.IsNotExist(err) {
			logger.WithError(err).Debugf("handleBlock: read %q failed", blockPath)
		}
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// writeJSON encodes v as JSON and writes it to w with Content-Type
// application/json and status 200.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
