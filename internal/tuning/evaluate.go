package tuning

import (
	"fmt"

	"flashflow/internal/proxy"
	"flashflow/internal/replay"
)

// Evaluate runs cfg (as an Adaptive candidate) against every scenario in
// scenarios -- the tuning-specific case of EvaluatePolicy, kept as its
// own function since it's what every tuning candidate, 008-A, and the
// search loop (008-B) already call by name.
func Evaluate(cfg proxy.AdaptiveConfig, scenarios []replay.Scenario) (Metrics, Scores, error) {
	return EvaluatePolicy(replay.AdaptivePolicyWithConfig(cfg), scenarios)
}

// EvaluatePolicy runs spec (any PolicySpec, not just Adaptive) against
// every scenario in scenarios via RunWorld -- a fresh selector and
// fresh trackers per scenario (RunWorld's own guarantee, see
// internal/replay/world.go), so evaluating one scenario can never leak
// state into the next. This is the one path every tuning candidate AND
// every baseline-policy comparison (008-F) goes through, so "the tuner
// cheated via state leakage" (master context rule 25) has exactly one
// place to check rather than several.
func EvaluatePolicy(spec replay.PolicySpec, scenarios []replay.Scenario) (Metrics, Scores, error) {
	results := make([]replay.WorldResult, len(scenarios))
	for i, sc := range scenarios {
		result, err := replay.RunWorld(sc, spec)
		if err != nil {
			return Metrics{}, Scores{}, fmt.Errorf("tuning: evaluating scenario seed %d: %w", sc.Seed, err)
		}
		results[i] = result
	}
	m, err := ComputeMetrics(results)
	if err != nil {
		return Metrics{}, Scores{}, err
	}
	return m, ComputeScores(m), nil
}
