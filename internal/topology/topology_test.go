package topology

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

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
