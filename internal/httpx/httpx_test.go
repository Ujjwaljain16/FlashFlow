package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"flashflow/internal/clock"
)

func TestRequestID_GenerationAndExtraction(t *testing.T) {
	id1 := GenerateRequestID()
	id2 := GenerateRequestID()

	if len(id1) != 32 {
		t.Fatalf("expected 32 hex chars, got %d (%s)", len(id1), id1)
	}
	if id1 == id2 {
		t.Fatalf("expected unique IDs, got duplicate: %s", id1)
	}

	// Test extraction with empty header
	req := httptest.NewRequest("GET", "/test", nil)
	extracted := ExtractOrGenerateRequestID(req)
	if len(extracted) != 32 {
		t.Fatalf("expected 32 hex chars generated, got %s", extracted)
	}

	// Test extraction with existing header
	req.Header.Set(HeaderRequestID, "custom-test-id-12345")
	extractedCustom := ExtractOrGenerateRequestID(req)
	if extractedCustom != "custom-test-id-12345" {
		t.Fatalf("expected custom ID preserved, got %s", extractedCustom)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := OriginResponse{
		Service:   "origin",
		Instance:  "origin-1",
		RequestID: "req-123",
	}

	if err := WriteJSON(rec, http.StatusOK, payload); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON content-type, got %s", ct)
	}

	var resp OriginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if resp.Instance != "origin-1" || resp.RequestID != "req-123" {
		t.Fatalf("unexpected unmarshaled response: %+v", resp)
	}
}

func TestRequestTimings_Latencies(t *testing.T) {
	t0 := clock.VirtualTime(1000 * time.Millisecond.Nanoseconds())
	t1 := clock.VirtualTime(1005 * time.Millisecond.Nanoseconds())
	t2 := clock.VirtualTime(1008 * time.Millisecond.Nanoseconds())
	t3 := clock.VirtualTime(1028 * time.Millisecond.Nanoseconds()) // 20ms upstream
	t4 := clock.VirtualTime(1030 * time.Millisecond.Nanoseconds()) // 25ms total proxy, 5ms proxy proc
	t5 := clock.VirtualTime(1035 * time.Millisecond.Nanoseconds()) // 35ms end-to-end

	timings := RequestTimings{
		T0: t0, T1: t1, T2: t2, T3: t3, T4: t4, T5: t5,
	}

	if timings.UpstreamLatency() != 20*time.Millisecond {
		t.Fatalf("expected 20ms upstream latency, got %v", timings.UpstreamLatency())
	}
	if timings.ProxyProcessingLatency() != 5*time.Millisecond {
		t.Fatalf("expected 5ms proxy processing latency, got %v", timings.ProxyProcessingLatency())
	}
	if timings.EndToEndLatency() != 35*time.Millisecond {
		t.Fatalf("expected 35ms end-to-end latency, got %v", timings.EndToEndLatency())
	}
}

func TestCopyEndToEndHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("X-Custom-Header", "value123")
	src.Set("Connection", "close, X-Ephemeral-Header")
	src.Set("Keep-Alive", "timeout=5, max=1000")
	src.Set("Transfer-Encoding", "chunked")
	src.Set("Upgrade", "websocket")
	src.Set("X-Ephemeral-Header", "should-be-stripped")

	dst := http.Header{}
	CopyEndToEndHeaders(dst, src)

	// End-to-end headers must be copied
	if dst.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type preserved, got %q", dst.Get("Content-Type"))
	}
	if dst.Get("X-Custom-Header") != "value123" {
		t.Fatalf("expected X-Custom-Header preserved, got %q", dst.Get("X-Custom-Header"))
	}

	// Hop-by-hop headers must be stripped
	if dst.Get("Connection") != "" {
		t.Fatalf("expected Connection header stripped, got %q", dst.Get("Connection"))
	}
	if dst.Get("Keep-Alive") != "" {
		t.Fatalf("expected Keep-Alive stripped, got %q", dst.Get("Keep-Alive"))
	}
	if dst.Get("Transfer-Encoding") != "" {
		t.Fatalf("expected Transfer-Encoding stripped, got %q", dst.Get("Transfer-Encoding"))
	}
	if dst.Get("Upgrade") != "" {
		t.Fatalf("expected Upgrade stripped, got %q", dst.Get("Upgrade"))
	}
	if dst.Get("X-Ephemeral-Header") != "" {
		t.Fatalf("expected dynamic hop header declared in Connection stripped, got %q", dst.Get("X-Ephemeral-Header"))
	}
}

// TestRunHTTPBenchmark_PathFunc proves PathFunc actually varies the request
// path per call (not just accepted and ignored), and that it overrides the
// static Path field when both are set — the mechanism a hot/cold key
// workload for the Stage 4 cache experiments builds on.
func TestRunHTTPBenchmark_PathFunc(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	paths := []string{"/a", "/b", "/c"}
	var idx int
	var idxMu sync.Mutex
	pathFunc := func() string {
		idxMu.Lock()
		defer idxMu.Unlock()
		p := paths[idx%len(paths)]
		idx++
		return p
	}

	res, err := RunHTTPBenchmark(BenchmarkConfig{
		TargetURL:   ts.URL,
		Path:        "/should-be-overridden",
		Requests:    30,
		Concurrency: 3,
		PathFunc:    pathFunc,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SuccessfulRequests != 30 {
		t.Fatalf("expected 30 successful requests, got %d", res.SuccessfulRequests)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen["/should-be-overridden"] != 0 {
		t.Fatalf("expected PathFunc to override the static Path, but the server saw %d requests for it", seen["/should-be-overridden"])
	}
	if len(seen) != len(paths) {
		t.Fatalf("expected requests spread across all %d configured paths, server saw %v", len(paths), seen)
	}
	for _, p := range paths {
		if seen[p] != 10 {
			t.Fatalf("expected exactly 10 requests for %s (30 requests / 3 paths), got %d (seen=%v)", p, seen[p], seen)
		}
	}
}
