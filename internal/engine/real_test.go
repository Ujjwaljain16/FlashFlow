package engine

import (
	"testing"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

func realTestExperiment() Experiment {
	return Experiment{
		ID:   "real-test-exp",
		Name: "Real engine test",
		Scenario: replay.Scenario{
			// Targets deliberately named and timed to match Real.Edges
			// below -- ValidateConsistency (added after the Stage 10
			// demo-readiness audit found Scenario/Real could silently
			// diverge) requires this now.
			Targets: []replay.TargetProfile{
				{Name: "edge-a", ServiceTime: 5 * time.Millisecond},
				{Name: "edge-b", ServiceTime: 5 * time.Millisecond},
			},
			Seeds: replay.DeriveSeeds(1),
		},
		Policy: replay.RoundRobinPolicy(),
		Real: &RealExperimentConfig{
			OriginDelay: 5 * time.Millisecond,
			Edges: map[string]time.Duration{
				"edge-a": 5 * time.Millisecond,
				"edge-b": 5 * time.Millisecond,
			},
			TrafficPattern: traffic.Constant,
			TrafficParams:  traffic.Params{Requests: 20, Horizon: 500 * time.Millisecond, BaseRate: 40},
		},
	}
}

func TestRealEngine_Prepare_RejectsNilRealConfig(t *testing.T) {
	r := NewRealEngine()
	if err := r.Prepare(Experiment{ID: "no-real"}); err == nil {
		t.Fatal("expected an error for an Experiment with a nil Real config")
	}
}

func TestRealEngine_Prepare_RejectsNoEdges(t *testing.T) {
	r := NewRealEngine()
	exp := realTestExperiment()
	exp.Real.Edges = nil
	if err := r.Prepare(exp); err == nil {
		t.Fatal("expected an error for an Experiment with zero configured edges")
	}
}

func TestRealEngine_Prepare_RejectsZeroHorizon(t *testing.T) {
	r := NewRealEngine()
	exp := realTestExperiment()
	exp.Real.TrafficParams.Horizon = 0
	if err := r.Prepare(exp); err == nil {
		t.Fatal("expected an error for a zero traffic Horizon")
	}
}

func TestRealEngine_Run_EndToEnd(t *testing.T) {
	r := NewRealEngine()
	exp := realTestExperiment()

	result, err := r.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Engine != "real" {
		t.Errorf("Engine = %q, want \"real\"", result.Engine)
	}
	if result.WorldResult != nil {
		t.Error("expected a nil WorldResult for a real run")
	}
	if result.Real == nil {
		t.Fatal("expected a non-nil Real result")
	}
	if result.Real.Requests != 20 {
		t.Errorf("Requests = %d, want 20 (all should succeed against a healthy real backend)", result.Real.Requests)
	}
	total := 0
	for _, n := range result.Real.Metrics.RequestsTotal {
		total += int(n)
	}
	if total != 20 {
		t.Errorf("sum of RequestsTotal across targets = %d, want 20", total)
	}
}

func TestRealEngine_Replay_UsesAlternatePolicy(t *testing.T) {
	r := NewRealEngine()
	exp := realTestExperiment()

	result, err := r.Replay(exp, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if result.Real == nil || result.Real.Requests != 20 {
		t.Fatalf("expected 20 completed requests under the alternate policy, got %+v", result.Real)
	}
}

// TestRealEngine_Run_EWMAPrefersFastRealTarget is a regression test for a
// real, confirmed Stage 11 finding (docs/StageArtifacts/Stage11.md §10):
// before real.go bridged policy.New's Instrumentation to real per-request
// completions, EWMASelector's LatencyTracker never received a single real
// observation during a RealEngine run -- every decision after cold-start
// tied forever, so the deterministic tie-break locked 100% of traffic onto
// one target for the whole run, and WHICH target won was effectively
// arbitrary (dependent on Go's randomized map iteration order over
// RealExperimentConfig.Edges), not the genuinely faster one. With the
// bridge in place, a target with a real, large latency advantage should
// win a clear majority of real requests -- not by chance, but because EWMA
// actually observed it being faster.
func TestRealEngine_Run_EWMAPrefersFastRealTarget(t *testing.T) {
	r := NewRealEngine()
	exp := Experiment{
		ID: "ewma-real-signal-test",
		Scenario: replay.Scenario{
			Targets: []replay.TargetProfile{
				{Name: "edge-fast", ServiceTime: 2 * time.Millisecond},
				{Name: "edge-slow", ServiceTime: 100 * time.Millisecond},
			},
			Seeds: replay.DeriveSeeds(7),
		},
		Policy: replay.EWMAPolicy(),
		Real: &RealExperimentConfig{
			Edges: map[string]time.Duration{
				"edge-fast": 2 * time.Millisecond,
				"edge-slow": 100 * time.Millisecond,
			},
			TrafficPattern: traffic.Constant,
			TrafficParams:  traffic.Params{Requests: 100, Horizon: 2 * time.Second, BaseRate: 50},
		},
	}

	result, err := r.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	total := 0
	for _, n := range result.Real.Metrics.RequestsTotal {
		total += int(n)
	}
	if total == 0 {
		t.Fatal("expected at least some completed requests")
	}
	// RequestsTotal is keyed by the real edge's URL, not its Scenario name
	// ("edge-fast"/"edge-slow"), so which specific map key is which target
	// isn't directly recoverable here -- the meaningful assertions are the
	// aggregate p50 (which latency CLASS won) and the max single-target
	// share (whether one target dominates, and by how much), both below.
	if result.Real.Metrics.Histogram == nil {
		t.Fatal("expected a non-nil histogram")
	}
	p50 := result.Real.Metrics.Histogram.ValueAtPercentile(50)
	// A run still locked onto edge-slow (100ms service time) would show a
	// p50 near 100ms; a run correctly favoring edge-fast (2ms) shows a p50
	// far below the slow target's own fixed delay. 50ms is comfortably
	// between the two and catches the pre-fix failure mode (which locks
	// onto whichever target won cold-start, roughly 50/50 across repeated
	// test runs -- this assertion would fail about half the time before
	// the fix, and should pass consistently after it).
	if p50 > 50_000_000 { // 50ms in nanoseconds
		t.Errorf("p50 latency = %.2fms, want well under 50ms -- EWMA should have learned edge-fast (2ms) is faster than edge-slow (100ms) from real observations, not stayed locked onto whichever target cold-start happened to pick", float64(p50)/1e6)
	}
	fastShare := 0
	for _, n := range result.Real.Metrics.RequestsTotal {
		if int(n) > fastShare {
			fastShare = int(n)
		}
	}
	if float64(fastShare)/float64(total) < 0.6 {
		t.Errorf("max single-target share = %.2f, want >= 0.6 -- EWMA with a real 2ms-vs-100ms gap should concentrate clearly on the faster target once its latency signal is live", float64(fastShare)/float64(total))
	}
}

func TestRealEngine_Run_WithChaosSchedule(t *testing.T) {
	r := NewRealEngine()
	exp := realTestExperiment()
	exp.Real.TrafficParams.Horizon = 300 * time.Millisecond
	exp.Real.Chaos = chaos.Schedule{
		{At: 50 * time.Millisecond, Target: "edge-a", Action: chaos.Crash},
		{At: 150 * time.Millisecond, Target: "edge-a", Action: chaos.Recover},
	}

	result, err := r.Run(exp)
	if err != nil {
		t.Fatalf("Run with a chaos schedule failed: %v", err)
	}
	// Some requests may fail while edge-a is down and edge-b alone
	// can't absorb every arrival's worth of traffic instantly, but the
	// run itself must complete without error and report SOME
	// completions.
	if result.Real.Requests == 0 {
		t.Error("expected at least some requests to complete despite the chaos schedule")
	}
}
