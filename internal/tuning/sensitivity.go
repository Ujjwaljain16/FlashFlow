package tuning

import (
	"fmt"
	"time"

	"flashflow/internal/proxy"
	"flashflow/internal/replay"
)

// Perturbation is one nudge applied to a config for sensitivity
// analysis, and the resulting outcome. Weight perturbations scale one
// weight by +/-10% of its own value and renormalize the whole vector
// back onto the simplex (never leaving it, since ConfigSpace treats
// off-simplex points as invalid) -- master context rule 21's own
// example ("weight +10%, weight -10%"). Duration perturbations shift
// ReferenceLatency/StaleAfter by +/-100ms, clamped to ConfigSpace's
// bounds, exactly the example rule 21 gives ("staleness +100ms,
// staleness -100ms").
type Perturbation struct {
	Parameter string               `json:"parameter"` // e.g. "Weights.Load", "ReferenceLatency"
	Direction string               `json:"direction"` // "+" or "-"
	Config    proxy.AdaptiveConfig `json:"config"`
	Utility   float64              `json:"utility"`
	Delta     float64              `json:"delta"` // Utility - baseline utility
}

// SensitivityReport is the full result of perturbing every tunable
// parameter of one winning configuration in both directions: is it a
// robust basin (small deltas everywhere) or a fragile knife-edge (a
// large delta from a small nudge) -- master context rule 21's own
// question, answered directly rather than assumed from a smooth-looking
// search curve.
type SensitivityReport struct {
	BaselineConfig  proxy.AdaptiveConfig `json:"baseline_config"`
	BaselineUtility float64              `json:"baseline_utility"`
	Perturbations   []Perturbation       `json:"perturbations"`
	MaxAbsDelta     float64              `json:"max_abs_delta"`
	MeanAbsDelta    float64              `json:"mean_abs_delta"`
}

func clampDuration(d, min, max int64) int64 {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func perturbWeight(w proxy.AdaptiveWeights, which int, factor float64) proxy.AdaptiveWeights {
	vals := [4]float64{w.Load, w.Latency, w.Cache, w.Cost}
	vals[which] *= factor
	sum := vals[0] + vals[1] + vals[2] + vals[3]
	return proxy.AdaptiveWeights{
		Load: vals[0] / sum, Latency: vals[1] / sum, Cache: vals[2] / sum, Cost: vals[3] / sum,
	}
}

// RunSensitivityAnalysis perturbs every one of cfg's six tunable
// parameters in both directions, re-evaluates against scenarios, and
// reports the utility delta from cfg's own baseline utility on the
// identical scenario set.
func RunSensitivityAnalysis(cfg proxy.AdaptiveConfig, scenarios []replay.Scenario, weights ObjectiveWeights, cs ConfigSpace) (SensitivityReport, error) {
	_, baseScores, err := Evaluate(cfg, scenarios)
	if err != nil {
		return SensitivityReport{}, fmt.Errorf("evaluating baseline: %w", err)
	}
	baseUtility := Utility(baseScores, weights)

	weightNames := []string{"Weights.Load", "Weights.Latency", "Weights.Cache", "Weights.Cost"}
	var perturbations []Perturbation

	evalAndRecord := func(param, direction string, perturbed proxy.AdaptiveConfig) error {
		_, scores, err := Evaluate(perturbed, scenarios)
		if err != nil {
			return fmt.Errorf("evaluating perturbation %s%s: %w", param, direction, err)
		}
		u := Utility(scores, weights)
		perturbations = append(perturbations, Perturbation{
			Parameter: param, Direction: direction, Config: perturbed, Utility: u, Delta: u - baseUtility,
		})
		return nil
	}

	for i, name := range weightNames {
		up := cfg
		up.Weights = perturbWeight(cfg.Weights, i, 1.1)
		if err := evalAndRecord(name, "+10%", up); err != nil {
			return SensitivityReport{}, err
		}
		down := cfg
		down.Weights = perturbWeight(cfg.Weights, i, 0.9)
		if err := evalAndRecord(name, "-10%", down); err != nil {
			return SensitivityReport{}, err
		}
	}

	const durationStep = int64(100 * time.Millisecond) // rule 21's own example

	refUpNs := clampDuration(int64(cfg.ReferenceLatency)+durationStep, int64(cs.ReferenceLatencyMin), int64(cs.ReferenceLatencyMax))
	refUp := proxy.AdaptiveConfig{Weights: cfg.Weights, ReferenceLatency: time.Duration(refUpNs), StaleAfter: cfg.StaleAfter}
	if err := evalAndRecord("ReferenceLatency", "+100ms", refUp); err != nil {
		return SensitivityReport{}, err
	}
	refDownNs := clampDuration(int64(cfg.ReferenceLatency)-durationStep, int64(cs.ReferenceLatencyMin), int64(cs.ReferenceLatencyMax))
	refDown := proxy.AdaptiveConfig{Weights: cfg.Weights, ReferenceLatency: time.Duration(refDownNs), StaleAfter: cfg.StaleAfter}
	if err := evalAndRecord("ReferenceLatency", "-100ms", refDown); err != nil {
		return SensitivityReport{}, err
	}

	staleUpNs := clampDuration(int64(cfg.StaleAfter)+durationStep, int64(cs.StaleAfterMin), int64(cs.StaleAfterMax))
	staleUp := proxy.AdaptiveConfig{Weights: cfg.Weights, ReferenceLatency: cfg.ReferenceLatency, StaleAfter: time.Duration(staleUpNs)}
	if err := evalAndRecord("StaleAfter", "+100ms", staleUp); err != nil {
		return SensitivityReport{}, err
	}
	staleDownNs := clampDuration(int64(cfg.StaleAfter)-durationStep, int64(cs.StaleAfterMin), int64(cs.StaleAfterMax))
	staleDown := proxy.AdaptiveConfig{Weights: cfg.Weights, ReferenceLatency: cfg.ReferenceLatency, StaleAfter: time.Duration(staleDownNs)}
	if err := evalAndRecord("StaleAfter", "-100ms", staleDown); err != nil {
		return SensitivityReport{}, err
	}

	var sumAbs, maxAbs float64
	for _, p := range perturbations {
		d := p.Delta
		if d < 0 {
			d = -d
		}
		sumAbs += d
		if d > maxAbs {
			maxAbs = d
		}
	}

	return SensitivityReport{
		BaselineConfig: cfg, BaselineUtility: baseUtility, Perturbations: perturbations,
		MaxAbsDelta: maxAbs, MeanAbsDelta: sumAbs / float64(len(perturbations)),
	}, nil
}
