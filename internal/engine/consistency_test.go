package engine

import (
	"testing"
	"time"

	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

// TestValidateConsistency_CatchesTheExactMismatchTheAuditFound is a
// direct regression test for a real gap the Stage 10 demo-readiness
// audit found: an Experiment could carry a Scenario describing one
// topology and a Real config describing a completely different one,
// and neither engine's Prepare noticed. This reproduces that exact
// probe (3 targets at 5/50/500ms vs. 1 edge at 1ms) and confirms
// RealEngine.Prepare now rejects it.
func TestValidateConsistency_CatchesTheExactMismatchTheAuditFound(t *testing.T) {
	exp := Experiment{
		ID: "consistency-regression",
		Scenario: replay.Scenario{
			Targets: []replay.TargetProfile{
				{Name: "fast", ServiceTime: 5 * time.Millisecond},
				{Name: "medium", ServiceTime: 50 * time.Millisecond},
				{Name: "slow", ServiceTime: 500 * time.Millisecond},
			},
			Seeds: replay.DeriveSeeds(1),
		},
		Policy: replay.RoundRobinPolicy(),
		Real: &RealExperimentConfig{
			OriginDelay:    1 * time.Millisecond,
			Edges:          map[string]time.Duration{"only-one-edge": 1 * time.Millisecond},
			TrafficPattern: traffic.Constant,
			TrafficParams:  traffic.Params{Requests: 10, Horizon: 200 * time.Millisecond, BaseRate: 50},
		},
	}

	if err := ValidateConsistency(exp); err == nil {
		t.Fatal("expected ValidateConsistency to reject a Scenario/Real mismatch (3 targets vs 1 edge, no matching names)")
	}

	r := NewRealEngine()
	if err := r.Prepare(exp); err == nil {
		t.Fatal("expected RealEngine.Prepare to reject the same mismatch")
	}
}

func TestValidateConsistency_AcceptsMatchingNames(t *testing.T) {
	exp := Experiment{
		ID: "consistency-match",
		Scenario: replay.Scenario{
			Targets: []replay.TargetProfile{
				{Name: "edge-a", ServiceTime: 5 * time.Millisecond},
				{Name: "edge-b", ServiceTime: 10 * time.Millisecond},
			},
			Seeds: replay.DeriveSeeds(1),
		},
		Real: &RealExperimentConfig{
			Edges: map[string]time.Duration{"edge-a": 5 * time.Millisecond, "edge-b": 10 * time.Millisecond},
		},
	}
	if err := ValidateConsistency(exp); err != nil {
		t.Fatalf("expected matching names to pass, got: %v", err)
	}
}

func TestValidateConsistency_DetectsCountMismatch(t *testing.T) {
	exp := Experiment{
		ID: "consistency-count",
		Scenario: replay.Scenario{
			Targets: []replay.TargetProfile{{Name: "edge-a", ServiceTime: 5 * time.Millisecond}},
		},
		Real: &RealExperimentConfig{
			Edges: map[string]time.Duration{"edge-a": 5 * time.Millisecond, "edge-b": 5 * time.Millisecond},
		},
	}
	if err := ValidateConsistency(exp); err == nil {
		t.Fatal("expected a count mismatch (1 target vs 2 edges) to be rejected")
	}
}

func TestValidateConsistency_DetectsNameMismatchSameCount(t *testing.T) {
	exp := Experiment{
		ID: "consistency-name",
		Scenario: replay.Scenario{
			Targets: []replay.TargetProfile{{Name: "fast", ServiceTime: 5 * time.Millisecond}},
		},
		Real: &RealExperimentConfig{
			Edges: map[string]time.Duration{"slow": 5 * time.Millisecond},
		},
	}
	if err := ValidateConsistency(exp); err == nil {
		t.Fatal("expected a same-count-but-different-name mismatch to be rejected")
	}
}

func TestValidateConsistency_NilRealIsAlwaysConsistent(t *testing.T) {
	exp := Experiment{ID: "virtual-only", Scenario: replay.Scenario{Targets: []replay.TargetProfile{{Name: "x"}}}}
	if err := ValidateConsistency(exp); err != nil {
		t.Fatalf("expected a nil Real config to be trivially consistent, got: %v", err)
	}
}
