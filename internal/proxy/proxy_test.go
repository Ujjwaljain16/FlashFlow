package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/httpx"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

func TestProxy_EndToEnd_SingleOrigin(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{
		Instance: "origin-test",
	})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:            []string{origin.URL()},
		TransportConfig:    transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:       health.DefaultConfig(),
		ProberConfig:       health.DefaultCheckerConfig(),
		ExposeDebugHeaders: true,
	}
	pxy := NewReverseProxy(cfg, clk, NewStaticSelector(origin.URL()))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}
	reqBody := []byte("hello flashflow")
	req, err := http.NewRequest(http.MethodPost, pxy.URL()+"/data?test=1", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Custom-Header", "proxy-test-val")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	reqID := resp.Header.Get(httpx.HeaderRequestID)
	if reqID == "" || len(reqID) != 32 {
		t.Fatalf("expected 32-char request ID, got %q", reqID)
	}

	var originResp httpx.OriginResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &originResp); err != nil {
		t.Fatalf("failed to parse origin response JSON: %v", err)
	}

	if originResp.Instance != "origin-test" {
		t.Fatalf("expected origin-test, got %s", originResp.Instance)
	}
	if originResp.PayloadSize != len(reqBody) {
		t.Fatalf("expected payload size %d, got %d", len(reqBody), originResp.PayloadSize)
	}

	// Verify transport connection stats
	stats := pxy.TransportStats()
	if stats.RequestsCompleted != 1 {
		t.Fatalf("expected 1 request completed in proxy transport, got %d", stats.RequestsCompleted)
	}
}

func TestProxy_EndToEnd_MultiEdge_HealthExclusion(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-main"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edgeA, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-a",
		OriginURL: origin.URL(),
	})
	if err != nil {
		t.Fatalf("failed to create edge-a: %v", err)
	}
	if err := edgeA.Start(); err != nil {
		t.Fatalf("failed to start edge-a: %v", err)
	}
	defer edgeA.Stop(context.Background())

	edgeB, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-b",
		OriginURL: origin.URL(),
	})
	if err != nil {
		t.Fatalf("failed to create edge-b: %v", err)
	}
	if err := edgeB.Start(); err != nil {
		t.Fatalf("failed to start edge-b: %v", err)
	}
	defer edgeB.Stop(context.Background())

	clk := clock.NewWallClock()
	hCfg := health.DefaultConfig()
	hCfg.UnhealthyFailThreshold = 1

	chkCfg := health.CheckerConfig{
		Interval: 20 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		Path:     "/health",
	}

	cfg := Config{
		Targets:            []string{edgeA.URL(), edgeB.URL()},
		TransportConfig:    transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:       hCfg,
		ProberConfig:       chkCfg,
		ExposeDebugHeaders: true,
	}

	pxy := NewReverseProxy(cfg, clk, NewRoundRobinSelector())
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Send requests and ensure both edges are reachable
	for i := 0; i < 4; i++ {
		resp, err := client.Get(pxy.URL() + "/data")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// 2. Stop Edge B
	edgeB.Stop(context.Background())

	// Wait for prober to detect edge-b down
	time.Sleep(100 * time.Millisecond)

	// 3. Send more requests; all must succeed by routing solely through Edge A
	for i := 0; i < 6; i++ {
		resp, err := client.Get(pxy.URL() + "/data")
		if err != nil {
			t.Fatalf("post-failure request %d failed: %v", i, err)
		}
		edgeID := resp.Header.Get(httpx.HeaderEdgeID)
		if edgeID != "edge-a" {
			t.Fatalf("expected all traffic routed to healthy edge-a, got edge %q", edgeID)
		}
		resp.Body.Close()
	}
}

func TestProxy_EndToEnd_RequestBodyForwarding(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-post"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-post",
		OriginURL: origin.URL(),
	})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:            []string{edge.URL()},
		TransportConfig:    transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:       health.DefaultConfig(),
		ProberConfig:       health.DefaultCheckerConfig(),
		ExposeDebugHeaders: true,
	}

	pxy := NewReverseProxy(cfg, clk, NewStaticSelector(edge.URL()))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	// Issue POST with a realistic payload across client -> proxy -> edge -> origin
	client := &http.Client{Timeout: 5 * time.Second}
	postPayload := bytes.Repeat([]byte("X"), 4096) // 4KB payload
	req, err := http.NewRequest(http.MethodPost, pxy.URL()+"/data", bytes.NewReader(postPayload))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Custom-App-Header", "post-test-payload")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get(httpx.HeaderEdgeID) != "edge-post" {
		t.Fatalf("expected edge-post header, got %q", resp.Header.Get(httpx.HeaderEdgeID))
	}

	var originResp httpx.OriginResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &originResp); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if originResp.Instance != "origin-post" {
		t.Fatalf("expected origin-post, got %s", originResp.Instance)
	}
	if originResp.PayloadSize != len(postPayload) {
		t.Fatalf("expected payload size %d received by origin, got %d", len(postPayload), originResp.PayloadSize)
	}
}

// TestProxy_ForwardsContentLength_NotAlwaysChunked regression-tests F-37:
// outReq.ContentLength was never copied from the inbound request, so
// http.NewRequestWithContext's ContentLength stayed 0 for any body that
// isn't a *bytes.Buffer/*bytes.Reader/*strings.Reader (an inbound
// *http.Request's Body never is), and net/http always sent the upstream
// request Transfer-Encoding: chunked regardless of a real, known length.
func TestProxy_ForwardsContentLength_NotAlwaysChunked(t *testing.T) {
	var gotContentLength int64
	var gotTransferEncoding []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentLength = r.ContentLength
		gotTransferEncoding = r.TransferEncoding
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{upstream.URL},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, NewStaticSelector(upstream.URL))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	payload := strings.Repeat("x", 4096)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(pxy.URL()+"/data", "text/plain", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if gotContentLength != int64(len(payload)) {
		t.Fatalf("expected upstream to see Content-Length %d, got %d (TransferEncoding=%v)", len(payload), gotContentLength, gotTransferEncoding)
	}
	if len(gotTransferEncoding) != 0 {
		t.Fatalf("expected no Transfer-Encoding (Content-Length was known), got %v", gotTransferEncoding)
	}
}

// TestProxy_ClientCancellation_DoesNotRecordAppError regression-tests F-16:
// a client disconnecting/timing out mid-flight made RoundTrip return an
// error wrapping context.Canceled, which was recorded as an app-level
// failure against the target indistinguishably from a genuine upstream
// failure -- corrupting health telemetry for a target that never actually
// failed.
func TestProxy_ClientCancellation_DoesNotRecordAppError(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until the test explicitly lets this request proceed
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	defer close(release)

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{upstream.URL},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, NewStaticSelector(upstream.URL))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	before, _ := pxy.Registry().GetHealth(upstream.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pxy.URL()+"/data", nil)
	//nolint:bodyclose // request is expected to fail via context deadline; no body to close
	if _, err := http.DefaultClient.Do(req); err == nil {
		t.Fatalf("expected request to fail via client-side context deadline")
	}

	// Give ServeHTTP's error branch time to run before checking the registry.
	time.Sleep(50 * time.Millisecond)
	after, _ := pxy.Registry().GetHealth(upstream.URL)
	if after.TotalAppErrors != before.TotalAppErrors {
		t.Fatalf("expected TotalAppErrors unchanged after client cancellation (before=%d after=%d)", before.TotalAppErrors, after.TotalAppErrors)
	}
}

func TestProxy_NoHealthyTargets_503(t *testing.T) {
	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{"http://127.0.0.1:59999"}, // unreachable
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, NewStaticSelector(""))
	// Explicitly mark all targets unhealthy
	pxy.Registry().RecordProbeResult("http://127.0.0.1:59999", false)
	pxy.Registry().RecordProbeResult("http://127.0.0.1:59999", false)

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(pxy.URL() + "/data")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}
}

// TestProxy_LeastConnections_DecrementsAfterSuccess proves the real
// ServeHTTP lifecycle (not just the isolated LoadTracker unit tests) pairs
// every Increment with exactly one Decrement on the ordinary success path.
func TestProxy_LeastConnections_DecrementsAfterSuccess(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-lc"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{origin.URL()},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, nil)
	sel := NewLeastConnectionsSelector(pxy.LoadTracker())
	pxy.SetSelector(sel)

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(pxy.URL() + "/data")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}

		if got := pxy.LoadTracker().Get(origin.URL()); got != 0 {
			t.Fatalf("request %d: expected in-flight count 0 after response fully read, got %d", i, got)
		}
	}
}

// TestProxy_LeastConnections_DecrementsAfterUpstreamError proves the load
// tracker does not leak when the upstream RoundTrip itself fails (target
// unreachable) — the exact "increment succeeds, request fails, decrement
// forgotten" leak scenario that would slowly poison Least Connections
// decisions if the defer in ServeHTTP did not cover this path.
func TestProxy_LeastConnections_DecrementsAfterUpstreamError(t *testing.T) {
	const unreachable = "http://127.0.0.1:59999" // same convention as TestProxy_NoHealthyTargets_503

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{unreachable},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, nil)
	sel := NewLeastConnectionsSelector(pxy.LoadTracker())
	pxy.SetSelector(sel)
	// Registry defaults new targets to HEALTHY, and the background prober
	// is never started in this test, so the target stays selectable —
	// selection must succeed and then the transport RoundTrip must fail.

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(pxy.URL() + "/data")
		if err != nil {
			t.Fatalf("request %d: client-level error (expected a 502 from the proxy, not a transport error): %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("request %d: expected 502 Bad Gateway from unreachable upstream, got %d", i, resp.StatusCode)
		}

		if got := pxy.LoadTracker().Get(unreachable); got != 0 {
			t.Fatalf("request %d: expected in-flight count 0 after upstream error, got %d — decrement leaked on the error path",
				i, got)
		}
	}
}

// TestProxy_EWMA_ObservesLatencyOnSuccess proves the real ServeHTTP
// lifecycle records a latency observation after an ordinary successful
// round trip, using the origin's artificial-delay hook to make the
// expected latency easy to bound.
func TestProxy_EWMA_ObservesLatencyOnSuccess(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-ewma", DefaultDelay: 20 * time.Millisecond})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{origin.URL()},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, nil)
	pxy.SetSelector(NewEWMASelector(pxy.LatencyTracker()))

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	if _, ok := pxy.LatencyTracker().Estimate(origin.URL()); ok {
		t.Fatalf("expected no latency estimate before any request")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(pxy.URL() + "/data")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	got, ok := pxy.LatencyTracker().Estimate(origin.URL())
	if !ok {
		t.Fatalf("expected a latency observation to have been recorded after a successful request")
	}
	if got < 20*time.Millisecond {
		t.Fatalf("expected observed latency to be at least the origin's 20ms artificial delay, got %v", got)
	}
}

// TestProxy_EWMA_NoObservationOnUpstreamError proves a RoundTrip failure
// does NOT produce a latency observation — the deliberate design decision
// documented on LatencyTracker (failures are a health concern, not a
// latency concern).
func TestProxy_EWMA_NoObservationOnUpstreamError(t *testing.T) {
	const unreachable = "http://127.0.0.1:59999"

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{unreachable},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, nil)
	pxy.SetSelector(NewEWMASelector(pxy.LatencyTracker()))

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(pxy.URL() + "/data")
	if err != nil {
		t.Fatalf("client-level error (expected a 502 from the proxy): %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway, got %d", resp.StatusCode)
	}

	if _, ok := pxy.LatencyTracker().Estimate(unreachable); ok {
		t.Fatalf("expected no latency observation to be recorded for a failed round trip")
	}
}

// TestProxy_P2C_EndToEnd_AvoidsBusyEdge proves P2CSelector wired through
// the real ServeHTTP lifecycle (using the proxy's own LoadTracker, the
// same instance ServeHTTP increments/decrements) correctly steers traffic
// away from an edge that is kept artificially busy by slow requests,
// across real concurrent HTTP traffic rather than a synthetic tracker.
func TestProxy_P2C_EndToEnd_AvoidsBusyEdge(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-p2c"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	slowEdge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance: "edge-slow", OriginURL: origin.URL(), DefaultDelay: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create slow edge: %v", err)
	}
	if err := slowEdge.Start(); err != nil {
		t.Fatalf("failed to start slow edge: %v", err)
	}
	defer slowEdge.Stop(context.Background())

	fastEdge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance: "edge-fast", OriginURL: origin.URL(), DefaultDelay: 0,
	})
	if err != nil {
		t.Fatalf("failed to create fast edge: %v", err)
	}
	if err := fastEdge.Start(); err != nil {
		t.Fatalf("failed to start fast edge: %v", err)
	}
	defer fastEdge.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := Config{
		Targets:         []string{slowEdge.URL(), fastEdge.URL()},
		TransportConfig: transport.DefaultTransportConfig("proxy_upstream"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := NewReverseProxy(cfg, clk, nil)
	pxy.SetSelector(NewP2CSelector(ScorerFromLoad(pxy.LoadTracker()), rand.New(rand.NewSource(1))))

	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	// Fire 40 concurrent requests. Whichever edge is currently busier
	// (the slow one, since its requests take much longer to complete)
	// should lose most P2C comparisons it's sampled into once real
	// concurrent load builds up on it.
	const totalRequests = 40
	results := make(chan string, totalRequests)
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < totalRequests; i++ {
		go func() {
			resp, err := client.Get(pxy.URL() + "/data")
			if err != nil {
				results <- "error"
				return
			}
			edgeID := resp.Header.Get(httpx.HeaderEdgeID)
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results <- edgeID
		}()
	}

	counts := map[string]int{}
	for i := 0; i < totalRequests; i++ {
		counts[<-results]++
	}

	if counts["error"] > 0 {
		t.Fatalf("expected no request errors, got %d (counts=%v)", counts["error"], counts)
	}
	if counts["edge-fast"] <= counts["edge-slow"] {
		t.Fatalf("expected edge-fast to receive more traffic than edge-slow under real concurrent load, got %v", counts)
	}

	// The deferred Decrement fires on the server's own goroutine stack
	// unwind, which can trail the client finishing its read by a hair;
	// give it a moment to settle before asserting the final count.
	time.Sleep(20 * time.Millisecond)
	if got := pxy.LoadTracker().Get(slowEdge.URL()); got != 0 {
		t.Fatalf("expected slow edge in-flight count to return to 0 after all requests complete, got %d", got)
	}
	if got := pxy.LoadTracker().Get(fastEdge.URL()); got != 0 {
		t.Fatalf("expected fast edge in-flight count to return to 0 after all requests complete, got %d", got)
	}
}
