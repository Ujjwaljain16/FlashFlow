package statistics

import (
	"fmt"
	"math/rand"
)

// Statistic is any summary function a bootstrap can resample — Mean,
// Median, or a caller-supplied closure like "p99" (via a Percentile
// partial application).
type Statistic func([]float64) float64

// MeanStat and MedianStat adapt Mean/Median (which return an error for
// empty input) to the Statistic signature. They panic on empty input
// rather than returning an error, which is safe specifically because
// every caller in this file (BootstrapCI/BootstrapDiffCI) only ever
// invokes a Statistic on a resample whose length matches an
// already-validated non-empty input — this is not a general-purpose
// substitute for Mean/Median.
func MeanStat(samples []float64) float64 {
	m, err := Mean(samples)
	if err != nil {
		panic(fmt.Sprintf("statistics: MeanStat called with invalid input: %v", err))
	}
	return m
}

func MedianStat(samples []float64) float64 {
	m, err := Median(samples)
	if err != nil {
		panic(fmt.Sprintf("statistics: MedianStat called with invalid input: %v", err))
	}
	return m
}

// PercentileStat returns a Statistic computing the p-th percentile,
// e.g. PercentileStat(99) for bootstrapping a p99 estimate. Panics under
// the same "never called on genuinely invalid input" contract as
// MeanStat/MedianStat.
func PercentileStat(p float64) Statistic {
	return func(samples []float64) float64 {
		v, err := Percentile(samples, p)
		if err != nil {
			panic(fmt.Sprintf("statistics: PercentileStat(%v) called with invalid input: %v", p, err))
		}
		return v
	}
}

// BootstrapResult is a percentile bootstrap confidence interval for one
// statistic computed on one sample.
//
// What it answers: given the observed sample, what range of values for
// this statistic would be consistent with resampling that same evidence
// — a measure of the estimate's uncertainty, not a claim about where
// some external "true" population value must lie 95% of the time (that
// common misreading of a confidence interval is wrong for any interval
// method, this one included; the correct reading is procedural: an
// interval built this way covers the true value in ~Confidence of
// hypothetical repeated experiments).
//
// What it does not answer: whether the sample itself is representative,
// or whether the statistic is well-resolved at this sample size. A
// bootstrap CI for p99 from 30 observations will look precise-sounding
// and still be a bad estimate, because p99 of 30 points is inherently a
// near-order-statistic with almost no data actually informing the tail —
// the bootstrap resamples the *sample*, it cannot manufacture missing
// information the sample never had.
type BootstrapResult struct {
	Estimate   float64 `json:"estimate"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
	Confidence float64 `json:"confidence"`
	NResamples int     `json:"n_resamples"`
	SampleSize int     `json:"sample_size"`
}

// MinBootstrapResamples is the smallest resample count this package will
// run without complaint. Fewer than this and the percentile bootstrap's
// own interval estimate becomes as noisy as the thing it's trying to
// measure — not a hard statistical law, a pragmatic floor.
const MinBootstrapResamples = 200

// BootstrapCI computes a percentile bootstrap confidence interval for
// statistic(sample), using nResamples resamples drawn with replacement
// via rng.
//
// rng must be a *rand.Rand dedicated to this analysis — never an
// experiment's own RNG. Reusing an experiment's seeded generator here
// would consume draws from it, silently changing what that experiment
// itself produces if it's re-run afterward; the whole point of a
// separate analysis seed is that running an analysis must never be able
// to alter the evidence it's analyzing.
func BootstrapCI(sample []float64, statistic Statistic, confidence float64, nResamples int, rng *rand.Rand) (BootstrapResult, error) {
	if len(sample) < 2 {
		return BootstrapResult{}, fmt.Errorf("statistics: bootstrap requires at least 2 observations, got %d", len(sample))
	}
	if containsNonFinite(sample) {
		return BootstrapResult{}, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}
	if confidence <= 0 || confidence >= 1 {
		return BootstrapResult{}, fmt.Errorf("statistics: confidence must be in (0,1), got %v", confidence)
	}
	if nResamples < MinBootstrapResamples {
		return BootstrapResult{}, fmt.Errorf("statistics: nResamples must be >= %d, got %d", MinBootstrapResamples, nResamples)
	}
	if rng == nil {
		return BootstrapResult{}, fmt.Errorf("statistics: rng must not be nil -- pass an analysis-dedicated *rand.Rand")
	}

	n := len(sample)
	estimate := statistic(sample)

	resampled := make([]float64, nResamples)
	scratch := make([]float64, n)
	for i := 0; i < nResamples; i++ {
		for j := 0; j < n; j++ {
			scratch[j] = sample[rng.Intn(n)]
		}
		resampled[i] = statistic(scratch)
	}

	alpha := (1 - confidence) / 2
	lower, err := Percentile(resampled, alpha*100)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("statistics: computing lower bound: %w", err)
	}
	upper, err := Percentile(resampled, (1-alpha)*100)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("statistics: computing upper bound: %w", err)
	}

	return BootstrapResult{
		Estimate: estimate, Lower: lower, Upper: upper,
		Confidence: confidence, NResamples: nResamples, SampleSize: n,
	}, nil
}

// BootstrapDiffCI computes a percentile bootstrap confidence interval
// for statistic(a) - statistic(b), resampling a and b independently on
// each iteration. Use this for questions like "what's the plausible
// range for the median latency difference between policy A and policy
// B" — a two-sample generalization of BootstrapCI, with the same rng and
// resample-count requirements and the same caution about tail statistics
// at small sample sizes.
func BootstrapDiffCI(a, b []float64, statistic Statistic, confidence float64, nResamples int, rng *rand.Rand) (BootstrapResult, error) {
	if len(a) < 2 || len(b) < 2 {
		return BootstrapResult{}, fmt.Errorf("statistics: bootstrap requires at least 2 observations per sample, got %d and %d", len(a), len(b))
	}
	if containsNonFinite(a) || containsNonFinite(b) {
		return BootstrapResult{}, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}
	if confidence <= 0 || confidence >= 1 {
		return BootstrapResult{}, fmt.Errorf("statistics: confidence must be in (0,1), got %v", confidence)
	}
	if nResamples < MinBootstrapResamples {
		return BootstrapResult{}, fmt.Errorf("statistics: nResamples must be >= %d, got %d", MinBootstrapResamples, nResamples)
	}
	if rng == nil {
		return BootstrapResult{}, fmt.Errorf("statistics: rng must not be nil -- pass an analysis-dedicated *rand.Rand")
	}

	nA, nB := len(a), len(b)
	estimate := statistic(a) - statistic(b)

	diffs := make([]float64, nResamples)
	scratchA := make([]float64, nA)
	scratchB := make([]float64, nB)
	for i := 0; i < nResamples; i++ {
		for j := 0; j < nA; j++ {
			scratchA[j] = a[rng.Intn(nA)]
		}
		for j := 0; j < nB; j++ {
			scratchB[j] = b[rng.Intn(nB)]
		}
		diffs[i] = statistic(scratchA) - statistic(scratchB)
	}

	alpha := (1 - confidence) / 2
	lower, err := Percentile(diffs, alpha*100)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("statistics: computing lower bound: %w", err)
	}
	upper, err := Percentile(diffs, (1-alpha)*100)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("statistics: computing upper bound: %w", err)
	}

	return BootstrapResult{
		Estimate: estimate, Lower: lower, Upper: upper,
		Confidence: confidence, NResamples: nResamples, SampleSize: nA + nB,
	}, nil
}
