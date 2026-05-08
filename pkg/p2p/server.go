package p2p

import (
	"encoding/json"
	"net/http"
	"os"
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

// handleStatus reports overall download progress.
// GET /status → {"completed": bool, "progress": {"total": N, "done": N}}
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total := s.totalBlocks.Load()
	done := s.doneBlocks.Load()
	completed := total > 0 && done >= total

	type progress struct {
		Total int64 `json:"total"`
		Done  int64 `json:"done"`
	}
	writeJSON(w, map[string]interface{}{
		"completed": completed,
		"progress": progress{
			Total: total,
			Done:  done,
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

// handleBlock serves raw block data from the local disk cache.
// GET /block/<key> → binary file contents, or 404
// The key is everything after "/block/" in the URL path.
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/block/")
	if key == "" {
		http.NotFound(w, r)
		return
	}

	path := filepath.Join(s.cacheDir, "raw", key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
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
