package engine

import (
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

func testExperiment() Experiment {
	const spacing = 5 * time.Millisecond
	const n = 50
	arrivals := make([]replay.Arrival, n)
	for i := 0; i < n; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: "/x"}
	}
	return Experiment{
		ID:   "test-exp",
		Name: "Virtual engine test",
		Scenario: replay.Scenario{
			Targets:  []replay.TargetProfile{{Name: "fast", ServiceTime: 10 * time.Millisecond}, {Name: "slow", ServiceTime: 50 * time.Millisecond}},
			Arrivals: arrivals,
			Horizon:  clock.VirtualTime((500 * time.Millisecond).Nanoseconds()),
			Seeds:    replay.DeriveSeeds(1),
		},
		Policy: replay.RoundRobinPolicy(),
	}
}

func TestVirtualEngine_Prepare_RejectsEmptyScenario(t *testing.T) {
	v := NewVirtualEngine()
	if err := v.Prepare(Experiment{ID: "empty"}); err == nil {
		t.Fatal("expected an error for an Experiment with no targets/arrivals")
	}
}

func TestVirtualEngine_Run_ProducesAWorldResult(t *testing.T) {
	v := NewVirtualEngine()
	exp := testExperiment()
	result, err := v.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Engine != "virtual" {
		t.Errorf("Engine = %q, want \"virtual\"", result.Engine)
	}
	if result.WorldResult == nil {
		t.Fatal("expected a non-nil WorldResult")
	}
	if result.Real != nil {
		t.Error("expected a nil Real field for a virtual run")
	}
	if len(result.WorldResult.Records) != len(exp.Scenario.Arrivals) {
		t.Errorf("got %d records, want %d", len(result.WorldResult.Records), len(exp.Scenario.Arrivals))
	}
}

func TestVirtualEngine_Replay_UsesAlternatePolicy(t *testing.T) {
	v := NewVirtualEngine()
	exp := testExperiment()

	baseline, err := v.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	counterfactual, err := v.Replay(exp, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}

	if idx, diverged := replay.FirstDivergence(baseline.WorldResult.Trace, counterfactual.WorldResult.Trace); !diverged {
		t.Fatalf("expected Round Robin and Adaptive to diverge on this heterogeneous scenario (idx=%d, diverged=%v)", idx, diverged)
	}
}

func TestVirtualEngine_Run_IsDeterministic(t *testing.T) {
	v := NewVirtualEngine()
	exp := testExperiment()
	a, err := v.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	b, err := v.Run(exp)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if idx, diverged := replay.FirstDivergence(a.WorldResult.Trace, b.WorldResult.Trace); diverged {
		t.Fatalf("expected two Run calls on the identical Experiment to be identical, diverged at %d", idx)
	}
}

func TestCompareProtocol_DetectsMismatch(t *testing.T) {
	a := testExperiment()
	b := testExperiment()
	b.Scenario.Horizon = a.Scenario.Horizon + clock.VirtualTime(time.Second.Nanoseconds())
	if err := CompareProtocol(a, b); err == nil {
		t.Fatal("expected an error for experiments differing only in Horizon")
	}
}

func TestCompareProtocol_AcceptsMatch(t *testing.T) {
	a := testExperiment()
	b := testExperiment()
	if err := CompareProtocol(a, b); err != nil {
		t.Fatalf("expected no error for experiments with identical protocol fields, got: %v", err)
	}
}
