package tuning

import (
	"math"
	"math/rand"
	"time"

	"flashflow/internal/proxy"
	"flashflow/internal/replay"
)

// TunerVersion identifies the search algorithm implementation for
// provenance (master context rule 18) -- recorded on every SearchResult
// so a later Bayesian-optimization version (if one is ever justified)
// can never be confused with a Random Search result just because both
// live in the same package.
const TunerVersion = "random-search-v1"

// Evaluation is one search-ledger entry: everything the master context
// asks to be preserved per candidate (rule 13/18) -- the configuration,
// its hash, the scenario set it was scored against, the objective
// components, runtime, and outcome. Invalid is included even though
// ConfigSpace.Sample can never itself produce an out-of-space candidate
// (ConfigSpace.Valid always holds for a sampled config -- see
// space_test.go) -- kept because Evaluate can still fail for a reason
// unrelated to the config itself (e.g. a scenario execution error), and
// "never silently skip a failed candidate, record it" (rule 12/13)
// should be true of the ledger's shape regardless of why a particular
// evaluation failed.
type Evaluation struct {
	Index           int                  `json:"index"`
	ConfigHash      string               `json:"config_hash"`
	Config          proxy.AdaptiveConfig `json:"config"`
	ScenarioSetHash string               `json:"scenario_set_hash"`
	Valid           bool                 `json:"valid"`
	InvalidReason   string               `json:"invalid_reason,omitempty"`
	Metrics         Metrics              `json:"metrics"`
	Scores          Scores               `json:"scores"`
	Utility         float64              `json:"utility"`
	RuntimeMs       float64              `json:"runtime_ms"`
	CacheHit        bool                 `json:"cache_hit"`
}

// ConvergenceSummary answers "was the search still finding improvements,
// or had it plateaued" (master context rule 20) -- computed from the
// ledger, never asserted from a fixed iteration count alone.
type ConvergenceSummary struct {
	BestSoFarUtility      []float64 `json:"best_so_far_utility"`     // one entry per valid evaluation, in submission order
	LastImprovedAtIndex   int       `json:"last_improved_at_index"`  // ledger Index of the evaluation that last raised the best-so-far value
	Plateaued             bool      `json:"plateaued"`               // true if no improvement occurred in the final quarter of valid evaluations
	PlateauWindowFraction float64   `json:"plateau_window_fraction"` // fraction of valid evaluations used as the "final stretch" for the plateau check
}

// RandomSearchConfig bundles the parameters governing one Random Search
// run. OptimizerSeed is deliberately separate from any Scenario's own
// Seed (experiment randomness) and from any *rand.Rand used later for
// statistical analysis (analysis randomness) -- the three-way RNG
// separation docs/learning/007-adaptive-routing-replay.md established
// for Stage 7's P2C policy, extended here to the search algorithm
// itself, exactly as master context rule 17 asks.
type RandomSearchConfig struct {
	Evaluations      int
	OptimizerSeed    int64
	ConfigSpace      ConfigSpace
	ObjectiveWeights ObjectiveWeights
}

// DefaultRandomSearchConfig runs 200 evaluations -- large enough to
// characterize the search space's noise and sensitivity (this
// experiment's own purpose per master context rule 13), small enough
// that even at 40 scenarios per evaluation (8000 RunWorld calls total)
// a full run completes in well under a minute against the
// virtual-time engine.
func DefaultRandomSearchConfig() RandomSearchConfig {
	return RandomSearchConfig{
		Evaluations: 200, OptimizerSeed: 20260908,
		ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights(),
	}
}

// SearchResult is the full, permanent search ledger from one Random
// Search run, plus the derived best-candidate index and convergence
// summary. Nothing here is discarded after picking a winner --
// evaluation 1 through evaluation N are all preserved (master context
// rule 19), which is what makes 008-C's sensitivity analysis and this
// stage's overfitting/generalization-gap reporting possible at all.
type SearchResult struct {
	TunerVersion    string             `json:"tuner_version"`
	OptimizerSeed   int64              `json:"optimizer_seed"`
	ScenarioSetHash string             `json:"scenario_set_hash"`
	Evaluations     []Evaluation       `json:"evaluations"`
	BestIndex       int                `json:"best_index"` // index into Evaluations, -1 if every evaluation was invalid
	Convergence     ConvergenceSummary `json:"convergence"`
}

// Best returns the highest-utility valid Evaluation, or false if every
// evaluation in the run was invalid.
func (r SearchResult) Best() (Evaluation, bool) {
	if r.BestIndex < 0 {
		return Evaluation{}, false
	}
	return r.Evaluations[r.BestIndex], true
}

type cachedEval struct {
	Metrics Metrics
	Scores  Scores
}

// evaluateWithCache returns cache[hash]'s Metrics/Scores if present
// (hit=true, Evaluate never called), otherwise calls Evaluate and, on
// success, stores the result under hash for future calls to reuse.
// Factored out from RunRandomSearch's loop specifically so the caching
// behavior itself -- not just the search's overall determinism -- has
// a direct, isolated test (see search_test.go).
func evaluateWithCache(cache map[string]cachedEval, hash string, cfg proxy.AdaptiveConfig, scenarios []replay.Scenario) (Metrics, Scores, bool, error) {
	if cached, ok := cache[hash]; ok {
		return cached.Metrics, cached.Scores, true, nil
	}
	m, s, err := Evaluate(cfg, scenarios)
	if err != nil {
		return Metrics{}, Scores{}, false, err
	}
	cache[hash] = cachedEval{Metrics: m, Scores: s}
	return m, s, false, nil
}

// evaluateCandidate gates evaluateWithCache behind cs.Valid: a candidate
// outside the space is rejected -- recorded as invalid, never scored --
// before ever calling Evaluate. Factored out from RunRandomSearch's loop
// specifically so this gate has a direct, isolated test independent of
// whether ConfigSpace.Sample can itself produce an out-of-space value
// (search_test.go's own regression test constructs one by hand instead).
func evaluateCandidate(cs ConfigSpace, cache map[string]cachedEval, hash string, cfg proxy.AdaptiveConfig, scenarios []replay.Scenario) (m Metrics, s Scores, cacheHit bool, valid bool, invalidReason string) {
	if ok, reason := cs.Valid(cfg); !ok {
		return Metrics{}, Scores{}, false, false, "candidate outside ConfigSpace: " + reason
	}
	var err error
	m, s, cacheHit, err = evaluateWithCache(cache, hash, cfg, scenarios)
	if err != nil {
		return Metrics{}, Scores{}, cacheHit, false, err.Error()
	}
	return m, s, cacheHit, true, ""
}

// RunRandomSearch draws rsc.Evaluations candidates from rsc.ConfigSpace
// using an RNG seeded from rsc.OptimizerSeed alone, evaluates each
// against scenarios (fixed for the whole run -- every candidate sees
// the identical Development set, never Holdout, per the sacred split),
// and returns the complete search ledger.
//
// An in-memory cache keyed by config hash avoids re-running Evaluate
// for a configuration this run has already scored -- safe specifically
// because Evaluate is deterministic for a fixed (config, scenario set)
// pair (008-A's Prediction 7), and scoped to this run's scenario set
// alone rather than persisted across runs, so it can never accidentally
// serve a cached result computed against a different scenario set
// (master context rule 27/28's "never confuse cached analysis with a
// new experiment").
func RunRandomSearch(rsc RandomSearchConfig, scenarios []replay.Scenario) SearchResult {
	optimizerRNG := rand.New(rand.NewSource(rsc.OptimizerSeed))
	setHash := ScenarioSetHash(scenarios)
	cache := make(map[string]cachedEval)

	evaluations := make([]Evaluation, 0, rsc.Evaluations)
	bestIndex := -1
	bestUtility := math.Inf(-1)

	for i := 0; i < rsc.Evaluations; i++ {
		cfg := rsc.ConfigSpace.Sample(optimizerRNG)
		hash := Hash(cfg)
		start := time.Now()

		// A sampler is expected to always produce an in-space candidate
		// (ConfigSpace.Sample does today, per space_test.go), but nothing
		// enforced that boundary at the evaluation site itself -- a future
		// sampling strategy (a mutation/crossover step, a hand-edited
		// re-scored config) could silently produce an out-of-simplex or
		// negative-duration candidate and have it scored and potentially
		// ranked as a winner anyway. evaluateCandidate rejects before ever
		// calling Evaluate.
		m, s, cacheHit, valid, invalidReason := evaluateCandidate(rsc.ConfigSpace, cache, hash, cfg, scenarios)

		eval := Evaluation{
			Index: i, ConfigHash: hash, Config: cfg, ScenarioSetHash: setHash,
			Valid: valid, InvalidReason: invalidReason,
			Metrics: m, Scores: s, RuntimeMs: float64(time.Since(start).Microseconds()) / 1000.0,
			CacheHit: cacheHit,
		}
		if valid {
			eval.Utility = Utility(s, rsc.ObjectiveWeights)
		}
		evaluations = append(evaluations, eval)

		if valid && eval.Utility > bestUtility {
			bestUtility = eval.Utility
			bestIndex = len(evaluations) - 1
		}
	}

	return SearchResult{
		TunerVersion: TunerVersion, OptimizerSeed: rsc.OptimizerSeed, ScenarioSetHash: setHash,
		Evaluations: evaluations, BestIndex: bestIndex,
		Convergence: computeConvergence(evaluations),
	}
}

// computeConvergence builds the best-so-far curve over valid
// evaluations only (an invalid evaluation contributes no new
// information about the search's progress) and checks whether the
// final quarter of the run improved on the best found before it -- a
// direct, computed answer to "did the search plateau," never assumed
// from the evaluation count alone (master context rule 20).
func computeConvergence(evaluations []Evaluation) ConvergenceSummary {
	var bestSoFar []float64
	var lastImprovedAt int
	best := math.Inf(-1)

	for _, e := range evaluations {
		if !e.Valid {
			continue
		}
		if e.Utility > best {
			best = e.Utility
			lastImprovedAt = e.Index
		}
		bestSoFar = append(bestSoFar, best)
	}

	const plateauWindowFraction = 0.25
	plateaued := false
	if n := len(bestSoFar); n > 0 {
		windowStart := int(float64(n) * (1 - plateauWindowFraction))
		// The baseline is the best-so-far value immediately before the
		// final-quarter window starts; with too few evaluations to have
		// a "before" at all (windowStart==0), there's no basis to call
		// it plateaued, so the comparison is against -Inf (never equal
		// to a real utility), correctly defaulting to false.
		valueBeforeWindow := math.Inf(-1)
		if windowStart > 0 {
			valueBeforeWindow = bestSoFar[windowStart-1]
		}
		plateaued = bestSoFar[n-1] == valueBeforeWindow
	}

	return ConvergenceSummary{
		BestSoFarUtility: bestSoFar, LastImprovedAtIndex: lastImprovedAt,
		Plateaued: plateaued, PlateauWindowFraction: plateauWindowFraction,
	}
}
