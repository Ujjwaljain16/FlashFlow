package challenge

import (
	"math"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
	"flashflow/internal/tuning"
)

// singleTargetScenarios builds a scenario set with exactly ONE target
// per scenario -- the cleanest way to construct a genuinely FLAT
// objective: with only one candidate ever available, every weight
// combination in AdaptiveConfig trivially "selects" it, so Utility is
// identical for every sampled config regardless of its weights. This
// is a real flat region, not an approximation of one.
func singleTargetScenarios(n int) []replay.Scenario {
	const requests = 100
	const spacing = 10 * time.Millisecond
	scenarios := make([]replay.Scenario, n)
	for s := 0; s < n; s++ {
		arrivals := make([]replay.Arrival, requests)
		for i := 0; i < requests; i++ {
			arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: "/x"}
		}
		scenarios[s] = replay.Scenario{
			Targets:  []replay.TargetProfile{{Name: "only", ServiceTime: 20 * time.Millisecond}},
			Arrivals: arrivals,
			Seed:     int64(s),
		}
	}
	return scenarios
}

// TestTuningChallenge_FlatObjectiveDoesNotBreakSearch confirms Random
// Search behaves sanely when the objective genuinely cannot
// discriminate between candidates: no panic, a coherent convergence
// summary (the very first evaluation is as good as any later one, so
// the search should report having "last improved" at evaluation 0 and
// plateaued immediately), and a valid Best() result.
func TestTuningChallenge_FlatObjectiveDoesNotBreakSearch(t *testing.T) {
	scenarios := singleTargetScenarios(5)
	rsc := tuning.RandomSearchConfig{
		Evaluations: 30, OptimizerSeed: 1,
		ConfigSpace: tuning.DefaultConfigSpace(), ObjectiveWeights: tuning.DefaultObjectiveWeights(),
	}

	result := tuning.RunRandomSearch(rsc, scenarios)

	best, ok := result.Best()
	if !ok {
		t.Fatal("expected a valid Best() result even under a flat objective")
	}
	for i, e := range result.Evaluations {
		if !e.Valid {
			t.Fatalf("evaluation %d: expected every evaluation to succeed under a flat (not erroring) objective, got invalid: %s", i, e.InvalidReason)
		}
		if math.Abs(e.Utility-best.Utility) > 1e-9 {
			t.Fatalf("evaluation %d: expected every evaluation to score identically under a genuinely flat objective (best=%v), got %v", i, best.Utility, e.Utility)
		}
	}
	if result.Convergence.LastImprovedAtIndex != 0 {
		t.Fatalf("expected a flat objective to 'last improve' at evaluation 0 (nothing after it can improve on it), got %d", result.Convergence.LastImprovedAtIndex)
	}
	if !result.Convergence.Plateaued {
		t.Fatal("expected a flat objective to be reported as plateaued")
	}
}

// TestTuningChallenge_BoundaryOptimum confirms the search correctly
// explores and evaluates a ConfigSpace collapsed to a single point at
// its own boundary (Min==Max for both durations) -- the case master
// context rule 16 names directly: an optimizer must not implicitly
// assume the true optimum sits safely in the interior of the allowed
// range.
func TestTuningChallenge_BoundaryOptimum(t *testing.T) {
	const boundaryRef = 1 * time.Millisecond // the space's own stated minimum
	const boundaryStale = 5 * time.Second    // the space's own stated maximum
	cs := tuning.ConfigSpace{
		ReferenceLatencyMin: boundaryRef, ReferenceLatencyMax: boundaryRef,
		StaleAfterMin: boundaryStale, StaleAfterMax: boundaryStale,
	}
	scenarios := tuning.DefaultScenarioSpace().GenerateSet(1, 5)
	rsc := tuning.RandomSearchConfig{
		Evaluations: 20, OptimizerSeed: 2, ConfigSpace: cs, ObjectiveWeights: tuning.DefaultObjectiveWeights(),
	}

	result := tuning.RunRandomSearch(rsc, scenarios)
	for i, e := range result.Evaluations {
		if !e.Valid {
			t.Fatalf("evaluation %d: expected success at a boundary-collapsed ConfigSpace, got invalid: %s", i, e.InvalidReason)
		}
		if e.Config.ReferenceLatency != boundaryRef {
			t.Fatalf("evaluation %d: expected ReferenceLatency pinned exactly at the boundary %v, got %v", i, boundaryRef, e.Config.ReferenceLatency)
		}
		if e.Config.StaleAfter != boundaryStale {
			t.Fatalf("evaluation %d: expected StaleAfter pinned exactly at the boundary %v, got %v", i, boundaryStale, e.Config.StaleAfter)
		}
		if ok, reason := cs.Valid(e.Config); !ok {
			t.Fatalf("evaluation %d: boundary config failed its own space's validity check: %s", i, reason)
		}
	}
	if _, ok := result.Best(); !ok {
		t.Fatal("expected a valid Best() result at a boundary optimum")
	}
}

// TestTuningChallenge_NoisyObjectiveCompletes confirms the search
// tolerates run-to-run scenario noise (arrival-timing jitter, 007-H's
// own technique) without crashing or producing a non-finite utility --
// a "noisy objective" in the sense master context rule 39 names, not
// injected by corrupting the objective function itself, but by the kind
// of noise a real deployment's scenario generator would actually
// produce.
func TestTuningChallenge_NoisyObjectiveCompletes(t *testing.T) {
	noisy := tuning.ScenarioSpace{
		MinTargets: 2, MaxTargets: 4,
		MinServiceTime: 5 * time.Millisecond, MaxServiceTime: 150 * time.Millisecond,
		ArrivalSpacing: 5 * time.Millisecond, JitterFraction: 0.9, // aggressive jitter
		Requests:           150,
		FailureProbability: 0.7,
		MinFailureDuration: 200 * time.Millisecond,
		MaxFailureDuration: 1200 * time.Millisecond,
	}
	scenarios := noisy.GenerateSet(1, 10)
	rsc := tuning.RandomSearchConfig{
		Evaluations: 30, OptimizerSeed: 3, ConfigSpace: tuning.DefaultConfigSpace(), ObjectiveWeights: tuning.DefaultObjectiveWeights(),
	}

	result := tuning.RunRandomSearch(rsc, scenarios)
	best, ok := result.Best()
	if !ok {
		t.Fatal("expected a valid Best() result under an aggressively noisy scenario generator")
	}
	if math.IsNaN(best.Utility) || math.IsInf(best.Utility, 0) {
		t.Fatalf("expected a finite utility, got %v", best.Utility)
	}
	for i, e := range result.Evaluations {
		if e.Valid && (math.IsNaN(e.Utility) || math.IsInf(e.Utility, 0)) {
			t.Fatalf("evaluation %d: non-finite utility %v under noisy scenarios", i, e.Utility)
		}
	}
}
