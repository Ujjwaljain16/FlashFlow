package statistics

import (
	"errors"
	"math"
	"testing"
)

func TestPercentile_EmptySample(t *testing.T) {
	if _, err := Percentile(nil, 50); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
}

func TestPercentile_InvalidP(t *testing.T) {
	for _, p := range []float64{-1, 100.1, 200} {
		if _, err := Percentile([]float64{1, 2, 3}, p); err == nil {
			t.Fatalf("expected an error for p=%v", p)
		}
	}
}

func TestPercentile_RejectsNaNAndInf(t *testing.T) {
	if _, err := Percentile([]float64{1, math.NaN(), 3}, 50); err == nil {
		t.Fatal("expected an error for a NaN value")
	}
	if _, err := Percentile([]float64{1, math.Inf(1), 3}, 50); err == nil {
		t.Fatal("expected an error for an infinite value")
	}
}

func TestPercentile_SingleValue(t *testing.T) {
	v, err := Percentile([]float64{42}, 0)
	if err != nil || v != 42 {
		t.Fatalf("expected 42, got %v err=%v", v, err)
	}
	v, err = Percentile([]float64{42}, 100)
	if err != nil || v != 42 {
		t.Fatalf("expected 42, got %v err=%v", v, err)
	}
}

func TestPercentile_KnownValues(t *testing.T) {
	// 1..10: p50 should be the interpolated midpoint (5.5), p0 the min, p100 the max.
	samples := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		p    float64
		want float64
	}{
		{0, 1}, {100, 10}, {50, 5.5}, {25, 3.25}, {75, 7.75},
	}
	for _, c := range cases {
		got, err := Percentile(samples, c.p)
		if err != nil {
			t.Fatalf("p=%v: unexpected error: %v", c.p, err)
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("p=%v: expected %v, got %v", c.p, c.want, got)
		}
	}
}

func TestPercentile_UnsortedInputSameAsSorted(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5}
	shuffled := []float64{3, 1, 5, 2, 4}
	for _, p := range []float64{0, 25, 50, 75, 100} {
		want, _ := Percentile(sorted, p)
		got, _ := Percentile(shuffled, p)
		if want != got {
			t.Fatalf("p=%v: sorted gave %v, shuffled gave %v", p, want, got)
		}
	}
}

func TestPercentile_DoesNotMutateInput(t *testing.T) {
	samples := []float64{5, 3, 1, 4, 2}
	original := append([]float64(nil), samples...)
	Percentile(samples, 50)
	for i := range samples {
		if samples[i] != original[i] {
			t.Fatalf("input was mutated: got %v, want %v", samples, original)
		}
	}
}

func TestPercentile_AllIdenticalValues(t *testing.T) {
	samples := []float64{7, 7, 7, 7, 7}
	for _, p := range []float64{0, 50, 99, 100} {
		got, err := Percentile(samples, p)
		if err != nil || got != 7 {
			t.Fatalf("p=%v: expected 7, got %v err=%v", p, got, err)
		}
	}
}

func TestMean_KnownValue(t *testing.T) {
	m, err := Mean([]float64{1, 2, 3, 4, 5})
	if err != nil || m != 3 {
		t.Fatalf("expected 3, got %v err=%v", m, err)
	}
}

func TestMean_EmptySample(t *testing.T) {
	if _, err := Mean(nil); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
}

func TestStdDev_KnownValue(t *testing.T) {
	// Sample stddev of 2,4,4,4,5,5,7,9 is 2.13809... (textbook example).
	sd, err := StdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(sd-2.13809) > 1e-4 {
		t.Fatalf("expected ~2.13809, got %v", sd)
	}
}

func TestStdDev_RequiresAtLeastTwo(t *testing.T) {
	if _, err := StdDev([]float64{5}); err == nil {
		t.Fatal("expected an error for a single-element sample")
	}
	if _, err := StdDev(nil); err == nil {
		t.Fatal("expected an error for an empty sample")
	}
}

func TestStdDev_IdenticalValuesIsZero(t *testing.T) {
	sd, err := StdDev([]float64{3, 3, 3, 3})
	if err != nil || sd != 0 {
		t.Fatalf("expected 0, got %v err=%v", sd, err)
	}
}

func TestMinMax(t *testing.T) {
	samples := []float64{5, 1, 9, 3, 7}
	min, err := Min(samples)
	if err != nil || min != 1 {
		t.Fatalf("expected min 1, got %v err=%v", min, err)
	}
	max, err := Max(samples)
	if err != nil || max != 9 {
		t.Fatalf("expected max 9, got %v err=%v", max, err)
	}
}

func TestMinMax_EmptySample(t *testing.T) {
	if _, err := Min(nil); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
	if _, err := Max(nil); !errors.Is(err, ErrEmptySample) {
		t.Fatalf("expected ErrEmptySample, got %v", err)
	}
}
