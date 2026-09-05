package statistics

import (
	"errors"
	"math"
	"testing"
)

func TestCliffsDelta_EmptySample(t *testing.T) {
	if _, err := CliffsDelta(nil, []float64{1}); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
}

func TestCliffsDelta_RejectsNonFinite(t *testing.T) {
	if _, err := CliffsDelta([]float64{1, math.Inf(1)}, []float64{1, 2}); err == nil {
		t.Fatal("expected an error for an infinite value")
	}
}

func TestCliffsDelta_CompleteDominanceIsPlusOne(t *testing.T) {
	res, err := CliffsDelta([]float64{4, 5, 6}, []float64{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Delta != 1 {
		t.Fatalf("expected delta=1, got %v", res.Delta)
	}
	if res.Magnitude != "large" {
		t.Fatalf("expected magnitude large, got %v", res.Magnitude)
	}
}

func TestCliffsDelta_CompleteInverseDominanceIsMinusOne(t *testing.T) {
	res, err := CliffsDelta([]float64{1, 2, 3}, []float64{4, 5, 6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Delta != -1 {
		t.Fatalf("expected delta=-1, got %v", res.Delta)
	}
}

func TestCliffsDelta_Antisymmetry(t *testing.T) {
	a := []float64{5, 1, 9, 3, 7}
	b := []float64{4, 8, 6, 2, 3}
	ab, err := CliffsDelta(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ba, err := CliffsDelta(b, a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(ab.Delta+ba.Delta) > 1e-9 {
		t.Fatalf("expected delta(a,b) == -delta(b,a), got %v and %v", ab.Delta, ba.Delta)
	}
}

func TestCliffsDelta_IdenticalSamplesIsZero(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	res, err := CliffsDelta(vals, vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Delta != 0 {
		t.Fatalf("expected delta=0 for identical samples, got %v", res.Delta)
	}
	if res.Magnitude != "negligible" {
		t.Fatalf("expected magnitude negligible, got %v", res.Magnitude)
	}
}

// TestCliffsDelta_HandVerifiedPartialOverlap: A=[1,2,3], B=[2,3,4].
// Pairs (9 total): (1,2)<,(1,3)<,(1,4)<, (2,2)=,(2,3)<,(2,4)<,
// (3,2)>,(3,3)=,(3,4)<. greater=1, less=6, equal=2.
// delta = (1-6)/9 = -5/9.
func TestCliffsDelta_HandVerifiedPartialOverlap(t *testing.T) {
	res, err := CliffsDelta([]float64{1, 2, 3}, []float64{2, 3, 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := -5.0 / 9.0
	if math.Abs(res.Delta-want) > 1e-9 {
		t.Fatalf("expected delta=%v, got %v", want, res.Delta)
	}
}

func TestCliffsDeltaMagnitude_Thresholds(t *testing.T) {
	cases := []struct {
		delta float64
		want  string
	}{
		{0.0, "negligible"}, {0.1, "negligible"},
		{0.2, "small"}, {0.32, "small"},
		{0.4, "medium"}, {0.47, "medium"},
		{0.5, "large"}, {0.9, "large"},
		{-0.5, "large"}, // magnitude ignores sign
	}
	for _, c := range cases {
		got := CliffsDeltaMagnitude(c.delta)
		if got != c.want {
			t.Fatalf("delta=%v: expected %v, got %v", c.delta, c.want, got)
		}
	}
}
