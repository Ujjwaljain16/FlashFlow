package tuning

import (
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
)

// PerScenarioUtility evaluates cfg (as an Adaptive candidate) against
// each scenario individually -- the tuning-specific case of
// PerScenarioUtilityForPolicy, kept as its own function for 008-C/008-D's
// existing call sites.
func PerScenarioUtility(cfg proxy.AdaptiveConfig, scenarios []replay.Scenario, weights ObjectiveWeights) ([]float64, error) {
	return PerScenarioUtilityForPolicy(replay.AdaptivePolicyWithConfig(cfg), scenarios, weights)
}

// PerScenarioUtilityForPolicy evaluates spec (any PolicySpec) against
// each scenario in scenarios INDIVIDUALLY (never pooled), returning one
// utility value per scenario in the same order. Evaluate/ComputeMetrics
// pool every scenario's completions into one aggregate distribution --
// appropriate for a single scalar to rank search candidates by, but
// exactly the wrong shape for master context rule 22's demand to look
// at mean, median, worst-case, and variance ACROSS scenarios, which
// requires one utility number per scenario, not one number for the
// whole set.
func PerScenarioUtilityForPolicy(spec replay.PolicySpec, scenarios []replay.Scenario, weights ObjectiveWeights) ([]float64, error) {
	utilities := make([]float64, len(scenarios))
	for i, sc := range scenarios {
		result, err := replay.RunWorld(sc, spec)
		if err != nil {
			return nil, err
		}
		m, err := ComputeMetrics([]replay.WorldResult{result})
		if err != nil {
			return nil, err
		}
		utilities[i] = Utility(ComputeScores(m), weights)
	}
	return utilities, nil
}

// RobustnessSummary reports a configuration's utility distribution
// across a scenario set -- mean, median, worst-case, and standard
// deviation, per master context rule 22's explicit instruction not to
// judge a configuration by its average alone. A configuration with a
// slightly worse mean but a dramatically better worst-case may be
// preferable; this type makes that comparison possible instead of
// collapsing it into one number before anyone gets to see it.
type RobustnessSummary struct {
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	Worst  float64 `json:"worst"` // minimum utility across the scenario set -- the worst single scenario, not an aggregate
	StdDev float64 `json:"std_dev"`
}

// ComputeRobustness summarizes utilities (as produced by
// PerScenarioUtility) into a RobustnessSummary.
func ComputeRobustness(utilities []float64) (RobustnessSummary, error) {
	mean, err := statistics.Mean(utilities)
	if err != nil {
		return RobustnessSummary{}, err
	}
	median, err := statistics.Median(utilities)
	if err != nil {
		return RobustnessSummary{}, err
	}
	stddev, err := statistics.StdDev(utilities)
	if err != nil {
		return RobustnessSummary{}, err
	}
	worst := utilities[0]
	for _, u := range utilities[1:] {
		if u < worst {
			worst = u
		}
	}
	return RobustnessSummary{Mean: mean, Median: median, Worst: worst, StdDev: stddev}, nil
}
