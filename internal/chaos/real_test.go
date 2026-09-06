package chaos

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flashflow/internal/topology"
)

func TestToRealSchedule_RejectsUnknownTarget(t *testing.T) {
	s := Schedule{{At: time.Second, Target: "edge-nonexistent", Action: Crash}}
	if _, err := s.ToRealSchedule(map[string]*topology.EdgeServer{}); err == nil {
		t.Fatal("expected an error for an event targeting an EdgeServer not provided")
	}
}

func TestToRealSchedule_And_RunReal_EndToEnd(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-chaos"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{Instance: "edge-chaos", OriginURL: origin.URL()})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	s := Schedule{
		{At: 50 * time.Millisecond, Target: "edge-chaos", Action: Crash},
		{At: 150 * time.Millisecond, Target: "edge-chaos", Action: Recover},
	}
	actions, err := s.ToRealSchedule(map[string]*topology.EdgeServer{"edge-chaos": edge})
	if err != nil {
		t.Fatalf("ToRealSchedule failed: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	getStatus := func() int {
		resp, err := client.Get(edge.URL() + "/data/hot")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := getStatus(); got != http.StatusOK {
		t.Fatalf("expected 200 before the schedule starts, got %d", got)
	}

	start := time.Now()
	RunReal(actions, start)

	// Poll for the crash to actually take effect rather than a fixed
	// sleep landing exactly at 50ms (flaky under scheduler jitter).
	deadline := start.Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if getStatus() == http.StatusServiceUnavailable {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := getStatus(); got != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after the scheduled crash fired, got %d", got)
	}

	for time.Now().Before(deadline) {
		if getStatus() == http.StatusOK {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := getStatus(); got != http.StatusOK {
		t.Fatalf("expected 200 after the scheduled recover fired, got %d", got)
	}
}

func TestToRealSchedule_LatencyAction(t *testing.T) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-chaos-latency"})
	if err := origin.Start(); err != nil {
		t.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{Instance: "edge-chaos-latency", OriginURL: origin.URL()})
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		t.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	s := Schedule{{At: 0, Target: "edge-chaos-latency", Action: Latency, Delay: 100 * time.Millisecond}}
	actions, err := s.ToRealSchedule(map[string]*topology.EdgeServer{"edge-chaos-latency": edge})
	if err != nil {
		t.Fatalf("ToRealSchedule failed: %v", err)
	}
	RunReal(actions, time.Now())
	time.Sleep(50 * time.Millisecond) // give the At:0 action time to fire

	client := &http.Client{Timeout: 2 * time.Second}
	start := time.Now()
	resp, err := client.Get(edge.URL() + "/data/hot")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	elapsed := time.Since(start)
	if elapsed < 90*time.Millisecond {
		t.Fatalf("expected the request to take at least ~100ms after the latency action fired, took %v", elapsed)
	}
}
