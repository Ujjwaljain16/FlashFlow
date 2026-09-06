package topology

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/clock"
	"flashflow/internal/httpx"
	"flashflow/internal/netsim"
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

// TestEdgeServer_SetDown_CrashesAndRecovers is Stage 10's (§10.7) real-
// engine proof that SetDown makes an edge look genuinely crashed to any
// caller, including its own /health endpoint, and that clearing it
// restores normal forwarding.
func TestEdgeServer_SetDown_CrashesAndRecovers(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-setdown"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{Instance: "edge-setdown", OriginURL: origin.URL()})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 before SetDown, got %d", resp.StatusCode)
	}

	edge.SetDown(true)

	resp, err = client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while down, got %d", resp.StatusCode)
	}

	healthResp, err := client.Get(edge.URL() + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected /health to also report 503 while down, got %d", healthResp.StatusCode)
	}

	edge.SetDown(false)

	resp, err = client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after recovery, got %d", resp.StatusCode)
	}
}

// TestEdgeServer_SWR_StaleHitServesImmediatelyThenRevalidates is
// Stage 10's (§10.5) real-engine end-to-end proof that StaleWindow
// actually activates GetSWR's behavior through the live HTTP path, not
// just at the internal/cache package level: a request landing after
// CacheTTL but within CacheTTL+StaleWindow gets HIT-STALE immediately,
// and a subsequent request (after giving the background revalidation
// time to complete) gets a fresh HIT again rather than a MISS.
func TestEdgeServer_SWR_StaleHitServesImmediatelyThenRevalidates(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-swr"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	mc := clock.NewMockClock(0)
	edge, err := NewEdgeServer(EdgeConfig{
		Instance:    "edge-swr",
		OriginURL:   origin.URL(),
		CacheTTL:    10 * time.Millisecond,
		StaleWindow: 100 * time.Millisecond,
		Coalesce:    true, // required for SWR's background revalidation to activate -- see EdgeConfig.StaleWindow's own doc comment
		Clock:       mc,
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

	mc.Advance(20 * time.Millisecond) // past the 10ms TTL, within the 100ms StaleWindow
	if got := get(); got != "HIT-STALE" {
		t.Fatalf("expected a stale hit within the StaleWindow, got %q", got)
	}

	// The background revalidation fired by that stale hit runs
	// concurrently with this test goroutine; give it a moment to
	// complete and store the fresh entry before checking the next
	// request's status.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if edge.CacheStats().StaleHits >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// Poll for the revalidation's completion via TransportStats rather
	// than a fixed sleep -- it should reach exactly 2 edge->origin
	// requests (the initial miss, plus the one background revalidation)
	// well before the deadline.
	for time.Now().Before(deadline) {
		if edge.TransportStats().RequestsCompleted >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if got := get(); got != "HIT" {
		t.Fatalf("expected a fresh HIT after the background revalidation completed (still within a fresh TTL window from the mock clock's perspective), got %q", got)
	}
	if stats := edge.TransportStats(); stats.RequestsCompleted != 2 {
		t.Fatalf("expected exactly 2 edge->origin requests (1 initial miss + 1 background revalidation, no extra synchronous fetch), got %d", stats.RequestsCompleted)
	}
	if stats := edge.CacheStats(); stats.StaleHits != 1 {
		t.Fatalf("expected exactly 1 recorded stale hit, got %d", stats.StaleHits)
	}
}

// TestEdgeServer_SWR_PastStaleWindowIsPlainMiss confirms a request
// arriving after CacheTTL+StaleWindow gets a normal synchronous MISS,
// not a stale hit -- SWR extends the cache's useful window, it doesn't
// remove the outer expiry.
func TestEdgeServer_SWR_PastStaleWindowIsPlainMiss(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-swr-expired"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	mc := clock.NewMockClock(0)
	edge, err := NewEdgeServer(EdgeConfig{
		Instance:    "edge-swr-expired",
		OriginURL:   origin.URL(),
		CacheTTL:    10 * time.Millisecond,
		StaleWindow: 20 * time.Millisecond,
		Coalesce:    true,
		Clock:       mc,
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
	mc.Advance(100 * time.Millisecond) // past CacheTTL+StaleWindow (30ms) entirely
	if got := get(); got != "MISS" {
		t.Fatalf("expected a plain MISS past CacheTTL+StaleWindow, got %q", got)
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

// TestEdgeServer_Coalesce_ConcurrentMissesShareOneUpstreamFetch is the
// direct EdgeServer-level version of Experiment 004-C's stampede: a burst
// of concurrent requests for the same never-cached key must collapse into
// exactly one origin fetch when coalescing is enabled.
func TestEdgeServer_Coalesce_ConcurrentMissesShareOneUpstreamFetch(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-coalesce-hit", DefaultDelay: 50 * time.Millisecond})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-coalesce-hit",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
		Coalesce:  true,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}
	const n = 20
	var wg sync.WaitGroup
	cacheStatuses := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(edge.URL() + "/data/hot")
			if err != nil {
				t.Errorf("request %d failed: %v", i, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: expected 200, got %d", i, resp.StatusCode)
			}
			cacheStatuses[i] = resp.Header.Get(httpx.HeaderCacheStatus)
		}(i)
	}
	wg.Wait()

	if stats := edge.TransportStats(); stats.RequestsCompleted != 1 {
		t.Fatalf("expected exactly one upstream fetch for %d coalesced misses, got %d", n, stats.RequestsCompleted)
	}

	leaders, shared := 0, 0
	for i, s := range cacheStatuses {
		switch s {
		case "MISS":
			leaders++
		case "MISS-COALESCED":
			shared++
		default:
			t.Fatalf("request %d: unexpected cache status %q", i, s)
		}
	}
	if leaders != 1 || shared != n-1 {
		t.Fatalf("expected 1 leader (MISS) and %d waiters (MISS-COALESCED), got %d and %d", n-1, leaders, shared)
	}

	coalesceStats := edge.CoalesceStats()
	if coalesceStats.Leads != 1 || coalesceStats.Shared != n-1 {
		t.Fatalf("expected CoalesceStats{Leads:1, Shared:%d}, got %+v", n-1, coalesceStats)
	}
}

// TestEdgeServer_Coalesce_DisabledByDefault proves Coalesce is opt-in:
// with CacheTTL set but Coalesce left false, a concurrent burst against a
// cold key produces duplicate upstream fetches, same as Stage 4 step 3's
// stampede finding before coalescing existed at all.
func TestEdgeServer_Coalesce_DisabledByDefault(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-no-coalesce", DefaultDelay: 50 * time.Millisecond})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-no-coalesce",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
		// Coalesce intentionally left at its zero value (false).
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(edge.URL() + "/data/hot")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	if stats := edge.TransportStats(); stats.RequestsCompleted <= 1 {
		t.Fatalf("expected multiple duplicate upstream fetches with coalescing disabled, got %d", stats.RequestsCompleted)
	}
	if coalesceStats := edge.CoalesceStats(); coalesceStats != (cache.CoalesceStats{}) {
		t.Fatalf("expected zero CoalesceStats when coalescing is disabled, got %+v", coalesceStats)
	}
}

// TestEdgeServer_Coalesce_LeaderCancellationDoesNotAbortWaiters is the
// master-context invariant that motivates using context.Background() for
// the shared fetch instead of the leader's own r.Context(): the leader's
// downstream client disconnecting must not cancel the fetch for waiters
// still behind it.
func TestEdgeServer_Coalesce_LeaderCancellationDoesNotAbortWaiters(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-coalesce-cancel", DefaultDelay: 150 * time.Millisecond})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-coalesce-cancel",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
		Coalesce:  true,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		req, _ := http.NewRequestWithContext(leaderCtx, http.MethodGet, edge.URL()+"/data/hot", nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		// The leader's own request is expected to fail client-side once
		// cancelled below; that failure is not what this test checks.
	}()

	// Give the leader time to register itself as in-flight and start the
	// (150ms) origin fetch before cutting it off.
	time.Sleep(30 * time.Millisecond)
	cancelLeader()

	const waiters = 5
	var wg sync.WaitGroup
	statuses := make([]int, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(edge.URL() + "/data/hot")
			if err != nil {
				t.Errorf("waiter %d failed: %v", i, err)
				return
			}
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	<-leaderDone

	for i, code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("waiter %d: expected 200 despite the leader's cancellation, got %d", i, code)
		}
	}
	if stats := edge.TransportStats(); stats.RequestsCompleted != 1 {
		t.Fatalf("expected the leader's cancellation to still leave exactly one completed upstream fetch, got %d", stats.RequestsCompleted)
	}
}

// TestEdgeServer_Coalesce_FailureCleansUpAndRecovers is the real-failure
// counterpart to coalesce_test.go's synthetic error/panic cases: origin is
// actually stopped (a genuine connection-refused failure, not a mocked
// error), so a coalesced burst for a never-cached key must fail cleanly
// with the same failure shared by every waiter, the key must not be left
// stuck once the burst is over, and the same key must fetch successfully
// again once origin comes back.
func TestEdgeServer_Coalesce_FailureCleansUpAndRecovers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a free port: %v", err)
	}
	fixedAddr := ln.Addr().String()
	ln.Close()

	origin := NewOriginServer(OriginConfig{Addr: fixedAddr, Instance: "origin-coalesce-fail"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-coalesce-fail",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute,
		Coalesce:  true,
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	// Sanity-check origin is actually healthy before injecting any failure,
	// so a later failure can't be mistaken for a setup bug.
	sanity, err := client.Get(edge.URL() + "/data/outage-key")
	if err != nil || sanity.StatusCode != http.StatusOK {
		t.Fatalf("expected the pre-outage sanity request to succeed, err=%v resp=%v", err, sanity)
	}
	sanity.Body.Close()

	if err := origin.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop origin: %v", err)
	}

	// A connection-refused failure returns almost instantly, so without
	// some delay the leader's fetch could fail and clear the in-flight
	// entry before the rest of the burst even reaches Do — some callers
	// would start their own leader attempt instead of coalescing. An
	// artificial edge-side delay (origin's own delay is moot while it's
	// down) widens that window, the same trick 004-C used via Origin's
	// delay to make sure a burst actually races concurrently.
	edge.SetArtificialDelay(50 * time.Millisecond)

	// A concurrent burst for a different, never-cached key while origin is
	// down: coalescing should still collapse this into one dial attempt,
	// and the resulting failure should be shared by every caller.
	const n = 10
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(edge.URL() + "/data/outage-burst")
			if err != nil {
				statuses[i] = -1
				return
			}
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	for i, code := range statuses {
		if code != http.StatusBadGateway {
			t.Fatalf("request %d during outage: expected 502, got %d", i, code)
		}
	}
	// One lead from the pre-outage sanity request (a different key, so it
	// coalesced alone) plus one lead from the burst; every burst caller,
	// leader included, counted as a failure.
	if stats := edge.CoalesceStats(); stats.Leads != 2 || stats.Shared != n-1 || stats.Failures != n {
		t.Fatalf("expected 2 leads, %d shared, %d failures, got %+v", n-1, n, stats)
	}
	if stats := edge.TransportStats(); stats.FailedDials < 1 {
		t.Fatalf("expected at least one failed dial while origin was down, got %+v", stats)
	}

	// The in-flight entry for outage-burst must not be left stuck: a fresh
	// request for the same key, still during the outage, must reach a
	// fresh attempt (fail again cleanly) rather than hang forever.
	resp2, err := client.Get(edge.URL() + "/data/outage-burst")
	if err != nil {
		t.Fatalf("post-burst request during outage failed unexpectedly: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected the post-burst request to also fail with 502, got %d", resp2.StatusCode)
	}

	if err := origin.Start(); err != nil {
		t.Fatalf("failed to restart origin: %v", err)
	}
	defer origin.Stop(context.Background())

	resp3, err := client.Get(edge.URL() + "/data/outage-burst")
	if err != nil {
		t.Fatalf("post-recovery request failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected the same key to succeed once origin recovered, got %d", resp3.StatusCode)
	}
	if got := resp3.Header.Get(httpx.HeaderCacheStatus); got != "MISS" {
		t.Fatalf("expected a fresh MISS on recovery (nothing was ever successfully cached for this key), got %q", got)
	}
}

// TestEdgeServer_NetworkConditions_LatencyOnlyAffectsMisses proves the
// simulated edge-origin latency shows up on a fetch but is completely
// invisible on a cache hit, the same insulation property 004-E already
// demonstrated for a total outage, now shown for partial degradation.
// TestEdgeServer_Cache_OverrideStatusHeaderDoesNotCollide regression-tests
// F-15: the cache key was built purely from method+path+query, while
// X-Override-Status (and X-Artificial-Delay-Ms) are debug headers Origin
// itself treats as response-determining and this edge forwards unmodified
// -- so two GETs to the identical path differing only in that header used
// to collide on one cache entry, serving whichever response was cached
// first regardless of the second request's own override.
func TestEdgeServer_Cache_OverrideStatusHeaderDoesNotCollide(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-override-key"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:  "edge-override-key",
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

	// First request: no override, gets cached as a 200 MISS.
	resp, err := client.Get(edge.URL() + "/data/override-key-test")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected first response 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "MISS" {
		t.Fatalf("expected first response MISS, got %q", got)
	}
	resp.Body.Close()

	// Second request: identical path/query but a 204 override -- must NOT
	// hit the first entry (that would silently return 200), and must
	// itself be served as its own, separately-cached MISS.
	req, err := http.NewRequest(http.MethodGet, edge.URL()+"/data/override-key-test", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("X-Override-Status", "204")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected overridden response 204 (not the colliding cached 200), got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "MISS" {
		t.Fatalf("expected the overridden request to be its own MISS (distinct cache key), got %q", got)
	}
}

// TestEdgeServer_NetworkConditions_SeededIsReproducible regression-tests
// F-13: netsim.NewTransport used to be called with a nil *rand.Rand from
// EdgeServer, which falls back to a wall-clock-seeded source -- meaning
// every run's loss/jitter sequence was different and non-reproducible.
// Two EdgeServers built with the identical (non-zero) Conditions.Seed must
// now produce the identical pass/fail sequence for the same sequential
// request pattern.
func TestEdgeServer_NetworkConditions_SeededIsReproducible(t *testing.T) {
	runSequence := func(seed int64) []bool {
		origin := NewOriginServer(OriginConfig{Instance: "origin-netsim-seed"})
		if err := origin.Start(); err != nil {
			t.Fatalf("failed to start origin: %v", err)
		}
		defer origin.Stop(context.Background())

		edge, err := NewEdgeServer(EdgeConfig{
			Instance:          "edge-netsim-seed",
			OriginURL:         origin.URL(),
			NetworkConditions: netsim.Conditions{LossRate: 0.5, Seed: seed},
		})
		if err != nil {
			t.Fatalf("failed to create edge: %v", err)
		}
		if err := edge.Start(); err != nil {
			t.Fatalf("failed to start edge: %v", err)
		}
		defer edge.Stop(context.Background())

		client := &http.Client{Timeout: 2 * time.Second}
		results := make([]bool, 20)
		for i := range results {
			// Sequential, not concurrent -- each RoundTrip's RNG draw must
			// complete before the next request's, or ordering (not the
			// seed) would determine the observed sequence.
			resp, err := client.Get(edge.URL() + "/data/seeded")
			if err != nil {
				results[i] = false
				continue
			}
			results[i] = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
		return results
	}

	first := runSequence(7)
	second := runSequence(7)

	if len(first) != len(second) {
		t.Fatalf("sequence length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("request %d: seeded runs diverged (first=%v second=%v) -- loss sequence is not reproducible", i, first[i], second[i])
		}
	}
}

func TestEdgeServer_NetworkConditions_LatencyOnlyAffectsMisses(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-netsim-latency"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:          "edge-netsim-latency",
		OriginURL:         origin.URL(),
		CacheTTL:          time.Minute,
		NetworkConditions: netsim.Conditions{Latency: 60 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	start := time.Now()
	resp, err := client.Get(edge.URL() + "/data/hot")
	missElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("miss request failed: %v", err)
	}
	if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "MISS" {
		t.Fatalf("expected MISS, got %q", got)
	}
	resp.Body.Close()
	if missElapsed < 60*time.Millisecond {
		t.Fatalf("expected the miss to pay the simulated 60ms latency, took %v", missElapsed)
	}

	start = time.Now()
	resp, err = client.Get(edge.URL() + "/data/hot")
	hitElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("hit request failed: %v", err)
	}
	if got := resp.Header.Get(httpx.HeaderCacheStatus); got != "HIT" {
		t.Fatalf("expected HIT, got %q", got)
	}
	resp.Body.Close()
	if hitElapsed >= 60*time.Millisecond {
		t.Fatalf("expected a cache hit to be unaffected by the simulated link latency, took %v", hitElapsed)
	}

	if stats := edge.NetworkStats(); stats.Requests != 1 {
		t.Fatalf("expected exactly 1 request to reach the simulated network (the hit never should), got %+v", stats)
	}
}

// TestEdgeServer_NetworkConditions_LossFailsMissesButNotHits proves
// simulated packet loss on the edge-origin link fails a fetch but never
// touches an already-cached key, the same insulation shown for a total
// outage in 004-E, now for probabilistic loss instead of a hard failure.
func TestEdgeServer_NetworkConditions_LossFailsMissesButNotHits(t *testing.T) {
	origin := NewOriginServer(OriginConfig{Instance: "origin-netsim-loss"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := NewEdgeServer(EdgeConfig{
		Instance:          "edge-netsim-loss",
		OriginURL:         origin.URL(),
		CacheTTL:          time.Minute,
		NetworkConditions: netsim.Conditions{LossRate: 1.0}, // always drop, for a deterministic test
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(edge.URL() + "/data/lossy-key")
	if err != nil {
		t.Fatalf("request failed at the transport level: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected a 100%% loss rate to fail the miss with 502, got %d", resp.StatusCode)
	}

	if stats := edge.NetworkStats(); stats.Requests != 1 || stats.Dropped != 1 {
		t.Fatalf("expected NetworkStats{Requests:1, Dropped:1}, got %+v", stats)
	}
	if stats := edge.CacheStats(); stats.Fills != 0 {
		t.Fatalf("expected a dropped fetch to never be cached, got %+v", stats)
	}
}
