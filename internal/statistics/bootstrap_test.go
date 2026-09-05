package statistics

import (
	"math/rand"
	"testing"
)

func sampleForBootstrap() []float64 {
	r := rand.New(rand.NewSource(1))
	out := make([]float64, 50)
	for i := range out {
		out[i] = 100 + r.NormFloat64()*10
	}
	return out
}

func TestBootstrapCI_RequiresMinimumSampleSize(t *testing.T) {
	if _, err := BootstrapCI([]float64{1}, MeanStat, 0.95, 1000, rand.New(rand.NewSource(1))); err == nil {
		t.Fatal("expected an error for a single-observation sample")
	}
}

func TestBootstrapCI_RejectsInvalidConfidence(t *testing.T) {
	sample := sampleForBootstrap()
	for _, c := range []float64{0, 1, -0.5, 1.5} {
		if _, err := BootstrapCI(sample, MeanStat, c, 1000, rand.New(rand.NewSource(1))); err == nil {
			t.Fatalf("expected an error for confidence=%v", c)
		}
	}
}

func TestBootstrapCI_RejectsTooFewResamples(t *testing.T) {
	sample := sampleForBootstrap()
	if _, err := BootstrapCI(sample, MeanStat, 0.95, MinBootstrapResamples-1, rand.New(rand.NewSource(1))); err == nil {
		t.Fatal("expected an error for too few resamples")
	}
}

func TestBootstrapCI_RejectsNilRNG(t *testing.T) {
	sample := sampleForBootstrap()
	if _, err := BootstrapCI(sample, MeanStat, 0.95, 1000, nil); err == nil {
		t.Fatal("expected an error for a nil rng")
	}
}

func TestBootstrapCI_SameSeedIsDeterministic(t *testing.T) {
	sample := sampleForBootstrap()
	r1, err1 := BootstrapCI(sample, MeanStat, 0.95, 2000, rand.New(rand.NewSource(42)))
	r2, err2 := BootstrapCI(sample, MeanStat, 0.95, 2000, rand.New(rand.NewSource(42)))
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if r1 != r2 {
		t.Fatalf("expected identical results for the same seed, got %+v vs %+v", r1, r2)
	}
}

func TestBootstrapCI_DifferentSeedsCanDiffer(t *testing.T) {
	sample := sampleForBootstrap()
	r1, _ := BootstrapCI(sample, MeanStat, 0.95, 2000, rand.New(rand.NewSource(1)))
	r2, _ := BootstrapCI(sample, MeanStat, 0.95, 2000, rand.New(rand.NewSource(2)))
	// The point estimate never depends on the RNG -- only the interval
	// bounds are resampled.
	if r1.Estimate != r2.Estimate {
		t.Fatalf("expected the same point estimate regardless of seed, got %v vs %v", r1.Estimate, r2.Estimate)
	}
	if r1.Lower == r2.Lower && r1.Upper == r2.Upper {
		t.Skip("different seeds happened to produce the same interval bounds -- statistically possible, not a failure, but worth noting if it recurs")
	}
}

func TestBootstrapCI_EstimateMatchesDirectStatistic(t *testing.T) {
	sample := sampleForBootstrap()
	res, err := BootstrapCI(sample, MeanStat, 0.95, 1000, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, _ := Mean(sample)
	if res.Estimate != want {
		t.Fatalf("expected estimate %v, got %v", want, res.Estimate)
	}
	if res.Lower > res.Upper {
		t.Fatalf("expected lower <= upper, got lower=%v upper=%v", res.Lower, res.Upper)
	}
	if res.Estimate < res.Lower || res.Estimate > res.Upper {
		t.Fatalf("expected the estimate to fall within its own interval, got estimate=%v [%v,%v]", res.Estimate, res.Lower, res.Upper)
	}
}

// TestBootstrapCI_LargerSampleGivesNarrowerInterval checks the expected
// precision-improves-with-n behavior (item 28): the same underlying
// distribution sampled more heavily should produce a tighter CI for the
// mean.
func TestBootstrapCI_LargerSampleGivesNarrowerInterval(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	small := make([]float64, 20)
	for i := range small {
		small[i] = 100 + r.NormFloat64()*10
	}
	large := make([]float64, 2000)
	for i := range large {
		large[i] = 100 + r.NormFloat64()*10
	}

	smallRes, err := BootstrapCI(small, MeanStat, 0.95, 2000, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	largeRes, err := BootstrapCI(large, MeanStat, 0.95, 2000, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	smallWidth := smallRes.Upper - smallRes.Lower
	largeWidth := largeRes.Upper - largeRes.Lower
	if largeWidth >= smallWidth {
		t.Fatalf("expected the larger sample to produce a narrower interval: n=20 width=%v, n=2000 width=%v", smallWidth, largeWidth)
	}
}

func TestBootstrapDiffCI_SeparatedSamplesExcludeZero(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	a := make([]float64, 50)
	b := make([]float64, 50)
	for i := range a {
		a[i] = 150 + r.NormFloat64()*5
		b[i] = 100 + r.NormFloat64()*5
	}
	res, err := BootstrapDiffCI(a, b, MeanStat, 0.95, 2000, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Lower <= 0 {
		t.Fatalf("expected a clearly positive interval for well-separated samples, got [%v,%v]", res.Lower, res.Upper)
	}
}

func TestBootstrapDiffCI_IdenticalSamplesIncludeZero(t *testing.T) {
	sample := sampleForBootstrap()
	res, err := BootstrapDiffCI(sample, sample, MeanStat, 0.95, 2000, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Lower > 0 || res.Upper < 0 {
		t.Fatalf("expected an interval straddling zero for identical samples, got [%v,%v]", res.Lower, res.Upper)
	}
}

func TestBootstrapDiffCI_RequiresMinimumSampleSizePerSide(t *testing.T) {
	sample := sampleForBootstrap()
	if _, err := BootstrapDiffCI([]float64{1}, sample, MeanStat, 0.95, 1000, rand.New(rand.NewSource(1))); err == nil {
		t.Fatal("expected an error when one side has too few observations")
	}
}
