package tuning

import (
	"testing"

	"flashflow/internal/proxy"
)

func TestRunRandomSearch_IsDeterministicForTheSameSeed(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 10)
	rsc := RandomSearchConfig{Evaluations: 20, OptimizerSeed: 42, ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights()}

	r1 := RunRandomSearch(rsc, scenarios)
	r2 := RunRandomSearch(rsc, scenarios)

	if len(r1.Evaluations) != len(r2.Evaluations) {
		t.Fatalf("evaluation counts differ: %d vs %d", len(r1.Evaluations), len(r2.Evaluations))
	}
	for i := range r1.Evaluations {
		if r1.Evaluations[i].ConfigHash != r2.Evaluations[i].ConfigHash {
			t.Fatalf("evaluation %d sampled a different config across runs with the same OptimizerSeed", i)
		}
		if r1.Evaluations[i].Utility != r2.Evaluations[i].Utility {
			t.Fatalf("evaluation %d produced a different utility across runs with the same OptimizerSeed", i)
		}
	}
	if r1.BestIndex != r2.BestIndex {
		t.Fatalf("best index differs: %d vs %d", r1.BestIndex, r2.BestIndex)
	}
}

func TestRunRandomSearch_EveryEvaluationIsRecorded(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 5)
	rsc := RandomSearchConfig{Evaluations: 15, OptimizerSeed: 1, ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights()}
	r := RunRandomSearch(rsc, scenarios)
	if len(r.Evaluations) != 15 {
		t.Fatalf("expected 15 recorded evaluations (master context rule 13: never discard), got %d", len(r.Evaluations))
	}
	for i, e := range r.Evaluations {
		if e.Index != i {
			t.Fatalf("evaluation %d has Index %d, expected sequential indices", i, e.Index)
		}
		if !e.Valid {
			t.Fatalf("evaluation %d unexpectedly invalid: %s", i, e.InvalidReason)
		}
	}
}

func TestRunRandomSearch_BestIndexIsTrulyTheBest(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 10)
	rsc := RandomSearchConfig{Evaluations: 30, OptimizerSeed: 7, ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights()}
	r := RunRandomSearch(rsc, scenarios)

	best, ok := r.Best()
	if !ok {
		t.Fatal("expected at least one valid evaluation")
	}
	for _, e := range r.Evaluations {
		if e.Valid && e.Utility > best.Utility {
			t.Fatalf("evaluation %d (utility %v) exceeds the reported best (utility %v)", e.Index, e.Utility, best.Utility)
		}
	}
}

// TestEvaluateWithCache_HitReturnsCachedValueWithoutRecomputing seeds
// the cache with a deliberately wrong-looking result for a given config
// hash, then confirms a lookup for that same hash returns exactly the
// seeded (fabricated) value rather than a freshly-computed one -- the
// only way that could happen is if the cache path, not Evaluate, served
// the answer.
func TestEvaluateWithCache_HitReturnsCachedValueWithoutRecomputing(t *testing.T) {
	cfg := proxy.DefaultAdaptiveConfig()
	hash := Hash(cfg)
	fabricated := cachedEval{
		Metrics: Metrics{P99LatencyMs: 999999}, // nothing a real evaluation could plausibly produce
		Scores:  Scores{LatencyScore: 0.123456},
	}
	cache := map[string]cachedEval{hash: fabricated}

	// scenarios is nil: if this ever fell through to a real Evaluate
	// call, it would fail (or panic) rather than silently succeed --
	// making the test fail loudly if caching isn't actually engaging.
	m, s, hit, err := evaluateWithCache(cache, hash, cfg, nil)
	if err != nil {
		t.Fatalf("expected the cache hit path, got an error instead: %v", err)
	}
	if !hit {
		t.Fatal("expected hit=true for a pre-seeded cache entry")
	}
	if m != fabricated.Metrics || s != fabricated.Scores {
		t.Fatalf("expected the fabricated cached values back, got Metrics=%+v Scores=%+v", m, s)
	}
}

func TestEvaluateWithCache_MissComputesAndStores(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 5)
	cfg := proxy.DefaultAdaptiveConfig()
	hash := Hash(cfg)
	cache := make(map[string]cachedEval)

	m, s, hit, err := evaluateWithCache(cache, hash, cfg, scenarios)
	if err != nil {
		t.Fatalf("evaluateWithCache failed: %v", err)
	}
	if hit {
		t.Fatal("expected hit=false on an empty cache")
	}
	cached, ok := cache[hash]
	if !ok {
		t.Fatal("expected the miss path to populate the cache")
	}
	if cached.Metrics != m || cached.Scores != s {
		t.Fatalf("cached entry doesn't match the returned result: cached=%+v/%+v returned=%+v/%+v", cached.Metrics, cached.Scores, m, s)
	}
}

// TestEvaluateCandidate_RejectsOutOfSpaceConfigWithoutCallingEvaluate
// regression-tests F-24: RunRandomSearch's loop sampled a candidate and
// went straight to evaluateWithCache/Hash, never calling
// ConfigSpace.Valid -- safe only by accident of ConfigSpace.Sample always
// producing a valid point today. A hand-built out-of-simplex config
// (weights summing far from 1) must be rejected and recorded as invalid
// without ever reaching Evaluate -- proven here with a nil scenarios
// slice that would fail loudly if Evaluate were reached.
func TestEvaluateCandidate_RejectsOutOfSpaceConfigWithoutCallingEvaluate(t *testing.T) {
	cs := DefaultConfigSpace()
	badCfg := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: 5, Latency: 5, Cache: 5, Cost: 5}, // sums to 20, not 1
		ReferenceLatency: cs.ReferenceLatencyMin,
		StaleAfter:       cs.StaleAfterMin,
	}
	if ok, _ := cs.Valid(badCfg); ok {
		t.Fatalf("test setup invalid: badCfg must actually be rejected by ConfigSpace.Valid")
	}

	cache := make(map[string]cachedEval)
	// scenarios is nil: if evaluateCandidate ever fell through to
	// Evaluate, it would error/panic on the nil slice rather than silently
	// return a plausible-looking result.
	_, _, cacheHit, valid, reason := evaluateCandidate(cs, cache, Hash(badCfg), badCfg, nil)

	if valid {
		t.Fatalf("expected an out-of-space candidate to be rejected as invalid")
	}
	if cacheHit {
		t.Fatalf("expected cacheHit=false for a rejected candidate")
	}
	if reason == "" {
		t.Fatalf("expected a non-empty InvalidReason explaining the rejection")
	}
	if len(cache) != 0 {
		t.Fatalf("expected the cache to remain untouched by a rejected candidate, got %d entries", len(cache))
	}
}

func TestConvergence_DetectsPlateau(t *testing.T) {
	evals := []Evaluation{
		{Index: 0, Valid: true, Utility: 0.1},
		{Index: 1, Valid: true, Utility: 0.5},
		{Index: 2, Valid: true, Utility: 0.9},
		{Index: 3, Valid: true, Utility: 0.9}, // no improvement from here on
		{Index: 4, Valid: true, Utility: 0.9},
		{Index: 5, Valid: true, Utility: 0.9},
		{Index: 6, Valid: true, Utility: 0.9},
		{Index: 7, Valid: true, Utility: 0.9},
	}
	c := computeConvergence(evals)
	if !c.Plateaued {
		t.Fatalf("expected plateau to be detected after 5 unimproved evaluations, got %+v", c)
	}
	if c.LastImprovedAtIndex != 2 {
		t.Fatalf("expected LastImprovedAtIndex 2, got %d", c.LastImprovedAtIndex)
	}
}

func TestConvergence_StillImprovingIsNotAPlateau(t *testing.T) {
	evals := []Evaluation{
		{Index: 0, Valid: true, Utility: 0.1},
		{Index: 1, Valid: true, Utility: 0.2},
		{Index: 2, Valid: true, Utility: 0.3},
		{Index: 3, Valid: true, Utility: 0.4},
		{Index: 4, Valid: true, Utility: 0.5},
		{Index: 5, Valid: true, Utility: 0.6},
		{Index: 6, Valid: true, Utility: 0.7},
		{Index: 7, Valid: true, Utility: 0.9}, // improved in the final quarter
	}
	c := computeConvergence(evals)
	if c.Plateaued {
		t.Fatalf("expected no plateau when the final quarter still improved, got %+v", c)
	}
}

func TestConvergence_InvalidEvaluationsAreExcludedFromTheCurve(t *testing.T) {
	evals := []Evaluation{
		{Index: 0, Valid: true, Utility: 0.5},
		{Index: 1, Valid: false, InvalidReason: "boom"},
		{Index: 2, Valid: true, Utility: 0.7},
	}
	c := computeConvergence(evals)
	if len(c.BestSoFarUtility) != 2 {
		t.Fatalf("expected the invalid evaluation to be excluded from the best-so-far curve, got %d entries", len(c.BestSoFarUtility))
	}
}
