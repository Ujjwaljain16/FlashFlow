package statistics

import (
	"fmt"
	"math"
	"sort"
)

// MannWhitneyResult is a two-sample rank-sum comparison.
//
// What it answers: are the two samples drawn from populations where one
// tends to produce systematically larger (or smaller) values than the
// other — a test of stochastic dominance, not of any particular
// parameter like the mean or median. A significant result means "values
// from A tend to rank higher than values from B" (or vice versa), not
// "A's mean is higher" — the two coincide often enough in practice to
// blur together, but they are not the same claim.
//
// What it does not answer: how large the difference is (see CliffsDelta
// for that), or whether it matters practically. A p-value here is
// evidence against the null hypothesis "these samples come from
// identical distributions," under this test's own assumptions — not
// proof of anything, and not a substitute for looking at the actual
// distributions.
type MannWhitneyResult struct {
	NA        int     `json:"n_a"`
	NB        int     `json:"n_b"`
	UA        float64 `json:"u_a"`
	UB        float64 `json:"u_b"`
	Z         float64 `json:"z"`
	PValue    float64 `json:"p_value"`
	TieGroups int     `json:"tie_groups"`
}

// MannWhitneyU computes the Mann-Whitney U statistic and a two-sided
// p-value via the normal approximation with tie and continuity
// correction.
//
// Assumptions and limitations, stated plainly rather than left implicit:
//   - The normal approximation is reasonable once both samples have at
//     least ~8 observations; below that, the p-value is not reliable and
//     this function does not warn about it beyond this comment — check
//     NA/NB before trusting a p-value from a small sample.
//   - This is an unpaired (independent two-sample) test. Passing two
//     samples that are actually paired observations (e.g. the same
//     request measured two ways) will produce a technically-computed but
//     conceptually wrong answer — nothing here can detect that.
//   - Ties are handled via average ranks and a standard tie-correction
//     term in the variance; heavily tied data (many identical values)
//     reduces the test's power, which is reported via TieGroups for
//     visibility, not corrected away.
func MannWhitneyU(a, b []float64) (MannWhitneyResult, error) {
	if len(a) == 0 || len(b) == 0 {
		return MannWhitneyResult{}, ErrEmptySample
	}
	if containsNonFinite(a) || containsNonFinite(b) {
		return MannWhitneyResult{}, fmt.Errorf("statistics: sample contains NaN or infinite value")
	}

	nA, nB := len(a), len(b)
	n := nA + nB

	type labeled struct {
		val   float64
		fromA bool
		rank  float64
	}
	combined := make([]labeled, 0, n)
	for _, v := range a {
		combined = append(combined, labeled{val: v, fromA: true})
	}
	for _, v := range b {
		combined = append(combined, labeled{val: v, fromA: false})
	}
	sort.Slice(combined, func(i, j int) bool { return combined[i].val < combined[j].val })

	// Assign average ranks to tied groups, and accumulate the tie
	// correction term sum(t^3 - t) over each group of size t.
	tieCorrection := 0.0
	tieGroups := 0
	i := 0
	for i < n {
		j := i
		for j < n && combined[j].val == combined[i].val {
			j++
		}
		groupSize := j - i
		// Ranks are 1-based; average of the (i+1)..(j) rank positions.
		avgRank := (float64(i+1) + float64(j)) / 2
		for k := i; k < j; k++ {
			combined[k].rank = avgRank
		}
		if groupSize > 1 {
			tieGroups++
			t := float64(groupSize)
			tieCorrection += t*t*t - t
		}
		i = j
	}

	rA := 0.0
	for _, c := range combined {
		if c.fromA {
			rA += c.rank
		}
	}

	uA := rA - float64(nA)*float64(nA+1)/2
	uB := float64(nA)*float64(nB) - uA

	meanU := float64(nA) * float64(nB) / 2
	nF := float64(n)
	varianceU := (float64(nA) * float64(nB) / 12) * ((nF + 1) - tieCorrection/(nF*(nF-1)))
	if varianceU <= 0 {
		// Every observation tied (n identical values across both
		// samples) -- there is no variability to test.
		return MannWhitneyResult{NA: nA, NB: nB, UA: uA, UB: uB, Z: 0, PValue: 1, TieGroups: tieGroups}, nil
	}
	sigmaU := math.Sqrt(varianceU)

	diff := uA - meanU
	var continuity float64
	switch {
	case diff > 0:
		continuity = -0.5
	case diff < 0:
		continuity = 0.5
	}
	z := (diff + continuity) / sigmaU
	pValue := 2 * (1 - standardNormalCDF(math.Abs(z)))
	if pValue > 1 {
		pValue = 1
	}

	return MannWhitneyResult{NA: nA, NB: nB, UA: uA, UB: uB, Z: z, PValue: pValue, TieGroups: tieGroups}, nil
}

// standardNormalCDF returns Φ(z), the standard normal CDF, via the
// standard library's erf.
func standardNormalCDF(z float64) float64 {
	return 0.5 * (1 + math.Erf(z/math.Sqrt2))
}
