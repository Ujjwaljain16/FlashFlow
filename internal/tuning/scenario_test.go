package tuning

import (
	"testing"

	"flashflow/internal/replay"
)

// TestScenarioSpace_GenerateProducesExecutableScenarios is the "an
// optimizer should never be allowed to create an invalid experiment
// silently" check (master context rule 12), applied directly: every
// generated Scenario must actually run to completion through RunWorld
// without error, for a range of seeds and both a stateless and a
// stateful policy.
func TestScenarioSpace_GenerateProducesExecutableScenarios(t *testing.T) {
	ss := DefaultScenarioSpace()
	for seed := int64(1); seed <= 50; seed++ {
		scenario := ss.Generate(seed)
		if len(scenario.Targets) < ss.MinTargets || len(scenario.Targets) > ss.MaxTargets {
			t.Fatalf("seed %d: target count %d outside [%d, %d]", seed, len(scenario.Targets), ss.MinTargets, ss.MaxTargets)
		}
		for _, tgt := range scenario.Targets {
			if tgt.ServiceTime < ss.MinServiceTime || tgt.ServiceTime > ss.MaxServiceTime {
				t.Fatalf("seed %d: target %s service time %v outside [%v, %v]", seed, tgt.Name, tgt.ServiceTime, ss.MinServiceTime, ss.MaxServiceTime)
			}
		}
		if len(scenario.Failures) > 0 {
			f := scenario.Failures[0]
			if f.DownAt >= f.UpAt {
				t.Fatalf("seed %d: failure DownAt %v not before UpAt %v", seed, f.DownAt, f.UpAt)
			}
			if f.DownAt < 0 {
				t.Fatalf("seed %d: failure DownAt %v is negative", seed, f.DownAt)
			}
		}

		if _, err := replay.RunWorld(scenario, replay.RoundRobinPolicy()); err != nil {
			t.Fatalf("seed %d: RunWorld (round-robin) failed: %v", seed, err)
		}
		if _, err := replay.RunWorld(scenario, replay.AdaptivePolicy()); err != nil {
			t.Fatalf("seed %d: RunWorld (adaptive) failed: %v", seed, err)
		}
	}
}

func TestScenarioSpace_GenerateIsDeterministicForASeed(t *testing.T) {
	ss := DefaultScenarioSpace()
	a := ss.Generate(7)
	b := ss.Generate(7)
	resultA, err := replay.RunWorld(a, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("RunWorld(a) failed: %v", err)
	}
	resultB, err := replay.RunWorld(b, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("RunWorld(b) failed: %v", err)
	}
	if idx, diverged := replay.FirstDivergence(resultA.Trace, resultB.Trace); diverged {
		t.Fatalf("same-seed scenarios diverged at event %d", idx)
	}
}

func TestNewSplit_DevelopmentAndHoldoutSeedsDoNotOverlap(t *testing.T) {
	split := NewSplit(DefaultScenarioSpace())
	seen := make(map[int64]bool, len(split.Development))
	for _, s := range split.Development {
		seen[s.Seed] = true
	}
	for _, s := range split.Holdout {
		if seen[s.Seed] {
			t.Fatalf("holdout seed %d overlaps a development seed", s.Seed)
		}
	}
	if len(split.Development) != 40 {
		t.Fatalf("expected 40 development scenarios, got %d", len(split.Development))
	}
	if len(split.Holdout) != 20 {
		t.Fatalf("expected 20 holdout scenarios, got %d", len(split.Holdout))
	}
}
