package telemetry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

func TestSnapshotFromProxy_ReflectsRealTraffic(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-telemetry"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := proxy.Config{
		Targets:         []string{origin.URL()},
		TransportConfig: transport.DefaultTransportConfig("telemetry_test"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := proxy.NewReverseProxy(cfg, clk, proxy.NewStaticSelector(origin.URL()))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	hist := AttachHistogram(pxy)

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(pxy.URL() + "/data")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	m := SnapshotFromProxy(pxy)
	m.Histogram = hist

	total := m.RequestsTotal[origin.URL()]
	if total != 5 {
		t.Errorf("RequestsTotal[%s] = %d, want 5", origin.URL(), total)
	}
	if _, ok := m.LatencySeconds[origin.URL()]; !ok {
		t.Error("expected a latency estimate for the target that actually received traffic")
	}
	if state := m.HealthState[origin.URL()]; state == "" {
		t.Error("expected a non-empty health state for the target")
	}
	if m.Histogram == nil || m.Histogram.Count() != 5 {
		t.Fatalf("expected the attached histogram to have recorded 5 observations, got %v", m.Histogram)
	}
}

func TestSnapshotFromEdge_ReflectsRealTraffic(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-edge-telemetry"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-telemetry",
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

	client := &http.Client{Timeout: 5 * time.Second}
	get := func() {
		resp, err := client.Get(edge.URL() + "/data/hot")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}
	get() // miss
	get() // hit

	m := SnapshotFromEdge(edge)
	if got := m.RequestsTotal[edge.Instance()]; got != 1 {
		t.Errorf("RequestsTotal[%s] = %d, want 1 (one real origin fetch; the second request was a cache hit)", edge.Instance(), got)
	}
	if m.CacheHits != 1 {
		t.Errorf("CacheHits = %d, want 1", m.CacheHits)
	}
	if m.CacheMisses != 1 {
		t.Errorf("CacheMisses = %d, want 1", m.CacheMisses)
	}
	if m.CacheFills != 1 {
		t.Errorf("CacheFills = %d, want 1", m.CacheFills)
	}
}

func TestSnapshotFromProxy_EmptyBeforeAnyTraffic(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-empty"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	clk := clock.NewWallClock()
	cfg := proxy.Config{
		Targets:         []string{origin.URL()},
		TransportConfig: transport.DefaultTransportConfig("telemetry_empty_test"),
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}
	pxy := proxy.NewReverseProxy(cfg, clk, proxy.NewStaticSelector(origin.URL()))
	if err := pxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer pxy.Stop(context.Background())

	m := SnapshotFromProxy(pxy)
	if m.Histogram != nil {
		t.Error("expected a nil Histogram when AttachHistogram was never called")
	}
}
