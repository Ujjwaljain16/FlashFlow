package topology

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/httpx"
	"flashflow/internal/transport"
)

func TestOriginServer_HealthAndData(t *testing.T) {
	origin := NewOriginServer(OriginConfig{
		Instance: "origin-unit",
	})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	// 1. Check health
	resp, err := client.Get(origin.URL() + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// 2. Check data with artificial delay
	start := time.Now()
	respData, err := client.Get(origin.URL() + "/data?delay_ms=25")
	if err != nil {
		t.Fatalf("data request failed: %v", err)
	}
	defer respData.Body.Close()
	elapsed := time.Since(start)

	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected at least 20ms elapsed due to delay, got %v", elapsed)
	}

	var oResp httpx.OriginResponse
	body, _ := io.ReadAll(respData.Body)
	if err := json.Unmarshal(body, &oResp); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if oResp.Instance != "origin-unit" {
		t.Fatalf("expected origin-unit, got %s", oResp.Instance)
	}
}

// TestOriginServer_ConcurrencyStats_TracksPeak proves the peak tracker
// actually reflects real concurrent load rather than just the final
// snapshot's active count — the exact measurement a cache stampede
// experiment depends on to size "how big was the burst".
func TestOriginServer_ConcurrencyStats_TracksPeak(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-concurrency", DefaultDelay: 100 * time.Millisecond})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	if stats := origin.ConcurrencyStats(); stats.Active != 0 || stats.Peak != 0 {
		t.Fatalf("expected zero concurrency before any traffic, got %+v", stats)
	}

	const concurrent = 20
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get(origin.URL() + "/data")
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	stats := origin.ConcurrencyStats()
	if stats.Active != 0 {
		t.Fatalf("expected active=0 after all requests complete, got %d", stats.Active)
	}
	if stats.Peak != concurrent {
		t.Fatalf("expected peak concurrency to reach exactly %d (100ms delay held all %d requests in flight "+
			"simultaneously), got %d", concurrent, concurrent, stats.Peak)
	}
}

func TestEdgeServer_ForwardingAndDelay(t *testing.T) {
	origin := NewOriginServer(OriginConfig{
		Instance: "origin-unit-2",
	})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:        "edge-unit-1",
		OriginURL:       origin.URL(),
		TransportConfig: transport.DefaultTransportConfig("edge_origin_test"),
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	// Health check on edge
	hResp, err := client.Get(edge.URL() + "/health")
	if err != nil {
		t.Fatalf("edge health request failed: %v", err)
	}
	hResp.Body.Close()
	if hResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", hResp.StatusCode)
	}

	// Forwarded request through edge
	req, _ := http.NewRequest(http.MethodGet, edge.URL()+"/data", nil)
	req.Header.Set(httpx.HeaderRequestID, "test-req-edge-forward")

	dResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("edge forwarded request failed: %v", err)
	}
	defer dResp.Body.Close()

	if dResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", dResp.StatusCode)
	}
	if dResp.Header.Get(httpx.HeaderEdgeID) != "edge-unit-1" {
		t.Fatalf("expected edge header 'edge-unit-1', got %q", dResp.Header.Get(httpx.HeaderEdgeID))
	}
	if dResp.Header.Get(httpx.HeaderRequestID) != "test-req-edge-forward" {
		t.Fatalf("expected preserved request ID, got %q", dResp.Header.Get(httpx.HeaderRequestID))
	}

	// Verify edge-to-origin transport stats
	stats := edge.TransportStats()
	if stats.RequestsCompleted != 1 {
		t.Fatalf("expected 1 request completed between edge and origin, got %d", stats.RequestsCompleted)
	}
}

// TestEdgeServer_Cache_MissThenHit proves the full real-HTTP path: a first
// GET is a MISS that reaches Origin and gets cached; an identical second
// GET is a HIT served from the edge with no second Origin request, and the
// reconstructed response is a semantically valid, byte-identical copy of
// the original — not just "some response with the same status code".
func TestEdgeServer_Cache_MissThenHit(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-cache-hit"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-cache-hit",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	resp1, err := client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if got := resp1.Header.Get(httpx.HeaderCacheStatus); got != "MISS" {
		t.Fatalf("expected first request to be a MISS, got %q", got)
	}

	resp2, err := client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if got := resp2.Header.Get(httpx.HeaderCacheStatus); got != "HIT" {
		t.Fatalf("expected second request to be a HIT, got %q", got)
	}
	if resp2.StatusCode != resp1.StatusCode {
		t.Fatalf("expected matching status codes, got %d then %d", resp1.StatusCode, resp2.StatusCode)
	}
	if resp2.Header.Get("Content-Type") != resp1.Header.Get("Content-Type") {
		t.Fatalf("expected matching Content-Type, got %q then %q", resp1.Header.Get("Content-Type"), resp2.Header.Get("Content-Type"))
	}
	if string(body2) != string(body1) {
		t.Fatalf("expected byte-identical body on hit, got %q then %q", body1, body2)
	}

	if stats := edge.TransportStats(); stats.RequestsCompleted != 1 {
		t.Fatalf("expected exactly 1 edge->origin request (the MISS only), got %d", stats.RequestsCompleted)
	}
	cs := edge.CacheStats()
	if cs.Fills != 1 || cs.Hits != 1 || cs.Misses != 1 {
		t.Fatalf("unexpected cache stats: %+v", cs)
	}
}

// TestEdgeServer_Cache_ExpiredEntryRefetches proves TTL expiration actually
// triggers a fresh Origin fetch rather than silently continuing to serve
// (or silently failing on) the stale entry.
func TestEdgeServer_Cache_ExpiredEntryRefetches(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-cache-ttl"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	mc := clock.NewMockClock(0)
	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-cache-ttl",
		OriginURL: origin.URL(),
		CacheTTL:  10 * time.Millisecond,
		Clock:     mc,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	get := func() string {
		resp, err := client.Get(edge.URL() + "/data/hot")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		return resp.Header.Get(httpx.HeaderCacheStatus)
	}

	if got := get(); got != "MISS" {
		t.Fatalf("expected first request MISS, got %q", got)
	}
	if got := get(); got != "HIT" {
		t.Fatalf("expected second request HIT (still within TTL), got %q", got)
	}

	mc.Advance(20 * time.Millisecond) // past the 10ms TTL

	if got := get(); got != "MISS" {
		t.Fatalf("expected third request MISS after TTL expired, got %q", got)
	}

	if stats := edge.TransportStats(); stats.RequestsCompleted != 2 {
		t.Fatalf("expected exactly 2 edge->origin requests (2 misses), got %d", stats.RequestsCompleted)
	}
}

// TestEdgeServer_Cache_OnlyCachesGET proves POST requests bypass the cache
// entirely — no X-Cache-Status header, and every POST reaches Origin.
func TestEdgeServer_Cache_OnlyCachesGET(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-cache-post"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-cache-post",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 3; i++ {
		resp, err := client.Post(edge.URL()+"/data/hot", "application/octet-stream", nil)
		if err != nil {
			t.Fatalf("POST %d failed: %v", i, err)
		}
		if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "" {
			t.Fatalf("POST %d: expected no cache status header, got %q", i, got)
		}
		resp.Body.Close()
	}

	if stats := edge.TransportStats(); stats.RequestsCompleted != 3 {
		t.Fatalf("expected all 3 POSTs to reach origin (never cached), got %d", stats.RequestsCompleted)
	}
	cs := edge.CacheStats()
	if cs.Fills != 0 {
		t.Fatalf("expected 0 cache fills for POST traffic, got %d", cs.Fills)
	}
}

// TestEdgeServer_Cache_DoesNotCacheErrorStatus proves a 5xx response from
// Origin is never stored — every request to an always-failing endpoint
// must keep reaching Origin, not get "stuck" serving a cached failure.
func TestEdgeServer_Cache_DoesNotCacheErrorStatus(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-cache-error"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-cache-error",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 3; i++ {
		resp, err := client.Get(edge.URL() + "/data/broken?status_code=500")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("request %d: expected 500 from origin, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	if stats := edge.TransportStats(); stats.RequestsCompleted != 3 {
		t.Fatalf("expected all 3 requests to reach origin (5xx never cached), got %d", stats.RequestsCompleted)
	}
	cs := edge.CacheStats()
	if cs.Fills != 0 {
		t.Fatalf("expected 0 cache fills for 5xx responses, got %d", cs.Fills)
	}
}

// TestEdgeServer_Cache_DisabledByDefault proves an edge with no CacheTTL
// configured behaves exactly as it did before caching existed: no
// X-Cache-Status header ever appears, every request reaches Origin.
func TestEdgeServer_Cache_DisabledByDefault(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-no-cache"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{Instance: "edge-no-cache", OriginURL: origin.URL()})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 2; i++ {
		resp, err := client.Get(edge.URL() + "/data/hot")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "" {
			t.Fatalf("request %d: expected no cache status header when CacheTTL is unset, got %q", i, got)
		}
		resp.Body.Close()
	}

	if stats := edge.TransportStats(); stats.RequestsCompleted != 2 {
		t.Fatalf("expected both requests to reach origin, got %d", stats.RequestsCompleted)
	}
}
