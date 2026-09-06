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
		scenario := ss.GenerateFromRoot(seed)
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
	a := ss.GenerateFromRoot(7)
	b := ss.GenerateFromRoot(7)
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
		seen[s.Seeds.Global] = true
	}
	for _, s := range split.Holdout {
		if seen[s.Seeds.Global] {
			t.Fatalf("holdout seed %d overlaps a development seed", s.Seeds.Global)
		}
	}
	if len(split.Development) != 40 {
		t.Fatalf("expected 40 development scenarios, got %d", len(split.Development))
	}
	if len(split.Holdout) != 20 {
		t.Fatalf("expected 20 holdout scenarios, got %d", len(split.Holdout))
	}
}

// TestGenerateFromRoot_EquivalentToGenerateDeriveSeeds proves the
// refactor GenerateFromRoot was introduced to formalize is genuinely
// behavior-preserving: GenerateFromRoot(N) must produce the byte-for-
// byte identical Scenario as calling Generate directly with
// replay.DeriveSeeds(N) -- if these ever diverged, GenerateFromRoot
// would be a second, silently-different code path rather than a named
// convenience wrapper.
func TestGenerateFromRoot_EquivalentToGenerateDeriveSeeds(t *testing.T) {
	ss := DefaultScenarioSpace()
	for _, root := range []int64{1, 7, 12345} {
		a := ss.GenerateFromRoot(root)
		b := ss.Generate(replay.DeriveSeeds(root))
		if a.Seeds != b.Seeds {
			t.Fatalf("root %d: GenerateFromRoot's Seeds %+v != Generate(DeriveSeeds(...))'s Seeds %+v", root, a.Seeds, b.Seeds)
		}
		if len(a.Targets) != len(b.Targets) || len(a.Arrivals) != len(b.Arrivals) {
			t.Fatalf("root %d: GenerateFromRoot and Generate(DeriveSeeds(...)) produced different-shaped scenarios", root)
		}
		for i := range a.Targets {
			if a.Targets[i] != b.Targets[i] {
				t.Fatalf("root %d: target %d differs: %+v vs %+v", root, i, a.Targets[i], b.Targets[i])
			}
		}
		for i := range a.Arrivals {
			if a.Arrivals[i] != b.Arrivals[i] {
				t.Fatalf("root %d: arrival %d differs: %+v vs %+v", root, i, a.Arrivals[i], b.Arrivals[i])
			}
		}
	}
}

// TestGenerate_IndependentAxisControl demonstrates the entire point of
// widening Scenario.Seed into a SeedTree (Stage 10, §10.3's confirmed
// design decision): holding Traffic and Topology fixed while varying
// Failure must produce IDENTICAL targets and arrivals, with only the
// failure window (presence, timing, or target) potentially differing.
// Under the old single-shared-RNG design this was impossible -- any
// change consumed from the RNG for the failure draw would have shifted
// every draw sequenced after it, and here failure is drawn last, so the
// old design would have gotten this particular case right by accident;
// the real proof is that Topology and Traffic draws never even
// consult seeds.Failure now, by construction, not by argument.
func TestGenerate_IndependentAxisControl(t *testing.T) {
	ss := DefaultScenarioSpace()
	base := replay.DeriveSeeds(1)

	varyFailure := base
	varyFailure.Failure = base.Failure + 999999 // an arbitrary, different failure seed

	a := ss.Generate(base)
	b := ss.Generate(varyFailure)

	if len(a.Targets) != len(b.Targets) {
		t.Fatalf("expected identical target count with only Failure varied, got %d vs %d", len(a.Targets), len(b.Targets))
	}
	for i := range a.Targets {
		if a.Targets[i] != b.Targets[i] {
			t.Fatalf("target %d differs despite only Failure seed changing: %+v vs %+v", i, a.Targets[i], b.Targets[i])
		}
	}
	if len(a.Arrivals) != len(b.Arrivals) {
		t.Fatalf("expected identical arrival count with only Failure varied, got %d vs %d", len(a.Arrivals), len(b.Arrivals))
	}
	for i := range a.Arrivals {
		if a.Arrivals[i] != b.Arrivals[i] {
			t.Fatalf("arrival %d differs despite only Failure seed changing: %+v vs %+v", i, a.Arrivals[i], b.Arrivals[i])
		}
	}
}
