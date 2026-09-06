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
