package p2p

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer_UUID(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("test-uuid-123", tracker, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/uuid", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if got := body["uuid"]; got != "test-uuid-123" {
		t.Errorf("expected uuid=test-uuid-123, got %q", got)
	}
}

func TestServer_Status(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, t.TempDir())
	srv.SetProgress(100, 40)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Completed bool `json:"completed"`
		Progress  struct {
			Total int64 `json:"total"`
			Done  int64 `json:"done"`
		} `json:"progress"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Completed {
		t.Error("expected completed=false")
	}
	if body.Progress.Total != 100 {
		t.Errorf("expected total=100, got %d", body.Progress.Total)
	}
	if body.Progress.Done != 40 {
		t.Errorf("expected done=40, got %d", body.Progress.Done)
	}
}

func TestServer_Status_Completed(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, t.TempDir())
	srv.SetProgress(50, 50)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var body struct {
		Completed bool `json:"completed"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !body.Completed {
		t.Error("expected completed=true when done==total")
	}
}

func TestServer_Status_ExtendedFields(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, t.TempDir())
	srv.SetProgress(6400, 6400)
	srv.SetRole("leader")
	srv.SetPeers(12)
	srv.SetStats(5800, 600, 0, 0, 0, 95563022336, 11811160064)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Completed bool `json:"completed"`
		Progress  struct {
			Total int64 `json:"total"`
			Done  int64 `json:"done"`
		} `json:"progress"`
		Role    string `json:"role"`
		Peers   int64  `json:"peers"`
		Sources struct {
			FromPeers   int64 `json:"from_peers"`
			FromStorage int64 `json:"from_storage"`
			FromCache   int64 `json:"from_cache"`
			Skipped     int64 `json:"skipped"`
			Failed      int64 `json:"failed"`
		} `json:"sources"`
		Bytes struct {
			FromPeers   int64 `json:"from_peers"`
			FromStorage int64 `json:"from_storage"`
		} `json:"bytes"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Baseline fields remain unchanged.
	if !body.Completed {
		t.Error("expected completed=true")
	}
	if body.Progress.Total != 6400 || body.Progress.Done != 6400 {
		t.Errorf("progress = %d/%d, want 6400/6400", body.Progress.Done, body.Progress.Total)
	}

	// Additive fields.
	if body.Role != "leader" {
		t.Errorf("role = %q, want %q", body.Role, "leader")
	}
	if body.Peers != 12 {
		t.Errorf("peers = %d, want 12", body.Peers)
	}
	if body.Sources.FromPeers != 5800 {
		t.Errorf("sources.from_peers = %d, want 5800", body.Sources.FromPeers)
	}
	if body.Sources.FromStorage != 600 {
		t.Errorf("sources.from_storage = %d, want 600", body.Sources.FromStorage)
	}
	if body.Sources.FromCache != 0 || body.Sources.Skipped != 0 || body.Sources.Failed != 0 {
		t.Errorf("sources zero-fields = cache:%d skipped:%d failed:%d, want all 0",
			body.Sources.FromCache, body.Sources.Skipped, body.Sources.Failed)
	}
	if body.Bytes.FromPeers != 95563022336 {
		t.Errorf("bytes.from_peers = %d, want 95563022336", body.Bytes.FromPeers)
	}
	if body.Bytes.FromStorage != 11811160064 {
		t.Errorf("bytes.from_storage = %d, want 11811160064", body.Bytes.FromStorage)
	}
}

// A freshly constructed Server (before any role/stats push) must still serve a
// well-formed /status: baseline fields present, additive fields at their zero
// values. Operators poll /status from the first probe, before election runs.
func TestServer_Status_DefaultsBeforePush(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Completed bool   `json:"completed"`
		Role      string `json:"role"`
		Peers     int64  `json:"peers"`
		Sources   struct {
			FromPeers int64 `json:"from_peers"`
		} `json:"sources"`
		Bytes struct {
			FromPeers int64 `json:"from_peers"`
		} `json:"bytes"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Completed {
		t.Error("expected completed=false for a fresh server")
	}
	if body.Role != "" {
		t.Errorf("role = %q, want empty before election", body.Role)
	}
	if body.Peers != 0 || body.Sources.FromPeers != 0 || body.Bytes.FromPeers != 0 {
		t.Errorf("expected zero-valued additive fields, got peers:%d sources.from_peers:%d bytes.from_peers:%d",
			body.Peers, body.Sources.FromPeers, body.Bytes.FromPeers)
	}
}

func TestServer_Available(t *testing.T) {
	tracker := NewAvailabilityTracker()
	tracker.MarkLocal("key1")
	tracker.MarkLocal("key2")

	srv := NewServer("u1", tracker, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/available", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Blocks    []string `json:"blocks"`
		UpdatedAt int64    `json:"updated_at"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(body.Blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d: %v", len(body.Blocks), body.Blocks)
	}
	if body.UpdatedAt <= 0 {
		t.Errorf("expected updated_at > 0, got %d", body.UpdatedAt)
	}
}

func TestServer_Available_Since(t *testing.T) {
	tracker := NewAvailabilityTracker()
	tracker.MarkLocal("key1")

	_, ts := tracker.LocalBlocksSince(0)

	// Ensure key2 gets a later timestamp
	time.Sleep(2 * time.Millisecond)
	tracker.MarkLocal("key2")

	srv := NewServer("u1", tracker, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/available?since="+itoa64(ts), nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Blocks []string `json:"blocks"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(body.Blocks) != 1 {
		t.Errorf("expected 1 block after since, got %d: %v", len(body.Blocks), body.Blocks)
	}
	if body.Blocks[0] != "key2" {
		t.Errorf("expected key2, got %q", body.Blocks[0])
	}
}

func TestServer_Block(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a fake block file at tmpDir/raw/chunks/0/0/1_0_100
	rawDir := filepath.Join(tmpDir, "raw", "chunks", "0", "0")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	blockData := []byte("hello block data")
	blockPath := filepath.Join(rawDir, "1_0_100")
	if err := os.WriteFile(blockPath, blockData, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/block/chunks/0/0/1_0_100", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %q", ct)
	}
	if got := rr.Body.Bytes(); string(got) != string(blockData) {
		t.Errorf("expected %q, got %q", blockData, got)
	}
}

func TestServer_Block_NotFound(t *testing.T) {
	tracker := NewAvailabilityTracker()
	srv := NewServer("u1", tracker, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/block/nonexistent", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// itoa64 is a helper to convert int64 to string for query params.
func itoa64(n int64) string {
	return string(appendInt64(nil, n))
}

func appendInt64(dst []byte, n int64) []byte {
	if n < 0 {
		dst = append(dst, '-')
		n = -n
	}
	if n < 10 {
		return append(dst, byte('0'+n))
	}
	dst = appendInt64(dst, n/10)
	return append(dst, byte('0'+n%10))
}

func TestValidBlockKey(t *testing.T) {
	valid := []string{
		"chunks/0/0/100_0_4194304",
		"chunks/FF/0/9999_3_2048",
		"chunks/0/1234567/9999999_0_8192",
	}
	invalid := []string{
		"",
		"/abs",
		"\\abs",
		"C:windows",
		"C:/x",
		"..",
		".",
		"/",
		"\\",
		"../escape",
		"chunks/../escape",
		"chunks/./hidden",
		"chunks/a//b",
		"chunks/a/",
		"chunks/a/..",
	}
	for _, k := range valid {
		if !validBlockKey(k) {
			t.Errorf("validBlockKey(%q) = false, want true", k)
		}
	}
	for _, k := range invalid {
		if validBlockKey(k) {
			t.Errorf("validBlockKey(%q) = true, want false", k)
		}
	}
}

func TestServer_HandleBlock_RejectsPathTraversal(t *testing.T) {
	// Place a sentinel file *outside* the cache dir; if the handler ever
	// leaks it, the assertion below fails.
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	if err := os.MkdirAll(filepath.Join(cacheDir, "raw"), 0700); err != nil {
		t.Fatal(err)
	}
	outsideContent := []byte("CONFIDENTIAL")
	outsidePath := filepath.Join(tmpDir, "secret.txt")
	if err := os.WriteFile(outsidePath, outsideContent, 0600); err != nil {
		t.Fatal(err)
	}

	tracker := NewAvailabilityTracker()
	srv := NewServer("uuid", tracker, cacheDir)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []string{
		"/block/../secret.txt",
		"/block/../../etc/passwd",
		"/block/./hidden",
		"/block/sub/../escape",
		"/block//abs",
		"/block/%2E%2E/secret.txt",   // URL-encoded ".."
		"/block/%2E%2E%2Fsecret.txt", // URL-encoded "../"
		"/block/\\windows\\path",
		"/block/C:/x",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			resp, err := http.Get(ts.URL + p)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for %q", resp.StatusCode, p)
			}
			body := make([]byte, len(outsideContent))
			n, _ := resp.Body.Read(body)
			if string(body[:n]) == string(outsideContent) {
				t.Fatalf("LEAK: handler returned outside-cache sentinel content for %q", p)
			}
		})
	}
}

func TestServer_HandleBlock_ServesValidKey(t *testing.T) {
	cacheDir := t.TempDir()
	rawDir := filepath.Join(cacheDir, "raw", "chunks", "0", "0")
	if err := os.MkdirAll(rawDir, 0700); err != nil {
		t.Fatal(err)
	}
	want := []byte("block-payload")
	if err := os.WriteFile(filepath.Join(rawDir, "100_0_13"), want, 0600); err != nil {
		t.Fatal(err)
	}

	tracker := NewAvailabilityTracker()
	srv := NewServer("uuid", tracker, cacheDir)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/block/chunks/0/0/100_0_13")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := make([]byte, 64)
	n, _ := resp.Body.Read(got)
	if string(got[:n]) != string(want) {
		t.Errorf("body = %q, want %q", got[:n], want)
	}
}
