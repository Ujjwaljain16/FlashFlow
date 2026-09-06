package tuning

import (
	"reflect"
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

// TestGenerate_TopologySeedIndependentOfFailureSelection is the Stage 12
// Track B fix confirmation for a real reproducibility hazard Stage 11
// Program G discovered (docs/StageArtifacts/Stage11.md §16): the previous
// TestGenerate_IndependentAxisControl only ever checked ONE direction
// (varying Failure leaves Topology/Traffic unchanged) -- it never checked
// the reverse. Generate used to draw target COUNT n from topoRNG, then
// draw failureRNG.Intn(n) to pick which target fails -- since n is
// topology-seed-dependent whenever MinTargets != MaxTargets, the same
// failureRNG seed and draw sequence could select a different failure
// outcome purely because Topology changed n.
//
// The fix (scenario.go's Generate): the failure-target candidate is now
// drawn from Intn(len(targetNames)) -- the FIXED name-pool size, never
// derived from topoRNG -- so the draw itself never depends on n. This
// test confirms full independence with target count held fixed (so the
// candidate draw is always valid, isolating the axis-independence claim
// from the separate "what happens when it's invalid" case covered by
// TestGenerate_SmallTopologyCanSuppressAnOtherwiseRolledFailure below).
func TestGenerate_TopologySeedIndependentOfFailureSelection(t *testing.T) {
	ss := DefaultScenarioSpace()
	ss.MinTargets, ss.MaxTargets = len(targetNames), len(targetNames) // n always == len(targetNames): every failureRNG candidate is always valid
	base := replay.SeedTree{Global: 0, Traffic: 100, Topology: 200, Failure: 300, Policy: 400}

	for _, topologySeed := range []int64{999, 12345, -42, 0} {
		varyTopology := base
		varyTopology.Topology = topologySeed

		a := ss.Generate(base)
		b := ss.Generate(varyTopology)

		if len(a.Failures) != len(b.Failures) {
			t.Fatalf("topology seed %d: expected identical failure PRESENCE, got %d vs %d failure windows", topologySeed, len(a.Failures), len(b.Failures))
		}
		for i := range a.Failures {
			if a.Failures[i] != b.Failures[i] {
				t.Fatalf("topology seed %d: varying ONLY Topology changed the failure window: %+v vs %+v -- independence is broken", topologySeed, a.Failures[i], b.Failures[i])
			}
		}
	}
}

// TestGenerate_SmallTopologyCanSuppressAnOtherwiseRolledFailure documents
// the accepted, disclosed consequence of the Track B fix: a topology
// smaller than the full name pool has a correspondingly higher chance
// that the fixed-pool failure-target draw lands outside its own target
// set, in which case this scenario simply has no failure even though the
// FailureProbability roll succeeded. This is deliberate (reintroducing
// n-dependent reshuffling to force a failure to happen would restore the
// exact coupling Track B removes), not an oversight -- this test exists
// so the behavior is pinned and explained, not silently discovered later.
func TestGenerate_SmallTopologyCanSuppressAnOtherwiseRolledFailure(t *testing.T) {
	small := DefaultScenarioSpace()
	small.MinTargets, small.MaxTargets = 1, 1
	small.FailureProbability = 1.0 // always roll "yes, attempt a failure"

	full := DefaultScenarioSpace()
	full.MinTargets, full.MaxTargets = len(targetNames), len(targetNames)
	full.FailureProbability = 1.0

	// Sweep enough Failure seeds that at least one produces a candidate
	// index >= 1 (out of range for the 1-target topology) -- with 5
	// possible candidates and a uniform draw, roughly 4/5 of seeds should
	// qualify, so a handful of tries suffices deterministically enough
	// for a unit test without flakiness in practice.
	foundSuppressed := false
	for failureSeed := int64(0); failureSeed < 20; failureSeed++ {
		seeds := replay.SeedTree{Global: 0, Traffic: 1, Topology: 1, Failure: failureSeed, Policy: 1}
		smallScenario := small.Generate(seeds)
		fullScenario := full.Generate(seeds)
		if len(fullScenario.Failures) == 1 && len(smallScenario.Failures) == 0 {
			foundSuppressed = true
			break
		}
	}
	if !foundSuppressed {
		t.Fatal("expected at least one Failure seed (out of 20 tried) where the 1-target topology suppresses a failure the full-size topology would have applied -- the out-of-range-suppression path may be broken")
	}
}

// TestGenerate_TrafficSeedIndependentOfTopologyAndFailure completes the
// symmetric set of axis-ownership tests (Stage 12 Track B, Section 9):
// varying ONLY Traffic must leave Targets and Failures unchanged, and
// must actually change Arrivals (a seed axis that changes nothing when
// varied would be exactly as broken as one that leaks into another axis).
func TestGenerate_TrafficSeedIndependentOfTopologyAndFailure(t *testing.T) {
	ss := DefaultScenarioSpace()
	base := replay.SeedTree{Global: 0, Traffic: 100, Topology: 200, Failure: 300, Policy: 400}
	varyTraffic := base
	varyTraffic.Traffic = 999

	a := ss.Generate(base)
	b := ss.Generate(varyTraffic)

	if len(a.Targets) != len(b.Targets) {
		t.Fatalf("expected identical target count with only Traffic varied, got %d vs %d", len(a.Targets), len(b.Targets))
	}
	for i := range a.Targets {
		if a.Targets[i] != b.Targets[i] {
			t.Fatalf("target %d differs despite only Traffic seed changing: %+v vs %+v", i, a.Targets[i], b.Targets[i])
		}
	}
	if len(a.Failures) != len(b.Failures) {
		t.Fatalf("expected identical failure presence with only Traffic varied, got %d vs %d", len(a.Failures), len(b.Failures))
	}
	for i := range a.Failures {
		if a.Failures[i] != b.Failures[i] {
			t.Fatalf("failure window %d differs despite only Traffic seed changing: %+v vs %+v", i, a.Failures[i], b.Failures[i])
		}
	}
	if reflect.DeepEqual(a.Arrivals, b.Arrivals) {
		t.Fatal("expected Arrivals to differ when Traffic seed changes (jitter is Traffic-seed-derived) -- a Traffic seed that changes nothing would be exactly as broken as one that leaks into another axis")
	}
}

// TestGenerate_PolicySeedOwnsNoScenarioContent documents, rather than
// merely assumes, that Generate never reads seeds.Policy at all: a
// Scenario is exogenous "physics," and no policy's own randomness (e.g.
// P2C's pair sampling, drawn from seeds.Policy inside
// internal/replay/policies.go, not here) is part of it. The correct
// expectation is ownership, not "every axis must change something at
// every layer" -- Stage 11 Program G's G3 already confirmed Policy-seed
// ownership at the routing-decision level (only p2c-load consumes it);
// this test pins the complementary claim at the generator level.
func TestGenerate_PolicySeedOwnsNoScenarioContent(t *testing.T) {
	ss := DefaultScenarioSpace()
	base := replay.SeedTree{Global: 0, Traffic: 100, Topology: 200, Failure: 300, Policy: 400}
	varyPolicy := base
	varyPolicy.Policy = 999

	a := ss.Generate(base)
	b := ss.Generate(varyPolicy)

	if !reflect.DeepEqual(a.Targets, b.Targets) || !reflect.DeepEqual(a.Arrivals, b.Arrivals) || !reflect.DeepEqual(a.Failures, b.Failures) {
		t.Fatalf("expected a byte-identical Scenario when only Policy varies (Generate has no Policy-seed consumer): targets equal=%v arrivals equal=%v failures equal=%v",
			reflect.DeepEqual(a.Targets, b.Targets), reflect.DeepEqual(a.Arrivals, b.Arrivals), reflect.DeepEqual(a.Failures, b.Failures))
	}
}

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
