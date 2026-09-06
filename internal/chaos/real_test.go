package chaos

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"flashflow/internal/topology"
)

// TestRunReal_WaitGroupBlocksUntilEveryActionFires is a regression test
// for a real bug an independent audit found: internal/engine's real
// engine used to call RunReal and immediately move on to wait only for
// TRAFFIC to finish, snapshot metrics, and tear down the edges -- with
// no way to know whether a chaos action scheduled near or after the
// traffic horizon had actually fired yet. A late action would then
// silently run against an already-stopped edge, never affecting the
// recorded run at all. The fix is this function's returned WaitGroup;
// this test proves it genuinely blocks until every action's own Run has
// been called, not just until they've been scheduled.
func TestRunReal_WaitGroupBlocksUntilEveryActionFires(t *testing.T) {
	var fired int32
	actions := []ScheduledAction{
		{At: 40 * time.Millisecond, Run: func() { atomic.AddInt32(&fired, 1) }},
		{At: 10 * time.Millisecond, Run: func() { atomic.AddInt32(&fired, 1) }},
		{At: 0, Run: func() { atomic.AddInt32(&fired, 1) }},
	}
	wg := RunReal(actions, time.Now())
	wg.Wait()
	if got := atomic.LoadInt32(&fired); got != int32(len(actions)) {
		t.Fatalf("after wg.Wait(), fired=%d, want %d -- every scheduled action's Run must have been called before Wait returns", got, len(actions))
	}
}

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
