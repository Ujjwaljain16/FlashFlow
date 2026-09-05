package statistics

import (
	"errors"
	"math"
	"testing"
)

func TestMannWhitneyU_EmptySample(t *testing.T) {
	if _, err := MannWhitneyU(nil, []float64{1, 2}); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
	if _, err := MannWhitneyU([]float64{1, 2}, nil); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
}

func TestMannWhitneyU_RejectsNonFinite(t *testing.T) {
	if _, err := MannWhitneyU([]float64{1, math.NaN()}, []float64{1, 2}); err == nil {
		t.Fatal("expected an error for a NaN value")
	}
}

// TestMannWhitneyU_CompletelySeparated is a hand-verifiable case: every
// value in A is below every value in B, so U_A must be exactly 0 and
// U_B exactly nA*nB (the maximum possible separation).
func TestMannWhitneyU_CompletelySeparated(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	res, err := MannWhitneyU(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.UA != 0 {
		t.Fatalf("expected UA=0, got %v", res.UA)
	}
	if res.UB != 9 {
		t.Fatalf("expected UB=9, got %v", res.UB)
	}
	if res.PValue >= 0.5 {
		t.Fatalf("expected a small p-value for total separation, got %v", res.PValue)
	}
}

// TestMannWhitneyU_Symmetry checks that swapping the two samples swaps
// UA/UB and leaves the (two-sided) p-value unchanged -- a structural
// property any correct implementation must have, independent of the
// exact numbers involved.
func TestMannWhitneyU_Symmetry(t *testing.T) {
	a := []float64{5, 1, 9, 3, 7, 2}
	b := []float64{4, 8, 6, 10, 2, 3}

	ab, err := MannWhitneyU(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ba, err := MannWhitneyU(b, a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if math.Abs(ab.UA-ba.UB) > 1e-9 || math.Abs(ab.UB-ba.UA) > 1e-9 {
		t.Fatalf("expected UA/UB to swap: ab=%+v ba=%+v", ab, ba)
	}
	if math.Abs(ab.PValue-ba.PValue) > 1e-9 {
		t.Fatalf("expected the same p-value regardless of argument order: %v vs %v", ab.PValue, ba.PValue)
	}
}

// TestMannWhitneyU_IdenticalDistributionsGivesLargePValue uses two
// samples drawn from the exact same fixed set of values (heavily
// overlapping, many ties) and expects no evidence of a shift.
func TestMannWhitneyU_IdenticalDistributionsGivesLargePValue(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	res, err := MannWhitneyU(vals, vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PValue < 0.9 {
		t.Fatalf("expected a high p-value for identical samples, got %v", res.PValue)
	}
	// UA should sit exactly at the midpoint (nA*nB/2) when the two
	// samples are indistinguishable.
	wantMid := float64(len(vals)*len(vals)) / 2
	if math.Abs(res.UA-wantMid) > 1e-9 {
		t.Fatalf("expected UA at the midpoint %v, got %v", wantMid, res.UA)
	}
}

// TestMannWhitneyU_LocationShiftOnly_DoesNotFlagAPureShapeDifference
// regression-tests the lesson behind 006-C's statistical-method
// correction (cmd/experiment-006c): a first attempt compared per-burst
// failure patterns via Mann-Whitney and got an unstable p-value (0.003 to
// 0.41 across identical reruns) because the actual phenomenon being
// compared -- coalescing concentrating failure all-or-nothing on whole
// bursts versus spreading it across partial failures -- is a *shape*
// difference (bimodal vs. spread), not a location/central-tendency one,
// and Mann-Whitney only ever tests the latter. This test encodes that
// boundary directly: two samples with essentially the same median but
// very different shapes (bimodal-at-the-extremes vs. uniformly spread)
// must NOT produce a small p-value from MannWhitneyU alone -- proving the
// test correctly has nothing useful to say about a pure shape difference,
// so a future experiment facing the same kind of question is warned by
// this test's own existence to reach for a shape-sensitive statistic
// (e.g. comparing the all-or-nothing proportion directly, as 006-C's
// corrected approach did) instead of reintroducing 006-C's original
// mistake.
func TestMannWhitneyU_LocationShiftOnly_DoesNotFlagAPureShapeDifference(t *testing.T) {
	// Bimodal/"all-or-nothing": half at 0, half at 1 -- median 0.5.
	bimodal := make([]float64, 20)
	for i := range bimodal {
		if i%2 == 0 {
			bimodal[i] = 0
		} else {
			bimodal[i] = 1
		}
	}
	// Uniformly spread across the identical [0,1] range -- median also 0.5.
	spread := make([]float64, 20)
	for i := range spread {
		spread[i] = float64(i) / float64(len(spread)-1)
	}

	res, err := MannWhitneyU(bimodal, spread)
	if err != nil {
		t.Fatalf("MannWhitneyU failed: %v", err)
	}
	if res.PValue < 0.3 {
		t.Fatalf("expected a large, non-significant p-value for two same-median but different-shape samples (got %v) -- if this now fails, Mann-Whitney is (correctly or not) detecting a shape difference it isn't designed to test, and any experiment relying on this test's documented boundary should be revisited", res.PValue)
	}
}

// TestMannWhitneyU_HandVerifiedTieCase checks the tie-correction path
// against ranks computed by hand:
//
//	combined sorted: 1(A) 2(A) 2(A) 2(B) 3(A) 3(B) 3(B) 4(B)
//	rank(1)=1; rank(2-group, avg of positions 2,3,4)=3; rank(3-group, avg
//	of positions 5,6,7)=6; rank(4)=8
//	R_A = 1 + 3 + 3 + 6 = 13; U_A = 13 - 4*5/2 = 3; U_B = 16 - 3 = 13
//	Two tie groups (the 2s and the 3s), each of size 3.
func TestMannWhitneyU_HandVerifiedTieCase(t *testing.T) {
	a := []float64{1, 2, 2, 3}
	b := []float64{2, 3, 3, 4}
	res, err := MannWhitneyU(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.UA != 3 {
		t.Fatalf("expected UA=3, got %v", res.UA)
	}
	if res.UB != 13 {
		t.Fatalf("expected UB=13, got %v", res.UB)
	}
	if res.TieGroups != 2 {
		t.Fatalf("expected 2 tie groups, got %d", res.TieGroups)
	}
}

func TestMannWhitneyU_AllValuesIdenticalAcrossBothSamples(t *testing.T) {
	a := []float64{5, 5, 5}
	b := []float64{5, 5, 5, 5}
	res, err := MannWhitneyU(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PValue != 1 {
		t.Fatalf("expected p-value 1 when every observation is tied, got %v", res.PValue)
	}
}

func TestMannWhitneyU_SingleObservationEachSide(t *testing.T) {
	res, err := MannWhitneyU([]float64{1}, []float64{2})
	if err != nil {
		t.Fatalf("unexpected error with minimal samples: %v", err)
	}
	if res.NA != 1 || res.NB != 1 {
		t.Fatalf("unexpected sample sizes: %+v", res)
	}
}
