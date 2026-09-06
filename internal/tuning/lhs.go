package tuning

import (
	"math"
	"math/rand"
	"time"

	"flashflow/internal/proxy"
)

// LHSTuner draws its ENTIRE design up front, at construction time, from
// a Latin Hypercube over the 6 underlying uniform draws
// ConfigSpace.Sample's own Dirichlet-simplex construction is built
// from (see configFromUnitCube) -- a Latin Hypercube's defining
// property (each of N per-dimension strata used exactly once across N
// samples) is a property of a FIXED-SIZE design, not something that
// can be built incrementally one Suggest call at a time without
// knowing the total budget in advance. This is why NewLHSTuner takes
// evaluations as a constructor argument, unlike RandomSearchTuner and
// BayesOptTuner, which need no such upfront commitment.
type LHSTuner struct {
	rng    *rand.Rand
	space  ConfigSpace
	seed   int64
	design []proxy.AdaptiveConfig
	next   int
}

// NewLHSTuner builds an evaluations-point Latin Hypercube design over
// space, seeded from seed.
func NewLHSTuner(seed int64, space ConfigSpace, evaluations int) *LHSTuner {
	rng := rand.New(rand.NewSource(seed))
	return &LHSTuner{
		rng:    rng,
		space:  space,
		seed:   seed,
		design: buildLHSDesign(rng, space, evaluations),
	}
}

// Suggest returns the next unused design point. previous is ignored --
// LHS's design is fixed at construction time and does not adapt to
// results, the same as Random Search's own previous-ignoring Suggest,
// just with a structured (not independent) sampling pattern. If more
// evaluations are requested than the design was built for (a caller
// mismatching NewLHSTuner's evaluations argument and RunSearch's own
// evaluations argument), Suggest falls back to plain uniform sampling
// for the excess calls rather than panicking or repeating a design
// point -- a budget mismatch should degrade gracefully, not corrupt the
// run.
func (t *LHSTuner) Suggest(previous []TrialResult) proxy.AdaptiveConfig {
	if t.next < len(t.design) {
		cfg := t.design[t.next]
		t.next++
		return cfg
	}
	return t.space.Sample(t.rng)
}

func (t *LHSTuner) Space() ConfigSpace { return t.space }
func (t *LHSTuner) Seed() int64        { return t.seed }
func (t *LHSTuner) Name() string       { return "lhs-v1" }

// buildLHSDesign constructs an n-point Latin Hypercube over 6
// dimensions (4 feeding the Dirichlet-simplex weight construction, 1
// for ReferenceLatency, 1 for StaleAfter): each dimension's [0,1) range
// is divided into n equal strata, one uniform draw is taken within each
// stratum, and each dimension's n stratum assignments are
// INDEPENDENTLY permuted across the n design points -- the permutation
// is what makes this a genuine Latin hypercube (every stratum used
// exactly once per dimension, paired with an independently-chosen
// stratum from every other dimension) rather than n points running
// diagonally through the unit cube.
func buildLHSDesign(rng *rand.Rand, space ConfigSpace, n int) []proxy.AdaptiveConfig {
	if n <= 0 {
		return nil
	}
	const dims = 6
	perms := make([][]int, dims)
	for d := 0; d < dims; d++ {
		perms[d] = rng.Perm(n)
	}

	design := make([]proxy.AdaptiveConfig, n)
	for i := 0; i < n; i++ {
		var u [dims]float64
		for d := 0; d < dims; d++ {
			stratum := perms[d][i]
			u[d] = (float64(stratum) + rng.Float64()) / float64(n)
		}
		design[i] = configFromUnitCube(space, u)
	}
	return design
}

// configFromUnitCube maps 6 values in [0,1) to a proxy.AdaptiveConfig,
// deliberately mirroring ConfigSpace.Sample's own construction (the
// -log(1-u)/sum Dirichlet-simplex transform for the four weights,
// linear interpolation for the two durations) rather than sharing code
// with it: Sample's own behavior must stay byte-for-byte unchanged for
// Random Search's existing determinism guarantees (see search.go's own
// doc comment on RunRandomSearch), so this is a deliberate, small,
// documented duplication rather than a refactor that risks Sample's
// established reproducibility. The one difference from Sample: the two
// duration dimensions are mapped via continuous linear interpolation
// here (u*range) rather than Sample's discrete rng.Int63n(range+1) --
// harmless at nanosecond resolution, and necessary since LHS's whole
// point is stratifying a CONTINUOUS [0,1) range, not an integer one.
func configFromUnitCube(space ConfigSpace, u [6]float64) proxy.AdaptiveConfig {
	raw := [4]float64{
		-math.Log(1 - u[0]),
		-math.Log(1 - u[1]),
		-math.Log(1 - u[2]),
		-math.Log(1 - u[3]),
	}
	sum := raw[0] + raw[1] + raw[2] + raw[3]

	refRange := float64(space.ReferenceLatencyMax - space.ReferenceLatencyMin)
	staleRange := float64(space.StaleAfterMax - space.StaleAfterMin)

	return proxy.AdaptiveConfig{
		Weights: proxy.AdaptiveWeights{
			Load:    raw[0] / sum,
			Latency: raw[1] / sum,
			Cache:   raw[2] / sum,
			Cost:    raw[3] / sum,
		},
		ReferenceLatency: space.ReferenceLatencyMin + time.Duration(u[4]*refRange),
		StaleAfter:       space.StaleAfterMin + time.Duration(u[5]*staleRange),
	}
}
