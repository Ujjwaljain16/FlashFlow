package tuning

import (
	"sort"
	"testing"
)

func TestLHSTuner_Interface(t *testing.T) {
	var _ Tuner = NewLHSTuner(1, DefaultConfigSpace(), 10)
}

func TestLHSTuner_ProducesExactlyEvaluationsValidConfigs(t *testing.T) {
	const n = 20
	tuner := NewLHSTuner(1, DefaultConfigSpace(), n)
	var previous []TrialResult
	for i := 0; i < n; i++ {
		cfg := tuner.Suggest(previous)
		if ok, reason := tuner.Space().Valid(cfg); !ok {
			t.Fatalf("design point %d is invalid: %s", i, reason)
		}
		previous = append(previous, TrialResult{Config: cfg, Utility: 0, Valid: true})
	}
}

// TestLHSTuner_StratifiesReferenceLatency verifies the defining Latin
// Hypercube property directly: dividing [ReferenceLatencyMin,
// ReferenceLatencyMax] into n equal-width bins, EXACTLY one design
// point's ReferenceLatency must fall in each bin -- not "on average
// evenly distributed" (true of plain uniform sampling too), but
// EXACTLY one per bin, every time. ReferenceLatency is a clean choice
// for this check since configFromUnitCube maps it via a strictly
// monotonic, one-dimensional transform (u[4] -> Min + u[4]*range),
// so verifying its stratification directly verifies dimension 4's own
// stratum permutation was applied correctly.
func TestLHSTuner_StratifiesReferenceLatency(t *testing.T) {
	const n = 30
	space := DefaultConfigSpace()
	tuner := NewLHSTuner(1, space, n)

	var refLatencies []float64
	for i := 0; i < n; i++ {
		cfg := tuner.Suggest(nil)
		frac := float64(cfg.ReferenceLatency-space.ReferenceLatencyMin) / float64(space.ReferenceLatencyMax-space.ReferenceLatencyMin)
		refLatencies = append(refLatencies, frac)
	}
	sort.Float64s(refLatencies)

	for i, frac := range refLatencies {
		lo := float64(i) / float64(n)
		hi := float64(i+1) / float64(n)
		if frac < lo || frac >= hi {
			t.Errorf("sorted ReferenceLatency fraction %d = %v, want within bin [%v, %v) -- Latin Hypercube stratification violated", i, frac, lo, hi)
		}
	}
}

func TestLHSTuner_ExhaustsDesignThenFallsBackToRandom(t *testing.T) {
	const n = 5
	tuner := NewLHSTuner(1, DefaultConfigSpace(), n)
	for i := 0; i < n; i++ {
		tuner.Suggest(nil)
	}
	// One more call than the design has points for -- must not panic,
	// and must still produce a valid config via the documented
	// fallback.
	extra := tuner.Suggest(nil)
	if ok, reason := tuner.Space().Valid(extra); !ok {
		t.Errorf("post-exhaustion fallback produced an invalid config: %s", reason)
	}
}

func TestLHSTuner_Determinism(t *testing.T) {
	a := NewLHSTuner(42, DefaultConfigSpace(), 10)
	b := NewLHSTuner(42, DefaultConfigSpace(), 10)
	for i := 0; i < 10; i++ {
		ca := a.Suggest(nil)
		cb := b.Suggest(nil)
		if ca != cb {
			t.Fatalf("design point %d differs between two same-seed LHSTuners: %+v vs %+v", i, ca, cb)
		}
	}
}

func TestLHSTuner_DifferentSeedsDifferentDesigns(t *testing.T) {
	a := NewLHSTuner(1, DefaultConfigSpace(), 10)
	b := NewLHSTuner(2, DefaultConfigSpace(), 10)
	same := true
	for i := 0; i < 10; i++ {
		if a.Suggest(nil) != b.Suggest(nil) {
			same = false
		}
	}
	if same {
		t.Fatal("expected different seeds to produce different LHS designs")
	}
}

func TestLHSTuner_ZeroEvaluationsProducesEmptyDesign(t *testing.T) {
	tuner := NewLHSTuner(1, DefaultConfigSpace(), 0)
	// Should fall straight to the random-sampling fallback without
	// panicking on an empty design.
	cfg := tuner.Suggest(nil)
	if ok, reason := tuner.Space().Valid(cfg); !ok {
		t.Errorf("expected a valid fallback config for a zero-size design: %s", reason)
	}
}
