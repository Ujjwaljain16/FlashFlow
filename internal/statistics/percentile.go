// Package statistics is FlashFlow's generic, domain-independent
// statistical toolkit: percentiles, rank-sum comparison, effect size, and
// bootstrap confidence intervals. Every function here takes plain
// []float64 (or two of them) and returns a plain result — no knowledge
// of requests, targets, edges, or experiments. FlashFlow-specific
// interpretation (which samples to compare, what a result means for a
// routing policy) belongs in the experiment that calls this package, not
// in it — see the package's own commentary in each file for the
// question a given function answers and, as importantly, the question it
// does not answer.
package statistics

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ErrEmptySample is returned by any function that requires at least one
// observation and received none.
var ErrEmptySample = errors.New("statistics: empty sample")

// sortedCopy returns a sorted ascending copy of samples, never mutating
// the caller's slice — every function in this package treats input
// samples as read-only evidence, not scratch space.
func sortedCopy(samples []float64) []float64 {
	out := append([]float64(nil), samples...)
	sort.Float64s(out)
	return out
}

// containsNonFinite reports whether samples has any NaN or ±Inf value.
func containsNonFinite(samples []float64) bool {
	for _, v := range samples {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return true
		}
	}
	return false
}

// Percentile returns the p-th percentile of samples (p in [0, 100]),
// using linear interpolation between closest ranks (the "R-7" / NumPy
// default method) — chosen for reasonable small-sample behavior, not to
// match internal/httpx's separate nearest-rank convention used for quick
// benchmark reporting. The two are intentionally different tools: this
// one is for statistical analysis, that one for fast operational
// summaries: do not expect them to produce byte-identical numbers on the
// same data. A third, independent nearest-rank implementation also exists
// in cmd/experiment-005h/main.go, predating this package (Stage 5 vs.
// this package's Stage 6) — frozen historical-experiment code, not a live
// call site, but a third source of "the percentile" a reader comparing
// numbers across experiments should be aware of.
//
// Percentile estimation from a small sample is inherently imprecise,
// especially in the tail (p99 from 30 observations has very few — often
// zero or one — values actually informing it). This function computes
// the number asked for; it does not, and cannot, tell the caller whether
// that number is well-resolved. Callers presenting a percentile from a
// small sample should say so.
func Percentile(samples []float64, p float64) (float64, error) {
	if len(samples) == 0 {
		return 0, ErrEmptySample
	}
	if p < 0 || p > 100 {
		return 0, fmt.Errorf("statistics: percentile p=%v out of [0,100]", p)
	}
	if containsNonFinite(samples) {
		return 0, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}

	sorted := sortedCopy(samples)
	if len(sorted) == 1 {
		return sorted[0], nil
	}

	rank := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo], nil
	}
	frac := rank - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo]), nil
}

// Median is Percentile(samples, 50).
func Median(samples []float64) (float64, error) {
	return Percentile(samples, 50)
}

// Mean returns the arithmetic mean of samples.
func Mean(samples []float64) (float64, error) {
	if len(samples) == 0 {
		return 0, ErrEmptySample
	}
	if containsNonFinite(samples) {
		return 0, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}
	sum := 0.0
	for _, v := range samples {
		sum += v
	}
	return sum / float64(len(samples)), nil
}

// StdDev returns the sample standard deviation (Bessel-corrected, n-1
// denominator). Requires at least 2 observations — standard deviation of
// a single point is undefined, not zero.
func StdDev(samples []float64) (float64, error) {
	if len(samples) < 2 {
		return 0, fmt.Errorf("statistics: StdDev requires at least 2 observations, got %d", len(samples))
	}
	if containsNonFinite(samples) {
		return 0, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}
	mean, _ := Mean(samples)
	sumSq := 0.0
	for _, v := range samples {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(samples)-1)), nil
}

// Min and Max return the smallest and largest observed values.
func Min(samples []float64) (float64, error) {
	if len(samples) == 0 {
		return 0, ErrEmptySample
	}
	m := samples[0]
	for _, v := range samples[1:] {
		if v < m {
			m = v
		}
	}
	return m, nil
}

func Max(samples []float64) (float64, error) {
	if len(samples) == 0 {
		return 0, ErrEmptySample
	}
	m := samples[0]
	for _, v := range samples[1:] {
		if v > m {
			m = v
		}
	}
	return m, nil
}
