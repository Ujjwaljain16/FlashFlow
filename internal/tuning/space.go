// Package tuning implements Stage 8's automatic configuration search:
// a bounded, validated space over AdaptiveSelector's actual tunable
// parameters, an explicit multi-metric objective, scenario generation
// for development/holdout splitting, and the search algorithms
// (starting with Random Search) that explore the space.
//
// Every piece here is deliberately built on top of internal/proxy and
// internal/replay rather than reimplementing anything: the tuner
// evaluates a candidate AdaptiveConfig by constructing an
// AdaptiveSelector from it and running it through RunWorld, exactly the
// same call every Stage 7 experiment already makes.
package tuning

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"time"

	"flashflow/internal/proxy"
)

// ConfigSpace bounds the six parameters AdaptiveSelector's own type
// definitions expose as tunable -- proxy.AdaptiveWeights' four fields
// plus proxy.AdaptiveConfig's ReferenceLatency and StaleAfter. No other
// parameter exists to tune: AdaptiveSelector has no exploration
// probability, no hysteresis, no stabilization knob -- the master
// context's own instruction is to match the actual Stage 7
// implementation, not invent parameters to make the optimizer look more
// sophisticated.
//
// The four weights are searched as a simplex (each >= 0, summing to 1),
// not as four independent unconstrained non-negatives. This is not a
// simplification for convenience -- it follows directly from
// AdaptiveSelector.SelectTarget's own math: SelectTarget picks the
// argmax of CombinedScore across candidates for one request, and
// CombinedScore is a weighted sum of the same four per-target scores
// for every candidate. Scaling all four weights by any positive
// constant multiplies every candidate's score by that same constant,
// which cannot change which candidate is the argmax. So two weight
// vectors that differ only by an overall scale factor (e.g. {0.4, 0.4,
// 0.1, 0.1} and {0.8, 0.8, 0.2, 0.2}) produce IDENTICAL routing
// behavior -- only the ratios between weights carry any information.
// Searching the unconstrained 4D non-negative space would waste a real
// fraction of every evaluation budget on configurations that are
// behaviorally indistinguishable from ones already tried. Searching
// the 3-dimensional simplex instead searches exactly the space that
// can actually produce distinct behavior.
type ConfigSpace struct {
	// ReferenceLatencyMin/Max bound the latency (in the same units the
	// scenario's service times use) at which the latency signal is
	// "50% penalized". Below the lower bound the signal becomes
	// hypersensitive to tiny latency differences; above the upper bound
	// it becomes nearly flat across the realistic service-time range
	// this project's scenarios use (5ms-200ms, see scenario.go).
	ReferenceLatencyMin, ReferenceLatencyMax time.Duration
	// StaleAfterMin/Max bound how long a target's latency data is
	// trusted after it was last selected. Below the lower bound,
	// staleness discounting fires almost immediately after every
	// selection, permanently starving the latency signal toward neutral
	// (0.5) -- a legitimate but extreme boundary case (007-D's own
	// StaleAfter=150ms sits near this end deliberately). Above the upper
	// bound, staleness essentially never fires within the scenario
	// lengths this project uses (typically 1.5-3s of virtual time).
	StaleAfterMin, StaleAfterMax time.Duration
}

// DefaultConfigSpace bounds ReferenceLatency to [1ms, 500ms] and
// StaleAfter to [50ms, 5s] -- wide enough to include every value any
// Stage 7 experiment actually used (007's default 100ms/1s sits
// comfortably inside both ranges; 007-D/G's 150ms StaleAfter does too),
// without extending so far past realistic scenario timescales that most
// of the space is behaviorally degenerate (e.g. a 10-minute StaleAfter
// against 2-second scenarios is indistinguishable from "never stale").
func DefaultConfigSpace() ConfigSpace {
	return ConfigSpace{
		ReferenceLatencyMin: 1 * time.Millisecond,
		ReferenceLatencyMax: 500 * time.Millisecond,
		StaleAfterMin:       50 * time.Millisecond,
		StaleAfterMax:       5 * time.Second,
	}
}

// Sample draws one valid, uniformly-distributed-over-the-simplex weight
// vector (via the standard Dirichlet(1,1,1,1) construction: normalize
// four independent Exponential(1) draws, equivalent to four
// independent Uniform(0,1) draws run through -log and renormalized) and
// independently uniform ReferenceLatency/StaleAfter within their bounds.
// rng must be dedicated to sampling (the tuner's own optimizer RNG, per
// docs/learning/007-adaptive-routing-replay.md's established
// experiment/optimizer/analysis RNG separation) -- never an experiment's
// own seed, and never internal/statistics' analysis RNG.
func (cs ConfigSpace) Sample(rng *rand.Rand) proxy.AdaptiveConfig {
	// 1-Float64() rather than Float64() directly: rand.Float64() can
	// return exactly 0, and Log(0) is -Inf, which would corrupt the sum.
	raw := [4]float64{
		-math.Log(1 - rng.Float64()),
		-math.Log(1 - rng.Float64()),
		-math.Log(1 - rng.Float64()),
		-math.Log(1 - rng.Float64()),
	}
	sum := raw[0] + raw[1] + raw[2] + raw[3]

	refRange := int64(cs.ReferenceLatencyMax - cs.ReferenceLatencyMin)
	staleRange := int64(cs.StaleAfterMax - cs.StaleAfterMin)

	return proxy.AdaptiveConfig{
		Weights: proxy.AdaptiveWeights{
			Load:    raw[0] / sum,
			Latency: raw[1] / sum,
			Cache:   raw[2] / sum,
			Cost:    raw[3] / sum,
		},
		ReferenceLatency: cs.ReferenceLatencyMin + time.Duration(rng.Int63n(refRange+1)),
		StaleAfter:       cs.StaleAfterMin + time.Duration(rng.Int63n(staleRange+1)),
	}
}

// Valid reports whether cfg falls within cs's bounds and satisfies the
// weight-simplex invariant (non-negative, summing to ~1 within floating
// point tolerance). An optimizer must never be allowed to silently
// accept or silently repair a candidate outside this space -- see
// scenario.go's identical discipline for generated scenarios.
func (cs ConfigSpace) Valid(cfg proxy.AdaptiveConfig) (bool, string) {
	w := cfg.Weights
	if w.Load < 0 || w.Latency < 0 || w.Cache < 0 || w.Cost < 0 {
		return false, "weights must be non-negative"
	}
	sum := w.Load + w.Latency + w.Cache + w.Cost
	if sum < 0.999 || sum > 1.001 {
		return false, fmt.Sprintf("weights must sum to 1 (within tolerance), got %v", sum)
	}
	if cfg.ReferenceLatency < cs.ReferenceLatencyMin || cfg.ReferenceLatency > cs.ReferenceLatencyMax {
		return false, fmt.Sprintf("ReferenceLatency %v outside [%v, %v]", cfg.ReferenceLatency, cs.ReferenceLatencyMin, cs.ReferenceLatencyMax)
	}
	if cfg.StaleAfter < cs.StaleAfterMin || cfg.StaleAfter > cs.StaleAfterMax {
		return false, fmt.Sprintf("StaleAfter %v outside [%v, %v]", cfg.StaleAfter, cs.StaleAfterMin, cs.StaleAfterMax)
	}
	return true, ""
}

// Hash returns a short, stable identifier for cfg -- for search-ledger
// provenance and evaluation-result-cache keys (see cache.go), not for
// cryptographic purposes. Truncated to 16 hex characters: collision
// resistance at that length is more than sufficient for deduplicating
// within one search run's evaluation count (hundreds, not billions),
// and a shorter identifier is more usable in a printed ledger.
func Hash(cfg proxy.AdaptiveConfig) string {
	canonical := fmt.Sprintf("load=%.10f;latency=%.10f;cache=%.10f;cost=%.10f;ref=%d;stale=%d",
		cfg.Weights.Load, cfg.Weights.Latency, cfg.Weights.Cache, cfg.Weights.Cost,
		cfg.ReferenceLatency, cfg.StaleAfter)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:16]
}
