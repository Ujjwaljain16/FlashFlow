package statistics

import "fmt"

// CliffsDelta is an effect-size measure answering a question p-values
// can't: not "is there evidence of a difference," but "how large is it,
// in a way that doesn't depend on sample size." Delta is in [-1, 1]:
// positive means A's values tend to exceed B's, negative the reverse,
// 0 means no tendency either way. Unlike a difference of means, it's a
// probability-based statistic (essentially P(A>B) - P(A<B)) that doesn't
// assume any particular distribution shape — appropriate for the same
// skewed, heavy-tailed latency data Mann-Whitney is used for.
//
// Magnitude thresholds (Romano et al. 2006, the standard reference for
// this statistic): |delta| < 0.147 negligible, < 0.33 small, < 0.474
// medium, otherwise large. These are conventions, not laws — a
// "negligible" effect can still matter in a context where it does,
// and a "large" one can be uninteresting if it's not the metric anyone
// cares about. Use CliffsDeltaMagnitude as a starting vocabulary, not a
// verdict.
type CliffsDeltaResult struct {
	Delta     float64 `json:"delta"`
	Magnitude string  `json:"magnitude"`
	NA        int     `json:"n_a"`
	NB        int     `json:"n_b"`
}

// CliffsDelta computes delta = (#{a>b} - #{a<b}) / (NA*NB) by direct
// pairwise comparison — O(NA*NB), which is deliberately the simple,
// obviously-correct implementation rather than the O(n log n) rank-based
// algorithm, since FlashFlow's sample sizes (tens to low hundreds of
// runs) make the naive approach fast enough. Revisit only if a future
// experiment needs comparisons at a scale where this becomes the
// bottleneck.
func CliffsDelta(a, b []float64) (CliffsDeltaResult, error) {
	if len(a) == 0 || len(b) == 0 {
		return CliffsDeltaResult{}, ErrEmptySample
	}
	if containsNonFinite(a) || containsNonFinite(b) {
		return CliffsDeltaResult{}, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}

	var greater, less int
	for _, x := range a {
		for _, y := range b {
			switch {
			case x > y:
				greater++
			case x < y:
				less++
			}
		}
	}

	delta := float64(greater-less) / float64(len(a)*len(b))
	return CliffsDeltaResult{
		Delta: delta, Magnitude: CliffsDeltaMagnitude(delta), NA: len(a), NB: len(b),
	}, nil
}

// CliffsDeltaMagnitude classifies |delta| using the Romano et al. (2006)
// thresholds.
func CliffsDeltaMagnitude(delta float64) string {
	abs := delta
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs < 0.147:
		return "negligible"
	case abs < 0.33:
		return "small"
	case abs < 0.474:
		return "medium"
	default:
		return "large"
	}
}
