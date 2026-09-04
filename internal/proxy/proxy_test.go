package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
